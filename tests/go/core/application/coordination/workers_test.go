package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

type fakeTaskStore struct {
	task        Task
	claimErr    error
	completeCh  chan corecoord.TaskOutcome
	failCh      chan TaskFailure
	completeErr error
	extendErr   error

	mu            sync.Mutex
	claimedScope  []string
	claimOnce     bool
	completeCalls int
}

func (s *fakeTaskStore) claimScopeSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.claimedScope...)
}

func (s *fakeTaskStore) completeCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeCalls
}

type TestTaskHandler func(context.Context, Task) (corecoord.TaskOutcome, error)

type testOperationExecutor map[string]TestTaskHandler

func operationHandlerKey(classID, operationID string, version int) string {
	return classID + "\x00" + operationID + "\x00" + strconv.Itoa(version)
}

func taskHandlerForTest(context.Context, Task) (corecoord.TaskOutcome, error) {
	return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
}

func (executor testOperationExecutor) Execute(ctx context.Context, task Task) (corecoord.TaskOutcome, error) {
	if task.Operation == nil {
		return corecoord.TaskOutcome{}, NewTaskFailure("handler_unavailable", false)
	}
	handler := executor[operationHandlerKey(task.Operation.ClassID, task.Operation.ID, task.Operation.Version)]
	if handler == nil {
		return corecoord.TaskOutcome{}, NewTaskFailure("handler_unavailable", false)
	}
	outcome, err := handler(ctx, task)
	if err != nil {
		return corecoord.TaskOutcome{}, err
	}
	for _, child := range outcome.Children {
		if child.Kind != corecoord.TaskKindOperation {
			continue
		}
		if child.Operation == nil || executor[operationHandlerKey(child.Operation.ClassID, child.Operation.ID, child.Operation.Version)] == nil {
			return corecoord.TaskOutcome{}, NewTaskFailure("handler_unavailable", false)
		}
	}
	return outcome, nil
}

func testExecutorFor(handlers map[string]TestTaskHandler) OperationExecutor {
	return testOperationExecutor(handlers)
}

func (s *fakeTaskStore) Claim(ctx context.Context, owner uuid.UUID, epoch int64, componentIDs []string, limit int, lease, retention time.Duration) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if owner != s.task.LeaseOwner || epoch != s.task.ConfigEpoch || limit != 1 || lease <= 0 || retention <= 0 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	s.claimedScope = append([]string(nil), componentIDs...)
	claimOnce := s.claimOnce
	s.claimOnce = true
	s.mu.Unlock()
	if claimOnce {
		return nil, nil
	}
	return []Task{s.task}, nil
}

func (s *fakeTaskStore) Extend(ctx context.Context, _ uuid.UUID, _ uuid.UUID, _ int64, _ int64, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.extendErr
}

func (s *fakeTaskStore) Complete(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ int64, _ int64,
	_, _ int, _ time.Duration, outcome corecoord.TaskOutcome) error {
	s.mu.Lock()
	s.completeCalls++
	s.mu.Unlock()
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

func workerTestConfig(owner uuid.UUID) TaskExecutorConfig {
	return TaskExecutorConfig{Owner: owner, ConfigEpoch: 3, TaskClasses: []string{"media-pool"}, Concurrency: 1, LeaseDuration: 3 * time.Second,
		TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond, RetryBase: 20 * time.Millisecond,
		RetryMaximum: time.Second, MaxWorkflowTasks: 100, MaxChildrenPerTask: 8, WorkflowRetention: time.Hour}
}

func workerTestTask(owner uuid.UUID) Task {
	return Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "inspect", Version: 1}, State: "running", Attempts: 1, MaxAttempts: 3,
		LeaseOwner: owner, FencingToken: 1, ConfigEpoch: 3}
}

func TestTaskExecutorAtomicallyPersistsDynamicTaskOutcome(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{task: workerTestTask(owner), completeCh: make(chan corecoord.TaskOutcome, 1)}
	handlers := map[string]TestTaskHandler{
		operationHandlerKey("worker.example", "inspect", 1): func(context.Context, Task) (corecoord.TaskOutcome, error) {
			return corecoord.TaskOutcome{Result: json.RawMessage(`{"count":1}`), Children: []corecoord.TaskChild{{
				Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "engine.secondary", ID: "index", Version: 2}, EmissionKey: "index:1",
				Payload: json.RawMessage(`{"artifact":"opaque"}`), MaxAttempts: 2,
			}}}, nil
		},
		operationHandlerKey("engine.secondary", "index", 2): taskHandlerForTest,
	}
	pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(handlers))
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-store.completeCh:
		if string(outcome.Result) != `{"count":1}` || len(outcome.Children) != 1 || outcome.Children[0].Operation == nil || outcome.Children[0].Operation.ClassID != "engine.secondary" {
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

func TestTaskExecutorCopiesClaimScopeAtConstruction(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{task: workerTestTask(owner), completeCh: make(chan corecoord.TaskOutcome, 1)}
	config := workerTestConfig(owner)
	pool, err := NewTaskExecutor(store, config, testExecutorFor(map[string]TestTaskHandler{
		operationHandlerKey("worker.example", "inspect", 1): taskHandlerForTest,
	}))
	if err != nil {
		t.Fatal(err)
	}
	config.TaskClasses[0] = "untrusted-pool"
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.completeCh:
	case <-time.After(time.Second):
		t.Fatal("task executor did not complete its claimed task")
	}
	if scope := store.claimScopeSnapshot(); len(scope) != 1 || scope[0] != "media-pool" {
		t.Fatalf("task executor claim scope changed after construction: %v", scope)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTaskExecutorRejectsUnregisteredDynamicChildBeforePersistence(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{task: workerTestTask(owner), failCh: make(chan TaskFailure, 1)}
	handlers := map[string]TestTaskHandler{operationHandlerKey("worker.example", "inspect", 1): func(context.Context, Task) (corecoord.TaskOutcome, error) {
		return corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{{
			Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.missing", ID: "run", Version: 1}, EmissionKey: "missing",
			Payload: json.RawMessage(`{}`), MaxAttempts: 1,
		}}}, nil
	}}
	pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(handlers))
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
	if store.completeCallCount() != 0 {
		t.Fatal("worker unexpectedly persisted an unregistered child")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTaskExecutorMapsHandlerFailuresAndPanicsToSafeCodes(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler TestTaskHandler
		code    string
		retry   bool
	}{
		{name: "typed failure", handler: func(context.Context, Task) (corecoord.TaskOutcome, error) {
			return corecoord.TaskOutcome{}, NewTaskFailure("remote_unavailable", true)
		}, code: "remote_unavailable", retry: true},
		{name: "panic", handler: func(context.Context, Task) (corecoord.TaskOutcome, error) {
			panic("secret details must not escape")
		}, code: "operation_panic", retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := uuid.New()
			store := &fakeTaskStore{task: workerTestTask(owner), failCh: make(chan TaskFailure, 1)}
			pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(map[string]TestTaskHandler{
				operationHandlerKey("worker.example", "inspect", 1): test.handler,
			}))
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

func TestTaskExecutorRejectsInvalidOutcomeAndInvalidHandlerRegistration(t *testing.T) {
	oversizedResult := make([]byte, MaxTaskResultBytes+1)
	for index := range oversizedResult {
		oversizedResult[index] = ' '
	}
	copy(oversizedResult, []byte(`{}`))
	for _, outcome := range []corecoord.TaskOutcome{
		{Result: json.RawMessage(`[]`)},
		{Result: json.RawMessage(oversizedResult)},
		{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{{
			Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "scan", Version: 0}, EmissionKey: "bad",
			Payload: json.RawMessage(`{}`), MaxAttempts: 1,
		}}},
	} {
		if validTaskOutcome(outcome, 8) {
			t.Errorf("invalid workflow outcome passed validation: %+v", outcome)
		}
	}
	owner := uuid.New()
	store := &fakeTaskStore{task: workerTestTask(owner)}
	if _, err := NewTaskExecutor(store, workerTestConfig(owner), nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing composition executor was accepted: %v", err)
	}
}

func TestTaskOutcomeAcceptsCoreJoinTasksWithDependencies(t *testing.T) {
	join := corecoord.TaskChild{
		Kind:        corecoord.TaskKindJoin,
		EmissionKey: "join:rendered-assets",
		Payload:     json.RawMessage(`{}`),
		DependsOn:   []uuid.UUID{uuid.New()},
	}
	outcome := corecoord.TaskOutcome{
		Result:   json.RawMessage(`{"fanout":true}`),
		Children: []corecoord.TaskChild{join},
	}
	if !validTaskOutcome(outcome, 2) {
		t.Fatal("Coordinator rejected a handler-free join task with an explicit dependency")
	}
}

func TestTaskExecutorLifecycleRejectsRepeatedStartAndHonorsCancellation(t *testing.T) {
	owner := uuid.New()
	started := make(chan struct{})
	store := &fakeTaskStore{task: workerTestTask(owner)}
	handler := func(ctx context.Context, _ Task) (corecoord.TaskOutcome, error) {
		close(started)
		<-ctx.Done()
		return corecoord.TaskOutcome{}, ctx.Err()
	}
	pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(map[string]TestTaskHandler{operationHandlerKey("worker.example", "inspect", 1): handler}))
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(context.Background()); !errors.Is(err, ErrTaskExecutorNotStarted) {
		t.Fatalf("unstarted worker pool closed without error: %v", err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := pool.Start(context.Background()); !errors.Is(err, ErrTaskExecutorAlreadyStarted) {
		t.Fatalf("worker pool started twice: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatalf("worker pool failed to stop a cooperative handler: %v", err)
	}
}

func TestTaskExecutorFailsClosedWhenDurableStoreIsUnavailable(t *testing.T) {
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
			store := &fakeTaskStore{task: workerTestTask(owner), claimErr: test.claimErr, completeErr: test.finishErr}
			pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(map[string]TestTaskHandler{
				operationHandlerKey("worker.example", "inspect", 1): taskHandlerForTest,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err := pool.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case got, open := <-pool.Errors():
				if !open || !errors.Is(got, ErrTaskExecutorStore) {
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

func TestTaskExecutorTimesOutHandlerAndSchedulesRetry(t *testing.T) {
	owner := uuid.New()
	store := &fakeTaskStore{task: workerTestTask(owner), failCh: make(chan TaskFailure, 1)}
	handler := func(ctx context.Context, _ Task) (corecoord.TaskOutcome, error) {
		<-ctx.Done()
		return corecoord.TaskOutcome{}, ctx.Err()
	}
	config := workerTestConfig(owner)
	config.TaskTimeout = 20 * time.Millisecond
	pool, err := NewTaskExecutor(store, config, testExecutorFor(map[string]TestTaskHandler{
		operationHandlerKey("worker.example", "inspect", 1): handler,
	}))
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
