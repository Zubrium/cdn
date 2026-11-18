package publicforms_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	router "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/presentation/router/publicforms"
)

const (
	snapshotID = "01938f7a-68af-7c10-a51d-78b6ca125310"
	formID     = "01938f7a-68af-7c10-a51d-78b6ca125311"
	formToken  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	formRoute  = `{"enabled":true,"snapshot_id":"01938f7a-68af-7c10-a51d-78b6ca125310","form_id":"01938f7a-68af-7c10-a51d-78b6ca125311","schema_version":1}`
)

type visitorAPI struct {
	created       forms.VisitorSessionCredential
	read          forms.VisitorSession
	getErr        error
	createCalls   int
	getCalls      int
	cancelCalls   int
	submitCalls   int
	lastGet       forms.GetVisitorSessionRequest
	lastSubmit    forms.SubmitVisitorResponseRequest
	submitReceipt forms.VisitorSubmissionReceipt
}

func (*visitorAPI) ValidatePublicForm(context.Context, forms.PublicFormBinding) error { return nil }
func (*visitorAPI) ValidatePublicUploadRoute(context.Context, forms.PublicUploadRouteBinding) error {
	return nil
}
func (api *visitorAPI) CreateVisitorSession(context.Context, forms.CreateVisitorSessionRequest) (forms.VisitorSessionCredential, error) {
	api.createCalls++
	return api.created, nil
}
func (api *visitorAPI) GetVisitorSession(_ context.Context, request forms.GetVisitorSessionRequest) (forms.VisitorSession, error) {
	api.getCalls++
	api.lastGet = request
	return api.read, api.getErr
}
func (*visitorAPI) BeginVisitorUpload(context.Context, forms.BeginVisitorUploadRequest) (forms.VisitorUploadReservation, error) {
	return forms.VisitorUploadReservation{}, forms.ErrUnavailable
}
func (*visitorAPI) CompleteVisitorUpload(context.Context, forms.CompleteVisitorUploadRequest) (forms.VisitorAttachment, error) {
	return forms.VisitorAttachment{}, forms.ErrUnavailable
}
func (*visitorAPI) AbortVisitorUpload(context.Context, forms.AbortVisitorUploadRequest) error {
	return forms.ErrUnavailable
}
func (api *visitorAPI) CancelVisitorSession(context.Context, forms.CancelVisitorSessionRequest) error {
	api.cancelCalls++
	return nil
}
func (api *visitorAPI) SubmitVisitorResponse(_ context.Context, request forms.SubmitVisitorResponseRequest) (forms.VisitorSubmissionReceipt, error) {
	api.submitCalls++
	api.lastSubmit = request
	return api.submitReceipt, nil
}

func TestCreateVisitorSessionReturnsCapabilityOnlyThroughRouterCookie(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	api := &visitorAPI{created: forms.VisitorSessionCredential{
		Token:   formToken,
		Session: forms.VisitorSession{ID: snapshotID, FormID: formID, SchemaVersion: 1, Fields: map[string]any{"name": map[string]any{"type": "text", "required": true}}, ExpiresAt: expires},
	}}
	operation, err := publicforms.NewSessionOperation(api)
	if err != nil {
		t.Fatal(err)
	}
	response, err := operation.Execute(context.Background(), json.RawMessage(formRoute), router.PublicOperationRequest{
		Method: "POST", EscapedPath: "/contact/session", ContentLength: 0, Body: httpNoBody(),
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 201 || response.CacheControl != "no-store" || response.FormSessionCookie == nil ||
		response.FormSessionCookie.Token != formToken || response.FormSessionCookie.FormID != formID || response.FormSessionCookie.ExpiresAt != expires {
		t.Fatalf("unexpected create response: %+v", response)
	}
	if strings.Contains(string(body), formToken) || strings.Contains(string(body), "token") {
		t.Fatalf("capability token leaked into the response body: %s", body)
	}
	if api.createCalls != 1 {
		t.Fatalf("expected one Forms session creation, got %d", api.createCalls)
	}
}

func TestVisitorSessionReadResumeAndCancelUseCookieCapability(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	api := &visitorAPI{read: forms.VisitorSession{ID: snapshotID, FormID: formID, SchemaVersion: 1,
		Fields: map[string]any{"name": map[string]any{"type": "text", "required": true}}, ExpiresAt: expires}}
	operation, err := publicforms.NewSessionOperation(api)
	if err != nil {
		t.Fatal(err)
	}

	read, err := operation.Execute(context.Background(), json.RawMessage(formRoute), router.PublicOperationRequest{
		Method: "GET", EscapedPath: "/contact/session", ContentLength: 0, Body: httpNoBody(),
		FormSessionTokens: map[string]string{formID: formToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = read.Body.Close()
	if read.StatusCode != 200 || read.FormSessionCookie == nil || read.FormSessionCookie.Token != formToken || api.lastGet.Token != formToken {
		t.Fatalf("GET did not use and refresh the current capability: response=%+v request=%+v", read, api.lastGet)
	}

	resume, err := operation.Execute(context.Background(), json.RawMessage(formRoute), router.PublicOperationRequest{
		Method: "POST", EscapedPath: "/contact/session", ContentLength: 0, Body: httpNoBody(),
		FormSessionTokens: map[string]string{formID: formToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resume.Body.Close()
	if resume.StatusCode != 200 || resume.FormSessionCookie == nil || resume.FormSessionCookie.Token != formToken || api.createCalls != 0 {
		t.Fatalf("POST did not resume the existing session: response=%+v creates=%d", resume, api.createCalls)
	}

	cancel, err := operation.Execute(context.Background(), json.RawMessage(formRoute), router.PublicOperationRequest{
		Method: "DELETE", EscapedPath: "/contact/session", ContentLength: 0, Body: httpNoBody(),
		FormSessionTokens: map[string]string{formID: formToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancel.StatusCode != 204 || cancel.FormSessionCookie == nil || !cancel.FormSessionCookie.Clear || api.cancelCalls != 1 {
		t.Fatalf("DELETE did not cancel and clear the session capability: response=%+v cancels=%d", cancel, api.cancelCalls)
	}
}

func TestStaleSchemaSessionReturnsConflictAndClearsCookie(t *testing.T) {
	api := &visitorAPI{getErr: forms.ErrConflict}
	operation, err := publicforms.NewSessionOperation(api)
	if err != nil {
		t.Fatal(err)
	}
	response, err := operation.Execute(context.Background(), json.RawMessage(formRoute), router.PublicOperationRequest{
		Method: "POST", EscapedPath: "/contact/session", ContentLength: 0, Body: httpNoBody(),
		FormSessionTokens: map[string]string{formID: formToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 409 || response.FormSessionCookie == nil || !response.FormSessionCookie.Clear || api.createCalls != 0 {
		t.Fatalf("stale schema session was resumed or silently recreated: response=%+v creates=%d", response, api.createCalls)
	}
}

func TestSubmitKeepsSessionCookieAvailableForIdempotentRetry(t *testing.T) {
	api := &visitorAPI{submitReceipt: forms.VisitorSubmissionReceipt{ID: snapshotID, CreatedAt: time.Now().UTC()}}
	operation, err := publicforms.NewSubmitOperation(api, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		body := `{"values":{"name":"Ada"}}`
		response, err := operation.Execute(context.Background(), json.RawMessage(formRoute), router.PublicOperationRequest{
			Method: "POST", EscapedPath: "/contact/submit", ContentType: "application/json", ContentLength: int64(len(body)),
			IdempotencyKey: "submission-retry-0001", Body: strings.NewReader(body),
			FormSessionTokens: map[string]string{formID: formToken},
		})
		if err != nil {
			t.Fatalf("attempt %d returned %v", attempt+1, err)
		}
		payload, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != 201 || response.FormSessionCookie != nil || !strings.Contains(string(payload), api.submitReceipt.ID) {
			t.Fatalf("attempt %d lost the retry capability or receipt: status=%d cookie=%+v body=%s", attempt+1, response.StatusCode, response.FormSessionCookie, payload)
		}
		if api.lastSubmit.Token != formToken || api.lastSubmit.IdempotencyKey != "submission-retry-0001" {
			t.Fatalf("attempt %d did not forward the stable capability and idempotency key: %+v", attempt+1, api.lastSubmit)
		}
	}
	if api.submitCalls != 2 {
		t.Fatalf("expected both retry attempts to reach the idempotent Forms API, got %d", api.submitCalls)
	}
}

func TestFormRouteBindingRequiresCanonicalSnapshotAndFormUUIDs(t *testing.T) {
	operation, err := publicforms.NewSessionOperation(&visitorAPI{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"enabled":true,"snapshot_id":"not-a-uuid","form_id":"` + formID + `","schema_version":1}`,
		`{"enabled":true,"snapshot_id":"` + snapshotID + `","form_id":"01938F7A-68AF-7C10-A51D-78B6CA125311","schema_version":1}`,
	} {
		if err := operation.ValidateRouteConfig(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted non-canonical public form binding %s", raw)
		}
	}
}

func httpNoBody() io.Reader { return strings.NewReader("") }

var _ publicforms.VisitorAPI = (*visitorAPI)(nil)
