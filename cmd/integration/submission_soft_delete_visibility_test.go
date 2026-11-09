package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"websiteengine/pkg/environment"
)

// TestSubmissionSoftDeleteHidesDirectReads verifies that a tombstoned
// submission is no longer retrievable by ID even for an authenticated role.
// Its database is suite-owned and disposable; the helper refuses other names.
func TestSubmissionSoftDeleteHidesDirectReads(t *testing.T) {
	requireSubmissionSoftDeleteDatabase(t)
	env := newTestEnv(t)

	const formKey = "qa-soft-delete-visibility"
	manifest := testFeatureManifestFor(formKey, "1.0.0")
	manifest["form"] = map[string]any{"submissionSchema": objectSchema(map[string]any{
		"name": map[string]any{"type": "string"},
	}, "name")}
	uploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }\n")
	saveAndDeploySnapshot(t, env, singlePageConfig(formKey, "1.0.0"), "Submission soft-delete visibility")

	submission, err := env.forms.Submit(context.Background(), environment.Environment(string(env.devBranch)), formKey,
		json.RawMessage(`{"name":"Synthetic retained tombstone"}`))
	if err != nil {
		t.Fatalf("create synthetic submission: %v", err)
	}
	roles := roleRevisionTokens(t, env)
	path := "/api/v1/admin/submissions/" + submission.ID.String()
	for _, role := range roles {
		status, _, _ := env.request(http.MethodGet, path, role.token, nil)
		if status != http.StatusOK {
			t.Fatalf("active submission detail for %s: status=%d, want 200", role.role, status)
		}
	}

	status, body, _ := env.request(http.MethodDelete, path, env.adminToken, nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin soft-delete: status=%d body=%v", status, body)
	}
	for _, role := range roles {
		status, body, _ := env.request(http.MethodGet, path, role.token, nil)
		if status != http.StatusNotFound {
			t.Fatalf("deleted submission detail for %s: status=%d body=%v, want 404", role.role, status, body)
		}
		status, body, _ = env.request(http.MethodGet, "/api/v1/admin/submissions", role.token, nil)
		if status != http.StatusOK {
			t.Fatalf("submission list for %s after deletion: status=%d body=%v", role.role, status, body)
		}
		items, ok := body["items"].([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("deleted submission remained visible in list for %s: %v", role.role, body)
		}
	}

	var stored []byte
	var deleted bool
	err = env.pool.QueryRow(context.Background(), `SELECT data, deleted_at IS NOT NULL FROM form_submissions WHERE id=$1`, submission.ID).Scan(&stored, &deleted)
	if err != nil {
		t.Fatalf("read disposable soft-delete tombstone: %v", err)
	}
	if len(stored) == 0 || !deleted {
		t.Fatalf("soft delete must retain an inaccessible tombstone: data=%q deleted=%v", stored, deleted)
	}
}

func requireSubmissionSoftDeleteDatabase(t *testing.T) {
	t.Helper()
	rawURL := os.Getenv("TEST_DATABASE_URL")
	if rawURL == "" {
		return // newTestEnv skips before connecting when no database is configured.
	}
	parsed, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse submission soft-delete disposable database URL: %v", err)
	}
	const prefix = "website_engine_submission_soft_delete_"
	if !strings.HasPrefix(parsed.Database, prefix) || strings.TrimPrefix(parsed.Database, prefix) == "" {
		t.Skipf("submission soft-delete acceptance requires a unique disposable database with prefix %q; refusing to migrate or truncate %q", prefix, parsed.Database)
	}
}
