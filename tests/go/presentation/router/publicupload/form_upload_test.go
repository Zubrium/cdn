package publicupload_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	loader "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	router "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/presentation/router/publicupload"
)

const (
	formSnapshotID = "01938f7a-68af-7c10-a51d-78b6ca125310"
	formID         = "01938f7a-68af-7c10-a51d-78b6ca125311"
	formToken      = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

type fakeVisitorForms struct {
	completeErr   error
	completeCalls int
	abortCalls    int
}

func (*fakeVisitorForms) ValidatePublicForm(context.Context, forms.PublicFormBinding) error {
	return nil
}
func (*fakeVisitorForms) ValidatePublicUploadRoute(context.Context, forms.PublicUploadRouteBinding) error {
	return nil
}
func (*fakeVisitorForms) CreateVisitorSession(context.Context, forms.CreateVisitorSessionRequest) (forms.VisitorSessionCredential, error) {
	return forms.VisitorSessionCredential{}, forms.ErrUnavailable
}
func (*fakeVisitorForms) GetVisitorSession(context.Context, forms.GetVisitorSessionRequest) (forms.VisitorSession, error) {
	return forms.VisitorSession{}, forms.ErrUnavailable
}
func (*fakeVisitorForms) BeginVisitorUpload(_ context.Context, request forms.BeginVisitorUploadRequest) (forms.VisitorUploadReservation, error) {
	return forms.VisitorUploadReservation{ID: uploadID, FieldID: request.FieldID, MaxBytes: request.RouteMaxBytes, AccessGroup: request.AccessGroup}, nil
}
func (f *fakeVisitorForms) CompleteVisitorUpload(context.Context, forms.CompleteVisitorUploadRequest) (forms.VisitorAttachment, error) {
	f.completeCalls++
	if f.completeErr != nil {
		return forms.VisitorAttachment{}, f.completeErr
	}
	return forms.VisitorAttachment{ID: uploadID, FieldID: "attachment"}, nil
}
func (f *fakeVisitorForms) AbortVisitorUpload(context.Context, forms.AbortVisitorUploadRequest) error {
	f.abortCalls++
	return nil
}
func (*fakeVisitorForms) CancelVisitorSession(context.Context, forms.CancelVisitorSessionRequest) error {
	return forms.ErrUnavailable
}
func (*fakeVisitorForms) SubmitVisitorResponse(context.Context, forms.SubmitVisitorResponseRequest) (forms.VisitorSubmissionReceipt, error) {
	return forms.VisitorSubmissionReceipt{}, forms.ErrUnavailable
}

var _ publicupload.GroupAuthorization = (*fakeGroupAuthorizer)(nil)

func TestFormUploadCompensatesCommittedFileWhenSessionHasExpired(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 3}}
	formsAPI := &fakeVisitorForms{completeErr: forms.ErrExpired}
	operation := newFormUploadOperation(t, api, formsAPI)
	response, err := operation.Execute(context.Background(), formUploadConfig(t), formUploadRequest())
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("response is not a JSON error: %s (%v)", body, err)
	}
	if response.StatusCode != 404 || result["error"] != "not_found" {
		t.Fatalf("unexpected response: status=%d body=%s", response.StatusCode, body)
	}
	if api.deleteCalls != 1 || api.addCalls != 0 || formsAPI.abortCalls != 1 {
		t.Fatalf("unattached committed file was not compensated before indexing: deletes=%d set_adds=%d aborts=%d", api.deleteCalls, api.addCalls, formsAPI.abortCalls)
	}
}

func TestFormUploadPreservesCommittedFileOnAmbiguousFormsConflict(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 3}}
	formsAPI := &fakeVisitorForms{completeErr: forms.ErrConflict}
	operation := newFormUploadOperation(t, api, formsAPI)
	response, err := operation.Execute(context.Background(), formUploadConfig(t), formUploadRequest())
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 409 || api.deleteCalls != 0 || api.addCalls != 0 || formsAPI.abortCalls != 0 {
		t.Fatalf("ambiguous Forms conflict destroyed retryable upload state: status=%d deletes=%d set_adds=%d aborts=%d", response.StatusCode, api.deleteCalls, api.addCalls, formsAPI.abortCalls)
	}
}

func TestFormUploadRetainsCommittedFileWhenFormsFailureMayBeRetried(t *testing.T) {
	api := &fakeLoader{set: loader.FileSet{ID: fileSetID, ManagementGroup: "asset_index", Revision: 3}}
	formsAPI := &fakeVisitorForms{completeErr: forms.ErrUnavailable}
	operation := newFormUploadOperation(t, api, formsAPI)
	response, err := operation.Execute(context.Background(), formUploadConfig(t), formUploadRequest())
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 503 || api.deleteCalls != 0 || api.addCalls != 0 || formsAPI.abortCalls != 0 {
		t.Fatalf("transient Forms failure destroyed retryable state: status=%d deletes=%d set_adds=%d aborts=%d", response.StatusCode, api.deleteCalls, api.addCalls, formsAPI.abortCalls)
	}
}

func newFormUploadOperation(t *testing.T, api *fakeLoader, formsAPI *fakeVisitorForms) *publicupload.Operation {
	t.Helper()
	operation, err := publicupload.New(api, publicupload.Config{
		Credential: "server-only-secret", StagingDirectory: privateTempDir(t), MaxFileBytes: 32,
		PartSize: 4, MaxParts: 8, MaxConcurrentUploads: 2, MaxStagedBytes: 64,
		AllowedContentTypes: []string{"application/octet-stream"}, GroupAuthorization: &fakeGroupAuthorizer{}, Forms: formsAPI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func formUploadConfig(t *testing.T) json.RawMessage {
	t.Helper()
	config := `{"enabled":true,"access_group":"public_uploads","file_set_id":"` + fileSetID + `","max_bytes":16,"allowed_content_types":["application/octet-stream"],"form":{"enabled":true,"snapshot_id":"` + formSnapshotID + `","form_id":"` + formID + `","schema_version":1}}`
	if !json.Valid([]byte(config)) {
		t.Fatal("invalid form-upload fixture")
	}
	return json.RawMessage(config)
}

func formUploadRequest() router.PublicOperationRequest {
	return router.PublicOperationRequest{
		Method: "POST", Host: "forms.example", EscapedPath: "/upload", Query: "field=attachment",
		ContentType: "application/octet-stream", ContentLength: 3,
		IdempotencyKey: "visitor-form-upload-0001", Body: strings.NewReader("abc"),
		FormSessionTokens: map[string]string{formID: formToken},
	}
}
