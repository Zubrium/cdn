package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corecoord "github.com/Zubrium/cdn/core/domain/coordination"
	"github.com/google/uuid"
)

type serviceStore struct {
	mu          sync.Mutex
	lease       bool
	leaseEpoch  int64
	leaseOwner  uuid.UUID
	released    bool
	startCalls  int
	cancelCalls int
	cancelOwner uuid.UUID
	cancelEpoch int64
	workflow    corecoord.Workflow
	workflowErr error
}

func (s *serviceStore) RegisterInstance(ctx context.Context, owner uuid.UUID, _ string, _ time.Duration, _ time.Duration) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lease {
		return 0, ErrInvalid
	}
	s.lease, s.leaseEpoch, s.leaseOwner = true, 1, owner
	return s.leaseEpoch, nil
}

func (s *serviceStore) RenewInstance(ctx context.Context, _ uuid.UUID, epoch int64, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lease || epoch != s.leaseEpoch {
		return ErrInstanceNotReady
	}
	return nil
}

func (s *serviceStore) ReleaseInstance(_ context.Context, _ uuid.UUID, epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.leaseEpoch {
		return ErrInstanceNotReady
	}
	s.lease, s.released = false, true
	return nil
}

func (s *serviceStore) Claim(context.Context, uuid.UUID, int64, []string, int, time.Duration, time.Duration) ([]Task, error) {
	return nil, nil
}
func (*serviceStore) Extend(context.Context, uuid.UUID, uuid.UUID, int64, int64, time.Duration) error {
	return nil
}
func (*serviceStore) Complete(context.Context, uuid.UUID, uuid.UUID, int64, int64, int, int, time.Duration, corecoord.TaskOutcome) error {
	return nil
}
func (*serviceStore) Fail(context.Context, uuid.UUID, uuid.UUID, int64, int64, string, bool, time.Duration, time.Duration) error {
	return nil
}

func (*serviceStore) ListWorkflows(context.Context, corecoord.WorkflowListRequest) (corecoord.WorkflowPage, error) {
	return corecoord.WorkflowPage{}, nil
}
func (*serviceStore) GetWorkflowDetail(context.Context, uuid.UUID, int64, uuid.UUID, int, *corecoord.TaskCursor) (corecoord.WorkflowDetail, error) {
	return corecoord.WorkflowDetail{}, nil
}
func (*serviceStore) RetryTask(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) error {
	return nil
}

func (s *serviceStore) StartWorkflow(_ context.Context, request corecoord.WorkflowRequest) (corecoord.Workflow, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCalls++
	if s.workflowErr != nil {
		return corecoord.Workflow{}, false, s.workflowErr
	}
	if s.workflow.ID == uuid.Nil {
		s.workflow = corecoord.Workflow{ID: request.ID, OwnerClassID: request.OwnerClassID,
			RootTaskID: request.Root.ID, State: "queued"}
	}
	return s.workflow, true, nil
}
func (s *serviceStore) GetWorkflow(_ context.Context, owner string, instance uuid.UUID, epoch int64, id uuid.UUID) (corecoord.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lease || instance != s.leaseOwner || epoch != s.leaseEpoch {
		return corecoord.Workflow{}, corecoord.ErrLeaseLost
	}
	if owner != s.workflow.OwnerClassID || id != s.workflow.ID {
		return corecoord.Workflow{}, corecoord.ErrTaskMissing
	}
	return s.workflow, nil
}
func (s *serviceStore) CancelWorkflow(_ context.Context, owner string, instance uuid.UUID, epoch int64, _ uuid.UUID, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lease || instance != s.leaseOwner || epoch != s.leaseEpoch {
		return corecoord.ErrLeaseLost
	}
	if owner != s.workflow.OwnerClassID {
		return corecoord.ErrTaskMissing
	}
	s.cancelCalls++
	s.cancelOwner, s.cancelEpoch = instance, epoch
	return nil
}

func serviceTestConfig(store *serviceStore) ServiceConfig {
	return ServiceConfig{Store: store, Fingerprint: strings.Repeat("a", 64), InstanceLease: 6 * time.Second,
		HeartbeatInterval: time.Second, RetryBase: time.Second, RetryMaximum: 5 * time.Second,
		MaxAttempts: 3, MaxWorkflowTasks: 100, MaxChildrenPerTask: 10, WorkflowRetention: time.Hour,
		TaskClasses: []string{"engine.example"},
		Pools: []TaskPoolConfig{{
			ClassID: "engine.example", Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second,
			TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{
				operationHandlerKey("engine.example", "process", 1): taskHandlerForTest,
			}),
		}},
	}
}

func TestCoordinatorServiceLifecycleAndScopedWorkflowCapability(t *testing.T) {
	store := &serviceStore{}
	service, err := NewService(serviceTestConfig(store))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.Ready() {
		t.Fatal("Coordinator reported fully ready before final worker activation")
	}
	if err := service.CheckStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Scoped workflow capabilities are available before final worker
	// activation; engines construct them during composition, while Admin
	// control is registered during the same window.
	if _, err := service.NewWorkflowControl(); err != nil {
		t.Fatalf("read-only workflow control was unavailable during startup: %v", err)
	}
	if _, err := service.NewWorkflowClient("engine.example"); err != nil {
		t.Fatalf("scoped workflow client was unavailable during startup: %v", err)
	}
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.Ready() {
		t.Fatal("Coordinator did not report ready after final worker activation")
	}
	client, err := service.NewWorkflowClient("engine.example")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "process", ContractVersion: 1,
		IdempotencyKey: "process:one", Payload: json.RawMessage(`{"value":"opaque"}`)})
	if err != nil || receipt.WorkflowID == uuid.Nil || receipt.RootTaskID == uuid.Nil || !receipt.Created {
		t.Fatalf("registered engine operation did not create a durable workflow: receipt=%+v err=%v", receipt, err)
	}
	if _, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "opaque.operation", ContractVersion: 1,
		IdempotencyKey: "process:two", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("Core rejected a syntactically valid opaque operation identity: %v", err)
	}
	if _, err := service.NewWorkflowClient("content-pool"); !errors.Is(err, ErrTaskClassUnavailable) {
		t.Fatalf("client was issued for a task class outside the composition: %v", err)
	}
	if _, err := client.Get(context.Background(), receipt.WorkflowID); err != nil {
		t.Fatalf("active capability could not read its workflow: %v", err)
	}
	if err := client.Cancel(context.Background(), receipt.WorkflowID); err != nil {
		t.Fatalf("active capability could not cancel its workflow: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.released || store.startCalls != 2 || store.cancelCalls != 1 || store.cancelOwner == uuid.Nil || store.cancelEpoch != 1 {
		t.Fatalf("service lifecycle or scoped workflow operations incomplete: released=%v starts=%d cancels=%d owner=%s epoch=%d", store.released, store.startCalls, store.cancelCalls, store.cancelOwner, store.cancelEpoch)
	}
}

type isolatedPoolStore struct {
	*serviceStore
	mu          sync.Mutex
	claims      map[string]int
	tasks       map[string]Task
	completions map[string]int
	byID        map[uuid.UUID]string
}

func newIsolatedPoolStore(classes ...string) *isolatedPoolStore {
	store := &isolatedPoolStore{
		serviceStore: &serviceStore{leaseOwner: uuid.New()},
		claims:       make(map[string]int),
		tasks:        make(map[string]Task),
		completions:  make(map[string]int),
		byID:         make(map[uuid.UUID]string),
	}
	for _, class := range classes {
		taskID := uuid.New()
		store.tasks[class] = Task{ID: taskID, WorkflowID: uuid.New(), Kind: corecoord.TaskKindOperation,
			Operation: &corecoord.OperationRef{ClassID: class, ID: "process", Version: 1},
			State:     "queued", Attempts: 1, MaxAttempts: 3, ConfigEpoch: 1}
		store.byID[taskID] = class
	}
	return store
}

func (s *isolatedPoolStore) Claim(ctx context.Context, owner uuid.UUID, epoch int64, classes []string, limit int, _ time.Duration, _ time.Duration) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(classes) != 1 || limit != 1 {
		return nil, fmt.Errorf("isolated pools must claim exactly one class and one task: %v", classes)
	}
	class := classes[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims[class]++
	if s.claims[class] > 1 {
		return nil, nil
	}
	task, ok := s.tasks[class]
	if !ok {
		return nil, fmt.Errorf("unexpected claim scope %q", class)
	}
	task.LeaseOwner, task.ConfigEpoch, task.FencingToken = owner, epoch, 1
	return []Task{task}, nil
}

func (s *isolatedPoolStore) Complete(_ context.Context, taskID uuid.UUID, _ uuid.UUID, _ int64, _ int64, _, _ int, _ time.Duration, _ corecoord.TaskOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completions[s.byID[taskID]]++
	return nil
}

func TestCoordinatorServiceActivatesIsolatedClassPools(t *testing.T) {
	store := newIsolatedPoolStore("worker.media", "worker.prerender", "engine.website")
	cfg := serviceTestConfig(store.serviceStore)
	cfg.Store = store
	cfg.TaskClasses = []string{"worker.archive", "worker.media", "worker.prerender", "engine.website"}
	cfg.Pools = []TaskPoolConfig{
		{ClassID: "worker.archive", Enabled: false, Concurrency: 1, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond},
		{ClassID: "worker.media", Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{operationHandlerKey("worker.media", "process", 1): taskHandlerForTest})},
		{ClassID: "worker.prerender", Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{operationHandlerKey("worker.prerender", "process", 1): taskHandlerForTest})},
		{ClassID: "engine.website", Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 10 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{operationHandlerKey("engine.website", "process", 1): taskHandlerForTest})},
	}
	service, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.CheckStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.Ready() {
		t.Fatal("isolated pools reported ready before activation")
	}
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.Ready() {
		t.Fatal("isolated pools did not become ready after activation")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		complete := len(store.completions) == 3
		store.mu.Unlock()
		if complete {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, class := range []string{"worker.media", "worker.prerender", "engine.website"} {
		if store.claims[class] == 0 || store.completions[class] != 1 {
			t.Fatalf("pool %s: claims=%d completions=%d", class, store.claims[class], store.completions[class])
		}
	}
	if store.claims["worker.archive"] != 0 || store.completions["worker.archive"] != 0 {
		t.Fatalf("disabled pool was claimed: %+v %+v", store.claims, store.completions)
	}
}

func TestCoordinatorServiceFailedActivationIsTerminal(t *testing.T) {
	store := &serviceStore{}
	cfg := serviceTestConfig(store)
	cfg.TaskClasses = []string{"engine.example", "engine.second"}
	cfg.Pools = append(cfg.Pools, TaskPoolConfig{
		ClassID: "engine.second", Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second,
		TaskTimeout: time.Minute, PollInterval: 100 * time.Millisecond,
		Executor: testExecutorFor(map[string]TestTaskHandler{
			operationHandlerKey("engine.second", "process", 1): taskHandlerForTest,
		}),
	})
	service, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := service.executors[1]
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = second.Close(ctx)
	}()
	if err := service.Activate(context.Background()); !errors.Is(err, ErrTaskExecutorAlreadyStarted) {
		t.Fatalf("activation with an already started pool returned %v", err)
	}
	if service.Ready() {
		t.Fatal("service reported Ready after a failed pool activation")
	}
	if err := service.Activate(context.Background()); !errors.Is(err, ErrInstanceNotReady) {
		t.Fatalf("retry after failed activation returned %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type saturationStore struct {
	*serviceStore
	mu        sync.Mutex
	queues    map[string][]Task
	active    map[string]int
	peak      map[string]int
	completed map[string]int
	byID      map[uuid.UUID]string
}

func newSaturationStore(queues map[string][]Task) *saturationStore {
	store := &saturationStore{
		serviceStore: &serviceStore{leaseOwner: uuid.New()},
		queues:       queues, active: make(map[string]int), peak: make(map[string]int),
		completed: make(map[string]int), byID: make(map[uuid.UUID]string),
	}
	for class, tasks := range queues {
		for _, task := range tasks {
			store.byID[task.ID] = class
		}
		_ = class
	}
	return store
}

func (s *saturationStore) Claim(ctx context.Context, owner uuid.UUID, epoch int64, classes []string, limit int, _ time.Duration, _ time.Duration) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(classes) != 1 || limit != 1 {
		return nil, fmt.Errorf("each isolated pool must claim its own class only: %v", classes)
	}
	class := classes[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.queues[class]
	if len(queue) == 0 {
		return nil, nil
	}
	task := queue[0]
	s.queues[class] = queue[1:]
	s.active[class]++
	if s.active[class] > s.peak[class] {
		s.peak[class] = s.active[class]
	}
	task.LeaseOwner, task.ConfigEpoch, task.FencingToken = owner, epoch, 1
	return []Task{task}, nil
}

func (s *saturationStore) Complete(_ context.Context, taskID uuid.UUID, _ uuid.UUID, _ int64, _ int64, _, _ int, _ time.Duration, _ corecoord.TaskOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	class := s.byID[taskID]
	s.active[class]--
	s.completed[class]++
	return nil
}

func TestCoordinatorServiceIsolatesSaturatedWorkerPool(t *testing.T) {
	const (
		mediaClass = "worker.media"
		prerClass  = "worker.prerender"
		pubClass   = "engine.website"
	)
	newTasks := func(class string, count int) []Task {
		tasks := make([]Task, 0, count)
		for range count {
			tasks = append(tasks, Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: corecoord.TaskKindOperation,
				Operation: &corecoord.OperationRef{ClassID: class, ID: "process", Version: 1},
				State:     "queued", Attempts: 1, MaxAttempts: 3, ConfigEpoch: 1})
		}
		return tasks
	}
	store := newSaturationStore(map[string][]Task{
		mediaClass: newTasks(mediaClass, 2), prerClass: newTasks(prerClass, 2), pubClass: newTasks(pubClass, 1),
	})
	mediaStarted := make(chan struct{}, 1)
	releaseMedia := make(chan struct{})
	cfg := serviceTestConfig(store.serviceStore)
	cfg.Store = store
	cfg.TaskClasses = []string{mediaClass, prerClass, pubClass}
	cfg.Pools = []TaskPoolConfig{
		{ClassID: mediaClass, Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 5 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{operationHandlerKey(mediaClass, "process", 1): func(ctx context.Context, _ Task) (corecoord.TaskOutcome, error) {
				select {
				case mediaStarted <- struct{}{}:
				default:
				}
				select {
				case <-releaseMedia:
					return corecoord.TaskOutcome{Result: json.RawMessage(`{}`)}, nil
				case <-ctx.Done():
					return corecoord.TaskOutcome{}, ctx.Err()
				}
			}})},
		{ClassID: prerClass, Enabled: true, Concurrency: 2, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 5 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{operationHandlerKey(prerClass, "process", 1): taskHandlerForTest})},
		{ClassID: pubClass, Enabled: true, Concurrency: 1, LeaseDuration: 4 * time.Second, TaskTimeout: time.Minute, PollInterval: 5 * time.Millisecond,
			Executor: testExecutorFor(map[string]TestTaskHandler{operationHandlerKey(pubClass, "process", 1): taskHandlerForTest})},
	}
	service, err := NewService(cfg)
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
	case <-mediaStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("saturated media pool did not start its single task")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		done := store.completed[prerClass] == 2 && store.completed[pubClass] == 1
		store.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	store.mu.Lock()
	snapshot := struct {
		mediaCompleted, prerCompleted, pubCompleted int
		mediaPeak, prerPeak, pubPeak                int
	}{store.completed[mediaClass], store.completed[prerClass], store.completed[pubClass],
		store.peak[mediaClass], store.peak[prerClass], store.peak[pubClass]}
	store.mu.Unlock()
	if snapshot.prerCompleted != 2 || snapshot.pubCompleted != 1 {
		t.Fatalf("saturated media pool blocked sibling pools: %+v", snapshot)
	}
	if snapshot.mediaCompleted != 0 || snapshot.mediaPeak != 1 {
		t.Fatalf("media pool borrowed another pool's capacity: %+v", snapshot)
	}
	if snapshot.prerPeak > 2 || snapshot.pubPeak > 1 {
		t.Fatalf("sibling pool exceeded its configured limit: %+v", snapshot)
	}
	close(releaseMedia)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		done := store.completed[mediaClass] == 2
		store.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	store.mu.Lock()
	mediaDone := store.completed[mediaClass]
	store.mu.Unlock()
	if mediaDone != 2 {
		t.Fatalf("media pool did not drain after release: %d", mediaDone)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
