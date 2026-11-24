package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zubrium/cdn/engines/router/features/routes/model"
	engine "github.com/Zubrium/cdn/engines/router/presentation"
)

type localePreferenceResolver func(context.Context, string, string) (model.Route, error)

func (resolve localePreferenceResolver) ResolveRoute(ctx context.Context, host, path string) (model.Route, error) {
	return resolve(ctx, host, path)
}

func localePreferenceTestRoute(locale string) model.Route {
	return model.Route{BranchID: "main", State: model.StateActive, Target: model.Target{
		Kind:          model.TargetStaticSiteArtifact,
		DefaultLocale: "en-US",
		LocaleTargets: []model.LocaleArtifactTarget{{Locale: locale, ArtifactManifestFileID: "01938f7a-68af-7c10-a51d-78b6ca125301"}},
	}}
}

func localePreferenceTestHandler(t *testing.T, enabled bool) *Handler {
	t.Helper()
	handler, err := NewHandler(cacheScenarioHandlerAPI{deliver: func(context.Context, engine.DeliveryRequest) (engine.DeliveryResponse, error) {
		return engine.DeliveryResponse{}, nil
	}}, Config{
		BranchID:        "main",
		MaxJSONBytes:    1024,
		DefaultPageSize: 1,
		MaxPageSize:     10,
		LocalePreference: LocalePreferenceConfig{
			Enabled: enabled,
			Secure:  true,
			MaxAge:  24 * time.Hour,
		},
		LocaleResolver: localePreferenceResolver(func(_ context.Context, _, path string) (model.Route, error) {
			if path != "/site/" {
				t.Fatalf("resolver path = %q, want /site/", path)
			}
			return localePreferenceTestRoute("de-DE"), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestLocalePreferenceSetsHostOnlyCookieAndRedirects(t *testing.T) {
	handler := localePreferenceTestHandler(t, true)
	request := httptest.NewRequest(http.MethodPost, "/api/locale-preference", strings.NewReader(`{"locale":"de-DE","return_path":"/site/"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Host = "www.example.invalid"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/site/" {
		t.Fatalf("Location = %q, want /site/", location)
	}
	if control := response.Header().Get("Cache-Control"); control != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", control)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "zubrium_locale" || cookie.Value != "de-DE" {
		t.Fatalf("cookie = %s=%s", cookie.Name, cookie.Value)
	}
	if cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure || cookie.MaxAge != 86400 || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes = %+v", cookie)
	}
}

func TestLocalePreferenceRejectsInvalidRequestsWithoutCookie(t *testing.T) {
	handler := localePreferenceTestHandler(t, true)
	cases := []struct {
		name string
		body string
	}{
		{"unsupported_locale", `{"locale":"fr-FR","return_path":"/site/"}`},
		{"external_path", `{"locale":"de-DE","return_path":"https://evil.example/site/"}`},
		{"fragment", `{"locale":"de-DE","return_path":"/site/#section"}`},
		{"encoded_separator", `{"locale":"de-DE","return_path":"/site%2F"}`},
		{"unknown_field", `{"locale":"de-DE","return_path":"/site/","extra":true}`},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/locale-preference", strings.NewReader(item.body))
			request.Header.Set("Content-Type", "application/json")
			request.Host = "www.example.invalid"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			if cookies := response.Result().Cookies(); len(cookies) != 0 {
				t.Fatalf("unexpected cookies: %+v", cookies)
			}
		})
	}
}

func TestLocalePreferenceRequiresExplicitConfig(t *testing.T) {
	api := cacheScenarioHandlerAPI{deliver: func(context.Context, engine.DeliveryRequest) (engine.DeliveryResponse, error) {
		return engine.DeliveryResponse{}, nil
	}}
	resolver := localePreferenceResolver(func(context.Context, string, string) (model.Route, error) {
		return localePreferenceTestRoute("de-DE"), nil
	})
	for _, item := range []struct {
		name string
		cfg  Config
	}{
		{"missing_max_age", Config{BranchID: "main", MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10, LocalePreference: LocalePreferenceConfig{Enabled: true}, LocaleResolver: resolver}},
		{"missing_resolver", Config{BranchID: "main", MaxJSONBytes: 1024, DefaultPageSize: 1, MaxPageSize: 10, LocalePreference: LocalePreferenceConfig{Enabled: true, MaxAge: time.Hour}}},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := NewHandler(api, item.cfg); err == nil {
				t.Fatal("NewHandler accepted incomplete locale preference configuration")
			}
		})
	}
}

func TestLocalePreferenceDisabledFailsClosed(t *testing.T) {
	handler := localePreferenceTestHandler(t, false)
	request := httptest.NewRequest(http.MethodPost, "/api/locale-preference", strings.NewReader(`{"locale":"de-DE","return_path":"/site/"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("unexpected cookies: %+v", cookies)
	}
}
