package content_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	content "github.com/Zubrium/cdn/engines/content"
	"github.com/Zubrium/cdn/engines/content/domain"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/engines/content/repository"
)

const engineSnapshotID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestEngineEnforcesContentGroupLevelsAndNormalizesEmptyWebsiteModel(t *testing.T) {
	access := &testAuthorization{level: core.AccessLevelRead}
	store := newEngineStore()
	engine := newTestEngine(t, access, store)
	ctx, err := engine.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	projection := schemaSnapshot("ready")
	workspace, err := engine.GetWorkspace(ctx, projection)
	if err != nil || workspace.Revision != 0 || string(workspace.DataModel) != `{"formatVersion":1,"entities":[]}` {
		t.Fatalf("GetWorkspace() = %#v, err %v", workspace, err)
	}
	if access.required != core.AccessLevelRead || len(access.groups) != 1 || access.groups[0] != "content" {
		t.Fatalf("read authorization = level %v groups %v", access.required, access.groups)
	}
	putsBeforeDeniedWrite := store.putCount
	_, err = engine.SaveWorkspaceDraft(ctx, projection, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: engineSnapshotID, IdempotencyKey: "save-key", ExpectedRevision: 0,
		ExpectedSelectionRevision: workspace.SelectionRevision,
		Records:                   []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	})
	if !errors.Is(err, contentapi.ErrForbidden) || store.putCount != putsBeforeDeniedWrite {
		t.Fatalf("read-only principal write error = %v, Put count %d", err, store.putCount)
	}
	access.level = core.AccessLevelWrite
	saved, err := engine.SaveWorkspaceDraft(ctx, projection, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: engineSnapshotID, IdempotencyKey: "save-key", ExpectedRevision: 0,
		ExpectedSelectionRevision: workspace.SelectionRevision,
		Records:                   []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	})
	if err != nil || saved.SelectionRevision != workspace.SelectionRevision+1 || access.required != core.AccessLevelWrite {
		t.Fatalf("write authorized SaveWorkspaceDraft() = %#v, err %v", saved, err)
	}
}

func TestEngineRejectsMalformedSelectionPolicyBeforeAuthorizationOrMutation(t *testing.T) {
	access := &testAuthorization{level: core.AccessLevelWrite}
	store := newEngineStore()
	engine := newTestEngine(t, access, store)
	_, err := engine.CopySnapshotContent(context.Background(), contentapi.CopySnapshotContentRequest{SelectionPolicy: "merge"})
	if !errors.Is(err, contentapi.ErrInvalid) {
		t.Fatalf("malformed policy error = %v, want invalid", err)
	}
	if store.putCount != 0 || access.required != 0 {
		t.Fatalf("malformed policy reached authorization or persistence: puts=%d required=%v", store.putCount, access.required)
	}
}

func TestEngineRejectsUnreadySnapshotAndForeignAuthenticationContext(t *testing.T) {
	access := &testAuthorization{level: core.AccessLevelRead}
	store := newEngineStore()
	first := newTestEngine(t, access, store)
	ctx, err := first.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if _, err := first.GetWorkspace(ctx, schemaSnapshot("provisioning")); !errors.Is(err, contentapi.ErrNotFound) {
		t.Fatalf("GetWorkspace() before Website readiness error = %v, want not found", err)
	}
	if store.putCount != 0 {
		t.Fatalf("unready snapshot initialized persisted Content state; Put count = %d", store.putCount)
	}
	second := newTestEngine(t, access, newEngineStore())
	if _, err := second.GetWorkspace(ctx, schemaSnapshot("ready")); !errors.Is(err, contentapi.ErrUnauthorized) {
		t.Fatalf("foreign engine context error = %v, want unauthorized", err)
	}
}

func newTestEngine(t *testing.T, authorization *testAuthorization, store *testDataStore) *content.Engine {
	t.Helper()
	principal := core.Principal{ID: "user-1", Name: "User", Active: true}
	identity := testIdentity{principal: principal}
	sessions := testSessions{principal: principal}
	engine, err := content.New(content.Dependencies{
		Identity: identity, Sessions: sessions, Authorization: authorization,
		Files: testFiles{}, Data: store, Projections: testProjectionsProvider{},
	})
	if err != nil {
		t.Fatalf("content.New() error = %v", err)
	}
	return engine
}

type testIdentity struct{ principal core.Principal }

func (identity testIdentity) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, identity.principal, nil
}

type testSessions struct{ principal core.Principal }

func (sessions testSessions) CreateSession(context.Context, string) (core.CreatedSession, error) {
	return core.CreatedSession{}, errors.New("unexpected CreateSession call")
}
func (sessions testSessions) AuthenticateSession(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, sessions.principal, nil
}
func (testSessions) RevokeSession(context.Context, string) error { return nil }

type testAuthorization struct {
	level    core.AccessLevel
	required core.AccessLevel
	groups   []string
}

func (authorization *testAuthorization) Authenticate(ctx context.Context, credential string) (core.Session, core.Principal, error) {
	return testIdentity{principal: core.Principal{ID: "user-1", Name: "User", Active: true}}.Authenticate(ctx, credential)
}
func (authorization *testAuthorization) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, required core.AccessLevel) error {
	authorization.required = required
	authorization.groups = append([]string(nil), groups...)
	if authorization.level < required {
		return core.ErrForbidden
	}
	return nil
}

type testFiles struct{}

func (testFiles) StatFile(context.Context, core.Session, string) (core.File, error) {
	return core.File{}, core.ErrFileNotFound
}

type testProjectionsProvider struct{}

func (testProjectionsProvider) GetContentProjections(_ context.Context, pageSnapshotID string) (contentapi.Projections, error) {
	if pageSnapshotID != engineSnapshotID {
		return contentapi.Projections{}, contentapi.ErrNotFound
	}
	return schemaSnapshot("ready"), nil
}

func schemaSnapshot(state string) contentapi.Projections {
	model := contentapi.ContentModelProjection{Version: contentapi.ProjectionVersion, ModelRevision: 1, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`)}
	model.Digest, _ = model.ComputeDigest()
	page := contentapi.PagePlacementProjection{Version: contentapi.ProjectionVersion, PageSnapshotID: engineSnapshotID, PageID: "page-1", Revision: 1, State: state, Placements: []contentapi.Placement{}}
	page.Digest, _ = page.ComputeDigest()
	return contentapi.Projections{ContentModel: model, Page: page, Locale: "en-US"}
}

func TestEngineRejectsInvalidSchemaSnapshotVersionDigestStateAndBinding(t *testing.T) {
	access := &testAuthorization{level: core.AccessLevelWrite}
	store := newEngineStore()
	engine := newTestEngine(t, access, store)
	ctx, err := engine.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	unsupported := schemaSnapshot("ready")
	unsupported.ContentModel.Version++
	unsupported.ContentModel.Digest, _ = unsupported.ContentModel.ComputeDigest()
	if _, err := engine.GetWorkspace(ctx, unsupported); !errors.Is(err, contentapi.ErrInvalid) {
		t.Errorf("unsupported version with valid digest error = %v", err)
	}

	staleDigest := schemaSnapshot("ready")
	staleDigest.Page.Placements = []contentapi.Placement{{ID: "placement-changed"}}
	if _, err := engine.GetWorkspace(ctx, staleDigest); !errors.Is(err, contentapi.ErrInvalid) {
		t.Errorf("changed projection with stale digest error = %v", err)
	}

	unready := schemaSnapshot("provisioning")
	if _, err := engine.GetWorkspace(ctx, unready); !errors.Is(err, contentapi.ErrNotFound) {
		t.Errorf("unready snapshot error = %v", err)
	}

	mismatched := schemaSnapshot("ready")
	_, err = engine.SaveWorkspaceDraft(ctx, mismatched, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", IdempotencyKey: "save-key",
		ExpectedRevision: 0, Records: []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	})
	if !errors.Is(err, contentapi.ErrInvalid) {
		t.Errorf("request/projection snapshot mismatch error = %v", err)
	}
	if store.putCount != 0 {
		t.Fatalf("invalid schema projection persisted state: Put count = %d", store.putCount)
	}
}

type testDataStore struct {
	rows     map[core.ModuleDataKey]core.ModuleDataRecord
	putCount int
}

func newEngineStore() *testDataStore {
	store := &testDataStore{rows: map[core.ModuleDataKey]core.ModuleDataRecord{}}
	repo, err := repository.New(store)
	if err != nil {
		panic(err)
	}
	if _, err := repo.ReplaceLocaleConfig(context.Background(), domain.ReplaceLocaleConfigRequest{
		ExpectedRevision: 0, Locales: []string{"en-US"}, DefaultLocale: "en-US",
	}, "seed-key-0001", "seed-fingerprint-0001"); err != nil {
		panic(err)
	}
	store.putCount = 0
	return store
}
func (store *testDataStore) Get(_ context.Context, key core.ModuleDataKey) (core.ModuleDataRecord, error) {
	row, exists := store.rows[key]
	if !exists {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return cloneEngineRecord(row), nil
}
func (store *testDataStore) Put(_ context.Context, key core.ModuleDataKey, value core.ModuleDataValue, expected int64) (core.ModuleDataRecord, error) {
	current, exists := store.rows[key]
	if (!exists && expected != 0) || (exists && current.Revision != expected) {
		return core.ModuleDataRecord{}, core.ErrModuleDataConflict
	}
	row := core.ModuleDataRecord{Payload: append(json.RawMessage(nil), value.Payload...), PayloadVersion: value.PayloadVersion, IndexData: append(json.RawMessage(nil), value.IndexData...), Revision: expected + 1}
	store.rows[key] = row
	store.putCount++
	return cloneEngineRecord(row), nil
}
func (store *testDataStore) Batch(_ context.Context, mutations []core.ModuleDataMutation) error {
	for _, mutation := range mutations {
		current, exists := store.rows[mutation.Key]
		if (!exists && mutation.ExpectedRevision != 0) || (exists && current.Revision != mutation.ExpectedRevision) {
			return core.ErrModuleDataConflict
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(store.rows, mutation.Key)
			continue
		}
		store.rows[mutation.Key] = core.ModuleDataRecord{
			Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
			IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: mutation.ExpectedRevision + 1,
		}
	}
	return nil
}
func (store *testDataStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []core.ModuleDataSelector, limit int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error) {
	keys := make([]core.ModuleDataKey, 0)
	for key, record := range store.rows {
		if key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matchesSelectors(record, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	records := make([]core.ModuleDataRecord, len(keys))
	for index, key := range keys {
		records[index] = cloneEngineRecord(store.rows[key])
	}
	if more && len(keys) > 0 {
		return keys, records, keys[len(keys)-1].RecordKey, nil
	}
	return keys, records, "", nil
}
func (*testDataStore) MaxPageSize() int     { return 100 }
func (*testDataStore) MaxBatchSize() int    { return 256 }
func (*testDataStore) MaxPayloadBytes() int { return 1 << 20 }
func (*testDataStore) MaxIndexBytes() int   { return 1 << 16 }
func (*testDataStore) MaxBatchBytes() int   { return 2 << 20 }

func matchesSelectors(record core.ModuleDataRecord, selectors []core.ModuleDataSelector) bool {
	for _, selector := range selectors {
		if selector.Operator != core.SelectorEqual {
			return false
		}
		var index map[string]any
		if json.Unmarshal(record.IndexData, &index) != nil {
			return false
		}
		value, ok := index[selector.Field].(string)
		if !ok {
			return false
		}
		matched := false
		for _, expected := range selector.Values {
			if expectedValue, ok := expected.(string); ok && value == expectedValue {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func cloneEngineRecord(record core.ModuleDataRecord) core.ModuleDataRecord {
	record.Payload = append(json.RawMessage(nil), record.Payload...)
	record.IndexData = append(json.RawMessage(nil), record.IndexData...)
	return record
}
