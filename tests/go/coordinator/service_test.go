package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

type serviceStore struct {
	mu               sync.Mutex
	leaseReleased    bool
	claimErr         error
	publicationCalls int
	queuedJobs       int
	lastRetry        uuid.UUID
}

func (s *serviceStore) RegisterInstance(context.Context, string, uuid.UUID, string, time.Duration) (int64, error) {
	return 1, nil
}
func (s *serviceStore) RenewInstance(context.Context, string, uuid.UUID, int64, time.Duration) error {
	return nil
}
func (s *serviceStore) ReleaseInstance(context.Context, string, uuid.UUID, int64) error {
	s.mu.Lock()
	s.leaseReleased = true
	s.mu.Unlock()
	return nil
}
func (s *serviceStore) Enqueue(_ context.Context, request coreplatform.CoordinatorJobRequest) (coreplatform.CoordinatorJob, bool, error) {
	s.mu.Lock()
	s.queuedJobs++
	s.mu.Unlock()
	return coreplatform.CoordinatorJob{ID: request.ID, SystemID: request.SystemID, Type: request.Type, State: "queued"}, true, nil
}
func (s *serviceStore) Claim(context.Context, string, uuid.UUID, int64, int, time.Duration) ([]CoreJob, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return nil, nil
}
func (*serviceStore) Extend(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error {
	return nil
}
func (*serviceStore) Complete(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, json.RawMessage) error {
	return nil
}
func (*serviceStore) Fail(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration) error {
	return nil
}
func (*serviceStore) Retry(context.Context, string, uuid.UUID, uuid.UUID, int64) error { return nil }
func (s *serviceStore) ListJobs(context.Context, coreplatform.CoordinatorJobListRequest) (coreplatform.CoordinatorJobPage, error) {
	return coreplatform.CoordinatorJobPage{}, nil
}
func (s *serviceStore) SchedulePublication(_ context.Context, request coreplatform.CoordinatorPublicationRequest) (int64, coreplatform.CoordinatorJob, bool, error) {
	s.mu.Lock()
	s.publicationCalls++
	s.mu.Unlock()
	return 3, coreplatform.CoordinatorJob{ID: request.JobID, SystemID: request.SystemID, Type: request.JobType, State: "queued"}, true, nil
}
func (*serviceStore) ActivatePublication(context.Context, string, uuid.UUID, uuid.UUID, uuid.UUID, int64, int64, int64, uuid.UUID) (coreplatform.CoordinatorRouteTarget, error) {
	return coreplatform.CoordinatorRouteTarget{}, nil
}

func serviceTestConfig(store ServiceStore) ServiceConfig {
	return ServiceConfig{
		Store: store, SystemID: "test-system", Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		InstanceLease: 3 * time.Second, HeartbeatInterval: 100 * time.Millisecond,
		WorkerConcurrency: 1, JobLease: 3 * time.Second, TaskTimeout: time.Minute,
		PollInterval: 10 * time.Millisecond, RetryBase: time.Second, RetryMaximum: time.Minute,
		MaxAttempts: 3,
	}
}

func TestServiceStartsOneWorkerPoolAndIssuesScopedCapabilities(t *testing.T) {
	store := &serviceStore{}
	catalog, err := BuildTaskCatalog(BuildPlan{Components: []string{"core", "coordinator", "worker.archive"}}, []WorkerTaskSet{{
		ComponentID: "worker.archive", Handlers: map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := serviceTestConfig(store)
	config.Catalog = catalog
	routeID, siteID, contentID := uuid.New(), uuid.New(), uuid.New()
	config.Bindings = []PublicationBinding{{RouteID: routeID, SiteID: siteID, ContentID: contentID, AutoPublish: true}}
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.Ready() {
		t.Fatal("Coordinator reported fully ready before the worker pool activation phase")
	}
	if _, _, _, err := service.ScheduleAutomaticPublication(context.Background(), routeID, PublicationInput{
		SnapshotID: uuid.New(), SnapshotRevision: 1, ContentRevision: 1,
	}); !errors.Is(err, ErrInstanceNotReady) {
		t.Fatalf("Coordinator accepted product work before composition activation: %v", err)
	}
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.Ready() {
		t.Fatal("started Coordinator service is not ready")
	}
	scheduler, err := service.NewJobScheduler([]string{"archive.inspect/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Schedule(context.Background(), "site-publication/v1", "untrusted-key", json.RawMessage(`{}`)); !errors.Is(err, ErrTaskTypeUnavailable) {
		t.Fatalf("engine scheduler accepted reserved publication task: %v", err)
	}
	if _, err := scheduler.Schedule(context.Background(), "archive.inspect/v1", "request-00000000001", json.RawMessage(`{"file_id":"opaque"}`)); err != nil {
		t.Fatalf("selected task could not be queued: %v", err)
	}
	input := PublicationInput{SnapshotID: uuid.New(), SnapshotRevision: 2, ContentRevision: 4, ProviderVersion: "v1"}
	if _, _, created, err := service.ScheduleAutomaticPublication(context.Background(), routeID, input); err != nil || !created {
		t.Fatalf("configured automatic publication was not scheduled: created=%v err=%v", created, err)
	}
	if _, _, _, err := service.ScheduleAutomaticPublication(context.Background(), uuid.New(), input); !errors.Is(err, ErrPublicationBindingMissing) {
		t.Fatalf("unconfigured route publication was accepted: %v", err)
	}
	if _, err := service.NewJobScheduler([]string{"unknown.task/v1"}); !errors.Is(err, ErrTaskTypeUnavailable) {
		t.Fatalf("unregistered job type was granted: %v", err)
	}
	if _, err := service.NewJobControl(); err != nil {
		t.Fatalf("job control capability unavailable while ready: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.leaseReleased || store.queuedJobs != 1 || store.publicationCalls != 1 {
		t.Fatalf("Coordinator lifecycle or durable operations incomplete: released=%v queued=%d publications=%d", store.leaseReleased, store.queuedJobs, store.publicationCalls)
	}
}

func TestServiceDoesNotScheduleAutomaticPublicationForDisabledBinding(t *testing.T) {
	store := &serviceStore{}
	config := serviceTestConfig(store)
	config.Bindings = []PublicationBinding{{RouteID: uuid.New(), SiteID: uuid.New(), ContentID: uuid.New(), AutoPublish: false}}
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = service.ScheduleAutomaticPublication(context.Background(), config.Bindings[0].RouteID, PublicationInput{
		SnapshotID: uuid.New(), SnapshotRevision: 1, ContentRevision: 1,
	})
	if !errors.Is(err, ErrAutomaticPublicationDisabled) {
		t.Fatalf("disabled automatic publication binding was ignored: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceBecomesUnreadyOnDurableWorkerStoreFailure(t *testing.T) {
	store := &serviceStore{claimErr: errors.New("database unavailable")}
	catalog, err := BuildTaskCatalog(BuildPlan{Components: []string{"core", "coordinator", "worker.archive"}}, []WorkerTaskSet{{
		ComponentID: "worker.archive", Handlers: map[string]TaskHandler{"archive.inspect/v1": taskHandlerForTest},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := serviceTestConfig(store)
	config.Catalog = catalog
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-service.Errors():
		if got == nil || got.Error() != "Coordinator workers component reported a fatal failure" {
			t.Fatalf("durable failure was not sanitized: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("durable store failure was not surfaced")
	}
	if service.Ready() {
		t.Fatal("Coordinator remained ready after durable worker failure")
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewServiceRejectsUnsafeResourceLimitsAndDuplicateRoutes(t *testing.T) {
	store := &serviceStore{}
	base := serviceTestConfig(store)
	invalid := []ServiceConfig{base, base, base, base, base}
	invalid[0].Fingerprint = "secret"
	invalid[1].WorkerConcurrency = 0
	invalid[2].PollInterval = invalid[2].JobLease
	invalid[3].MaxAttempts = 101
	invalid[4].Bindings = []PublicationBinding{
		{RouteID: uuid.Nil, SiteID: uuid.New(), ContentID: uuid.New()},
	}
	duplicateRoute := uuid.New()
	duplicate := base
	duplicate.Bindings = []PublicationBinding{
		{RouteID: duplicateRoute, SiteID: uuid.New(), ContentID: uuid.New()},
		{RouteID: duplicateRoute, SiteID: uuid.New(), ContentID: uuid.New()},
	}
	invalid = append(invalid, duplicate)
	for index, config := range invalid {
		if _, err := NewService(config); !errors.Is(err, ErrInvalid) {
			t.Errorf("unsafe configuration %d accepted: %v", index, err)
		}
	}
}

func TestCloseBeforeStartClosesFailureStream(t *testing.T) {
	service, err := NewService(serviceTestConfig(&serviceStore{}))
	if err != nil {
		t.Fatal(err)
	}
	failures := service.Errors()
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, open := <-failures; open {
		t.Fatal("failure stream remained open after closing an unstarted service")
	}
	if err := service.Start(context.Background()); !errors.Is(err, ErrWorkerAlreadyStarted) {
		t.Fatalf("closed Coordinator service restarted: %v", err)
	}
}
