package observability

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type capturedOTLPRequest struct {
	path   string
	body   []byte
	header http.Header
}

type otlpCollector struct {
	mu       sync.Mutex
	requests []capturedOTLPRequest
}

func (c *otlpCollector) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.requests = append(c.requests, capturedOTLPRequest{
		path:   r.URL.Path,
		body:   body,
		header: r.Header.Clone(),
	})
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func (c *otlpCollector) snapshot() []capturedOTLPRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedOTLPRequest(nil), c.requests...)
}

func (c *otlpCollector) bodies(path string) [][]byte {
	var bodies [][]byte
	for _, request := range c.snapshot() {
		if request.path == path {
			bodies = append(bodies, request.body)
		}
	}
	return bodies
}

func testOTLPConfig(server *httptest.Server) Config {
	return Config{
		Mode:        ModeOTLP,
		Endpoint:    server.URL,
		Insecure:    true,
		Timeout:     2 * time.Second,
		MetricFlush: time.Hour,
		Headers:     map[string]string{"x-otel-test": "unit"},
	}
}

func testIdentity() Identity {
	return Identity{
		ServiceName:    "zubrium-test",
		ServiceVersion: "1.2.3",
		InstanceID:     "instance-1",
		Profile:        "test",
		BranchID:       "main",
	}
}

func requireContainsAny(t *testing.T, bodies [][]byte, values ...string) {
	t.Helper()
	for _, body := range bodies {
		complete := true
		for _, value := range values {
			if !bytes.Contains(body, []byte(value)) {
				complete = false
				break
			}
		}
		if complete {
			return
		}
	}
	t.Errorf("no OTLP body contains all of %q", values)
}

func requireHeaders(t *testing.T, requests []capturedOTLPRequest) {
	t.Helper()
	for _, request := range requests {
		if got := request.header.Get("x-otel-test"); got != "unit" {
			t.Errorf("%s: expected configured OTLP header, got %q", request.path, got)
		}
	}
}

func findExportedSpan(t *testing.T, bodies [][]byte, name string) *tracev1.Span {
	t.Helper()
	for _, body := range bodies {
		var data tracev1.TracesData
		if err := proto.Unmarshal(body, &data); err != nil {
			t.Fatalf("decode OTLP traces: %v", err)
		}
		for _, resourceSpans := range data.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					if span.Name == name {
						return span
					}
				}
			}
		}
	}
	return nil
}

func TestOTLPBackendExportsAllSignals(t *testing.T) {
	collector := &otlpCollector{}
	server := httptest.NewServer(http.HandlerFunc(collector.serve))
	defer server.Close()

	var trace TraceContext
	func() {
		backend, err := newOTLPBackend(testOTLPConfig(server), testIdentity())
		if err != nil {
			t.Fatalf("newOTLPBackend: %v", err)
		}
		defer func() {
			if err := backend.Close(context.Background()); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()

		_, child, end := backend.Start(context.Background(), testIdentity(), "otlp-operation",
			[]Attribute{String("operation.id", "op-42"), Int("attempt", 7)}, TraceContext{})
		trace = child
		if trace.TraceID == "" || trace.SpanID == "" {
			t.Fatal("Start returned an empty trace context")
		}
		end(nil)

		backend.Log(context.Background(), testIdentity(), LevelError, "otlp log message",
			[]Attribute{String("module.id", "core"), Bool("ready", true), Float("ratio", 0.5)}, trace)
		backend.Counter(context.Background(), "zubrium_test_counter_total", []Attribute{String("metric.kind", "counter")}, 3)
		backend.Histogram(context.Background(), "zubrium_test_histogram_seconds", []Attribute{String("metric.kind", "histogram")}, 0.25)
	}()

	requests := collector.snapshot()
	paths := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		paths[request.path] = struct{}{}
	}
	for _, path := range []string{"/v1/logs", "/v1/traces", "/v1/metrics"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("no POST to %s (paths: %v)", path, paths)
		}
	}
	requireHeaders(t, requests)

	logs := collector.bodies("/v1/logs")
	requireContainsAny(t, logs, "otlp log message", "module.id", "service.name", "zubrium-test")

	var exportedLog *logspb.LogRecord
	for _, body := range logs {
		var data logspb.LogsData
		if err := proto.Unmarshal(body, &data); err != nil {
			t.Fatalf("decode OTLP logs: %v", err)
		}
		for _, resourceLogs := range data.ResourceLogs {
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				for _, record := range scopeLogs.LogRecords {
					if record.Body.GetStringValue() == "otlp log message" {
						exportedLog = record
					}
				}
			}
		}
		if exportedLog != nil {
			break
		}
	}
	if exportedLog == nil {
		t.Fatal("log record was not exported")
	}
	if got := hex.EncodeToString(exportedLog.TraceId); got != trace.TraceID {
		t.Errorf("log trace ID = %q, want %q", got, trace.TraceID)
	}
	if got := hex.EncodeToString(exportedLog.SpanId); got != trace.SpanID {
		t.Errorf("log span ID = %q, want %q", got, trace.SpanID)
	}

	traces := collector.bodies("/v1/traces")
	requireContainsAny(t, traces, "otlp-operation", "operation.id", "span.status")

	metrics := collector.bodies("/v1/metrics")
	requireContainsAny(t, metrics, "zubrium_test_counter_total", "zubrium_test_histogram_seconds", "metric.kind")
}

func TestOTLPBackendPropagatesRemoteParent(t *testing.T) {
	const (
		traceID = "0123456789abcdef0123456789abcdef"
		spanID  = "abcdef0123456789"
	)
	parent := ExtractTrace(http.Header{"Traceparent": []string{"00-" + traceID + "-" + spanID + "-01"}})
	if parent.TraceID != traceID || parent.SpanID != spanID || !parent.Sampled {
		t.Fatalf("unexpected parsed parent: %+v", parent)
	}
	propagatorFields := otel.GetTextMapPropagator().Fields()
	hasTraceparent := false
	for _, field := range propagatorFields {
		if strings.EqualFold(field, "traceparent") {
			hasTraceparent = true
		}
	}
	if !hasTraceparent {
		t.Fatal("global propagator does not carry traceparent")
	}

	collector := &otlpCollector{}
	server := httptest.NewServer(http.HandlerFunc(collector.serve))
	defer server.Close()

	var trace TraceContext
	func() {
		backend, err := newOTLPBackend(testOTLPConfig(server), testIdentity())
		if err != nil {
			t.Fatalf("newOTLPBackend: %v", err)
		}
		defer func() {
			if err := backend.Close(context.Background()); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()

		_, child, end := backend.Start(context.Background(), testIdentity(), "parented-operation", nil, parent)
		trace = child
		if trace.TraceID != parent.TraceID {
			t.Errorf("trace ID = %q, want parent %q", trace.TraceID, parent.TraceID)
		}
		if trace.ParentID != parent.SpanID {
			t.Errorf("parent span ID = %q, want %q", trace.ParentID, parent.SpanID)
		}
		if trace.SpanID == "" || trace.SpanID == parent.SpanID {
			t.Errorf("unexpected child span ID %q", trace.SpanID)
		}
		end(nil)
	}()

	bodies := collector.bodies("/v1/traces")
	if len(bodies) == 0 {
		t.Fatal("no POST to /v1/traces")
	}
	exported := findExportedSpan(t, bodies, "parented-operation")
	if exported == nil {
		t.Fatal("parented-operation span was not exported")
	}
	if got := hex.EncodeToString(exported.TraceId); got != traceID {
		t.Errorf("exported trace ID = %q, want %q", got, traceID)
	}
	if got := hex.EncodeToString(exported.ParentSpanId); got != spanID {
		t.Errorf("exported parent span ID = %q, want %q", got, spanID)
	}
	if got := hex.EncodeToString(exported.SpanId); got != trace.SpanID {
		t.Errorf("exported span ID = %q, want %q", got, trace.SpanID)
	}
}

func TestOTLPBackendRecordsSpanError(t *testing.T) {
	collector := &otlpCollector{}
	server := httptest.NewServer(http.HandlerFunc(collector.serve))
	defer server.Close()

	func() {
		backend, err := newOTLPBackend(testOTLPConfig(server), testIdentity())
		if err != nil {
			t.Fatalf("newOTLPBackend: %v", err)
		}
		defer func() {
			if err := backend.Close(context.Background()); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()

		_, _, end := backend.Start(context.Background(), testIdentity(), "failing-operation", nil, TraceContext{})
		end(errors.New("boom: detail"))
	}()

	span := findExportedSpan(t, collector.bodies("/v1/traces"), "failing-operation")
	if span == nil {
		t.Fatal("failing-operation span was not exported")
	}
	if span.Status == nil || span.Status.Code != tracev1.Status_STATUS_CODE_ERROR {
		t.Errorf("span status is not Error: %+v", span.Status)
	}
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[attr.GetKey()] = attr.Value.GetStringValue()
	}
	if attrs["error.type"] != "boom" {
		t.Errorf("error.type = %q, want boom", attrs["error.type"])
	}
	if !strings.Contains(attrs["error.message"], "boom: detail") {
		t.Errorf("error.message = %q, want boom: detail", attrs["error.message"])
	}
	if attrs["span.status"] != "error" {
		t.Errorf("span.status = %q, want error", attrs["span.status"])
	}
}
