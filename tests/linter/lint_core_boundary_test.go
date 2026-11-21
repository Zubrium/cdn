package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIsolationProfileSupportsCompiledProfiles(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minimal", "full", "router", "development-fixtures"} {
		data, err := os.ReadFile(filepath.Join(root, "main", "profiles", name+".yaml"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		profile, err := parseIsolationProfile(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(profile.admin) == 0 || len(profile.public) == 0 && name != "minimal" && name != "development-fixtures" {
			t.Fatalf("%s: unexpected API selection %+v", name, profile)
		}
		if name == "development-fixtures" && !profile.devFixture {
			t.Fatal("development fixtures profile did not enable fixtures")
		}
		plan, err := isolationBuildPlan(profile)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if plan[0] != "core" {
			t.Fatalf("%s: plan starts with %q", name, plan[0])
		}
	}
}

func TestParseIsolationProfileRejectsDrift(t *testing.T) {
	for name, input := range map[string]string{
		"unknown key":     "version: 1\nengines: []\nworkers: []\napis:\n  admin: []\n  public: []\nextra: true\n",
		"missing version": "engines: []\nworkers: []\napis:\n  admin: []\n  public: []\n",
		"duplicate key":   "version: 1\nversion: 1\nengines: []\nworkers: []\napis:\n  admin: []\n  public: []\n",
		"unknown engine":  "version: 1\nengines: [missing]\nworkers: []\napis:\n  admin: []\n  public: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			profile, err := parseIsolationProfile([]byte(input))
			if err == nil {
				if _, planErr := isolationBuildPlan(profile); planErr == nil {
					t.Fatal("invalid profile was accepted")
				}
			}
		})
	}
}

func TestGeneratedProfileExtraction(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main", "internal", "composition", "profile_full_generated.go")
	generated, err := parseGeneratedProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if generated.packageName != "composition" || generated.buildTag != "full && !devfixtures && !router" {
		t.Fatalf("unexpected generated header: %+v", generated)
	}
	if generated.devFixtures {
		t.Fatal("full profile unexpectedly enables development fixtures")
	}
	if !strings.Contains(strings.Join(generated.plan, ","), "engine.content") {
		t.Fatalf("full plan omitted Content: %v", generated.plan)
	}
	if !generated.imports["github.com/Zubrium/cdn/engines/content/presentation"] {
		t.Fatal("full profile did not import the Content facade")
	}
}

func TestPresentationEngineInternalImportRule(t *testing.T) {
	if !isPresentationModule("github.com/Zubrium/cdn/presentation/admin/content") {
		t.Fatal("outer presentation module was not classified")
	}
	if isPresentationModule("github.com/Zubrium/cdn/core/presentation") {
		t.Fatal("core presentation package must not be classified as an outer module")
	}
	if !isInternalEngineImport("github.com/Zubrium/cdn/engines/forms/features/sessions/application") {
		t.Fatal("engine internal import was accepted")
	}
	if isInternalEngineImport("github.com/Zubrium/cdn/engines/forms/presentation") {
		t.Fatal("public engine facade must not be treated as internal")
	}
	if !presentationImportAllowed("presentation/admin/content/handler.go", "github.com/Zubrium/cdn/engines/content/domain") {
		t.Fatal("frozen baseline import must remain allowed")
	}
	if presentationImportAllowed("presentation/admin/content/handler.go", "github.com/Zubrium/cdn/engines/content/application") {
		t.Fatal("new internal engine import must be rejected")
	}
}

func TestGeneratedDevelopmentFixturesReadsBooleanLiteral(t *testing.T) {
	source := "package composition\nfunc GeneratedDevelopmentFixtures() bool { return true }\n"
	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "GeneratedDevelopmentFixtures" {
			continue
		}
		value, err := generatedBoolFromFunction(function)
		if err != nil || !value {
			t.Fatalf("boolean extraction = %t, %v", value, err)
		}
		return
	}
	t.Fatal("GeneratedDevelopmentFixtures declaration not found")
}
