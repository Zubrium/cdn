package contentapi

import (
	"bytes"
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
	schemaErr       error
	getCalled       bool
	saveCalled      bool
	saveRequest     engine.SaveWorkspaceDraftRequest
	publishCalled   bool
	publishRequest  engine.PublishWorkspaceRequest
	historyCalled   bool
	historySnapshot string
	historyCursor   string
	historyLimit    int
	restoreCalled   bool
	restoreRequest  engine.RestoreHistoryRequest
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

func emptyWorkspace(snapshotID string) engine.Workspace {
	return engine.Workspace{SnapshotID: snapshotID, DataModel: json.RawMessage("null"), Revision: 1,
		Records: []engine.Record{}, Selections: []engine.Selection{}, Placements: []engine.Placement{}}
}

func (f *fakeContentAPI) GetWorkspace(_ context.Context, projection engine.Projections) (engine.Workspace, error) {
	f.getCalled = true
	return emptyWorkspace(projection.Page.PageSnapshotID), nil
}

func (f *fakeContentAPI) SaveWorkspaceDraft(_ context.Context, _ engine.Projections, request engine.SaveWorkspaceDraftRequest) (engine.Workspace, error) {
	f.saveCalled, f.saveRequest = true, request
	workspace := emptyWorkspace(request.SnapshotID)
	workspace.Revision = request.ExpectedRevision + 1
	return workspace, nil
}

func (f *fakeContentAPI) PublishWorkspace(_ context.Context, _ engine.Projections, request engine.PublishWorkspaceRequest) (engine.Workspace, error) {
	f.publishCalled, f.publishRequest = true, request
	workspace := emptyWorkspace(request.SnapshotID)
	workspace.Revision = request.ExpectedRevision
	workspace.PublishedRevision = request.ExpectedRevision
	return workspace, nil
}

func (f *fakeContentAPI) ListHistory(_ context.Context, projection engine.Projections, cursor string, limit int) (engine.HistoryPage, error) {
	f.historyCalled, f.historySnapshot, f.historyCursor, f.historyLimit = true, projection.Page.PageSnapshotID, cursor, limit
	return engine.HistoryPage{Items: []engine.HistoryItem{}}, nil
}

func (f *fakeContentAPI) RestoreHistory(_ context.Context, _ engine.Projections, request engine.RestoreHistoryRequest) (engine.Workspace, error) {
	f.restoreCalled, f.restoreRequest = true, request
	return emptyWorkspace(request.SnapshotID), nil
}

func (f *fakeContentAPI) ReadPublishedContent(context.Context, engine.Projections) (engine.PublishedContent, error) {
	return engine.PublishedContent{}, engine.ErrUnavailable
}

func (f *fakeContentAPI) CopySnapshotContent(context.Context, engine.CopySnapshotContentRequest) (engine.CopySnapshotContentResult, error) {
	return engine.CopySnapshotContentResult{}, engine.ErrUnavailable
}

type fakeSchemaSnapshots struct{}

func (fakeSchemaSnapshots) GetContentProjections(_ context.Context, pageSnapshotID string) (engine.Projections, error) {
	site := engine.SiteContentSchema{Version: engine.ProjectionVersion, SiteID: "site-1", ModelRevision: 1, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`)}
	site.Digest, _ = site.ComputeDigest()
	page := engine.PagePlacementProjection{Version: engine.ProjectionVersion, PageSnapshotID: pageSnapshotID, PageID: "page-1", SiteID: site.SiteID, Revision: 1, State: "ready", Placements: []engine.Placement{}}
	page.Digest, _ = page.ComputeDigest()
	return engine.Projections{Site: site, Page: page}, nil
}

func (f *fakeContentAPI) GetContentProjections(ctx context.Context, pageSnapshotID string) (engine.Projections, error) {
	if f.schemaErr != nil { return engine.Projections{}, f.schemaErr }
	return (fakeSchemaSnapshots{}).GetContentProjections(ctx, pageSnapshotID)
}

func newHandler(t *testing.T, api *fakeContentAPI) http.Handler {
	t.Helper()
	handler, err := NewHandler(api, api, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20,
		AllowedOrigins: []string{"https://admin.example"}})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestContentHandlerFailsClosedWhenTrustedSchemaProjectionIsUnavailable(t *testing.T) {
	api := &fakeContentAPI{schemaErr: engine.ErrUnavailable}
	handler := newHandler(t, api)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/content/snapshots/snap-1/workspace", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || api.getCalled {
		t.Fatalf("workspace without trusted schema projection status=%d content-called=%t body=%s", response.Code, api.getCalled, response.Body.String())
	}
}

func TestContentHandlerRequiresExplicitSchemaProvider(t *testing.T) {
	api := &fakeContentAPI{}
	if _, err := NewHandler(api, nil, Config{MaxJSONBytes: 4096, MaxPageSize: 100, DefaultPageSize: 20,
		AllowedOrigins: []string{"https://admin.example"}}); err == nil {
		t.Fatal("NewHandler() accepted missing trusted schema provider")
	}
}

func TestContentHandlerReadsWorkspaceAndSetsNoStoreHeaders(t *testing.T) {
	api := &fakeContentAPI{}
	handler := newHandler(t, api)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/content/snapshots/snap-1/workspace", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.getCalled {
		t.Fatalf("workspace status=%d called=%t body=%s", response.Code, api.getCalled, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing secure response headers: %v", response.Header())
	}
}

func TestContentHandlerRequiresCookieCSRFOriginAndIdempotencyForDraftWrite(t *testing.T) {
	api := &fakeContentAPI{}
	handler := newHandler(t, api)
	body := `{"expected_revision":0,"records":[],"selections":[]}`
	path := "/api/v2/admin/content/snapshots/snap-1/workspace"
	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || api.saveCalled {
		t.Fatalf("write without origin/CSRF status=%d called=%t", response.Code, api.saveCalled)
	}

	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://admin.example")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "content-save-1")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	request.AddCookie(&http.Cookie{Name: adminhttpauth.CSRFCookieName, Value: csrf})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.saveCalled || api.saveRequest.SnapshotID != "snap-1" ||
		api.saveRequest.ExpectedRevision != 0 || api.saveRequest.IdempotencyKey != "content-save-1" {
		t.Fatalf("valid cookie write status=%d called=%t request=%+v body=%s", response.Code, api.saveCalled, api.saveRequest, response.Body.String())
	}
}

func TestContentHandlerPaginatesHistoryAndRejectsDuplicateQueryValues(t *testing.T) {
	api := &fakeContentAPI{}
	handler := newHandler(t, api)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/content/snapshots/snap-1/history?cursor=next&limit=7", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.historyCalled || api.historySnapshot != "snap-1" || api.historyCursor != "next" || api.historyLimit != 7 {
		t.Fatalf("history status=%d called=%t snapshot=%q cursor=%q limit=%d body=%s", response.Code, api.historyCalled, api.historySnapshot, api.historyCursor, api.historyLimit, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v2/admin/content/snapshots/snap-1/history?limit=2&limit=3", nil)
	request.Header.Set("Authorization", "Bearer service-key")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate query status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestContentHandlerRejectsUnknownDuplicateAndInvalidWorkspaceJSON(t *testing.T) {
	api := &fakeContentAPI{}
	handler := newHandler(t, api)
	path := "/api/v2/admin/content/snapshots/snap-1/workspace"
	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"expected_revision":0,"records":[],"selections":[],"ignored":true}`))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "save-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.saveCalled {
		t.Fatalf("unknown field status=%d called=%t", response.Code, api.saveCalled)
	}

	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"expected_revision":0,"records":[],"records":[],"selections":[]}`))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "save-1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.saveCalled {
		t.Fatalf("duplicate key status=%d called=%t", response.Code, api.saveCalled)
	}

	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"expected_revision":0,"records":[{"entity_id":"entity","sort_order":0,"values":{}}],"selections":[]}`))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "save-1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.saveCalled {
		t.Fatalf("record without ID or client key status=%d called=%t", response.Code, api.saveCalled)
	}
}

func TestContentHandlerRejectsDuplicateContentTypeAndInvalidUTF8(t *testing.T) {
	api := &fakeContentAPI{}
	handler := newHandler(t, api)
	path := "/api/v2/admin/content/snapshots/snap-1/workspace"
	validBody := `{"expected_revision":0,"records":[],"selections":[]}`

	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(validBody))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Content-Type", "text/plain")
	request.Header.Set("Idempotency-Key", "save-duplicate-content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.saveCalled {
		t.Fatalf("duplicate Content-Type status=%d called=%t body=%s", response.Code, api.saveCalled, response.Body.String())
	}

	invalidUTF8 := []byte(`{"expected_revision":0,"records":[{"client_key":"draft-1","entity_id":"entity-1","sort_order":0,"values":{"title":"`)
	invalidUTF8 = append(invalidUTF8, 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}}],"selections":[]}`)...)
	request = httptest.NewRequest(http.MethodPut, path, bytes.NewReader(invalidUTF8))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "save-invalid-utf8")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.saveCalled {
		t.Fatalf("invalid UTF-8 status=%d called=%t body=%s", response.Code, api.saveCalled, response.Body.String())
	}
}

func TestContentHandlerRestoresHistoryWithCASAndIdempotency(t *testing.T) {
	api := &fakeContentAPI{}
	handler := newHandler(t, api)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/content/snapshots/snap-1/history/history-1/restore", strings.NewReader(`{"expected_revision":4}`))
	request.Header.Set("Authorization", "Bearer service-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "restore-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !api.restoreCalled || api.restoreRequest.SnapshotID != "snap-1" ||
		api.restoreRequest.HistoryID != "history-1" || api.restoreRequest.ExpectedRevision != 4 || api.restoreRequest.IdempotencyKey != "restore-1" {
		t.Fatalf("restore status=%d called=%t request=%+v body=%s", response.Code, api.restoreCalled, api.restoreRequest, response.Body.String())
	}
}

func TestContentWorkspaceAllowsClientKeysThatLookLikeIDsInSelections(t *testing.T) {
	request := engine.SaveWorkspaceDraftRequest{
		Records:    []engine.RecordInput{{ClientKey: "draft-1", EntityID: "entity-1", Values: map[string]any{}}},
		Selections: []engine.Selection{{PlacementID: "placement-1", InputKey: "items", Mode: "ids", RecordIDs: []string{"draft-1"}}},
	}
	if !validWorkspaceInput(request) {
		t.Fatal("request-local record key should resolve before being mistaken for a persisted record ID")
	}
}
