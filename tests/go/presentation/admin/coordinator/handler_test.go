package coordinatoradmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/coordinator"
	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

type fakeJobs struct {
	page       platform.CoordinatorJobPage
	filter     coordinator.JobListFilter
	listCalls  int
	listErr    error
	retryID    uuid.UUID
	retryCalls int
	retryErr   error
}

type fakeGroupAuthorization struct {
	credential string
	group      string
	level      access.Level
	err        error
	checks     int
}

func (f *fakeGroupAuthorization) Authenticate(_ context.Context, credential string) (platform.Session, error) {
	f.credential = credential
	return platform.Session{}, f.err
}

func (f *fakeGroupAuthorization) RequireGroupLevel(_ context.Context, _ platform.Session, group string, level access.Level) error {
	f.group, f.level = group, level
	f.checks++
	return f.err
}

func TestGroupAuthorizerRechecksDirectAccessAtRequiredLevel(t *testing.T) {
	core := &fakeGroupAuthorization{}
	allow, err := NewGroupAuthorizer(core, "coordinator_jobs")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		permission Permission
		level      access.Level
	}{
		{permission: ReadJobs, level: access.LevelRead},
		{permission: RetryJobs, level: access.LevelWrite},
	} {
		if err := allow(context.Background(), "credential", test.permission); err != nil {
			t.Fatalf("authorized permission %q rejected: %v", test.permission, err)
		}
		if core.credential != "credential" || core.group != "coordinator_jobs" || core.level != test.level {
			t.Fatalf("authorization checked wrong principal/group/level: %+v want=%d", core, test.level)
		}
	}
	if core.checks != 2 {
		t.Fatalf("current direct assignments were not checked on every request: %d", core.checks)
	}
	if _, err := NewGroupAuthorizer(core, ""); !errors.Is(err, coordinator.ErrInvalid) {
		t.Fatalf("empty access group accepted: %v", err)
	}
	if _, err := NewGroupAuthorizer((*fakeGroupAuthorization)(nil), "coordinator_jobs"); !errors.Is(err, coordinator.ErrInvalid) {
		t.Fatalf("typed-nil Core capability accepted: %v", err)
	}
}

func TestPermissionsEndpointReportsRetryCapabilityWithoutWeakeningReadGate(t *testing.T) {
	jobs := &fakeJobs{}
	var seen []Permission
	handler := newTestHandler(t, jobs, func(_ context.Context, _ string, permission Permission) error {
		seen = append(seen, permission)
		if permission == RetryJobs {
			return platform.ErrForbidden
		}
		return nil
	})
	request := httptest.NewRequest(http.MethodGet, apiPrefix+"/permissions", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read-only permissions response failed: status=%d body=%s", response.Code, response.Body)
	}
	var body PermissionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.CanRetry {
		t.Fatalf("read-only principal was incorrectly granted retry: %+v err=%v", body, err)
	}
	if len(seen) != 2 || seen[0] != ReadJobs || seen[1] != RetryJobs {
		t.Fatalf("retry capability check did not authenticate/read-gate both capabilities: %v", seen)
	}
	request = httptest.NewRequest(http.MethodGet, apiPrefix+"/permissions", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("permissions endpoint leaked access without authentication: %d", response.Code)
	}
}

func (f *fakeJobs) List(_ context.Context, filter coordinator.JobListFilter) (platform.CoordinatorJobPage, error) {
	f.listCalls++
	f.filter = filter
	return f.page, f.listErr
}

func (f *fakeJobs) Retry(_ context.Context, id uuid.UUID) error {
	f.retryCalls++
	f.retryID = id
	return f.retryErr
}

func newTestHandler(t *testing.T, jobs *fakeJobs, authorize AuthorizeFunc) *Handler {
	t.Helper()
	handler, err := NewHandler(jobs, authorize, Config{DefaultPageSize: 25, MaxPageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestListRequiresAuthorizationAndReturnsSafeSummaries(t *testing.T) {
	jobID := uuid.New()
	createdAt := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	jobs := &fakeJobs{page: platform.CoordinatorJobPage{
		Items: []platform.CoordinatorJobSummary{{ID: jobID, SystemID: "private-system", Type: "archive.inspect/v1", State: "failed",
			Attempts: 2, MaxAttempts: 5, AvailableAt: createdAt, ErrorCode: "archive_invalid", CreatedAt: createdAt, UpdatedAt: createdAt}},
		Next: &platform.CoordinatorJobCursor{CreatedAt: createdAt, ID: jobID},
	}}
	var authorizedCredential string
	var authorizedPermission Permission
	handler := newTestHandler(t, jobs, func(_ context.Context, credential string, permission Permission) error {
		authorizedCredential, authorizedPermission = credential, permission
		return nil
	})
	request := httptest.NewRequest(http.MethodGet, apiPrefix+"?type=archive.inspect%2Fv1&state=failed", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("job list response headers/status are unsafe: code=%d headers=%v body=%s", response.Code, response.Header(), response.Body)
	}
	if authorizedCredential != strings.Repeat("s", 48) || authorizedPermission != ReadJobs {
		t.Fatalf("list did not require the read permission: credential=%q permission=%q", authorizedCredential, authorizedPermission)
	}
	if jobs.listCalls != 1 || jobs.filter.Type != "archive.inspect/v1" || jobs.filter.State != "failed" || jobs.filter.Limit != 25 {
		t.Fatalf("list filter is wrong: calls=%d filter=%+v", jobs.listCalls, jobs.filter)
	}
	var body ListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != jobID.String() || body.Items[0].ErrorCode != "archive_invalid" || body.NextCursor == "" {
		t.Fatalf("safe job page was not encoded: %+v", body)
	}
	if strings.Contains(response.Body.String(), "private-system") || strings.Contains(response.Body.String(), "payload") || strings.Contains(response.Body.String(), "result") {
		t.Fatalf("job list exposed internal scope or payload/result: %s", response.Body)
	}
	request = httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
	request.URL.RawQuery = url.Values{"before": []string{body.NextCursor}}.Encode()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || jobs.listCalls != 2 || jobs.filter.Before == nil || jobs.filter.Before.ID != jobID {
		t.Fatalf("opaque pagination cursor did not round trip: code=%d filter=%+v body=%s", response.Code, jobs.filter, response.Body)
	}
}

func TestListRejectsMissingAuthRepeatedParamsAndMalformedCursor(t *testing.T) {
	jobs := &fakeJobs{}
	handler := newTestHandler(t, jobs, func(context.Context, string, Permission) error { return nil })
	for _, test := range []struct {
		name   string
		url    string
		header string
		status int
	}{
		{name: "missing bearer", url: apiPrefix, status: http.StatusUnauthorized},
		{name: "repeated limit", url: apiPrefix + "?limit=1&limit=2", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "large limit", url: apiPrefix + "?limit=101", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "malformed cursor", url: apiPrefix + "?before=not-a-cursor", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "malformed bearer", url: apiPrefix, header: "Bearer  token", status: http.StatusUnauthorized},
		{name: "unknown query", url: apiPrefix + "?system_id=private", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.url, nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("got status %d, want %d: %s", response.Code, test.status, response.Body)
			}
		})
	}
	if jobs.listCalls != 0 {
		t.Fatalf("invalid request reached Coordinator job control: %d calls", jobs.listCalls)
	}
	request := httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	request.Header.Add("Authorization", "Bearer "+strings.Repeat("s", 48))
	request.Header.Add("Authorization", "Bearer "+strings.Repeat("t", 48))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || jobs.listCalls != 0 {
		t.Fatalf("ambiguous duplicate authorization headers were accepted: status=%d calls=%d", response.Code, jobs.listCalls)
	}
}

func TestAuthorizationErrorsAreSanitizedAndRetryRequiresWritePermission(t *testing.T) {
	jobs := &fakeJobs{retryErr: platform.ErrCoordinatorConflict}
	var got Permission
	handler := newTestHandler(t, jobs, func(_ context.Context, _ string, permission Permission) error {
		got = permission
		if permission == ReadJobs {
			return platform.ErrForbidden
		}
		return nil
	})
	request := httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || got != ReadJobs || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("forbidden job list was not safely denied: status=%d body=%s", response.Code, response.Body)
	}

	jobID := uuid.New()
	request = httptest.NewRequest(http.MethodPost, apiPrefix+"/"+jobID.String()+"/retry", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || got != RetryJobs || jobs.retryCalls != 1 || jobs.retryID != jobID {
		t.Fatalf("retry did not require write permission or map conflict: status=%d permission=%q jobs=%+v body=%s", response.Code, got, jobs, response.Body)
	}
}

func TestRetryRejectsBodyAndInvalidJobID(t *testing.T) {
	jobs := &fakeJobs{}
	handler := newTestHandler(t, jobs, func(context.Context, string, Permission) error { return nil })
	for _, test := range []struct {
		path string
		body string
	}{
		{path: apiPrefix + "/not-a-uuid/retry"},
		{path: apiPrefix + "/" + uuid.NewString() + "/retry", body: "{}"},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid retry request accepted: path=%q status=%d body=%s", test.path, response.Code, response.Body)
		}
	}
	if jobs.retryCalls != 0 {
		t.Fatalf("invalid retry request reached Core: %+v", jobs)
	}
}

func TestNewHandlerRejectsMissingDependenciesAndInvalidLimits(t *testing.T) {
	jobs := &fakeJobs{}
	allow := func(context.Context, string, Permission) error { return nil }
	for _, test := range []struct {
		jobs      JobControl
		authorize AuthorizeFunc
		config    Config
	}{
		{jobs: jobs, authorize: allow, config: Config{DefaultPageSize: 0, MaxPageSize: 100}},
		{jobs: jobs, authorize: allow, config: Config{DefaultPageSize: 50, MaxPageSize: 49}},
		{jobs: jobs, authorize: allow, config: Config{DefaultPageSize: 25, MaxPageSize: 201}},
		{jobs: nil, authorize: allow, config: Config{DefaultPageSize: 25, MaxPageSize: 100}},
		{jobs: jobs, authorize: nil, config: Config{DefaultPageSize: 25, MaxPageSize: 100}},
	} {
		if _, err := NewHandler(test.jobs, test.authorize, test.config); !errors.Is(err, coordinator.ErrInvalid) {
			t.Errorf("invalid job API dependencies/config accepted: %+v err=%v", test.config, err)
		}
	}
}
