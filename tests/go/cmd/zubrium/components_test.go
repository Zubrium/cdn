package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/coordinator"
	"github.com/google/uuid"
)

type testApplicationReadiness bool

func (r testApplicationReadiness) Ready() bool { return bool(r) }

type testCoordinatorServiceStore struct{ coordinator.ServiceStore }

func (testCoordinatorServiceStore) RegisterInstance(context.Context, string, uuid.UUID, string, time.Duration) (int64, error) {
	return 1, nil
}
func (testCoordinatorServiceStore) RenewInstance(context.Context, string, uuid.UUID, int64, time.Duration) error {
	return nil
}
func (testCoordinatorServiceStore) ReleaseInstance(context.Context, string, uuid.UUID, int64) error {
	return nil
}

func TestFileAPIStopsServingWhenCoordinatorIsUnavailable(t *testing.T) {
	called := false
	component := &fileAPIComponent{}
	handler := component.requireCoordinator(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/files", nil))
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("File API served while Coordinator is unavailable: status=%d handlerCalled=%v", response.Code, called)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Coordinator outage response is cacheable or lacks retry guidance: %v", response.Header())
	}
}

func TestFileAPIRemainsUnavailableWhenAnotherComponentFailsReadiness(t *testing.T) {
	service, err := coordinator.NewService(coordinator.ServiceConfig{
		Store: testCoordinatorServiceStore{}, SystemID: "test", Fingerprint: strings.Repeat("a", 64),
		InstanceLease: 3 * time.Second, HeartbeatInterval: 100 * time.Millisecond,
		WorkerConcurrency: 1, JobLease: 3 * time.Second, TaskTimeout: time.Minute,
		PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: time.Minute,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close Coordinator service: %v", err)
		}
	}()

	component := &fileAPIComponent{
		coordinator:          &coordinatorComponent{service: service},
		applicationReadiness: testApplicationReadiness(false),
		ready:                true,
	}
	called := false
	response := httptest.NewRecorder()
	component.requireCoordinator(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(
		response, httptest.NewRequest(http.MethodGet, "/api/v2/files", nil),
	)
	if response.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("File API served after composed application lost readiness: status=%d handlerCalled=%v", response.Code, called)
	}
}
