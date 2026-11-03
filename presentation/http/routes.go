// Package httptransport wires HTTP routes and their access policies.
package httptransport

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"websiteengine/features/auth"
	"websiteengine/pkg/environment"
	"websiteengine/pkg/response"
	publicruntime "websiteengine/pkg/runtime"
	"websiteengine/pkg/static"
	hauth "websiteengine/presentation/http/handlers/auth"
	hcontent "websiteengine/presentation/http/handlers/content"
	hfeatures "websiteengine/presentation/http/handlers/features"
	hforms "websiteengine/presentation/http/handlers/forms"
	hmedia "websiteengine/presentation/http/handlers/media"
	hoverview "websiteengine/presentation/http/handlers/overview"
	hpublication "websiteengine/presentation/http/handlers/publication"
	hreleases "websiteengine/presentation/http/handlers/releases"
	hsettings "websiteengine/presentation/http/handlers/settings"
	hsitebuilder "websiteengine/presentation/http/handlers/sitebuilder"
	"websiteengine/presentation/http/middleware"
)

type Dependencies struct {
	DB interface {
		QueryRow(context.Context, string, ...interface{}) interface{ Scan(...interface{}) error }
	}
	Log               *slog.Logger
	AuthService       *auth.Service
	Auth              *hauth.Handler
	Features          *hfeatures.Handler
	Content           *hcontent.Handler
	Forms             *hforms.Handler
	Media             *hmedia.Handler
	Overview          *hoverview.Handler
	Releases          *hreleases.Handler
	PublicationEvents *hpublication.Handler
	Settings          *hsettings.Handler
	FeatureBuilds     *hsitebuilder.BuildsHandler
	SiteSnapshots     *hsitebuilder.SnapshotsHandler
	CorsResolver      middleware.CorsResolver
	DevPublicBaseURL  string
	ProdPublicBaseURL string
	Health            func(ctx context.Context) error
	MaxJSONBytes      int64
	FormRateLimit     int
	FormRateWindow    time.Duration
	TrustProxy        bool
	PublicCORSOrigins []string
	PublicCORSByEnv   map[environment.Environment][]string
	S3PublicBaseURL   string
}

func NewRouter(d Deps) http.Handler {
	if d.FormRateWindow <= 0 {
		d.FormRateWindow = time.Minute
	}
	mux := http.NewServeMux()
	authMW := middleware.Authenticate(d.AuthService, d.Log)
	user := func(h http.HandlerFunc) http.Handler {
		return authMW(middleware.RequireUser(http.HandlerFunc(h)))
	}
	siteManager := func(h http.HandlerFunc) http.Handler {
		return authMW(middleware.RequireSiteManager(http.HandlerFunc(h)))
	}
	admin := func(h http.HandlerFunc) http.Handler {
		return authMW(middleware.RequireAdmin(http.HandlerFunc(h)))
	}
	jsonLimit := middleware.BodyLimit(d.MaxJSONBytes)
	jsonUser := func(h http.HandlerFunc) http.Handler {
		return jsonLimit(user(h))
	}
	jsonSiteManager := func(h http.HandlerFunc) http.Handler {
		return jsonLimit(siteManager(h))
	}

	// Service health.
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if d.Health != nil {
			if err := d.Health(ctx); err != nil {
				d.Log.Warn("readiness check failed", "request_id", response.RequestID(r.Context()), "error", err)
				response.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
				return
			}
		}
		response.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	// Current token and token management.
	mux.Handle("GET /api/v1/admin/me", authMW(http.HandlerFunc(d.Auth.Me)))
	mux.Handle("GET /api/v1/admin/tokens", siteManager(d.Auth.ListTokens))
	mux.Handle("POST /api/v1/admin/tokens", jsonSiteManager(d.Auth.CreateToken))
	mux.Handle("GET /api/v1/admin/tokens/{id}", siteManager(d.Auth.GetToken))
	mux.Handle("DELETE /api/v1/admin/tokens/{id}", siteManager(d.Auth.RevokeToken))

	// Sanitized runtime configuration for the admin settings section.
	if d.Settings != nil {
		mux.Handle("GET /api/v1/admin/settings", siteManager(http.HandlerFunc(d.Settings.Get)))
	}

	// Content publication audit feed.
	if d.PublicationEvents != nil {
		mux.Handle("GET /api/v1/admin/publication-events", siteManager(d.PublicationEvents.List))
	}

	// Operational summary for the admin dashboard.
	mux.Handle("GET /api/v1/admin/overview", user(d.Overview.Get))

	// Feature metadata and schema publication.
	if d.FeatureBuilds != nil {
		mux.Handle("GET /api/v2/admin/feature-builds", siteManager(d.FeatureBuilds.List))
		mux.Handle("POST /api/v2/admin/feature-builds", siteManager(d.FeatureBuilds.Import))
	}
	if d.SiteSnapshots != nil {
		mux.Handle("GET /api/v2/admin/content/features", user(d.SiteSnapshots.ContentCatalog))
		mux.Handle("GET /api/v2/admin/content/snapshots", user(d.SiteSnapshots.ContentSnapshots))
		mux.Handle("GET /api/v2/admin/content/snapshots/{id}/features", user(d.SiteSnapshots.SnapshotContentCatalog))
		mux.Handle("GET /api/v2/admin/content/snapshots/{id}/features/{featureKey}", user(d.SiteSnapshots.ContentWorkspace))
		mux.Handle("PUT /api/v2/admin/content/snapshots/{id}/features/{featureKey}", jsonUser(d.SiteSnapshots.SaveContentWorkspace))
		mux.Handle("POST /api/v2/admin/content/snapshots/{id}/fork", jsonSiteManager(d.SiteSnapshots.ForkSnapshotFromContent))
		mux.Handle("GET /api/v2/admin/site/draft", siteManager(d.SiteSnapshots.Draft))
		mux.Handle("PUT /api/v2/admin/site/draft", jsonSiteManager(d.SiteSnapshots.SaveDraft))
		mux.Handle("GET /api/v2/admin/site/metadata", user(d.SiteSnapshots.Metadata))
		mux.Handle("PUT /api/v2/admin/site/metadata", jsonUser(d.SiteSnapshots.SaveMetadata))
		mux.Handle("POST /api/v2/admin/site/metadata/publish", user(d.SiteSnapshots.PublishMetadata))
		mux.Handle("POST /api/v2/admin/site/metadata/promote", jsonSiteManager(d.SiteSnapshots.PromoteMetadata))
		mux.Handle("GET /api/v2/admin/site/design-system", siteManager(d.SiteSnapshots.DesignSystem))
		mux.Handle("PUT /api/v2/admin/site/design-system", jsonSiteManager(d.SiteSnapshots.SaveDesignSystem))
		mux.Handle("POST /api/v2/admin/site/design-system/preview", jsonSiteManager(d.SiteSnapshots.PreviewDesignSystem))
		mux.Handle("GET /api/v2/admin/site/snapshots", siteManager(d.SiteSnapshots.List))
		mux.Handle("GET /api/v2/admin/site/snapshot-events", siteManager(d.SiteSnapshots.Events))
		mux.Handle("POST /api/v2/admin/site/snapshots", jsonSiteManager(d.SiteSnapshots.Create))
		mux.Handle("POST /api/v2/admin/site/snapshots/{id}/deploy", jsonSiteManager(d.SiteSnapshots.Deploy))
		mux.Handle("POST /api/v2/admin/site/snapshots/{id}/promote", jsonSiteManager(d.SiteSnapshots.Promote))
		mux.Handle("POST /api/v2/admin/site/snapshots/{id}/rollback", jsonSiteManager(d.SiteSnapshots.Rollback))
	}
	// Content users may read feature metadata and schemas needed to edit content
	// and interpret existing form submissions. Site mutations remain restricted.
	mux.Handle("GET /api/v1/admin/features", user(d.Features.List))
	mux.Handle("POST /api/v1/admin/features", jsonSiteManager(d.Features.Create))
	mux.Handle("GET /api/v1/admin/features/{id}", user(d.Features.Get))
	mux.Handle("PATCH /api/v1/admin/features/{id}", jsonSiteManager(d.Features.Update))
	mux.Handle("POST /api/v1/admin/features/{id}/archive", siteManager(d.Features.Archive))
	mux.Handle("GET /api/v1/admin/features/{id}/versions", user(d.Features.Versions))
	mux.Handle("GET /api/v1/admin/features/{id}/draft", user(d.Features.Draft))
	mux.Handle("PUT /api/v1/admin/features/{id}/draft", jsonSiteManager(d.Features.SaveDraft))
	mux.Handle("POST /api/v1/admin/features/{id}/publish", jsonSiteManager(d.Features.Publish))
	mux.Handle("POST /api/v1/admin/features/{id}/promote", jsonSiteManager(d.Features.Promote))
	mux.Handle("POST /api/v1/admin/features/{id}/rollback", jsonSiteManager(d.Features.Rollback))

	// Content entries.
	mux.Handle("GET /api/v2/admin/features/{id}/settings", user(d.Content.GetSettings))
	mux.Handle("POST /api/v2/admin/features/{id}/settings", jsonUser(d.Content.CreateSettings))
	mux.Handle("PUT /api/v2/admin/features/{id}/entries/{entryId}/move", jsonUser(d.Content.Move))
	mux.Handle("PUT /api/v2/admin/features/{id}/entries/order", jsonUser(d.Content.Reorder))
	mux.Handle("GET /api/v1/admin/features/{id}/entries", user(d.Content.ListEntries))
	mux.Handle("POST /api/v1/admin/features/{id}/entries", jsonUser(d.Content.Create))
	mux.Handle("POST /api/v2/admin/features/{id}/entries/import", jsonUser(d.Content.Import))
	mux.Handle("GET /api/v1/admin/entries/{id}", user(d.Content.Get))
	mux.Handle("PATCH /api/v1/admin/entries/{id}/draft", jsonUser(d.Content.SaveDraft))
	mux.Handle("POST /api/v1/admin/entries/{id}/publish", jsonUser(d.Content.Publish))
	mux.Handle("POST /api/v1/admin/entries/{id}/promote", jsonSiteManager(d.Content.Promote))
	mux.Handle("POST /api/v1/admin/entries/{id}/rollback", jsonUser(d.Content.Rollback))
	mux.Handle("POST /api/v1/admin/entries/{id}/unpublish", jsonUser(d.Content.Unpublish))
	mux.Handle("POST /api/v1/admin/entries/{id}/discard-draft", user(d.Content.DiscardDraft))
	mux.Handle("POST /api/v1/admin/entries/{id}/archive", user(d.Content.Archive))
	mux.Handle("GET /api/v1/admin/entries/{id}/versions", user(d.Content.Versions))

	// Site releases and environments.
	if d.Releases != nil {
		mux.Handle("GET /api/v1/admin/releases", user(d.Releases.List))
		mux.Handle("POST /api/v1/admin/releases", siteManager(d.Releases.Import))
		mux.Handle("POST /api/v1/admin/releases/{id}/activate", jsonSiteManager(d.Releases.Activate))
		mux.Handle("POST /api/v1/admin/releases/{id}/rollback", jsonSiteManager(d.Releases.Rollback))
		mux.Handle("GET /api/v1/admin/site/environments", siteManager(d.Releases.Environments))
		mux.Handle("PATCH /api/v1/admin/site/environments/{key}", jsonSiteManager(d.Releases.UpdateEnvironment))
		mux.Handle("GET /api/v1/admin/release-sources", siteManager(d.Releases.Sources))
		mux.Handle("POST /api/v1/admin/release-sources", jsonSiteManager(d.Releases.CreateSource))
		mux.Handle("DELETE /api/v1/admin/release-sources/{id}", siteManager(d.Releases.DeleteSource))
		mux.Handle("GET /api/v1/admin/release-sources/{id}/releases", siteManager(d.Releases.ListReleasesForSource))
		mux.Handle("POST /api/v1/admin/release-sources/{id}/sync", jsonSiteManager(d.Releases.SyncSource))
	}

	// Form submissions.
	mux.Handle("GET /api/v1/admin/forms/{key}/submissions", user(d.Forms.List))
	mux.Handle("GET /api/v1/admin/forms/{key}/submissions/export", user(d.Forms.Export))
	mux.Handle("GET /api/v1/admin/submissions/{id}", user(d.Forms.Get))
	mux.Handle("PATCH /api/v1/admin/submissions/{id}/status", jsonUser(d.Forms.UpdateStatus))
	mux.Handle("POST /api/v1/admin/submissions/{id}/archive", user(d.Forms.Archive))
	mux.Handle("DELETE /api/v1/admin/submissions/{id}", admin(d.Forms.Delete))

	// Media administration and public redirect.
	mux.Handle("POST /api/v1/admin/media", user(d.Media.Upload))
	mux.Handle("POST /api/v2/admin/media/{id}/renditions/prepare", jsonUser(d.Media.PrepareRendition))
	mux.Handle("GET /api/v2/admin/media/{id}/renditions/state", user(d.Media.RenditionState))
	mux.Handle("GET /api/v1/admin/media", user(d.Media.List))
	mux.Handle("GET /api/v1/admin/media/{id}", user(d.Media.Get))
	mux.Handle("PATCH /api/v1/admin/media/{id}", jsonUser(d.Media.Update))
	mux.Handle("GET /api/v1/admin/media/{id}/usage", user(d.Media.Usage))
	mux.Handle("POST /api/v1/admin/media/{id}/retry", user(d.Media.Retry))
	mux.Handle("DELETE /api/v1/admin/media/{id}", admin(d.Media.Delete))

	// Public content and forms. CORS is opt-in per environment; the legacy
	// PUBLIC_CORS_ORIGINS value applies to both environments when set.
	corsByEnv := d.PublicCORSByEnv
	if corsByEnv == nil {
		corsByEnv = map[environment.Environment][]string{
			environment.Dev:  d.PublicCORSOrigins,
			environment.Prod: d.PublicCORSOrigins,
		}
	}
	cors := middleware.CORSFor(corsByEnv)
	if d.CorsResolver != nil {
		cors = middleware.DynamicCORS(d.CorsResolver, corsByEnv)
	}
	mux.Handle("GET /media/{key}", cors(http.HandlerFunc(d.Media.Redirect)))
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, pattern := range []string{
		"OPTIONS /api/v1/content",
		"OPTIONS /api/v1/content/{key}",
		"OPTIONS /api/v1/content/{key}/entries",
		"OPTIONS /api/v1/forms/{key}/submissions",
	} {
		mux.Handle(pattern, cors(noop))
	}
	mux.Handle("GET /api/v1/content", cors(http.HandlerFunc(d.Content.PublicContent)))
	mux.Handle("GET /api/v1/content/{key}", cors(http.HandlerFunc(d.Content.PublicFeature)))
	mux.Handle("GET /api/v1/content/{key}/entries", cors(http.HandlerFunc(d.Content.PublicEntries)))
	formLimiter := middleware.NewRateLimiter(d.FormRateLimit, d.FormRateWindow, d.TrustProxy)
	mux.Handle("POST /api/v1/forms/{key}/submissions",
		cors(jsonLimit(formLimiter.Middleware(http.HandlerFunc(d.Forms.Submit)))))

	// Admin SPA.
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.Handle("GET /admin/", http.StripPrefix("/admin", static.Handler()))

	// Consistent fallbacks for the API and the root URL.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || len(r.URL.Path) > 5 && r.URL.Path[:5] == "/api/" {
			response.Error(w, r, http.StatusNotFound, "not_found", "Маршрут API не найден")
			return
		}
		response.Error(w, r, http.StatusNotFound, "not_found", "Страница не найдена")
	})

	// Order matters: outermost middleware runs first.
	handler := middleware.RequestID(mux)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(d.Log)(handler)
	handler = middleware.Logger(d.Log)(handler)
	return handler
}

// NewPublicRouter serves a single site environment. The handler tags itself
// with that environment, so content and CORS resolve to the right listener.
func NewPublicRouter(d Deps, env environment.Environment) http.Handler {
	if d.FormRateWindow <= 0 {
		d.FormRateWindow = time.Minute
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if d.Health != nil {
			if err := d.Health(ctx); err != nil {
				response.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
				return
			}
		}
		response.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	if d.FeatureBuilds != nil {
		mux.HandleFunc("GET /feature-assets/{digest}/{asset...}", d.FeatureBuilds.Asset)
	}

	corsByEnv := d.PublicCORSByEnv
	if corsByEnv == nil {
		corsByEnv = map[environment.Environment][]string{
			environment.Dev:  d.PublicCORSOrigins,
			environment.Prod: d.PublicCORSOrigins,
		}
	}
	cors := middleware.CORSFor(corsByEnv)
	if d.CorsResolver != nil {
		cors = middleware.DynamicCORS(d.CorsResolver, corsByEnv)
	}
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, pattern := range []string{
		"OPTIONS /api/v1/content",
		"OPTIONS /api/v1/content/{key}",
		"OPTIONS /api/v1/content/{key}/entries",
		"OPTIONS /api/v1/forms/{key}/submissions",
		"OPTIONS /api/v2/runtime",
		"OPTIONS /api/v2/site-themes/snapshots/{id}",
		"OPTIONS /api/v2/site-themes/previews/{token}",
		"OPTIONS /api/v2/system-assets/{id}",
		"OPTIONS /api/v2/content/{key}/records",
		"OPTIONS /api/v2/forms/{key}/submissions",
	} {
		mux.Handle(pattern, cors(noop))
	}
	// The v1 CMS endpoints remain direct content/form APIs for existing
	// integrations. The v2 runtime resolves page composition from the active
	// snapshot and editorial data from independent environment content pointers.
	mux.Handle("GET /api/v1/content", cors(http.HandlerFunc(d.Content.PublicContent)))
	mux.Handle("GET /api/v1/content/{key}", cors(http.HandlerFunc(d.Content.PublicFeature)))
	mux.Handle("GET /api/v1/content/{key}/entries", cors(http.HandlerFunc(d.Content.PublicEntries)))
	formLimiter := middleware.NewRateLimiter(d.FormRateLimit, d.FormRateWindow, d.TrustProxy)
	mux.Handle("POST /api/v1/forms/{key}/submissions",
		cors(middleware.BodyLimit(d.MaxJSONBytes)(formLimiter.Middleware(http.HandlerFunc(d.Forms.Submit)))))
	if d.SiteSnapshots != nil {
		mux.Handle("GET /api/v2/runtime", cors(http.HandlerFunc(d.SiteSnapshots.Runtime)))
		mux.Handle("GET /api/v2/site-themes/snapshots/{id}", cors(http.HandlerFunc(d.SiteSnapshots.SnapshotThemeCSS)))
		mux.Handle("GET /api/v2/site-themes/previews/{token}", cors(http.HandlerFunc(d.SiteSnapshots.PreviewThemeCSS)))
		mux.Handle("GET /api/v2/system-assets/{id}", cors(http.HandlerFunc(d.Media.SystemAsset)))
		mux.Handle("GET /api/v2/content/{key}/records", cors(http.HandlerFunc(d.SiteSnapshots.Collection)))
		mux.Handle("POST /api/v2/forms/{key}/submissions", cors(middleware.BodyLimit(d.MaxJSONBytes)(formLimiter.Middleware(http.HandlerFunc(d.SiteSnapshots.SubmitForm)))))
	}
	mux.Handle("GET /media/{key}", cors(http.HandlerFunc(d.Media.Redirect)))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, http.StatusNotFound, "not_found", "Маршрут API не найден")
	})
	// The engine always serves its embedded runtime shell. Published snapshots
	// supply the composition and feature assets; the old full-site release
	// handler is intentionally not part of this runtime path.
	if d.SiteSnapshots != nil {
		resolver := publicruntime.ShellResolver(func(ctx context.Context, requestPath string) ([]byte, bool, error) {
			return d.SiteSnapshots.HTMLShell(ctx, env, requestPath)
		})
		mux.Handle("/", publicruntime.Handler(d.S3PublicBaseURL, resolver))
	} else {
		mux.Handle("/", publicruntime.Handler(d.S3PublicBaseURL))
	}

	handler := middleware.RequestID(mux)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(d.Log)(handler)
	handler = middleware.Logger(d.Log)(handler)
	// WithEnvironment stays outermost, mirroring the previous wiring where
	// each public server wrapped the router itself.
	return middleware.WithEnvironment(env)(handler)
}

// Deps is an alias used to keep composite literals readable in tests.
type Deps = Dependencies
