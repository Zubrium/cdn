package formsapi

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	engine "github.com/Zubrium/cdn/engines/forms/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

type fakeFormsAPI struct {
	engine.API
	listed       bool
	request      engine.ListSubmissionsRequest
	listRequests []engine.ListSubmissionsRequest
	listPages    map[string]engine.SubmissionPage
	listGate     chan struct{}
	listStarted  chan struct{}
	listStart    sync.Once
	status       engine.SetSubmissionStatusRequest
	updated      bool
	schemaWrites int
	deletes      int
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
	f.listRequests = append(f.listRequests, request)
	if f.listGate != nil {
		f.listStart.Do(func() { close(f.listStarted) })
		<-f.listGate
	}
	if page, ok := f.listPages[request.Cursor]; ok {
		return page, nil
	}
	return engine.SubmissionPage{Items: []engine.Submission{{ID: "submission-1", FormID: request.FormID, State: "new"}}}, nil
}
func (f *fakeFormsAPI) SetSubmissionStatus(_ context.Context, request engine.SetSubmissionStatusRequest) (engine.Submission, error) {
	f.updated, f.status = true, request
	return engine.Submission{ID: request.SubmissionID, State: request.Status, Revision: request.ExpectedRevision + 1}, nil
}
func (f *fakeFormsAPI) SaveSchema(_ context.Context, request engine.SaveSchemaRequest) (engine.Schema, error) {
	f.schemaWrites++
	return engine.Schema{ID: "schema-1", Key: request.Key, Version: 1, Fields: request.Fields, Revision: 1}, nil
}
func (f *fakeFormsAPI) DeleteSubmission(_ context.Context, request engine.DeleteSubmissionRequest) error {
	f.deletes++
	if request.ExpectedRevision < 1 {
		return engine.ErrInvalid
	}
	return nil
}

func formsTestConfig() Config {
	return Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20, ExportMaxConcurrent: 2, ExportMaxRows: 100, ExportMaxBytes: 1 << 20,
		AllowedOrigins: []string{"https://admin.example"}}
}

func TestFormsHandlerMapsSubmissionsQueryToEngineContract(t *testing.T) {
	api := &fakeFormsAPI{}
	handler, err := NewHandler(api, formsTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions?snapshot_id=snapshot-1&form_id=contact&state=new&cursor=next&limit=20", nil)
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
	handler, err := NewHandler(api, formsTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"snapshot_id":"snapshot-1","status":"read","expected_revision":6}`
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/forms/submissions/submission-1/status", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || api.updated {
		t.Fatalf("missing CSRF status=%d updated=%t", response.Code, api.updated)
	}

	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request = httptest.NewRequest(http.MethodPatch, "/api/admin/forms/submissions/submission-1/status", strings.NewReader(body))
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

func TestFormsHandlerRejectsDuplicateJSONKeysBeforeMutation(t *testing.T) {
	api := &fakeFormsAPI{}
	handler, err := NewHandler(api, formsTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"snapshot_id":"snapshot-1","snapshot_id":"snapshot-2","key":"contact","fields":{},"expected_revision":0}`
	request := httptest.NewRequest(http.MethodPut, "/api/admin/forms/schemas", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.schemaWrites != 0 {
		t.Fatalf("duplicate JSON key status=%d mutations=%d body=%s", response.Code, api.schemaWrites, response.Body.String())
	}
}

func TestFormsHandlerRejectsDuplicateAndZeroRevisionIfMatch(t *testing.T) {
	for _, test := range []struct {
		name      string
		revisions []string
	}{
		{name: "duplicate header", revisions: []string{`"r4"`, `"r5"`}},
		{name: "zero revision", revisions: []string{`"r0"`}},
		{name: "leading zero", revisions: []string{`"r04"`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeFormsAPI{}
			handler, err := NewHandler(api, formsTestConfig())
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodDelete, "/api/admin/forms/submissions/submission-1?snapshot_id=snapshot-1", nil)
			request.Header.Set("Authorization", "Bearer service-key")
			for _, revision := range test.revisions {
				request.Header.Add("If-Match", revision)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || api.deletes != 0 {
				t.Fatalf("If-Match %v status=%d mutations=%d body=%s", test.revisions, response.Code, api.deletes, response.Body.String())
			}
		})
	}
}

func TestFormsHandlerExportsFilteredCSVAndNeutralizesSpreadsheetFormulas(t *testing.T) {
	api := &fakeFormsAPI{listPages: map[string]engine.SubmissionPage{
		"": {Items: []engine.Submission{{ID: "submission-1", FormID: "form-1", SchemaVersion: 2, State: "new",
			Values: map[string]any{"name": "=HYPERLINK(\"https://evil.test\")", "note": "  +SUM(1,1)", "hidden": "\x00=1+1"}}}, NextCursor: "page-2"},
		"page-2": {Items: []engine.Submission{{ID: "submission-2", FormID: "form-1", SchemaVersion: 2, State: "new",
			Values: map[string]any{"note": "safe", "extra": 3}}}},
	}}
	handler, err := NewHandler(api, formsTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1&form_id=form-1&state=new", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("export status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if response.Header().Get("Content-Disposition") == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing safe download headers: %v", response.Header())
	}
	rows, err := csv.NewReader(strings.NewReader(response.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") != "id,form_id,schema_version,state,created_at,values_json" {
		t.Fatalf("unexpected CSV rows: %#v", rows)
	}
	var exportedValues map[string]any
	if err := json.Unmarshal([]byte(rows[1][5]), &exportedValues); err != nil {
		t.Fatal(err)
	}
	if exportedValues["name"] != "'=HYPERLINK(\"https://evil.test\")" || exportedValues["note"] != "'  +SUM(1,1)" || exportedValues["hidden"] != "'\x00=1+1" {
		t.Fatalf("formula-like answer was not neutralized: %#v", exportedValues)
	}
	if len(api.listRequests) != 2 {
		t.Fatalf("expected two cursor requests, got %#v", api.listRequests)
	}
	for i, query := range api.listRequests {
		if query.SnapshotID != "snapshot-1" || query.FormID != "form-1" || query.State != "new" || query.Limit != 20 {
			t.Fatalf("page %d did not preserve selected filters: %+v", i, query)
		}
	}
}

func TestFormsHandlerExportRequiresAuthentication(t *testing.T) {
	api := &fakeFormsAPI{}
	handler, err := NewHandler(api, formsTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || api.listed || strings.Contains(response.Body.String(), "text/csv") {
		t.Fatalf("unauthenticated export status=%d listed=%t body=%q", response.Code, api.listed, response.Body.String())
	}
}

func TestFormsHandlerDoesNotReturnPartialCSVWhenExportExceedsRowLimit(t *testing.T) {
	api := &fakeFormsAPI{listPages: map[string]engine.SubmissionPage{
		"":       {Items: []engine.Submission{{ID: "submission-1", FormID: "form-1", State: "new"}}, NextCursor: "page-2"},
		"page-2": {Items: []engine.Submission{{ID: "submission-2", FormID: "form-1", State: "new"}}},
	}}
	config := formsTestConfig()
	config.ExportMaxRows = 1
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || strings.Contains(response.Body.String(), "form_id,schema_version") {
		t.Fatalf("partial CSV leaked after row limit: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestFormsHandlerDoesNotReturnPartialCSVWhenExportExceedsByteLimit(t *testing.T) {
	api := &fakeFormsAPI{listPages: map[string]engine.SubmissionPage{
		"": {Items: []engine.Submission{{ID: "submission-1", FormID: "form-1", State: "new",
			Values: map[string]any{"message": strings.Repeat("x", 2048)}}}},
	}}
	config := formsTestConfig()
	config.ExportMaxBytes = 1024
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || strings.Contains(response.Body.String(), "form_id,schema_version") {
		t.Fatalf("partial CSV leaked after byte limit: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestFormsHandlerRejectsRepeatedCursorDuringExport(t *testing.T) {
	api := &fakeFormsAPI{listPages: map[string]engine.SubmissionPage{
		"":     {Items: []engine.Submission{{ID: "submission-1", FormID: "form-1", State: "new"}}, NextCursor: "same"},
		"same": {Items: []engine.Submission{{ID: "submission-2", FormID: "form-1", State: "new"}}, NextCursor: "same"},
	}}
	handler, err := NewHandler(api, formsTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "form_id,schema_version") {
		t.Fatalf("repeated cursor should fail closed without CSV: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestFormsHandlerBoundsConcurrentExports(t *testing.T) {
	api := &fakeFormsAPI{listGate: make(chan struct{}), listStarted: make(chan struct{})}
	config := formsTestConfig()
	config.ExportMaxConcurrent = 1
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1", nil)
		request.Header.Set("Authorization", "Bearer service-key")
		handler.ServeHTTP(firstResponse, request)
		close(firstDone)
	}()
	<-api.listStarted

	secondRequest := httptest.NewRequest(http.MethodGet, "/api/admin/forms/submissions/export?snapshot_id=snapshot-1", nil)
	secondRequest.Header.Set("Authorization", "Bearer service-key")
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusTooManyRequests || secondResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("concurrent export status=%d headers=%v body=%s", secondResponse.Code, secondResponse.Header(), secondResponse.Body.String())
	}
	close(api.listGate)
	<-firstDone
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first export status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
}

func TestNewHandlerRequiresExplicitCSVExportLimits(t *testing.T) {
	config := formsTestConfig()
	config.ExportMaxBytes = 0
	if _, err := NewHandler(&fakeFormsAPI{}, config); err == nil {
		t.Fatal("missing export byte limit was accepted")
	}
	config = formsTestConfig()
	config.ExportMaxConcurrent = 0
	if _, err := NewHandler(&fakeFormsAPI{}, config); err == nil {
		t.Fatal("missing export concurrency limit was accepted")
	}
}
