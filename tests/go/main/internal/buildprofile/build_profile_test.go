package buildprofile

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBuildProfileRejectsInvalidDocuments(t *testing.T) {
	for _, input := range []string{
		"version: 1\nengines: [loader]\nunknown: true\n",
		"version: 1\nversion: 1\n",
		"version: 1\n---\nversion: 1\n",
		"version: 2\n",
		"version: 1\nengines: [loader, loader]\n",
		"version: 1\nengines: [unknown]\n",
		"version: 1\napis:\n  admin: [files]\n",
		"version: 1\nprofile: &name test\nengines: [*name]\n",
	} {
		if _, err := ParseBuildProfile(strings.NewReader(input)); err == nil {
			t.Errorf("invalid profile accepted: %q", input)
		}
	}
}

func TestGeneratedProfileSourceIsDeterministicAndImportsOnlySelectedComponents(t *testing.T) {
	plan, err := ParseBuildProfile(strings.NewReader("version: 1\nengines: [loader]\nworkers: []\napis:\n  admin: [files]\n  public: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := GenerateBuildProfileGoWithTag(plan, "main", "!full")
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateBuildProfileGoWithTag(plan, "main", "!full")
	if err != nil || string(first) != string(second) {
		t.Fatalf("profile generation is not deterministic: equal=%v err=%v", string(first) == string(second), err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", first, parser.AllErrors); err != nil {
		t.Fatalf("generated Go source does not parse: %v", err)
	}
	text := string(first)
	if !strings.Contains(text, "github.com/Zubrium/cdn/engines/loader/presentation") || !strings.Contains(text, "github.com/Zubrium/cdn/presentation/fileapi") {
		t.Fatalf("selected module imports are missing:\n%s", text)
	}
	if !strings.Contains(text, "github.com/Zubrium/cdn/core/presentation") || strings.Contains(text, "core/application") || strings.Contains(text, "core/domain") {
		t.Fatalf("generated profile must use only the public Core facade:\n%s", text)
	}
	for _, unselected := range []string{"engines/router/presentation", "workers/prerender", "presentation/admin/forms"} {
		if strings.Contains(text, unselected) {
			t.Fatalf("unselected module %q leaked into generated source:\n%s", unselected, text)
		}
	}
	if _, err := GenerateBuildProfileGoWithTag(plan, "not-valid", "full ||"); err == nil {
		t.Fatal("invalid Go package or build-tag expression accepted")
	}
	if _, err := GenerateBuildProfileGoWithTag(plan, "main", "!full\npackage injected"); err == nil {
		t.Fatal("multi-line build-tag input was allowed to inject generated Go source")
	}
}

func TestBuildProfileProducesDeterministicDependencyFirstPlan(t *testing.T) {
	first := "version: 1\nengines: [router, loader]\nworkers: []\napis:\n  admin: [files]\n  public: [router]\n"
	second := "apis:\n  public: [router]\n  admin: [files]\nengines: [loader, router]\nworkers: []\nversion: 1\n"
	a, err := ParseBuildProfile(strings.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBuildProfile(strings.NewReader(second))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Components, b.Components) {
		t.Fatalf("equivalent profiles produced different plans: %v and %v", a.Components, b.Components)
	}
	positions := map[string]int{}
	for i, id := range a.Components {
		positions[id] = i
	}
	for _, edge := range [][2]string{{"core", "engine.loader"}, {"engine.loader", "admin.files"}, {"engine.router", "public.router"}} {
		if _, ok := positions[edge[0]]; !ok {
			t.Fatalf("missing %s", edge[0])
		}
		if _, ok := positions[edge[1]]; !ok || positions[edge[0]] >= positions[edge[1]] {
			t.Fatalf("dependency order %v violated: %v", edge, a.Components)
		}
	}
}

func TestIdentityAPIRequiresSharedFileAPIServer(t *testing.T) {
	plan, err := ParseBuildProfile(strings.NewReader("version: 1\nengines: [loader]\nworkers: []\napis:\n  admin: [files, identity]\n  public: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for i, id := range plan.Components {
		positions[id] = i
	}
	if positions["admin.files"] >= positions["admin.identity"] {
		t.Fatalf("identity API must start after the shared Admin listener: %v", plan.Components)
	}
	if _, err := ParseBuildProfile(strings.NewReader("version: 1\nengines: []\nworkers: []\napis:\n  admin: [identity]\n  public: []\n")); err == nil {
		t.Fatal("identity API was accepted without its File API listener")
	}
}

func TestSharedAdminListenerStartsAfterSelectedEngines(t *testing.T) {
	for name, testCase := range map[string]struct {
		input   string
		earlier []string
	}{
		"explicit": {
			input:   "version: 1\nengines: [forms, loader]\nworkers: []\napis:\n  admin: [files, forms]\n  public: []\n",
			earlier: []string{"engine.forms"},
		},
		"full": {
			input:   mustReadProfileFixture(t, "full.yaml"),
			earlier: []string{"engine.forms", "engine.website", "engine.router"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := ParseBuildProfile(strings.NewReader(testCase.input))
			if err != nil {
				t.Fatal(err)
			}
			positions := map[string]int{}
			for i, id := range plan.Components {
				positions[id] = i
			}
			if positions["admin.files"] == 0 {
				t.Fatalf("profile omitted the shared Admin listener: %v", plan.Components)
			}
			for _, id := range testCase.earlier {
				if positions[id] == 0 {
					t.Fatalf("profile omitted %s: %v", id, plan.Components)
				}
				if positions[id] >= positions["admin.files"] {
					t.Fatalf("%s must start before the Admin listener that wires it: %v", id, plan.Components)
				}
			}
			if name == "full" && positions["engine.content"] >= positions["engine.router"] {
				t.Fatalf("Content engine must start before Router consumes its public runtime: %v", plan.Components)
			}
		})
	}
}

func mustReadProfileFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "profiles", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestPublicUploadIsASeparateLoaderAndRouterOperation(t *testing.T) {
	input := "version: 1\nengines: [loader, router]\nworkers: []\napis:\n  admin: []\n  public: [router, form-upload]\n"
	plan, err := ParseBuildProfile(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	components := map[string]bool{}
	for _, component := range plan.Components {
		components[component] = true
	}
	for _, component := range []string{"engine.loader", "engine.router", "public.router", "public.form-upload"} {
		if !components[component] {
			t.Errorf("upload profile omitted %s: %v", component, plan.Components)
		}
	}
	for _, component := range []string{"engine.forms", "public.form-submit"} {
		if components[component] {
			t.Errorf("independent upload route unexpectedly requires %s: %v", component, plan.Components)
		}
	}
	source, err := GenerateBuildProfileGo(plan, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "github.com/Zubrium/cdn/presentation/router/publicupload") {
		t.Fatalf("selected upload operation was not linked into the profile:\n%s", source)
	}
}

func TestProductionRouterProfileRunsWithoutUnimplementedEngines(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "profiles", "router.yaml"))
	if err != nil {
		t.Fatalf("read production Router profile: %v", err)
	}
	plan, err := ParseBuildProfile(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("parse production Router profile: %v", err)
	}
	selected := make(map[string]bool, len(plan.Components))
	for _, component := range plan.Components {
		selected[component] = true
	}
	for _, component := range []string{
		"engine.loader", "engine.router", "admin.files", "admin.identity", "admin.router",
		"public.router", "public.form-upload",
	} {
		if !selected[component] {
			t.Errorf("production Router profile omitted %s: %v", component, plan.Components)
		}
	}
	for _, component := range []string{
		"development.fixtures", "engine.website", "engine.content", "engine.forms", "public.form-submit",
	} {
		if selected[component] {
			t.Errorf("Router profile unexpectedly selected %s: %v", component, plan.Components)
		}
	}
}

func TestDevelopmentFixturesRequireExplicitProfileSelection(t *testing.T) {
	input := "version: 1\nengines: [website, content, forms, loader]\nworkers: []\napis:\n  admin: [website, content, forms, files, identity]\n  public: []\ndevelopment_fixtures: true\n"
	plan, err := ParseBuildProfile(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	withoutIdentity := strings.Replace(input, "forms, files, identity", "forms, files", 1)
	if _, err := ParseBuildProfile(strings.NewReader(withoutIdentity)); err == nil {
		t.Fatal("development fixtures were accepted without the Admin Identity API needed to enter the UI")
	}
	components := make(map[string]bool, len(plan.Components))
	for _, component := range plan.Components {
		components[component] = true
	}
	if !components["development.fixtures"] {
		t.Fatalf("explicit development-fixture profile omitted its isolation marker: %v", plan.Components)
	}
	for _, component := range []string{"engine.website", "engine.content", "engine.forms", "admin.website", "admin.content", "admin.forms"} {
		if !components[component] {
			t.Errorf("development-fixture profile omitted %s: %v", component, plan.Components)
		}
	}

	production, err := ParseBuildProfile(strings.NewReader(strings.TrimSuffix(input, "development_fixtures: true\n")))
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range production.Components {
		if component == "development.fixtures" {
			t.Fatalf("fixture mode leaked into a profile without explicit opt-in: %v", production.Components)
		}
	}

	source, err := GenerateBuildProfileGoWithTag(plan, "main", "devfixtures")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "github.com/Zubrium/cdn/main/internal/devfixtures") {
		t.Fatalf("development fixture adapter was not linked into the explicit profile:\n%s", source)
	}
}
