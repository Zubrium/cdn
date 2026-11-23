package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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

func TestRuntimeModulesUseDataBusInsteadOfImportingCore(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	coreModule := "github.com/Zubrium/cdn/core"
	compositionModule := "github.com/Zubrium/cdn/main"
	for _, owner := range []string{"engines", "workers", "presentation", "pkg"} {
		base := filepath.Join(root, owner)
		if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Name() != "go.mod" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), coreModule) || strings.Contains(string(data), compositionModule) {
				t.Errorf("%s declares Core/composition as a module dependency; depend on pkg/contracts and use DataBus through main", path)
			}
			return nil
		}); err != nil {
			t.Fatalf("scan %s module manifests: %v", owner, err)
		}
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range file.Imports {
				importPath, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				if importPath == coreModule || strings.HasPrefix(importPath, coreModule+"/") || importPath == compositionModule || strings.HasPrefix(importPath, compositionModule+"/") {
					t.Errorf("%s imports Core/composition directly; depend on pkg/contracts and use DataBus through main", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", owner, err)
		}
	}
}

func TestCoreDoesNotModelEngineOrWorkerComponents(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]struct{}{
		"ComponentDescriptor": {}, "ComponentDescriptorProvider": {},
		"ComponentWorkerSet": {}, "WorkerCatalog": {}, "WorkerHandler": {},
		"WorkerRef": {}, "EngineIdentityCapability": {}, "EngineFilesCapability": {},
		"engineIdentityCapability": {}, "engineFilesCapability": {},
	}
	base := filepath.Join(root, "core")
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			if _, prohibited := forbidden[identifier.Name]; prohibited {
				t.Errorf("%s models %s; Core must use generic DataBus operation and task contracts", path, identifier.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan Core sources: %v", err)
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
