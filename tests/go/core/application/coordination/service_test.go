package coordinator

import (
	"context"
	"encoding/json"
	"errors"
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

func (s *serviceStore) Claim(context.Context, uuid.UUID, int64, int, time.Duration, time.Duration) ([]Task, error) {
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
		s.workflow = corecoord.Workflow{ID: request.ID, OwnerEngineID: request.OwnerEngineID,
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
	if owner != s.workflow.OwnerEngineID || id != s.workflow.ID {
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
	if owner != s.workflow.OwnerEngineID {
		return corecoord.ErrTaskMissing
	}
	s.cancelCalls++
	s.cancelOwner, s.cancelEpoch = instance, epoch
	return nil
}

func serviceTestConfig(store *serviceStore) ServiceConfig {
	catalog, err := BuildWorkerCatalog(BuildPlan{Components: []string{"core", "engine.example"}}, []ComponentWorkerSet{{
		ComponentID: "engine.example", Handlers: map[string]WorkerHandler{"process/v1": taskHandlerForTest},
	}})
	if err != nil {
		panic(err)
	}
	return ServiceConfig{Store: store, Fingerprint: strings.Repeat("a", 64), InstanceLease: 6 * time.Second,
		HeartbeatInterval: time.Second, ExecutorConcurrency: 1, TaskLease: 4 * time.Second, TaskTimeout: time.Minute,
		PollInterval: 100 * time.Millisecond, RetryBase: time.Second, RetryMaximum: 5 * time.Second,
		MaxAttempts: 3, MaxWorkflowTasks: 100, MaxChildrenPerTask: 10, WorkflowRetention: time.Hour,
		Workers: catalog}
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
	if _, err := client.Start(context.Background(), WorkflowStartRequest{OperationID: "missing", ContractVersion: 1,
		IdempotencyKey: "process:two", Payload: json.RawMessage(`{}`)}); !errors.Is(err, ErrWorkflowOperationUnavailable) {
		t.Fatalf("unregistered operation was accepted: %v", err)
	}
	if _, err := service.NewWorkflowClient("engine.content"); !errors.Is(err, ErrComponentUnavailable) {
		t.Fatalf("client was issued for an unregistered component: %v", err)
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
	if !store.released || store.startCalls != 1 || store.cancelCalls != 1 || store.cancelOwner == uuid.Nil || store.cancelEpoch != 1 {
		t.Fatalf("service lifecycle or scoped workflow operations incomplete: released=%v starts=%d cancels=%d owner=%s epoch=%d", store.released, store.startCalls, store.cancelCalls, store.cancelOwner, store.cancelEpoch)
	}
}
