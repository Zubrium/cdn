package contentbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

type workflowStub struct {
	workflowID   uuid.UUID
	rootID       uuid.UUID
	childID      uuid.UUID
	starts       []contracts.WorkflowStartInput
	startByKey   map[string]contracts.WorkflowStartReceipt
	startInputs  map[string]contracts.WorkflowStartInput
	stateCalls   int
	resolveCalls int
	resolveMiss  bool
	rootResult   []byte
	childResult  []byte
	rootClass    string
	rootOp       string
	childClass   string
	childOp      string
	childVersion int
}

func newWorkflowStub() *workflowStub {
	return &workflowStub{
		workflowID: uuid.New(), rootID: uuid.New(), childID: uuid.New(),
		startByKey: map[string]contracts.WorkflowStartReceipt{}, startInputs: map[string]contracts.WorkflowStartInput{}, rootClass: "engine.content",
		rootOp:     contracts.ContentProjectionOperationID,
		childClass: "engine.website", childOp: contracts.WebsiteProjectionOperationID,
		childVersion: contracts.ContentBridgeChildVersion,
	}
}

func (stub *workflowStub) Start(_ context.Context, input contracts.WorkflowStartInput) (contracts.WorkflowStartReceipt, error) {
	if prior, ok := stub.startInputs[input.IdempotencyKey]; ok {
		if !bytes.Equal(prior.Payload, input.Payload) {
			return contracts.WorkflowStartReceipt{}, contracts.ErrWorkflowConflict
		}
		return stub.startByKey[input.IdempotencyKey], nil
	}
	stub.starts = append(stub.starts, input)
	receipt := contracts.WorkflowStartReceipt{WorkflowID: stub.workflowID, RootTaskID: stub.rootID, Created: true}
	stub.startByKey[input.IdempotencyKey] = receipt
	stub.startInputs[input.IdempotencyKey] = input
	return receipt, nil
}

func (stub *workflowStub) Get(_ context.Context, id uuid.UUID) (contracts.WorkflowState, error) {
	stub.stateCalls++
	state := "succeeded"
	if stub.stateCalls == 1 {
		state = "queued"
	}
	return contracts.WorkflowState{ID: id, RootTaskID: stub.rootID, State: state}, nil
}

func (stub *workflowStub) GetTaskResult(_ context.Context, workflowID, taskID uuid.UUID) (contracts.WorkflowTaskResult, error) {
	if taskID == stub.rootID {
		return contracts.WorkflowTaskResult{TaskID: taskID, WorkflowID: workflowID, State: "succeeded", Result: stub.rootResult,
			Operation: &contracts.WorkflowOperationRef{ClassID: stub.rootClass, ID: stub.rootOp, Version: contracts.ContentBridgeRootVersion}}, nil
	}
	return contracts.WorkflowTaskResult{TaskID: taskID, WorkflowID: workflowID, State: "succeeded", Result: stub.childResult,
		Operation: &contracts.WorkflowOperationRef{ClassID: stub.childClass, ID: stub.childOp, Version: stub.childVersion}}, nil
}

func (stub *workflowStub) ResolveTaskID(_ context.Context, _ uuid.UUID, _ string) (uuid.UUID, error) {
	stub.resolveCalls++
	if stub.resolveMiss && stub.resolveCalls == 1 {
		return uuid.Nil, contracts.ErrWorkflowTaskMissing
	}
	return stub.childID, nil
}

func projectionResult(snapshotID, locale string) []byte {
	raw, err := json.Marshal(contracts.ContentProjectionResponse{
		Format: contracts.WebsiteProjectionResultFormat,
		Projection: contracts.ContentProjection{
			Locale: locale, Page: contracts.PagePlacementProjection{PageSnapshotID: snapshotID, PageID: "page-1"},
		},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func clientForTest(stub *workflowStub) *Client {
	client, err := NewClient(stub, "test-configuration")
	if err != nil {
		panic(err)
	}
	client.pollInterval = time.Millisecond
	return client
}

func TestProjectionBridgeStartsNeutralRootAndReadsOnlyDeclaredChild(t *testing.T) {
	snapshotID := "55555555-5555-4555-8555-555555555555"
	stub := newWorkflowStub()
	stub.resolveMiss = true
	stub.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{
		Format:      contracts.ContentProjectionWorkflowFormat,
		EmissionKey: contracts.ContentProjectionEmissionKey(snapshotID, "en-US"),
	})
	stub.childResult = projectionResult(snapshotID, "en-US")
	client := clientForTest(stub)
	request := contracts.ContentProjectionRequest{PageSnapshotID: snapshotID, Locale: "en-US", AllowProvisioning: true}
	response, err := client.ReadProjection(context.Background(), request)
	if err != nil || response.Projection.Page.PageSnapshotID != snapshotID {
		t.Fatalf("projection = %+v, %v", response, err)
	}
	if len(stub.starts) != 1 || stub.starts[0].OperationID != contracts.ContentProjectionOperationID ||
		stub.starts[0].ContractVersion != contracts.ContentBridgeRootVersion {
		t.Fatalf("workflow starts = %+v", stub.starts)
	}
	if stub.resolveCalls != 2 {
		t.Fatalf("resolve calls = %d, want 2", stub.resolveCalls)
	}
	if _, err := client.ReadProjection(context.Background(), request); err != nil {
		t.Fatalf("idempotent second read failed: %v", err)
	}
	if len(stub.starts) != 1 {
		t.Fatalf("idempotency key was not honored: %d starts", len(stub.starts))
	}
}

func TestProjectionBridgeSeparatesProvisioningFromReadOnlyIdempotency(t *testing.T) {
	snapshotID := "44444444-4444-4444-8444-444444444444"
	stub := newWorkflowStub()
	stub.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{
		Format:      contracts.ContentProjectionWorkflowFormat,
		EmissionKey: contracts.ContentProjectionEmissionKey(snapshotID, "en-US"),
	})
	stub.childResult = projectionResult(snapshotID, "en-US")
	client := clientForTest(stub)

	if _, err := client.ReadProjection(context.Background(), contracts.ContentProjectionRequest{
		PageSnapshotID: snapshotID, Locale: "en-US", AllowProvisioning: true,
	}); err != nil {
		t.Fatalf("provisioning projection read failed: %v", err)
	}
	if _, err := client.ReadProjection(context.Background(), contracts.ContentProjectionRequest{
		PageSnapshotID: snapshotID, Locale: "en-US",
	}); err != nil {
		t.Fatalf("read-only projection read after provisioning failed: %v", err)
	}
	if len(stub.starts) != 2 {
		t.Fatalf("workflow starts = %d, want 2 distinct projection workflows", len(stub.starts))
	}
	if stub.starts[0].IdempotencyKey == stub.starts[1].IdempotencyKey {
		t.Fatalf("provisioning and read-only reads share idempotency key %q", stub.starts[0].IdempotencyKey)
	}
	if _, err := client.ReadProjection(context.Background(), contracts.ContentProjectionRequest{
		PageSnapshotID: snapshotID, Locale: "en-US", AllowProvisioning: true,
	}); err != nil {
		t.Fatalf("repeated provisioning read failed: %v", err)
	}
	if len(stub.starts) != 2 {
		t.Fatalf("provisioning idempotency was lost: %d starts", len(stub.starts))
	}
}

func TestProjectionBridgeScopesIdempotencyToConfiguration(t *testing.T) {
	snapshotID := "66666666-6666-4666-8666-666666666666"
	stub := newWorkflowStub()
	stub.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{
		Format:      contracts.ContentProjectionWorkflowFormat,
		EmissionKey: contracts.ContentProjectionEmissionKey(snapshotID, "en-US"),
	})
	stub.childResult = projectionResult(snapshotID, "en-US")
	request := contracts.ContentProjectionRequest{PageSnapshotID: snapshotID, Locale: "en-US"}

	first, err := NewClient(stub, "configuration-a")
	if err != nil {
		t.Fatal(err)
	}
	first.pollInterval = time.Millisecond
	if _, err := first.ReadProjection(context.Background(), request); err != nil {
		t.Fatalf("first configuration read failed: %v", err)
	}
	second, err := NewClient(stub, "configuration-b")
	if err != nil {
		t.Fatal(err)
	}
	second.pollInterval = time.Millisecond
	if _, err := second.ReadProjection(context.Background(), request); err != nil {
		t.Fatalf("second configuration read failed: %v", err)
	}
	if len(stub.starts) != 2 {
		t.Fatalf("workflow starts = %d, want one per configuration", len(stub.starts))
	}
	if stub.starts[0].IdempotencyKey == stub.starts[1].IdempotencyKey {
		t.Fatalf("different configurations share idempotency key %q", stub.starts[0].IdempotencyKey)
	}
	if _, err := first.ReadProjection(context.Background(), request); err != nil {
		t.Fatalf("repeated first configuration read failed: %v", err)
	}
	if len(stub.starts) != 2 {
		t.Fatalf("configuration idempotency was lost: %d starts", len(stub.starts))
	}
}

func TestBuildBridgeBindsRootToBothDeclaredEmissions(t *testing.T) {
	snapshotID := "77777777-7777-4777-8777-777777777777"
	stub := newWorkflowStub()
	stub.rootClass = "engine.website"
	stub.rootOp = contracts.ContentBuildOperationID
	stub.childClass = "engine.content"
	stub.childOp = contracts.ContentBuildResolveOperationID
	stub.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{
		Format: contracts.ContentBuildWorkflowFormat, EmissionKey: contracts.ContentBuildEmissionKey(snapshotID, "en-US"),
		ProjectionEmissionKey: contracts.ContentBuildProjectionEmissionKey(snapshotID, "en-US"),
	})
	stub.childResult, _ = json.Marshal(contracts.ContentBuildProjectionResponse{
		PublicationRevision: 3, SelectionDigest: strings.Repeat("a", 64),
	})
	response, err := clientForTest(stub).ReadBuild(context.Background(),
		contracts.ContentBuildProjectionRequest{PageSnapshotID: snapshotID, Locale: "en-US"})
	if err != nil || response.PublicationRevision != 3 {
		t.Fatalf("build response = %+v, %v", response, err)
	}
	if len(stub.starts) != 1 || stub.starts[0].OperationID != contracts.ContentBuildOperationID {
		t.Fatalf("workflow starts = %+v", stub.starts)
	}
}

func buildBridgeStub(snapshotID string, publicationRevision int64) *workflowStub {
	stub := newWorkflowStub()
	stub.rootClass = "engine.website"
	stub.rootOp = contracts.ContentBuildOperationID
	stub.childClass = "engine.content"
	stub.childOp = contracts.ContentBuildResolveOperationID
	stub.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{
		Format:                contracts.ContentBuildWorkflowFormat,
		EmissionKey:           contracts.ContentBuildEmissionKey(snapshotID, "en-US"),
		ProjectionEmissionKey: contracts.ContentBuildProjectionEmissionKey(snapshotID, "en-US"),
	})
	stub.childResult, _ = json.Marshal(contracts.ContentBuildProjectionResponse{
		PublicationRevision: publicationRevision, SelectionDigest: strings.Repeat("a", 64),
	})
	return stub
}

func TestBuildBridgePinsIdempotencyToExpectedPublicationRevision(t *testing.T) {
	snapshotID := "88888888-8888-4888-8888-888888888888"
	firstStub := buildBridgeStub(snapshotID, 1)
	first := clientForTest(firstStub)
	firstRequest := contracts.ContentBuildProjectionRequest{
		PageSnapshotID: snapshotID, Locale: "en-US", ExpectedPublicationRevision: 1,
	}
	if _, err := first.ReadBuild(context.Background(), firstRequest); err != nil {
		t.Fatalf("revision 1 build read: %v", err)
	}
	secondStub := buildBridgeStub(snapshotID, 2)
	second := clientForTest(secondStub)
	secondRequest := firstRequest
	secondRequest.ExpectedPublicationRevision = 2
	if _, err := second.ReadBuild(context.Background(), secondRequest); err != nil {
		t.Fatalf("revision 2 build read: %v", err)
	}
	if len(firstStub.starts) != 1 || len(secondStub.starts) != 1 {
		t.Fatalf("workflow starts = %d/%d, want one per revision", len(firstStub.starts), len(secondStub.starts))
	}
	if firstStub.starts[0].IdempotencyKey == secondStub.starts[0].IdempotencyKey {
		t.Fatalf("revisions share idempotency key %q", firstStub.starts[0].IdempotencyKey)
	}
	if _, err := first.ReadBuild(context.Background(), firstRequest); err != nil {
		t.Fatalf("repeated revision 1 build read: %v", err)
	}
	if len(firstStub.starts) != 1 {
		t.Fatalf("revision 1 idempotency was lost: %d starts", len(firstStub.starts))
	}
}

func TestBuildBridgeRejectsProjectionFromAnotherPublicationRevision(t *testing.T) {
	snapshotID := "99999999-9999-4999-8999-999999999999"
	stub := buildBridgeStub(snapshotID, 1)
	_, err := clientForTest(stub).ReadBuild(context.Background(), contracts.ContentBuildProjectionRequest{
		PageSnapshotID: snapshotID, Locale: "en-US", ExpectedPublicationRevision: 2,
	})
	if err == nil {
		t.Fatal("build bridge accepted a projection from an unexpected publication revision")
	}
}

func TestBridgeRejectsSubstitutedResultsAndForeignTasks(t *testing.T) {
	snapshotID := "66666666-6666-4666-8666-666666666666"
	validStart := func() *workflowStub {
		stub := newWorkflowStub()
		stub.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{
			Format:      contracts.ContentProjectionWorkflowFormat,
			EmissionKey: contracts.ContentProjectionEmissionKey(snapshotID, "en-US"),
		})
		stub.childResult = projectionResult(snapshotID, "en-US")
		return stub
	}
	cases := []struct {
		name   string
		mutate func(*workflowStub)
	}{
		{name: "substituted snapshot", mutate: func(s *workflowStub) { s.childResult = projectionResult(uuid.NewString(), "en-US") }},
		{name: "substituted locale", mutate: func(s *workflowStub) { s.childResult = projectionResult(snapshotID, "fr-FR") }},
		{name: "foreign task class", mutate: func(s *workflowStub) { s.childClass = "engine.content" }},
		{name: "foreign operation", mutate: func(s *workflowStub) { s.childOp = "engine.website.other" }},
		{name: "foreign version", mutate: func(s *workflowStub) { s.childVersion++ }},
		{name: "substituted root emission", mutate: func(s *workflowStub) {
			s.rootResult, _ = json.Marshal(contracts.ContentBridgeRootResult{Format: contracts.ContentProjectionWorkflowFormat, EmissionKey: "substituted"})
		}},
		{name: "injected module in result", mutate: func(s *workflowStub) {
			s.childResult = []byte(`{"format":"website.content-projection/v1","module_id":"engine.content","projection":{"locale":"en-US"}}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := validStart()
			tc.mutate(stub)
			_, err := clientForTest(stub).ReadProjection(context.Background(),
				contracts.ContentProjectionRequest{PageSnapshotID: snapshotID, Locale: "en-US"})
			if err == nil {
				t.Fatal("substituted or foreign result was accepted")
			}
		})
	}
}

func TestBridgeFailsClosedWithoutWorkflowClient(t *testing.T) {
	client, err := NewClient(nil, "test-configuration")
	if err == nil || client != nil {
		t.Fatal("bridge accepted a nil workflow client")
	}
	var missing *Client
	_, err = missing.ReadProjection(context.Background(), contracts.ContentProjectionRequest{PageSnapshotID: uuid.NewString(), Locale: "en-US"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client read = %v", err)
	}
}
