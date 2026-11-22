package publicforms_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	forms "github.com/Zubrium/cdn/engines/forms/presentation"
	router "github.com/Zubrium/cdn/engines/router/presentation"
	"github.com/Zubrium/cdn/presentation/router/publicforms"
)

const testFormID = "5b4ce75c-c2f5-47e5-95a4-6422bc1986b0"

type stubVisitorAPI struct {
	validateFormErr error
	submitErr       error
	submitRequests  int
}

func (api *stubVisitorAPI) ValidatePublicForm(context.Context, forms.PublicFormBinding) error {
	return api.validateFormErr
}

func (api *stubVisitorAPI) ValidatePublicUploadRoute(context.Context, forms.PublicUploadRouteBinding) error {
	return nil
}

func (api *stubVisitorAPI) CreateVisitorSession(context.Context, forms.CreateVisitorSessionRequest) (forms.VisitorSessionCredential, error) {
	return forms.VisitorSessionCredential{}, nil
}

func (api *stubVisitorAPI) GetVisitorSession(context.Context, forms.GetVisitorSessionRequest) (forms.VisitorSession, error) {
	return forms.VisitorSession{}, nil
}

func (api *stubVisitorAPI) BeginVisitorUpload(context.Context, forms.BeginVisitorUploadRequest) (forms.VisitorUploadReservation, error) {
	return forms.VisitorUploadReservation{}, nil
}

func (api *stubVisitorAPI) CompleteVisitorUpload(context.Context, forms.CompleteVisitorUploadRequest) (forms.VisitorAttachment, error) {
	return forms.VisitorAttachment{}, nil
}

func (api *stubVisitorAPI) AbortVisitorUpload(context.Context, forms.AbortVisitorUploadRequest) error {
	return nil
}

func (api *stubVisitorAPI) CancelVisitorSession(context.Context, forms.CancelVisitorSessionRequest) error {
	return nil
}

func (api *stubVisitorAPI) SubmitVisitorResponse(context.Context, forms.SubmitVisitorResponseRequest) (forms.VisitorSubmissionReceipt, error) {
	api.submitRequests++
	if api.submitErr != nil {
		return forms.VisitorSubmissionReceipt{}, api.submitErr
	}
	return forms.VisitorSubmissionReceipt{ID: "receipt-1", CreatedAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)}, nil
}

func routeConfig(t *testing.T, extra string) json.RawMessage {
	t.Helper()
	raw := `{"enabled":true,"form_id":"` + testFormID + `","schema_version":2` + extra + `}`
	return json.RawMessage(raw)
}

func submitRequest(body string, key string, token string) router.PublicOperationRequest {
	return router.PublicOperationRequest{
		Method:            "POST",
		EscapedPath:       "/forms/contact/submit",
		IdempotencyKey:    key,
		Body:              strings.NewReader(body),
		ContentLength:     int64(len(body)),
		FormSessionTokens: map[string]string{testFormID: token},
	}
}

func TestPublicFormOperationsRejectSnapshotScopedRouteConfig(t *testing.T) {
	api := &stubVisitorAPI{}
	session, err := publicforms.NewSessionOperation(api)
	if err != nil {
		t.Fatal(err)
	}
	submit, err := publicforms.NewSubmitOperation(api, 1024)
	if err != nil {
		t.Fatal(err)
	}
	stale := routeConfig(t, `,"snapshot_id":"11111111-1111-4111-8111-111111111111"`)
	if err := session.ValidateRouteConfig(stale); err == nil {
		t.Error("session operation accepted snapshot_id in route config")
	}
	if err := submit.ValidateRouteConfig(stale); err == nil {
		t.Error("submit operation accepted snapshot_id in route config")
	}
	if err := session.ValidateRouteConfig(routeConfig(t, "")); err != nil {
		t.Fatalf("valid session route config rejected: %v", err)
	}
	if err := submit.ValidateRouteConfig(routeConfig(t, "")); err != nil {
		t.Fatalf("valid submit route config rejected: %v", err)
	}
}

func TestSubmitOperationRejectsRequestsWithoutCapabilityOrIdempotency(t *testing.T) {
	api := &stubVisitorAPI{}
	operation, err := publicforms.NewSubmitOperation(api, 1024)
	if err != nil {
		t.Fatal(err)
	}
	config := routeConfig(t, "")
	body := `{"values":{"message":"hi"}}`

	response, err := operation.Execute(context.Background(), config, submitRequest(body, "qa-battery-valid-key-1", ""))
	if err != nil {
		t.Fatalf("missing token returned transport error: %v", err)
	}
	if response.StatusCode != 404 || response.CacheControl != "no-store" {
		t.Fatalf("missing token response = status %d, cache %q", response.StatusCode, response.CacheControl)
	}
	if api.submitRequests != 0 {
		t.Fatal("Forms was called without a session capability")
	}

	if _, err := operation.Execute(context.Background(), config, submitRequest(body, "", "capability-token")); !errors.Is(err, router.ErrInvalid) {
		t.Fatalf("missing idempotency key error = %v, want invalid", err)
	}
	request := submitRequest(body, "qa-battery-valid-key-1", "capability-token")
	request.Query = "field=attachment"
	if _, err := operation.Execute(context.Background(), config, request); !errors.Is(err, router.ErrInvalid) {
		t.Fatalf("query string error = %v, want invalid", err)
	}
}

func TestSubmitOperationMapsFormsErrorsAndCookiePolicy(t *testing.T) {
	config := routeConfig(t, "")
	body := `{"values":{"message":"hi"}}`

	t.Run("accepted", func(t *testing.T) {
		operation, err := publicforms.NewSubmitOperation(&stubVisitorAPI{}, 1024)
		if err != nil {
			t.Fatal(err)
		}
		response, err := operation.Execute(context.Background(), config, submitRequest(body, "qa-battery-valid-key-2", "capability-token"))
		if err != nil || response.StatusCode != 201 || response.FormSessionCookie != nil {
			t.Fatalf("accepted response = %#v, %v", response, err)
		}
	})
	t.Run("conflict keeps cookie", func(t *testing.T) {
		operation, err := publicforms.NewSubmitOperation(&stubVisitorAPI{submitErr: forms.ErrConflict}, 1024)
		if err != nil {
			t.Fatal(err)
		}
		response, err := operation.Execute(context.Background(), config, submitRequest(body, "qa-battery-valid-key-3", "capability-token"))
		if err != nil || response.StatusCode != 409 || response.FormSessionCookie != nil {
			t.Fatalf("conflict response = %#v, %v", response, err)
		}
	})
	t.Run("expired clears cookie", func(t *testing.T) {
		operation, err := publicforms.NewSubmitOperation(&stubVisitorAPI{submitErr: forms.ErrExpired}, 1024)
		if err != nil {
			t.Fatal(err)
		}
		response, err := operation.Execute(context.Background(), config, submitRequest(body, "qa-battery-valid-key-4", "capability-token"))
		if err != nil || response.StatusCode != 404 {
			t.Fatalf("expired response = %#v, %v", response, err)
		}
		if response.FormSessionCookie == nil || !response.FormSessionCookie.Clear || response.FormSessionCookie.FormID != testFormID {
			t.Fatalf("expired response cookie = %#v", response.FormSessionCookie)
		}
	})
}
