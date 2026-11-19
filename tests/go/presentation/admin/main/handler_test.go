package adminapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
)

type fakeIdentityAdmin struct {
	principal    platform.Principal
	groups       map[string]platform.AccessLevel
	principals   []platform.Principal
	credentials  []platform.Credential
	accessGroups []platform.Group
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
func (f *fakeIdentityAdmin) ListPrincipals(context.Context, platform.Session) ([]platform.Principal, error) {
	return f.principals, nil
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
func (f *fakeIdentityAdmin) ListCredentials(context.Context, platform.Session, string) ([]platform.Credential, error) {
	return f.credentials, nil
}
func (*fakeIdentityAdmin) RevokeCredential(context.Context, platform.Session, string) error {
	return nil
}
func (*fakeIdentityAdmin) SetGroupAccess(context.Context, platform.Session, string, string, platform.AccessLevel) error {
	return nil
}
func (*fakeIdentityAdmin) GroupAccess(context.Context, platform.Session, string, string) (platform.AccessLevel, error) {
	return 0, nil
}
func (f *fakeIdentityAdmin) ListAccessGroups(context.Context, platform.Session) ([]platform.Group, error) {
	return f.accessGroups, nil
}

func TestCreatePrincipalReturnsOneTimeCredentialAndExplicitGroupAccess(t *testing.T) {
	api := &fakeIdentityAdmin{groups: map[string]platform.AccessLevel{"features": platform.AccessLevelWrite}}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/principals", strings.NewReader(`{"name":"Feature editor","can_manage_administrators":false,"group_access":{"features":2}}`))
	request.Header.Set("Authorization", "Bearer manager-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || api.createdCalls != 1 || api.authCalls != 1 {
		t.Fatalf("status=%d creates=%d auth=%d body=%s", response.Code, api.createdCalls, api.authCalls, response.Body.String())
	}
	if api.createInput.Name != "Feature editor" || api.createInput.GroupAccess["features"] != platform.AccessLevelWrite || api.createInput.CanManageAdministrators {
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
		request := httptest.NewRequest(http.MethodPost, "/api/admin/principals", strings.NewReader(tc.body))
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
		request := httptest.NewRequest(http.MethodGet, "/api/admin/principals", nil)
		request.Header.Set("Authorization", "Bearer manager-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status || strings.Contains(response.Body.String(), "password") || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d body=%s headers=%v", response.Code, response.Body.String(), response.Header())
		}
	}
}

func TestAdminAPIUsesLowercaseIdentityContractsWithoutRoleFields(t *testing.T) {
	api := &fakeIdentityAdmin{
		principals:   []platform.Principal{{ID: "principal-1", Name: "Editor", Active: true, CanManageAdministrators: false}},
		credentials:  []platform.Credential{{ID: "credential-1", PrincipalID: "principal-1", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}},
		accessGroups: []platform.Group{{Key: "content", Active: true, Level: platform.AccessLevelWrite}},
	}
	handler, err := NewHandler(api, Config{MaxJSONBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path      string
		wantField string
	}{
		{apiPrefix + "/principals", `"can_manage_administrators":false`},
		{apiPrefix + "/principals/01938f7a-68af-7c10-a51d-78b6ca125301/credentials", `"principal_id":"principal-1"`},
		{apiPrefix + "/access-groups", `"key":"content"`},
	} {
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		request.Header.Set("Authorization", "Bearer manager-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), tc.wantField) {
			t.Fatalf("GET %s returned %d: %s", tc.path, response.Code, response.Body.String())
		}
		for _, forbidden := range []string{`"CanManageAdministrators"`, `"PrincipalID"`, `"Key"`, `"role"`, `"kind"`, `"secret_hash"`} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("GET %s exposed non-contract field %s: %s", tc.path, forbidden, response.Body.String())
			}
		}
	}
}
