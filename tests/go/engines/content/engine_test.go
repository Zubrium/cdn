package content_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	content "github.com/Zubrium/cdn/engines/content"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
)

const engineSnapshotID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestEngineEnforcesContentGroupLevelsAndNormalizesEmptyWebsiteModel(t *testing.T) {
	access := &testAuthorization{level: core.AccessLevelRead}
	store := newEngineStore()
	engine := newTestEngine(t, "ready", access, store)
	ctx, err := engine.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	workspace, err := engine.GetWorkspace(ctx, engineSnapshotID)
	if err != nil || workspace.Revision != 0 || string(workspace.DataModel) != `{"formatVersion":1,"entities":[]}` {
		t.Fatalf("GetWorkspace() = %#v, err %v", workspace, err)
	}
	if access.required != core.AccessLevelRead || len(access.groups) != 1 || access.groups[0] != "content" {
		t.Fatalf("read authorization = level %v groups %v", access.required, access.groups)
	}
	putsBeforeDeniedWrite := store.putCount
	_, err = engine.SaveWorkspaceDraft(ctx, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: engineSnapshotID, IdempotencyKey: "save-key", ExpectedRevision: 0,
		Records: []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	})
	if !errors.Is(err, contentapi.ErrForbidden) || store.putCount != putsBeforeDeniedWrite {
		t.Fatalf("read-only principal write error = %v, Put count %d", err, store.putCount)
	}
	access.level = core.AccessLevelWrite
	saved, err := engine.SaveWorkspaceDraft(ctx, contentapi.SaveWorkspaceDraftRequest{
		SnapshotID: engineSnapshotID, IdempotencyKey: "save-key", ExpectedRevision: 0,
		Records: []contentapi.RecordInput{}, Selections: []contentapi.Selection{},
	})
	if err != nil || saved.Revision != 1 || access.required != core.AccessLevelWrite {
		t.Fatalf("write authorized SaveWorkspaceDraft() = %#v, err %v", saved, err)
	}
}

func TestEngineRejectsUnreadySnapshotAndForeignAuthenticationContext(t *testing.T) {
	access := &testAuthorization{level: core.AccessLevelRead}
	store := newEngineStore()
	first := newTestEngine(t, "provisioning", access, store)
	ctx, err := first.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if _, err := first.GetWorkspace(ctx, engineSnapshotID); !errors.Is(err, contentapi.ErrNotFound) {
		t.Fatalf("GetWorkspace() before Website readiness error = %v, want not found", err)
	}
	if store.putCount != 0 {
		t.Fatalf("unready snapshot initialized persisted Content state; Put count = %d", store.putCount)
	}
	second := newTestEngine(t, "ready", access, newEngineStore())
	if _, err := second.GetWorkspace(ctx, engineSnapshotID); !errors.Is(err, contentapi.ErrUnauthorized) {
		t.Fatalf("foreign engine context error = %v, want unauthorized", err)
	}
}

func newTestEngine(t *testing.T, state string, authorization *testAuthorization, store *testDataStore) *content.Engine {
	t.Helper()
	principal := core.Principal{ID: "user-1", Name: "User", Active: true}
	identity := testIdentity{principal: principal}
	sessions := testSessions{principal: principal}
	engine, err := content.New(content.Dependencies{
		Identity: identity, Sessions: sessions, Authorization: authorization,
		Files: testFiles{}, Data: store, Schema: testSchemaResolver{state: state},
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

type testSchemaResolver struct{ state string }

func (resolver testSchemaResolver) ResolveContentSchema(context.Context, string) (contentapi.SnapshotSchema, error) {
	return contentapi.SnapshotSchema{State: resolver.state}, nil
}

type testDataStore struct {
	rows     map[core.ModuleDataKey]core.ModuleDataRecord
	putCount int
}

func newEngineStore() *testDataStore {
	return &testDataStore{rows: map[core.ModuleDataKey]core.ModuleDataRecord{}}
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
func (store *testDataStore) List(_ context.Context, namespace, scopeType, scopeID, after string, _ []core.ModuleDataSelector, limit int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error) {
	keys := make([]core.ModuleDataKey, 0)
	for key := range store.rows {
		if key.Namespace == namespace && key.ScopeType == scopeType && key.ScopeID == scopeID && key.RecordKey > after {
			keys = append(keys, key)
		}
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

func cloneEngineRecord(record core.ModuleDataRecord) core.ModuleDataRecord {
	record.Payload = append(json.RawMessage(nil), record.Payload...)
	record.IndexData = append(json.RawMessage(nil), record.IndexData...)
	return record
}
