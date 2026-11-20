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

	platform "github.com/Zubrium/cdn/core/presentation"
	"github.com/google/uuid"
)

type fakeWorkflowControl struct {
	page          platform.WorkflowPage
	detail        platform.WorkflowDetail
	listFilter    platform.WorkflowListFilter
	detailFilter  platform.WorkflowDetailFilter
	listCalls     int
	detailCalls   int
	listErr       error
	detailErr     error
	retryWorkflow uuid.UUID
	retryTask     uuid.UUID
	retryCalls    int
	retryErr      error
}

type fakeGroupAuthorization struct {
	credential string
	group      string
	level      platform.AccessLevel
	err        error
	checks     int
}

func (f *fakeGroupAuthorization) Authenticate(_ context.Context, credential string) (platform.Session, error) {
	f.credential = credential
	return platform.Session{}, f.err
}

func (f *fakeGroupAuthorization) RequireGroupLevel(_ context.Context, _ platform.Session, group string, level platform.AccessLevel) error {
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
		level      platform.AccessLevel
	}{
		{permission: ReadWorkflows, level: platform.AccessLevelRead},
		{permission: RetryTasks, level: platform.AccessLevelWrite},
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
	if _, err := NewGroupAuthorizer(core, ""); !errors.Is(err, platform.ErrCoordinatorInputInvalid) {
		t.Fatalf("empty access group accepted: %v", err)
	}
	if _, err := NewGroupAuthorizer((*fakeGroupAuthorization)(nil), "coordinator_jobs"); !errors.Is(err, platform.ErrCoordinatorInputInvalid) {
		t.Fatalf("typed-nil Core capability accepted: %v", err)
	}
}

func TestPermissionsEndpointReportsTaskRetryCapabilityBehindReadGate(t *testing.T) {
	control := &fakeWorkflowControl{}
	var seen []Permission
	handler := newTestHandler(t, control, func(_ context.Context, _ string, permission Permission) error {
		seen = append(seen, permission)
		if permission == RetryTasks {
			return platform.ErrForbidden
		}
		return nil
	})
	request := authorizedRequest(http.MethodGet, apiPrefix+"/permissions", "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read-only permissions response failed: status=%d body=%s", response.Code, response.Body)
	}
	var body PermissionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.CanRetryTasks {
		t.Fatalf("read-only principal was incorrectly granted task retry: %+v err=%v", body, err)
	}
	if len(seen) != 2 || seen[0] != ReadWorkflows || seen[1] != RetryTasks {
		t.Fatalf("capability check did not authenticate/read-gate both permissions: %v", seen)
	}
	request = httptest.NewRequest(http.MethodGet, apiPrefix+"/permissions", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("permissions endpoint leaked access without authentication: %d", response.Code)
	}
}

func TestWorkflowAPIAcceptsPresentationCookieSessionsAndProtectsWrites(t *testing.T) {
	control := &fakeWorkflowControl{}
	var sessionPermissions []Permission
	handler, err := NewHandler(control, func(context.Context, string, Permission) error {
		return errors.New("Bearer authorizer must not receive browser session tokens")
	}, Config{
		DefaultPageSize: 25, MaxPageSize: 100, AllowedOrigins: []string{"https://admin.example"},
		SessionAuthorize: func(_ context.Context, token string, permission Permission) error {
			if token != "ss_"+strings.Repeat("A", 43) {
				return platform.ErrUnauthorized
			}
			sessionPermissions = append(sessionPermissions, permission)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	read := httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	read.AddCookie(&http.Cookie{Name: "zubrium_admin_session", Value: "ss_" + strings.Repeat("A", 43)})
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK || control.listCalls != 1 || len(sessionPermissions) != 1 || sessionPermissions[0] != ReadWorkflows {
		t.Fatalf("session read status=%d calls=%d permissions=%v body=%s", readResponse.Code, control.listCalls, sessionPermissions, readResponse.Body)
	}

	workflowID, taskID := uuid.NewString(), uuid.NewString()
	write := httptest.NewRequest(http.MethodPost, apiPrefix+"/"+workflowID+"/tasks/"+taskID+"/retry", nil)
	write.AddCookie(&http.Cookie{Name: "zubrium_admin_session", Value: "ss_" + strings.Repeat("A", 43)})
	write.Header.Set("Origin", "https://admin.example")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, write)
	if denied.Code != http.StatusForbidden || control.retryCalls != 0 {
		t.Fatalf("session write without CSRF status=%d retries=%d", denied.Code, control.retryCalls)
	}

	csrf := strings.Repeat("A", 43)
	write = httptest.NewRequest(http.MethodPost, apiPrefix+"/"+workflowID+"/tasks/"+taskID+"/retry", nil)
	write.AddCookie(&http.Cookie{Name: "zubrium_admin_session", Value: "ss_" + strings.Repeat("A", 43)})
	write.AddCookie(&http.Cookie{Name: "zubrium_admin_csrf", Value: csrf})
	write.Header.Set("Origin", "https://admin.example")
	write.Header.Set("X-CSRF-Token", csrf)
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, write)
	if accepted.Code != http.StatusNoContent || control.retryCalls != 1 || len(sessionPermissions) != 2 || sessionPermissions[1] != RetryTasks {
		t.Fatalf("valid session write failed: status=%d retries=%d permissions=%v body=%s", accepted.Code, control.retryCalls, sessionPermissions, accepted.Body)
	}
}

func (f *fakeWorkflowControl) List(_ context.Context, filter platform.WorkflowListFilter) (platform.WorkflowPage, error) {
	f.listCalls++
	f.listFilter = filter
	return f.page, f.listErr
}

func (f *fakeWorkflowControl) Get(_ context.Context, id uuid.UUID, filter platform.WorkflowDetailFilter) (platform.WorkflowDetail, error) {
	f.detailCalls++
	f.detailFilter = filter
	if f.detail.Workflow.ID != uuid.Nil && f.detail.Workflow.ID != id {
		return platform.WorkflowDetail{}, platform.ErrTaskMissing
	}
	return f.detail, f.detailErr
}

func (f *fakeWorkflowControl) RetryTask(_ context.Context, workflowID, taskID uuid.UUID) error {
	f.retryCalls++
	f.retryWorkflow, f.retryTask = workflowID, taskID
	return f.retryErr
}

func newTestHandler(t *testing.T, control *fakeWorkflowControl, authorize AuthorizeFunc) *Handler {
	t.Helper()
	handler, err := NewHandler(control, authorize, Config{DefaultPageSize: 25, MaxPageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func authorizedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 48))
	return request
}

func TestWorkflowListRequiresAuthorizationAndReturnsSafeSummaries(t *testing.T) {
	workflowID := uuid.New()
	createdAt := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	control := &fakeWorkflowControl{page: platform.WorkflowPage{
		Items: []platform.WorkflowSummary{{ID: workflowID, OwnerEngineID: "engine.archive", State: "failed", TaskCount: 4,
			CreatedAt: createdAt, UpdatedAt: createdAt}},
		Next: &platform.WorkflowCursor{CreatedAt: createdAt, ID: workflowID},
	}}
	var authorizedCredential string
	var authorizedPermission Permission
	handler := newTestHandler(t, control, func(_ context.Context, credential string, permission Permission) error {
		authorizedCredential, authorizedPermission = credential, permission
		return nil
	})
	request := authorizedRequest(http.MethodGet, apiPrefix+"?state=failed", "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("workflow list response headers/status are unsafe: code=%d headers=%v body=%s", response.Code, response.Header(), response.Body)
	}
	if authorizedCredential != strings.Repeat("s", 48) || authorizedPermission != ReadWorkflows {
		t.Fatalf("list did not require read permission: credential=%q permission=%q", authorizedCredential, authorizedPermission)
	}
	if control.listCalls != 1 || control.listFilter.State != "failed" || control.listFilter.Limit != 25 {
		t.Fatalf("list filter is wrong: calls=%d filter=%+v", control.listCalls, control.listFilter)
	}
	var body WorkflowListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != workflowID.String() || body.Items[0].TaskCount != 4 || body.NextCursor == "" {
		t.Fatalf("workflow summary page was not encoded: %+v", body)
	}
	if strings.Contains(response.Body.String(), "payload") || strings.Contains(response.Body.String(), "result") || strings.Contains(response.Body.String(), "system_id") {
		t.Fatalf("workflow list exposed payload/result/internal scope: %s", response.Body)
	}
	request = authorizedRequest(http.MethodGet, apiPrefix, "")
	request.URL.RawQuery = url.Values{"before": []string{body.NextCursor}}.Encode()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || control.listCalls != 2 || control.listFilter.Before == nil || control.listFilter.Before.ID != workflowID {
		t.Fatalf("opaque workflow cursor did not round trip: code=%d filter=%+v body=%s", response.Code, control.listFilter, response.Body)
	}
}

func TestWorkflowDetailReturnsTaskGraphWithoutPayloadOrResults(t *testing.T) {
	workflowID, rootID, childID := uuid.New(), uuid.New(), uuid.New()
	createdAt := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	control := &fakeWorkflowControl{detail: platform.WorkflowDetail{
		Workflow: platform.Workflow{ID: workflowID, OwnerEngineID: "engine.archive", State: "running", RootTaskID: rootID,
			Result: json.RawMessage(`{"secret":"never expose"}`), CreatedAt: createdAt, UpdatedAt: createdAt},
		TaskPage: platform.TaskPage{Items: []platform.TaskSummary{
			{ID: rootID, WorkflowID: workflowID, Kind: platform.TaskKindWorker,
				Worker: &platform.WorkerRef{ComponentID: "worker.archive", OperationID: "feature-import", ContractVersion: 1},
				State:  "succeeded", Attempts: 1, MaxAttempts: 3, CreatedAt: createdAt, UpdatedAt: createdAt},
			{ID: childID, WorkflowID: workflowID, Kind: platform.TaskKindJoin, ParentTaskID: rootID,
				DependsOn: []uuid.UUID{rootID}, State: "queued", CreatedAt: createdAt, UpdatedAt: createdAt},
		}, Next: &platform.TaskCursor{CreatedAt: createdAt, ID: childID}},
	}}
	handler := newTestHandler(t, control, func(context.Context, string, Permission) error { return nil })
	request := authorizedRequest(http.MethodGet, apiPrefix+"/"+workflowID.String()+"?limit=2", "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || control.detailCalls != 1 || control.detailFilter.Limit != 2 {
		t.Fatalf("workflow detail failed: status=%d detail=%+v body=%s", response.Code, control.detailFilter, response.Body)
	}
	var body WorkflowDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Workflow.ID != workflowID.String() || body.Workflow.RootTaskID != rootID.String() || len(body.Tasks.Items) != 2 ||
		body.Tasks.Items[0].Worker == nil || body.Tasks.Items[0].Worker.OperationID != "feature-import" ||
		body.Tasks.Items[1].Kind != platform.TaskKindJoin || body.Tasks.Items[1].ParentTaskID != rootID.String() ||
		len(body.Tasks.Items[1].DependsOn) != 1 || body.Tasks.Items[1].DependsOn[0] != rootID.String() || body.Tasks.NextCursor == "" {
		t.Fatalf("task graph did not preserve worker/join dependencies: %+v", body)
	}
	if strings.Contains(response.Body.String(), "never expose") || strings.Contains(response.Body.String(), "payload") || strings.Contains(response.Body.String(), "result") {
		t.Fatalf("workflow detail exposed task payload or result: %s", response.Body)
	}
}

func TestWorkflowAPIRejectsInvalidQueriesIDsAndAuthentication(t *testing.T) {
	control := &fakeWorkflowControl{}
	handler := newTestHandler(t, control, func(context.Context, string, Permission) error { return nil })
	for _, test := range []struct {
		name   string
		path   string
		header string
		status int
	}{
		{name: "missing bearer", path: apiPrefix, status: http.StatusUnauthorized},
		{name: "repeated limit", path: apiPrefix + "?limit=1&limit=2", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "large limit", path: apiPrefix + "?limit=101", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "malformed cursor", path: apiPrefix + "?before=not-a-cursor", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "unknown query", path: apiPrefix + "?worker=private", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
		{name: "invalid workflow id", path: apiPrefix + "/not-a-uuid", header: "Bearer " + strings.Repeat("s", 48), status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
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
	if control.listCalls != 0 || control.detailCalls != 0 {
		t.Fatalf("invalid request reached workflow control: list=%d detail=%d", control.listCalls, control.detailCalls)
	}
	request := httptest.NewRequest(http.MethodGet, apiPrefix, nil)
	request.Header.Add("Authorization", "Bearer "+strings.Repeat("s", 48))
	request.Header.Add("Authorization", "Bearer "+strings.Repeat("t", 48))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || control.listCalls != 0 {
		t.Fatalf("ambiguous duplicate authorization headers were not rejected: status=%d calls=%d", response.Code, control.listCalls)
	}
}

func TestWorkflowTaskRetryRequiresWritePermissionAndMapsErrors(t *testing.T) {
	workflowID, taskID := uuid.New(), uuid.New()
	control := &fakeWorkflowControl{retryErr: platform.ErrCoordinatorConflict}
	var got Permission
	handler := newTestHandler(t, control, func(_ context.Context, _ string, permission Permission) error {
		got = permission
		if permission == ReadWorkflows {
			return platform.ErrForbidden
		}
		return nil
	})
	request := authorizedRequest(http.MethodGet, apiPrefix, "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || got != ReadWorkflows {
		t.Fatalf("forbidden workflow list was not denied: status=%d body=%s", response.Code, response.Body)
	}
	request = authorizedRequest(http.MethodPost, apiPrefix+"/"+workflowID.String()+"/tasks/"+taskID.String()+"/retry", "")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || got != RetryTasks || control.retryCalls != 1 || control.retryWorkflow != workflowID || control.retryTask != taskID {
		t.Fatalf("task retry did not require write permission or map conflict: status=%d permission=%q control=%+v body=%s", response.Code, got, control, response.Body)
	}
}

func TestTaskRetryRejectsBodyAndInvalidIDsAndNewHandlerRejectsInvalidConfig(t *testing.T) {
	control := &fakeWorkflowControl{}
	handler := newTestHandler(t, control, func(context.Context, string, Permission) error { return nil })
	for _, test := range []struct{ path, body string }{
		{path: apiPrefix + "/not-a-uuid/tasks/" + uuid.NewString() + "/retry"},
		{path: apiPrefix + "/" + uuid.NewString() + "/tasks/not-a-uuid/retry"},
		{path: apiPrefix + "/" + uuid.NewString() + "/tasks/" + uuid.NewString() + "/retry", body: "{}"},
	} {
		request := authorizedRequest(http.MethodPost, test.path, test.body)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid task retry accepted: path=%q status=%d body=%s", test.path, response.Code, response.Body)
		}
	}
	if control.retryCalls != 0 {
		t.Fatalf("invalid retry request reached Core: %+v", control)
	}
	if _, err := NewHandler(control, func(context.Context, string, Permission) error { return nil }, Config{DefaultPageSize: 0, MaxPageSize: 100}); !errors.Is(err, platform.ErrCoordinatorInputInvalid) {
		t.Fatalf("invalid page limit accepted: %v", err)
	}
}
