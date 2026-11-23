//go:build website_acceptance
// +build website_acceptance

package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"reflect"
	"sort"
	"strings"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	"github.com/google/uuid"
)

const (
	buildOperationID    = "engine.website.build-page"
	buildRootFormat     = "website.build-root/v1"
	buildTaskFormat     = "website.build-result/v1"
	buildLocale         = "en-US"
	buildIdempotencyKey = "web-build-workflow-key-000001"
	buildWorkflowIDText = "77777777-7777-4777-8777-777777777777"
	buildRootTaskText   = "88888888-8888-4888-8888-888888888888"
	buildPrerenderText  = "55555555-5555-4555-8555-555555555555"
	buildManifestFileID = "66666666-6666-4666-8666-666666666666"
	buildSnapshotDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	buildManifestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	buildStateQueued    = "queued"
	buildStateResolving = "resolving"
	buildStatePrerender = "prerendering"
	buildStateReady     = "ready"
	buildStateFailed    = "failed"
)

type buildWorkflowFake struct {
	startCalls    []core.WorkflowStartRequest
	startErr      error
	workflowID    uuid.UUID
	rootTaskID    uuid.UUID
	workflow      core.Workflow
	getErr        error
	beforeGet     func()
	taskResult    core.TaskResult
	taskErr       error
	taskCalls     int
	resolveCalls  int
	resolvedError error
}

func newBuildWorkflowFake() *buildWorkflowFake {
	return &buildWorkflowFake{
		workflowID: uuid.MustParse(buildWorkflowIDText),
		rootTaskID: uuid.MustParse(buildRootTaskText),
	}
}

func (f *buildWorkflowFake) Start(_ context.Context, request core.WorkflowStartRequest) (core.WorkflowReceipt, error) {
	f.startCalls = append(f.startCalls, core.WorkflowStartRequest{
		OperationID: request.OperationID, ContractVersion: request.ContractVersion,
		IdempotencyKey: request.IdempotencyKey, Payload: append(json.RawMessage(nil), request.Payload...),
	})
	if f.startErr != nil {
		return core.WorkflowReceipt{}, f.startErr
	}
	return core.WorkflowReceipt{WorkflowID: f.workflowID, RootTaskID: f.rootTaskID, State: buildStateQueued, Created: true}, nil
}

func (f *buildWorkflowFake) Get(_ context.Context, _ uuid.UUID) (core.Workflow, error) {
	if f.beforeGet != nil {
		f.beforeGet()
	}
	if f.getErr != nil {
		return core.Workflow{}, f.getErr
	}
	workflow := f.workflow
	if workflow.ID == uuid.Nil {
		workflow.ID = f.workflowID
	}
	if workflow.RootTaskID == uuid.Nil {
		workflow.RootTaskID = f.rootTaskID
	}
	return workflow, nil
}

func (f *buildWorkflowFake) GetTaskResult(_ context.Context, _, _ uuid.UUID) (core.TaskResult, error) {
	f.taskCalls++
	if f.taskErr != nil {
		return core.TaskResult{}, f.taskErr
	}
	return f.taskResult, nil
}

func (f *buildWorkflowFake) ResolveTaskID(_ context.Context, _ uuid.UUID, _ string) (uuid.UUID, error) {
	f.resolveCalls++
	if f.resolvedError != nil {
		return uuid.Nil, f.resolvedError
	}
	return uuid.MustParse(buildPrerenderText), nil
}

type buildCatalogFake struct {
	revision int64
	err      error
	locales  []string
}

type buildContentProjectionResolverFake struct {
	wantSnapshotID string
	gotSnapshotID  string
	projection     websiteapp.ContentBuildProjection
}

func (f *buildContentProjectionResolverFake) ResolveContentBuildProjection(_ context.Context, pageSnapshotID, _ string) (websiteapp.ContentBuildProjection, error) {
	f.gotSnapshotID = pageSnapshotID
	if pageSnapshotID != f.wantSnapshotID {
		return websiteapp.ContentBuildProjection{}, errors.New("unexpected page snapshot ID")
	}
	return f.projection, nil
}

func TestModuleBuildInputResolverUsesPageSnapshotIDForContentProjection(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	model, err := service.SaveContentModel(ctx, websiteapi.SaveContentModelRequest{
		ExpectedRevision: 0, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	featurePayload, err := json.Marshal(map[string]any{
		"record_type": "feature_version", "manifest": json.RawMessage(`{}`), "files": map[string]string{"index.html": strings.Repeat("a", 64)},
		"digest": strings.Repeat("b", 64), "state": "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.records[websiterepo.FeatureVersionKey("feature", "1.0.0")] = recorddata.Record{Payload: featurePayload, PayloadVersion: 1}
	projection := &buildContentProjectionResolverFake{
		wantSnapshotID: snapshotA,
		projection:     websiteapp.ContentBuildProjection{PublicationRevision: 1, SelectionDigest: strings.Repeat("c", 64)},
	}
	resolver, err := websiteapp.NewModuleBuildInputResolver(store, projection, "sha256:"+strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := websiteapi.Snapshot{
		ID: snapshotA, PageID: pageA, Revision: 1, Digest: buildSnapshotDigest, State: "ready",
		FeaturePins: map[string]string{"feature": "1.0.0"},
		Payload:     &websiteapi.SnapshotPayload{Config: json.RawMessage(`{"formatVersion":1}`), Metadata: json.RawMessage(`{}`)},
	}
	if _, err := resolver.ResolveBuildInput(ctx, snapshot, buildLocale, 1); err != nil {
		t.Fatalf("ResolveBuildInput(): %v", err)
	}
	if projection.gotSnapshotID != snapshot.ID || projection.gotSnapshotID == snapshot.PageID {
		t.Fatalf("Content projection snapshot ID = %q, want page snapshot %q (page ID %q)", projection.gotSnapshotID, snapshot.ID, snapshot.PageID)
	}
	if !model.VerifyDigest() {
		t.Fatal("test fixture content model digest is invalid")
	}
}

func (f *buildCatalogFake) LocaleCatalogRevision(_ context.Context, locale string) (int64, error) {
	f.locales = append(f.locales, locale)
	if f.err != nil {
		return 0, f.err
	}
	return f.revision, nil
}

func buildTestConfig(workflow *buildWorkflowFake, catalog *buildCatalogFake) websiteapp.Config {
	config := websiteConfig()
	config.BuildWorkflow = workflow
	config.LocaleCatalog = catalog
	config.BuildOperationID = buildOperationID
	config.BuildContractVersion = 1
	return config
}

func buildReadySnapshot(t *testing.T, store *memoryStore) websiteapi.Snapshot {
	t.Helper()
	putSnapshotWithState(t, store, snapshotA, 3, "ready")
	key := snapshotRecordKey(snapshotA)
	record := store.records[key]
	var snapshot websiteapi.Snapshot
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Digest = buildSnapshotDigest
	snapshot.ContentRevision = 2
	record.Payload = mustJSON(snapshot)
	store.records[key] = record
	return snapshot
}

func buildCreateRequest() websiteapi.BuildSnapshotRequest {
	return websiteapi.BuildSnapshotRequest{SnapshotID: snapshotA, Locale: buildLocale, IdempotencyKey: buildIdempotencyKey}
}

func buildRootEnvelope(inputDigest string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"format":%q,"prerender_task_id":%q,"input_digest":%q}`, buildRootFormat, buildPrerenderText, inputDigest))
}

func buildRootEnvelopeByEmission(inputDigest, emissionKey string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"format":%q,"prerender_emission_key":%q,"input_digest":%q}`, buildRootFormat, emissionKey, inputDigest))
}

func buildTaskEnvelope(inputDigest string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"format":%q,"input_digest":%q,"manifest":{"file_id":%q,"checksum":%q,"content_type":"application/json","size":128}}`, buildTaskFormat, inputDigest, buildManifestFileID, buildManifestDigest))
}

func buildValidTaskResult(inputDigest string) core.TaskResult {
	return core.TaskResult{
		TaskID: uuid.MustParse(buildPrerenderText), WorkflowID: uuid.MustParse(buildWorkflowIDText),
		Operation: &core.OperationRef{ClassID: "worker.prerender", ID: "render-page", Version: 1},
		State:     "succeeded", Result: buildTaskEnvelope(inputDigest),
	}
}

func buildRecordForJob(t *testing.T, store *memoryStore, jobID string) (recorddata.Record, map[string]json.RawMessage) {
	t.Helper()
	record, err := store.Get(context.Background(), websiterepo.BuildKey(jobID))
	if err != nil {
		t.Fatal(err)
	}
	if record.PayloadVersion != 1 {
		t.Fatalf("build payload version = %d, want 1", record.PayloadVersion)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return record, payload
}

func buildRecordFields(t *testing.T) []string {
	t.Helper()
	fields := make([]string, 0, 13)
	for field := range buildRecordFieldsExpected {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

var buildRecordFieldsExpected = map[string]bool{
	"id": true, "snapshot_id": true, "locale": true, "locale_catalog_revision": true,
	"input_digest": true, "state": true, "workflow_id": true, "root_task_id": true,
	"prerender_task_id": true, "artifact_manifest_file_id": true, "error_code": true,
	"created_at": true, "updated_at": true,
}

func buildIndex(t *testing.T, record recorddata.Record) map[string]any {
	t.Helper()
	var index map[string]any
	if err := json.Unmarshal(record.IndexData, &index); err != nil {
		t.Fatal(err)
	}
	return index
}

func buildMutateRecord(t *testing.T, store *memoryStore, jobID string, mutate func(map[string]any)) {
	t.Helper()
	key := websiterepo.BuildKey(jobID)
	record := store.records[key]
	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	mutate(payload)
	var index map[string]any
	if err := json.Unmarshal(record.IndexData, &index); err != nil {
		t.Fatal(err)
	}
	if state, ok := payload["state"].(string); ok {
		index["state"] = state
	}
	record.Payload = mustJSON(payload)
	record.IndexData = mustJSON(index)
	record.Revision++
	index["revision"] = record.Revision
	record.IndexData = mustJSON(index)
	store.records[key] = record
}

type buildStateRecordingStore struct {
	*memoryStore
	states []string
}

func (s *buildStateRecordingStore) Batch(ctx context.Context, mutations []recorddata.RecordMutation) error {
	for _, mutation := range mutations {
		if mutation.Key.Namespace != "website/builds" || mutation.Value == nil || !strings.HasPrefix(mutation.Key.RecordKey, "build/") {
			continue
		}
		var index map[string]any
		if err := json.Unmarshal(mutation.Value.IndexData, &index); err != nil {
			return err
		}
		if state, ok := index["state"].(string); ok {
			s.states = append(s.states, state)
		}
	}
	return s.memoryStore.Batch(ctx, mutations)
}

func newBuildStateRecordingService(t *testing.T, store *buildStateRecordingStore, config websiteapp.Config) *websiteapp.Service {
	t.Helper()
	service, err := websiteapp.NewService(config, store, &fakeAuthority{level: core.AccessLevelWrite, coordinatorLevel: core.AccessLevelWrite}, fakeSessions{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestWebsiteBuildWorkflowFailClosedWithoutCapabilities(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("build without capabilities = %#v, %v, want unavailable", job, err)
	}
	if !reflect.DeepEqual(job, websiteapi.BuildJob{}) {
		t.Fatalf("unavailable build exposed a partial job: %#v", job)
	}
	for key := range store.records {
		if key.Namespace == "website/builds" {
			t.Fatalf("fail-closed build wrote %#v", key)
		}
	}

	reader := newService(t, store, core.AccessLevelRead, websiteConfig())
	readCtx, err := reader.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetBuild(readCtx, buildWorkflowIDText); !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("build read without workflow capability = %v, want unavailable", err)
	}
}

func TestWebsiteBuildWorkflowCreateIdempotentReplayAndTypedPayload(t *testing.T) {
	store := newMemoryStore()
	snapshot := buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	catalog := &buildCatalogFake{revision: 7}
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, catalog))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatalf("build create: %v", err)
	}
	if job.ID == "" || job.SnapshotID != snapshot.ID || job.Locale != buildLocale || job.LocaleCatalogRevision != 7 ||
		job.State != buildStateResolving || len(job.InputDigest) != 64 || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		t.Fatalf("created build = %#v", job)
	}
	if job.ArtifactManifestFileID != "" || job.ErrorCode != "" {
		t.Fatalf("created build exposed artifact or error: %#v", job)
	}
	if len(workflow.startCalls) != 1 {
		t.Fatalf("workflow starts = %d, want 1", len(workflow.startCalls))
	}
	start := workflow.startCalls[0]
	if start.OperationID != buildOperationID || start.ContractVersion != 1 {
		t.Fatalf("workflow start identity = %#v", start)
	}
	if start.IdempotencyKey == "" || strings.ContainsAny(start.IdempotencyKey, "\x00\n") {
		t.Fatalf("workflow idempotency key = %q", start.IdempotencyKey)
	}
	var startPayload map[string]any
	if err := json.Unmarshal(start.Payload, &startPayload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"build_id", "snapshot_id", "snapshot_revision", "locale", "locale_catalog_revision", "input_digest"} {
		if _, present := startPayload[field]; !present {
			t.Fatalf("workflow payload lacks %q: %s", field, start.Payload)
		}
	}
	for _, forbidden := range []string{"operation_id", "route", "url", "branch", "records", "secret", "token", "file_set_id"} {
		if _, present := startPayload[forbidden]; present {
			t.Fatalf("workflow payload exposes %q: %s", forbidden, start.Payload)
		}
	}
	if startPayload["build_id"] != job.ID || startPayload["snapshot_id"] != snapshot.ID || startPayload["locale"] != buildLocale ||
		startPayload["input_digest"] != job.InputDigest || startPayload["locale_catalog_revision"] != float64(7) {
		t.Fatalf("typed workflow payload = %#v", startPayload)
	}
	if !reflect.DeepEqual(catalog.locales, []string{buildLocale}) {
		t.Fatalf("locale catalog lookups = %#v", catalog.locales)
	}
	if _, exists := reflect.TypeOf(websiteapi.BuildSnapshotRequest{}).FieldByName("OperationID"); exists {
		t.Fatal("BuildSnapshotRequest must not accept an operation ID")
	}

	record, payload := buildRecordForJob(t, store, job.ID)
	fields := buildRecordFields(t)
	actual := make([]string, 0, len(payload))
	for field := range payload {
		actual = append(actual, field)
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(actual, fields) {
		t.Fatalf("build payload keys = %#v, want %#v", actual, fields)
	}
	index := buildIndex(t, record)
	if index["record_type"] != "build" || index["state"] != buildStateResolving || index["snapshot_id"] != snapshot.ID || index["revision"] != float64(record.Revision) {
		t.Fatalf("build index = %#v", index)
	}
	for _, field := range []string{"workflow_id", "root_task_id", "prerender_task_id", "artifact_manifest_file_id", "error_code"} {
		if _, present := payload[field]; !present {
			t.Fatalf("build payload lacks %q", field)
		}
	}
	if string(payload["workflow_id"]) != `"`+buildWorkflowIDText+`"` || string(payload["root_task_id"]) != `"`+buildRootTaskText+`"` || string(payload["prerender_task_id"]) != `""` {
		t.Fatalf("workflow identity was not pinned: %#v", payload)
	}

	buildRecords := 0
	idempotencyRecords := 0
	for key := range store.records {
		if key.Namespace != "website/builds" {
			continue
		}
		if key.ScopeType != "job" {
			t.Fatalf("build record escaped its namespace/scope: %#v", key)
		}
		switch {
		case strings.HasPrefix(key.RecordKey, "build/"):
			buildRecords++
		case strings.HasPrefix(key.RecordKey, "idempotency/"):
			idempotencyRecords++
		default:
			t.Fatalf("unexpected build record key %q", key.RecordKey)
		}
	}
	if buildRecords != 1 || idempotencyRecords != 2 {
		t.Fatalf("build persistence = builds:%d idempotency:%d", buildRecords, idempotencyRecords)
	}

	replay, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replay.ID != job.ID || replay.InputDigest != job.InputDigest || replay.State != job.State ||
		!replay.CreatedAt.Equal(job.CreatedAt) || !replay.UpdatedAt.Equal(job.UpdatedAt) {
		t.Fatalf("idempotent replay changed the job: first=%#v replay=%#v", job, replay)
	}
	if len(workflow.startCalls) != 1 {
		t.Fatalf("idempotent replay started %d workflows", len(workflow.startCalls))
	}
	if len(store.records) != 5 {
		t.Fatalf("replay wrote unexpected records: %d", len(store.records))
	}
}

func TestWebsiteBuildWorkflowConflictOnChangedInput(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	catalog := &buildCatalogFake{revision: 7}
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, catalog))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatal(err)
	}

	catalog.revision = 8
	before := len(store.records)
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("changed pinned input = %#v, %v, want conflict", job, err)
	}
	if !reflect.DeepEqual(job, websiteapi.BuildJob{}) {
		t.Fatalf("conflicting build exposed a partial job: %#v", job)
	}
	if len(workflow.startCalls) != 1 || len(store.records) != before {
		t.Fatalf("conflicting build started work or wrote records: starts=%d records=%d", len(workflow.startCalls), len(store.records))
	}
	persisted, _ := buildRecordForJob(t, store, first.ID)
	if buildIndex(t, persisted)["state"] != buildStateResolving {
		t.Fatalf("conflicting build changed the first job state: %#v", persisted)
	}
}

func TestWebsiteBuildWorkflowStaleRevisionReconciliationCannotOverwrite(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	catalog := &buildCatalogFake{revision: 7}
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, catalog))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	workflow.workflow = core.Workflow{State: "running", Result: buildRootEnvelope(job.InputDigest)}
	workflow.beforeGet = func() {
		buildMutateRecord(t, store, job.ID, func(payload map[string]any) {
			payload["state"] = buildStateFailed
			payload["error_code"] = "external_override"
		})
	}

	stale, err := service.GetBuild(ctx, job.ID)
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale reconciliation = %#v, %v, want conflict", stale, err)
	}
	record, payload := buildRecordForJob(t, store, job.ID)
	if string(payload["state"]) != `"`+buildStateFailed+`"` || string(payload["error_code"]) != `"external_override"` || string(payload["prerender_task_id"]) != `""` {
		t.Fatalf("stale reconciliation overwrote the record: %#v", payload)
	}
	if string(record.Payload) == "" || buildIndex(t, record)["state"] != buildStateFailed {
		t.Fatalf("stale reconciliation left an inconsistent index: %#v", record)
	}
}

func TestWebsiteBuildWorkflowFailClosedWithoutWorkflowClient(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, &buildCatalogFake{revision: 7}))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	before := len(store.records)

	reader := newService(t, store, core.AccessLevelRead, websiteConfig())
	readCtx, err := reader.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.GetBuild(readCtx, job.ID)
	if !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("build read without workflow client = %#v, %v, want unavailable", got, err)
	}
	if len(store.records) != before || len(workflow.startCalls) != 1 {
		t.Fatalf("fail-closed read mutated state: records=%d starts=%d", len(store.records), len(workflow.startCalls))
	}
}

func TestWebsiteBuildWorkflowGrantsAndMissingConfiguration(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	readerWrite := newServiceWithLevels(t, store, core.AccessLevelRead, core.AccessLevelWrite, buildTestConfig(newBuildWorkflowFake(), &buildCatalogFake{revision: 7}))
	readCtx, err := readerWrite.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readerWrite.BuildSnapshot(readCtx, buildCreateRequest()); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("build without website write = %v, want forbidden", err)
	}

	coordinatorRead := newServiceWithLevels(t, store, core.AccessLevelWrite, core.AccessLevelRead, buildTestConfig(newBuildWorkflowFake(), &buildCatalogFake{revision: 7}))
	writeCtx, err := coordinatorRead.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinatorRead.BuildSnapshot(writeCtx, buildCreateRequest()); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("build without coordinator write = %v, want forbidden", err)
	}
	for key := range store.records {
		if key.Namespace == "website/builds" {
			t.Fatalf("forbidden build wrote %#v", key)
		}
	}

	denied := newService(t, store, core.AccessLevelNone, buildTestConfig(newBuildWorkflowFake(), &buildCatalogFake{revision: 7}))
	deniedCtx, err := denied.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.GetBuild(deniedCtx, buildWorkflowIDText); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("build read without grant = %v, want forbidden", err)
	}

	workflow := newBuildWorkflowFake()
	catalog := &buildCatalogFake{revision: 7}
	for _, config := range []websiteapp.Config{
		func() websiteapp.Config {
			value := buildTestConfig(workflow, catalog)
			value.BuildOperationID = ""
			return value
		}(),
		func() websiteapp.Config {
			value := buildTestConfig(workflow, catalog)
			value.BuildContractVersion = 0
			return value
		}(),
	} {
		incompleteStore := newMemoryStore()
		buildReadySnapshot(t, incompleteStore)
		incomplete := newService(t, incompleteStore, core.AccessLevelWrite, config)
		incompleteCtx, err := incomplete.Authenticate(context.Background(), "credential")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := incomplete.BuildSnapshot(incompleteCtx, buildCreateRequest()); !errors.Is(err, websiteapi.ErrUnavailable) {
			t.Fatalf("build with incomplete workflow configuration = %v, want unavailable", err)
		}
	}
	if len(workflow.startCalls) != 0 {
		t.Fatalf("incomplete configuration started %d workflows", len(workflow.startCalls))
	}
}

func TestWebsiteBuildWorkflowStartFailureDoesNotLeaveReadyJob(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	workflow.startErr = errors.New("coordinator rejected operation")
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, &buildCatalogFake{revision: 7}))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("failed workflow start = %#v, %v, want unavailable", job, err)
	}
	if job.State == buildStateReady || job.ArtifactManifestFileID != "" {
		t.Fatalf("failed workflow start exposed a ready job: %#v", job)
	}
	records := 0
	for key, record := range store.records {
		if key.Namespace != "website/builds" {
			continue
		}
		records++
		var index map[string]any
		if err := json.Unmarshal(record.IndexData, &index); err != nil {
			t.Fatal(err)
		}
		if index["state"] == buildStateReady {
			t.Fatalf("failed workflow start persisted a ready record: %#v", index)
		}
	}
	if records != 3 {
		t.Fatalf("failed workflow start persistence = %d records", records)
	}
}

func TestWebsiteBuildWorkflowRejectsInvalidAndStaleChildResults(t *testing.T) {
	for _, test := range []struct {
		name      string
		result    json.RawMessage
		errorCode string
	}{
		{name: "arbitrary artifact JSON", result: json.RawMessage(`{"format":"static-page-artifact/v1","document":{"file_id":"66666666-6666-4666-8666-666666666666"}}`), errorCode: "invalid_task_result"},
		{name: "stale input digest", result: buildTaskEnvelope(strings.Repeat("c", 64)), errorCode: "stale_input_digest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			buildReadySnapshot(t, store)
			workflow := newBuildWorkflowFake()
			service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, &buildCatalogFake{revision: 7}))
			ctx, err := service.Authenticate(context.Background(), "credential")
			if err != nil {
				t.Fatal(err)
			}
			job, err := service.BuildSnapshot(ctx, buildCreateRequest())
			if err != nil {
				t.Fatal(err)
			}
			workflow.workflow = core.Workflow{State: "running", Result: buildRootEnvelope(job.InputDigest)}
			advancing, err := service.GetBuild(ctx, job.ID)
			if err != nil || advancing.State != buildStatePrerender || advancing.ArtifactManifestFileID != "" {
				t.Fatalf("prerender advancement = %#v, %v", advancing, err)
			}

			result := buildValidTaskResult(job.InputDigest)
			result.Result = test.result
			workflow.taskResult = result
			failed, err := service.GetBuild(ctx, job.ID)
			if err != nil {
				t.Fatalf("rejected child result returned a transport error: %v", err)
			}
			if failed.State != buildStateFailed || failed.ErrorCode != test.errorCode || failed.ArtifactManifestFileID != "" {
				t.Fatalf("rejected child result = %#v, want failed/%s", failed, test.errorCode)
			}
			record, payload := buildRecordForJob(t, store, job.ID)
			if string(payload["state"]) != `"`+buildStateFailed+`"` || string(payload["error_code"]) != `"`+test.errorCode+`"` || string(payload["artifact_manifest_file_id"]) != `""` {
				t.Fatalf("rejected result persisted partial state: %#v", payload)
			}
			if buildIndex(t, record)["state"] != buildStateFailed {
				t.Fatalf("failed state was not indexed: %#v", record)
			}
		})
	}
}

func TestWebsiteBuildWorkflowReadyOnlyAfterValidChildResult(t *testing.T) {
	store := &buildStateRecordingStore{memoryStore: newMemoryStore()}
	buildReadySnapshot(t, store.memoryStore)
	workflow := newBuildWorkflowFake()
	catalog := &buildCatalogFake{revision: 7}
	config := buildTestConfig(workflow, catalog)
	service := newBuildStateRecordingService(t, store, config)
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	workflow.workflow = core.Workflow{State: "running", Result: buildRootEnvelope(job.InputDigest)}
	prerendering, err := service.GetBuild(ctx, job.ID)
	if err != nil || prerendering.State != buildStatePrerender || prerendering.ArtifactManifestFileID != "" {
		t.Fatalf("root reconciliation = %#v, %v", prerendering, err)
	}
	if prerendering.State == buildStateReady {
		t.Fatal("build became ready before a valid child result")
	}

	workflow.taskResult = buildValidTaskResult(job.InputDigest)
	ready, err := service.GetBuild(ctx, job.ID)
	if err != nil {
		t.Fatalf("valid child result reconciliation: %v", err)
	}
	if ready.State != buildStateReady || ready.ArtifactManifestFileID != buildManifestFileID || ready.ErrorCode != "" {
		t.Fatalf("ready build = %#v", ready)
	}
	if workflow.taskCalls != 1 {
		t.Fatalf("child result reads = %d, want 1", workflow.taskCalls)
	}
	record, payload := buildRecordForJob(t, store.memoryStore, job.ID)
	if string(payload["artifact_manifest_file_id"]) != `"`+buildManifestFileID+`"` || string(payload["state"]) != `"`+buildStateReady+`"` || string(payload["error_code"]) != `""` {
		t.Fatalf("ready record = %#v", payload)
	}
	if buildIndex(t, record)["state"] != buildStateReady {
		t.Fatalf("ready state was not indexed: %#v", record)
	}
	wantStates := []string{buildStateQueued, buildStateResolving, buildStatePrerender, "packaging", "verifying", buildStateReady}
	if !reflect.DeepEqual(store.states, wantStates) {
		t.Fatalf("build transitions = %#v, want %#v", store.states, wantStates)
	}

	stable, err := service.GetBuild(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(stable, ready) {
		t.Fatalf("stable ready build = %#v, %v; first=%#v", stable, err, ready)
	}
	if workflow.taskCalls != 1 {
		t.Fatalf("ready reconciliation reread the child result: %d", workflow.taskCalls)
	}
}

func TestWebsiteBuildWorkflowResolvesPrerenderEmissionKey(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, &buildCatalogFake{revision: 7}))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	emissionKey := "prerender:" + job.ID
	workflow.workflow = core.Workflow{State: "running", Result: buildRootEnvelopeByEmission(job.InputDigest, emissionKey)}
	workflow.taskResult = buildValidTaskResult(job.InputDigest)

	prerendering, err := service.GetBuild(ctx, job.ID)
	if err != nil || prerendering.State != buildStatePrerender {
		t.Fatalf("emission-key root reconciliation = %#v, %v", prerendering, err)
	}
	ready, err := service.GetBuild(ctx, job.ID)
	if err != nil {
		t.Fatalf("emission-key reconciliation: %v", err)
	}
	if ready.State != buildStateReady || ready.ArtifactManifestFileID != buildManifestFileID {
		t.Fatalf("emission-key build = %#v", ready)
	}
	if workflow.resolveCalls != 1 || workflow.taskCalls != 1 {
		t.Fatalf("resolution/read calls = %d/%d, want 1/1", workflow.resolveCalls, workflow.taskCalls)
	}
	_, payload := buildRecordForJob(t, store, job.ID)
	if string(payload["prerender_task_id"]) != `"`+buildPrerenderText+`"` {
		t.Fatalf("resolved task identity was not pinned: %#v", payload)
	}

	stable, err := service.GetBuild(ctx, job.ID)
	if err != nil || stable.State != buildStateReady || workflow.resolveCalls != 1 {
		t.Fatalf("stable emission-key build = %#v, %v; resolutions=%d", stable, err, workflow.resolveCalls)
	}
}

func TestWebsiteBuildWorkflowFailsWhenPrerenderEmissionCannotBeResolved(t *testing.T) {
	store := newMemoryStore()
	buildReadySnapshot(t, store)
	workflow := newBuildWorkflowFake()
	workflow.resolvedError = core.ErrTaskMissing
	service := newService(t, store, core.AccessLevelWrite, buildTestConfig(workflow, &buildCatalogFake{revision: 7}))
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.BuildSnapshot(ctx, buildCreateRequest())
	if err != nil {
		t.Fatal(err)
	}
	workflow.workflow = core.Workflow{State: "succeeded", Result: buildRootEnvelopeByEmission(job.InputDigest, "prerender:"+job.ID)}

	failed, err := service.GetBuild(ctx, job.ID)
	if err != nil {
		t.Fatalf("unresolvable emission reconciliation: %v", err)
	}
	if failed.State != buildStateFailed || failed.ErrorCode != "task_result_missing" {
		t.Fatalf("unresolvable emission build = %#v", failed)
	}
	if workflow.taskCalls != 0 {
		t.Fatalf("child result was read after failed resolution: %d", workflow.taskCalls)
	}
}
