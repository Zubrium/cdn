package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zubrium/cdn/pkg/observability"
	fileapi "github.com/Zubrium/cdn/presentation/fileapi"
)

type admissionReadiness struct{ ready bool }

func (r *admissionReadiness) Ready() bool { return r.ready }

type admissionLoader struct{ fileapi.Loader }

func admissionObserver(t *testing.T) *observability.Observer {
	t.Helper()
	observer, err := observability.New(observability.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	return observer
}

// admissionMetricLine requires an exact label set. The concurrency contract
// publishes zero-valued series for several scopes, so matching a single label
// could read an unrelated BUS or STORAGE series instead of the HTTP series.
func admissionMetricLine(t *testing.T, observer *observability.Observer, name, labels string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	required := labelSet(labels)
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, name+"{") {
			continue
		}
		open := strings.Index(line, "{")
		closeBrace := strings.Index(line, "}")
		if open < 0 || closeBrace < open {
			continue
		}
		if !sameLabels(labelSet(line[open+1:closeBrace]), required) {
			continue
		}
		return line
	}
	return ""
}

func labelSet(raw string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, label := range strings.Split(raw, ",") {
		if label = strings.TrimSpace(label); label != "" {
			result[label] = struct{}{}
		}
	}
	return result
}

func sameLabels(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for label := range a {
		if _, ok := b[label]; !ok {
			return false
		}
	}
	return true
}

func admissionMetricValue(t *testing.T, observer *observability.Observer, name, label string) float64 {
	t.Helper()
	line := admissionMetricLine(t, observer, name, label)
	if line == "" {
		t.Fatalf("metric %s with %q is absent", name, label)
	}
	fields := strings.Fields(line)
	value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	if err != nil {
		t.Fatalf("metric line %q: %v", line, err)
	}
	return value
}

func admissionServerConfig(observer *observability.Observer, ui http.Handler) AdminConfig {
	return AdminConfig{
		Server: ServerConfig{
			Address: "127.0.0.1:0", MaxInFlight: 1,
			RequestTimeout: 100 * time.Millisecond, ShutdownTimeout: 500 * time.Millisecond,
		},
		FileAPI: fileapi.Config{
			MaxFileBytes: 1 << 20, MaxPartBytes: 1 << 20, MaxUploadParts: 1,
			MaxJSONBytes: 1 << 20, MaxAttributeBytes: 1 << 10, MaxGroupsPerFile: 1,
			DefaultPageSize: 1, MaxPageSize: 10,
		},
		AllowedOrigins: []string{"http://localhost:5173"},
		MaxJSONBytes:   1 << 20, DefaultPageSize: 1, MaxPageSize: 10,
		UIHandler: ui, Observer: observer,
	}
}

func TestConcurrencyAdmissionScenario(t *testing.T) {
	t.Run("CONC-001", func(t *testing.T) {
		for _, moduleID := range []string{"admin", "public-router"} {
			t.Run(moduleID, func(t *testing.T) {
				observer := admissionObserver(t)
				limiter, err := newIngressLimiter(ServerConfig{
					MaxInFlight: 1, RequestTimeout: 20 * time.Millisecond, ShutdownTimeout: 100 * time.Millisecond,
				}, observer, moduleID)
				if err != nil {
					t.Fatal(err)
				}
				started := make(chan struct{})
				var closeStarted sync.Once
				var calls atomic.Int64
				var handlerErr atomic.Value
				handler := limiter.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					calls.Add(1)
					closeStarted.Do(func() { close(started) })
					<-r.Context().Done()
					handlerErr.Store(r.Context().Err())
					w.WriteHeader(http.StatusNoContent)
				}))
				firstDone := make(chan struct{})
				go func() {
					defer close(firstDone)
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/admin/model", nil))
				}()
				<-started

				rejectedRecorder := httptest.NewRecorder()
				handler.ServeHTTP(rejectedRecorder, httptest.NewRequest("GET", "/api/admin/model", nil))
				if rejectedRecorder.Code != http.StatusServiceUnavailable {
					t.Fatalf("overload status = %d, want 503", rejectedRecorder.Code)
				}
				if got := rejectedRecorder.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("overload Cache-Control = %q, want no-store", got)
				}
				if !strings.Contains(rejectedRecorder.Body.String(), `"overloaded"`) {
					t.Fatalf("overload body = %q, want standard error form", rejectedRecorder.Body.String())
				}
				if calls.Load() != 1 {
					t.Fatalf("handler calls under overload = %d, want 1", calls.Load())
				}

				for _, path := range []string{"/healthz", "/readyz"} {
					exemptRecorder := httptest.NewRecorder()
					handler.ServeHTTP(exemptRecorder, httptest.NewRequest("GET", path, nil))
					if exemptRecorder.Code != http.StatusNoContent {
						t.Fatalf("GET %s under overload = %d, want 204", path, exemptRecorder.Code)
					}
				}

				<-firstDone
				if err, ok := handlerErr.Load().(error); !ok || err == nil {
					t.Fatalf("admitted request context error = %v, want request_timeout", err)
				}
				if got := admissionMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="HTTP"`); got != 0 {
					t.Fatalf("HTTP in-flight after release = %v, want 0", got)
				}
				if got := admissionMetricValue(t, observer, "zubrium_concurrency_rejected_total", `scope="HTTP",reason="GLOBAL"`); got != 1 {
					t.Fatalf("rejected{GLOBAL} = %v, want 1", got)
				}
			})
		}

		for name, config := range map[string]ServerConfig{
			"missing max_in_flight":         {Address: "127.0.0.1:0", RequestTimeout: time.Second, ShutdownTimeout: 2 * time.Second},
			"missing request_timeout":       {Address: "127.0.0.1:0", MaxInFlight: 1, ShutdownTimeout: 2 * time.Second},
			"shutdown shorter than request": {Address: "127.0.0.1:0", MaxInFlight: 1, RequestTimeout: 2 * time.Second, ShutdownTimeout: time.Second},
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := NewAdminServer(AdminConfig{Server: config}, AdminDependencies{
					CoreReady: func() bool { return true }, Files: &admissionLoader{},
				}); err == nil {
					t.Fatal("NewAdminServer accepted an incomplete concurrency configuration")
				}
			})
		}
	})

	t.Run("CONC-011", func(t *testing.T) {
		observer := admissionObserver(t)
		started := make(chan struct{})
		var calls atomic.Int64
		ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			close(started)
			<-r.Context().Done()
			w.WriteHeader(http.StatusNoContent)
		})
		server, err := NewAdminServer(admissionServerConfig(observer, ui), AdminDependencies{
			CoreReady: func() bool { return true }, Files: &admissionLoader{},
		})
		if err != nil {
			t.Fatal(err)
		}
		readiness := &admissionReadiness{ready: true}
		server.SetApplicationReadiness(readiness)
		if err := server.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		firstDone := make(chan error, 1)
		go func() {
			response, err := client.Get("http://" + server.Address() + "/admin")
			if response != nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			firstDone <- err
		}()
		<-started
		response, err := client.Get("http://" + server.Address() + "/admin")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("overload status = %d, want 503", response.StatusCode)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("overload Cache-Control = %q, want no-store", response.Header.Get("Cache-Control"))
		}
		if calls.Load() != 1 {
			t.Fatalf("handler calls = %d, want 1", calls.Load())
		}

		stopCtx, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelStop()
		if err := server.Stop(stopCtx); err != nil {
			t.Fatalf("Stop under load = %v", err)
		}
		if err := <-firstDone; err != nil {
			t.Fatalf("admitted request failed: %v", err)
		}
		if err := server.Stop(context.Background()); err != nil {
			t.Fatalf("repeated Stop = %v", err)
		}
		if got := admissionMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="HTTP"`); got != 0 {
			t.Fatalf("HTTP in-flight after shutdown = %v, want 0", got)
		}
		if got := admissionMetricValue(t, observer, "zubrium_concurrency_rejected_total", `scope="HTTP",reason="GLOBAL"`); got != 1 {
			t.Fatalf("rejected after repeated Stop = %v, want 1", got)
		}
	})
}
