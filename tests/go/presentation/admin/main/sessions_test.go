package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
)

const testAdminOrigin = "https://admin.example.test"

type sessionStub struct {
	loginCalls    int
	loginSecret   string
	login         platform.CreatedSession
	resolveCalls  int
	resolvedToken string
	logoutCalls   int
	logoutToken   string
}

type sessionGroupAccessStub struct {
	groups map[string]platform.AccessLevel
	err    error
	calls  int
	token  string
}

func (s *sessionGroupAccessStub) ResolveGroupAccess(_ context.Context, token string) (map[string]platform.AccessLevel, error) {
	s.calls++
	s.token = token
	return s.groups, s.err
}

func (s *sessionStub) CreateSession(_ context.Context, credential string) (platform.CreatedSession, error) {
	s.loginCalls++
	s.loginSecret = credential
	return s.login, nil
}

func (s *sessionStub) AuthenticateSession(_ context.Context, token string) (platform.Session, platform.Principal, error) {
	s.resolveCalls++
	s.resolvedToken = token
	return platform.Session{}, s.login.Principal, nil
}

func (s *sessionStub) RevokeSession(_ context.Context, token string) error {
	s.logoutCalls++
	s.logoutToken = token
	return nil
}

func testSessionLogin() platform.CreatedSession {
	now := time.Now().UTC()
	return platform.CreatedSession{
		Token:             "ss_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Principal:         platform.Principal{ID: "principal-1", Name: "Operator", Active: true},
		IdleExpiresAt:     now.Add(8 * time.Hour),
		AbsoluteExpiresAt: now.Add(7 * 24 * time.Hour),
	}
}

func TestSessionLoginRejectsUntrustedOriginBeforeCreatingCoreSession(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, apiPrefix+"/session", strings.NewReader(`{"credential":"credential-secret-value"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusForbidden {
		t.Fatalf("login with an untrusted Origin returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if sessions.loginCalls != 0 {
		t.Fatalf("Core session creation ran %d times for an untrusted Origin", sessions.loginCalls)
	}
}

func TestSessionLoginSetsScopedHttpOnlySessionAndReadableCSRFCookies(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, apiPrefix+"/session", strings.NewReader(`{"credential":"credential-secret-value"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testAdminOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusCreated {
		t.Fatalf("login returned %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if sessions.loginCalls != 1 || sessions.loginSecret != "credential-secret-value" {
		t.Fatalf("Core received login calls=%d credential=%q", sessions.loginCalls, sessions.loginSecret)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case adminSessionCookieName:
			sessionCookie = cookie
		case adminCSRFCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.Path != "/api/v2" || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie has unsafe attributes: %#v", sessionCookie)
	}
	if csrfCookie == nil || csrfCookie.HttpOnly || !csrfCookie.Secure || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("CSRF cookie has unsafe attributes: %#v", csrfCookie)
	}
	if sessionCookie.Value != sessions.login.Token || csrfCookie.Value == "" {
		t.Fatalf("response cookies do not carry the Core session and Presentation CSRF tokens")
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(body)
	for _, secret := range []string{sessions.login.Token, csrfCookie.Value, "credential-secret-value"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("login response body leaked a credential or cookie token")
		}
	}
	if body["principal"] == nil {
		t.Fatalf("login response omitted the authenticated principal: %s", response.Body.String())
	}
}

func TestSessionLoginRejectsAmbiguousRequestHeadersAndJSON(t *testing.T) {
	newHandler := func(t *testing.T, sessions *sessionStub) http.Handler {
		t.Helper()
		handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
			MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin},
		})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	tests := []struct {
		name  string
		body  []byte
		setup func(*http.Request)
	}{
		{
			name: "duplicate content type",
			body: []byte(`{"credential":"credential-secret-value"}`),
			setup: func(request *http.Request) {
				request.Header.Add("Content-Type", "application/json")
				request.Header.Add("Content-Type", "text/plain")
			},
		},
		{
			name:  "duplicate JSON key",
			body:  []byte(`{"credential":"first-secret-value","credential":"second-secret-value"}`),
			setup: func(request *http.Request) { request.Header.Set("Content-Type", "application/json") },
		},
		{
			name:  "invalid UTF-8",
			body:  append(append([]byte(`{"credential":"`), 0xff), []byte(`"}`)...),
			setup: func(request *http.Request) { request.Header.Set("Content-Type", "application/json") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := &sessionStub{login: testSessionLogin()}
			request := httptest.NewRequest(http.MethodPost, apiPrefix+"/session", bytes.NewReader(test.body))
			test.setup(request)
			request.Header.Set("Origin", testAdminOrigin)
			response := httptest.NewRecorder()
			newHandler(t, sessions).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || sessions.loginCalls != 0 {
				t.Fatalf("ambiguous login request status=%d calls=%d body=%s", response.Code, sessions.loginCalls, response.Body.String())
			}
		})
	}
}

func TestSessionLoginRevokesMalformedSessionReturnedByCore(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	sessions.login.Token = "not-a-valid-session-token"
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, apiPrefix+"/session", strings.NewReader(`{"credential":"credential-secret-value"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testAdminOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable || sessions.logoutCalls != 1 || sessions.logoutToken != sessions.login.Token || len(response.Result().Cookies()) != 0 {
		t.Fatalf("malformed Core session was not revoked safely: status=%d revokes=%d cookies=%v", response.Code, sessions.logoutCalls, response.Result().Cookies())
	}
}

func TestSessionInspectionRequiresCookieAndReturnsCurrentPrincipal(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	groupAccess := &sessionGroupAccessStub{groups: map[string]platform.AccessLevel{"features": platform.AccessLevelRead}}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin}, GroupAccess: groupAccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/session", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("session inspection returned %d, want %d", response.Code, http.StatusOK)
	}
	if sessions.resolveCalls != 1 || sessions.resolvedToken != sessions.login.Token {
		t.Fatalf("session inspection did not resolve the current Core session correctly")
	}
	var body struct {
		Principal struct {
			ID          string                          `json:"id"`
			GroupAccess map[string]platform.AccessLevel `json:"group_access"`
		} `json:"principal"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Principal.ID != "principal-1" || body.Principal.GroupAccess["features"] != platform.AccessLevelRead || groupAccess.calls != 1 || groupAccess.token != sessions.login.Token {
		t.Fatalf("session inspection principal/access = %+v; resolver calls=%d token=%q", body.Principal, groupAccess.calls, groupAccess.token)
	}
}

func TestSessionInspectionFailsClosedWhenCurrentGroupAccessCannotBeResolved(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	groupAccess := &sessionGroupAccessStub{err: errors.New("authorization backend unavailable")}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin}, GroupAccess: groupAccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/session", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "features") {
		t.Fatalf("unresolved group access was exposed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSessionInspectionRejectsDuplicateSessionCookiesBeforeCore(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	handler, err := NewHandlerWithSessions(&fakeIdentityAdmin{}, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/session", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized || sessions.resolveCalls != 0 {
		t.Fatalf("duplicate session cookies returned %d and reached Core %d times", response.Code, sessions.resolveCalls)
	}
}

func TestCookieAuthenticatedMutationRequiresAllowedOriginAndMatchingCSRFProof(t *testing.T) {
	sessions := &sessionStub{login: testSessionLogin()}
	admin := &fakeIdentityAdmin{}
	handler, err := NewHandlerWithSessions(admin, sessions, Config{
		MaxJSONBytes: 1024, SessionCookieSecure: true, AllowedOrigins: []string{testAdminOrigin},
	})
	if err != nil {
		t.Fatal(err)
	}
	newRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, apiPrefix+"/principals", strings.NewReader(`{"name":"Editor","group_access":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: sessions.login.Token})
		return req
	}

	missingProof := newRequest()
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, missingProof)
	if missing.Code != http.StatusForbidden || admin.createdCalls != 0 || sessions.resolveCalls != 0 {
		t.Fatalf("mutation without origin/CSRF proof returned %d, reached Core=%d session=%d", missing.Code, admin.createdCalls, sessions.resolveCalls)
	}

	badProof := newRequest()
	badProof.Header.Set("Origin", testAdminOrigin)
	badProof.AddCookie(&http.Cookie{Name: adminCSRFCookieName, Value: strings.Repeat("A", 43)})
	badProof.Header.Set("X-CSRF-Token", strings.Repeat("B", 42)+"A")
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, badProof)
	if bad.Code != http.StatusForbidden || admin.createdCalls != 0 || sessions.resolveCalls != 0 {
		t.Fatalf("mutation with mismatched CSRF proof returned %d, reached Core=%d session=%d", bad.Code, admin.createdCalls, sessions.resolveCalls)
	}

	csrfToken := strings.Repeat("A", 43)
	validProof := newRequest()
	validProof.Header.Set("Origin", testAdminOrigin)
	validProof.AddCookie(&http.Cookie{Name: adminCSRFCookieName, Value: csrfToken})
	validProof.Header.Set("X-CSRF-Token", csrfToken)
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, validProof)
	if accepted.Code != http.StatusCreated || admin.createdCalls != 1 || sessions.resolveCalls != 1 || admin.authCalls != 0 {
		t.Fatalf("valid cookie-authenticated mutation returned %d, creates=%d session=%d bearer=%d", accepted.Code, admin.createdCalls, sessions.resolveCalls, admin.authCalls)
	}

	duplicateCSRF := newRequest()
	duplicateCSRF.Header.Set("Origin", testAdminOrigin)
	duplicateCSRF.AddCookie(&http.Cookie{Name: adminCSRFCookieName, Value: csrfToken})
	duplicateCSRF.AddCookie(&http.Cookie{Name: adminCSRFCookieName, Value: csrfToken})
	duplicateCSRF.Header.Set("X-CSRF-Token", csrfToken)
	duplicateCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateCSRFResponse, duplicateCSRF)
	if duplicateCSRFResponse.Code != http.StatusForbidden || admin.createdCalls != 1 || sessions.resolveCalls != 1 {
		t.Fatalf("duplicate CSRF cookies returned %d and reached Core/admin %d/%d times", duplicateCSRFResponse.Code, sessions.resolveCalls, admin.createdCalls)
	}

	duplicateOrigin := newRequest()
	duplicateOrigin.Header.Add("Origin", testAdminOrigin)
	duplicateOrigin.Header.Add("Origin", testAdminOrigin)
	duplicateOrigin.AddCookie(&http.Cookie{Name: adminCSRFCookieName, Value: csrfToken})
	duplicateOrigin.Header.Set("X-CSRF-Token", csrfToken)
	duplicateOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateOriginResponse, duplicateOrigin)
	if duplicateOriginResponse.Code != http.StatusForbidden || admin.createdCalls != 1 || sessions.resolveCalls != 1 {
		t.Fatalf("duplicate Origin headers returned %d and reached Core/admin %d/%d times", duplicateOriginResponse.Code, sessions.resolveCalls, admin.createdCalls)
	}
}
