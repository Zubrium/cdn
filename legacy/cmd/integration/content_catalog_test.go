package integration

import (
	"net/http"
	"testing"
)

func TestGlobalContentCatalogIsRemovedInSnapshotModel(t *testing.T) {
	env := newTestEnv(t)
	status, body, _ := env.request(http.MethodGet, "/api/v2/admin/content/features", env.adminToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("global content catalog bypasses snapshot selection: status=%d body=%v", status, body)
	}
}
