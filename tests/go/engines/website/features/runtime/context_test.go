package runtime_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

func TestBuildContextIsSecretFreeAndSerializable(t *testing.T) {
	projection := runtime.TrustedProjection{
		PlacementID: "placement-1", FeatureKey: "hero", Version: "1.2.3",
		Arguments: map[string]any{"title": "Hello"},
		Content:   map[string]any{"items": []any{"one", "two"}},
		RoutePath: "/products/:id", RouteParams: map[string]string{"id": "42"}, RouteSearch: "?sort=asc",
		SystemLogo: "https://cdn.example.com/logo.svg",
		Locale:     "de-DE", SDK: []string{"api"},
		TokenValues: map[string]string{"hero_color": "#123456"},
	}
	context, err := runtime.BuildContext(projection, "signal-1")
	if err != nil {
		t.Fatal(err)
	}
	if context.Signal.ID != "signal-1" || context.Locale != "de-DE" || context.Route.Path != "/products/:id" {
		t.Fatalf("unexpected context: %+v", context)
	}
	data, err := runtime.MarshalContext(context)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || !strings.Contains(string(data), `"placementId":"placement-1"`) {
		t.Fatalf("invalid public context payload: %s", data)
	}

	projection.Arguments["title"] = "changed after build"
	if context.Arguments["title"] != "Hello" {
		t.Fatal("public context aliases trusted projection maps")
	}
}

func TestBuildContextRejectsCredentialsAndInvalidTokens(t *testing.T) {
	base := runtime.TrustedProjection{PlacementID: "p", FeatureKey: "f"}
	if _, err := runtime.BuildContext(base, "s"); err != nil {
		t.Fatal(err)
	}
	secretArgs := base
	secretArgs.Arguments = map[string]any{"authorization": "Bearer secret"}
	if _, err := runtime.BuildContext(secretArgs, "s"); !errors.Is(err, runtime.ErrSecret) {
		t.Fatalf("argument credentials: err=%v", err)
	}
	secretContent := base
	secretContent.Content = map[string]any{"private_token": "secret"}
	if _, err := runtime.BuildContext(secretContent, "s"); !errors.Is(err, runtime.ErrSecret) {
		t.Fatalf("content credentials: err=%v", err)
	}
	secretRoute := base
	secretRoute.RouteParams = map[string]string{"api_key": "secret"}
	if _, err := runtime.BuildContext(secretRoute, "s"); !errors.Is(err, runtime.ErrSecret) {
		t.Fatalf("route credentials: err=%v", err)
	}
	badToken := base
	badToken.TokenValues = map[string]string{"hero": "url(javascript:alert(1))"}
	if _, err := runtime.BuildContext(badToken, "s"); !errors.Is(err, runtime.ErrUnsupportedToken) {
		t.Fatalf("invalid token value: err=%v", err)
	}
}

func TestEmbeddedBrowserHostExposesLifecycleGuarantees(t *testing.T) {
	if err := runtime.ValidateEmbeddedRuntime(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runtime.EmbeddedRuntimeSource(), "createFeatureHost") {
		t.Fatal("embedded browser host source is empty")
	}
}
