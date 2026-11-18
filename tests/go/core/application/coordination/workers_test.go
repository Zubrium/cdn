package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

type fakeTaskStore struct {
	job           CoreJob
	claimOnce     bool
	claimErr      error
	completeCh    chan corecoord.CoordinatorTaskOutcome
	failCh        chan TaskFailure
	completeCalls int
	completeErr   error
	extendErr     error
}

func (s *fakeTaskStore) Claim(ctx context.Context, owner uuid.UUID, epoch int64, limit int, lease, retention time.Duration) ([]CoreJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if owner != s.job.LeaseOwner || epoch != s.job.ConfigEpoch || limit != 1 || lease <= 0 || retention <= 0 {
		return nil, ErrInvalid
	}
	if s.claimOnce {
		return nil, nil
	}
	s.claimOnce = true
	return []CoreJob{s.job}, nil
}

func (s *fakeTaskStore) Extend(ctx context.Context, _ uuid.UUID, _ uuid.UUID, _ int64, _ int64, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.extendErr
}

func (s *fakeTaskStore) Complete(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ int64, _ int64,
	_, _ int, _ time.Duration, outcome corecoord.CoordinatorTaskOutcome) error {
	s.completeCalls++
	if s.completeCh != nil {
		s.completeCh <- outcome
	}
	return s.completeErr
}

func (s *fakeTaskStore) Fail(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ int64, _ int64,
	code string, retryable bool, _ time.Duration, _ time.Duration) error {
	if s.failCh != nil {
		s.failCh <- TaskFailure{Code: code, Retryable: retryable}
	}
	return nil
}

func workerTestConfig(owner uuid.UUID) WorkerConfig {
	return WorkerConfig{Owner: owner, ConfigEpoch: 3, Concurrency: 1, LeaseDuration: 3 * time.Second,
		TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond, RetryBase: 20 * time.Millisecond,
		RetryMaximum: time.Second, MaxWorkflowTasks: 100, MaxChildrenPerTask: 8, WorkflowRetention: time.Hour}
}

func workerTestJob(owner uuid.UUID) CoreJob {
	return CoreJob{ID: uuid.New(), WorkflowID: uuid.New(), ComponentID: "worker.example", OperationID: "inspect",
		ContractVersion: 1, Type: "inspect/v1", State: "running", Attempts: 1, MaxAttempts: 3,
		LeaseOwner: owner, FencingToken: 1, ConfigEpoch: 3}
}

func TestWorkerPoolAtomicallyPersistsDynamicTaskOutcome(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{job: workerTestJob(owner), completeCh: make(chan corecoord.CoordinatorTaskOutcome, 1)}
	handlers := map[string]TaskHandler{
		taskHandlerKey("worker.example", "inspect", 1): func(context.Context, CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
			return corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{"count":1}`), Children: []corecoord.CoordinatorTaskChild{{
				ComponentID: "engine.secondary", OperationID: "index", ContractVersion: 2, EmissionKey: "index:1",
				Payload: json.RawMessage(`{"artifact":"opaque"}`), MaxAttempts: 2,
			}}}, nil
		},
		taskHandlerKey("engine.secondary", "index", 2): taskHandlerForTest,
	}
	pool, err := NewWorkerPool(store, workerTestConfig(owner), handlers)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-store.completeCh:
		if string(outcome.Result) != `{"count":1}` || len(outcome.Children) != 1 || outcome.Children[0].ComponentID != "engine.secondary" {
			t.Fatalf("worker lost dynamic child outcome: %+v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not persist handler outcome")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPoolRejectsUnregisteredDynamicChildBeforePersistence(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{job: workerTestJob(owner), failCh: make(chan TaskFailure, 1)}
	handlers := map[string]TaskHandler{taskHandlerKey("worker.example", "inspect", 1): func(context.Context, CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
		return corecoord.CoordinatorTaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.CoordinatorTaskChild{{
			ComponentID: "worker.missing", OperationID: "run", ContractVersion: 1, EmissionKey: "missing",
			Payload: json.RawMessage(`{}`), MaxAttempts: 1,
		}}}, nil
	}}
	pool, err := NewWorkerPool(store, workerTestConfig(owner), handlers)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case failure := <-store.failCh:
		if failure.Code != "handler_unavailable" || failure.Retryable {
			t.Fatalf("unregistered child did not fail closed: %+v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not reject unregistered child")
	}
	if store.completeCalls != 0 {
		t.Fatal("worker unexpectedly persisted an unregistered child")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPoolMapsHandlerFailuresAndPanicsToSafeCodes(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler TaskHandler
		code    string
		retry   bool
	}{
		{name: "typed failure", handler: func(context.Context, CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
			return corecoord.CoordinatorTaskOutcome{}, NewTaskFailure("remote_unavailable", true)
		}, code: "remote_unavailable", retry: true},
		{name: "panic", handler: func(context.Context, CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
			panic("secret details must not escape")
		}, code: "worker_panic", retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := uuid.New()
			store := &fakeTaskStore{job: workerTestJob(owner), failCh: make(chan TaskFailure, 1)}
			pool, err := NewWorkerPool(store, workerTestConfig(owner), map[string]TaskHandler{
				taskHandlerKey("worker.example", "inspect", 1): test.handler,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := pool.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case failure := <-store.failCh:
				if failure.Code != test.code || failure.Retryable != test.retry {
					t.Fatalf("failure mapping mismatch: %+v", failure)
				}
			case <-time.After(time.Second):
				t.Fatal("worker did not persist handler failure")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := pool.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkerPoolRejectsInvalidOutcomeAndInvalidHandlerRegistration(t *testing.T) {
	oversizedResult := make([]byte, MaxTaskResultBytes+1)
	for index := range oversizedResult {
		oversizedResult[index] = ' '
	}
	copy(oversizedResult, []byte(`{}`))
	for _, outcome := range []corecoord.CoordinatorTaskOutcome{
		{Result: json.RawMessage(`[]`)},
		{Result: json.RawMessage(oversizedResult)},
		{Result: json.RawMessage(`{}`), Children: []corecoord.CoordinatorTaskChild{{
			ComponentID: "worker.example", OperationID: "scan", ContractVersion: 0, EmissionKey: "bad",
			Payload: json.RawMessage(`{}`), MaxAttempts: 1,
		}}},
	} {
		if validTaskOutcome(outcome, 8) {
			t.Errorf("invalid workflow outcome passed validation: %+v", outcome)
		}
	}
	owner := uuid.New()
	store := &fakeTaskStore{job: workerTestJob(owner)}
	for _, handlers := range []map[string]TaskHandler{
		nil,
		{"not-a-dispatch-key": taskHandlerForTest},
		{taskHandlerKey("worker.example", "inspect", 1): nil},
	} {
		if _, err := NewWorkerPool(store, workerTestConfig(owner), handlers); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid worker handlers were accepted: %v", err)
		}
	}
}

func TestWorkerPoolLifecycleRejectsRepeatedStartAndHonorsCancellation(t *testing.T) {
	owner := uuid.New()
	started := make(chan struct{})
	store := &fakeTaskStore{job: workerTestJob(owner)}
	handler := func(ctx context.Context, _ CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
		close(started)
		<-ctx.Done()
		return corecoord.CoordinatorTaskOutcome{}, ctx.Err()
	}
	pool, err := NewWorkerPool(store, workerTestConfig(owner), map[string]TaskHandler{taskHandlerKey("worker.example", "inspect", 1): handler})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(context.Background()); !errors.Is(err, ErrWorkerNotStarted) {
		t.Fatalf("unstarted worker pool closed without error: %v", err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := pool.Start(context.Background()); !errors.Is(err, ErrWorkerAlreadyStarted) {
		t.Fatalf("worker pool started twice: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatalf("worker pool failed to stop a cooperative handler: %v", err)
	}
}

func TestWorkerPoolFailsClosedWhenDurableStoreIsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name      string
		claimErr  error
		finishErr error
	}{
		{name: "claim", claimErr: errors.New("database unavailable")},
		{name: "persist result", finishErr: errors.New("database unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := uuid.New()
			store := &fakeTaskStore{job: workerTestJob(owner), claimErr: test.claimErr, completeErr: test.finishErr}
			pool, err := NewWorkerPool(store, workerTestConfig(owner), map[string]TaskHandler{
				taskHandlerKey("worker.example", "inspect", 1): taskHandlerForTest,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := pool.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case got, open := <-pool.Errors():
				if !open || !errors.Is(got, ErrWorkerStore) {
					t.Fatalf("durable store failure was not reported safely: error=%v open=%t", got, open)
				}
			case <-time.After(time.Second):
				t.Fatal("worker pool did not report durable store outage")
			}
			if pool.Healthy() {
				t.Fatal("worker pool remained healthy without a durable workflow store")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := pool.Close(ctx); err != nil {
				t.Fatalf("failed worker pool did not stop cleanly: %v", err)
			}
		})
	}
}

func TestWorkerPoolTimesOutHandlerAndSchedulesRetry(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{job: workerTestJob(owner), failCh: make(chan TaskFailure, 1)}
	handler := func(ctx context.Context, _ CoreJob) (corecoord.CoordinatorTaskOutcome, error) {
		<-ctx.Done()
		return corecoord.CoordinatorTaskOutcome{}, ctx.Err()
	}
	config := workerTestConfig(owner)
	config.TaskTimeout = 20 * time.Millisecond
	pool, err := NewWorkerPool(store, config, map[string]TaskHandler{
		taskHandlerKey("worker.example", "inspect", 1): handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case failure := <-store.failCh:
		if failure.Code != "task_timeout" || !failure.Retryable {
			t.Fatalf("task timeout did not become a retryable safe failure: %+v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not enforce the task deadline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatalf("worker pool failed to stop after timeout: %v", err)
	}
}
