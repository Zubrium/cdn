package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestContentSnapshotsPaginationCoversMoreThan201(t *testing.T) {
	e := newTestEnv(t)
	_, err := e.pool.Exec(context.Background(), `INSERT INTO site_snapshots(id,name,digest,payload) SELECT gen_random_uuid(),'Snapshot '||n,repeat('0',64),'{}'::jsonb FROM generate_series(1,231) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var cursor string
	last := int64(1 << 62)
	for {
		path := "/api/v2/admin/content/snapshots?limit=17"
		if cursor != "" {
			path += "&beforeSequence=" + cursor
		}
		status, body, _ := e.request(http.MethodGet, path, e.adminToken, nil)
		if status != http.StatusOK {
			t.Fatalf("page status=%d body=%v", status, body)
		}
		items := body["items"].([]any)
		if len(items) > 17 {
			t.Fatalf("page too large: %d", len(items))
		}
		for _, raw := range items {
			item := raw.(map[string]any)
			id := item["id"].(string)
			sequence := int64(item["sequence"].(float64))
			if seen[id] || sequence >= last {
				t.Fatalf("duplicate or unordered item %s sequence %d", id, sequence)
			}
			seen[id] = true
			last = sequence
		}
		value, ok := body["nextCursor"].(float64)
		if !ok {
			break
		}
		cursor = fmt.Sprintf("%.0f", value)
	}
	if len(seen) != 231 {
		t.Fatalf("expected all 231 snapshots, got %d", len(seen))
	}
	status, _, _ := e.request(http.MethodGet, "/api/v2/admin/content/snapshots?beforeSequence=garbage", e.adminToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid cursor accepted: %d", status)
	}
}
