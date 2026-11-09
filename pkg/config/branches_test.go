package config

import (
	"strings"
	"testing"
)

func TestBranchesRequireExplicitUniqueStaticConfiguration(t *testing.T) {
	for _, raw := range []string{`[]`, `[{"name":"x","httpAddr":":8081","corsOrigins":[]}]`, `[{"name":"x","httpAddr":":8080","corsOrigins":[],"indexable":false}]`, `[{"name":"x","httpAddr":":8081","corsOrigins":[],"indexable":false},{"name":"y","httpAddr":":8081","corsOrigins":[],"indexable":true}]`, `[{"name":"x","httpAddr":":8081","corsOrigins":["https://example.com/path"],"indexable":false}]`, `[{"name":"x","httpAddr":":8081,"corsOrigins":[],"indexable":false}] {}`} {
		if _, err := ParseBranches(raw, ":8080"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := ParseBranches(`[{"name":"произвольная ветка","httpAddr":":8081","corsOrigins":[],"indexable":true}]`, ":8080"); err != nil {
		t.Fatal(err)
	}
}

func TestBranchAddressErrorsDoNotEchoCredentials(t *testing.T) {
	_, err := ParseBranches(`[{"name":"preview","httpAddr":":8081","corsOrigins":[],"indexable":false}]`, "https://admin:private-password@example.test")
	if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
		t.Fatalf("admin address error = %v, want HTTP_ADDR validation", err)
	}
	if strings.Contains(err.Error(), "admin") || strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), "example.test") {
		t.Fatalf("admin address error leaked input: %v", err)
	}

	_, err = ParseBranches(`[{"name":"preview","httpAddr":":8081","corsOrigins":["https://service:private-password@example.test"],"indexable":false}]`, ":8080")
	if err == nil || !strings.Contains(err.Error(), "BRANCHES") {
		t.Fatalf("CORS error = %v, want BRANCHES validation", err)
	}
	if strings.Contains(err.Error(), "service") || strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), "example.test") {
		t.Fatalf("CORS error leaked origin input: %v", err)
	}
}
