package router

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
)

const publicFormID = "01938f7a-68af-7c10-a51d-78b6ca125301"

func publicFormToken() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func TestPublicFormSessionCookieIsHttpOnlyHostOnlyAndForwardedByIdentity(t *testing.T) {
	token := publicFormToken()
	api := &fakeRouter{delivery: engine.DeliveryResponse{
		StatusCode: http.StatusOK, ContentLength: 0, CacheControl: "no-store",
		FormSessionCookie: &engine.FormSessionCookieChange{FormID: publicFormID, Token: token, ExpiresAt: time.Now().Add(time.Hour)},
	}}
	config := validTestConfig()
	config.FormSessionCookieSecure = true
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/contact/session", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-zubrium-form-" + publicFormID, Value: token, Path: "/", Secure: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || api.deliveryInput.FormSessionTokens[publicFormID] != token {
		t.Fatalf("session token not forwarded to the configured public operation: status=%d input=%+v body=%s", response.Code, api.deliveryInput, response.Body.String())
	}
	cookie := response.Header().Get("Set-Cookie")
	for _, part := range []string{"__Host-zubrium-form-" + publicFormID + "=" + token, "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(cookie, part) {
			t.Fatalf("Set-Cookie %q is missing %q", cookie, part)
		}
	}
	if strings.Contains(strings.ToLower(cookie), "domain=") {
		t.Fatalf("form session cookie must remain host-only: %q", cookie)
	}
}

func TestPublicFormCookieRejectsDuplicateCapabilityCookie(t *testing.T) {
	api := &fakeRouter{delivery: engine.DeliveryResponse{StatusCode: http.StatusOK, ContentLength: 0, CacheControl: "no-store"}}
	config := validTestConfig()
	config.FormSessionCookieSecure = true
	handler, err := NewHandler(api, config)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/contact/session", nil)
	name := "__Host-zubrium-form-" + publicFormID
	request.AddCookie(&http.Cookie{Name: name, Value: publicFormToken(), Path: "/", Secure: true})
	request.AddCookie(&http.Cookie{Name: name, Value: publicFormToken(), Path: "/", Secure: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || api.deliveryInput.Method != "" {
		t.Fatalf("ambiguous form cookies reached Router: status=%d delivered=%+v body=%s", response.Code, api.deliveryInput, response.Body.String())
	}
}
