package forms_test

import (
	"context"
	"errors"
	"testing"

	core "github.com/Zubrium/cdn/core/presentation"
	forms "github.com/Zubrium/cdn/engines/forms"
	formapi "github.com/Zubrium/cdn/engines/forms/presentation"
)

func TestPublicFormSessionValidatesAndAcceptsImmutableSubmission(t *testing.T) {
	api, _, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	admin := authenticatedContext(t, api)
	schema, err := api.SaveSchema(admin, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{
			"email":   map[string]any{"type": "email", "required": true},
			"message": map[string]any{"type": "text", "required": false, "max_length": 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := api.CreateVisitorSession(context.Background(), formapi.CreateVisitorSessionRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
	})
	if err != nil || created.Token == "" || created.Session.FormID != schema.ID || len(created.Session.Fields) != 2 {
		t.Fatalf("CreateVisitorSession = %#v, %v", created, err)
	}
	resumed, err := api.GetVisitorSession(context.Background(), formapi.GetVisitorSessionRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version}, Token: created.Token,
	})
	if err != nil || resumed.ID != created.Session.ID {
		t.Fatalf("GetVisitorSession = %#v, %v", resumed, err)
	}

	submit := formapi.SubmitVisitorResponseRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
		Token:             created.Token, IdempotencyKey: "submission-key-0001",
		Values: map[string]any{"email": "visitor@example.test", "message": "hello"},
	}
	first, err := api.SubmitVisitorResponse(context.Background(), submit)
	if err != nil || first.ID == "" {
		t.Fatalf("SubmitVisitorResponse = %#v, %v", first, err)
	}
	retry, err := api.SubmitVisitorResponse(context.Background(), submit)
	if err != nil || retry.ID != first.ID {
		t.Fatalf("idempotent submission retry = %#v, %v; want %q", retry, err, first.ID)
	}
	submit.Values = map[string]any{"email": "different@example.test"}
	if _, err := api.SubmitVisitorResponse(context.Background(), submit); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("changed body with same idempotency key = %v, want conflict", err)
	}
	if _, err := api.SubmitVisitorResponse(context.Background(), formapi.SubmitVisitorResponseRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
		Token:             created.Token, IdempotencyKey: "submission-key-0002", Values: submit.Values,
	}); !errors.Is(err, formapi.ErrConflict) {
		t.Fatalf("second accepted answer for a completed session = %v, want conflict", err)
	}

	page, err := api.ListSubmissions(admin, formapi.ListSubmissionsRequest{FormID: schema.ID, Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != first.ID || page.Items[0].SchemaVersion != schema.Version {
		t.Fatalf("accepted submission page = %#v, %v", page, err)
	}
	byKey, err := api.ListSubmissions(admin, formapi.ListSubmissionsRequest{FormID: schema.ID, Key: schema.Key, Limit: 10})
	if err != nil || len(byKey.Items) != 1 || byKey.Items[0].ID != first.ID {
		t.Fatalf("submission filtered by form key %q = %#v, %v", schema.Key, byKey, err)
	}
	wrongKey, err := api.ListSubmissions(admin, formapi.ListSubmissionsRequest{FormID: schema.ID, Key: "another-key", Limit: 10})
	if err != nil || len(wrongKey.Items) != 0 {
		t.Fatalf("submission filtered by another key = %#v, %v", wrongKey, err)
	}
}

func TestPublicFormSessionCannotSubmitInvalidOrUnknownValues(t *testing.T) {
	api, _, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	schema, err := api.SaveSchema(authenticatedContext(t, api), formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{
			"email": map[string]any{"type": "email", "required": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := api.CreateVisitorSession(context.Background(), formapi.CreateVisitorSessionRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]any{
		{"email": "not-an-email"},
		{"email": "visitor@example.test", "admin": true},
		{},
	} {
		_, err := api.SubmitVisitorResponse(context.Background(), formapi.SubmitVisitorResponseRequest{
			PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
			Token:             session.Token, IdempotencyKey: "invalid-answer-0001", Values: values,
		})
		if !errors.Is(err, formapi.ErrInvalid) {
			t.Fatalf("invalid values %#v = %v, want invalid request", values, err)
		}
	}
	if page, err := api.ListSubmissions(authenticatedContext(t, api), formapi.ListSubmissionsRequest{FormID: schema.ID, Limit: 10}); err != nil || len(page.Items) != 0 {
		t.Fatalf("invalid answers created submissions: page=%#v err=%v", page, err)
	}
}

func TestCompletedUploadRetryRemainsSafeAfterItsFormWasSubmitted(t *testing.T) {
	api, _, _, files := newEngine(t, core.AccessLevelWrite, nil)
	schema, err := api.SaveSchema(authenticatedContext(t, api), formapi.SaveSchemaRequest{
		Key: "attachments", Fields: map[string]any{
			"document": map[string]any{"type": "file", "required": true, "max_files": 1, "max_file_bytes": 1024, "allowed_content_types": []any{"application/pdf"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := api.CreateVisitorSession(context.Background(), formapi.CreateVisitorSessionRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
	})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := api.BeginVisitorUpload(context.Background(), formapi.BeginVisitorUploadRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
		Token:             session.Token, FieldID: "document", IdempotencyKey: "visitor-upload-key-001", ContentType: "application/pdf",
		DeclaredSize: 3, RouteMaxBytes: 1024, RouteContentTypes: []string{"application/pdf"}, AccessGroup: "uploads",
	})
	if err != nil {
		t.Fatal(err)
	}
	const fileID = "01938f7a-68af-7c10-a51d-78b6ca125312"
	const checksum = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	files.mu.Lock()
	files.files[fileID] = core.File{ID: fileID, Groups: []string{"uploads"}, ContentType: "application/pdf", SHA256: checksum, Revision: 1, Size: 3}
	files.mu.Unlock()
	complete := formapi.CompleteVisitorUploadRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
		Token:             session.Token, ReservationID: reservation.ID, FileID: fileID, SHA256: checksum,
		ContentType: "application/pdf", Size: 3, AccessGroup: "uploads",
	}
	attachment, err := api.CompleteVisitorUpload(context.Background(), complete)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.SubmitVisitorResponse(context.Background(), formapi.SubmitVisitorResponseRequest{
		PublicFormBinding: complete.PublicFormBinding, Token: session.Token, IdempotencyKey: "submit-uploaded-file-01",
		Values: map[string]any{"document": []any{attachment.ID}},
	}); err != nil {
		t.Fatalf("submit uploaded file: %v", err)
	}
	retried, err := api.CompleteVisitorUpload(context.Background(), complete)
	if err != nil || retried.ID != attachment.ID || retried.FieldID != attachment.FieldID {
		t.Fatalf("retry of completed attachment after submit = %#v, %v; want same attachment %#v", retried, err, attachment)
	}
}

func TestPublicFormRouteVersionChangeRejectsExistingSession(t *testing.T) {
	api, _, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	admin := authenticatedContext(t, api)
	schema, err := api.SaveSchema(admin, formapi.SaveSchemaRequest{
		Key: "contact", Fields: map[string]any{"email": map[string]any{"type": "email", "required": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := api.CreateVisitorSession(context.Background(), formapi.CreateVisitorSessionRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := api.SaveSchema(admin, formapi.SaveSchemaRequest{
		SchemaID: schema.ID, Key: schema.Key,
		Fields:           map[string]any{"email": map[string]any{"type": "email", "required": true}, "name": map[string]any{"type": "text", "required": false}},
		ExpectedRevision: schema.Revision,
	})
	if err != nil || updated.Version != schema.Version+1 {
		t.Fatalf("schema update = %#v, %v", updated, err)
	}
	_, err = api.SubmitVisitorResponse(context.Background(), formapi.SubmitVisitorResponseRequest{
		PublicFormBinding: formapi.PublicFormBinding{FormID: schema.ID, SchemaVersion: schema.Version},
		Token:             session.Token, IdempotencyKey: "stale-form-answer-01", Values: map[string]any{"email": "visitor@example.test"},
	})
	if !errors.Is(err, formapi.ErrExpired) {
		t.Fatalf("schema update did not cancel the old visitor session: %v", err)
	}
}

func TestSchemaValidationRejectsUnsupportedAndUnsafeFieldDefinitions(t *testing.T) {
	api, data, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	for _, fields := range []map[string]any{
		{"value": map[string]any{"type": "unknown", "required": false}},
		{"value": map[string]any{"type": "text", "required": false, "pattern": "("}},
		{"value": map[string]any{"type": "select", "required": false}},
		{"upload": map[string]any{"type": "file", "required": false, "max_files": 1, "max_file_bytes": 1024, "allowed_content_types": []any{"text/html"}}},
	} {
		if _, err := api.SaveSchema(authenticatedContext(t, api), formapi.SaveSchemaRequest{
			Key: "invalid", Fields: fields,
		}); !errors.Is(err, formapi.ErrInvalid) {
			t.Fatalf("invalid field schema %#v = %v, want invalid", fields, err)
		}
	}
	if len(data.records) != 0 {
		t.Fatalf("invalid schemas persisted %d records", len(data.records))
	}
}

func TestSchemaAcceptsExplicitSupportedFieldTypesAndConstraints(t *testing.T) {
	api, _, _, _ := newEngine(t, core.AccessLevelWrite, nil)
	schema, err := api.SaveSchema(authenticatedContext(t, api), formapi.SaveSchemaRequest{
		Key: "typed-contact", Fields: map[string]any{
			"name":       map[string]any{"type": "text", "required": true, "min_length": 2, "max_length": 80, "pattern": "^[A-Za-z ]+$"},
			"email":      map[string]any{"type": "email", "required": true, "max_length": 120},
			"age":        map[string]any{"type": "number", "required": false, "minimum": 18, "maximum": 120},
			"subscribe":  map[string]any{"type": "boolean", "required": false},
			"department": map[string]any{"type": "select", "required": true, "options": []any{"sales", "support"}},
			"document":   map[string]any{"type": "file", "required": false, "max_files": 2, "max_file_bytes": 1048576, "allowed_content_types": []any{"application/pdf", "image/png"}},
		},
	})
	if err != nil {
		t.Fatalf("valid explicit field definitions were rejected: %v", err)
	}
	if len(schema.Fields) != 6 {
		t.Fatalf("schema did not persist all typed fields: %#v", schema.Fields)
	}
}

var _ formapi.API = (*forms.Engine)(nil)
