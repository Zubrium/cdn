package appconfig

import (
	"testing"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
)

func TestResolveMapsLocalePreferenceStartupContract(t *testing.T) {
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
		"ROUTER_CRED": "router-service-credential",
	}
	secure := true
	config := validRuntimeConfig()
	config.RouterAPI.ServiceCredentialEnv = "ROUTER_CRED"
	config.RouterAPI.LocalePreference = RouterLocalePreference{Enabled: true, Secure: &secure, MaxAge: "48h"}
	resolved, err := config.Resolve(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("explicit locale preference configuration rejected: %v", err)
	}
	if !resolved.RouterAPI.LocalePreferenceEnabled || !resolved.RouterAPI.LocalePreferenceSecure || resolved.RouterAPI.LocalePreferenceMaxAge != 48*time.Hour {
		t.Fatalf("resolved locale preference = %+v", resolved.RouterAPI)
	}
	plan := platform.BuildPlan{Components: []string{"core", "engine.router", "public.router", "public.locale-preference"}}
	if err := resolved.ValidateBuildPlan(plan); err != nil {
		t.Fatalf("compiled locale preference operation rejected: %v", err)
	}
}

func TestLocalePreferenceRequiresExplicitLifetimeAndTransport(t *testing.T) {
	values := map[string]string{
		"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
		"ACCESS":       "access", "SECRET": "secret",
	}
	secure := true
	for _, item := range []struct {
		name    string
		prefs   RouterLocalePreference
		wantErr bool
	}{
		{"missing_secure", RouterLocalePreference{Enabled: true, MaxAge: "1h"}, true},
		{"missing_max_age", RouterLocalePreference{Enabled: true, Secure: &secure}, true},
		{"zero_max_age", RouterLocalePreference{Enabled: true, Secure: &secure, MaxAge: "0s"}, true},
		{"fractional_max_age", RouterLocalePreference{Enabled: true, Secure: &secure, MaxAge: "1500ms"}, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			config := validRuntimeConfig()
			config.RouterAPI.LocalePreference = item.prefs
			_, err := config.Resolve(func(key string) string { return values[key] })
			if err == nil {
				t.Fatal("incomplete locale preference configuration was accepted")
			}
		})
	}
}

func TestLocalePreferenceProfileMustMatchConfiguration(t *testing.T) {
	compiled := platform.BuildPlan{Components: []string{"core", "engine.router", "public.router", "public.locale-preference"}}
	notCompiled := platform.BuildPlan{Components: []string{"core", "engine.router", "public.router"}}
	enabled := Resolved{RouterAPI: RouterAPISettings{
		ServiceCredential: "router-service-credential", LocalePreferenceEnabled: true,
		LocalePreferenceSecure: true, LocalePreferenceMaxAge: time.Hour,
	}}
	if err := enabled.ValidateBuildPlan(notCompiled); err == nil {
		t.Fatal("enabled locale preference accepted without a compiled operation")
	}
	disabled := Resolved{}
	if err := disabled.ValidateBuildPlan(compiled); err == nil {
		t.Fatal("compiled locale preference accepted without startup configuration")
	}
}
