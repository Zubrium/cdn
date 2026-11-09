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
	hfeatures "websiteengine/presentation/http/handlers/features"
	hfiles "websiteengine/presentation/http/handlers/files"
	hforms "websiteengine/presentation/http/handlers/forms"
	hmedia "websiteengine/presentation/http/handlers/media"
	hoverview "websiteengine/presentation/http/handlers/overview"
	hsettings "websiteengine/presentation/http/handlers/settings"
	hsitebuilder "websiteengine/presentation/http/handlers/sitebuilder"
	"websiteengine/presentation/http/middleware"
)

type Dependencies struct {
	Sitepacks       *hsitebuilder.SitepackHandler
	Files           *hfiles.Handler
	BranchIndexable map[environment.Environment]bool
	Prerender       map[environment.Environment]*publicruntime.Prerenderer
	DB              interface {
		QueryRow(context.Context, string, ...interface{}) interface{ Scan(...interface{}) error }
	}
	Log                   *slog.Logger
	AuthService           *auth.Service
	Auth                  *hauth.Handler
	Features              *hfeatures.Handler
	Forms                 *hforms.Handler
	Media                 *hmedia.Handler
	Overview              *hoverview.Handler
	Settings              *hsettings.Handler
	FeatureBuilds         *hsitebuilder.BuildsHandler
	SiteSnapshots         *hsitebuilder.SnapshotsHandler
	Health                func(ctx context.Context) error
	MaxJSONBytes          int64
	FormRateLimit         int
	FormRateWindow        time.Duration
	FormRateMaxClients    int
	FormRateSweepInterval time.Duration
	ReadinessTimeout      time.Duration
	RuntimeAssetCacheTTL  time.Duration
	TrustProxy            bool
	PublicCORSByBranch    map[environment.Environment][]string
	S3PublicBaseURL       string
}

func NewRouter(d Deps) http.Handler {
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

	if d.Files != nil {
		d.Files.RegisterServiceRoutes(mux)
		// The handler enforces its own bounded JSON and per-part limits. Do not
		// apply the JSON body limit to streamed multipart chunks.
		d.Files.RegisterAdminRoutes(mux, user)
	}
	if d.Sitepacks != nil {
		mux.Handle("GET /api/v2/admin/sitepack/limits", siteManager(d.Sitepacks.Limits))
		mux.Handle("GET /api/v2/admin/site/snapshots/{id}/export", siteManager(d.Sitepacks.Export))
		mux.Handle("POST /api/v2/admin/site/snapshots/import", siteManager(d.Sitepacks.Import))
	}
	// Service health.
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if d.ReadinessTimeout <= 0 {
			response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Health check timeout is not configured")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d.ReadinessTimeout)
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

	mux.Handle("GET /api/v1/admin/submission-branches", user(d.Forms.Branches))

	// Sanitized runtime configuration for the admin settings section.
	if d.Settings != nil {
		mux.Handle("GET /api/v1/admin/settings", siteManager(http.HandlerFunc(d.Settings.Get)))
	}

	// Operational summary for the admin dashboard.
	mux.Handle("GET /api/v1/admin/overview", user(d.Overview.Get))

	// Feature metadata and schema publication.
	if d.FeatureBuilds != nil {
		mux.Handle("GET /api/v2/admin/feature-builds", siteManager(d.FeatureBuilds.List))
		mux.Handle("POST /api/v2/admin/feature-builds", siteManager(d.FeatureBuilds.Import))
	}
	if d.SiteSnapshots != nil {
		mux.Handle("GET /api/v2/admin/branches", siteManager(d.SiteSnapshots.Branches))
		mux.Handle("PUT /api/v2/admin/branches/{id}/snapshot", jsonSiteManager(d.SiteSnapshots.AssignBranch))
		mux.Handle("GET /api/v2/admin/site/snapshots/{id}", siteManager(d.SiteSnapshots.Snapshot))
		mux.Handle("DELETE /api/v2/admin/site/snapshots/{id}", siteManager(d.SiteSnapshots.DeleteSnapshot))
		mux.Handle("GET /api/v2/admin/site/snapshots/{id}/design-system", siteManager(d.SiteSnapshots.SnapshotDesignSystem))
		mux.Handle("POST /api/v2/admin/site/snapshots/{id}/design-system", jsonSiteManager(d.SiteSnapshots.SaveSnapshotDesignSystem))

		mux.Handle("GET /api/v2/admin/content/snapshots", user(d.SiteSnapshots.ContentSnapshots))
		mux.Handle("GET /api/v2/admin/content/snapshots/{id}/model", user(d.SiteSnapshots.ModelContentWorkspace))
		mux.Handle("PUT /api/v2/admin/content/snapshots/{id}/model", jsonUser(d.SiteSnapshots.SaveModelContentWorkspace))
		mux.Handle("POST /api/v2/admin/content/snapshots/{id}/model/publish", jsonUser(d.SiteSnapshots.PublishModelContentWorkspace))
		mux.Handle("GET /api/v2/admin/content/snapshots/{id}/model/history", user(d.SiteSnapshots.ModelContentHistory))
		mux.Handle("POST /api/v2/admin/content/snapshots/{id}/model/history/{publicationId}/restore", jsonUser(d.SiteSnapshots.RestoreModelContentPublication))
		mux.Handle("GET /api/v2/admin/site/snapshots", siteManager(d.SiteSnapshots.List))
		mux.Handle("GET /api/v2/admin/site/snapshot-events", siteManager(d.SiteSnapshots.Events))
		mux.Handle("POST /api/v2/admin/site/snapshots", jsonSiteManager(d.SiteSnapshots.Create))
	}
	// Keep only feature identity and immutable version reads for historical
	// submission labels. Mutations use feature ZIP registration and snapshots.
	mux.Handle("GET /api/v1/admin/features", user(d.Features.List))
	mux.Handle("GET /api/v1/admin/features/{id}/versions", user(d.Features.Versions))

	// Form submissions.
	mux.Handle("GET /api/v1/admin/forms/{key}/submissions", user(d.Forms.List))
	mux.Handle("GET /api/v1/admin/forms/{key}/submissions/export", user(d.Forms.Export))
	mux.Handle("GET /api/v1/admin/submissions", user(d.Forms.List))
	mux.Handle("GET /api/v1/admin/submissions/export", user(d.Forms.Export))
	mux.Handle("GET /api/v1/admin/submissions/{id}", user(d.Forms.Get))
	mux.Handle("PATCH /api/v1/admin/submissions/{id}/status", jsonSiteManager(d.Forms.UpdateStatus))
	mux.Handle("POST /api/v1/admin/submissions/{id}/archive", siteManager(d.Forms.Archive))
	mux.Handle("DELETE /api/v1/admin/submissions/{id}", admin(d.Forms.Delete))

	// Media administration and public redirect.
	mux.Handle("POST /api/v1/admin/media", user(d.Media.Upload))
	mux.Handle("POST /api/v2/admin/media/{id}/renditions/prepare", jsonUser(d.Media.PrepareRendition))
	mux.Handle("GET /api/v2/admin/media/{id}/renditions/state", user(d.Media.RenditionState))
	mux.Handle("GET /api/v1/admin/media", user(d.Media.List))
	mux.Handle("GET /api/v1/admin/media/{id}", user(d.Media.Get))
	mux.Handle("PATCH /api/v1/admin/media/{id}", jsonUser(d.Media.Update))
	mux.Handle("POST /api/v1/admin/media/{id}/retry", user(d.Media.Retry))
	mux.Handle("DELETE /api/v1/admin/media/{id}", admin(d.Media.Delete))

	// Public media redirects use the configured branch CORS policy.
	corsByBranch := d.PublicCORSByBranch
	if corsByBranch == nil {
		corsByBranch = map[environment.Environment][]string{}
	}
	cors := middleware.CORSFor(corsByBranch)
	mux.Handle("GET /media/{key}", cors(http.HandlerFunc(d.Media.Redirect)))
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
	handler = middleware.CompressText(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(d.Log)(handler)
	handler = middleware.Logger(d.Log)(handler)
	return handler
}

// NewPublicRouter serves a single site environment. The handler tags itself
// with that environment, so content and CORS resolve to the right listener.
func NewPublicRouter(d Deps, env environment.Environment) http.Handler {
	indexable := d.BranchIndexable[env]
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if d.ReadinessTimeout <= 0 {
			response.Error(w, r, http.StatusInternalServerError, "configuration_error", "Health check timeout is not configured")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d.ReadinessTimeout)
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

	corsByBranch := d.PublicCORSByBranch
	if corsByBranch == nil {
		corsByBranch = map[environment.Environment][]string{}
	}
	cors := middleware.CORSFor(corsByBranch)
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, pattern := range []string{
		"OPTIONS /api/v2/runtime",
		"OPTIONS /api/v2/site-themes/snapshots/{id}",
		"OPTIONS /api/v2/site-themes/previews/{token}",
		"OPTIONS /api/v2/system-assets/{id}",
	} {
		mux.Handle(pattern, cors(noop))
	}
	// Snapshot assignment is the only public source for page and form schemas.
	formLimiter := middleware.NewRateLimiter(d.FormRateLimit, d.FormRateWindow, d.FormRateMaxClients, d.FormRateSweepInterval, d.TrustProxy)
	if d.SiteSnapshots != nil {
		mux.HandleFunc("GET /robots.txt", d.SiteSnapshots.Robots(indexable))
		mux.HandleFunc("GET /sitemap.xml", d.SiteSnapshots.Sitemap(indexable))
		mux.HandleFunc("GET /sitemaps/{part}", d.SiteSnapshots.SitemapPart(indexable))
		mux.Handle("GET /api/v2/runtime", cors(http.HandlerFunc(d.SiteSnapshots.Runtime)))
		mux.Handle("GET /api/v2/site-themes/snapshots/{id}", cors(http.HandlerFunc(d.SiteSnapshots.SnapshotThemeCSS)))
		mux.Handle("GET /api/v2/site-themes/previews/{token}", cors(http.HandlerFunc(d.SiteSnapshots.PreviewThemeCSS)))
		mux.Handle("GET /api/v2/system-assets/{id}", cors(http.HandlerFunc(d.Media.SystemAsset)))
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
		resolver := publicruntime.DocumentResolver(func(ctx context.Context, requestPath, rawQuery string) (publicruntime.Document, error) {
			return d.SiteSnapshots.PageDocument(ctx, env, requestPath, rawQuery, indexable)
		})
		mux.Handle("/", publicruntime.DocumentHandler(d.S3PublicBaseURL, resolver, d.Prerender[env], d.RuntimeAssetCacheTTL))
	} else {
		resolver := publicruntime.DocumentResolver(func(context.Context, string, string) (publicruntime.Document, error) {
			return publicruntime.SystemDocument(http.StatusServiceUnavailable), nil
		})
		mux.Handle("/", publicruntime.DocumentHandler(d.S3PublicBaseURL, resolver, nil, d.RuntimeAssetCacheTTL))
	}

	handler := middleware.RequestID(mux)
	handler = middleware.CompressText(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(d.Log)(handler)
	handler = middleware.Logger(d.Log)(handler)
	// WithEnvironment stays outermost, mirroring the previous wiring where
	// each public server wrapped the router itself.
	return middleware.WithEnvironment(env)(handler)
}

// Deps is an alias used to keep composite literals readable in tests.
type Deps = Dependencies
