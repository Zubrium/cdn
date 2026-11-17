package sitesapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	website "github.com/Zubrium/cdn/engines/website/presentation"
)

type fakeWebsite struct {
	authenticated bool
	beginCalls    int
	input         website.ImportFeatureRequest
	job           website.FeatureImport
}

func (f *fakeWebsite) Authenticate(ctx context.Context, _ string) (context.Context, error) {
	f.authenticated = true
	return context.WithValue(ctx, struct{}{}, "principal"), nil
}

func (f *fakeWebsite) BeginFeatureImport(_ context.Context, input website.ImportFeatureRequest) (website.FeatureImport, error) {
	f.beginCalls++
	f.input = input
	return f.job, nil
}

func (f *fakeWebsite) GetFeatureImport(context.Context, string) (website.FeatureImport, error) {
	return f.job, nil
}

func TestCreateFeatureImportUsesAuthenticatedFileIDAndConfiguredFileSet(t *testing.T) {
	api := &fakeWebsite{job: website.FeatureImport{ID: "job-1", State: "queued", SourceFileID: "file-1"}}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"file-1"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !api.authenticated || api.beginCalls != 1 {
		t.Fatalf("response=%d auth=%v calls=%d body=%s", response.Code, api.authenticated, api.beginCalls, response.Body.String())
	}
	if api.input.SourceFileID != "file-1" || api.input.IdempotencyKey != "feature-import-request-0001" || api.input.ExpectedSHA256 != "" {
		t.Fatalf("unexpected import input: %+v", api.input)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var body website.FeatureImport
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.ID != "job-1" {
		t.Fatalf("response body=%s error=%v", response.Body.String(), err)
	}
}

func TestCreateFeatureImportRejectsClientSuppliedFileSet(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"file-1","target_file_set_id":"set-1"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.beginCalls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, api.beginCalls, response.Body.String())
	}
}

func TestCreateFeatureImportRequiresAuthenticationBeforeCallingEngine(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"file-1"}`))
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || api.beginCalls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, api.beginCalls, response.Body.String())
	}
}

func TestCreateFeatureImportEnforcesBodyLimit(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"a-much-longer-file-id"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || api.beginCalls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, api.beginCalls, response.Body.String())
	}
}
