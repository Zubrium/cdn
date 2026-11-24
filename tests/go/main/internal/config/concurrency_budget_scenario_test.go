package appconfig

import (
	"strings"
	"testing"
	"time"
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

// TestConcurrencyClassPoolScenario owns the CONC-008 configuration contract.
// Each worker class has its own explicit pool settings; fail-fast validation
// applies to all configured pools, including disabled ones, because a disabled
// pool can still be the target of a durable cross-class child task.
func TestConcurrencyClassPoolScenario(t *testing.T) {
	t.Run("CONC-008", func(t *testing.T) {
		values := map[string]string{
			"DB":           "postgres://runtime:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"MIGRATION_DB": "postgres://migrator:local-secret@127.0.0.1:5432/zubrium_core_qa_unit",
			"ACCESS":       "access", "SECRET": "secret",
		}
		getenv := func(key string) string { return values[key] }
		config := validRuntimeConfig()
		config.Core.MaxConnections = 20
		config.Worker.Media.Concurrency = WorkerPoolConcurrency{Enabled: true, Limit: 2,
			PollInterval: "50ms", TaskTimeout: "2m", LeaseDuration: "5s"}
		config.Worker.Prerender.Concurrency = WorkerPoolConcurrency{Enabled: false, Limit: 3,
			PollInterval: "25ms", TaskTimeout: "3m", LeaseDuration: "8s"}
		resolved, err := config.Resolve(getenv)
		if err != nil {
			t.Fatalf("explicit per-class worker pools rejected: %v", err)
		}
		if got := resolved.Scales.WorkerPools.Media; !got.Enabled || got.Limit != 2 ||
			got.PollInterval != 50*time.Millisecond || got.TaskTimeout != 2*time.Minute || got.LeaseDuration != 5*time.Second {
			t.Fatalf("media pool settings were not resolved per class: %+v", got)
		}
		if got := resolved.Scales.WorkerPools.Prerender; got.Enabled || got.Limit != 3 ||
			got.PollInterval != 25*time.Millisecond || got.TaskTimeout != 3*time.Minute || got.LeaseDuration != 8*time.Second {
			t.Fatalf("disabled prerender pool settings were not resolved: %+v", got)
		}

		invalid := map[string]func(*Config){
			"limit":         func(c *Config) { c.Worker.Prerender.Concurrency.Limit = 257 },
			"poll_interval": func(c *Config) { c.Worker.Prerender.Concurrency.PollInterval = "10s" },
			"task_timeout":  func(c *Config) { c.Worker.Prerender.Concurrency.TaskTimeout = "25h" },
			"lease_duration": func(c *Config) {
				c.Worker.Prerender.Concurrency.LeaseDuration = "2s"
			},
		}
		for name, mutate := range invalid {
			t.Run(name, func(t *testing.T) {
				candidate := validRuntimeConfig()
				candidate.Worker.Prerender.Concurrency.Enabled = false
				mutate(&candidate)
				if _, err := candidate.Resolve(getenv); err == nil {
					t.Fatalf("disabled prerender pool with invalid %s was accepted", name)
				}
			})
		}
	})
}
