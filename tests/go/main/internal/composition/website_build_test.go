package composition

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/main/internal/bus"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/main/internal/contentbridge"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type bridgeWorkflowStub struct {
	workflowID uuid.UUID
	rootID     uuid.UUID
	childID    uuid.UUID
	starts     []contracts.WorkflowStartInput
	locale     contracts.ContentLocaleCatalogResponse
	build      contracts.ContentBuildProjectionResponse
}

func newBridgeWorkflowStub() *bridgeWorkflowStub {
	return &bridgeWorkflowStub{workflowID: uuid.New(), rootID: uuid.New(), childID: uuid.New()}
}

func (stub *bridgeWorkflowStub) Start(_ context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	stub.starts = append(stub.starts, input)
	return contracts.WorkflowStartReceipt{WorkflowID: stub.workflowID, RootTaskID: stub.rootID}, nil
}

func (stub *bridgeWorkflowStub) Get(_ context.Context, id uuid.UUID) (contracts.WorkflowState, error) {
	return contracts.WorkflowState{ID: id, RootTaskID: stub.rootID, State: "succeeded"}, nil
}

func (stub *bridgeWorkflowStub) GetTaskResult(_ context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	result, err := json.Marshal(contracts.ContentBridgeRootResult{
		Format: contracts.ContentLocaleWorkflowFormat, EmissionKey: contracts.ContentLocaleEmissionKey,
	})
	if err != nil {
		return contracts.WorkflowTaskResult{}, err
	}
	if taskID == stub.rootID {
		return contracts.WorkflowTaskResult{TaskID: taskID, WorkflowID: workflowID, State: "succeeded", Result: result,
			Operation: &contracts.WorkflowOperationRef{ClassID: contentBridgeTaskClassWebsite, ID: contracts.ContentLocaleOperationID, Version: contracts.ContentBridgeRootVersion}}, nil
	}
	payload, err := json.Marshal(stub.locale)
	if err != nil {
		return contracts.WorkflowTaskResult{}, err
	}
	return contracts.WorkflowTaskResult{TaskID: taskID, WorkflowID: workflowID, State: "succeeded", Result: payload,
		Operation: &contracts.WorkflowOperationRef{ClassID: contentBridgeTaskClassContent, ID: contracts.ContentLocaleReadOperationID, Version: contracts.ContentBridgeChildVersion}}, nil
}

func (stub *bridgeWorkflowStub) ResolveTaskID(_ context.Context, _ uuid.UUID, _ string) (uuid.UUID, error) {
	return stub.childID, nil
}

func TestWebsiteLocaleCatalogUsesNeutralWorkflowBridge(t *testing.T) {
	stub := newBridgeWorkflowStub()
	stub.locale = contracts.ContentLocaleCatalogResponse{Revision: 4, Locales: []string{"en-US", "ru-RU"}}
	bridge, err := contentbridge.NewClient(stub)
	if err != nil {
		t.Fatal(err)
	}
	resolver := newWebsiteLocaleCatalog(bridge)
	revision, err := resolver.LocaleCatalogRevision(context.Background(), "ru-RU")
	if err != nil || revision != 4 {
		t.Fatalf("configured locale revision = %d, %v", revision, err)
	}
	if _, err := resolver.LocaleCatalogRevision(context.Background(), "fr-FR"); err == nil {
		t.Fatal("locale outside the catalog was accepted")
	}
	if len(stub.starts) != 2 || stub.starts[0].OperationID != contracts.ContentLocaleOperationID ||
		stub.starts[0].ContractVersion != contracts.ContentBridgeRootVersion {
		t.Fatalf("workflow starts = %+v", stub.starts)
	}
	empty := newWebsiteLocaleCatalog(nil)
	if _, err := empty.LocaleCatalogRevision(context.Background(), "en-US"); err == nil {
		t.Fatal("locale catalog without a workflow bridge was accepted")
	}
}

func TestContentBridgeWorkersAreClassScoped(t *testing.T) {
	bindings := map[contracts.Key]TaskOperationBinding{}
	config := appconfig.Resolved{}
	config.Coordinator.MaxAttempts = 3
	if err := registerContentBridgeWorkerHandlers(func(classID, operationID string, version uint32, _ bool, handler bus.TaskOperationHandler) error {
		bindings[contracts.Key{ID: operationID, Version: version}] = TaskOperationBinding{TaskClass: classID, Handler: handler}
		return nil
	}, map[string]struct{}{contentBridgeTaskClassContent: {}, contentBridgeTaskClassWebsite: {}}, config, &coreComponent{}); err != nil {
		t.Fatal(err)
	}
	want := map[contracts.Key]string{
		{ID: contracts.ContentProjectionOperationID, Version: contracts.ContentBridgeRootVersion}:       contentBridgeTaskClassContent,
		{ID: contracts.WebsiteProjectionOperationID, Version: contracts.ContentBridgeChildVersion}:      contentBridgeTaskClassWebsite,
		{ID: contracts.ContentBuildProjectionOperationID, Version: contracts.ContentBridgeChildVersion}: contentBridgeTaskClassWebsite,
		{ID: contracts.ContentBuildOperationID, Version: contracts.ContentBridgeRootVersion}:            contentBridgeTaskClassWebsite,
		{ID: contracts.ContentBuildResolveOperationID, Version: contracts.ContentBridgeChildVersion}:    contentBridgeTaskClassContent,
		{ID: contracts.ContentLocaleOperationID, Version: contracts.ContentBridgeRootVersion}:           contentBridgeTaskClassWebsite,
		{ID: contracts.ContentLocaleReadOperationID, Version: contracts.ContentBridgeChildVersion}:      contentBridgeTaskClassContent,
	}
	if len(bindings) != len(want) {
		t.Fatalf("bindings = %d, want %d", len(bindings), len(want))
	}
	dispatcher, err := bus.NewAllowlisted(bus.ConcurrencyLimits{MaxInFlight: 4})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewClassScopedDataBusTaskExecutor(dispatcher, bindings, contentBridgeTaskClassWebsite)
	if err != nil {
		t.Fatal(err)
	}
	for key, classID := range want {
		task := platformTaskForBridge(key, classID)
		_, err := executor.Execute(context.Background(), task)
		if classID == contentBridgeTaskClassWebsite && key.ID == contracts.WebsiteProjectionOperationID {
			if err == nil {
				t.Fatal("website worker pool executed a task without its compiled engine dependency")
			}
			continue
		}
		if classID == contentBridgeTaskClassWebsite {
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "operation_unavailable") {
			t.Fatalf("foreign class task %s: error = %v, want operation_unavailable", key.ID, err)
		}
	}
}

func platformTaskForBridge(key contracts.Key, classID string) platform.Task {
	return platform.Task{ID: uuid.New(), WorkflowID: uuid.New(), Kind: platform.TaskKindOperation,
		Operation: &platform.OperationRef{ClassID: classID, ID: key.ID, Version: int(key.Version)},
		Payload:   []byte(`{"task_id":"foreign","workflow_id":"foreign","payload":{}}`)}
}

func TestWebsiteBuildWorkflowClientFailsClosedWithoutCore(t *testing.T) {
	client := newWebsiteBuildWorkflowClient(nil)
	ctx := context.Background()
	id := uuid.New()
	if _, err := client.Start(ctx, contracts.WorkflowStartInput{}); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("start without Core = %v", err)
	}
	if _, err := client.Get(ctx, id); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("get without Core = %v", err)
	}
	if _, err := client.GetTaskResult(ctx, id, id); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("task result without Core = %v", err)
	}
	if _, err := client.ResolveTaskID(ctx, id, "prerender:child"); !errors.Is(err, errWebsiteBuildUnavailable) {
		t.Fatalf("resolve without Core = %v", err)
	}
}
