package formsapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	engine "github.com/Zubrium/cdn/engines/forms/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

type fakeFormsAPI struct {
	engine.API
	listed  bool
	request engine.ListSubmissionsRequest
	status  engine.SetSubmissionStatusRequest
	updated bool
}

func (f *fakeFormsAPI) Authenticate(_ context.Context, credential string) (context.Context, error) {
	if credential != "service-key" {
		return nil, engine.ErrUnauthorized
	}
	return context.Background(), nil
}
func (f *fakeFormsAPI) AuthenticateSession(_ context.Context, token string) (context.Context, error) {
	if token != "ss_"+base64.RawURLEncoding.EncodeToString(make([]byte, 32)) {
		return nil, engine.ErrUnauthorized
	}
	return context.Background(), nil
}
func (f *fakeFormsAPI) ListSubmissions(_ context.Context, request engine.ListSubmissionsRequest) (engine.SubmissionPage, error) {
	f.listed, f.request = true, request
	return engine.SubmissionPage{Items: []engine.Submission{{ID: "submission-1", FormID: request.FormID, State: "new"}}}, nil
}
func (f *fakeFormsAPI) SetSubmissionStatus(_ context.Context, request engine.SetSubmissionStatusRequest) (engine.Submission, error) {
	f.updated, f.status = true, request
	return engine.Submission{ID: request.SubmissionID, State: request.Status, Revision: request.ExpectedRevision + 1}, nil
}

func TestFormsHandlerMapsSubmissionsQueryToEngineContract(t *testing.T) {
	api := &fakeFormsAPI{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20, AllowedOrigins: []string{"https://admin.example"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/forms/submissions?snapshot_id=snapshot-1&form_id=contact&state=new&cursor=next&limit=20", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.listed {
		t.Fatalf("list status=%d called=%t body=%s", response.Code, api.listed, response.Body.String())
	}
	if api.request.SnapshotID != "snapshot-1" || api.request.FormID != "contact" || api.request.State != "new" || api.request.Cursor != "next" || api.request.Limit != 20 {
		t.Fatalf("wrong engine request: %+v", api.request)
	}
}

func TestFormsHandlerRequiresCookieCSRFForStatusAndPassesExpectedRevision(t *testing.T) {
	api := &fakeFormsAPI{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20, AllowedOrigins: []string{"https://admin.example"}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"snapshot_id":"snapshot-1","status":"read","expected_revision":6}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v2/admin/forms/submissions/submission-1/status", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || api.updated {
		t.Fatalf("missing CSRF status=%d updated=%t", response.Code, api.updated)
	}

	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request = httptest.NewRequest(http.MethodPatch, "/api/v2/admin/forms/submissions/submission-1/status", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://admin.example")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	request.AddCookie(&http.Cookie{Name: adminhttpauth.CSRFCookieName, Value: csrf})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.updated || api.status.SubmissionID != "submission-1" || api.status.ExpectedRevision != 6 {
		t.Fatalf("status update status=%d updated=%t request=%+v body=%s", response.Code, api.updated, api.status, response.Body.String())
	}
}
