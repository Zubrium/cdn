package coordinator

import (
	"go/parser"
	"go/token"
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
	plan, err := ParseBuildProfile(strings.NewReader("version: 1\nengines: [loader]\nworkers: []\napis:\n  admin: [files]\n  public: []\nautomation: []\n"))
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
	first := "version: 1\nengines: [router, loader]\nworkers: []\napis:\n  admin: [files]\n  public: [router]\nautomation: []\n"
	second := "apis:\n  public: [router]\n  admin: [files]\nengines: [loader, router]\nworkers: []\nautomation: []\nversion: 1\n"
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
	for _, edge := range [][2]string{{"core", "coordinator"}, {"coordinator", "engine.loader"}, {"engine.loader", "admin.files"}, {"engine.router", "public.router"}} {
		if _, ok := positions[edge[0]]; !ok {
			t.Fatalf("missing %s", edge[0])
		}
		if _, ok := positions[edge[1]]; !ok || positions[edge[0]] >= positions[edge[1]] {
			t.Fatalf("dependency order %v violated: %v", edge, a.Components)
		}
	}
}
