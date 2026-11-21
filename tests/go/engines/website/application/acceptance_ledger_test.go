//go:build website_acceptance
// +build website_acceptance

package application_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var websiteScenarioIDs = map[string]struct{}{
	"WEB-001": {},
	"WEB-002": {},
	"WEB-003": {},
	"WEB-004": {},
	"WEB-005": {},
	"WEB-006": {},
	"WEB-007": {},
	"WEB-008": {},
	"WEB-009": {},
	"WEB-010": {},
	"WEB-011": {},
	"WEB-012": {},
	"WEB-013": {},
	"WEB-014": {},
	"WEB-015": {},
	"WEB-016": {},
	"WEB-017": {},
	"WEB-018": {},
	"WEB-019": {},
	"WEB-020": {},
	"WEB-021": {},
	"WEB-022": {},
	"WEB-023": {},
	"WEB-024": {},
	"WEB-025": {},
	"WEB-026": {},
	"WEB-027": {},
	"WEB-028": {},
	"WEB-029": {},
}

type websiteScenarioLedger struct {
	Scenarios []websiteScenario `json:"scenarios"`
}

type websiteScenario struct {
	ID             string `json:"id"`
	TestFile       string `json:"test_file"`
	TestSymbol     string `json:"test_symbol"`
	CoverageStatus string `json:"coverage_status"`
}

func TestWebsiteScenarioLedger(t *testing.T) {
	root := websiteRepositoryRoot(t)
	ledgerPath := filepath.Join(root, "tests", "qa", "website-scenarios.json")
	contents, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read Website scenario ledger: %v", err)
	}

	var ledger websiteScenarioLedger
	if err := json.Unmarshal(contents, &ledger); err != nil {
		t.Fatalf("decode Website scenario ledger: %v", err)
	}
	if len(ledger.Scenarios) == 0 {
		t.Fatal("Website scenario ledger is empty")
	}

	scenariosByID := make(map[string]websiteScenario, len(ledger.Scenarios))
	for _, scenario := range ledger.Scenarios {
		if scenario.ID == "" {
			t.Fatal("Website scenario ledger contains an empty ID")
		}
		if _, exists := scenariosByID[scenario.ID]; exists {
			t.Fatalf("Website scenario ledger duplicates %s", scenario.ID)
		}
		scenariosByID[scenario.ID] = scenario
	}

	ledgerIDs := sortedWebsiteScenarioIDs(scenariosByID)
	registeredIDs := sortedWebsiteScenarioSet(websiteScenarioIDs)
	if strings.Join(ledgerIDs, ",") != strings.Join(registeredIDs, ",") {
		t.Fatalf("ledger IDs = %v, registered acceptance IDs = %v", ledgerIDs, registeredIDs)
	}

	for _, scenarioID := range ledgerIDs {
		scenario := scenariosByID[scenarioID]
		t.Run(scenarioID, func(t *testing.T) {
			websiteAssertLedgerTestReference(t, root, scenario)
		})
	}
}

func TestWebsiteScenarioLedgerReferenceRequiresExactSubtest(t *testing.T) {
	source := []byte(`
package application_test

func TestExpected(t *testing.T) {
	t.Run("OTHER-001", func(t *testing.T) {})
}

func TestOther(t *testing.T) {
	t.Run("WEB-001", func(t *testing.T) {})
}

func TestFakeReceiver(t *testing.T) {
	fake.Run("WEB-002", func(t *testing.T) {})
}

func TestShadowedParameter(t *testing.T) {
	{
		t := fake
		t.Run("WEB-003", func(t *testing.T) {})
	}
}
`)
	if websiteSourceContainsSubtest(source, "TestExpected", "WEB-001") {
		t.Fatal("accepted a subtest ID declared by another test function")
	}
	if !websiteSourceContainsSubtest(source, "TestOther", "WEB-001") {
		t.Fatal("did not recognize a subtest declared by its test function")
	}
	if websiteSourceContainsSubtest(source, "TestFakeReceiver", "WEB-002") {
		t.Fatal("accepted a subtest declared by a fake receiver")
	}
	if websiteSourceContainsSubtest(source, "TestShadowedParameter", "WEB-003") {
		t.Fatal("accepted a subtest declared by a shadowed parameter")
	}
}

func websiteAssertLedgerTestReference(t *testing.T, root string, scenario websiteScenario) {
	t.Helper()
	if scenario.TestFile == "" || filepath.IsAbs(scenario.TestFile) || strings.Contains(scenario.TestFile, "..") {
		t.Fatalf("%s has invalid test_file %q", scenario.ID, scenario.TestFile)
	}
	functionName, subtestID, ok := strings.Cut(scenario.TestSymbol, "/")
	if !ok || functionName == "" || subtestID != scenario.ID {
		t.Fatalf("%s has invalid test_symbol %q", scenario.ID, scenario.TestSymbol)
	}

	testPath := filepath.Join(root, filepath.FromSlash(scenario.TestFile))
	source, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("%s read %s: %v", scenario.ID, scenario.TestFile, err)
	}
	functionPattern := regexp.MustCompile(`(?m)^func\s+` + regexp.QuoteMeta(functionName) + `\s*\(`)
	if !functionPattern.Match(source) {
		t.Fatalf("%s test file %s does not declare %s", scenario.ID, scenario.TestFile, functionName)
	}
	if !strings.Contains(string(source), `"`+scenario.ID+`"`) {
		t.Fatalf("%s test file %s does not declare its subtest ID", scenario.ID, scenario.TestFile)
	}
	if scenario.CoverageStatus == "covered" && !websiteSourceContainsSubtest(source, functionName, scenario.ID) {
		t.Fatalf("%s covered test symbol %s does not declare its exact subtest", scenario.ID, scenario.TestSymbol)
	}
}

func websiteSourceContainsSubtest(source []byte, functionName, subtestID string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "ledger_reference.go", source, 0)
	if err != nil {
		return false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName || function.Body == nil {
			continue
		}
		testingParameters := websiteTestingTParameters(function)
		if len(testingParameters) == 0 {
			return false
		}

		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if found {
				return false
			}
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Run" || len(call.Args) == 0 {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			if _, ok := testingParameters[receiver.Obj]; !ok {
				return true
			}
			label, ok := call.Args[0].(*ast.BasicLit)
			if !ok || label.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(label.Value)
			if err == nil && value == subtestID {
				found = true
			}
			return true
		})
		return found
	}
	return false
}

func websiteTestingTParameters(function *ast.FuncDecl) map[*ast.Object]struct{} {
	parameters := make(map[*ast.Object]struct{})
	if function.Type.Params == nil {
		return parameters
	}
	for _, parameter := range function.Type.Params.List {
		pointer, ok := parameter.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		selector, ok := pointer.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "T" {
			continue
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok || packageName.Name != "testing" {
			continue
		}
		for _, name := range parameter.Names {
			if name.Obj != nil {
				parameters[name.Obj] = struct{}{}
			}
		}
	}
	return parameters
}

func websiteRepositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if info, statErr := os.Stat(filepath.Join(current, "go.work")); statErr == nil && !info.IsDir() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("could not find repository go.work")
		}
		current = parent
	}
}

func sortedWebsiteScenarioIDs(scenarios map[string]websiteScenario) []string {
	ids := make([]string, 0, len(scenarios))
	for id := range scenarios {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedWebsiteScenarioSet(scenarios map[string]struct{}) []string {
	ids := make([]string, 0, len(scenarios))
	for id := range scenarios {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
