package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

func TestComposeRejectsInvalidDependencyOrderBeforeSideEffects(t *testing.T) {
	for _, test := range []struct {
		name     string
		requires []string
		plan     []string
	}{
		{name: "dependency ordered later", requires: []string{"core"}, plan: []string{"engine.example", "core"}},
		{name: "unknown dependency", requires: []string{"engine.missing"}, plan: []string{"core", "engine.example"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var factories atomic.Int32
			registry := NewRuntimeRegistry()
			for _, id := range []string{"core", "engine.example"} {
				id := id
				definition := RuntimeComponent{ID: id, New: func(context.Context) (ManagedComponent, error) {
					factories.Add(1)
					return &lifecycleRecorder{id: id, order: new([]string)}, nil
				}}
				if id == "engine.example" {
					definition.Requires = test.requires
				}
				if err := registry.Register(definition); err != nil {
					t.Fatal(err)
				}
			}
			runtime, err := Compose(context.Background(), BuildPlan{Components: test.plan}, registry, time.Second)
			if err == nil || runtime != nil {
				if runtime != nil {
					_ = runtime.Close(context.Background())
				}
				t.Fatalf("invalid component dependency was accepted: runtime=%v err=%v", runtime, err)
			}
			if factories.Load() != 0 {
				t.Fatalf("invalid dependency invoked %d component factories before failing", factories.Load())
			}
		})
	}
}

func TestTaskExecutorNeverInvokesClaimedJoinTask(t *testing.T) {
	owner := uuid.New()
	join := workerTestTask(owner)
	join.Kind = corecoord.TaskKindJoin
	join.Operation = nil
	store := &fakeTaskStore{task: join, failCh: make(chan TaskFailure, 1)}
	var invoked atomic.Int32
	pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(map[string]TestTaskHandler{
		operationHandlerKey("worker.example", "inspect", 1): func(context.Context, Task) (corecoord.TaskOutcome, error) {
			invoked.Add(1)
			return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case failure := <-store.failCh:
		if failure.Code != "invalid_task_kind" || failure.Retryable {
			t.Fatalf("claimed join task was not failed closed: %+v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("claimed join task was not rejected")
	}
	if invoked.Load() != 0 || store.completeCalls != 0 {
		t.Fatalf("join task invoked a worker handler or committed: handlers=%d completes=%d", invoked.Load(), store.completeCalls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerHandlerReceivesDurableTaskIdentity(t *testing.T) {
	owner := uuid.New()
	task := workerTestTask(owner)
	store := &fakeTaskStore{task: task}
	calls := make(chan Task, 1)
	pool, err := NewTaskExecutor(store, workerTestConfig(owner), testExecutorFor(map[string]TestTaskHandler{
		operationHandlerKey("worker.example", "inspect", 1): func(_ context.Context, received Task) (corecoord.TaskOutcome, error) {
			calls <- received
			return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-calls:
		if received.ID != task.ID || received.ID == uuid.Nil || received.WorkflowID != task.WorkflowID ||
			received.WorkflowID == uuid.Nil || received.LeaseOwner != owner || received.FencingToken != task.FencingToken ||
			received.ConfigEpoch != task.ConfigEpoch || received.Operation == nil || received.Operation.ID != "inspect" {
			t.Fatalf("handler received a non-durable or spoofable identity: got=%+v durable=%+v", received, task)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked with a durable task")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

type capturingWorkflowStore struct {
	mu        sync.Mutex
	requests  []corecoord.WorkflowRequest
	startErr  error
	getErr    error
	cancelErr error
}

func (s *capturingWorkflowStore) StartWorkflow(_ context.Context, request corecoord.WorkflowRequest) (corecoord.Workflow, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	if s.startErr != nil {
		return corecoord.Workflow{}, false, s.startErr
	}
	return corecoord.Workflow{ID: request.ID, OwnerClassID: request.OwnerClassID,
		RootTaskID: request.Root.ID, State: "queued"}, true, nil
}

func (s *capturingWorkflowStore) GetWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID) (corecoord.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return corecoord.Workflow{}, s.getErr
}

func (s *capturingWorkflowStore) CancelWorkflow(context.Context, string, uuid.UUID, int64, uuid.UUID, time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelErr
}

func (s *capturingWorkflowStore) copyRequests() []corecoord.WorkflowRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]corecoord.WorkflowRequest(nil), s.requests...)
}

func workflowClientForGaps(t *testing.T, store *capturingWorkflowStore) *WorkflowClient {
	t.Helper()
	client, err := NewWorkflowClient(WorkflowClientConfig{Store: store, TaskClass: "engine.example", TaskClasses: []string{"engine.example"},
		InstanceID: uuid.New(), ConfigEpoch: 5,
		MaxAttempts: 3, Retention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestWorkflowStartCannotBeSpoofedByPayloadOrCallerMetadata(t *testing.T) {
	store := &capturingWorkflowStore{}
	client := workflowClientForGaps(t, store)
	spoofedTask := uuid.New()
	payload := json.RawMessage(`{"task_id":"` + spoofedTask.String() + `","workflow_id":"` + uuid.NewString() +
		`","owner":"engine.other","config_epoch":999,"handler":"evil"}`)
	if _, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "process", ContractVersion: 1,
		IdempotencyKey: "spoof:one", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	requests := store.copyRequests()
	if len(requests) != 1 {
		t.Fatalf("workflow start reached the store %d times", len(requests))
	}
	request := requests[0]
	if request.Owner == uuid.Nil || request.ConfigEpoch != 5 || request.Root.ID == uuid.Nil || request.Root.ID == spoofedTask ||
		request.Root.WorkflowID != request.ID || request.Root.ParentTaskID != uuid.Nil || request.Root.EmissionKey != "" ||
		request.Root.Kind != corecoord.TaskKindOperation || request.Root.Operation == nil ||
		request.Root.Operation.ClassID != "engine.example" || request.Root.Operation.ID != "process" ||
		request.Root.Operation.Version != 1 {
		t.Fatalf("caller payload or metadata replaced server-derived workflow identity: %+v", request)
	}
	if len(request.InputHash) != 32 {
		t.Fatalf("fingerprint was not derived from the registered operation: %x", request.InputHash)
	}
	if _, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "process", ContractVersion: 1,
		IdempotencyKey: "spoof:one", Payload: json.RawMessage(`{"task_id":"other"}`)}); err != nil {
		t.Fatal(err)
	}
	requests = store.copyRequests()
	if len(requests) != 2 || string(requests[0].InputHash) == string(requests[1].InputHash) {
		t.Fatalf("different input did not produce a different fingerprint: %+v", requests)
	}
}

func TestWorkflowClientPropagatesStoreOutagesForGetCancelAndStart(t *testing.T) {
	outage := errors.New("store unavailable")
	store := &capturingWorkflowStore{startErr: outage, getErr: outage, cancelErr: outage}
	client := workflowClientForGaps(t, store)
	workflowID := uuid.New()
	if _, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "process", ContractVersion: 1,
		IdempotencyKey: "outage:start", Payload: json.RawMessage(`{}`)}); !errors.Is(err, outage) {
		t.Fatalf("start outage was not propagated: %v", err)
	}
	if _, err := client.Get(context.Background(), workflowID); !errors.Is(err, outage) {
		t.Fatalf("read outage was not propagated: %v", err)
	}
	if err := client.Cancel(context.Background(), workflowID); !errors.Is(err, outage) {
		t.Fatalf("cancel outage was not propagated: %v", err)
	}
}

type firstStartFailingStore struct {
	*serviceStore
	mu       sync.Mutex
	failNext bool
}

func (s *firstStartFailingStore) RegisterInstance(ctx context.Context, id uuid.UUID, fingerprint string, lease, retention time.Duration) (int64, error) {
	s.mu.Lock()
	fail := s.failNext
	s.failNext = false
	s.mu.Unlock()
	if fail {
		return 0, errors.New("registration store outage")
	}
	return s.serviceStore.RegisterInstance(ctx, id, fingerprint, lease, retention)
}

func TestServiceStartFailureIsRetryableAndDoesNotLeakLease(t *testing.T) {
	base := &serviceStore{}
	store := &firstStartFailingStore{serviceStore: base, failNext: true}
	config := serviceTestConfig(base)
	config.Store = store
	service, err := NewService(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err == nil {
		t.Fatal("registration outage did not fail service startup")
	}
	base.mu.Lock()
	leaked := base.lease
	base.mu.Unlock()
	if leaked {
		t.Fatal("failed startup left an instance lease registered")
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("service could not retry startup after a registration outage: %v", err)
	}
	if err := service.CheckStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsMissingOperationExecutor(t *testing.T) {
	config := serviceTestConfig(&serviceStore{})
	config.Pools[0].Executor = nil
	if _, err := NewService(config); !errors.Is(err, ErrInvalid) {
		t.Fatalf("service accepted an enabled pool without an operation executor: %v", err)
	}
}

func TestTaskOutcomeRejectsCombinedDependencyLimitAndRepeatedRefs(t *testing.T) {
	t.Run("repeated dependency", func(t *testing.T) {
		dependency := uuid.New()
		outcome := corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: []corecoord.TaskChild{{
			Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
			EmissionKey: "child", Payload: json.RawMessage(`{}`), MaxAttempts: 1,
			DependsOn: []uuid.UUID{dependency, dependency},
		}}}
		if validTaskOutcome(outcome, 8) {
			t.Fatal("repeated dependency reference was accepted")
		}
	})
	t.Run("combined dependency limit", func(t *testing.T) {
		const siblings = 200
		children := make([]corecoord.TaskChild, 0, siblings+1)
		emissions := make([]string, 0, siblings)
		for index := 0; index < siblings; index++ {
			key := "emission:" + uuid.NewString()
			emissions = append(emissions, key)
			children = append(children, corecoord.TaskChild{
				Kind: corecoord.TaskKindOperation, Operation: &corecoord.OperationRef{ClassID: "worker.example", ID: "run", Version: 1},
				EmissionKey: key, Payload: json.RawMessage(`{}`), MaxAttempts: 1,
			})
		}
		children = append(children, corecoord.TaskChild{
			Kind: corecoord.TaskKindJoin, EmissionKey: "barrier", Payload: json.RawMessage(`{}`),
			DependsOn:          make([]uuid.UUID, siblings),
			DependsOnEmissions: emissions,
		})
		for index := 0; index < siblings; index++ {
			children[len(children)-1].DependsOn[index] = uuid.New()
		}
		if validTaskOutcome(corecoord.TaskOutcome{Result: json.RawMessage(`{}`), Children: children}, 256) {
			t.Fatal("200 explicit dependencies plus 200 emission references exceeded the 256-edge bound")
		}
	})
}

func TestRetryDelayIsBoundedAndDeterministic(t *testing.T) {
	cfg := workerTestConfig(uuid.New())
	cfg.RetryBase = 100 * time.Millisecond
	cfg.RetryMaximum = time.Second
	taskID := uuid.New()
	for attempt := 1; attempt <= 12; attempt++ {
		first := retryDelay(cfg, taskID, attempt)
		second := retryDelay(cfg, taskID, attempt)
		if first != second || first < 0 || first > cfg.RetryMaximum {
			t.Fatalf("retry delay for attempt %d is unbounded or unstable: first=%s second=%s max=%s", attempt, first, second, cfg.RetryMaximum)
		}
	}
}

func TestTaskFailureNormalizesUnboundedErrorCodes(t *testing.T) {
	failure := NewTaskFailure("database password leaked\n/private/path", true)
	if failure.Code != "task_failed" || !failure.Retryable {
		t.Fatalf("unbounded handler error was not normalized: %+v", failure)
	}
}
