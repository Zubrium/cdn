package sitesapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	website "github.com/Zubrium/cdn/engines/website/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/base/admin/auth"
)

type fakeWebsite struct {
	website.API
	authenticated        bool
	snapshotSiteID       string
	snapshotPageID       string
	snapshotCursor       string
	snapshotLimit        int
	getSnapshotID        string
	getSnapshotCalls     int
	createSnapshotCalls  int
	createSnapshotInput  website.CreateSnapshotRequest
	modelSiteID          string
	modelSaveRequest     website.SaveSiteModelRequest
	modelGetCalls        int
	modelSaveCalls       int
	projectionSiteID     string
	projectionPageID     string
	projectionSnapshotID string
	projectionCalls      int
	deleteSnapshotCalls  int
	deleteSnapshotInput  website.DeleteSnapshotRequest
	beginCalls           int
	input                website.ImportFeatureRequest
	job                  website.FeatureImport
	versionCalls         int
	versionID            string
	versionCursor        string
	versionLimit         int
	catalogCalls         int
	catalogCursor        string
	catalogLimit         int
	configuredSiteCalls  int
	buildCalls           int
	buildInput           website.BuildSnapshotRequest
	mediaCalls           int
	mediaInput           website.CreateDerivativeRequest
	pageList             website.PagePage
	pageCursor           string
	pageLimit            int
	pageCalls            int
	pageInput            website.CreatePageRequest
	page                 website.Page
	templateList         website.PageTemplatePage
	templateCursor       string
	templateLimit        int
	templateCalls        int
	templateInput        website.CreatePageTemplateRequest
	templateUpdateInput  website.UpdatePageTemplateRequest
	templateDeleteInput  website.DeletePageTemplateRequest
	applyTemplateInput   website.ApplyPageTemplateRequest
	applyTemplateCalls   int
}

type fakeSnapshotForkWorkflow struct {
	request platform.WorkflowStartRequest
	calls   int
	err     error
}

func (f *fakeSnapshotForkWorkflow) Start(_ context.Context, request platform.WorkflowStartRequest) (platform.WorkflowReceipt, error) {
	f.calls++
	f.request = request
	return platform.WorkflowReceipt{State: "queued", Created: true}, f.err
}

func (f *fakeWebsite) ListConfiguredSites(context.Context) (website.ConfiguredSiteList, error) {
	f.configuredSiteCalls++
	return website.ConfiguredSiteList{Items: []website.ConfiguredSite{{SiteID: "site-live"}}}, nil
}

func (f *fakeWebsite) ListSnapshots(_ context.Context, siteID, pageID, cursor string, limit int) (website.SnapshotPage, error) {
	f.snapshotSiteID, f.snapshotPageID, f.snapshotCursor, f.snapshotLimit = siteID, pageID, cursor, limit
	if siteID == "" {
		siteID = "33333333-3333-4333-8333-333333333333"
	}
	return website.SnapshotPage{Items: []website.Snapshot{{ID: "snapshot-1", SiteID: siteID, PageID: "22222222-2222-4222-8222-222222222222", Name: "Homepage", Sequence: 1, Revision: 3, FeaturePins: map[string]string{}, State: "ready"}}, NextCursor: "next"}, nil
}

func (f *fakeWebsite) GetSiteContentSchema(_ context.Context, siteID string) (website.SiteContentSchema, error) {
	f.modelSiteID, f.modelGetCalls = siteID, f.modelGetCalls+1
	schema := website.SiteContentSchema{Version: website.ContentProjectionVersion, SiteID: siteID, ModelRevision: 2, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`)}
	schema.Digest, _ = schema.ComputeDigest()
	return schema, nil
}

func (f *fakeWebsite) SaveSiteModel(_ context.Context, request website.SaveSiteModelRequest) (website.SiteContentSchema, error) {
	f.modelSaveRequest, f.modelSaveCalls = request, f.modelSaveCalls+1
	schema := website.SiteContentSchema{Version: website.ContentProjectionVersion, SiteID: request.SiteID, ModelRevision: request.ExpectedRevision + 1, DataModel: request.DataModel}
	schema.Digest, _ = schema.ComputeDigest()
	return schema, nil
}

func (f *fakeWebsite) GetPagePlacementProjection(_ context.Context, siteID, pageID, snapshotID string) (website.PagePlacementProjection, error) {
	f.projectionSiteID, f.projectionPageID, f.projectionSnapshotID = siteID, pageID, snapshotID
	f.projectionCalls++
	projection := website.PagePlacementProjection{Version: website.ContentProjectionVersion, SiteID: siteID, PageID: pageID, PageSnapshotID: snapshotID, Revision: 3, State: "ready", Placements: []website.PagePlacement{}}
	projection.Digest, _ = projection.ComputeDigest()
	return projection, nil
}

func (f *fakeWebsite) GetSnapshot(_ context.Context, snapshotID string) (website.Snapshot, error) {
	f.getSnapshotID, f.getSnapshotCalls = snapshotID, f.getSnapshotCalls+1
	return website.Snapshot{ID: snapshotID, SiteID: "33333333-3333-4333-8333-333333333333", PageID: "22222222-2222-4222-8222-222222222222", Name: "Main", Sequence: 1, Revision: 4, FeaturePins: map[string]string{}, State: "ready",
		Payload: &website.SnapshotPayload{Config: json.RawMessage(`{"format_version":3}`), Metadata: json.RawMessage(`{"format_version":1}`)}}, nil
}

func (f *fakeWebsite) CreateSnapshot(_ context.Context, input website.CreateSnapshotRequest) (website.Snapshot, error) {
	f.createSnapshotInput, f.createSnapshotCalls = input, f.createSnapshotCalls+1
	payload := input.Payload
	state := "ready"
	if input.ParentID != "" {
		state = "provisioning"
	}
	return website.Snapshot{ID: "22222222-2222-4222-8222-222222222222", SiteID: input.SiteID, PageID: input.PageID, ParentID: input.ParentID, Name: input.Name, Sequence: 2, Revision: 1, Payload: &payload, FeaturePins: map[string]string{}, State: state}, nil
}

func (f *fakeWebsite) DeleteSnapshot(_ context.Context, input website.DeleteSnapshotRequest) error {
	f.deleteSnapshotInput, f.deleteSnapshotCalls = input, f.deleteSnapshotCalls+1
	return nil
}

func (f *fakeWebsite) Authenticate(ctx context.Context, _ string) (context.Context, error) {
	f.authenticated = true
	return context.WithValue(ctx, struct{}{}, "principal"), nil
}

func (f *fakeWebsite) AuthenticateSession(ctx context.Context, _ string) (context.Context, error) {
	f.authenticated = true
	return context.WithValue(ctx, struct{}{}, "session"), nil
}

func (f *fakeWebsite) BeginFeatureImport(_ context.Context, input website.ImportFeatureRequest) (website.FeatureImport, error) {
	f.beginCalls++
	f.input = input
	return f.job, nil
}

func (f *fakeWebsite) GetFeatureImport(context.Context, string) (website.FeatureImport, error) {
	return f.job, nil
}

func (f *fakeWebsite) ListFeatureVersions(_ context.Context, featureID, cursor string, limit int) (website.FeatureVersionPage, error) {
	f.versionCalls, f.versionID, f.versionCursor, f.versionLimit = f.versionCalls+1, featureID, cursor, limit
	return website.FeatureVersionPage{Items: []website.FeatureVersion{}}, nil
}

func (f *fakeWebsite) ListFeatureCatalog(_ context.Context, cursor string, limit int) (website.FeatureVersionPage, error) {
	f.catalogCalls, f.catalogCursor, f.catalogLimit = f.catalogCalls+1, cursor, limit
	return website.FeatureVersionPage{Items: []website.FeatureVersion{{FeatureID: "feature-a", Version: "1.2.0", Digest: strings.Repeat("a", 64), ArtifactSize: 10}}}, nil
}

func (f *fakeWebsite) BuildSnapshot(_ context.Context, input website.BuildSnapshotRequest) (website.BuildJob, error) {
	f.buildCalls, f.buildInput = f.buildCalls+1, input
	return website.BuildJob{ID: "job-1", SnapshotID: input.SnapshotID, State: "queued"}, nil
}

func (f *fakeWebsite) CreateMediaDerivative(_ context.Context, input website.CreateDerivativeRequest) (website.MediaJob, error) {
	f.mediaCalls, f.mediaInput = f.mediaCalls+1, input
	return website.MediaJob{ID: "media-job-1", SourceFileID: input.SourceFileID, Profile: input.Profile, State: "queued"}, nil
}

func (f *fakeWebsite) ListPages(_ context.Context, siteID, cursor string, limit int) (website.PagePage, error) {
	f.pageCursor, f.pageLimit, f.pageCalls = cursor, limit, f.pageCalls+1
	if f.pageList.Items == nil {
		f.pageList = website.PagePage{Items: []website.Page{{ID: "22222222-2222-4222-8222-222222222222", SiteID: siteID, Name: "Home", LatestSnapshotID: "11111111-1111-4111-8111-111111111111", LatestSnapshotState: "ready"}}}
	}
	return f.pageList, nil
}

func (f *fakeWebsite) GetPage(_ context.Context, siteID, pageID string) (website.Page, error) {
	if f.page.ID == "" {
		f.page = website.Page{ID: pageID, SiteID: siteID, Name: "Home", LatestSnapshotID: "11111111-1111-4111-8111-111111111111", LatestSnapshotState: "ready"}
	}
	return f.page, nil
}

func (f *fakeWebsite) CreatePage(_ context.Context, input website.CreatePageRequest) (website.Page, error) {
	f.pageInput, f.pageCalls = input, f.pageCalls+1
	f.page = website.Page{ID: "22222222-2222-4222-8222-222222222222", SiteID: input.SiteID, Name: input.Name, LatestSnapshotID: "11111111-1111-4111-8111-111111111111", LatestSnapshotState: "ready"}
	return f.page, nil
}

func (f *fakeWebsite) ListPageTemplates(_ context.Context, siteID, cursor string, limit int) (website.PageTemplatePage, error) {
	f.templateCursor, f.templateLimit, f.templateCalls = cursor, limit, f.templateCalls+1
	if f.templateList.Items == nil {
		f.templateList = website.PageTemplatePage{Items: []website.PageTemplate{testPageTemplate(siteID)}}
	}
	return f.templateList, nil
}

func (f *fakeWebsite) GetPageTemplate(_ context.Context, siteID, _ string) (website.PageTemplate, error) {
	return testPageTemplate(siteID), nil
}

func (f *fakeWebsite) CreatePageTemplate(_ context.Context, input website.CreatePageTemplateRequest) (website.PageTemplate, error) {
	f.templateInput, f.templateCalls = input, f.templateCalls+1
	value := testPageTemplate(input.SiteID)
	value.Name, value.Description, value.Configuration = input.Name, input.Description, input.Configuration
	return value, nil
}

func (f *fakeWebsite) UpdatePageTemplate(_ context.Context, input website.UpdatePageTemplateRequest) (website.PageTemplate, error) {
	f.templateUpdateInput, f.templateCalls = input, f.templateCalls+1
	value := testPageTemplate(input.SiteID)
	value.ID, value.Revision, value.Name, value.Description, value.Configuration = input.TemplateID, input.ExpectedRevision+1, input.Name, input.Description, input.Configuration
	return value, nil
}

func (f *fakeWebsite) DeletePageTemplate(_ context.Context, input website.DeletePageTemplateRequest) error {
	f.templateDeleteInput, f.templateCalls = input, f.templateCalls+1
	return nil
}

func (f *fakeWebsite) ApplyPageTemplate(_ context.Context, input website.ApplyPageTemplateRequest) (website.Snapshot, error) {
	f.applyTemplateInput, f.applyTemplateCalls = input, f.applyTemplateCalls+1
	return website.Snapshot{ID: "44444444-4444-4444-8444-444444444444", SiteID: input.SiteID, PageID: input.PageID, ParentID: input.ExpectedSnapshotID,
		Name: "Home", Sequence: 2, Revision: 1, FeaturePins: map[string]string{}, State: "provisioning",
		Payload: &website.SnapshotPayload{Config: json.RawMessage(`{"format_version":1}`), Metadata: json.RawMessage(`{"format_version":1}`)}}, nil
}

func testPageTemplate(siteID string) website.PageTemplate {
	return website.PageTemplate{ID: "33333333-3333-4333-8333-333333333333", SiteID: siteID, Name: "Template", Description: "Template description", Revision: 1,
		Digest: strings.Repeat("a", 64), Configuration: website.PageTemplateConfiguration{SchemaVersion: 1, FeaturePins: map[string]string{},
			Payload: website.SnapshotPayload{Config: json.RawMessage(`{"format_version":1}`), Metadata: json.RawMessage(`{"format_version":1}`)}}}
}

func testConfig(maxJSONBytes int64) Config {
	return Config{MaxJSONBytes: maxJSONBytes, DefaultPageSize: 50, MaxPageSize: 200, AllowedOrigins: []string{"https://admin.example"}}
}

func TestListConfiguredSitesReturnsStaticSiteIDs(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.configuredSiteCalls != 1 || response.Body.String() != "{\"items\":[{\"site_id\":\"site-live\"}]}\n" {
		t.Fatalf("configured sites response=%d calls=%d body=%s", response.Code, api.configuredSiteCalls, response.Body.String())
	}
}

func TestPagesAndTemplatesUseSiteScopedContractsAndCAS(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	const siteID = "33333333-3333-4333-8333-333333333333"
	pageRequest := httptest.NewRequest(http.MethodPost, "/api/admin/sites/"+siteID+"/pages", strings.NewReader(`{"name":"Landing"}`))
	pageRequest.Header.Set("Authorization", "Bearer user-secret")
	pageRequest.Header.Set("Content-Type", "application/json")
	pageRequest.Header.Set("Idempotency-Key", "page-create-request-0001")
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusCreated || api.pageInput.SiteID != siteID || api.pageInput.Name != "Landing" || api.pageInput.IdempotencyKey != "page-create-request-0001" {
		t.Fatalf("create page response=%d input=%+v body=%s", pageResponse.Code, api.pageInput, pageResponse.Body.String())
	}
	if pageResponse.Header().Get("Location") != "/api/admin/sites/"+siteID+"/pages/22222222-2222-4222-8222-222222222222" {
		t.Fatalf("page Location = %q", pageResponse.Header().Get("Location"))
	}

	config := `{"schema_version":1,"payload":{"config":{"format_version":1},"metadata":{"format_version":1}},"feature_pins":{},"placement_slots":[]}`
	templateRequest := httptest.NewRequest(http.MethodPost, "/api/admin/sites/"+siteID+"/templates", strings.NewReader(`{"name":"Landing template","description":"Reusable","configuration":`+config+`}`))
	templateRequest.Header.Set("Authorization", "Bearer user-secret")
	templateRequest.Header.Set("Content-Type", "application/json")
	templateRequest.Header.Set("Idempotency-Key", "template-create-request-01")
	templateResponse := httptest.NewRecorder()
	handler.ServeHTTP(templateResponse, templateRequest)
	if templateResponse.Code != http.StatusCreated || api.templateInput.SiteID != siteID || api.templateInput.IdempotencyKey != "template-create-request-01" {
		t.Fatalf("create template response=%d input=%+v body=%s", templateResponse.Code, api.templateInput, templateResponse.Body.String())
	}

	templateID := "33333333-3333-4333-8333-333333333333"
	updateRequest := httptest.NewRequest(http.MethodPut, "/api/admin/sites/"+siteID+"/templates/"+templateID, strings.NewReader(`{"name":"Updated","description":"","configuration":`+config+`}`))
	updateRequest.Header.Set("Authorization", "Bearer user-secret")
	updateRequest.Header.Set("Content-Type", "application/json")
	updateRequest.Header.Set("If-Match", `"4"`)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK || api.templateUpdateInput.ExpectedRevision != 4 || api.templateUpdateInput.TemplateID != templateID {
		t.Fatalf("update template response=%d input=%+v body=%s", updateResponse.Code, api.templateUpdateInput, updateResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/admin/sites/"+siteID+"/templates/"+templateID, nil)
	deleteRequest.Header.Set("Authorization", "Bearer user-secret")
	deleteRequest.Header.Set("If-Match", `"5"`)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent || api.templateDeleteInput.ExpectedRevision != 5 || api.templateDeleteInput.SiteID != siteID {
		t.Fatalf("delete template response=%d input=%+v body=%s", deleteResponse.Code, api.templateDeleteInput, deleteResponse.Body.String())
	}
}

func TestApplyPageTemplateStartsSelectionResetWorkflow(t *testing.T) {
	api := &fakeWebsite{}
	workflow := &fakeSnapshotForkWorkflow{}
	cfg := testConfig(4096)
	cfg.SnapshotForkWorkflow = workflow
	handler, err := NewHandler(api, cfg)
	if err != nil {
		t.Fatal(err)
	}
	const siteID = "33333333-3333-4333-8333-333333333333"
	const pageID = "22222222-2222-4222-8222-222222222222"
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/"+siteID+"/pages/"+pageID+"/template-applications", strings.NewReader(`{"template_id":"33333333-3333-4333-8333-333333333333","template_revision":2,"expected_snapshot_id":"11111111-1111-4111-8111-111111111111","expected_snapshot_revision":4,"expected_content_revision":7,"confirmed":true}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "template-apply-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || api.applyTemplateCalls != 1 || workflow.calls != 1 || api.applyTemplateInput.ExpectedContentRevision != 7 {
		t.Fatalf("response=%d apply=%+v workflow=%+v body=%s", response.Code, api.applyTemplateInput, workflow, response.Body.String())
	}
	var payload website.SnapshotForkWorkflowPayload
	if err := json.Unmarshal(workflow.request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if workflow.request.OperationID != "snapshot.fork.start" || payload.SelectionPolicy != "reset" || payload.TargetSnapshotID != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("unexpected template workflow: request=%+v payload=%+v", workflow.request, payload)
	}
}

func TestCreateFeatureImportUsesAuthenticatedFileIDAndConfiguredFileSet(t *testing.T) {
	api := &fakeWebsite{job: website.FeatureImport{ID: "job-1", State: "queued", SourceFileID: "11111111-1111-4111-8111-111111111111"}}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !api.authenticated || api.beginCalls != 1 {
		t.Fatalf("response=%d auth=%v calls=%d body=%s", response.Code, api.authenticated, api.beginCalls, response.Body.String())
	}
	if api.input.SourceFileID != "11111111-1111-4111-8111-111111111111" || api.input.IdempotencyKey != "feature-import-request-0001" || api.input.ExpectedSHA256 != "" {
		t.Fatalf("unexpected import input: %+v", api.input)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", response.Header().Get("X-Content-Type-Options"))
	}
	var body website.FeatureImport
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.ID != "job-1" {
		t.Fatalf("response body=%s error=%v", response.Body.String(), err)
	}
}

func TestCreateFeatureImportRejectsClientSuppliedFileSet(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111","target_file_set_id":"set-1"}`))
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
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || api.beginCalls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, api.beginCalls, response.Body.String())
	}
}

func TestCreateFeatureImportEnforcesBodyLimit(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(16))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || api.beginCalls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, api.beginCalls, response.Body.String())
	}
}

func TestFeatureVersionsValidatesQueryAndUsesEngineContract(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites/features/feature-a/versions?cursor=next&limit=7", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.versionCalls != 1 || api.versionID != "feature-a" || api.versionCursor != "next" || api.versionLimit != 7 {
		t.Fatalf("status=%d calls=%d id=%q cursor=%q limit=%d body=%s", response.Code, api.versionCalls, api.versionID, api.versionCursor, api.versionLimit, response.Body.String())
	}
}

func TestFeatureCatalogUsesConfiguredPaginationAndReturnsImmutableVersions(t *testing.T) {
	api := &fakeWebsite{}
	config := testConfig(1024)
	config.DefaultPageSize, config.MaxPageSize = 17, 21
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites/features?cursor=next&limit=19", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.catalogCalls != 1 || api.catalogCursor != "next" || api.catalogLimit != 19 {
		t.Fatalf("status=%d calls=%d cursor=%q limit=%d body=%s", response.Code, api.catalogCalls, api.catalogCursor, api.catalogLimit, response.Body.String())
	}
	var page website.FeatureVersionPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].FeatureID != "feature-a" {
		t.Fatalf("catalog response=%s error=%v", response.Body.String(), err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sites/features?limit=22", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.catalogCalls != 1 {
		t.Fatalf("out-of-range status=%d calls=%d", response.Code, api.catalogCalls)
	}
}

func TestBuildSnapshotRequiresIdempotencyAndUsesClientSnapshotOnly(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/builds", strings.NewReader(`{"snapshot_id":"snapshot-1"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "build-snapshot-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || api.buildCalls != 1 || api.buildInput.SnapshotID != "snapshot-1" || api.buildInput.IdempotencyKey != "build-snapshot-request-0001" {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", response.Code, api.buildCalls, api.buildInput, response.Body.String())
	}
}

func TestWriteRoutesRejectDuplicateIdempotencyHeaders(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		body  string
		calls func(*fakeWebsite) int
	}{
		{
			name:  "feature import",
			path:  "/api/admin/sites/feature-imports",
			body:  `{"source_file_id":"11111111-1111-4111-8111-111111111111"}`,
			calls: func(api *fakeWebsite) int { return api.beginCalls },
		},
		{
			name:  "snapshot build",
			path:  "/api/admin/sites/builds",
			body:  `{"snapshot_id":"snapshot-1"}`,
			calls: func(api *fakeWebsite) int { return api.buildCalls },
		},
		{
			name:  "media derivative",
			path:  "/api/admin/sites/media/derivatives",
			body:  `{"source_file_id":"11111111-1111-4111-8111-111111111111","feature_id":"feature-a","profile":"thumbnail"}`,
			calls: func(api *fakeWebsite) int { return api.mediaCalls },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeWebsite{}
			handler, err := NewHandler(api, testConfig(4096))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer user-secret")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Add("Idempotency-Key", "duplicate-write-request-0001")
			request.Header.Add("Idempotency-Key", "duplicate-write-request-0002")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || test.calls(api) != 0 {
				t.Fatalf("duplicate idempotency key status=%d calls=%d body=%s", response.Code, test.calls(api), response.Body.String())
			}
		})
	}
}

func TestSitesAPIRejectsDuplicateContentType(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Content-Type", "text/plain")
	request.Header.Set("Idempotency-Key", "feature-import-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.beginCalls != 0 {
		t.Fatalf("duplicate Content-Type status=%d calls=%d body=%s", response.Code, api.beginCalls, response.Body.String())
	}
}

func TestCookieWriteRequiresExactOriginAndCSRF(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader(`{"site_id":"site-1","expected_revision":0}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cookie write without Origin/CSRF: status=%d", response.Code)
	}
}

func TestFeatureVersionsUsesConfiguredPaginationBounds(t *testing.T) {
	api := &fakeWebsite{}
	config := testConfig(1024)
	config.DefaultPageSize, config.MaxPageSize = 17, 21
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites/features/feature-a/versions", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.versionCalls != 1 || api.versionLimit != 17 {
		t.Fatalf("default page status=%d calls=%d limit=%d body=%s", response.Code, api.versionCalls, api.versionLimit, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sites/features/feature-a/versions?limit=22", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.versionCalls != 1 {
		t.Fatalf("oversized page status=%d calls=%d body=%s", response.Code, api.versionCalls, response.Body.String())
	}
}

func TestListSnapshotsUsesValidatedSiteFilterAndConfiguredPagination(t *testing.T) {
	api := &fakeWebsite{}
	config := testConfig(1024)
	config.DefaultPageSize, config.MaxPageSize = 17, 21
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites/snapshots?site_id=11111111-1111-4111-8111-111111111111&page_id=22222222-2222-4222-8222-222222222222&cursor=next-page", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.snapshotSiteID != "11111111-1111-4111-8111-111111111111" || api.snapshotPageID != "22222222-2222-4222-8222-222222222222" || api.snapshotCursor != "next-page" || api.snapshotLimit != 17 {
		t.Fatalf("status=%d site=%q cursor=%q limit=%d body=%s", response.Code, api.snapshotSiteID, api.snapshotCursor, api.snapshotLimit, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sites/snapshots?site_id=not%20a%20valid%20id", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid site filter status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/admin/sites/snapshots?page_id=bad%20page", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid page filter status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateSnapshotRequiresIdempotencyAndCopiesExplicitPayload(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"site_id":"33333333-3333-4333-8333-333333333333","page_id":"22222222-2222-4222-8222-222222222222","name":"Main","expected_revision":0,"expected_content_revision":0,"payload":{"config":{"format_version":3,"page":{"placements":[]}},"metadata":{"format_version":1,"site_name":"Example"}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createSnapshotCalls != 0 {
		t.Fatalf("missing idempotency key: status=%d calls=%d body=%s", response.Code, api.createSnapshotCalls, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-snapshot-request-0001")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || api.createSnapshotCalls != 1 || api.createSnapshotInput.SiteID != "33333333-3333-4333-8333-333333333333" || api.createSnapshotInput.PageID != "22222222-2222-4222-8222-222222222222" ||
		api.createSnapshotInput.Name != "Main" || api.createSnapshotInput.ExpectedRevision != 0 || api.createSnapshotInput.ExpectedContentRevision != 0 || api.createSnapshotInput.IdempotencyKey != "create-snapshot-request-0001" ||
		len(api.createSnapshotInput.Payload.Config) == 0 || len(api.createSnapshotInput.Payload.Metadata) == 0 {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", response.Code, api.createSnapshotCalls, api.createSnapshotInput, response.Body.String())
	}
	if response.Header().Get("Location") != "/api/admin/sites/snapshots/22222222-2222-4222-8222-222222222222" {
		t.Fatalf("Location=%q", response.Header().Get("Location"))
	}
	if response.Header().Get("ETag") != `"1"` {
		t.Fatalf("ETag=%q", response.Header().Get("ETag"))
	}
}

func TestCreateChildSnapshotRequiresBothParentRevisions(t *testing.T) {
	api := &fakeWebsite{}
	workflow := &fakeSnapshotForkWorkflow{}
	config := testConfig(4096)
	config.SnapshotForkWorkflow = workflow
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	base := `"site_id":"33333333-3333-4333-8333-333333333333","page_id":"22222222-2222-4222-8222-222222222222","parent_id":"11111111-1111-4111-8111-111111111111","name":"Child","payload":{"config":{"format_version":3},"metadata":{"format_version":1}}`
	for _, revisions := range []string{`"expected_revision":0,"expected_content_revision":8`, `"expected_revision":4,"expected_content_revision":-1`} {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader("{"+base+","+revisions+"}"))
		request.Header.Set("Authorization", "Bearer user-secret")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "create-child-snapshot-0001")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.createSnapshotCalls != 0 {
			t.Fatalf("invalid revisions response=%d calls=%d body=%s", response.Code, api.createSnapshotCalls, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader("{"+base+`,"expected_revision":4,"expected_content_revision":0}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-child-snapshot-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var taskPayload map[string]any
	if err := json.Unmarshal(workflow.request.Payload, &taskPayload); err != nil {
		t.Fatal(err)
	}
	copyKey, hasCopyKey := taskPayload["content_copy_idempotency_key"].(string)
	if response.Code != http.StatusAccepted || api.createSnapshotCalls != 1 || api.createSnapshotInput.ExpectedRevision != 4 || api.createSnapshotInput.ExpectedContentRevision != 0 ||
		workflow.calls != 1 || workflow.request.OperationID != "snapshot.fork.start" || workflow.request.ContractVersion != 1 ||
		workflow.request.IdempotencyKey == "" || taskPayload["source_snapshot_id"] != "11111111-1111-4111-8111-111111111111" ||
		taskPayload["target_snapshot_id"] != "22222222-2222-4222-8222-222222222222" ||
		taskPayload["expected_source_content_revision"] != float64(0) || taskPayload["target_snapshot_revision"] != float64(1) ||
		!hasCopyKey || !strings.HasPrefix(copyKey, "content-copy:") {
		t.Fatalf("valid child response=%d calls=%d input=%+v body=%s", response.Code, api.createSnapshotCalls, api.createSnapshotInput, response.Body.String())
	}
}

func TestCreateChildSnapshotFailsClosedWithoutWorkflowBeforePersisting(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"site_id":"33333333-3333-4333-8333-333333333333","page_id":"22222222-2222-4222-8222-222222222222","parent_id":"11111111-1111-4111-8111-111111111111","name":"Child","expected_revision":4,"expected_content_revision":8,"payload":{"config":{},"metadata":{}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-child-snapshot-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || api.createSnapshotCalls != 0 {
		t.Fatalf("status=%d created=%d body=%s; expected fail-closed before persistence", response.Code, api.createSnapshotCalls, response.Body.String())
	}
}

func TestSiteModelRoutesUseTypedSiteScopedContract(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	siteID := "33333333-3333-4333-8333-333333333333"
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites/"+siteID+"/model", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.modelGetCalls != 1 || api.modelSiteID != siteID || response.Header().Get("ETag") != `"2"` {
		t.Fatalf("GET status=%d calls=%d site=%q ETag=%q body=%s", response.Code, api.modelGetCalls, api.modelSiteID, response.Header().Get("ETag"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/admin/sites/"+siteID+"/model", strings.NewReader(`{"expected_revision":2,"data_model":{"formatVersion":1,"entities":[]}}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.modelSaveCalls != 1 || api.modelSaveRequest.SiteID != siteID || api.modelSaveRequest.ExpectedRevision != 2 || string(api.modelSaveRequest.DataModel) != `{"formatVersion":1,"entities":[]}` || response.Header().Get("ETag") != `"3"` {
		t.Fatalf("PUT status=%d calls=%d input=%+v ETag=%q body=%s", response.Code, api.modelSaveCalls, api.modelSaveRequest, response.Header().Get("ETag"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/admin/sites/"+siteID+"/model", strings.NewReader(`{"site_id":"44444444-4444-4444-8444-444444444444","expected_revision":2,"data_model":{"formatVersion":1,"entities":[]}}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.modelSaveCalls != 1 {
		t.Fatalf("client-selected site ID status=%d calls=%d body=%s", response.Code, api.modelSaveCalls, response.Body.String())
	}
}

func TestPagePlacementProjectionIsReadOnlyAndBoundToPathScope(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	siteID := "33333333-3333-4333-8333-333333333333"
	snapshotID := "22222222-2222-4222-8222-222222222222"
	path := "/api/admin/sites/" + siteID + "/pages/22222222-2222-4222-8222-222222222222/snapshots/" + snapshotID + "/content-projection"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.projectionCalls != 1 || api.projectionSiteID != siteID || api.projectionPageID != "22222222-2222-4222-8222-222222222222" || api.projectionSnapshotID != snapshotID {
		t.Fatalf("GET status=%d calls=%d scope=(%q,%q,%q) body=%s", response.Code, api.projectionCalls, api.projectionSiteID, api.projectionPageID, api.projectionSnapshotID, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"data_model":{"formatVersion":1,"entities":[]},"placements":[]}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || api.projectionCalls != 1 {
		t.Fatalf("client-submitted projection status=%d calls=%d body=%s", response.Code, api.projectionCalls, response.Body.String())
	}
}

func TestGetSnapshotReturnsRevisionAndPayload(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/sites/snapshots/22222222-2222-4222-8222-222222222222", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.getSnapshotCalls != 1 || api.getSnapshotID != "22222222-2222-4222-8222-222222222222" || response.Header().Get("ETag") != `"4"` {
		t.Fatalf("status=%d calls=%d id=%q etag=%q body=%s", response.Code, api.getSnapshotCalls, api.getSnapshotID, response.Header().Get("ETag"), response.Body.String())
	}
	var snapshot website.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || snapshot.Payload == nil || len(snapshot.Payload.Config) == 0 {
		t.Fatalf("snapshot=%s error=%v", response.Body.String(), err)
	}
}

func TestCreateSnapshotRejectsDuplicateJSONKeysAtEveryDepth(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"site_id":"33333333-3333-4333-8333-333333333333","site_id":"44444444-4444-4444-8444-444444444444","name":"Main","expected_revision":0,"expected_content_revision":0,"payload":{"config":{"format_version":3},"metadata":{}}}`,
		`{"site_id":"33333333-3333-4333-8333-333333333333","page_id":"22222222-2222-4222-8222-222222222222","name":"Main","expected_revision":0,"expected_content_revision":0,"payload":{"config":{"format_version":2,"format_version":3},"metadata":{}}}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/sites/snapshots", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer user-secret")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "create-snapshot-request-0001")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.createSnapshotCalls != 0 {
			t.Fatalf("duplicate-key response=%d calls=%d body=%s", response.Code, api.createSnapshotCalls, response.Body.String())
		}
	}
}

func TestDeleteSnapshotRequiresRevisionAndPassesCAS(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	url := "/api/admin/sites/snapshots/22222222-2222-4222-8222-222222222222"
	request := httptest.NewRequest(http.MethodDelete, url, nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionRequired || api.deleteSnapshotCalls != 0 {
		t.Fatalf("missing revision: status=%d calls=%d body=%s", response.Code, api.deleteSnapshotCalls, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodDelete, url, nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("If-Match", `"4"`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || api.deleteSnapshotCalls != 1 || api.deleteSnapshotInput.SnapshotID != "22222222-2222-4222-8222-222222222222" || api.deleteSnapshotInput.ExpectedRevision != 4 {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", response.Code, api.deleteSnapshotCalls, api.deleteSnapshotInput, response.Body.String())
	}
}

func TestNewHandlerRequiresExplicitPaginationLimits(t *testing.T) {
	config := testConfig(1024)
	config.DefaultPageSize, config.MaxPageSize = 0, 0
	if _, err := NewHandler(&fakeWebsite{}, config); err == nil {
		t.Fatal("Sites API accepted missing page-size configuration")
	}
	config = testConfig(1024)
	config.DefaultPageSize, config.MaxPageSize = 51, 50
	if _, err := NewHandler(&fakeWebsite{}, config); err == nil {
		t.Fatal("Sites API accepted a maximum below its default page size")
	}
}

func TestUnknownSitesRouteStillSetsSecurityAndCacheHeaders(t *testing.T) {
	handler, err := NewHandler(&fakeWebsite{}, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/sites/unknown", nil))
	if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}
