package presentation_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
)

func TestPublicCoreFacadeProvidesAccessAndGenericCoordinatorContracts(t *testing.T) {
	var _ interface {
		GroupAuthorization() platform.GroupAuthorizationCapability
		StagedFiles() platform.StagedFilesCapability
	} = (*platform.Runtime)(nil)
	registry := platform.NewRuntimeRegistry()
	if registry == nil {
		t.Fatal("runtime registry is nil")
	}
	var operation platform.OperationRef = platform.OperationRef{ClassID: "media-pool", ID: "derivative", Version: 1}
	if operation.ClassID != "media-pool" || platform.TaskKindOperation != "operation" {
		t.Fatal("generic operation task contract is unavailable through the Core facade")
	}
	if !platform.ValidAccessGroupKey("editorial") || platform.AccessLevelWrite != 2 || platform.AccessActionWrite == "" {
		t.Fatal("Core access contracts are not available through its public facade")
	}
}

func TestCoreDoesNotDependOnConcreteEnginesOrWorkers(t *testing.T) {
	root := repositoryRoot(t)
	base := filepath.Join(root, "core")
	moduleFile, err := os.ReadFile(filepath.Join(base, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range moduleRequirePaths(moduleFile) {
		for _, forbidden := range []string{"github.com/Zubrium/cdn/engines/", "github.com/Zubrium/cdn/workers/"} {
			if strings.HasPrefix(required, forbidden) {
				t.Errorf("Core module directly requires a concrete component package %q", required)
			}
		}
	}
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(source), "Loader") || strings.Contains(string(source), "loader") {
			t.Errorf("Core source %s names a concrete consuming module; keep its contract generic", path)
		}
		for _, imported := range file.Imports {
			value := strings.Trim(imported.Path.Value, "\"")
			for _, forbidden := range []string{
				"github.com/Zubrium/cdn/engines",
				"github.com/Zubrium/cdn/workers",
			} {
				if value == forbidden || strings.HasPrefix(value, forbidden+"/") {
					t.Errorf("Core implementation %s imports concrete component package %s", path, value)
				}
			}
		}
		if strings.HasPrefix(filepath.Clean(path), filepath.Join(base, "presentation")) {
			for _, forbidden := range []string{"LoaderIdentityCapability", "LoaderFilesCapability", "LoaderIdentity()", "LoaderFiles()"} {
				if strings.Contains(string(source), forbidden) {
					t.Errorf("Core public API exposes concrete Loader contract %q in %s", forbidden, path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Core implementation: %v", err)
	}
}

func TestExternalPackagesUseOnlyPublicCoreFacade(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{"main", "engines", "workers", "presentation"} {
		base := filepath.Join(root, relative)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range file.Imports {
				value := strings.Trim(imported.Path.Value, "\"")
				for _, forbidden := range []string{
					"github.com/Zubrium/cdn/core/application",
					"github.com/Zubrium/cdn/core/domain",
					"github.com/Zubrium/cdn/core/infrastructure",
					"github.com/Zubrium/cdn/core/internal",
				} {
					if value == forbidden || strings.HasPrefix(value, forbidden+"/") {
						t.Errorf("%s imports Core implementation package %s; use core/presentation", path, value)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inspect %s: %v", relative, err)
		}
	}
}

func TestCoreLayersLiveDirectlyUnderModuleRoot(t *testing.T) {
	coreRoot := filepath.Join(repositoryRoot(t), "core")
	for _, layer := range []string{"domain", "application", "infrastructure", "presentation"} {
		info, err := os.Stat(filepath.Join(coreRoot, layer))
		if err != nil || !info.IsDir() {
			t.Errorf("Core layer %q must be a directory directly under core: %v", layer, err)
		}
	}
	err := filepath.WalkDir(coreRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "internal" {
			t.Errorf("Core layers must stay directly in core; internal directory found at %s", path)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Core layer paths: %v", err)
	}
}

func TestCoreLayerImportsFollowDependencyDirection(t *testing.T) {
	coreRoot := filepath.Join(repositoryRoot(t), "core")
	modulePath := "github.com/Zubrium/cdn/core/"
	err := filepath.WalkDir(coreRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(coreRoot, path)
		if err != nil {
			return err
		}
		from := strings.SplitN(filepath.ToSlash(relative), "/", 2)[0]
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			value := strings.Trim(imported.Path.Value, "\\\"")
			if !strings.HasPrefix(value, modulePath) {
				continue
			}
			to := strings.SplitN(strings.TrimPrefix(value, modulePath), "/", 2)[0]
			if !coreLayerImportAllowed(from, to) {
				t.Errorf("Core %s package %s imports outward layer %s", from, path, to)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Core layer imports: %v", err)
	}
}

func TestCoreLayerImportPolicyRejectsOutwardDependencies(t *testing.T) {
	for _, pair := range [][2]string{
		{"domain", "application"},
		{"application", "infrastructure"},
		{"infrastructure", "presentation"},
	} {
		if coreLayerImportAllowed(pair[0], pair[1]) {
			t.Errorf("Core import %s -> %s must be rejected", pair[0], pair[1])
		}
	}
}

// moduleRequirePaths reads only require directives. A replace directive is a
// workspace-local build mapping, not a module dependency, and must not make
// this contract test fail.
func moduleRequirePaths(moduleFile []byte) []string {
	var paths []string
	inRequire, inReplace := false, false
	for _, raw := range strings.Split(string(moduleFile), "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "//", 2)[0])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if inReplace {
			if fields[0] == ")" {
				inReplace = false
			}
			continue
		}
		if inRequire {
			if fields[0] == ")" {
				inRequire = false
				continue
			}
			if len(fields) >= 2 {
				paths = append(paths, strings.Trim(fields[0], `"`))
			}
			continue
		}
		if fields[0] == "replace" {
			if len(fields) > 1 && fields[1] == "(" {
				inReplace = true
			}
			continue
		}
		if fields[0] == "require" {
			if len(fields) > 1 && fields[1] == "(" {
				inRequire = true
				continue
			}
			if len(fields) >= 2 {
				paths = append(paths, strings.Trim(fields[1], `"`))
			}
		}
	}
	return paths
}

func coreLayerImportAllowed(from, to string) bool {
	allowed := map[string]map[string]bool{
		"domain":         {"domain": true},
		"application":    {"domain": true, "application": true},
		"infrastructure": {"domain": true, "application": true, "infrastructure": true},
		"presentation":   {"domain": true, "application": true, "infrastructure": true, "presentation": true},
	}
	return allowed[from][to]
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	for current := filepath.Dir(currentFile); ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, "go.work")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("cannot locate repository root")
		}
	}
}
