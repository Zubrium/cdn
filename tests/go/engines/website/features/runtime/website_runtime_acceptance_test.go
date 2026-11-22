//go:build website_acceptance
// +build website_acceptance

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/website/features/runtime"
)

// TestWebsiteScenarioFeatureRuntime owns the executable contract assertions for
// WEB-016 in the centralized Go acceptance catalog. It deliberately exercises
// only the exported surface of the feature runtime package.
func TestWebsiteScenarioFeatureRuntime(t *testing.T) {
	t.Run("WEB-016", func(t *testing.T) {
		t.Run("manifest network and typed contract policy", func(t *testing.T) {
			web016ManifestNetworkPolicy(t)
		})
		t.Run("public context excludes credentials", func(t *testing.T) {
			web016PublicContextExcludesCredentials(t)
		})
		t.Run("cancellation and dispose", func(t *testing.T) {
			web016LifecycleCancellationAndDispose(t)
		})
		t.Run("token values fail closed", func(t *testing.T) {
			web016TokenValuesFailClosed(t)
		})
		t.Run("invalid execution reply", func(t *testing.T) {
			web016InvalidExecutionReplyIsRejected(t)
		})
	})
}

func web016Manifest(t *testing.T, origins []string, requests []any) []byte {
	t.Helper()
	spec := map[string]any{
		"abi":     runtime.ABI,
		"entry":   "assets/app.js",
		"styles":  []string{"assets/app.css"},
		"sdk":     []string{"api"},
		"origins": origins,
	}
	manifest := map[string]any{
		"formatVersion": runtime.ManifestFormatVersion,
		"key":           "hero",
		"name":          "Hero",
		"version":       "1.2.3",
		"engineApi":     "v1",
		"slots":         []string{"main"},
		"arguments":     map[string]any{"title": "text"},
		"dataInputs": []any{
			map[string]any{
				"key":    "items",
				"kind":   "collection",
				"type":   "text",
				"fields": []any{map[string]any{"path": "title", "type": "text"}},
			},
		},
		"runtime": spec,
	}
	if requests != nil {
		manifest["requests"] = requests
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func web016ManifestNetworkPolicy(t *testing.T) {
	files := map[string]struct{}{"assets/app.js": {}, "assets/app.css": {}}
	validBytes := web016Manifest(t, []string{"https://api.example.com"}, nil)
	valid, err := runtime.ValidateManifest(validBytes, files)
	if err != nil {
		t.Fatalf("baseline manifest rejected: %v", err)
	}

	for _, origin := range []string{
		"*",
		"https://*",
		"http://api.example.com",
		"https://api.example.com/path",
		"https://api.example.com?token=x",
		"https://user@api.example.com",
		"https://localhost",
		"https://127.0.0.1",
	} {
		t.Run(origin, func(t *testing.T) {
			manifest, err := runtime.ValidateManifest(web016Manifest(t, []string{origin}, nil), files)
			if !errors.Is(err, runtime.ErrOrigin) {
				t.Fatalf("origin %q: err=%v, want ErrOrigin", origin, err)
			}
			if manifest.Key != "" || len(manifest.Runtime.Origins) != 0 {
				t.Fatalf("rejected origin produced a partial manifest: %+v", manifest)
			}
		})
	}

	requestCases := []struct {
		name     string
		requests []any
		want     error
	}{
		{
			name:     "wildcard request origin",
			requests: []any{map[string]any{"origin": "*", "ports": []string{"443"}, "schema": map[string]any{}}},
			want:     runtime.ErrOrigin,
		},
		{
			name:     "non HTTPS request origin",
			requests: []any{map[string]any{"origin": "http://api.example.com", "ports": []string{"443"}, "schema": map[string]any{}}},
			want:     runtime.ErrOrigin,
		},
		{
			name:     "unknown request contract field",
			requests: []any{map[string]any{"origin": "https://api.example.com", "ports": []string{"443"}, "schema": map[string]any{}, "credentials": "include"}},
			want:     runtime.ErrManifest,
		},
	}
	for _, testCase := range requestCases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest, err := runtime.ValidateManifest(web016Manifest(t, []string{"https://api.example.com"}, testCase.requests), files)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("request contract %q: err=%v, want %v", testCase.name, err, testCase.want)
			}
			if manifest.Key != "" || len(manifest.Requests) != 0 {
				t.Fatalf("rejected request contract produced a partial manifest: %+v", manifest)
			}
		})
	}

	secretManifestBytes := []byte(strings.Replace(string(validBytes), `"title":"text"`, `"api_key":"text"`, 1))
	if _, err := runtime.ValidateManifest(secretManifestBytes, files); !errors.Is(err, runtime.ErrSecret) {
		t.Fatalf("credential-like manifest argument: err=%v, want ErrSecret", err)
	}

	if err := runtime.ValidateBindings(valid, map[string]runtime.Binding{
		"items": {Port: "items", EntityKey: "item", Field: "title", OutputType: "text"},
	}); err != nil {
		t.Fatalf("typed data input binding rejected: %v", err)
	}
	if err := runtime.ValidateBindings(valid, map[string]runtime.Binding{
		"items": {Port: "items", EntityKey: "item", Field: "title", OutputType: "number"},
	}); !errors.Is(err, runtime.ErrBinding) {
		t.Fatalf("incompatible typed binding: err=%v, want ErrBinding", err)
	}
}

func web016PublicContextExcludesCredentials(t *testing.T) {
	base := runtime.TrustedProjection{
		PlacementID: "web016-placement",
		FeatureKey:  "hero",
		Version:     "1.2.3",
		Arguments:   map[string]any{"title": "Hello"},
		Content:     map[string]any{"items": []any{"one"}},
		RoutePath:   "/products/:id",
		RouteParams: map[string]string{"id": "42"},
		RouteSearch: "?sort=asc",
		SystemLogo:  "https://cdn.example.com/logo.svg",
		SDK:         []string{"api"},
		Locale:      "de-DE",
		TokenValues: map[string]string{"hero_color": "#123456"},
	}
	public, err := runtime.BuildContext(base, "web016-signal")
	if err != nil {
		t.Fatalf("valid projection rejected: %v", err)
	}
	data, err := runtime.MarshalContext(public)
	if err != nil {
		t.Fatalf("marshal public context: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("public context is not valid JSON: %s", data)
	}
	for _, forbidden := range []string{
		"web016-secret", `"authorization"`, `"cookie"`, `"api_key"`, `"private_key"`,
	} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("public context leaked %q: %s", forbidden, data)
		}
	}
	base.Arguments["title"] = "mutated after build"
	base.TokenValues["hero_color"] = "mutated after build"
	if public.Arguments["title"] != "Hello" || public.TokenValues["hero_color"] != "#123456" {
		t.Fatalf("public context aliases the trusted projection: %+v", public)
	}

	credentialCases := []struct {
		name   string
		mutate func(*runtime.TrustedProjection)
	}{
		{"argument credentials", func(projection *runtime.TrustedProjection) {
			projection.Arguments = map[string]any{"authorization": "Bearer web016-secret"}
		}},
		{"nested content credentials", func(projection *runtime.TrustedProjection) {
			projection.Content = map[string]any{"meta": map[string]any{"api_key": "web016-secret"}}
		}},
		{"route credentials", func(projection *runtime.TrustedProjection) {
			projection.RouteParams = map[string]string{"private_key": "web016-secret"}
		}},
	}
	for _, testCase := range credentialCases {
		t.Run(testCase.name, func(t *testing.T) {
			projection := base
			testCase.mutate(&projection)
			rejected, err := runtime.BuildContext(projection, "web016-signal")
			if !errors.Is(err, runtime.ErrSecret) {
				t.Fatalf("credentials in %s: err=%v, want ErrSecret", testCase.name, err)
			}
			if rejected.PlacementID != "" || rejected.Signal.ID != "" {
				t.Fatalf("rejected context leaked a partial public value: %+v", rejected)
			}
		})
	}
}

type web016LifecycleCall struct {
	operation   runtime.Operation
	signalID    string
	placementID string
}

type web016LifecycleRecorder struct {
	mu             sync.Mutex
	calls          []web016LifecycleCall
	renderStarted  chan struct{}
	renderCanceled chan struct{}
	updateStarted  chan struct{}
	updateCanceled chan struct{}
}

func newWeb016LifecycleRecorder() *web016LifecycleRecorder {
	return &web016LifecycleRecorder{
		renderStarted:  make(chan struct{}),
		renderCanceled: make(chan struct{}),
		updateStarted:  make(chan struct{}),
		updateCanceled: make(chan struct{}),
	}
}

func (recorder *web016LifecycleRecorder) Execute(ctx context.Context, op runtime.Operation, featureContext runtime.FeatureContext) error {
	recorder.mu.Lock()
	recorder.calls = append(recorder.calls, web016LifecycleCall{
		operation: op, signalID: featureContext.Signal.ID, placementID: featureContext.PlacementID,
	})
	recorder.mu.Unlock()

	switch op {
	case runtime.OpRender:
		close(recorder.renderStarted)
		select {
		case <-ctx.Done():
			close(recorder.renderCanceled)
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return errors.New("render context was not canceled")
		}
	case runtime.OpUpdate:
		close(recorder.updateStarted)
		select {
		case <-ctx.Done():
			close(recorder.updateCanceled)
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return errors.New("update context was not canceled")
		}
	default:
		return nil
	}
}

func (recorder *web016LifecycleRecorder) snapshot() []web016LifecycleCall {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]web016LifecycleCall(nil), recorder.calls...)
}

func web016LifecycleCancellationAndDispose(t *testing.T) {
	if err := runtime.ValidateEmbeddedRuntime(); err != nil {
		t.Fatalf("browser lifecycle host lost a required guarantee: %v", err)
	}
	recorder := newWeb016LifecycleRecorder()
	placement, err := runtime.NewPlacement(recorder, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := runtime.FeatureContext{PlacementID: "web016-placement", FeatureKey: "hero", Signal: runtime.Cancellation{ID: "signal-1"}}
	second := runtime.FeatureContext{PlacementID: "web016-placement", FeatureKey: "hero", Signal: runtime.Cancellation{ID: "signal-2"}}

	renderDone := make(chan error, 1)
	go func() { renderDone <- placement.Render(ctx, first) }()
	<-recorder.renderStarted

	updateDone := make(chan error, 1)
	go func() { updateDone <- placement.Update(ctx, second) }()
	<-recorder.renderCanceled
	if err := <-renderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled render: err=%v, want context.Canceled", err)
	}
	<-recorder.updateStarted

	disposeDone := make(chan error, 1)
	go func() { disposeDone <- placement.Dispose(ctx) }()
	<-recorder.updateCanceled
	if err := <-updateDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update: err=%v, want context.Canceled", err)
	}
	if err := <-disposeDone; err != nil {
		t.Fatalf("dispose after cancellation: %v", err)
	}

	calls := recorder.snapshot()
	wantOperations := []runtime.Operation{runtime.OpRender, runtime.OpUpdate, runtime.OpDispose}
	if len(calls) != len(wantOperations) {
		t.Fatalf("lifecycle calls = %+v, want %v", calls, wantOperations)
	}
	for index, want := range wantOperations {
		if calls[index].operation != want {
			t.Fatalf("lifecycle calls = %+v, want %v", calls, wantOperations)
		}
	}
	if calls[0].signalID != "signal-1" || calls[1].signalID != "signal-2" {
		t.Fatalf("cancellation applied the wrong context: %+v", calls)
	}
	if calls[2].placementID != "" || calls[2].signalID != "" {
		t.Fatalf("dispose received a feature context: %+v", calls[2])
	}
	if active, busy, disposed := placement.State(); active != "" || busy || !disposed {
		t.Fatalf("placement state after dispose: active=%q busy=%v disposed=%v", active, busy, disposed)
	}
	if err := placement.Render(ctx, first); !errors.Is(err, runtime.ErrDisposed) {
		t.Fatalf("render after dispose: err=%v, want ErrDisposed", err)
	}
	if err := placement.Update(ctx, second); !errors.Is(err, runtime.ErrDisposed) {
		t.Fatalf("update after dispose: err=%v, want ErrDisposed", err)
	}
	if err := placement.Dispose(ctx); err != nil {
		t.Fatalf("repeated dispose: %v", err)
	}
	if got := len(recorder.snapshot()); got != len(wantOperations) {
		t.Fatalf("operations executed after dispose: calls=%+v", recorder.snapshot())
	}
}

func web016TokenValuesFailClosed(t *testing.T) {
	base := runtime.TrustedProjection{PlacementID: "web016-placement", FeatureKey: "hero"}
	for _, values := range []map[string]string{
		{"hero_color": "#123456"},
		{"hero_spacing": "1.5rem"},
		{"hero.light": "#ffffff", "hero.dark": "#000000"},
	} {
		projection := base
		projection.TokenValues = values
		public, err := runtime.BuildContext(projection, "web016-signal")
		if err != nil {
			t.Fatalf("valid token values %v rejected: %v", values, err)
		}
		for key, want := range values {
			if public.TokenValues[key] != want {
				t.Fatalf("token %q = %q, want %q", key, public.TokenValues[key], want)
			}
		}
	}

	invalidValues := []string{
		"",
		" #123456",
		"url(javascript:alert(1))",
		"javascript:alert(1)",
		"expression(alert(1))",
		"@import url(evil.css)",
		"<script>alert(1)</script>",
		"#123456; color: red",
		`url("https://evil.example/x")`,
		"line\nbreak",
	}
	for _, value := range invalidValues {
		t.Run(value, func(t *testing.T) {
			projection := base
			projection.TokenValues = map[string]string{"hero_color": value}
			rejected, err := runtime.BuildContext(projection, "web016-signal")
			if !errors.Is(err, runtime.ErrUnsupportedToken) {
				t.Fatalf("token value %q: err=%v, want ErrUnsupportedToken", value, err)
			}
			if rejected.PlacementID != "" || len(rejected.TokenValues) != 0 {
				t.Fatalf("rejected token value produced a partial context: %+v", rejected)
			}
		})
	}
}

func web016InvalidExecutionReplyIsRejected(t *testing.T) {
	featureContext := runtime.FeatureContext{
		PlacementID: "web016-placement", FeatureKey: "hero", Signal: runtime.Cancellation{ID: "web016-signal"},
	}
	replyCases := []struct {
		name    string
		reply   string
		wantErr bool
	}{
		{name: "valid reply", reply: `{"ok":true}`, wantErr: false},
		{name: "malformed reply", reply: `not-json`, wantErr: true},
		{name: "invalid ok field", reply: `{"ok":"yes"}`, wantErr: true},
		{name: "rejected reply", reply: `{"ok":false,"code":"invalid_response"}`, wantErr: true},
	}
	for _, testCase := range replyCases {
		t.Run(testCase.name, func(t *testing.T) {
			executor, err := runtime.NewCommandExecutor(runtime.CommandConfig{
				Binary:         "/bin/echo",
				Args:           []string{testCase.reply},
				MaxOutputBytes: 1024,
			})
			if err != nil {
				t.Fatal(err)
			}
			err = executor.Execute(context.Background(), runtime.OpRender, featureContext)
			if testCase.wantErr {
				if !errors.Is(err, runtime.ErrExecutor) {
					t.Fatalf("reply %q: err=%v, want ErrExecutor", testCase.reply, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid reply rejected: %v", err)
			}
		})
	}
}
