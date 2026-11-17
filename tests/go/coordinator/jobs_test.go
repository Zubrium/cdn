package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

type fakeJobEnqueueStore struct {
	request coreplatform.CoordinatorJobRequest
	created bool
	job     coreplatform.CoordinatorJob
	err     error
	calls   int
}

func testTaskCatalog(t *testing.T, jobTypes ...string) *TaskCatalog {
	t.Helper()
	handlers := make(map[string]TaskHandler, len(jobTypes))
	for _, jobType := range jobTypes {
		handlers[jobType] = func(context.Context, CoreJob) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}
	}
	catalog, err := NewTaskCatalog(handlers)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func (s *fakeJobEnqueueStore) Enqueue(_ context.Context, request coreplatform.CoordinatorJobRequest) (coreplatform.CoordinatorJob, bool, error) {
	s.calls++
	s.request = request
	if s.err != nil {
		return coreplatform.CoordinatorJob{}, false, s.err
	}
	if s.job.ID == uuid.Nil {
		s.job = coreplatform.CoordinatorJob{ID: request.ID, SystemID: request.SystemID, Type: request.Type, State: "queued"}
	}
	return s.job, s.created, nil
}

func TestJobSchedulerBindsAllowedTaskToActiveCoordinator(t *testing.T) {
	store := &fakeJobEnqueueStore{created: true}
	owner := uuid.New()
	jobTypes := map[string]TaskHandler{"archive.inspect/v1": func(context.Context, CoreJob) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}}
	catalog, err := NewTaskCatalog(jobTypes)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewJobScheduler(JobSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: owner, ConfigEpoch: 3, MaxAttempts: 5, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(jobTypes, "archive.inspect/v1")
	payload := json.RawMessage(`{"file_id":"` + uuid.NewString() + `"}`)
	receipt, err := scheduler.Schedule(context.Background(), "archive.inspect/v1", "import:feature:1", payload)
	if err != nil || !receipt.Created || receipt.State != "queued" || receipt.ID == uuid.Nil {
		t.Fatalf("task was not scheduled: receipt=%+v err=%v", receipt, err)
	}
	if store.calls != 1 || store.request.SystemID != "test-system" || store.request.Owner != owner || store.request.ConfigEpoch != 3 ||
		store.request.Type != "archive.inspect/v1" || store.request.IdempotencyKey != "import:feature:1" || store.request.MaxAttempts != 5 ||
		string(store.request.Payload) != string(payload) || store.request.ID != receipt.ID {
		t.Fatalf("task was scheduled outside its immutable capability: request=%+v receipt=%+v", store.request, receipt)
	}
}

func TestTaskCatalogCreatesIsolatedPerEngineScopes(t *testing.T) {
	catalog := testTaskCatalog(t, "archive.inspect/v1", "media.convert/v1")
	scope, err := catalog.ForTypes([]string{"media.convert/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.JobTypes(); len(got) != 1 || got[0] != "media.convert/v1" {
		t.Fatalf("engine scope includes task types it does not own: %v", got)
	}
	if _, err := catalog.ForTypes([]string{"missing/v1"}); !errors.Is(err, ErrTaskTypeUnavailable) {
		t.Fatalf("catalog accepted an absent handler: %v", err)
	}
	if _, err := catalog.ForTypes([]string{"archive.inspect/v1", "archive.inspect/v1"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("catalog accepted a duplicate type scope: %v", err)
	}
	store := &fakeJobEnqueueStore{created: true}
	scheduler, err := NewJobScheduler(JobSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1, MaxAttempts: 1, Catalog: scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Schedule(context.Background(), "archive.inspect/v1", "archive-job", json.RawMessage(`{"ok":true}`)); !errors.Is(err, ErrTaskTypeUnavailable) {
		t.Fatalf("engine scheduler accepted another engine's task type: %v", err)
	}
}

func TestTaskCatalogKeepsPublicationHandlerOutOfGenericSubmissions(t *testing.T) {
	catalog, err := NewTaskCatalog(map[string]TaskHandler{
		SitePublicationJobType: func(context.Context, CoreJob) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil },
		"worker.process/v1":    func(context.Context, CoreJob) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if types := catalog.JobTypes(); len(types) != 2 {
		t.Fatalf("worker dispatch catalog lost a registered handler: %v", types)
	}
	if types := catalog.SubmissionTypes(); len(types) != 1 || types[0] != "worker.process/v1" {
		t.Fatalf("generic submission scope exposed publication job: %v", types)
	}
	if _, err := catalog.ForTypes([]string{SitePublicationJobType}); !errors.Is(err, ErrTaskTypeUnavailable) {
		t.Fatalf("engine was allowed to request publication handler scope: %v", err)
	}
}

func TestJobSchedulerRejectsUnregisteredAndReservedTypes(t *testing.T) {
	store := &fakeJobEnqueueStore{created: true}
	scheduler, err := NewJobScheduler(JobSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1, MaxAttempts: 1, Catalog: testTaskCatalog(t, "worker.process/v1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, jobType := range []string{"worker.unknown/v1", SitePublicationJobType} {
		if _, err := scheduler.Schedule(context.Background(), jobType, "key", json.RawMessage(`{"ok":true}`)); !errors.Is(err, ErrTaskTypeUnavailable) {
			t.Fatalf("job type %q bypassed the worker catalog: %v", jobType, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("unregistered task reached Core: calls=%d", store.calls)
	}
}

func TestJobSchedulerRejectsTypedNilStore(t *testing.T) {
	var store *fakeJobEnqueueStore
	_, err := NewJobScheduler(JobSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1, MaxAttempts: 1, Catalog: testTaskCatalog(t, "worker.process/v1"),
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed nil Core store was accepted: %v", err)
	}
}

func TestJobSchedulerRejectsInvalidSubmissionBeforeCore(t *testing.T) {
	store := &fakeJobEnqueueStore{created: true}
	scheduler, err := NewJobScheduler(JobSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1, MaxAttempts: 1, Catalog: testTaskCatalog(t, "worker.process/v1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, input := range []struct {
		ctx     context.Context
		key     string
		payload json.RawMessage
	}{
		{context.Background(), "key", json.RawMessage(`[]`)},
		{context.Background(), "key", json.RawMessage([]byte(`{"name":"` + string([]byte{0xff}) + `"}`))},
		{context.Background(), " key", json.RawMessage(`{"ok":true}`)},
		{context.Background(), "bad\x00key", json.RawMessage(`{"ok":true}`)},
		{canceled, "key", json.RawMessage(`{"ok":true}`)},
	} {
		if _, err := scheduler.Schedule(input.ctx, "worker.process/v1", input.key, input.payload); !errors.Is(err, ErrInvalid) && !errors.Is(err, context.Canceled) {
			t.Fatalf("invalid submission was accepted: key=%q payload=%s err=%v", input.key, input.payload, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid submission reached Core: calls=%d", store.calls)
	}
}

func TestJobSchedulerReturnsIdempotentExistingJobReceipt(t *testing.T) {
	existingID := uuid.New()
	store := &fakeJobEnqueueStore{job: coreplatform.CoordinatorJob{
		ID: existingID, SystemID: "test-system", Type: "worker.process/v1", State: "succeeded",
	}, created: false}
	scheduler, err := NewJobScheduler(JobSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1, MaxAttempts: 1, Catalog: testTaskCatalog(t, "worker.process/v1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := scheduler.Schedule(context.Background(), "worker.process/v1", "same-operation", json.RawMessage(`{"ok":true}`))
	if err != nil || receipt.Created || receipt.ID != existingID || receipt.State != "succeeded" {
		t.Fatalf("existing idempotent task was not returned safely: receipt=%+v err=%v", receipt, err)
	}
}
