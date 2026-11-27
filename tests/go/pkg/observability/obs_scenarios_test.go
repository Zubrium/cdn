package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	obsTraceA = "0123456789abcdef0123456789abcdef"
	obsSpanA  = "abcdef0123456789"
	obsTraceB = "fedcba9876543210fedcba9876543210"
	obsTraceC = "aabbccddeeff00112233445566778899"
	obsSpanB  = "0123456789abcdef"
)

var (
	obsHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
	obsHex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func obsFileConfig(dir string) Config {
	return Config{
		Mode: ModeFile, Directory: dir, MaxFileBytes: 16 << 10, MaxFiles: 3,
		MinLevel:    LevelDebug,
		ServiceName: "zubrium", ServiceVersion: "1.2.3", InstanceID: "obs-instance-1",
		Profile: "full",
	}
}

func obsIdentity(kind ModuleKind, module, component, operation string) Identity {
	return Identity{
		ModuleKind: kind, ModuleID: module, ComponentID: component, OperationID: operation,
	}
}

// obsSearchBody queries a live telemetry sink by trace ID. A plain GET URL
// template remains supported for simple sinks. OpenObserve v1.1.2 needs an
// authenticated POST with an explicit SQL window and separate metric streams,
// so it is selected by OBS_TEST_OQ_AUTH instead of encoding a secret in the URL.
func obsSearchBody(client *http.Client, searchURL, auth, signal, traceID string, required ...string) ([]byte, error) {
	if auth == "" {
		query := fmt.Sprintf(searchURL, url.QueryEscape(traceID))
		response, err := client.Get(strings.ReplaceAll(query, "{signal}", signal))
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			return body, fmt.Errorf("status %d", response.StatusCode)
		}
		return body, nil
	}
	return obsOpenObserveSearch(client, searchURL, auth, signal, traceID, required)
}

func obsOpenObserveSearch(client *http.Client, searchURL, auth, signal, traceID string, required []string) ([]byte, error) {
	searchURL = strings.TrimSuffix(searchURL, "/")
	streams := []string{"default"}
	if signal == "metrics" {
		streamURL := strings.TrimSuffix(searchURL, "/_search") + "/streams"
		request, err := http.NewRequest(http.MethodGet, streamURL, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Basic "+auth)
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if response.StatusCode != http.StatusOK {
			return body, fmt.Errorf("streams status %d", response.StatusCode)
		}
		var listing struct {
			List []struct {
				Name       string `json:"name"`
				StreamType string `json:"stream_type"`
			} `json:"list"`
		}
		if err := json.Unmarshal(body, &listing); err != nil {
			return nil, err
		}
		streams = nil
		for _, stream := range listing.List {
			if stream.StreamType == "metrics" && strings.HasPrefix(stream.Name, "zubrium_") {
				streams = append(streams, stream.Name)
			}
		}
	}
	end := time.Now().Add(time.Minute).UnixMicro()
	start := time.Now().Add(-2 * time.Hour).UnixMicro()
	var collected []byte
	for attempt := 0; attempt < 20; attempt++ {
		collected = nil
		for _, stream := range streams {
			sql := fmt.Sprintf("SELECT * FROM %q WHERE trace_id = '%s'", stream, traceID)
			if signal == "metrics" {
				// Gauge streams have no exemplars field; inspect recent records
				// client-side instead of issuing a stream-specific SQL filter.
				sql = fmt.Sprintf("SELECT * FROM %q", stream)
			}
			payload, err := json.Marshal(map[string]any{
				"query": map[string]any{
					"sql":        sql,
					"start_time": start,
					"end_time":   end,
					"size":       50,
				},
				"search_type": "ui",
			})
			if err != nil {
				return nil, err
			}
			request, err := http.NewRequest(http.MethodPost, searchURL+"?type="+signal, bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Basic "+auth)
			response, err := client.Do(request)
			if err != nil {
				return nil, err
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			if response.StatusCode != http.StatusOK {
				// OpenObserve reports streams that were registered but have no
				// data files as "Search stream not found"; they are empty here.
				if response.StatusCode == http.StatusBadRequest && bytes.Contains(body, []byte("Search stream not found")) {
					continue
				}
				return body, fmt.Errorf("%s/%s status %d", signal, stream, response.StatusCode)
			}
			collected = append(collected, body...)
			collected = append(collected, '\n')
			if bytes.Contains(body, []byte(traceID)) {
				collected = append(collected, []byte("stream="+stream+"\n")...)
			}
		}
		complete := true
		for _, marker := range append([]string{traceID}, required...) {
			if !obsContainsMarker(collected, marker) {
				complete = false
				break
			}
		}
		if complete {
			return collected, nil
		}
		time.Sleep(time.Second)
	}
	return collected, nil
}

func obsContainsMarker(body []byte, marker string) bool {
	text := string(body)
	return strings.Contains(text, marker) || strings.Contains(text, strings.ReplaceAll(marker, ".", "_"))
}

func obsObserver(t *testing.T, cfg Config) *Observer {
	t.Helper()
	observer, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	t.Cleanup(func() {
		if err := observer.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return observer
}

func obsSignalRecords(t *testing.T, dir, name string) []map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, name+".jsonl*"))
	if err != nil {
		t.Fatal(err)
	}
	var all []map[string]any
	for _, path := range paths {
		all = append(all, obsRecords(t, path)...)
	}
	return all
}

func obsRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var records []map[string]any
	for number, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("%s line %d is not a JSON object: %v (%q)", path, number+1, err, line)
		}
		records = append(records, record)
	}
	return records
}

func obsText(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func obsNumber(record map[string]any, key string) (float64, bool) {
	value, ok := record[key].(float64)
	return value, ok
}

func obsFind(records []map[string]any, match func(map[string]any) bool) map[string]any {
	for _, record := range records {
		if match(record) {
			return record
		}
	}
	return nil
}

func obsDescribe(records []map[string]any) string {
	encoded, err := json.Marshal(records)
	if err != nil {
		return fmt.Sprintf("%v", records)
	}
	return string(encoded)
}

var obsMandatoryFields = []string{
	"timestamp", "signal", "service.name", "service.version", "service.instance.id",
	"deployment.profile", "module.kind", "module.id", "component.id",
	"operation.id", "trace_id", "span_id",
}

func obsRequireMandatory(t *testing.T, record map[string]any, signal string) {
	t.Helper()
	for _, field := range obsMandatoryFields {
		if obsText(record, field) == "" {
			t.Errorf("mandatory field %q missing in %s record: %s", field, signal, obsDescribe([]map[string]any{record}))
		}
	}
	if got := obsText(record, "signal"); got != signal {
		t.Errorf("signal = %q, want %q", got, signal)
	}
	if _, err := time.Parse(time.RFC3339Nano, obsText(record, "timestamp")); err != nil {
		t.Errorf("timestamp %q is not RFC3339Nano: %v", obsText(record, "timestamp"), err)
	}
}

func obsRequireIdentity(t *testing.T, record map[string]any, identity Identity, where string) {
	t.Helper()
	for field, want := range map[string]string{
		"module.kind": string(identity.ModuleKind), "module.id": identity.ModuleID,
		"component.id": identity.ComponentID, "operation.id": identity.OperationID,
	} {
		if got := obsText(record, field); got != want {
			t.Errorf("%s: %s = %q, want %q; record: %s", where, field, got, want, obsDescribe([]map[string]any{record}))
		}
	}
}

func obsMetricsHTTP(t *testing.T, observer *Observer) (int, http.Header, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder.Code, recorder.Header(), recorder.Body.String()
}

func obsMetricLines(snapshot, prefix string) []string {
	var lines []string
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, line)
		}
	}
	return lines
}

func obsServe(t *testing.T, observer *Observer, identity Identity, method, pattern, target string, status int) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	recorder := httptest.NewRecorder()
	observer.HTTP(identity, mux).ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func TestObsScenario(t *testing.T) {
	t.Run("OBS-001", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		presentation := obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.admin/forms-list/v1")
		health := obsServe(t, observer, presentation, http.MethodGet, "/healthz", "/healthz", http.StatusNoContent)
		forms := obsServe(t, observer, presentation, http.MethodGet, "/api/admin/forms", "/api/admin/forms", http.StatusOK)
		if health.Code != http.StatusNoContent || forms.Code != http.StatusOK {
			t.Fatalf("unexpected business statuses: healthz=%d forms=%d", health.Code, forms.Code)
		}

		logs := obsRecords(t, filepath.Join(dir, "logs.jsonl"))
		for _, record := range logs {
			if obsText(record, "message") == "http request completed" && obsText(record, "http.route") == "/healthz" {
				t.Fatalf("healthz produced a completion log: %s", obsDescribe([]map[string]any{record}))
			}
		}
		if obsFind(logs, func(record map[string]any) bool {
			return obsText(record, "message") == "http request completed" && obsText(record, "http.route") == "/api/admin/forms"
		}) == nil {
			t.Fatalf("forms request completion log is missing: %s", obsDescribe(logs))
		}

		status, headers, snapshot := obsMetricsHTTP(t, observer)
		if status != http.StatusOK {
			t.Fatalf("/metrics status = %d", status)
		}
		if got := headers.Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
			t.Errorf("/metrics Content-Type = %q", got)
		}
		if got := headers.Get("Cache-Control"); got != "no-store" {
			t.Errorf("/metrics Cache-Control = %q", got)
		}
		for _, route := range []string{"/healthz", "/api/admin/forms"} {
			wantStatus := "200"
			if route == "/healthz" {
				wantStatus = "204"
			}
			counter := obsMetricLines(snapshot, "zubrium_http_server_requests_total{")
			found := false
			for _, line := range counter {
				if strings.Contains(line, `http.route="`+route+`"`) && strings.Contains(line, `http.status_code="`+wantStatus+`"`) && strings.HasSuffix(line, " 1") {
					found = true
				}
			}
			if !found {
				t.Errorf("no single HTTP counter observation for %s (%s): %s", route, wantStatus, snapshot)
			}
			histogram := obsMetricLines(snapshot, "zubrium_http_server_duration_seconds_count{")
			if obsFindMetricLine(histogram, `http.route="`+route+`"`, " 1") == "" {
				t.Errorf("no HTTP duration observation for %s: %s", route, snapshot)
			}
		}
	})

	t.Run("OBS-002", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		presentation := obsIdentity(ModuleKindPresentation, "forms-admin", "http-server", "presentation.forms/status-update/v1")
		mux := http.NewServeMux()
		mux.HandleFunc("PATCH /api/admin/forms/{id}", func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("Authorization") == "" || r.Header.Get("Cookie") == "" || !strings.Contains(string(body), "do-not-leak") {
				t.Error("test precondition: sensitive request data did not reach the handler")
			}
			w.WriteHeader(http.StatusConflict)
		})
		request := httptest.NewRequest(http.MethodPatch, "/api/admin/forms/form-42?form=do-not-leak",
			strings.NewReader(`{"form":{"values":{"secret":"do-not-leak"}}}`))
		request.Header.Set("Authorization", "Bearer SECRET-BEARER-VALUE")
		request.Header.Set("Cookie", "session=SECRET-COOKIE-VALUE")
		recorder := httptest.NewRecorder()
		observer.HTTP(presentation, mux).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("business status = %d, want 409", recorder.Code)
		}

		logs := obsRecords(t, filepath.Join(dir, "logs.jsonl"))
		spans := obsRecords(t, filepath.Join(dir, "spans.jsonl"))
		span := obsFind(spans, func(record map[string]any) bool { return obsText(record, "name") == "http.server" })
		if span == nil {
			t.Fatalf("http.server span is missing: %s", obsDescribe(spans))
		}
		if code, ok := obsNumber(span, "http.status_code"); !ok || code != 409 {
			t.Errorf("span http.status_code = %v (present=%v)", span["http.status_code"], ok)
		}
		if obsText(span, "span.status") != "error" || obsText(span, "outcome") != "error" {
			t.Errorf("span status/outcome = %q/%q", obsText(span, "span.status"), obsText(span, "outcome"))
		}
		if obsText(span, "error.type") == "" || obsText(span, "error.message") == "" {
			t.Errorf("span has no typed error attributes: %s", obsDescribe([]map[string]any{span}))
		}
		if obsText(span, "http.route") != "/api/admin/forms/{id}" {
			t.Errorf("span http.route = %q", obsText(span, "http.route"))
		}
		logRecord := obsFind(logs, func(record map[string]any) bool { return obsText(record, "message") == "http request completed" })
		if logRecord == nil {
			t.Fatalf("request completion log is missing: %s", obsDescribe(logs))
		}
		if level := obsText(logRecord, "level"); level != "warn" && level != "error" {
			t.Errorf("conflict log level = %q, want warn/error", level)
		}
		if obsText(logRecord, "trace_id") != obsText(span, "trace_id") || obsText(logRecord, "trace_id") == "" {
			t.Errorf("log and span trace IDs differ: log=%q span=%q", obsText(logRecord, "trace_id"), obsText(span, "trace_id"))
		}

		for _, path := range []string{"logs.jsonl", "spans.jsonl", "metrics.jsonl"} {
			body, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"SECRET-BEARER-VALUE", "SECRET-COOKIE-VALUE", "do-not-leak", "form-42?"} {
				if strings.Contains(string(body), secret) {
					t.Errorf("%s leaked %q", path, secret)
				}
			}
		}
	})

	t.Run("OBS-003", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		presentation := obsIdentity(ModuleKindPresentation, "admin-forms", "http-server", "presentation.admin/forms-list/v1")
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/admin/forms", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		request := httptest.NewRequest(http.MethodGet, "/api/admin/forms?token=RAW-QUERY-SECRET&file.id=file-77", nil)
		request.Header.Set("traceparent", "00-"+obsTraceA+"-"+obsSpanA+"-01")
		recorder := httptest.NewRecorder()
		observer.HTTP(presentation, mux).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("business status = %d", recorder.Code)
		}
		requestID, traceID := recorder.Header().Get("X-Request-ID"), recorder.Header().Get("X-Trace-ID")
		if !obsHex16.MatchString(requestID) || !obsHex32.MatchString(traceID) {
			t.Fatalf("invalid correlation headers: X-Request-ID=%q X-Trace-ID=%q", requestID, traceID)
		}
		if traceID != obsTraceA || requestID == obsSpanA {
			t.Errorf("incoming trace was not preserved: trace=%q request=%q", traceID, requestID)
		}

		logs := obsRecords(t, filepath.Join(dir, "logs.jsonl"))
		spans := obsRecords(t, filepath.Join(dir, "spans.jsonl"))
		logRecord := obsFind(logs, func(record map[string]any) bool { return obsText(record, "message") == "http request completed" })
		span := obsFind(spans, func(record map[string]any) bool { return obsText(record, "name") == "http.server" })
		if logRecord == nil || span == nil {
			t.Fatalf("log or span missing: logs=%s spans=%s", obsDescribe(logs), obsDescribe(spans))
		}
		for _, pair := range []struct {
			name   string
			record map[string]any
		}{{"log", logRecord}, {"span", span}} {
			if obsText(pair.record, "trace_id") != traceID || obsText(pair.record, "span_id") != requestID {
				t.Errorf("%s correlation = %q/%q, want %q/%q", pair.name,
					obsText(pair.record, "trace_id"), obsText(pair.record, "span_id"), traceID, requestID)
			}
			obsRequireIdentity(t, pair.record, presentation, pair.name)
			if obsText(pair.record, "http.route") != "/api/admin/forms" {
				t.Errorf("%s http.route = %q", pair.name, obsText(pair.record, "http.route"))
			}
		}
		for _, path := range []string{"logs.jsonl", "spans.jsonl", "metrics.jsonl"} {
			body, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			for _, leak := range []string{"RAW-QUERY-SECRET", "file-77", "token=", "file.id="} {
				if strings.Contains(string(body), leak) {
					t.Errorf("%s leaked %q", path, leak)
				}
			}
		}
	})

	t.Run("OBS-004", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		worker := obsIdentity(ModuleKindWorker, "worker.media", "worker.media", "engine.media/derivative/v1")
		var taskContext context.Context
		err := observer.Operation(context.Background(), worker, "worker.task",
			[]Attribute{String("worker.task.id", "task-1234")}, func(ctx context.Context) error {
				taskContext = ctx
				return nil
			})
		if err != nil {
			t.Fatalf("worker operation failed: %v", err)
		}
		labels := []Attribute{
			String("module.kind", string(worker.ModuleKind)), String("module.id", worker.ModuleID),
			String("component.id", worker.ComponentID), String("operation.id", worker.OperationID),
			String("outcome", "ok"),
		}
		observer.IncCounter(taskContext, "zubrium_worker_task_total", 1, labels...)
		observer.ObserveHistogram(taskContext, "zubrium_worker_task_duration_seconds", 0.125, labels...)

		spans := obsRecords(t, filepath.Join(dir, "spans.jsonl"))
		span := obsFind(spans, func(record map[string]any) bool { return obsText(record, "name") == "worker.task" })
		if span == nil {
			t.Fatalf("worker.task span is missing: %s", obsDescribe(spans))
		}
		if obsText(span, "span.status") != "ok" {
			t.Errorf("worker span status = %q", obsText(span, "span.status"))
		}
		obsRequireIdentity(t, span, worker, "worker span")
		if kind := obsText(span, "module.kind"); kind != "worker" {
			t.Errorf("worker span module.kind = %q", kind)
		}

		metrics := obsRecords(t, filepath.Join(dir, "metrics.jsonl"))
		for _, name := range []string{"zubrium_worker_task_total", "zubrium_worker_task_duration_seconds"} {
			record := obsFind(metrics, func(item map[string]any) bool { return obsText(item, "metric.name") == name })
			if record == nil {
				t.Fatalf("%s sample is missing: %s", name, obsDescribe(metrics))
			}
			obsRequireIdentity(t, record, worker, name)
			if obsText(record, "module.kind") != "worker" || obsText(record, "outcome") != "ok" {
				t.Errorf("%s labels = %q/%q", name, obsText(record, "module.kind"), obsText(record, "outcome"))
			}
			if obsText(record, "trace_id") != obsText(span, "trace_id") {
				t.Errorf("%s exemplar trace does not match the worker span", name)
			}
		}
	})

	t.Run("OBS-005", func(t *testing.T) {
		dir := t.TempDir()
		cfg := obsFileConfig(dir)
		cfg.MaxFileBytes, cfg.MaxFiles = 1024, 2
		observer := obsObserver(t, cfg)
		presentation := obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.http/get/v1")
		bus := obsIdentity(ModuleKindBus, "bus", "dispatcher", "core.example/read/v1")
		if code := obsServe(t, observer, presentation, http.MethodGet, "/api/admin/forms", "/api/admin/forms", http.StatusOK).Code; code != http.StatusOK {
			t.Fatalf("HTTP business status = %d", code)
		}
		busCtx := observer.WithIdentity(context.Background(), bus)
		busCtx, busSpan := observer.Start(busCtx, "bus.dispatch", String("bus.target.module", "core"))
		observer.IncCounter(busCtx, "zubrium_bus_dispatch_total", 1, String("outcome", "ok"))
		busSpan.End(nil)
		httpTrace := obsText(obsFind(obsSignalRecords(t, dir, "logs"), func(record map[string]any) bool {
			return obsText(record, "message") == "http request completed"
		}), "trace_id")
		if httpTrace == "" {
			t.Fatal("HTTP trace id is missing")
		}

		for _, signal := range []struct{ name, kind string }{{"logs", "log"}, {"spans", "span"}, {"metrics", "metric"}} {
			records := obsSignalRecords(t, dir, signal.name)
			if len(records) == 0 {
				t.Fatalf("%s.jsonl is empty", signal.name)
			}
			for _, record := range records {
				obsRequireMandatory(t, record, signal.kind)
			}
			if obsFind(records, func(record map[string]any) bool { return obsText(record, "trace_id") == httpTrace }) == nil {
				t.Errorf("%s records do not carry HTTP trace %s", signal.name, httpTrace)
			}
		}

		for index := 0; index < 24; index++ {
			observer.Log(busCtx, LevelInfo, fmt.Sprintf("rotation fill %02d", index), String("detail", strings.Repeat("x", 96)))
		}
		observer.Log(busCtx, LevelInfo, "rotation-marker-final")
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "logs.jsonl") {
				names = append(names, entry.Name())
			}
		}
		if len(names) > cfg.MaxFiles+1 {
			t.Fatalf("rotation created %d log files (max_files=%d): %v", len(names), cfg.MaxFiles, names)
		}
		if _, err := os.Stat(filepath.Join(dir, "logs.jsonl.1")); err != nil {
			t.Fatalf("first rotated file is missing: %v", err)
		}
		for index := cfg.MaxFiles + 1; index <= cfg.MaxFiles+2; index++ {
			if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("logs.jsonl.%d", index))); !os.IsNotExist(err) {
				t.Errorf("rotation index above max_files: stat error %v", err)
			}
		}
		active := obsRecords(t, filepath.Join(dir, "logs.jsonl"))
		if obsFind(active, func(record map[string]any) bool { return obsText(record, "message") == "rotation-marker-final" }) == nil {
			t.Fatalf("active file lost the last record after rotation: %s", obsDescribe(active))
		}
	})

	t.Run("OBS-006", func(t *testing.T) {
		endpoint := os.Getenv("OBS_TEST_OTEL_ENDPOINT")
		searchURL := os.Getenv("OBS_TEST_OQ_SEARCH_URL")
		auth := os.Getenv("OBS_TEST_OQ_AUTH")
		if endpoint == "" || searchURL == "" {
			t.Skip("blocked: no live OTLP Collector/OpenObserve stack: set OBS_TEST_OTEL_ENDPOINT and OBS_TEST_OQ_SEARCH_URL to verify three exported signal streams")
		}
		if auth == "" && !strings.Contains(searchURL, "%s") {
			t.Fatal("OBS_TEST_OQ_SEARCH_URL must be a query URL template with a trace id placeholder unless OBS_TEST_OQ_AUTH selects OpenObserve")
		}
		traceID := obsTraceB
		cfg := Config{
			Mode: ModeOTLP, Endpoint: endpoint, Insecure: true,
			Timeout: 2 * time.Second, MetricFlush: 250 * time.Millisecond,
			ServiceName: "zubrium", ServiceVersion: "1.2.3", InstanceID: "obs-instance-1",
			Profile: "full",
		}
		observer, err := New(cfg)
		if err != nil {
			t.Fatalf("New(otel): %v", err)
		}
		identity := obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.http/get/v1")
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/admin/forms", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		request := httptest.NewRequest(http.MethodGet, "/api/admin/forms", nil)
		request.Header.Set("traceparent", "00-"+traceID+"-"+obsSpanB+"-01")
		recorder := httptest.NewRecorder()
		observer.HTTP(identity, mux).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Header().Get("X-Trace-ID") != traceID {
			t.Fatalf("HTTP business result changed: status=%d trace=%q", recorder.Code, recorder.Header().Get("X-Trace-ID"))
		}
		traceCtx := withTrace(context.Background(), TraceContext{TraceID: traceID, SpanID: obsSpanB, Sampled: true})
		worker := obsIdentity(ModuleKindWorker, "worker.media", "worker.media", "engine.media/derivative/v1")
		if err := observer.Operation(traceCtx, worker, "worker.task", nil, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		busCtx := observer.WithIdentity(traceCtx, obsIdentity(ModuleKindBus, "bus", "dispatcher", "core.example/read/v1"))
		busCtx, busSpan := observer.Start(busCtx, "bus.dispatch")
		observer.IncCounter(busCtx, "zubrium_bus_dispatch_total", 1, String("outcome", "ok"))
		busSpan.End(nil)
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := observer.Close(closeCtx); err != nil {
			t.Fatalf("Close(otel): %v", err)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		requirements := map[string][]string{
			"logs":    {"http request completed", "service.name", "deployment.profile", "module.kind"},
			"traces":  {"http.server", "worker.task", "bus.dispatch", "service.name", "module.kind"},
			"metrics": {"zubrium_http_server_requests_total", "zubrium_bus_dispatch_total", "deployment.profile"},
		}
		for signal, markers := range requirements {
			body, err := obsSearchBody(client, searchURL, auth, signal, traceID, markers...)
			if err != nil {
				t.Fatalf("%s query: %v", signal, err)
			}
			for _, marker := range append([]string{traceID}, markers...) {
				if !obsContainsMarker(body, marker) {
					t.Errorf("%s stream does not contain %q: %.512s", signal, marker, body)
				}
			}
		}
	})

	t.Run("OBS-007", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		presentation := obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.http/get/v1")
		cases := []struct {
			name, header, wantTrace string
			valid                   bool
		}{
			{"short trace id", "00-abcdef-" + obsSpanA + "-01", "", false},
			{"non-hex trace id", "00-" + strings.Repeat("z", 32) + "-" + obsSpanA + "-01", "", false},
			{"zero span id", "00-" + obsTraceA + "-" + strings.Repeat("0", 16) + "-01", "", false},
			{"valid parent", "00-" + obsTraceB + "-" + obsSpanB + "-01", obsTraceB, true},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, "/api/admin/forms", nil)
				request.Header.Set("traceparent", testCase.header)
				recorder := httptest.NewRecorder()
				mux := http.NewServeMux()
				mux.HandleFunc("GET /api/admin/forms", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
				recorder = httptest.NewRecorder()
				observer.HTTP(presentation, mux).ServeHTTP(recorder, request)
				if recorder.Code != http.StatusOK {
					t.Fatalf("invalid traceparent changed business status: %d", recorder.Code)
				}
				traceID, requestID := recorder.Header().Get("X-Trace-ID"), recorder.Header().Get("X-Request-ID")
				if !obsHex32.MatchString(traceID) || !obsHex16.MatchString(requestID) {
					t.Fatalf("new root context is not W3C-shaped: %q/%q", traceID, requestID)
				}
				if testCase.valid {
					if traceID != testCase.wantTrace || requestID == obsSpanB {
						t.Errorf("valid parent trace was not inherited: %q/%q", traceID, requestID)
					}
				} else if traceID == strings.Repeat("0", 32) {
					t.Errorf("new root trace must not be zero: %q", traceID)
				}
				spans := obsRecords(t, filepath.Join(dir, "spans.jsonl"))
				span := obsFind(spans, func(record map[string]any) bool {
					return obsText(record, "name") == "http.server" && obsText(record, "trace_id") == traceID && obsText(record, "span_id") == requestID
				})
				if span == nil {
					t.Fatalf("child span for %q is missing: %s", traceID, obsDescribe(spans))
				}
				if testCase.valid && obsText(span, "parent_span_id") != obsSpanB {
					t.Errorf("parent_span_id = %q, want %q", obsText(span, "parent_span_id"), obsSpanB)
				}
				logs := obsRecords(t, filepath.Join(dir, "logs.jsonl"))
				if obsFind(logs, func(record map[string]any) bool {
					return obsText(record, "trace_id") == traceID && obsText(record, "level") == "error"
				}) != nil {
					t.Errorf("invalid traceparent produced a tracing error log: %s", obsDescribe(logs))
				}
			})
		}
	})

	t.Run("OBS-008", func(t *testing.T) {
		live := os.Getenv("OBS_TEST_OTEL_ENDPOINT")
		failed := os.Getenv("OBS_TEST_OTEL_FAILURE_ENDPOINT")
		searchURL := os.Getenv("OBS_TEST_OQ_SEARCH_URL")
		auth := os.Getenv("OBS_TEST_OQ_AUTH")
		if live == "" || failed == "" || searchURL == "" {
			t.Skip("blocked: no disposable stack with a running Collector/OpenObserve and a controllable Collector outage: set OBS_TEST_OTEL_ENDPOINT, OBS_TEST_OTEL_FAILURE_ENDPOINT and OBS_TEST_OQ_SEARCH_URL")
		}
		if auth == "" && !strings.Contains(searchURL, "%s") {
			t.Fatal("OBS_TEST_OQ_SEARCH_URL must be a query URL template with a trace id placeholder unless OBS_TEST_OQ_AUTH selects OpenObserve")
		}
		run := func(endpoint, traceID string) (int, error, time.Duration) {
			started := time.Now()
			observer, err := New(Config{
				Mode: ModeOTLP, Endpoint: endpoint, Insecure: true, Timeout: 200 * time.Millisecond,
				MetricFlush: time.Hour, ServiceName: "zubrium", ServiceVersion: "1.2.3",
				InstanceID: "obs-instance-1", Profile: "full",
			})
			if err != nil {
				return 0, err, time.Since(started)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/admin/forms", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			request := httptest.NewRequest(http.MethodGet, "/api/admin/forms", nil)
			request.Header.Set("traceparent", "00-"+traceID+"-"+obsSpanA+"-01")
			recorder := httptest.NewRecorder()
			observer.HTTP(obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.http/get/v1"), mux).ServeHTTP(recorder, request)
			worker := obsIdentity(ModuleKindWorker, "worker.media", "worker.media", "engine.media/derivative/v1")
			if err := observer.Operation(context.Background(), worker, "worker.task", nil, func(context.Context) error { return nil }); err != nil {
				return recorder.Code, err, time.Since(started)
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// A failed telemetry flush during an outage is an honest limitation;
			// only the business result and the bounded shutdown are contractual.
			return recorder.Code, observer.Close(closeCtx), time.Since(started)
		}
		outageTrace := obsTraceA
		status, closeErr, elapsed := run(failed, outageTrace)
		if status != http.StatusOK {
			t.Fatalf("telemetry outage changed the business result: status=%d", status)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("unreachable Collector blocked the request for %s", elapsed)
		}
		if closeErr == nil {
			t.Log("unreachable Collector: telemetry flush completed without error")
		} else {
			t.Logf("unreachable Collector: telemetry flush failed as expected: %v", closeErr)
		}
		recoveryTrace := obsTraceC
		status, closeErr, elapsed = run(live, recoveryTrace)
		if status != http.StatusOK || closeErr != nil || elapsed > 3*time.Second {
			t.Fatalf("export did not resume after outage recovery: status=%d close=%v elapsed=%s", status, closeErr, elapsed)
		}
		body, err := obsSearchBody(&http.Client{Timeout: 30 * time.Second}, searchURL, auth, "traces", recoveryTrace, recoveryTrace)
		if err != nil {
			t.Fatalf("recovery query: %v", err)
		}
		if !obsContainsMarker(body, recoveryTrace) {
			t.Fatalf("recovered trace is not in telemetry sink: body=%.512s", body)
		}
	})

	t.Run("OBS-009", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		ctx := observer.WithIdentity(context.Background(), obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.http/get/v1"))
		blockedKeys := []string{
			"token", "cookie", "authorization", "set-cookie", "body", "payload", "values",
			"query", "form", "response", "user.token", "request-body", "api_key", "privateKey",
			"cache.key", "cache_key", "file.id", "principal.id", "url", "uri", "locale",
			"user.agent", "user.email", "username", "phone",
		}
		for _, key := range blockedKeys {
			if got := String(key, "safe-looking value"); got.Key != "" {
				t.Errorf("sensitive key %q was accepted: %#v", key, got)
			}
			if got := Int(key, 7); got.Key != "" {
				t.Errorf("sensitive integer key %q was accepted: %#v", key, got)
			}
		}
		secretValues := []string{
			"Authorization: Bearer SECRET-BEARER-VALUE",
			"Basic SECRET-BASIC-VALUE",
			"Cookie: session=SECRET-COOKIE-VALUE; theme=dark",
			"token=SECRET-TOKEN-VALUE",
			"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.SECRETJWTVALUE",
			"PII user@example.test",
		}
		for _, value := range secretValues {
			attribute := String("detail", value)
			if attribute.Key != "detail" {
				t.Fatalf("safe key was rejected: %q", value)
			}
			if strings.Contains(attribute.Text, "SECRET") || strings.Contains(attribute.Text, "user@example.test") {
				t.Errorf("credential or PII leaked under a safe key: %q -> %q", value, attribute.Text)
			}
		}
		observer.Log(ctx, LevelInfo, "redaction scenario", String("detail", "safe value"),
			String("note", "Bearer SECRET-BEARER-VALUE user@example.test"),
			String("long", strings.Repeat("x", 300)+"\n\x00tail"))
		observer.IncCounter(ctx, "zubrium_obs_test_total", 1, String("detail", "token=SECRET-TOKEN-VALUE user@example.test"))
		_, span := observer.Start(ctx, "component.lifecycle", String("note", "Cookie: session=SECRET-COOKIE-VALUE"))
		span.End(nil)

		for _, path := range []string{"logs.jsonl", "spans.jsonl", "metrics.jsonl"} {
			body, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, leak := range []string{
				"SECRET-BEARER-VALUE", "SECRET-BASIC-VALUE", "SECRET-COOKIE-VALUE",
				"SECRET-TOKEN-VALUE", "SECRETJWTVALUE", "user@example.test",
				"user.token", "request-body", "cache.key", "principal.id", "user.agent", "user.email",
			} {
				if strings.Contains(text, leak) {
					t.Errorf("%s leaked %q: %s", path, leak, text)
				}
			}
			for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
				if strings.ContainsAny(line, "\n\x00") {
					t.Errorf("%s contains a control character: %q", path, line)
				}
			}
		}
		_, _, snapshot := obsMetricsHTTP(t, observer)
		for _, leak := range []string{"SECRET-TOKEN-VALUE", "user@example.test"} {
			if strings.Contains(snapshot, leak) {
				t.Errorf("/metrics leaked %q: %s", leak, snapshot)
			}
		}
	})

	t.Run("OBS-010", func(t *testing.T) {
		dir := t.TempDir()
		observer := obsObserver(t, obsFileConfig(dir))
		presentation := obsIdentity(ModuleKindPresentation, "admin", "http-server", "presentation.http/get/v1")
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/admin/forms", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		handler := observer.HTTP(presentation, mux)
		const requests = 12
		traceIDs := make([]string, requests)
		requestIDs := make([]string, requests)
		statuses := make([]int, requests)
		var wait sync.WaitGroup
		for index := 0; index < requests; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				traceID := fmt.Sprintf("%032x", index+1)
				spanID := fmt.Sprintf("%016x", index+1)
				request := httptest.NewRequest(http.MethodGet, "/api/admin/forms", nil)
				request.Header.Set("traceparent", "00-"+traceID+"-"+spanID+"-01")
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				traceIDs[index], requestIDs[index] = recorder.Header().Get("X-Trace-ID"), recorder.Header().Get("X-Request-ID")
				statuses[index] = recorder.Code
			}(index)
		}
		wait.Wait()
		for index := 0; index < requests; index++ {
			if statuses[index] != http.StatusOK || traceIDs[index] != fmt.Sprintf("%032x", index+1) || requestIDs[index] == "" {
				t.Fatalf("request %d: status=%d trace=%q request=%q", index, statuses[index], traceIDs[index], requestIDs[index])
			}
		}
		spans := obsRecords(t, filepath.Join(dir, "spans.jsonl"))
		for index, traceID := range traceIDs {
			matches := 0
			for _, record := range spans {
				if obsText(record, "name") == "http.server" && obsText(record, "trace_id") == traceID {
					matches++
					if obsText(record, "span_id") != requestIDs[index] {
						t.Errorf("trace %s mixed span ids: %q != %q", traceID, obsText(record, "span_id"), requestIDs[index])
					}
					obsRequireIdentity(t, record, presentation, "concurrent span")
				}
			}
			if matches != 1 {
				t.Errorf("trace %s has %d http.server spans, want exactly 1", traceID, matches)
			}
		}

		_, duplicateSpan := observer.Start(context.Background(), "component.lifecycle")
		duplicateTrace := duplicateSpan.Trace().TraceID
		duplicateSpan.End(nil)
		duplicateSpan.End(errors.New("second end must be ignored"))
		spans = obsRecords(t, filepath.Join(dir, "spans.jsonl"))
		duplicates := 0
		for _, record := range spans {
			if obsText(record, "name") == "component.lifecycle" && obsText(record, "trace_id") == duplicateTrace {
				duplicates++
			}
		}
		if duplicates != 1 {
			t.Errorf("second End produced duplicate spans: %d", duplicates)
		}

		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		started := time.Now()
		if err := observer.Close(closeCtx); err != nil {
			t.Fatalf("graceful Close: %v", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Errorf("Close exceeded shutdown timeout: %s", elapsed)
		}
		spansBefore := len(obsRecords(t, filepath.Join(dir, "spans.jsonl")))
		metricsBefore := len(obsRecords(t, filepath.Join(dir, "metrics.jsonl")))
		duplicateSpan.End(nil)
		observer.IncCounter(context.Background(), "zubrium_obs_test_total", 1)
		if got := len(obsRecords(t, filepath.Join(dir, "spans.jsonl"))); got != spansBefore {
			t.Errorf("spans were emitted after Close: %d -> %d", spansBefore, got)
		}
		if got := len(obsRecords(t, filepath.Join(dir, "metrics.jsonl"))); got != metricsBefore {
			t.Errorf("metric samples were duplicated after Close: %d -> %d", metricsBefore, got)
		}
	})
}

func obsFindMetricLine(lines []string, substring, suffix string) string {
	for _, line := range lines {
		if strings.Contains(line, substring) && strings.HasSuffix(line, suffix) {
			return line
		}
	}
	return ""
}
