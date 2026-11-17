package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeTaskStore struct {
	completed   int
	failed      int
	result      json.RawMessage
	errorCode   string
	retryable   bool
	leaseID     uuid.UUID
	leaseEpoch  int64
	leaseToken  int64
	completeErr error
	failErr     error
	claimErr    error
	extendErr   error
}

func (s *fakeTaskStore) Claim(context.Context, string, uuid.UUID, int64, int, time.Duration) ([]CoreJob, error) {
	return nil, s.claimErr
}
func (s *fakeTaskStore) Extend(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error {
	return s.extendErr
}
func (s *fakeTaskStore) Complete(_ context.Context, _ string, id, owner uuid.UUID, epoch, token int64, result json.RawMessage) error {
	s.completed++
	s.result = append(json.RawMessage(nil), result...)
	s.leaseID, s.leaseEpoch, s.leaseToken = id, epoch, token
	return s.completeErr
}
func (s *fakeTaskStore) Fail(_ context.Context, _ string, id, owner uuid.UUID, epoch, token int64, code string, retryable bool, _ time.Duration) error {
	s.failed++
	s.errorCode, s.retryable = code, retryable
	s.leaseID, s.leaseEpoch, s.leaseToken = id, epoch, token
	return s.failErr
}

func TestWorkerCompletesAValidFencedTask(t *testing.T) {
	store := &fakeTaskStore{}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 4, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil }})
	if err != nil {
		t.Fatal(err)
	}
	job := CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 4, FencingToken: 9, Attempts: 1}
	pool.execute(context.Background(), job)
	if store.completed != 1 || store.failed != 0 || store.leaseID != job.ID || store.leaseEpoch != 4 || store.leaseToken != 9 || string(store.result) != `{"ok":true}` {
		t.Fatalf("task was not completed with its fencing tuple: %+v", store)
	}
}

func TestWorkerPersistsOnlySafeFailureCode(t *testing.T) {
	store := &fakeTaskStore{}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 4, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) {
			return nil, &TaskFailure{Code: "provider.incompatible", Retryable: false}
		}})
	if err != nil {
		t.Fatal(err)
	}
	job := CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 4, FencingToken: 9, Attempts: 1}
	pool.execute(context.Background(), job)
	if store.failed != 1 || store.completed != 0 || store.errorCode != "provider.incompatible" || store.retryable {
		t.Fatalf("failure code or retry policy not persisted safely: %+v", store)
	}
}

func TestWorkerHidesUnclassifiedErrorDetails(t *testing.T) {
	store := &fakeTaskStore{}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) {
			return nil, errors.New("secret that must not be persisted")
		}})
	if err != nil {
		t.Fatal(err)
	}
	job := CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 2, Attempts: 1}
	pool.execute(context.Background(), job)
	if store.errorCode != "task_failed" || store.retryable != true {
		t.Fatalf("unclassified error details were not reduced to a safe retry code: %+v", store)
	}
}

func TestWorkerRejectsNonObjectResult(t *testing.T) {
	store := &fakeTaskStore{}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) { return json.RawMessage(`[]`), nil }})
	if err != nil {
		t.Fatal(err)
	}
	job := CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 2, Attempts: 1}
	pool.execute(context.Background(), job)
	if store.failed != 1 || store.errorCode != "invalid_task_result" || store.completed != 0 {
		t.Fatalf("invalid task result was accepted: %+v", store)
	}
}

func TestWorkerRejectsInvalidUTF8ResultBeforeCore(t *testing.T) {
	store := &fakeTaskStore{}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) {
			return json.RawMessage{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	job := CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 2, Attempts: 1}
	pool.execute(context.Background(), job)
	if store.failed != 1 || store.errorCode != "invalid_task_result" || store.completed != 0 {
		t.Fatalf("invalid UTF-8 result reached Core completion: %+v", store)
	}
}

func TestRetryDelayIsBoundedAndStablePerAttempt(t *testing.T) {
	cfg := WorkerConfig{RetryBase: time.Second, RetryMaximum: 5 * time.Second}
	id := uuid.New()
	first := retryDelay(cfg, id, 3)
	if first != retryDelay(cfg, id, 3) || first < 0 || first > cfg.RetryMaximum {
		t.Fatalf("retry delay not deterministic or bounded: %s", first)
	}
}

func TestWorkerPollWaitReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitContext(ctx, time.Hour) {
		t.Fatal("worker poll kept waiting after its runtime context was cancelled")
	}
}

func TestWorkerTreatsLostFencingTokenAsExpectedOwnershipChange(t *testing.T) {
	for _, store := range []*fakeTaskStore{{completeErr: ErrCoordinatorJobNotOwned}, {failErr: ErrCoordinatorJobNotOwned}} {
		owner := uuid.New()
		pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
			LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second},
			map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) {
				if store.failErr != nil {
					return nil, errors.New("task failure")
				}
				return json.RawMessage(`{"ok":true}`), nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		pool.execute(context.Background(), CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 7, Attempts: 1})
		select {
		case err := <-pool.Errors():
			t.Fatalf("lost job lease was treated as a process failure: %v", err)
		default:
		}
	}
}

func TestWorkerPoolDegradesAfterFatalLeaseRenewalStoreFailure(t *testing.T) {
	store := &fakeTaskStore{extendErr: errors.New("database diagnostics must not leak")}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: 2 * time.Second, PollInterval: 100 * time.Millisecond,
		RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(ctx context.Context, _ CoreJob) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}})
	if err != nil {
		t.Fatal(err)
	}
	pool.execute(context.Background(), CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 1, Attempts: 1})
	select {
	case err := <-pool.Errors():
		if !errors.Is(err, ErrWorkerStore) {
			t.Fatalf("lease renewal store failure was not normalized: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lease renewal store failure did not degrade the worker pool")
	}
	if pool.failed.Load() == false {
		t.Fatal("worker pool remained healthy after lease renewal storage failed")
	}
}

func TestWorkerPoolTreatsLostJobLeaseRenewalAsExpectedOwnershipChange(t *testing.T) {
	store := &fakeTaskStore{extendErr: ErrCoordinatorJobNotOwned}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: 2 * time.Second, PollInterval: 100 * time.Millisecond,
		RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(ctx context.Context, _ CoreJob) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}})
	if err != nil {
		t.Fatal(err)
	}
	pool.execute(context.Background(), CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 1, Attempts: 1})
	select {
	case err := <-pool.Errors():
		t.Fatalf("expected job lease loss became a component failure: %v", err)
	default:
	}
	if pool.failed.Load() {
		t.Fatal("expected job lease loss marked the worker pool unhealthy")
	}
}

func TestWorkerPoolDegradesAfterCoordinatorInstanceLeaseLoss(t *testing.T) {
	store := &fakeTaskStore{extendErr: ErrCoordinatorLeaseLost}
	owner := uuid.New()
	pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: 2 * time.Second, PollInterval: 100 * time.Millisecond,
		RetryBase: time.Second, RetryMaximum: 5 * time.Second},
		map[string]TaskHandler{"test/v1": func(ctx context.Context, _ CoreJob) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}})
	if err != nil {
		t.Fatal(err)
	}
	pool.execute(context.Background(), CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 1, Attempts: 1})
	select {
	case err := <-pool.Errors():
		if !errors.Is(err, ErrInstanceNotReady) {
			t.Fatalf("Coordinator lease loss was not normalized: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Coordinator instance lease loss did not degrade the worker pool")
	}
}

func TestTaskFailureCodeCannotUseJobTypeSeparators(t *testing.T) {
	if failure := NewTaskFailure("worker/retry", true); failure.Code != "task_failed" {
		t.Fatalf("failure code accepted a slash that Core cannot persist: %+v", failure)
	}
}

func TestWorkerAppliesTimeoutAndContainsHandlerPanics(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		store := &fakeTaskStore{}
		owner := uuid.New()
		pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
			LeaseDuration: 3 * time.Second, TaskTimeout: 20 * time.Millisecond, PollInterval: 100 * time.Millisecond,
			RetryBase: time.Second, RetryMaximum: 5 * time.Second},
			map[string]TaskHandler{"test/v1": func(ctx context.Context, _ CoreJob) (json.RawMessage, error) {
				<-ctx.Done()
				return json.RawMessage(`{"late":true}`), nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		pool.execute(context.Background(), CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 2, Attempts: 1})
		if store.failed != 1 || store.errorCode != "task_timeout" || !store.retryable || store.completed != 0 {
			t.Fatalf("timed-out task was not fenced as a retryable failure: %+v", store)
		}
	})

	t.Run("panic", func(t *testing.T) {
		store := &fakeTaskStore{}
		owner := uuid.New()
		pool, err := NewWorkerPool(store, WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
			LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond,
			RetryBase: time.Second, RetryMaximum: 5 * time.Second},
			map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) { panic("secret panic detail") }})
		if err != nil {
			t.Fatal(err)
		}
		pool.execute(context.Background(), CoreJob{ID: uuid.New(), Type: "test/v1", LeaseOwner: owner, ConfigEpoch: 1, FencingToken: 2, Attempts: 1})
		if store.failed != 1 || store.errorCode != "worker_panic" || store.completed != 0 {
			t.Fatalf("handler panic was not converted to a safe task failure: %+v", store)
		}
	})
}

func TestWorkerComponentOwnsLifecycleAndDegradesOnStoreFailure(t *testing.T) {
	owner := uuid.New()
	config := WorkerConfig{SystemID: "test", Owner: owner, ConfigEpoch: 1, Concurrency: 1,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond,
		RetryBase: time.Second, RetryMaximum: 5 * time.Second}
	handlers := map[string]TaskHandler{"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil }}
	component, err := NewWorkerComponent(&fakeTaskStore{}, config, handlers)
	if err != nil {
		t.Fatal(err)
	}
	if err := component.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := component.Ready(context.Background()); err != nil {
		t.Fatalf("healthy worker reported not ready: %v", err)
	}
	if err := component.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if component.IsReady() {
		t.Fatal("stopped worker reported ready")
	}

	failedComponent, err := NewWorkerComponent(&fakeTaskStore{claimErr: errors.New("database diagnostics must not leak")}, config, handlers)
	if err != nil {
		t.Fatal(err)
	}
	if err := failedComponent.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failedComponent.Errors():
		if !errors.Is(err, ErrWorkerStore) {
			t.Fatalf("worker store failure was not normalized: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker store failure was not reported")
	}
	if failedComponent.IsReady() {
		t.Fatal("worker remained ready after a durable-store failure")
	}
	if err := failedComponent.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type cancelAwareClaimStore struct{ calls atomic.Int32 }

func (s *cancelAwareClaimStore) Claim(ctx context.Context, _ string, _ uuid.UUID, _ int64, _ int, _ time.Duration) ([]CoreJob, error) {
	if s.calls.Add(1) == 1 {
		return nil, errors.New("database diagnostic must not leak")
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*cancelAwareClaimStore) Extend(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error {
	return nil
}
func (*cancelAwareClaimStore) Complete(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, json.RawMessage) error {
	return nil
}
func (*cancelAwareClaimStore) Fail(context.Context, string, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration) error {
	return nil
}

func TestWorkerPoolStopsAllWorkersAfterFatalStoreFailure(t *testing.T) {
	store := &cancelAwareClaimStore{}
	config := WorkerConfig{SystemID: "test", Owner: uuid.New(), ConfigEpoch: 1, Concurrency: 2,
		LeaseDuration: 3 * time.Second, TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond,
		RetryBase: time.Second, RetryMaximum: 5 * time.Second}
	pool, err := NewWorkerPool(store, config, map[string]TaskHandler{
		"test/v1": func(context.Context, CoreJob) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-pool.Errors():
		if !errors.Is(err, ErrWorkerStore) {
			t.Fatalf("fatal store failure was not normalized: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fatal store failure was not reported")
	}
	select {
	case <-pool.done:
	case <-time.After(time.Second):
		t.Fatal("worker pool did not stop its sibling after a fatal store failure")
	}
	if pool.Healthy() {
		t.Fatal("failed worker pool reported healthy")
	}
}
