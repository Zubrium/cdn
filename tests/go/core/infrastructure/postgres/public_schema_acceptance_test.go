package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// A clean install creates bounded context schemas described by SPEC-core-architecture.
func TestInitialAtlasSchemaCreatesBoundedContextTables(t *testing.T) {
	schema := strings.ToLower(initialSchema(t))
	for _, table := range coreSchemaTables {
		pattern := regexp.MustCompile(`(?m)\bcreate\s+table\s+` + regexp.QuoteMeta(table) + `\s*\(`)
		if !pattern.MatchString(schema) {
			t.Errorf("clean Atlas baseline does not create %s", table)
		}
	}
	if regexp.MustCompile(`(?m)\bcreate\s+(?:table|sequence)\s+(?:core\.)`).MatchString(schema) {
		t.Error("clean Atlas baseline unexpectedly creates objects in retired core schema")
	}
}
