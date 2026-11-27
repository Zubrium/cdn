package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

var errContentBridgeTaskResultReached = errors.New("content build worker reached task result")

type lateBoundWorkflowOperations struct {
	taskID       uuid.UUID
	resolveCalls int
	moduleIDs    []string
}

func (operations *lateBoundWorkflowOperations) Start(context.Context, contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	return contracts.WorkflowStartReceipt{}, fmt.Errorf("unexpected workflow start")
}

func (operations *lateBoundWorkflowOperations) Get(context.Context, uuid.UUID) (contracts.WorkflowState, error) {
	return contracts.WorkflowState{}, fmt.Errorf("unexpected workflow state")
}

func (operations *lateBoundWorkflowOperations) GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (contracts.WorkflowTaskResult, error) {
	return contracts.WorkflowTaskResult{}, errContentBridgeTaskResultReached
}

func (operations *lateBoundWorkflowOperations) ResolveTaskID(ctx context.Context, _ uuid.UUID, _ string) (uuid.UUID, error) {
	operations.resolveCalls++
	operations.moduleIDs = append(operations.moduleIDs, bus.ModuleIDFromContext(ctx))
	return operations.taskID, nil
}

func TestContentBridgeBuildWorkerUsesDispatcherBoundAfterRegistration(t *testing.T) {
	core := &coreComponent{}
	core.contentHandle.Store(&contentEngineComponent{})
	handlers := map[contracts.Key]bus.TaskOperationHandler{}
	config := appconfig.Resolved{}
	config.Coordinator.MaxAttempts = 3
	if err := registerContentBridgeWorkerHandlers(func(classID, operationID string, version uint32, _ bool, handler bus.TaskOperationHandler) error {
		handlers[contracts.Key{ID: operationID, Version: version}] = handler
		return nil
	}, map[string]struct{}{contentBridgeTaskClassContent: {}, contentBridgeTaskClassWebsite: {}}, config, core); err != nil {
		t.Fatal(err)
	}
	handler := handlers[contracts.Key{ID: contracts.ContentBuildResolveOperationID, Version: contracts.ContentBridgeChildVersion}]
	if handler == nil {
		t.Fatal("content build resolve handler was not registered")
	}

	snapshotID := uuid.NewString()
	workflowID := uuid.New()
	payload, err := json.Marshal(contracts.ContentBuildProjectionRequest{PageSnapshotID: snapshotID, Locale: "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	task := contracts.OperationTask{
		TaskID:      uuid.NewString(),
		WorkflowID:  workflowID.String(),
		EmissionKey: contracts.ContentBuildEmissionKey(snapshotID, "en-US"),
		Payload:     payload,
	}
	if _, err := handler(context.Background(), task); !errors.Is(err, errContentBridgeDependencyUnavailable) {
		t.Fatalf("unbound content build error = %v, want dependency unavailable", err)
	}

	operations := &lateBoundWorkflowOperations{taskID: uuid.New()}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4},
		contracts.WorkflowStartOperation().Key, contracts.WorkflowStateOperation().Key,
		contracts.WorkflowTaskIDResolveOperation().Key, contracts.WorkflowTaskResultOperation().Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterWorkflowOperations(dispatcher, map[string]bus.WorkflowOperations{
		contentBridgeTaskClassContent: operations, contentBridgeTaskClassWebsite: operations,
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Seal(); err != nil {
		t.Fatal(err)
	}
	core.dispatcher = dispatcher
	_, err = handler(context.Background(), task)
	if !errors.Is(err, errContentBridgeTaskResultReached) {
		t.Fatalf("late-bound content build error = %v, want task result lookup", err)
	}
	if operations.resolveCalls != 1 {
		t.Fatalf("resolve calls = %d, want 1", operations.resolveCalls)
	}
	if len(operations.moduleIDs) != 1 || operations.moduleIDs[0] != contentBridgeTaskClassWebsite {
		t.Fatalf("resolve module identities = %v, want [%s] (the bridge workflow owner)", operations.moduleIDs, contentBridgeTaskClassWebsite)
	}
}
