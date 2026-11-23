package appconfig

import (
	"strings"
	"testing"
)

// TestConcurrencyBudgetScenario verifies that the configuration rejects a
// connection pool that cannot fund the configured process-local admission
// bounds. The assertion happens before any listener is opened.
func TestConcurrencyBudgetScenario(t *testing.T) {
	t.Run("CONC-004", func(t *testing.T) {
		values := map[string]string{
			"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"ACCESS":       "access", "SECRET": "secret",
		}
		getenv := func(key string) string { return values[key] }
		config := validRuntimeConfig()
		if _, err := config.Resolve(getenv); err != nil {
			t.Fatalf("valid connection budget rejected: %v", err)
		}
		config.Core.MaxConnections = 11
		_, err := config.Resolve(getenv)
		if err == nil {
			t.Fatal("connection pool below the configured concurrency budget was accepted")
		}
		if !strings.Contains(err.Error(), "core.max_connections 11 is below required budget 12") {
			t.Fatalf("unexpected budget error: %v", err)
		}
	})
}

// TestConcurrencyClassPoolScenario owns the blocked CONC-008 contract. The
// scenario stays blocked until class-scoped claim activates isolated worker
// pools in production; it is deliberately not executed by the unit runner.
func TestConcurrencyClassPoolScenario(t *testing.T) {
	t.Run("CONC-008", func(t *testing.T) {
		t.Skip("isolated worker pools are not activated until class-scoped claim is wired in production")
	})
}
