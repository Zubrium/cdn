package contentapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	engine "github.com/Zubrium/cdn/engines/content/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

type fakeContentAPI struct {
	engine.API
	listCalled  bool
	listRequest engine.ListRecordsRequest
	saveCalled  bool
	saved       engine.SaveModelRequest
}

func (f *fakeContentAPI) Authenticate(_ context.Context, credential string) (context.Context, error) {
	if credential != "service-key" {
		return nil, engine.ErrUnauthorized
	}
	return context.WithValue(context.Background(), struct{}{}, true), nil
}
func (f *fakeContentAPI) AuthenticateSession(_ context.Context, token string) (context.Context, error) {
	if token != "ss_"+base64.RawURLEncoding.EncodeToString(make([]byte, 32)) {
		return nil, engine.ErrUnauthorized
	}
	return context.WithValue(context.Background(), struct{}{}, true), nil
}
func (f *fakeContentAPI) ListRecords(_ context.Context, request engine.ListRecordsRequest) (engine.RecordPage, error) {
	f.listCalled, f.listRequest = true, request
	return engine.RecordPage{Items: []engine.Record{{ID: "record-1", ModelID: request.ModelID}}}, nil
}
func (f *fakeContentAPI) SaveModel(_ context.Context, request engine.SaveModelRequest) (engine.Model, error) {
	f.saveCalled, f.saved = true, request
	return engine.Model{ID: "model-1", Key: request.Key, Name: request.Name, Cardinality: request.Cardinality}, nil
}

func TestContentHandlerAuthenticatesAndValidatesListQuery(t *testing.T) {
	api := &fakeContentAPI{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20, AllowedOrigins: []string{"https://admin.example"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/content/records?snapshot_id=snap-1&model_id=model-1&limit=7&cursor=next", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.listCalled {
		t.Fatalf("list status=%d called=%t body=%s", response.Code, api.listCalled, response.Body.String())
	}
	if api.listRequest.SnapshotID != "snap-1" || api.listRequest.ModelID != "model-1" || api.listRequest.Limit != 7 || api.listRequest.Cursor != "next" {
		t.Fatalf("wrong engine request: %+v", api.listRequest)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing secure response headers: %v", response.Header())
	}
}

func TestContentHandlerRequiresCookieCSRFAndExactOriginForWrites(t *testing.T) {
	api := &fakeContentAPI{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20, AllowedOrigins: []string{"https://admin.example"}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"snapshot_id":"snap-1","key":"article","name":"Article","cardinality":"many","fields":[],"expected_revision":0}`
	request := httptest.NewRequest(http.MethodPut, "/api/v2/admin/content/models", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || api.saveCalled {
		t.Fatalf("write without origin/CSRF status=%d called=%t", response.Code, api.saveCalled)
	}

	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request = httptest.NewRequest(http.MethodPut, "/api/v2/admin/content/models", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://admin.example")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	request.AddCookie(&http.Cookie{Name: adminhttpauth.CSRFCookieName, Value: csrf})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.saveCalled || api.saved.SnapshotID != "snap-1" {
		t.Fatalf("valid cookie write status=%d called=%t request=%+v body=%s", response.Code, api.saveCalled, api.saved, response.Body.String())
	}
}

func TestContentHandlerRejectsUnknownJSONFieldsAndDuplicateQueryValues(t *testing.T) {
	api := &fakeContentAPI{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20, AllowedOrigins: []string{"https://admin.example"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/content/records?snapshot_id=s1&snapshot_id=s2&model_id=m1", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.listCalled {
		t.Fatalf("duplicate query status=%d called=%t", response.Code, api.listCalled)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v2/admin/content/models", strings.NewReader(`{"snapshot_id":"s1","key":"x","name":"X","cardinality":"many","fields":[],"expected_revision":0,"ignored":true}`))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.saveCalled {
		t.Fatalf("unknown field status=%d called=%t", response.Code, api.saveCalled)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload["error"] != "invalid_request" {
		t.Fatalf("error response=%q err=%v", response.Body.String(), err)
	}
}
