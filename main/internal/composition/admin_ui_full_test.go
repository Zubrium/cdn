//go:build full && !devfixtures && !router

package composition

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFullProfileAdminUIComposition(t *testing.T) {
	plan := BuildPlan()
	selected := make(map[string]struct{}, len(plan.Components))
	for _, id := range plan.Components {
		selected[id] = struct{}{}
	}
	if _, enabled := selected["admin.ui"]; !enabled {
		t.Fatal("full profile plan does not select admin.ui")
	}
	if err := validateRunnableComposition(selected); err != nil {
		t.Fatalf("full profile runtime composition: %v", err)
	}

	handler, err := newAdminUIHandler()
	if err != nil {
		t.Fatalf("create Admin UI handler: %v", err)
	}
	if handler == nil {
		t.Fatal("full profile did not provide an Admin UI handler")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /admin/ status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `<div id="root"></div>`) {
		t.Fatalf("GET /admin/ did not return the Admin SPA: %q", body)
	}
	const assetPrefix = `src="/admin/assets/`
	start := strings.Index(body, assetPrefix)
	if start < 0 {
		t.Fatalf("GET /admin/ has no embedded asset reference: %q", body)
	}
	start += len(`src="`)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("GET /admin/ has a malformed asset reference: %q", body)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, body[start:start+end], nil))
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
		t.Fatalf("GET %s status = %d, body length = %d; want an embedded asset", body[start:start+end], asset.Code, asset.Body.Len())
	}
}
