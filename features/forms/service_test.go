package forms

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSubmissionSearchUsesEverySchemaFieldAndLiteralWildcards(t *testing.T) {
	fields := []FieldDefinition{}
	for i := range 14 {
		fields = append(fields, FieldDefinition{Key: fmt.Sprintf("field%d", i)})
	}
	selected, err := chooseSearchFields(fields, "")
	if err != nil || len(selected) != 14 {
		t.Fatalf("search silently dropped fields: %v %v", selected, err)
	}
	if _, err := chooseSearchFields(fields, "unknown"); err == nil {
		t.Fatal("unknown search field accepted")
	}
	if got := escapeLikeSearch(`50%_\`); got != `50\%\_\\` {
		t.Fatalf("wildcards not literal: %q", got)
	}
}
func TestSubmissionJSONExposesBranchIdentity(t *testing.T) {
	raw, err := json.Marshal(Submission{Environment: "branch-id"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"branchId":"branch-id"`) || !strings.Contains(string(raw), `"environment":"branch-id"`) {
		t.Fatalf("missing branch contract: %s", raw)
	}
}

func TestSubmissionLimitsRejectExportIncrementOverflow(t *testing.T) {
	service := NewService(nil, nil)
	if err := service.SetLimits(50, int(^uint(0)>>1)); err == nil {
		t.Fatal("export row limit must leave room for the overflow probe row")
	}
}
