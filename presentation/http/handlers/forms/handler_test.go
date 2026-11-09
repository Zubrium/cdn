package forms

import (
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmissionBranchFilterRejectsAmbiguousAndInvalidRequests(t *testing.T) {
	id := uuid.NewString()
	for _, query := range []string{"branchId=not-a-uuid", "branchId=" + id + "&environment=dev", "created_from=2026-11-05T00:00:00Z&created_to=2026-11-04T00:00:00Z", "sort=random", "cursor=bad"} {
		req := httptest.NewRequest("GET", "/?"+query, nil)
		if _, err := parseFilterWithLimits(httptest.NewRecorder(), req, true, 50, 200); err == nil {
			t.Fatalf("accepted invalid %q", query)
		}
	}
	filter, err := parseFilterWithLimits(httptest.NewRecorder(), httptest.NewRequest("GET", "/?branchId="+id+"&sort=asc", nil), true, 50, 200)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Environment.String() != id || filter.Sort != "asc" {
		t.Fatalf("lost branch filter: %+v", filter)
	}
	upperCase, err := parseFilterWithLimits(httptest.NewRecorder(), httptest.NewRequest("GET", "/?branchId="+strings.ToUpper(id)+"&environment="+id, nil), true, 50, 200)
	if err != nil {
		t.Fatal(err)
	}
	if upperCase.Environment.String() != id {
		t.Fatalf("branch ID should be normalized before filtering: %+v", upperCase)
	}
}

func TestSafeCSVNeutralizesSpreadsheetFormulasAfterWhitespace(t *testing.T) {
	for _, value := range []string{"=1+1", "+SUM(A1)", "-SUM(A1)", "@SUM(A1)", " =1+1", "\n=1+1", "\t=1+1", "\r@SUM(A1)"} {
		if got := safeCSV(value); got != "'"+value {
			t.Errorf("safeCSV(%q) = %q, want %q", value, got, "'"+value)
		}
	}
	for _, value := range []string{"ordinary text", "value =1+1", "  "} {
		if got := safeCSV(value); got != value {
			t.Errorf("safeCSV(%q) changed a non-formula value to %q", value, got)
		}
	}
}

func TestSubmissionListsFailClosedWithoutConfiguredPagination(t *testing.T) {
	handler := NewHandler(nil, nil, 1024)
	for _, route := range []func(http.ResponseWriter, *http.Request){handler.List, handler.Export} {
		response := httptest.NewRecorder()
		route(response, httptest.NewRequest("GET", "/?limit=1", nil))
		if response.Code != 500 {
			t.Fatalf("unconfigured submission list status = %d, want 500", response.Code)
		}
	}
}
