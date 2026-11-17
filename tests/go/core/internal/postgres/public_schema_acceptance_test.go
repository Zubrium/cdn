package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// A clean install creates the isolated Core schema described by SPEC-core-v2.
func TestInitialAtlasSchemaCreatesCoreTablesInCore(t *testing.T) {
	schema := strings.ToLower(initialSchema(t))
	for _, table := range coreSchemaTables {
		pattern := regexp.MustCompile(`(?m)\bcreate\s+table\s+core\.` + regexp.QuoteMeta(table) + `\s*\(`)
		if !pattern.MatchString(schema) {
			t.Errorf("clean Atlas baseline does not create core.%s", table)
		}
	}
	if regexp.MustCompile(`(?m)\bcreate\s+(?:table|sequence)\s+public\.`).MatchString(schema) {
		t.Error("clean Atlas baseline unexpectedly creates application objects in public")
	}
}
