package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// frozenLinterRules is the SHA-256 manifest of the linter rules after the
// final green architecture sweep. These rules are frozen: relaxations or new
// rules require an explicit decision and a deliberate manifest update.
var frozenLinterRules = map[string]string{
	"lint_core_boundary.go":     "fcf9f9585baea891abd0bcc2d6675ef7aa94bd3a34d504ba49ee0f221b3e9b16",
	"lint_core_boundary_test.go": "a653172a5e20dced71ab68684b938f259a95fbddd17732f52ebbbbf4ec7c1c0c",
	"presentation_check.go":     "b67345d617b90b96853b35dd6bb259587a0a7a5c1e3c36eab906027578aa44ff",
	"profile_check.go":          "ee9ffa3d91cf0176d190f2d77d5423d1f7355e5800bd4a980d5b01b3ff15a2df",
	"runtime_check.go":          "46a557dbdbd6b6d8411f487f86bd7291efca65a4c2485e51804889a896a81080",
	"strict_boundary_check.go":  "c2b18f5d6f69220b2d0407202df8a8d91be5d3c62f43800a62dc0116858f9a11",
}

func TestLinterRulesAreFrozenAfterFinalSweep(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "tests", "linter")
	for name, want := range frozenLinterRules {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("read frozen rule %s: %v", name, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("linter rule %s digest = %s, want %s", name, got, want)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || entry.Name() == "rules_freeze_test.go" {
			continue
		}
		if _, frozen := frozenLinterRules[entry.Name()]; !frozen {
			t.Errorf("new linter rule file %s is not covered by the freeze guard", entry.Name())
		}
	}
}
