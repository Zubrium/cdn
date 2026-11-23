package forms_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	forms "github.com/Zubrium/cdn/engines/forms"
	submissionpkg "github.com/Zubrium/cdn/engines/forms/features/submissions/pkg"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

func TestSaveSchemaUsesGeneratedIDsAndIdempotentUniqueKey(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	request := formapi.SaveSchemaRequest{
		Key:    "contact-us",
		Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
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
	if len(data.records) != 2 {
		t.Fatalf("schema and unique-key index should be persisted atomically, got %d records", len(data.records))
	}
}

func TestSaveSchemaRejectsDuplicateKeyAndUsesCASOnUpdate(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	created, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "text", "required": false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("duplicate key with different schema = %v, want conflict", err)
	}

	updated, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SchemaID: created.ID, Key: "contact-v2",
		Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}}, ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Key != "contact-v2" || updated.Version != 2 || updated.Revision != 2 {
		t.Fatalf("updated schema did not preserve identity and increment version/revision: %#v", updated)
	}
	if len(data.records) != 2 {
		t.Fatalf("renaming should atomically retain one schema and one key index, got %d records", len(data.records))
	}
	if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SchemaID: created.ID, Key: "contact-v2",
		Fields: map[string]any{"email": map[string]any{"type": "text", "required": false}}, ExpectedRevision: created.Revision,
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("stale schema write = %v, want conflict", err)
	}

	oldKey, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "text", "required": false, "max_length": 128}},
	})
	if err != nil || oldKey.ID == created.ID {
		t.Fatalf("renaming should atomically release the old key: new=%#v err=%v", oldKey, err)
	}
}

func TestFormKeyIsGloballyUniqueAndReleasedOnRename(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	first, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "text", "required": false}},
	})
	if !errors.Is(err, formapi.ErrConflict) || duplicate.ID != "" {
		t.Fatalf("globally duplicate key with different fields = %#v, %v, want conflict", duplicate, err)
	}
	for key, record := range data.records {
		if key.ScopeType != "global" || key.ScopeID != "global" {
			t.Fatalf("schema record stored outside global scope: %#v (revision %d)", key, record.Revision)
		}
	}
	renamed, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		SchemaID: first.ID, Key: "contact-v2", Fields: first.Fields, ExpectedRevision: first.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	reused, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"message": map[string]any{"type": "text", "required": true}},
	})
	if err != nil || reused.ID == "" || reused.ID == renamed.ID || reused.ID == first.ID {
		t.Fatalf("released form key could not be reused: %#v, %v", reused, err)
	}
}

func TestFormsNamespacesUseFixedGlobalScope(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	schema, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{ID: "entry-a", FormID: schema.ID, FormKey: schema.Key, SchemaVersion: 1, State: "new"})
	if _, err := engine.CreateVisitorSession(ctx, formapi.CreateVisitorSessionRequest{PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: 1}}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for key := range data.records {
		if key.ScopeType != "global" || key.ScopeID != "global" {
			t.Fatalf("Forms record stored outside global scope: %#v", key)
		}
		seen[key.Namespace] = true
	}
	for _, namespace := range []string{"forms/schemas", "forms/submissions", "forms/sessions"} {
		if !seen[namespace] {
			t.Errorf("namespace %q missing from global-scope records: %v", namespace, seen)
		}
	}
}

func TestFormsReadsRequireDirectReadGroupAccess(t *testing.T) {
	engine, _, authorization, _ := newEngine(t, recorddata.AccessLevelDenied, nil)
	ctx := authenticatedContext(t, engine)
	if _, err := engine.ListSchemas(ctx, "", 10); !errors.Is(err, formapi.ErrForbidden) {
		t.Fatalf("read without Forms group access = %v, want forbidden", err)
	}
	if len(authorization.checkedGroups) != 1 || authorization.checkedGroups[0] != formapi.SchemaAccessGroup || authorization.checkedLevel != recorddata.AccessLevelRead {
		t.Fatalf("Forms read authorization did not require direct forms-group level 1: %#v", authorization)
	}
}

func TestSchemaAndSubmissionPermissionsAreIndependent(t *testing.T) {
	engine, data, authorization, _ := newEngine(t, recorddata.AccessLevelDenied, nil)
	authorization.levels = map[string]recorddata.AccessLevel{
		formapi.SchemaAccessGroup:     recorddata.AccessLevelWrite,
		formapi.SubmissionAccessGroup: recorddata.AccessLevelDenied,
	}
	ctx := authenticatedContext(t, engine)
	if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
	}); err != nil {
		t.Fatalf("schema operator could not save a schema: %v", err)
	}
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{ID: "entry-a", FormID: "form-a", SchemaVersion: 1, State: "new"})
	if _, err := engine.ListSubmissions(ctx, formapi.ListSubmissionsRequest{Limit: 10}); !errors.Is(err, formapi.ErrForbidden) {
		t.Fatalf("schema operator could read submissions: %v", err)
	}

	authorization.levels[formapi.SchemaAccessGroup] = recorddata.AccessLevelDenied
	authorization.levels[formapi.SubmissionAccessGroup] = recorddata.AccessLevelRead
	if _, err := engine.ListSchemas(ctx, "", 10); !errors.Is(err, formapi.ErrForbidden) {
		t.Fatalf("submission operator could read schemas: %v", err)
	}
	if _, err := engine.GetSubmission(ctx, "entry-a"); err != nil {
		t.Fatalf("submission operator could not read a response: %v", err)
	}
}

func TestFormsReadAccessCannotWriteSchemas(t *testing.T) {
	engine, data, authorization, _ := newEngine(t, recorddata.AccessLevelRead, nil)
	_, err := engine.SaveSchema(authenticatedContext(t, engine), formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{},
	})
	if !errors.Is(err, formapi.ErrForbidden) {
		t.Fatalf("schema write with read-only Forms access = %v, want forbidden", err)
	}
	if len(data.records) != 0 || authorization.checkedLevel != recorddata.AccessLevelWrite {
		t.Fatalf("read-only write changed data or checked wrong level: records=%d authorization=%#v", len(data.records), authorization)
	}
}

func TestListSchemasUsesOpaqueCursorAndRejectsInvalidCursor(t *testing.T) {
	engine, _, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	for _, key := range []string{"alpha", "beta"} {
		if _, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{Key: key, Fields: map[string]any{"message": map[string]any{"type": "text", "required": false}}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := engine.ListSchemas(ctx, "", 1)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first schema page = %#v, %v", first, err)
	}
	second, err := engine.ListSchemas(ctx, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != "" {
		t.Fatalf("second schema page = %#v, %v", second, err)
	}
	if _, err := engine.ListSchemas(ctx, "!not-a-cursor", 1); !errors.Is(err, formapi.ErrInvalid) {
		t.Fatalf("invalid cursor = %v, want invalid request", err)
	}
}

func TestGetSchemaAndSubmissionReturnPersistedRevisions(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	ctx := authenticatedContext(t, engine)
	saved, err := engine.SaveSchema(ctx, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	loadedSchema, err := engine.GetSchema(ctx, saved.ID)
	if err != nil || loadedSchema.Revision != saved.Revision {
		t.Fatalf("GetSchema = %#v, %v", loadedSchema, err)
	}
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: saved.ID, SchemaVersion: 1, State: "new", Values: map[string]any{"email": "ada@example.test"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	loadedSubmission, err := engine.GetSubmission(ctx, "entry-a")
	if err != nil || loadedSubmission.Revision != 1 || loadedSubmission.Values["email"] != "ada@example.test" {
		t.Fatalf("GetSubmission = %#v, %v", loadedSubmission, err)
	}
}

func TestAuthenticatedContextCannotBeReusedAcrossEngines(t *testing.T) {
	first, _, _, _ := newEngine(t, recorddata.AccessLevelRead, nil)
	second, _, secondAuthorization, _ := newEngine(t, recorddata.AccessLevelRead, nil)
	ctx := authenticatedContext(t, first)
	if _, err := second.ListSchemas(ctx, "", 10); !errors.Is(err, formapi.ErrUnauthorized) {
		t.Fatalf("cross-engine auth context = %v, want unauthorized", err)
	}
	if len(secondAuthorization.checkedGroups) != 0 {
		t.Fatalf("cross-engine auth context reached group authorization: %#v", secondAuthorization)
	}
}

func TestListSubmissionsAppliesFormAndStateSelectors(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelRead, nil)
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Ada"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	seedSubmission(t, data, "entry-b", submissionpkg.PayloadV1{
		ID: "entry-b", FormID: "form-a", SchemaVersion: 1, State: "archived", Values: map[string]any{"name": "Grace"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
	})
	seedSubmission(t, data, "entry-c", submissionpkg.PayloadV1{
		ID: "entry-c", FormID: "form-b", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Lin"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC),
	})

	page, err := engine.ListSubmissions(authenticatedContext(t, engine), formapi.ListSubmissionsRequest{
		FormID: "form-a", State: "new", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "entry-a" || page.Items[0].Values["name"] != "Ada" {
		t.Fatalf("form/state filters returned %#v", page.Items)
	}
}

func TestListSubmissionsFiltersByFormIDAndKey(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelRead, nil)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", FormKey: "contact", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Ada"}, CreatedAt: created,
	})
	seedSubmission(t, data, "entry-b", submissionpkg.PayloadV1{
		ID: "entry-b", FormID: "form-a", FormKey: "support", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Grace"}, CreatedAt: created.Add(time.Second),
	})
	seedSubmission(t, data, "entry-c", submissionpkg.PayloadV1{
		ID: "entry-c", FormID: "form-b", FormKey: "contact", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Lin"}, CreatedAt: created.Add(2 * time.Second),
	})
	ctx := authenticatedContext(t, engine)
	page, err := engine.ListSubmissions(ctx, formapi.ListSubmissionsRequest{FormID: "form-a", Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("form filter = %#v, %v, want two submissions", page.Items, err)
	}
	filtered, err := engine.ListSubmissions(ctx, formapi.ListSubmissionsRequest{FormID: "form-a", Key: "support", Limit: 10})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != "entry-b" {
		t.Fatalf("form/key filter = %#v, %v", filtered.Items, err)
	}
	if _, err := engine.ListSubmissions(ctx, formapi.ListSubmissionsRequest{Key: "contact", Limit: 10}); !errors.Is(err, formapi.ErrInvalid) {
		t.Fatalf("missing mandatory form_id = %v, want invalid request", err)
	}
}

func TestSubmissionStatusUpdateUsesCAS(t *testing.T) {
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, nil)
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Ada"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})

	updated, err := engine.SetSubmissionStatus(authenticatedContext(t, engine), formapi.SetSubmissionStatusRequest{
		SubmissionID: "entry-a", Status: "reviewed", ExpectedRevision: 1,
	})
	if err != nil || updated.State != "reviewed" || updated.Revision != 2 || updated.Values["name"] != "Ada" {
		t.Fatalf("status change = %#v, %v", updated, err)
	}
	if _, err := engine.SetSubmissionStatus(authenticatedContext(t, engine), formapi.SetSubmissionStatusRequest{
		SubmissionID: "entry-a", Status: "archived", ExpectedRevision: 1,
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("stale status update = %v, want conflict", err)
	}
}

func TestDeleteSubmissionStaleRevisionCannotDeleteAttachments(t *testing.T) {
	files := &fakeFiles{files: map[string]recorddata.File{"file-a": {ID: "file-a", Revision: 1}}, failOnce: map[string]error{}}
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, files)
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", SchemaVersion: 1, State: "new", Values: map[string]any{}, AttachmentIDs: []string{"file-a"},
	})
	err := engine.DeleteSubmission(authenticatedContext(t, engine), formapi.DeleteSubmissionRequest{
		SubmissionID: "entry-a", ExpectedRevision: 42,
	})
	if !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("stale delete = %v, want conflict", err)
	}
	if _, ok := files.files["file-a"]; !ok {
		t.Fatal("stale delete removed an attachment")
	}
	if _, err := data.Get(context.Background(), submissionKey("entry-a")); err != nil {
		t.Fatalf("stale delete removed the submission: %v", err)
	}
}

func TestDeleteSubmissionResumesAfterPartialAttachmentCleanup(t *testing.T) {
	files := &fakeFiles{files: map[string]recorddata.File{
		"file-a": {ID: "file-a", Revision: 1},
		"file-b": {ID: "file-b", Revision: 1},
	}, failOnce: map[string]error{"file-b": errors.New("storage unavailable")}}
	engine, data, _, _ := newEngine(t, recorddata.AccessLevelWrite, files)
	seedSubmission(t, data, "entry-a", submissionpkg.PayloadV1{
		ID: "entry-a", FormID: "form-a", SchemaVersion: 1, State: "new", Values: map[string]any{"name": "Ada"},
		AttachmentIDs: []string{"file-a", "file-b"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})

	request := formapi.DeleteSubmissionRequest{SubmissionID: "entry-a", ExpectedRevision: 1}
	if err := engine.DeleteSubmission(authenticatedContext(t, engine), request); !errors.Is(err, formapi.ErrUnavailable) {
		t.Fatalf("partial attachment deletion = %v, want retryable unavailable", err)
	}
	if _, ok := files.files["file-a"]; ok {
		t.Fatal("first attachment was not deleted")
	}
	if _, err := data.Get(context.Background(), submissionKey("entry-a")); err != nil {
		t.Fatalf("submission record must remain durable until every attachment is deleted: %v", err)
	}

	if err := engine.DeleteSubmission(authenticatedContext(t, engine), request); err != nil {
		t.Fatalf("retry did not resume exact attachment cleanup: %v", err)
	}
	if len(files.files) != 0 {
		t.Fatalf("attachments remain after retry: %#v", files.files)
	}
	if _, err := data.Get(context.Background(), submissionKey("entry-a")); !errors.Is(err, recorddata.ErrRecordNotFound) {
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

func newEngine(t *testing.T, level recorddata.AccessLevel, files *fakeFiles) (*forms.Engine, *fakeDataStore, *fakeAuthorization, *fakeFiles) {
	t.Helper()
	data := newFakeDataStore()
	authorization := &fakeAuthorization{level: level}
	if files == nil {
		files = &fakeFiles{files: map[string]recorddata.File{}, failOnce: map[string]error{}}
	}
	engine, err := forms.New(forms.Dependencies{
		Identity:          fakeIdentity{},
		Sessions:          fakeSessions{},
		Authorization:     authorization,
		Files:             files,
		Data:              data,
		VisitorSessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, data, authorization, files
}

func seedSubmission(t *testing.T, data *fakeDataStore, id string, payload submissionpkg.PayloadV1) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(map[string]any{"form_id": payload.FormID, "form_key": payload.FormKey, "schema_version": payload.SchemaVersion, "state": payload.State, "created_at": payload.CreatedAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.Put(context.Background(), submissionKey(id), recorddata.RecordValue{
		Payload: encoded, PayloadVersion: submissionpkg.PayloadVersion, IndexData: index,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func submissionKey(id string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: "forms/submissions", ScopeType: "global", ScopeID: "global", RecordKey: id}
}

type fakeIdentity struct{}

func (fakeIdentity) Authenticate(context.Context, string) (recorddata.AuthorizationSubject, recorddata.Principal, error) {
	return recorddata.AuthorizationSubject{}, recorddata.Principal{ID: "admin-1", Active: true}, nil
}

type fakeSessions struct{}

func (fakeSessions) AuthenticateSession(context.Context, string) (recorddata.AuthorizationSubject, recorddata.Principal, error) {
	return recorddata.AuthorizationSubject{}, recorddata.Principal{ID: "admin-1", Active: true}, nil
}
func (fakeSessions) RevokeSession(context.Context, string) error { return nil }

type fakeAuthorization struct {
	level         recorddata.AccessLevel
	levels        map[string]recorddata.AccessLevel
	checkedGroups []string
	checkedLevel  recorddata.AccessLevel
}

func (authorization *fakeAuthorization) Authenticate(context.Context, string) (recorddata.AuthorizationSubject, recorddata.Principal, error) {
	return recorddata.AuthorizationSubject{}, recorddata.Principal{ID: "admin-1", Active: true}, nil
}
func (authorization *fakeAuthorization) RequireGroupLevel(_ context.Context, _ recorddata.AuthorizationSubject, groups []string, required recorddata.AccessLevel) error {
	authorization.checkedGroups = append([]string(nil), groups...)
	authorization.checkedLevel = required
	level := authorization.level
	if len(groups) == 1 && authorization.levels != nil {
		level = authorization.levels[groups[0]]
	}
	if level < required {
		return recorddata.ErrAccessForbidden
	}
	return nil
}

type fakeFiles struct {
	mu       sync.Mutex
	files    map[string]recorddata.File
	failOnce map[string]error
}

func (files *fakeFiles) StatFile(_ context.Context, _ recorddata.AuthorizationSubject, id string) (recorddata.File, error) {
	files.mu.Lock()
	defer files.mu.Unlock()
	file, ok := files.files[id]
	if !ok {
		return recorddata.File{}, recorddata.ErrFileNotFound
	}
	return file, nil
}
func (files *fakeFiles) DeleteFile(_ context.Context, _ recorddata.AuthorizationSubject, id string, expectedRevision int64) error {
	files.mu.Lock()
	defer files.mu.Unlock()
	if err, exists := files.failOnce[id]; exists {
		delete(files.failOnce, id)
		return err
	}
	file, ok := files.files[id]
	if !ok {
		return recorddata.ErrFileNotFound
	}
	if file.Revision != expectedRevision {
		return recorddata.ErrFileRevisionConflict
	}
	delete(files.files, id)
	return nil
}

type fakeDataStore struct {
	mu      sync.Mutex
	records map[recorddata.RecordKey]recorddata.Record
}

func newFakeDataStore() *fakeDataStore {
	return &fakeDataStore{records: make(map[recorddata.RecordKey]recorddata.Record)}
}
func (*fakeDataStore) MaxPageSize() int     { return 100 }
func (*fakeDataStore) MaxPayloadBytes() int { return 1 << 20 }
func (*fakeDataStore) MaxIndexBytes() int   { return 1 << 16 }
func (*fakeDataStore) MaxBatchSize() int    { return 100 }
func (*fakeDataStore) MaxBatchBytes() int   { return 8 << 20 }

func (data *fakeDataStore) Get(_ context.Context, key recorddata.RecordKey) (recorddata.Record, error) {
	data.mu.Lock()
	defer data.mu.Unlock()
	record, ok := data.records[key]
	if !ok || record.ExpiresAt != nil && !record.ExpiresAt.After(time.Now()) {
		return recorddata.Record{}, recorddata.ErrRecordNotFound
	}
	return cloneRecord(record), nil
}

func (data *fakeDataStore) Put(ctx context.Context, key recorddata.RecordKey, value recorddata.RecordValue, expected int64) (recorddata.Record, error) {
	if err := data.Batch(ctx, []recorddata.RecordMutation{{Key: key, ExpectedRevision: expected, Value: &value}}); err != nil {
		return recorddata.Record{}, err
	}
	return data.Get(ctx, key)
}

func (data *fakeDataStore) Delete(_ context.Context, key recorddata.RecordKey, expected int64) error {
	data.mu.Lock()
	defer data.mu.Unlock()
	current, ok := data.records[key]
	if !ok {
		return recorddata.ErrRecordNotFound
	}
	if current.Revision != expected {
		return recorddata.ErrRecordConflict
	}
	delete(data.records, key)
	return nil
}

func (data *fakeDataStore) Batch(_ context.Context, mutations []recorddata.RecordMutation) error {
	data.mu.Lock()
	defer data.mu.Unlock()
	if len(mutations) == 0 || len(mutations) > data.MaxBatchSize() {
		return recorddata.ErrRecordInvalid
	}
	for _, mutation := range mutations {
		current, exists := data.records[mutation.Key]
		if mutation.Value == nil {
			if !exists {
				return recorddata.ErrRecordNotFound
			}
			if current.Revision != mutation.ExpectedRevision {
				return recorddata.ErrRecordConflict
			}
			continue
		}
		if mutation.ExpectedRevision == 0 && exists || mutation.ExpectedRevision > 0 && (!exists || current.Revision != mutation.ExpectedRevision) {
			return recorddata.ErrRecordConflict
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
		record := recorddata.Record{Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
			IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: revision, CreatedAt: created, UpdatedAt: now}
		if mutation.Value.TTL > 0 {
			expiresAt := now.Add(mutation.Value.TTL)
			record.ExpiresAt = &expiresAt
		}
		data.records[mutation.Key] = record
	}
	return nil
}

func (data *fakeDataStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []recorddata.RecordSelector, limit int) ([]recorddata.RecordKey, []recorddata.Record, string, error) {
	data.mu.Lock()
	defer data.mu.Unlock()
	keys := make([]recorddata.RecordKey, 0)
	for key, record := range data.records {
		if record.ExpiresAt != nil && !record.ExpiresAt.After(time.Now()) || key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matchesSelectors(record.IndexData, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	if limit < 1 || limit > data.MaxPageSize() {
		return nil, nil, "", recorddata.ErrRecordLimit
	}
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	rows := make([]recorddata.Record, len(keys))
	for i, key := range keys {
		rows[i] = cloneRecord(data.records[key])
	}
	next := ""
	if more {
		next = keys[len(keys)-1].RecordKey
	}
	return keys, rows, next, nil
}

func (data *fakeDataStore) ListExpired(_ context.Context, namespace, scopeType, scopeID, after string, limit int) ([]recorddata.RecordKey, []recorddata.Record, string, error) {
	data.mu.Lock()
	defer data.mu.Unlock()
	keys := make([]recorddata.RecordKey, 0)
	for key, record := range data.records {
		if record.ExpiresAt == nil || record.ExpiresAt.After(time.Now()) || key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	if limit < 1 || limit > data.MaxPageSize() {
		return nil, nil, "", recorddata.ErrRecordLimit
	}
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	rows := make([]recorddata.Record, len(keys))
	for index, key := range keys {
		rows[index] = cloneRecord(data.records[key])
	}
	next := ""
	if more {
		next = keys[len(keys)-1].RecordKey
	}
	return keys, rows, next, nil
}

func matchesSelectors(index json.RawMessage, selectors []recorddata.RecordSelector) bool {
	if len(selectors) == 0 {
		return true
	}
	var values map[string]any
	if json.Unmarshal(index, &values) != nil {
		return false
	}
	for _, selector := range selectors {
		if selector.Operator != recorddata.RecordSelectorEqual || len(selector.Values) != 1 || fmt.Sprint(values[selector.Field]) != fmt.Sprint(selector.Values[0]) {
			return false
		}
	}
	return true
}

func cloneRecord(record recorddata.Record) recorddata.Record {
	record.Payload = append(json.RawMessage(nil), record.Payload...)
	record.IndexData = append(json.RawMessage(nil), record.IndexData...)
	return record
}
