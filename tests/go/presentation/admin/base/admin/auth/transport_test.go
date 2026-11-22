package httpauth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sessionFixture() string {
	return "ss_" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func csrfFixture() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func TestResolveSessionAndBearerCredentials(t *testing.T) {
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	sessionRequest.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionFixture()})
	credential, err := Resolve(sessionRequest)
	if err != nil || !credential.Session || credential.Value != sessionFixture() {
		t.Fatalf("session credential=%+v err=%v", credential, err)
	}

	bearerRequest := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	bearerRequest.Header.Set("Authorization", "Bearer service-secret")
	credential, err = Resolve(bearerRequest)
	if err != nil || credential.Session || credential.Value != "service-secret" {
		t.Fatalf("bearer credential=%+v err=%v", credential, err)
	}
}

func TestResolveRejectsAmbiguousAndMalformedCredentials(t *testing.T) {
	for name, request := range map[string]*http.Request{
		"cookie and bearer": func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionFixture()})
			request.Header.Set("Authorization", "Bearer service-secret")
			return request
		}(),
		"duplicate cookie": func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionFixture()})
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionFixture()})
			return request
		}(),
		"multiple bearer headers": func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Add("Authorization", "Bearer first")
			request.Header.Add("Authorization", "Bearer second")
			return request
		}(),
		"invalid session token": func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "ss_invalid"})
			return request
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(request)
			if err == nil {
				t.Fatal("malformed authentication was accepted")
			}
			if name == "cookie and bearer" || name == "multiple bearer headers" {
				if !errors.Is(err, ErrAmbiguous) {
					t.Fatalf("error=%v, want ErrAmbiguous", err)
				}
			} else if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("error=%v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestSessionWritesRequireExactOriginAndMatchingCSRF(t *testing.T) {
	transport, err := New([]string{"https://admin.example"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/files/123", nil)
	request.Header.Set("Origin", "https://admin.example")
	request.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: csrfFixture()})
	request.Header.Set("X-CSRF-Token", csrfFixture())
	if err := transport.ValidateSessionRequest(request); err != nil {
		t.Fatalf("valid cookie request rejected: %v", err)
	}

	request.Header.Set("Origin", "https://evil.example")
	if err := transport.ValidateSessionRequest(request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("untrusted origin returned %v", err)
	}
	request.Header.Set("Origin", "https://admin.example")
	request.Header.Set("X-CSRF-Token", csrfFixture()+"x")
	if err := transport.ValidateSessionRequest(request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("invalid CSRF proof returned %v", err)
	}
}

func TestSessionReadsDoNotRequireCSRFAndOriginsAreStrict(t *testing.T) {
	transport, err := New([]string{"https://admin.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.ValidateSessionRequest(httptest.NewRequest(http.MethodGet, "/api/files", nil)); err != nil {
		t.Fatalf("safe read was rejected: %v", err)
	}
	for _, origin := range [][]string{
		{},
		{"https://admin.example", "https://admin.example"},
		{"https://admin.example/"},
		{"https://*.example"},
		{"https://admin.example:443"},
	} {
		if _, err := New(origin); err == nil {
			t.Errorf("invalid origin list %v accepted", origin)
		}
	}
}

func TestTrustedReadOriginAcceptsSameOriginEventSourceAndRejectsSpoofing(t *testing.T) {
	transport, err := New([]string{"http://127.0.0.1:18080"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/notifications", nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Referer", "http://127.0.0.1:18080/admin/")
	if !transport.TrustedReadOrigin(request) {
		t.Fatal("browser-style same-origin EventSource request was rejected")
	}

	for name, mutate := range map[string]func(*http.Request){
		"cross-site fetch metadata": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"foreign referer":           func(r *http.Request) { r.Header.Set("Referer", "http://evil.example/admin/") },
		"missing referer":           func(r *http.Request) { r.Header.Del("Referer") },
		"explicit foreign origin":   func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") },
	} {
		t.Run(name, func(t *testing.T) {
			rejected := httptest.NewRequest(http.MethodGet, "/api/admin/notifications", nil)
			rejected.Header.Set("Sec-Fetch-Site", "same-origin")
			rejected.Header.Set("Referer", "http://127.0.0.1:18080/admin/")
			mutate(rejected)
			if transport.TrustedReadOrigin(rejected) {
				t.Fatal("untrusted read request was accepted")
			}
		})
	}
}
