package adminapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"
)

type fakeIdentityAdmin struct {
	principal    platform.Principal
	groups       map[string]access.Level
	createInput  CreatePrincipalRequest
	createdCalls int
	authCalls    int
	authErr      error
}

func (f *fakeIdentityAdmin) Authenticate(ctx context.Context, _ string) (platform.Session, error) {
	f.authCalls++
	if f.authErr != nil {
		return platform.Session{}, f.authErr
	}
	return platform.Session{}, nil
}
func (f *fakeIdentityAdmin) CreatePrincipal(_ context.Context, _ platform.Session, input CreatePrincipalRequest) (PrincipalCreated, error) {
	f.createInput = input
	f.createdCalls++
	return PrincipalCreated{ID: "01938f7a-68af-7c10-a51d-78b6ca125301", Credential: "one-time-secret"}, nil
}
func (*fakeIdentityAdmin) ListPrincipals(context.Context, platform.Session) ([]platform.Principal, error) {
	return nil, nil
}
func (*fakeIdentityAdmin) SetPrincipalManagement(context.Context, platform.Session, string, bool) error {
	return nil
}
func (*fakeIdentityAdmin) DeactivatePrincipal(context.Context, platform.Session, string) error {
	return nil
}
func (*fakeIdentityAdmin) IssueCredential(context.Context, platform.Session, string, *time.Time) (string, string, error) {
	return "cred", "secret", nil
}
func (*fakeIdentityAdmin) ListCredentials(context.Context, platform.Session, string) ([]platform.Credential, error) {
	return nil, nil
}
func (*fakeIdentityAdmin) RevokeCredential(context.Context, platform.Session, string) error {
	return nil
}
func (*fakeIdentityAdmin) SetGroupAccess(context.Context, platform.Session, string, string, access.Level) error {
	return nil
}
func (*fakeIdentityAdmin) GroupAccess(context.Context, platform.Session, string, string) (access.Level, error) {
	return 0, nil
}
func (*fakeIdentityAdmin) ListAccessGroups(context.Context, platform.Session) ([]platform.Group, error) {
	return nil, nil
}

func TestCreatePrincipalReturnsOneTimeCredentialAndExplicitGroupAccess(t *testing.T) {
	api := &fakeIdentityAdmin{groups: map[string]access.Level{"features": access.LevelWrite}}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/principals", strings.NewReader(`{"name":"Feature editor","can_manage_administrators":false,"group_access":{"features":2}}`))
	request.Header.Set("Authorization", "Bearer manager-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || api.createdCalls != 1 || api.authCalls != 1 {
		t.Fatalf("status=%d creates=%d auth=%d body=%s", response.Code, api.createdCalls, api.authCalls, response.Body.String())
	}
	if api.createInput.Name != "Feature editor" || api.createInput.GroupAccess["features"] != access.LevelWrite || api.createInput.CanManageAdministrators {
		t.Fatalf("unexpected creation input: %+v", api.createInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "one-time-secret") {
		t.Fatalf("response headers=%v body=%s", response.Header(), response.Body.String())
	}
}

func TestAdminAPIRequiresBearerAndRejectsRolesOrUnknownFields(t *testing.T) {
	for _, tc := range []struct{ authorization, body string }{
		{"", `{"name":"User"}`},
		{"Bearer manager-secret", `{"name":"User","role":"admin"}`},
		{"Bearer manager-secret", `{"name":"User","kind":"service"}`},
	} {
		api := &fakeIdentityAdmin{}
		handler, err := NewHandler(api, Config{MaxJSONBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/principals", strings.NewReader(tc.body))
		if tc.authorization != "" {
			request.Header.Set("Authorization", tc.authorization)
		}
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if api.createdCalls != 0 {
			t.Fatal("invalid request reached Core")
		}
	}
}

func TestAdminAPIMapsAuthorizationAndHidesInternalErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{platform.ErrForbidden, http.StatusForbidden}, {errors.New("postgres password leaked"), http.StatusServiceUnavailable}} {
		api := &fakeIdentityAdmin{authErr: tc.err}
		handler, err := NewHandler(api, Config{MaxJSONBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/principals", nil)
		request.Header.Set("Authorization", "Bearer manager-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status || strings.Contains(response.Body.String(), "password") || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d body=%s headers=%v", response.Code, response.Body.String(), response.Header())
		}
	}
}
