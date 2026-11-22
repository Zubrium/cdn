package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPublication(t *testing.T) {
	t.Run("E2E-PUB-001", func(t *testing.T) {
		runPlaywrightScenario(t, "E2E-PUB-001", "specs/publication.spec.ts")
	})
}

func TestRetirement(t *testing.T) {
	t.Run("E2E-RET-001", func(t *testing.T) {
		runPlaywrightScenario(t, "E2E-RET-001", "specs/retirement.spec.ts")
	})
}

func runPlaywrightScenario(t *testing.T, scenarioID, spec string) {
	t.Helper()
	command := exec.Command("node", "node_modules/@playwright/test/cli.js", "test",
		"--config=playwright.config.ts", "--grep="+scenarioID, "--workers=1", spec)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	t.Logf("playwright %s output:\n%s", scenarioID, string(output))
	if err != nil {
		t.Fatalf("playwright %s failed: %v", scenarioID, err)
	}
	if !strings.Contains(string(output), "PASS "+scenarioID) {
		t.Fatalf("playwright %s did not report an exact PASS line", scenarioID)
	}
	if strings.Contains(string(output), "FAIL ") {
		t.Fatalf("playwright %s reported failure", scenarioID)
	}
}
