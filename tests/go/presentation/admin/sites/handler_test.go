package sitesapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	website "github.com/Zubrium/cdn/engines/website/presentation"
	adminhttpauth "github.com/Zubrium/cdn/presentation/admin/httpauth"
)

type fakeWebsite struct {
	website.API
	authenticated         bool
	snapshotSiteID        string
	snapshotCursor        string
	snapshotLimit         int
	getSnapshotID         string
	getSnapshotCalls      int
	createSnapshotCalls   int
	createSnapshotInput   website.CreateSnapshotRequest
	deleteSnapshotCalls   int
	deleteSnapshotInput   website.DeleteSnapshotRequest
	beginCalls            int
	input                 website.ImportFeatureRequest
	job                   website.FeatureImport
	versionCalls          int
	versionID             string
	versionCursor         string
	versionLimit          int
	catalogCalls          int
	catalogCursor         string
	catalogLimit          int
	buildCalls            int
	buildInput            website.BuildSnapshotRequest
	branchCalls           int
	branchInput           website.AssignBranchRequest
	wrongAssignedSnapshot bool
	eventCalls            int
	eventBranchID         string
	eventCursor           string
	eventLimit            int
	branchPage            *website.BranchPage
	eventPage             *website.BranchEventPage
}

func (f *fakeWebsite) ListSnapshots(_ context.Context, siteID, cursor string, limit int) (website.SnapshotPage, error) {
	f.snapshotSiteID, f.snapshotCursor, f.snapshotLimit = siteID, cursor, limit
	if siteID == "" {
		siteID = "33333333-3333-4333-8333-333333333333"
	}
	return website.SnapshotPage{Items: []website.Snapshot{{ID: "snapshot-1", SiteID: siteID, Name: "Homepage", Sequence: 1, Revision: 3, FeaturePins: map[string]string{}, State: "draft"}}, NextCursor: "next"}, nil
}

func (f *fakeWebsite) GetSnapshot(_ context.Context, snapshotID string) (website.Snapshot, error) {
	f.getSnapshotID, f.getSnapshotCalls = snapshotID, f.getSnapshotCalls+1
	return website.Snapshot{ID: snapshotID, SiteID: "33333333-3333-4333-8333-333333333333", Name: "Main", Sequence: 1, Revision: 4, FeaturePins: map[string]string{}, State: "draft",
		Payload: &website.SnapshotPayload{Config: json.RawMessage(`{"format_version":3}`), Metadata: json.RawMessage(`{"format_version":1}`), DesignSystem: json.RawMessage(`{"formatVersion":2}`)}}, nil
}

func (f *fakeWebsite) GetSnapshotDesignSystem(_ context.Context, snapshotID string) (website.SnapshotDesignSystem, error) {
	return website.SnapshotDesignSystem{
		Snapshot:      website.Snapshot{ID: snapshotID, SiteID: "33333333-3333-4333-8333-333333333333", Name: "Main", Sequence: 1, Revision: 4, FeaturePins: map[string]string{}, State: "draft"},
		DesignSystem:  json.RawMessage(`{"formatVersion":2,"groups":[],"baseTokens":[],"featureValues":{},"breakpoints":[],"fonts":[]}`),
		FeatureTokens: json.RawMessage(`[]`),
	}, nil
}

func (f *fakeWebsite) CreateSnapshot(_ context.Context, input website.CreateSnapshotRequest) (website.Snapshot, error) {
	f.createSnapshotInput, f.createSnapshotCalls = input, f.createSnapshotCalls+1
	payload := input.Payload
	return website.Snapshot{ID: "22222222-2222-4222-8222-222222222222", SiteID: input.SiteID, ParentID: input.ParentID, Name: input.Name, Sequence: 2, Revision: 1, Payload: &payload, FeaturePins: map[string]string{}, State: "draft"}, nil
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

func (f *fakeWebsite) ListBranches(context.Context) (website.BranchPage, error) {
	if f.branchPage != nil {
		return *f.branchPage, nil
	}
	return website.BranchPage{Items: []website.Branch{{
		ID: "11111111-1111-4111-8111-111111111111", Name: "preview", SiteID: "33333333-3333-4333-8333-333333333333", ListenAddress: "127.0.0.1:8081",
		CORSOrigins: []string{"https://preview.example.test"}, Indexable: false, Revision: 4, Enabled: true, UpdatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
	}}}, nil
}

func (f *fakeWebsite) AssignBranch(_ context.Context, input website.AssignBranchRequest) (website.Branch, error) {
	f.branchCalls++
	f.branchInput = input
	snapshotID := input.SnapshotID
	if f.wrongAssignedSnapshot {
		snapshotID = "55555555-5555-4555-8555-555555555555"
	}
	return website.Branch{ID: input.BranchID, Name: "preview", SiteID: "33333333-3333-4333-8333-333333333333", SnapshotID: &snapshotID,
		Revision: input.ExpectedRevision + 1, Enabled: true, UpdatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}, nil
}

func (f *fakeWebsite) ListBranchEvents(_ context.Context, branchID, cursor string, limit int) (website.BranchEventPage, error) {
	f.eventCalls++
	f.eventBranchID, f.eventCursor, f.eventLimit = branchID, cursor, limit
	if f.eventPage != nil {
		return *f.eventPage, nil
	}
	return website.BranchEventPage{Items: []website.BranchEvent{{
		ID: "44444444-4444-4444-8444-444444444444", BranchID: branchID, SnapshotID: "22222222-2222-4222-8222-222222222222",
		SnapshotName: "Homepage refresh", SnapshotSequence: 12, Action: "assigned", CreatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
	}}}, nil
}

func testConfig(maxJSONBytes int64) Config {
	return Config{MaxJSONBytes: maxJSONBytes, DefaultPageSize: 50, MaxPageSize: 200, AllowedOrigins: []string{"https://admin.example"}}
}

func TestCreateFeatureImportUsesAuthenticatedFileIDAndConfiguredFileSet(t *testing.T) {
	api := &fakeWebsite{job: website.FeatureImport{ID: "job-1", State: "queued", SourceFileID: "11111111-1111-4111-8111-111111111111"}}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
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
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111","target_file_set_id":"set-1"}`))
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
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
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
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/feature-imports", strings.NewReader(`{"source_file_id":"11111111-1111-4111-8111-111111111111"}`))
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
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/features/feature-a/versions?cursor=next&limit=7", nil)
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
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/features?cursor=next&limit=19", nil)
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
	request = httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/features?limit=22", nil)
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
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/builds", strings.NewReader(`{"snapshot_id":"snapshot-1"}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "build-snapshot-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || api.buildCalls != 1 || api.buildInput.SnapshotID != "snapshot-1" || api.buildInput.IdempotencyKey != "build-snapshot-request-0001" {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", response.Code, api.buildCalls, api.buildInput, response.Body.String())
	}
}

func TestListBranchesReturnsOnlyConfiguredBranchTopology(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page website.BranchPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Name != "preview" || page.Items[0].ListenAddress != "127.0.0.1:8081" {
		t.Fatalf("branch page=%s error=%v", response.Body.String(), err)
	}
}

func TestListBranchesRejectsInvalidConfiguredTopology(t *testing.T) {
	valid := website.Branch{
		ID: "11111111-1111-4111-8111-111111111111", Name: "preview", SiteID: "33333333-3333-4333-8333-333333333333",
		ListenAddress: "127.0.0.1:8081", CORSOrigins: []string{"https://preview.example.test"},
		SnapshotID: branchStringPointer("22222222-2222-4222-8222-222222222222"), Revision: 4, Enabled: true,
		UpdatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
	}
	cases := []struct {
		name   string
		mutate func(*website.Branch)
	}{
		{name: "invalid branch ID", mutate: func(branch *website.Branch) { branch.ID = "branch id" }},
		{name: "invalid site ID", mutate: func(branch *website.Branch) { branch.SiteID = "site id" }},
		{name: "invalid listener address", mutate: func(branch *website.Branch) { branch.ListenAddress = "127.0.0.1:not-a-port" }},
		{name: "invalid CORS origin", mutate: func(branch *website.Branch) { branch.CORSOrigins = []string{"https://example.test/path"} }},
		{name: "noncanonical default CORS port", mutate: func(branch *website.Branch) { branch.CORSOrigins = []string{"https://preview.example.test:443"} }},
		{name: "duplicate CORS origin", mutate: func(branch *website.Branch) {
			branch.CORSOrigins = []string{"https://preview.example.test", "https://preview.example.test"}
		}},
		{name: "invalid assigned snapshot", mutate: func(branch *website.Branch) { branch.SnapshotID = branchStringPointer("snapshot with spaces") }},
		{name: "control character in name", mutate: func(branch *website.Branch) { branch.Name = "preview\nadmin" }},
		{name: "zero update time", mutate: func(branch *website.Branch) { branch.UpdatedAt = time.Time{} }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			branch := valid
			testCase.mutate(&branch)
			api := &fakeWebsite{branchPage: &website.BranchPage{Items: []website.Branch{branch}}}
			handler, err := NewHandler(api, testConfig(1024))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches", nil)
			request.Header.Set("Authorization", "Bearer user-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusBadGateway, response.Body.String())
			}
		})
	}
}

func TestListBranchesReturnsEmptyArraysForUnconfiguredCollections(t *testing.T) {
	branchAPI := &fakeWebsite{branchPage: &website.BranchPage{}}
	handler, err := NewHandler(branchAPI, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "{\"items\":[]}\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	branchPage, err := (&fakeWebsite{}).ListBranches(context.Background())
	if err != nil || len(branchPage.Items) == 0 {
		t.Fatalf("load test branch: page=%+v error=%v", branchPage, err)
	}
	branch := branchPage.Items[0]
	branch.CORSOrigins = nil
	branchAPI = &fakeWebsite{branchPage: &website.BranchPage{Items: []website.Branch{branch}}}
	handler, err = NewHandler(branchAPI, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page website.BranchPage
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].CORSOrigins == nil {
		t.Fatalf("branch list must contain an empty CORS array: body=%s error=%v", response.Body.String(), err)
	}
}

func branchStringPointer(value string) *string {
	return &value
}

func TestListBranchesRejectsDuplicateConfiguredBranchIDs(t *testing.T) {
	branchPage, err := (&fakeWebsite{}).ListBranches(context.Background())
	if err != nil || len(branchPage.Items) == 0 {
		t.Fatalf("load test branch: page=%+v error=%v", branchPage, err)
	}
	branch := branchPage.Items[0]
	api := &fakeWebsite{branchPage: &website.BranchPage{Items: []website.Branch{branch, branch}}}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusBadGateway, response.Body.String())
	}
}

func TestAssignBranchUsesExplicitBranchAndSnapshotRevisions(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v2/admin/sites/branches/11111111-1111-4111-8111-111111111111/snapshot", strings.NewReader(`{"snapshot_id":"22222222-2222-4222-8222-222222222222","expected_revision":4,"expected_snapshot_revision":9}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.branchCalls != 1 || api.branchInput.BranchID != "11111111-1111-4111-8111-111111111111" ||
		api.branchInput.SnapshotID != "22222222-2222-4222-8222-222222222222" || api.branchInput.ExpectedRevision != 4 || api.branchInput.ExpectedSnapshotRevision != 9 {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", response.Code, api.branchCalls, api.branchInput, response.Body.String())
	}
	if response.Header().Get("ETag") != `"5"` {
		t.Fatalf("ETag=%q, want revision 5", response.Header().Get("ETag"))
	}
}

func TestAssignBranchRejectsMissingOrInvalidRevisionBeforeEngineCall(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"snapshot_id":"22222222-2222-4222-8222-222222222222","expected_snapshot_revision":9}`,
		`{"snapshot_id":"22222222-2222-4222-8222-222222222222","expected_revision":0,"expected_snapshot_revision":9}`,
		`{"snapshot_id":"22222222-2222-4222-8222-222222222222","expected_revision":4,"expected_snapshot_revision":-1}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/v2/admin/sites/branches/11111111-1111-4111-8111-111111111111/snapshot", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer user-secret")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.branchCalls != 0 {
			t.Fatalf("body=%s status=%d calls=%d", body, response.Code, api.branchCalls)
		}
	}
}

func TestCookieBranchAssignmentRequiresOriginAndCSRFBeforeEngineCall(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v2/admin/sites/branches/11111111-1111-4111-8111-111111111111/snapshot", strings.NewReader(`{"snapshot_id":"22222222-2222-4222-8222-222222222222","expected_revision":4,"expected_snapshot_revision":9}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: adminhttpauth.SessionCookieName, Value: "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || api.branchCalls != 0 || api.authenticated {
		t.Fatalf("cookie write without Origin/CSRF: status=%d calls=%d authenticated=%v", response.Code, api.branchCalls, api.authenticated)
	}
}

func TestAssignBranchFailsClosedIfEngineReturnsAnotherSnapshot(t *testing.T) {
	api := &fakeWebsite{wrongAssignedSnapshot: true}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v2/admin/sites/branches/11111111-1111-4111-8111-111111111111/snapshot", strings.NewReader(`{"snapshot_id":"22222222-2222-4222-8222-222222222222","expected_revision":4,"expected_snapshot_revision":9}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || api.branchCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, api.branchCalls, response.Body.String())
	}
}

func TestListBranchEventsUsesConfiguredPagination(t *testing.T) {
	api := &fakeWebsite{}
	config := testConfig(1024)
	config.DefaultPageSize, config.MaxPageSize = 7, 10
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches/11111111-1111-4111-8111-111111111111/events?cursor=opaque&limit=9", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.eventCalls != 1 || api.eventBranchID != "11111111-1111-4111-8111-111111111111" || api.eventCursor != "opaque" || api.eventLimit != 9 {
		t.Fatalf("status=%d calls=%d branch=%q cursor=%q limit=%d", response.Code, api.eventCalls, api.eventBranchID, api.eventCursor, api.eventLimit)
	}
}

func TestListBranchEventsRejectsMalformedEventRecords(t *testing.T) {
	branchID := "11111111-1111-4111-8111-111111111111"
	valid := website.BranchEvent{
		ID: "44444444-4444-4444-8444-444444444444", BranchID: branchID, SnapshotID: "22222222-2222-4222-8222-222222222222",
		SnapshotName: "Homepage", SnapshotSequence: 2, Action: "assigned", CreatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
	}
	cases := []struct {
		name   string
		mutate func(*website.BranchEvent)
	}{
		{name: "invalid event ID", mutate: func(event *website.BranchEvent) { event.ID = "event id" }},
		{name: "wrong branch", mutate: func(event *website.BranchEvent) { event.BranchID = "55555555-5555-4555-8555-555555555555" }},
		{name: "control character in snapshot name", mutate: func(event *website.BranchEvent) { event.SnapshotName = "Homepage\nadmin" }},
		{name: "missing timestamp", mutate: func(event *website.BranchEvent) { event.CreatedAt = time.Time{} }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event := valid
			testCase.mutate(&event)
			api := &fakeWebsite{eventPage: &website.BranchEventPage{Items: []website.BranchEvent{event}}}
			handler, err := NewHandler(api, testConfig(1024))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/branches/"+branchID+"/events", nil)
			request.Header.Set("Authorization", "Bearer user-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusBadGateway, response.Body.String())
			}
		})
	}
}

func TestCookieWriteRequiresExactOriginAndCSRF(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/snapshots", strings.NewReader(`{"site_id":"site-1","expected_revision":0}`))
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
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/features/feature-a/versions", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.versionCalls != 1 || api.versionLimit != 17 {
		t.Fatalf("default page status=%d calls=%d limit=%d body=%s", response.Code, api.versionCalls, api.versionLimit, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/features/feature-a/versions?limit=22", nil)
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
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/snapshots?site_id=11111111-1111-4111-8111-111111111111&cursor=next-page", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.snapshotSiteID != "11111111-1111-4111-8111-111111111111" || api.snapshotCursor != "next-page" || api.snapshotLimit != 17 {
		t.Fatalf("status=%d site=%q cursor=%q limit=%d body=%s", response.Code, api.snapshotSiteID, api.snapshotCursor, api.snapshotLimit, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/snapshots?site_id=not-a-uuid", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid site filter status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateSnapshotRequiresIdempotencyAndCopiesExplicitPayload(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"site_id":"33333333-3333-4333-8333-333333333333","name":"Main","expected_revision":0,"expected_content_revision":0,"payload":{"config":{"format_version":3,"pages":[],"route_tree":{"id":"root","type":"site","children":[]}},"metadata":{"format_version":1,"site_name":"Example"},"design_system":{"formatVersion":2,"groups":[],"baseTokens":[],"featureValues":{},"breakpoints":[],"fonts":[]}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/snapshots", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.createSnapshotCalls != 0 {
		t.Fatalf("missing idempotency key: status=%d calls=%d body=%s", response.Code, api.createSnapshotCalls, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/snapshots", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-snapshot-request-0001")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || api.createSnapshotCalls != 1 || api.createSnapshotInput.SiteID != "33333333-3333-4333-8333-333333333333" ||
		api.createSnapshotInput.Name != "Main" || api.createSnapshotInput.ExpectedRevision != 0 || api.createSnapshotInput.ExpectedContentRevision != 0 || api.createSnapshotInput.IdempotencyKey != "create-snapshot-request-0001" ||
		len(api.createSnapshotInput.Payload.Config) == 0 || len(api.createSnapshotInput.Payload.Metadata) == 0 || len(api.createSnapshotInput.Payload.DesignSystem) == 0 {
		t.Fatalf("status=%d calls=%d input=%+v body=%s", response.Code, api.createSnapshotCalls, api.createSnapshotInput, response.Body.String())
	}
	if response.Header().Get("Location") != "/api/v2/admin/sites/snapshots/22222222-2222-4222-8222-222222222222" {
		t.Fatalf("Location=%q", response.Header().Get("Location"))
	}
	if response.Header().Get("ETag") != `"1"` {
		t.Fatalf("ETag=%q", response.Header().Get("ETag"))
	}
}

func TestCreateChildSnapshotRequiresBothParentRevisions(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	base := `"site_id":"33333333-3333-4333-8333-333333333333","parent_id":"11111111-1111-4111-8111-111111111111","name":"Child","payload":{"config":{"format_version":3},"metadata":{"format_version":1},"design_system":{"formatVersion":2}}`
	for _, revisions := range []string{`"expected_revision":0,"expected_content_revision":8`, `"expected_revision":4,"expected_content_revision":0`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/snapshots", strings.NewReader("{"+base+","+revisions+"}"))
		request.Header.Set("Authorization", "Bearer user-secret")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "create-child-snapshot-0001")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || api.createSnapshotCalls != 0 {
			t.Fatalf("invalid revisions response=%d calls=%d body=%s", response.Code, api.createSnapshotCalls, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/snapshots", strings.NewReader("{"+base+`,"expected_revision":4,"expected_content_revision":8}`))
	request.Header.Set("Authorization", "Bearer user-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-child-snapshot-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || api.createSnapshotCalls != 1 || api.createSnapshotInput.ExpectedRevision != 4 || api.createSnapshotInput.ExpectedContentRevision != 8 {
		t.Fatalf("valid child response=%d calls=%d input=%+v body=%s", response.Code, api.createSnapshotCalls, api.createSnapshotInput, response.Body.String())
	}
}

func TestGetSnapshotReturnsRevisionAndPayload(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/snapshots/22222222-2222-4222-8222-222222222222", nil)
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

func TestGetSnapshotDesignSystemUsesWebsiteContract(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(1024))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/snapshots/22222222-2222-4222-8222-222222222222/design-system", nil)
	request.Header.Set("Authorization", "Bearer user-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"4"` || !strings.Contains(response.Body.String(), `"feature_tokens":[]`) {
		t.Fatalf("status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
}

func TestCreateSnapshotRejectsDuplicateJSONKeysAtEveryDepth(t *testing.T) {
	api := &fakeWebsite{}
	handler, err := NewHandler(api, testConfig(4096))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"site_id":"33333333-3333-4333-8333-333333333333","site_id":"44444444-4444-4444-8444-444444444444","name":"Main","expected_revision":0,"expected_content_revision":0,"payload":{"config":{"format_version":3},"metadata":{},"design_system":{}}}`,
		`{"site_id":"33333333-3333-4333-8333-333333333333","name":"Main","expected_revision":0,"expected_content_revision":0,"payload":{"config":{"format_version":2,"format_version":3},"metadata":{},"design_system":{}}}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/sites/snapshots", strings.NewReader(body))
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
	url := "/api/v2/admin/sites/snapshots/22222222-2222-4222-8222-222222222222"
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/admin/sites/unknown", nil))
	if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}
