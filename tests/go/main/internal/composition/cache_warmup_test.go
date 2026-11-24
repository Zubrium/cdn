package composition

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appcache "github.com/Zubrium/cdn/core/application/cache"
	platform "github.com/Zubrium/cdn/core/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/presentation/router"
)

const cacheWarmupRouteID = "01938f7a-68af-7c20-a51d-78b6ca125399"

func resolvedForRouterWarmup() appconfig.Resolved {
	return appconfig.Resolved{
		Core: platform.RuntimeConfig{Warmup: appcache.Config{
			Enabled: true,
			Mode:    appcache.ModeStrict,
			Targets: appcache.Targets{RouterManifests: true, RouterRoutes: true},
		}},
		Scales: appconfig.ResolvedScales{WarmupTimeouts: appconfig.WarmupResolvedTimeouts{
			Timeout: 30 * time.Second, PerKeyTimeout: 5 * time.Second, RefreshInterval: 2 * time.Second,
		}},
		PublishedArtifacts: []appconfig.ResolvedPublishedArtifact{{
			RouteID: cacheWarmupRouteID, Locale: "en-US",
			FileID: "01938f7a-68af-7c10-a51d-78b6ca125301",
			Tier:   platform.CacheTierRequired,
			Paths:  []string{"/site/", "/site/assets/app.js"},
			Authorizations: []platform.CachePrincipalAction{{
				PrincipalID: "01938f7a-68af-7c30-a51d-78b6ca125301", Action: "read",
			}},
		}},
	}
}

func TestRouterWarmupContractFromResolvedConfig(t *testing.T) {
	resolved := resolvedForRouterWarmup()
	targets := routerWarmupTargets(resolved)
	if len(targets) != 1 {
		t.Fatalf("router warm-up targets = %+v, want one published artifact", targets)
	}
	target := targets[0]
	if target.RouteID != cacheWarmupRouteID || target.Locale != "en-US" ||
		target.FileID != "01938f7a-68af-7c10-a51d-78b6ca125301" || target.Tier != router.CacheTierRequired ||
		len(target.Paths) != 2 {
		t.Fatalf("mapped router warm-up target = %+v", target)
	}

	cfg := routerWarmupConfig(resolved)
	if !cfg.Manifests || !cfg.Routes || cfg.Timeout != 30*time.Second ||
		cfg.PerKeyTimeout != 5*time.Second || cfg.RefreshInterval != 2*time.Second {
		t.Fatalf("mapped router warm-up config = %+v", cfg)
	}

	resolved.Core.Warmup.Targets.RouterRoutes = false
	if cfg := routerWarmupConfig(resolved); !cfg.Manifests || cfg.Routes {
		t.Fatalf("disabled router target was not gated: %+v", cfg)
	}
	resolved.Core.Warmup.Mode = appcache.ModeOff
	if cfg := routerWarmupConfig(resolved); cfg.Manifests || cfg.Routes || cfg.RefreshInterval != 0 {
		t.Fatalf("off mode still enables router warm-up: %+v", cfg)
	}
}

func TestRouterCacheObserverUsesExactLowCardinalityLabels(t *testing.T) {
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	adapter := routerCacheObserver{observer: observer}
	ctx := context.Background()
	adapter.IncCounterExact(ctx, "zubrium_cache_warmup_keys_total", 1,
		router.StringCacheLabel("cache", "router.manifests"),
		router.StringCacheLabel("tier", "REQUIRED"),
		router.StringCacheLabel("result", "WARMED"))
	adapter.SetGaugeExact(ctx, "zubrium_cache_entries", 3,
		router.StringCacheLabel("cache", "router.manifests"))
	adapter.ObserveHistogramExact(ctx, "zubrium_cache_warmup_duration_seconds", 0.01,
		router.StringCacheLabel("cache", "router.routes"))

	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("/metrics Cache-Control = %q, want no-store", got)
	}
	body := recorder.Body.String()
	for _, fragment := range []string{
		`cache="router.manifests"`, `tier="REQUIRED"`, `result="WARMED"`,
		`cache="router.routes"`,
		"zubrium_cache_warmup_keys_total", "zubrium_cache_warmup_duration_seconds",
		"zubrium_cache_entries",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("metrics snapshot is missing %q:\n%s", fragment, body)
		}
	}
	for _, forbidden := range []string{cacheWarmupRouteID, "file_id", "principal_id", "locale=", "http://", "?"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics snapshot leaked %q:\n%s", forbidden, body)
		}
	}
}
