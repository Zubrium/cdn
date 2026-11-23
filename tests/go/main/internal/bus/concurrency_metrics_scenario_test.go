package bus_test

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/presentation/admin/base/transport"
)

type metricsPingRequest struct{ Value string }
type metricsPingResponse struct{ Value string }

func metricsObserver(t *testing.T) *observability.Observer {
	t.Helper()
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	return observer
}

func metricsSnapshot(t *testing.T, observer *observability.Observer) (*httptest.ResponseRecorder, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	return recorder, recorder.Body.String()
}

func metricsLabelValue(line, key string) string {
	open := strings.IndexByte(line, '{')
	if open < 0 {
		return ""
	}
	close := strings.IndexByte(line[open:], '}')
	if close < 0 {
		return ""
	}
	for _, part := range strings.Split(line[open+1:open+close], ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && name == key {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

func metricSeries(body, name string) []string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+"{") || strings.HasPrefix(line, name+" ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestConcurrencyLimitsScenario(t *testing.T) {
	t.Run("CONC-010", func(t *testing.T) {
		observer := metricsObserver(t)
		if _, err := transport.NewPublicServer(transport.PublicConfig{
			Server: transport.ServerConfig{
				Address: "127.0.0.1:0", MaxInFlight: 2,
				RequestTimeout: time.Second, ShutdownTimeout: 2 * time.Second,
			},
			Observer: observer,
		}, &routerenginepresentation.APIAdapter{}, func() bool { return true }); err != nil {
			t.Fatalf("NewPublicServer: %v", err)
		}

		operation := contracts.Operation[metricsPingRequest, metricsPingResponse]{
			Key: contracts.Key{ID: "core.metrics.ping", Version: 1},
		}
		dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 1}, operation.Key)
		if err != nil {
			t.Fatal(err)
		}
		dispatcher.WithObserver(observer)
		if err := bus.Register(dispatcher, operation, func(context.Context, metricsPingRequest) (metricsPingResponse, error) {
			return metricsPingResponse{}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Seal(); err != nil {
			t.Fatal(err)
		}
		if _, err := contracts.Call(context.Background(), dispatcher, operation, metricsPingRequest{}); err != nil {
			t.Fatal(err)
		}

		recorder, body := metricsSnapshot(t, observer)
		if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("/metrics Cache-Control = %q, want no-store", got)
		}
		if got := recorder.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
			t.Fatalf("/metrics Content-Type = %q", got)
		}

		for _, name := range []string{
			"zubrium_concurrency_in_flight",
			"zubrium_concurrency_rejected_total",
			"zubrium_cache_coalesced_total",
			"zubrium_cache_invalidation_batch_total",
			"zubrium_worker_pool_active",
			"zubrium_worker_pool_tasks_total",
			"zubrium_db_pool_usage",
		} {
			if len(metricSeries(body, name)) == 0 {
				t.Errorf("metric %s is absent from /metrics", name)
			}
		}

		scopes := map[string]bool{}
		for _, line := range metricSeries(body, "zubrium_concurrency_in_flight") {
			scopes[metricsLabelValue(line, "scope")] = true
		}
		for _, scope := range []string{"HTTP", "BUS", "STORAGE", "LOADER"} {
			if !scopes[scope] {
				t.Errorf("zubrium_concurrency_in_flight has no scope=%s series", scope)
			}
		}

		rejections := map[string]bool{}
		for _, line := range metricSeries(body, "zubrium_concurrency_rejected_total") {
			rejections[metricsLabelValue(line, "scope")+"/"+metricsLabelValue(line, "reason")] = true
		}
		for _, scope := range []string{"HTTP", "BUS", "STORAGE", "LOADER"} {
			for _, reason := range []string{"GLOBAL", "PER_ROUTE", "PER_OPERATION", "DB_RESERVE"} {
				if !rejections[scope+"/"+reason] {
					t.Errorf("zubrium_concurrency_rejected_total has no %s/%s series", scope, reason)
				}
			}
		}

		results := map[string]bool{}
		for _, line := range metricSeries(body, "zubrium_cache_coalesced_total") {
			results[metricsLabelValue(line, "result")] = true
		}
		for _, result := range []string{"JOINED", "LEADER", "FAILED"} {
			if !results[result] {
				t.Errorf("zubrium_cache_coalesced_total has no result=%s series", result)
			}
		}

		batches := map[string]bool{}
		for _, line := range metricSeries(body, "zubrium_cache_invalidation_batch_total") {
			batches[metricsLabelValue(line, "result")] = true
		}
		for _, result := range []string{"APPLIED", "OVERFLOW", "CLEARED"} {
			if !batches[result] {
				t.Errorf("zubrium_cache_invalidation_batch_total has no result=%s series", result)
			}
		}

		pools := map[string]bool{}
		for _, line := range metricSeries(body, "zubrium_worker_pool_active") {
			pools[metricsLabelValue(line, "pool")] = true
		}
		for _, pool := range []string{"ARCHIVE", "MEDIA", "PRERENDER", "PUBLICATION"} {
			if !pools[pool] {
				t.Errorf("zubrium_worker_pool_active has no pool=%s series", pool)
			}
		}

		tasks := map[string]bool{}
		for _, line := range metricSeries(body, "zubrium_worker_pool_tasks_total") {
			tasks[metricsLabelValue(line, "pool")+"/"+metricsLabelValue(line, "outcome")] = true
		}
		for _, pool := range []string{"ARCHIVE", "MEDIA", "PRERENDER", "PUBLICATION"} {
			for _, outcome := range []string{"OK", "ERROR"} {
				if !tasks[pool+"/"+outcome] {
					t.Errorf("zubrium_worker_pool_tasks_total has no %s/%s series", pool, outcome)
				}
			}
		}

		busInFlight := metricSeries(body, "zubrium_concurrency_in_flight")
		busSeen := false
		for _, line := range busInFlight {
			if metricsLabelValue(line, "scope") != "BUS" {
				continue
			}
			busSeen = true
			fields := strings.Fields(line)
			value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				t.Fatal(err)
			}
			if value != 0 {
				t.Errorf("BUS in-flight after dispatch = %v, want 0: %s", value, line)
			}
		}
		if !busSeen {
			t.Error("canonical BUS in-flight gauge is absent")
		}

		// SPEC-concurrency.md §12 fixes the complete label set of every
		// contract series. Inherited identity labels and listener/pool
		// identifiers are forbidden, even when their values are low-cardinality.
		expectedLabels := map[string]map[string]bool{
			"zubrium_concurrency_in_flight":          {"scope": true},
			"zubrium_bus_in_flight":                  {"scope": true},
			"zubrium_concurrency_rejected_total":     {"scope": true, "reason": true},
			"zubrium_cache_coalesced_total":          {"cache": true, "result": true},
			"zubrium_cache_invalidation_batch_total": {"result": true},
			"zubrium_worker_pool_active":             {"pool": true},
			"zubrium_worker_pool_tasks_total":        {"pool": true, "outcome": true},
			"zubrium_db_pool_usage":                  {},
		}
		forbiddenValues := regexp.MustCompile(`(?i)(route[._-]?id|operation[._-]?id="[^"]|file[._-]?id|principal|/api/|https?://|locale|user[._-]?agent|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
		for _, line := range strings.Split(body, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			name := strings.SplitN(fields[0], "{", 2)[0]
			expected, ok := expectedLabels[name]
			if !ok {
				continue
			}
			actual := map[string]bool{}
			if open := strings.IndexByte(line, '{'); open >= 0 {
				if close := strings.IndexByte(line[open:], '}'); close >= 0 {
					for _, part := range strings.Split(line[open+1:open+close], ",") {
						label, _, found := strings.Cut(strings.TrimSpace(part), "=")
						if !found {
							t.Errorf("metric %q has a malformed label", line)
							continue
						}
						actual[label] = true
					}
				}
			}
			if len(actual) != len(expected) {
				t.Errorf("metric %q has labels %v, want exactly %v", line, actual, expected)
			}
			for label := range actual {
				if !expected[label] {
					t.Errorf("metric %q has unsupported label %q", line, label)
				}
			}
			for label := range expected {
				if !actual[label] {
					t.Errorf("metric %q is missing label %q", line, label)
				}
			}
			if forbiddenValues.MatchString(line) {
				t.Errorf("metric line contains high-cardinality value: %s", line)
			}
		}
	})
}
