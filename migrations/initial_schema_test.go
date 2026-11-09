package migrations

import (
	"regexp"
	"strings"
	"testing"
)

var initialSchemaAlterStatement = regexp.MustCompile(`(?im)^\s*ALTER\b`)

func TestFreshInitialSchemaHasNoLegacyTablesOrAlterStatements(t *testing.T) {
	initial, err := FS.ReadFile("0001_init.sql")
	if err != nil {
		t.Fatalf("read fresh initial schema: %v", err)
	}
	if match := initialSchemaAlterStatement.Find(initial); match != nil {
		t.Fatalf("fresh initial schema must declare columns and constraints directly, found ALTER near %q", match)
	}
	for _, legacyTable := range []string{
		"CREATE TABLE public.site_snapshot_content",
		"CREATE TABLE public.feature_entries",
		"CREATE TABLE public.content_relations",
		"CREATE TABLE public.site_html_shells",
	} {
		if strings.Contains(string(initial), legacyTable) {
			t.Errorf("fresh initial schema contains retired legacy table declaration %q", legacyTable)
		}
	}
}
