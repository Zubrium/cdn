package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWritesOnlyValidatedSource(t *testing.T) {
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "profile.yaml")
	outputPath := filepath.Join(directory, "generated.go")
	valid := "version: 1\nengines: [loader]\nworkers: []\napis:\n  admin: [files]\n  public: []\nautomation: []\n"
	if err := os.WriteFile(profilePath, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generate(profilePath, outputPath, "main", "!full"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(outputPath)
	if err != nil || !strings.Contains(string(before), "GeneratedBuildPlan") {
		t.Fatalf("generated source missing: err=%v source=%s", err, before)
	}
	if err := os.WriteFile(profilePath, []byte("version: 1\nengines: [missing]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generate(profilePath, outputPath, "main", "!full"); err == nil {
		t.Fatal("invalid profile overwrote generated source")
	}
	after, err := os.ReadFile(outputPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed generation changed the previous output: err=%v", err)
	}
}
