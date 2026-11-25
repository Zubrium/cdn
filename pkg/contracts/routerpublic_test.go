package contracts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type rejectingDispatcher struct{ calls int }

func (dispatcher *rejectingDispatcher) Dispatch(_ context.Context, request Envelope) (Envelope, error) {
	dispatcher.calls++
	return Envelope{OperationID: request.OperationID, Version: request.Version, Payload: []byte(`{}`)}, nil
}

func TestPublicOperationKeysAreVersioned(t *testing.T) {
	operations := []struct {
		name string
		key  Key
	}{
		{"validate-form", PublicFormValidateOperation().Key},
		{"validate-upload-route", PublicFormValidateUploadOperation().Key},
		{"create-session", PublicFormCreateSessionOperation().Key},
		{"get-session", PublicFormGetSessionOperation().Key},
		{"cancel-session", PublicFormCancelSessionOperation().Key},
		{"begin-upload", PublicFormBeginUploadOperation().Key},
		{"complete-upload", PublicFormCompleteUploadOperation().Key},
		{"abort-upload", PublicFormAbortUploadOperation().Key},
		{"submit-response", PublicFormSubmitOperation().Key},
		{"loader-begin", PublicLoaderBeginUploadOperation().Key},
		{"loader-put-part", PublicLoaderPutPartOperation().Key},
		{"loader-commit", PublicLoaderCommitOperation().Key},
		{"loader-abort", PublicLoaderAbortOperation().Key},
		{"loader-stat", PublicLoaderStatFileOperation().Key},
		{"loader-delete", PublicLoaderDeleteFileOperation().Key},
		{"loader-get-fileset", PublicLoaderGetFileSetOperation().Key},
		{"loader-add-fileset", PublicLoaderAddFileSetOperation().Key},
	}
	for _, operation := range operations {
		if operation.key.Version != PublicOperationContractVersion || PublicOperationContractVersion != 1 {
			t.Fatalf("%s version = %d", operation.name, operation.key.Version)
		}
		if !strings.HasPrefix(operation.key.ID, "forms.public-") && !strings.HasPrefix(operation.key.ID, "loader.public-") {
			t.Fatalf("%s key = %q", operation.name, operation.key.ID)
		}
	}
}

func TestPublicPayloadRejectsIdentitySubstitution(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"forms-binding", `{"form_id":"form-1","schema_version":1,"module":"engine.forms","class":"forms","id":"override","engine":"forms","credential":"secret"}`},
		{"loader-begin", `{"stream_ref":"ref","name":"file","idempotency_key":"1111111111111111","content_type":"image/png","declared_size":1,"max_bytes":10,"part_size":5,"groups":["uploads"],"module":"engine.loader","class":"loader","id":"override","credential":"secret"}`},
		{"form-submit", `{"form_id":"form-1","schema_version":1,"token":"token","idempotency_key":"1111111111111111","values":{"a":"b"},"module":"engine.forms","class":"forms","id":"override"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch test.name {
			case "forms-binding":
				_, err := DecodePayload[PublicFormBinding]([]byte(test.payload))
				requireRequestMismatch(t, err)
			case "loader-begin":
				_, err := DecodePayload[PublicLoaderBeginUploadRequest]([]byte(test.payload))
				requireRequestMismatch(t, err)
			default:
				_, err := DecodePayload[PublicSubmitVisitorResponseRequest]([]byte(test.payload))
				requireRequestMismatch(t, err)
			}
		})
	}
}

func requireRequestMismatch(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrRequestTypeMismatch) {
		t.Fatalf("error = %v, want ErrRequestTypeMismatch", err)
	}
}

func TestPublicOperationValidatesBeforeDispatch(t *testing.T) {
	dispatcher := &rejectingDispatcher{}
	_, err := Call(context.Background(), dispatcher, PublicFormValidateOperation(), PublicFormBinding{})
	if !errors.Is(err, ErrPublicInvalid) {
		t.Fatalf("error = %v, want ErrPublicInvalid", err)
	}
	_, err = Call(context.Background(), dispatcher, PublicLoaderBeginUploadOperation(), PublicLoaderBeginUploadRequest{
		Stream: "ref", Name: "file", IdempotencyKey: "1111111111111111", ContentType: "image/png",
		DeclaredSize: 1, MaxBytes: 10, PartSize: 5, Groups: []string{"uploads"},
	})
	if err != nil {
		t.Fatalf("valid request error = %v", err)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls = %d, want 1", dispatcher.calls)
	}
}

func TestPublicLoaderBeginUploadValidation(t *testing.T) {
	base := PublicLoaderBeginUploadRequest{
		Stream: "ref", Name: "file", IdempotencyKey: "1111111111111111", ContentType: "image/png",
		DeclaredSize: -1, MaxBytes: 10, PartSize: 5, Groups: []string{"uploads"},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("chunked size validation: %v", err)
	}
	base.Name = ""
	if err := base.Validate(); !errors.Is(err, ErrPublicInvalid) {
		t.Fatalf("empty name error = %v", err)
	}
}
