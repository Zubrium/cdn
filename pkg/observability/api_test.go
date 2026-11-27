package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitisationAndSensitiveKeys(t *testing.T) {
	if got := String("Authorization", "Bearer secret"); got.Key != "" {
		t.Fatalf("sensitive attribute was not dropped: %#v", got)
	}
	if got := String("display name", "Василий\nПетрович\x00"); got.Key != "display_name" || strings.ContainsAny(got.Text, "\n\x00") {
		t.Fatalf("unexpected sanitised attribute: %#v", got)
	}
	long := strings.Repeat("x", 300)
	if got := String("detail", long); len(got.Text) <= maxAttributeValue {
		t.Fatalf("long value was not truncated: %d", len(got.Text))
	}
}

func TestIdentityAttributesOmitEmptyValues(t *testing.T) {
	attrs := identityAttributes(Identity{ModuleKind: ModuleKindBus, ModuleID: "bus", ComponentID: "dispatcher", OperationID: "core.foo"})
	for _, attr := range attrs {
		if attr.Key == "" || attr.Text == "" {
			t.Fatalf("empty identity attribute was emitted: %#v", attr)
		}
	}
	if _, found := attributesMap(attrs)["service.name"]; found {
		t.Fatal("empty service.name shadows the process resource attribute")
	}

	dir := t.TempDir()
	observer, err := New(Config{Mode: ModeFile, Directory: dir, MinLevel: LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	identity := Identity{ModuleKind: ModuleKindBus, ModuleID: "bus", ComponentID: "dispatcher", OperationID: "core.foo"}
	if err := observer.Operation(context.Background(), identity, "bus.dispatch", nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	metrics, err := os.ReadFile(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(metrics)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"service.name", "deployment.profile"} {
			if value, found := record[key]; found && value == "" {
				t.Fatalf("empty %s shadows the resource attribute in metric: %s", key, line)
			}
		}
	}
}

func TestTracePropagation(t *testing.T) {
	incoming := http.Header{}
	TraceContext{TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), Sampled: true}.Inject(incoming)
	parent := ExtractTrace(incoming)
	if parent.TraceID != strings.Repeat("a", 32) || parent.SpanID != strings.Repeat("b", 16) || !parent.Sampled {
		t.Fatalf("round trip failed: %#v", parent)
	}
	child := newTraceContext(parent)
	if child.TraceID != parent.TraceID || child.ParentID != parent.SpanID || child.SpanID == parent.SpanID {
		t.Fatalf("child context is invalid: %#v", child)
	}
	if invalid := ExtractTrace(http.Header{"Traceparent": []string{"01-invalid"}}); invalid.TraceID != "" {
		t.Fatalf("malformed traceparent accepted: %#v", invalid)
	}
}

func TestFileBackendWritesAllSignals(t *testing.T) {
	dir := t.TempDir()
	observer, err := New(Config{Mode: ModeFile, Directory: dir, MinLevel: LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())

	ctx := observer.WithIdentity(context.Background(), Identity{
		ModuleKind: ModuleKindBus, ModuleID: "bus", ComponentID: "dispatcher", OperationID: "core.foo",
	})
	ctx, span := observer.Start(ctx, "bus.dispatch")
	observer.Log(ctx, LevelInfo, "hello", String("detail", "value"))
	metricIdentity := []Attribute{String("module.kind", "bus"), String("component.id", "dispatcher")}
	observer.IncCounter(ctx, "zubrium_test_total", 1, append(metricIdentity, String("outcome", "ok"))...)
	observer.ObserveHistogram(ctx, "zubrium_test_seconds", 0.1, append(metricIdentity, String("outcome", "ok"))...)
	observer.SetGauge(ctx, "zubrium_cache_entries", 7, String("cache", "core.bytes"))
	span.End(nil)

	for _, name := range []string{"logs.jsonl", "spans.jsonl", "metrics.jsonl"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(body) == 0 {
			t.Fatalf("%s is missing or empty: %v", name, err)
		}
		firstLine := strings.SplitN(string(body), "\n", 2)[0]
		var record map[string]any
		if err := json.Unmarshal([]byte(firstLine), &record); err != nil {
			t.Fatalf("invalid JSON in %s: %v", name, err)
		}
		if record["module.kind"] != "bus" || record["component.id"] != "dispatcher" {
			t.Fatalf("identity missing in %s: %#v", name, record)
		}
		if record["trace_id"] == "" || record["span_id"] == "" {
			t.Fatalf("trace correlation missing in %s: %#v", name, record)
		}
	}
	metrics, err := os.ReadFile(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metrics), "\"metric.kind\":\"gauge\"") ||
		!strings.Contains(string(metrics), "\"metric.name\":\"zubrium_cache_entries\"") {
		t.Fatalf("gauge sample missing from metrics.jsonl: %s", metrics)
	}
}

func TestRedactsDerivedKeysAndCredentialValues(t *testing.T) {
	if got := String("user.token", "abc"); got.Key != "" {
		t.Fatalf("derived sensitive key was accepted: %#v", got)
	}
	if got := Int("session.id", 42); got.Key != "" {
		t.Fatalf("sensitive numeric key was accepted: %#v", got)
	}
	values := []string{
		"Authorization: Bearer abcdefghijklmnop",
		"Cookie: session=abcdefghijklmnop; theme=dark",
		"token=abcdefghijklmnop",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijklmnop",
	}
	for _, value := range values {
		got := sanitiseValue(value)
		for _, secret := range []string{"abcdefghijklmnop", "eyJhbGciOiJIUzI1NiJ9"} {
			if strings.Contains(got, secret) {
				t.Fatalf("credential leaked from %q: %q", value, got)
			}
		}
	}
}

func TestRejectsCacheCorrelationKeys(t *testing.T) {
	keys := []string{
		"cache.key", "cache_key", "file.id", "principal.id", "request.url",
		"request_uri", "locale", "user.agent", "user.email", "username",
	}
	for _, key := range keys {
		if got := String(key, "safe-looking value"); got.Key != "" {
			t.Errorf("cache correlation key %q was accepted: %#v", key, got)
		}
	}
	if got := String("http.route", "/api/admin/forms/{id}"); got.Key == "" {
		t.Fatal("safe HTTP route attribute was rejected")
	}
}

func TestGaugeSnapshotReplacesPreviousValue(t *testing.T) {
	observer, err := New(Config{Mode: ModeNoop})
	if err != nil {
		t.Fatal(err)
	}
	ctx := observer.WithIdentity(context.Background(), Identity{
		ModuleKind: ModuleKindCore, ModuleID: "core", ComponentID: "cache",
	})
	labels := []Attribute{String("cache", "core.bytes")}
	observer.SetGauge(ctx, "zubrium_cache_bytes", 10, labels...)
	observer.SetGauge(ctx, "zubrium_cache_bytes", 4, labels...)

	snapshot := observer.metrics.render()
	if !strings.Contains(snapshot, "# TYPE zubrium_cache_bytes gauge") {
		t.Fatalf("gauge type is missing: %s", snapshot)
	}
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.HasPrefix(line, "zubrium_cache_bytes{") {
			if !strings.HasSuffix(line, " 4") {
				t.Fatalf("gauge was not replaced by the last observation: %s", snapshot)
			}
			return
		}
	}
	t.Fatalf("gauge series is missing: %s", snapshot)
}

func TestOperationMetricsStayLowCardinality(t *testing.T) {
	observer, err := New(Config{Mode: ModeNoop})
	if err != nil {
		t.Fatal(err)
	}
	err = observer.Operation(context.Background(), Identity{
		ModuleKind: ModuleKindWorker, ModuleID: "worker.media", ComponentID: "worker.media", OperationID: "media/v1",
	}, "worker.task", []Attribute{String("worker.task.id", "3f2e1d0c")}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := observer.metrics.render()
	if strings.Contains(snapshot, "worker.task.id") {
		t.Fatalf("high-cardinality attribute became a metric label: %s", snapshot)
	}
	if !strings.Contains(snapshot, "module.id=\"worker.media\"") || !strings.Contains(snapshot, "operation.id=\"media/v1\"") {
		t.Fatalf("operation identity missing from metrics: %s", snapshot)
	}
}

func TestHTTPMiddlewareAddsTraceAndMetrics(t *testing.T) {
	observer, err := New(Config{Mode: ModeNoop, MinLevel: LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	handler := observer.Handler(Identity{ModuleKind: ModuleKindPresentation, ModuleID: "admin", ComponentID: "http-server"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if observer.CurrentTrace(r.Context()).TraceID == "" {
			t.Error("trace context was not propagated to handler")
		}
		w.WriteHeader(http.StatusConflict)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID is missing")
	}
	snapshot := observer.metrics.render()
	if !strings.Contains(snapshot, "http.status_code=\"409\"") {
		t.Fatalf("409 is not reflected in metrics: %s", snapshot)
	}
}

func TestHTTPMiddlewareUsesRoutePatternAndSkipsHealthLog(t *testing.T) {
	dir := t.TempDir()
	observer, err := New(Config{Mode: ModeFile, Directory: dir, MinLevel: LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/admin/forms", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := observer.Handler(Identity{ModuleKind: ModuleKindPresentation, ModuleID: "admin", ComponentID: "http-server"}, mux)
	for _, path := range []string{"/healthz", "/api/admin/forms"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Header().Get("X-Trace-ID") == "" {
			t.Fatalf("trace header missing for %s", path)
		}
	}

	snapshot := observer.metrics.render()
	if !strings.Contains(snapshot, "http.route=\"/api/admin/forms\"") || !strings.Contains(snapshot, "http.route=\"/healthz\"") {
		t.Fatalf("route pattern is not reflected in metrics: %s", snapshot)
	}
	logs, err := os.ReadFile(filepath.Join(dir, "logs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logs), "http request completed") {
		t.Fatalf("request completion log is missing: %s", logs)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(logs)), "\n") {
		if strings.Contains(line, "/healthz") {
			t.Fatalf("healthz produced a completion log: %s", line)
		}
	}
}
