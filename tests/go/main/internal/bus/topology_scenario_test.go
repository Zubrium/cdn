//go:build router && !full && !devfixtures

package bus_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Zubrium/cdn/main/internal/composition"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
)

func topologyRead(t *testing.T, relative string) string {
	t.Helper()
	absolute, err := filepath.Abs(filepath.Join("../../..", relative))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

func topologyYAMLPaths(source string) []string {
	var paths []string
	for _, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(line, "  /") {
			paths = append(paths, strings.TrimSpace(strings.SplitN(line, ":", 2)[0]))
		}
	}
	return paths
}

func TestRouterTopologyScenario(t *testing.T) {
	t.Run("CONC-012", func(t *testing.T) {
		plan := composition.BuildPlan()
		publicRouterCount := 0
		for _, component := range plan.Components {
			if component == "public.router" {
				publicRouterCount++
			}
		}
		if publicRouterCount != 1 {
			t.Fatalf("compiled profile selects public.router %d times, want exactly one process component: %v", publicRouterCount, plan.Components)
		}

		configSource := topologyRead(t, "main/config.example.yaml")
		cfg, err := appconfig.Load(strings.NewReader(configSource))
		if err != nil {
			t.Fatalf("load example config: %v", err)
		}
		resolved, err := cfg.Resolve(func(name string) string {
			switch name {
			case "ZUBRIUM_DATABASE_URL":
				return "postgres://runtime:runtime@127.0.0.1:5432/zubrium?sslmode=disable"
			case "ZUBRIUM_MIGRATION_DATABASE_URL":
				return "postgres://migration:migration@127.0.0.1:5432/zubrium?sslmode=disable"
			default:
				return "topology-test-value"
			}
		})
		if err != nil {
			t.Fatalf("resolve example config: %v", err)
		}
		if strings.TrimSpace(resolved.RouterAPI.BranchID) == "" {
			t.Fatal("resolved configuration has no single Router branch")
		}
		if _, err := resolved.WithBuildPlan(plan); err != nil {
			t.Fatalf("build plan does not match resolved topology: %v", err)
		}

		versionPrefix := regexp.MustCompile(`/api/v[0-9]+(/|$)`)
		specFiles, err := filepath.Glob(filepath.Join("../../..", "spec", "presentation", "openapi", "*.yaml"))
		if err != nil || len(specFiles) == 0 {
			t.Fatalf("cannot enumerate public OpenAPI contracts: %v", err)
		}
		for _, file := range specFiles {
			source := topologyRead(t, strings.TrimPrefix(file, "../../../"))
			for _, path := range topologyYAMLPaths(source) {
				if versionPrefix.MatchString(path) {
					t.Errorf("%s declares versioned public path %s", file, path)
				}
			}
		}

		routerSource := topologyRead(t, "presentation/router/handler.go")
		publicMux := regexp.MustCompile(`publicMux\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(routerSource, -1)
		registered := map[string]bool{}
		for _, match := range publicMux {
			registered[match[1]] = true
		}
		for _, path := range []string{"/{$}", "/{path...}"} {
			if !registered[path] {
				t.Errorf("public Router mux no longer registers %s: %v", path, registered)
			}
		}
		for path := range registered {
			if path != "/{$}" && path != "/{path...}" {
				t.Errorf("new public Router path %s was registered", path)
			}
			if versionPrefix.MatchString(path) || strings.HasPrefix(path, "/api/admin") {
				t.Errorf("public Router mux exposes a reserved path: %s", path)
			}
		}

		filesSpec := topologyYAMLPaths(topologyRead(t, "spec/presentation/openapi/files.yaml"))
		expectedCorePaths := []string{
			"/api/files/limits", "/api/files", "/api/files/uploads", "/api/files/uploads/{id}",
			"/api/files/uploads/{id}/parts/{number}", "/api/files/uploads/{id}/commit",
			"/api/files/objects/{id}", "/api/files/objects/{id}/content",
			"/api/files/objects/{id}/groups/{group}", "/api/files/sets", "/api/files/sets/{id}",
			"/api/files/sets/{id}/files", "/api/files/sets/{id}/files/{fileId}",
		}
		if len(filesSpec) != len(expectedCorePaths) {
			t.Fatalf("public Core file paths changed: %v", filesSpec)
		}
		for index, path := range expectedCorePaths {
			if filesSpec[index] != path {
				t.Errorf("public Core file path %d = %s, want %s", index, filesSpec[index], path)
			}
		}

		transportSource := topologyRead(t, "presentation/admin/base/transport/transport.go")
		for _, path := range []string{"/api/files", "/api/files/", "/healthz", "/readyz"} {
			if !strings.Contains(transportSource, `"`+path+`"`) && !strings.Contains(transportSource, `"GET `+path+`"`) {
				t.Errorf("transport no longer registers existing path %s", path)
			}
		}
		if versionPrefix.MatchString(transportSource) {
			t.Error("transport source declares a versioned public path")
		}
	})
}
