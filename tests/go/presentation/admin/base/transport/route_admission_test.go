package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	routerenginepresentation "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/pkg/observability"
)

func routeAdmissionConfig(observer *observability.Observer, perRoute PerRouteAdmission) PublicConfig {
	return PublicConfig{
		Server: ServerConfig{
			Address: "127.0.0.1:0", MaxInFlight: 2,
			RequestTimeout: 100 * time.Millisecond, ShutdownTimeout: 500 * time.Millisecond,
		},
		Observer: observer,
		PerRoute: perRoute,
	}
}

func routeAdmissionServer(t *testing.T, observer *observability.Observer, perRoute PerRouteAdmission) *PublicServer {
	t.Helper()
	server, err := NewPublicServer(routeAdmissionConfig(observer, perRoute), &routerenginepresentation.APIAdapter{}, func() bool { return true })
	if err != nil {
		t.Fatalf("NewPublicServer: %v", err)
	}
	return server
}

type routeAdmissionCall struct {
	status int
	done   chan struct{}
}

func routeAdmissionStart(t *testing.T, handler http.Handler, path string, headers map[string]string, started chan struct{}, once *sync.Once) *routeAdmissionCall {
	t.Helper()
	result := &routeAdmissionCall{done: make(chan struct{})}
	signalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		handler.ServeHTTP(w, r)
	})
	go func() {
		defer close(result.done)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		signalled.ServeHTTP(recorder, request)
		result.status = recorder.Code
		once.Do(func() { close(started) })
	}()
	<-started
	return result
}

func routeAdmissionRejections(t *testing.T, observer *observability.Observer) float64 {
	t.Helper()
	recorder := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, `zubrium_concurrency_rejected_total{`) ||
			!strings.Contains(line, `scope="HTTP"`) || !strings.Contains(line, `reason="PER_ROUTE"`) {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	t.Fatal("HTTP PER_ROUTE rejection series is absent")
	return 0
}

func routeAdmissionFinish(t *testing.T, call *routeAdmissionCall) int {
	t.Helper()
	select {
	case <-call.done:
		return call.status
	case <-time.After(2 * time.Second):
		t.Fatal("admitted request did not finish")
		return 0
	}
}

func TestConcurrencyLimitsScenario(t *testing.T) {
	t.Run("CONC-002", func(t *testing.T) {
		t.Run("override_above_global_rejected_before_start", func(t *testing.T) {
			observer := admissionObserver(t)
			_, err := NewPublicServer(routeAdmissionConfig(observer, PerRouteAdmission{
				Enabled:            true,
				DefaultMaxInFlight: 2,
				Overrides: []RouteAdmissionOverride{
					{ID: "route-a", Path: "/static", MaxInFlight: 3},
				},
			}), &routerenginepresentation.APIAdapter{}, func() bool { return true })
			if !errors.Is(err, ErrInvalidRouteAdmission) {
				t.Fatalf("override above global = %v, want ErrInvalidRouteAdmission", err)
			}
		})

		t.Run("default_above_global_rejected_before_start", func(t *testing.T) {
			observer := admissionObserver(t)
			_, err := NewPublicServer(routeAdmissionConfig(observer, PerRouteAdmission{
				Enabled:            true,
				DefaultMaxInFlight: 3,
			}), &routerenginepresentation.APIAdapter{}, func() bool { return true })
			if !errors.Is(err, ErrInvalidRouteAdmission) {
				t.Fatalf("default above global = %v, want ErrInvalidRouteAdmission", err)
			}
		})

		t.Run("unknown_or_duplicate_static_key_rejected", func(t *testing.T) {
			observer := admissionObserver(t)
			for name, perRoute := range map[string]PerRouteAdmission{
				"empty id":   {Enabled: true, DefaultMaxInFlight: 2, Overrides: []RouteAdmissionOverride{{Path: "/static", MaxInFlight: 1}}},
				"empty path": {Enabled: true, DefaultMaxInFlight: 2, Overrides: []RouteAdmissionOverride{{ID: "route-a", MaxInFlight: 1}}},
				"duplicate id": {Enabled: true, DefaultMaxInFlight: 2, Overrides: []RouteAdmissionOverride{
					{ID: "route-a", Path: "/static", MaxInFlight: 1}, {ID: "route-a", Path: "/other", MaxInFlight: 1},
				}},
			} {
				t.Run(name, func(t *testing.T) {
					_, err := NewPublicServer(routeAdmissionConfig(observer, perRoute), &routerenginepresentation.APIAdapter{}, func() bool { return true })
					if !errors.Is(err, ErrInvalidRouteAdmission) {
						t.Fatalf("static route config = %v, want ErrInvalidRouteAdmission", err)
					}
				})
			}
		})

		t.Run("valid_override_narrows_only_configured_path", func(t *testing.T) {
			observer := admissionObserver(t)
			server := routeAdmissionServer(t, observer, PerRouteAdmission{
				Enabled: true, DefaultMaxInFlight: 2,
				Overrides: []RouteAdmissionOverride{{ID: "route-a", Path: "/static", MaxInFlight: 1}},
			})
			release := make(chan struct{})
			var started sync.Once
			firstStarted := make(chan struct{})
			var admitted atomic.Int64
			handler := server.ingress.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				admitted.Add(1)
				if r.URL.Path == "/static" {
					<-release
				}
				w.WriteHeader(http.StatusNoContent)
			}))

			first := routeAdmissionStart(t, handler, "/static", nil, firstStarted, &started)
			rejectedStarted := make(chan struct{})
			var rejectedOnce sync.Once
			second := routeAdmissionStart(t, handler, "/static", nil, rejectedStarted, &rejectedOnce)
			if got := routeAdmissionFinish(t, second); got != http.StatusServiceUnavailable {
				t.Fatalf("second request to overridden route = %d, want 503", got)
			}
			otherStarted := make(chan struct{})
			var otherOnce sync.Once
			other := routeAdmissionStart(t, handler, "/other", nil, otherStarted, &otherOnce)
			if got := routeAdmissionFinish(t, other); got != http.StatusNoContent {
				t.Fatalf("request to another route = %d, want 204", got)
			}
			close(release)
			if got := routeAdmissionFinish(t, first); got != http.StatusNoContent {
				t.Fatalf("admitted request = %d, want 204", got)
			}
			if admitted.Load() != 2 {
				t.Fatalf("handler admissions = %d, want 2", admitted.Load())
			}
			if got := routeAdmissionRejections(t, observer); got != 1 {
				t.Fatalf("rejected{PER_ROUTE} = %v, want 1", got)
			}
			if got := admissionMetricValue(t, observer, "zubrium_concurrency_in_flight", `scope="HTTP",pool="public-router"`); got != 0 {
				t.Fatalf("HTTP in-flight after release = %v, want 0", got)
			}
			for _, metricLine := range []string{
				admissionMetricLine(t, observer, "zubrium_concurrency_rejected_total", `scope="HTTP",pool="public-router",reason="PER_ROUTE"`),
				admissionMetricLine(t, observer, "zubrium_concurrency_in_flight", `scope="HTTP",pool="public-router"`),
			} {
				lowered := strings.ToLower(metricLine)
				for _, forbidden := range []string{"route_id", "route-id", "routeid", "path=", "url=", "principal", "operation_id", "operation-id"} {
					if strings.Contains(lowered, forbidden) {
						t.Fatalf("metric line %q contains forbidden %q", metricLine, forbidden)
					}
				}
			}
		})

		t.Run("visitor_cannot_select_route", func(t *testing.T) {
			observer := admissionObserver(t)
			server := routeAdmissionServer(t, observer, PerRouteAdmission{
				Enabled: true, DefaultMaxInFlight: 2,
				Overrides: []RouteAdmissionOverride{{ID: "route-a", Path: "/static", MaxInFlight: 1}},
			})
			release := make(chan struct{})
			var started sync.Once
			firstStarted := make(chan struct{})
			handler := server.ingress.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/static" {
					<-release
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			first := routeAdmissionStart(t, handler, "/static", nil, firstStarted, &started)

			querySpoofStarted := make(chan struct{})
			var queryOnce sync.Once
			querySpoof := routeAdmissionStart(t, handler, "/static?route_id=route-b", nil, querySpoofStarted, &queryOnce)
			if got := routeAdmissionFinish(t, querySpoof); got != http.StatusServiceUnavailable {
				t.Fatalf("route spoof via query = %d, want 503", got)
			}

			headerSpoofStarted := make(chan struct{})
			var headerOnce sync.Once
			headerSpoof := routeAdmissionStart(t, handler, "/route-a", map[string]string{"X-Route-ID": "route-a"}, headerSpoofStarted, &headerOnce)
			if got := routeAdmissionFinish(t, headerSpoof); got != http.StatusNoContent {
				t.Fatalf("route spoof via header = %d, want 204 (static mapping only)", got)
			}

			close(release)
			if got := routeAdmissionFinish(t, first); got != http.StatusNoContent {
				t.Fatalf("admitted request = %d, want 204", got)
			}
		})
	})
}
