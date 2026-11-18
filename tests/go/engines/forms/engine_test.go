package forms_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	forms "github.com/Zubrium/cdn/engines/forms"
	submissionpkg "github.com/Zubrium/cdn/engines/forms/features/submissions/pkg"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

func TestSaveSchemaUsesGeneratedIDsAndIdempotentUniqueKey(t *testing.T) {
	engine, data, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	request := formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a",
		Key:        "contact-us",
		Fields:     map[string]any{"email": map[string]any{"type": "email", "required": true}},
	}

	created, err := engine.SaveSchema(authenticatedContext(t, engine), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID == request.Key || created.Version != 1 || created.Revision != 1 {
		t.Fatalf("created schema has invalid generated identity/version: %#v", created)
	}
	if len(created.ID) != 36 || strings.Count(created.ID, "-") != 4 {
		t.Fatalf("schema ID is not a server-generated UUID: %q", created.ID)
	}

	retried, err := engine.SaveSchema(authenticatedContext(t, engine), request)
	if err != nil || retried.ID != created.ID || retried.Revision != created.Revision {
		t.Fatalf("identical create retry was not idempotent: got %#v, err %v", retried, err)
	}
	if len(data.records) != 1 {
		t.Fatalf("schema should be the only persisted record, got %d", len(data.records))
	}
}

func TestSaveSchemaRejectsDuplicateKeyAndUsesCASOnUpdate(t *testing.T) {
	engine, _, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	created, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a", Key: "contact", Fields: map[string]any{"email": "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a", Key: "contact", Fields: map[string]any{"email": "text"},
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("duplicate key with different schema = %v, want conflict", err)
	}

	updated, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a", SchemaID: created.ID, Key: "contact-v2",
		Fields: map[string]any{"email": map[string]any{"type": "email"}}, ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Key != "contact-v2" || updated.Version != 2 || updated.Revision != 2 {
		t.Fatalf("updated schema did not preserve identity and increment version/revision: %#v", updated)
	}
	if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a", SchemaID: created.ID, Key: "contact-v2",
		Fields: map[string]any{"email": "stale"}, ExpectedRevision: created.Revision,
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("stale schema write = %v, want conflict", err)
	}

	oldKey, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a", Key: "contact", Fields: map[string]any{"email": "new"},
	})
	if err != nil || oldKey.ID == created.ID {
		t.Fatalf("renaming should atomically release the old key: new=%#v err=%v", oldKey, err)
	}
}

func TestFormsReadsRequireDirectReadGroupAccess(t *testing.T) {
	engine, _, authorization, _ := newEngine(t, core.AccessLevelNone, nil)
	ctx := authenticatedContext(t, engine)
	if _, err := engine.ListSchemas(ctx, "snapshot-a", "", 10); !errors.Is(err, formapi.ErrForbidden) {
		t.Fatalf("read without Forms group access = %v, want forbidden", err)
	}
	if len(authorization.checkedGroups) != 1 || authorization.checkedGroups[0] != "forms" || authorization.checkedLevel != core.AccessLevelRead {
		t.Fatalf("Forms read authorization did not require direct forms-group level 1: %#v", authorization)
	}
}

func TestFormsReadAccessCannotWriteSchemas(t *testing.T) {
	engine, data, authorization, _ := newEngine(t, core.AccessLevelRead, nil)
	_, err := engine.SaveSchema(authenticatedContext(t, engine), formapi.SaveSchemaRequest{
		SnapshotID: "snapshot-a", Key: "contact", Fields: map[string]any{},
	})
	if !errors.Is(err, formapi.ErrForbidden) {
		t.Fatalf("schema write with read-only Forms access = %v, want forbidden", err)
	}
	if len(data.records) != 0 || authorization.checkedLevel != core.AccessLevelWrite {
		t.Fatalf("read-only write changed data or checked wrong level: records=%d authorization=%#v", len(data.records), authorization)
	}
}

func TestListSchemasUsesOpaqueCursorAndRejectsInvalidCursor(t *testing.T) {
	engine, _, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	for _, key := range []string{"alpha", "beta"} {
		if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{SnapshotID: "snapshot-a", Key: key, Fields: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := engine.ListSchemas(ctx, "snapshot-a", "", 1)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first schema page = %#v, %v", first, err)
	}
	second, err := engine.ListSchemas(ctx, "snapshot-a", first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != "" {
		t.Fatalf("second schema page = %#v, %v", second, err)
	}
	if _, err := engine.ListSchemas(ctx, "snapshot-a", "!not-a-cursor", 1); !errors.Is(err, formapi.ErrInvalid) {
		t.Fatalf("invalid cursor = %v, want invalid request", err)
	}
}

func TestAuthenticatedContextCannotBeReusedAcrossEngines(t *testing.T) {
	first, _, _, _ := newEngine(t, core.AccessLevelRead, nil)
	second, _, secondAuthorization, _ := newEngine(t, core.AccessLevelRead, nil)
	ctx := authenticatedContext(t, first)
	if _, err := second.ListSchemas(ctx, "snapshot-a", "", 10); !errors.Is(err, formapi.ErrUnauthorized) {
		t.Fatalf("cross-engine auth context = %v, want unauthorized", err)
	}
	if len(secondAuthorization.checkedGroups) != 0 {
		t.Fatalf("cross-engine auth context reached group authorization: %#v", secondAuthorization)
	}
}

func TestListSubmissionsAppliesFormAndStateSelectors(t *testing.T) {
	engine, data, _, _ := newEngine(t, core.AccessLevelRead, nil)
	seedSubmission(t, data, "snapshot-a", "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", State: "new", Values: map[string]any{"name": "Ada"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	seedSubmission(t, data, "snapshot-a", "entry-b", submissionpkg.PayloadV1{
		ID: "entry-b", FormID: "form-a", State: "archived", Values: map[string]any{"name": "Grace"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
	})
	seedSubmission(t, data, "snapshot-a", "entry-c", submissionpkg.PayloadV1{
		ID: "entry-c", FormID: "form-b", State: "new", Values: map[string]any{"name": "Lin"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC),
	})

	page, err := engine.ListSubmissions(authenticatedContext(t, engine), formapi.ListSubmissionsRequest{
		SnapshotID: "snapshot-a", FormID: "form-a", State: "new", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "entry-a" || page.Items[0].Values["name"] != "Ada" {
		t.Fatalf("form/state filters returned %#v", page.Items)
	}
}

func TestSubmissionStatusUpdateUsesCAS(t *testing.T) {
	engine, data, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	seedSubmission(t, data, "snapshot-a", "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", State: "new", Values: map[string]any{"name": "Ada"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})

	updated, err := engine.SetSubmissionStatus(authenticatedContext(t, engine), formapi.SetSubmissionStatusRequest{
		SnapshotID: "snapshot-a", SubmissionID: "entry-a", Status: "reviewed", ExpectedRevision: 1,
	})
	if err != nil || updated.State != "reviewed" || updated.Revision != 2 || updated.Values["name"] != "Ada" {
		t.Fatalf("status change = %#v, %v", updated, err)
	}
	if _, err := engine.SetSubmissionStatus(authenticatedContext(t, engine), formapi.SetSubmissionStatusRequest{
		SnapshotID: "snapshot-a", SubmissionID: "entry-a", Status: "archived", ExpectedRevision: 1,
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("stale status update = %v, want conflict", err)
	}
}

func TestDeleteSubmissionStaleRevisionCannotDeleteAttachments(t *testing.T) {
	files := &fakeFiles{files: map[string]core.File{"file-a": {ID: "file-a", Revision: 1}}, failOnce: map[string]error{}}
	engine, data, _, _ := newEngine(t, core.AccessLevelWrite, files)
	seedSubmission(t, data, "snapshot-a", "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", State: "new", Values: map[string]any{}, AttachmentIDs: []string{"file-a"},
	})
	err := engine.DeleteSubmission(authenticatedContext(t, engine), formapi.DeleteSubmissionRequest{
		SnapshotID: "snapshot-a", SubmissionID: "entry-a", ExpectedRevision: 42,
	})
	if !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("stale delete = %v, want conflict", err)
	}
	if _, ok := files.files["file-a"]; !ok {
		t.Fatal("stale delete removed an attachment")
	}
	if _, err := data.Get(context.Background(), submissionKey("snapshot-a", "entry-a")); err != nil {
		t.Fatalf("stale delete removed the submission: %v", err)
	}
}

func TestDeleteSubmissionResumesAfterPartialAttachmentCleanup(t *testing.T) {
	files := &fakeFiles{files: map[string]core.File{
		"file-a": {ID: "file-a", Revision: 1},
		"file-b": {ID: "file-b", Revision: 1},
	}, failOnce: map[string]error{"file-b": errors.New("storage unavailable")}}
	engine, data, _, _ := newEngine(t, core.AccessLevelWrite, files)
	seedSubmission(t, data, "snapshot-a", "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", State: "new", Values: map[string]any{"name": "Ada"},
		AttachmentIDs: []string{"file-a", "file-b"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})

	request := formapi.DeleteSubmissionRequest{SnapshotID: "snapshot-a", SubmissionID: "entry-a", ExpectedRevision: 1}
	if err := engine.DeleteSubmission(authenticatedContext(t, engine), request); !errors.Is(err, formapi.ErrUnavailable) {
		t.Fatalf("partial attachment deletion = %v, want retryable unavailable", err)
	}
	if _, ok := files.files["file-a"]; ok {
		t.Fatal("first attachment was not deleted")
	}
	if _, err := data.Get(context.Background(), submissionKey("snapshot-a", "entry-a")); err != nil {
		t.Fatalf("submission record must remain durable until every attachment is deleted: %v", err)
	}

	if err := engine.DeleteSubmission(authenticatedContext(t, engine), request); err != nil {
		t.Fatalf("retry did not resume exact attachment cleanup: %v", err)
	}
	if len(files.files) != 0 {
		t.Fatalf("attachments remain after retry: %#v", files.files)
	}
	if _, err := data.Get(context.Background(), submissionKey("snapshot-a", "entry-a")); !errors.Is(err, core.ErrModuleDataNotFound) {
		t.Fatalf("submission record remains after full cleanup: %v", err)
	}
}

func authenticatedContext(t *testing.T, api formapi.API) context.Context {
	t.Helper()
	ctx, err := api.Authenticate(context.Background(), "forms-admin")
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func newEngine(t *testing.T, level core.AccessLevel, files *fakeFiles) (*forms.Engine, *fakeDataStore, *fakeAuthorization, *fakeFiles) {
	t.Helper()
	data := newFakeDataStore()
	authorization := &fakeAuthorization{level: level}
	if files == nil {
		files = &fakeFiles{files: map[string]core.File{}, failOnce: map[string]error{}}
	}
	engine, err := forms.New(forms.Dependencies{
		Identity:      fakeIdentity{},
		Sessions:      fakeSessions{},
		Authorization: authorization,
		Files:         files,
		Data:          data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, data, authorization, files
}

func seedSubmission(t *testing.T, data *fakeDataStore, snapshotID, id string, payload submissionpkg.PayloadV1) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(map[string]any{"form_id": payload.FormID, "state": payload.State, "created_at": payload.CreatedAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.Put(context.Background(), submissionKey(snapshotID, id), core.ModuleDataValue{
		Payload: encoded, PayloadVersion: submissionpkg.PayloadVersion, IndexData: index,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func submissionKey(snapshotID, id string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: "forms/submissions", ScopeType: "submission", ScopeID: snapshotID, RecordKey: id}
}

type fakeIdentity struct{}

func (fakeIdentity) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{ID: "admin-1", Active: true}, nil
}

type fakeSessions struct{}

func (fakeSessions) CreateSession(context.Context, string) (core.CreatedSession, error) {
	return core.CreatedSession{}, core.ErrUnavailable
}
func (fakeSessions) AuthenticateSession(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{ID: "admin-1", Active: true}, nil
}
func (fakeSessions) RevokeSession(context.Context, string) error { return nil }

type fakeAuthorization struct {
	level         core.AccessLevel
	checkedGroups []string
	checkedLevel  core.AccessLevel
}

func (authorization *fakeAuthorization) Authenticate(context.Context, string) (core.Session, core.Principal, error) {
	return core.Session{}, core.Principal{ID: "admin-1", Active: true}, nil
}
func (authorization *fakeAuthorization) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, required core.AccessLevel) error {
	authorization.checkedGroups = append([]string(nil), groups...)
	authorization.checkedLevel = required
	if authorization.level < required {
		return core.ErrForbidden
	}
	return nil
}

type fakeFiles struct {
	mu       sync.Mutex
	files    map[string]core.File
	failOnce map[string]error
}

func (files *fakeFiles) StatFile(_ context.Context, _ core.Session, id string) (core.File, error) {
	files.mu.Lock()
	defer files.mu.Unlock()
	file, ok := files.files[id]
	if !ok {
		return core.File{}, core.ErrFileNotFound
	}
	return file, nil
}
func (files *fakeFiles) DeleteFile(_ context.Context, _ core.Session, id string, expectedRevision int64) error {
	files.mu.Lock()
	defer files.mu.Unlock()
	if err, exists := files.failOnce[id]; exists {
		delete(files.failOnce, id)
		return err
	}
	file, ok := files.files[id]
	if !ok {
		return core.ErrFileNotFound
	}
	if file.Revision != expectedRevision {
		return core.ErrRevisionConflict
	}
	delete(files.files, id)
	return nil
}

type fakeDataStore struct {
	mu      sync.Mutex
	records map[core.ModuleDataKey]core.ModuleDataRecord
}

func newFakeDataStore() *fakeDataStore {
	return &fakeDataStore{records: make(map[core.ModuleDataKey]core.ModuleDataRecord)}
}
func (*fakeDataStore) MaxPageSize() int     { return 100 }
func (*fakeDataStore) MaxPayloadBytes() int { return 1 << 20 }
func (*fakeDataStore) MaxIndexBytes() int   { return 1 << 16 }
func (*fakeDataStore) MaxBatchSize() int    { return 100 }
func (*fakeDataStore) MaxBatchBytes() int   { return 8 << 20 }

func (data *fakeDataStore) Get(_ context.Context, key core.ModuleDataKey) (core.ModuleDataRecord, error) {
	data.mu.Lock()
	defer data.mu.Unlock()
	record, ok := data.records[key]
	if !ok {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return cloneRecord(record), nil
}

func (data *fakeDataStore) Put(ctx context.Context, key core.ModuleDataKey, value core.ModuleDataValue, expected int64) (core.ModuleDataRecord, error) {
	if err := data.Batch(ctx, []core.ModuleDataMutation{{Key: key, ExpectedRevision: expected, Value: &value}}); err != nil {
		return core.ModuleDataRecord{}, err
	}
	return data.Get(ctx, key)
}

func (data *fakeDataStore) Delete(_ context.Context, key core.ModuleDataKey, expected int64) error {
	data.mu.Lock()
	defer data.mu.Unlock()
	current, ok := data.records[key]
	if !ok {
		return core.ErrModuleDataNotFound
	}
	if current.Revision != expected {
		return core.ErrModuleDataConflict
	}
	delete(data.records, key)
	return nil
}

func (data *fakeDataStore) Batch(_ context.Context, mutations []core.ModuleDataMutation) error {
	data.mu.Lock()
	defer data.mu.Unlock()
	if len(mutations) == 0 || len(mutations) > data.MaxBatchSize() {
		return core.ErrModuleDataInvalid
	}
	for _, mutation := range mutations {
		current, exists := data.records[mutation.Key]
		if mutation.Value == nil {
			if !exists {
				return core.ErrModuleDataNotFound
			}
			if current.Revision != mutation.ExpectedRevision {
				return core.ErrModuleDataConflict
			}
			continue
		}
		if mutation.ExpectedRevision == 0 && exists || mutation.ExpectedRevision > 0 && (!exists || current.Revision != mutation.ExpectedRevision) {
			return core.ErrModuleDataConflict
		}
	}
	now := time.Now().UTC()
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(data.records, mutation.Key)
			continue
		}
		current := data.records[mutation.Key]
		revision := int64(1)
		created := now
		if current.Revision > 0 {
			revision = current.Revision + 1
			created = current.CreatedAt
		}
		data.records[mutation.Key] = core.ModuleDataRecord{Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
			IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: revision, CreatedAt: created, UpdatedAt: now}
	}
	return nil
}

func (data *fakeDataStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []core.ModuleDataSelector, limit int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error) {
	data.mu.Lock()
	defer data.mu.Unlock()
	keys := make([]core.ModuleDataKey, 0)
	for key, record := range data.records {
		if key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matchesSelectors(record.IndexData, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	if limit < 1 || limit > data.MaxPageSize() {
		return nil, nil, "", core.ErrModuleDataLimitExceeded
	}
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	rows := make([]core.ModuleDataRecord, len(keys))
	for i, key := range keys {
		rows[i] = cloneRecord(data.records[key])
	}
	next := ""
	if more {
		next = keys[len(keys)-1].RecordKey
	}
	return keys, rows, next, nil
}

func matchesSelectors(index json.RawMessage, selectors []core.ModuleDataSelector) bool {
	if len(selectors) == 0 {
		return true
	}
	var values map[string]any
	if json.Unmarshal(index, &values) != nil {
		return false
	}
	for _, selector := range selectors {
		if selector.Operator != core.SelectorEqual || len(selector.Values) != 1 || fmt.Sprint(values[selector.Field]) != fmt.Sprint(selector.Values[0]) {
			return false
		}
	}
	return true
}

func cloneRecord(record core.ModuleDataRecord) core.ModuleDataRecord {
	record.Payload = append(json.RawMessage(nil), record.Payload...)
	record.IndexData = append(json.RawMessage(nil), record.IndexData...)
	return record
}
