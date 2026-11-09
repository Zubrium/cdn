package integration

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"websiteengine/features/forms"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
)

func TestBranchSubmissionFiltersHistoryAndExport(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.forms.SetSnapshotSchemaProvider(env.snapshots)
	var actor uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM access_tokens LIMIT 1`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	yes := true
	branches, err := env.snapshots.RegisterBranches(ctx, []config.BranchConfig{{Name: "Website", HTTPAddr: ":39991", CORSOrigins: []string{}, Indexable: &yes}, {Name: "Application", HTTPAddr: ":39992", CORSOrigins: []string{}, Indexable: &yes}})
	if err != nil {
		t.Fatal(err)
	}
	key := "branch-contacts"
	makeSnapshot := func(version string, schema map[string]any) uuid.UUID {
		t.Helper()
		manifest := testFeatureManifestFor(key, version)
		manifest["form"] = map[string]any{"submissionSchema": schema}
		uploadFeatureBuild(t, env, manifest, "export default function Feature(){}")
		raw, _ := json.Marshal(singlePageConfig(key, version))
		cfg, err := sitebuilder.ParseSiteConfig(raw)
		if err != nil {
			t.Fatal(err)
		}
		var actor uuid.UUID
		if err := env.pool.QueryRow(ctx, `SELECT id FROM access_tokens LIMIT 1`).Scan(&actor); err != nil {
			t.Fatal(err)
		}
		snapshot, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "Form " + version, Config: &cfg})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.ID
	}
	first := makeSnapshot("1.0.0", objectSchema(map[string]any{"name": map[string]any{"type": "string"}, "old": map[string]any{"type": "string"}, "photo": map[string]any{"type": "string", "format": "media"}}, "name"))
	second := makeSnapshot("1.0.1", objectSchema(map[string]any{"name": map[string]any{"type": "string"}, "email": map[string]any{"type": "string", "format": "email"}}, "name", "email"))
	for index, snapshotID := range []uuid.UUID{second, first} {
		_, err := env.snapshots.AssignBranch(ctx, actor, branches[index].ID, snapshotID, branches[index].Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Branch ordering is alphabetical: Application uses v2, Website uses v1.
	if _, err := env.forms.Submit(ctx, environment.Environment(branches[0].ID.String()), key, json.RawMessage(`{"name":"Missing email"}`)); err == nil {
		t.Fatal("submission required validation weakened")
	}
	if _, err := env.forms.Submit(ctx, environment.Environment(branches[1].ID.String()), key, json.RawMessage(`{"name":"A","photo":"`+uuid.NewString()+`"}`)); err == nil {
		t.Fatal("submission accepted a missing media object")
	}
	one, err := env.forms.Submit(ctx, environment.Environment(branches[1].ID.String()), key, json.RawMessage(`{"name":"A","old":"50%_literal"}`))
	if err != nil {
		t.Fatal(err)
	}
	two, err := env.forms.Submit(ctx, environment.Environment(branches[0].ID.String()), key, json.RawMessage(`{"name":"B","email":"b@example.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	// A submission waiting on a concurrent assignment must validate against
	// the newly committed snapshot, not the schema observed before the wait.
	tx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var ignored string
	if err := tx.QueryRow(ctx, `SELECT key FROM site_environments WHERE key=$1 FOR UPDATE`, branches[1].ID.String()).Scan(&ignored); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := env.forms.Submit(ctx, environment.Environment(branches[1].ID.String()), key, json.RawMessage(`{"name":"Racing"}`))
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := env.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%active_snapshot_id FROM site_environments%FOR SHARE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("submission did not wait for the branch assignment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.Exec(ctx, `UPDATE site_environments SET active_snapshot_id=$1,revision=revision+1 WHERE key=$2`, second, branches[1].ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, forms.ErrValidation) {
		t.Fatalf("stale schema accepted during assignment: %v", err)
	}
	if _, err := env.snapshots.AssignBranch(ctx, actor, branches[1].ID, first, branches[1].Revision+2); err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO media_objects(id,object_key,original_filename,content_type,size_bytes,status,variants,visibility) VALUES($1,$2,'qa.png','image/png',1,'ready','{}','public')`, fileID, "qa/"+fileID.String()); err != nil {
		t.Fatal(err)
	}
	deleteTx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteTx.Rollback(context.Background())
	if _, err := deleteTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_file_references'))`); err != nil {
		t.Fatal(err)
	}
	fileResult := make(chan error, 1)
	go func() {
		_, err := env.forms.Submit(ctx, environment.Environment(branches[1].ID.String()), key, json.RawMessage(`{"name":"Racing file","photo":"`+fileID.String()+`"}`))
		fileResult <- err
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := env.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%websiteengine_file_references%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("submission did not wait for file deletion lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := deleteTx.Exec(ctx, `DELETE FROM media_objects WHERE id=$1`, fileID); err != nil {
		t.Fatal(err)
	}
	if err := deleteTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-fileResult; !errors.Is(err, forms.ErrValidation) {
		t.Fatalf("deleted file accepted in submission: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE form_submissions SET created_at='2026-11-04T12:00:00Z' WHERE id=ANY($1::uuid[])`, []uuid.UUID{one.ID, two.ID}); err != nil {
		t.Fatal(err)
	}
	listPath := "/api/v1/admin/forms/" + key + "/submissions"
	status, firstPage, _ := env.request(http.MethodGet, listPath+"?limit=1&sort=asc", env.adminToken, nil)
	if status != 200 {
		t.Fatalf("first page %d %v", status, firstPage)
	}
	items := firstPage["items"].([]any)
	if len(items) != 1 {
		t.Fatal(firstPage)
	}
	firstID := items[0].(map[string]any)["id"]
	if firstID != one.ID.String() && firstID != two.ID.String() {
		t.Fatalf("first equal-timestamp row is unexpected: %v", firstPage)
	}
	// A submission inserted after page one must not move an existing row across
	// the cursor. The new row remains on the next page of an ascending walk.
	late, err := env.forms.Submit(ctx, environment.Environment(branches[1].ID.String()), key, json.RawMessage(`{"name":"Late"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE form_submissions SET created_at='2026-11-05T12:00:00Z' WHERE id=$1`, late.ID); err != nil {
		t.Fatal(err)
	}
	status, secondPage, _ := env.request(http.MethodGet, listPath+"?limit=1&sort=asc&cursor="+firstPage["next_cursor"].(string), env.adminToken, nil)
	if status != 200 || len(secondPage["items"].([]any)) != 1 || secondPage["items"].([]any)[0].(map[string]any)["id"] == firstID {
		t.Fatalf("unstable cursor: %d %v", status, secondPage)
	}
	secondID := secondPage["items"].([]any)[0].(map[string]any)["id"]
	if secondID == late.ID.String() {
		t.Fatalf("new row shifted into the next page: %v", secondPage)
	}
	status, thirdPage, _ := env.request(http.MethodGet, listPath+"?limit=1&sort=asc&cursor="+secondPage["next_cursor"].(string), env.adminToken, nil)
	if status != 200 || len(thirdPage["items"].([]any)) != 1 || thirdPage["items"].([]any)[0].(map[string]any)["id"] != late.ID.String() {
		t.Fatalf("new row should follow the original equal-timestamp rows: %d %v", status, thirdPage)
	}
	// Descending pagination must use the same deterministic timestamp/ID tuple.
	status, descFirst, _ := env.request(http.MethodGet, listPath+"?limit=1&sort=desc", env.adminToken, nil)
	if status != 200 || len(descFirst["items"].([]any)) != 1 || descFirst["items"].([]any)[0].(map[string]any)["id"] != late.ID.String() {
		t.Fatalf("descending first page: %d %v", status, descFirst)
	}
	status, descSecond, _ := env.request(http.MethodGet, listPath+"?limit=1&sort=desc&cursor="+descFirst["next_cursor"].(string), env.adminToken, nil)
	if status != 200 || len(descSecond["items"].([]any)) != 1 {
		t.Fatalf("descending second page: %d %v", status, descSecond)
	}
	status, descThird, _ := env.request(http.MethodGet, listPath+"?limit=1&sort=desc&cursor="+descSecond["next_cursor"].(string), env.adminToken, nil)
	if status != 200 || len(descThird["items"].([]any)) != 1 {
		t.Fatalf("descending third page: %d %v", status, descThird)
	}
	seen := map[any]bool{
		descFirst["items"].([]any)[0].(map[string]any)["id"]:  true,
		descSecond["items"].([]any)[0].(map[string]any)["id"]: true,
		descThird["items"].([]any)[0].(map[string]any)["id"]:  true,
	}
	if !seen[one.ID.String()] || !seen[two.ID.String()] || !seen[late.ID.String()] || len(seen) != 3 {
		t.Fatalf("descending cursor order repeated or skipped submissions: %v", seen)
	}
	query := "?branchId=" + strings.ToUpper(branches[1].ID.String()) + "&environment=" + branches[1].ID.String() + "&field=old&q=50%25_&sort=asc"
	status, filtered, _ := env.request(http.MethodGet, listPath+query, env.adminToken, nil)
	if status != 200 || len(filtered["items"].([]any)) != 1 {
		t.Fatalf("branch/literal search %d %v", status, filtered)
	}
	if filtered["items"].([]any)[0].(map[string]any)["branchId"] != branches[1].ID.String() {
		t.Fatal("branch identity missing")
	}
	// Disable v1's branch; historical fields must remain available.
	if _, err := env.snapshots.RegisterBranches(ctx, []config.BranchConfig{{Name: "Application", HTTPAddr: ":39992", CORSOrigins: []string{}, Indexable: &yes}}); err != nil {
		t.Fatal(err)
	}
	status, choices, _ := env.request(http.MethodGet, "/api/v1/admin/submission-branches", env.adminToken, nil)
	if status != http.StatusOK || len(choices["items"].([]any)) != 2 {
		t.Fatalf("submission branch choices: %d %v", status, choices)
	}
	foundRetiredBranch := false
	for _, rawBranch := range choices["items"].([]any) {
		branch := rawBranch.(map[string]any)
		if branch["id"] == branches[1].ID.String() {
			foundRetiredBranch = branch["name"] == "Website" && branch["enabled"] == false
		}
		if _, exposesPort := branch["httpAddr"]; exposesPort {
			t.Fatal("submission branch choices must not expose runtime configuration")
		}
	}
	if !foundRetiredBranch {
		t.Fatal("disabled branch with historical submissions must remain selectable by name")
	}
	status, filtered, _ = env.request(http.MethodGet, listPath+query, env.adminToken, nil)
	if status != 200 || len(filtered["items"].([]any)) != 1 {
		t.Fatalf("retired branch lost schema %d %v", status, filtered)
	}
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+listPath+"/export"+query, nil)
	req.Header.Set("Authorization", "Bearer "+env.adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || resp.StatusCode != 200 || len(rows) != 2 {
		t.Fatalf("filtered CSV %d %s %v", resp.StatusCode, raw, err)
	}
	if !strings.Contains(string(raw), "branchId") || !strings.Contains(string(raw), one.ID.String()) || strings.Contains(string(raw), two.ID.String()) {
		t.Fatalf("CSV filters differ: %s", raw)
	}

	// Period filters use [from,to), and status/archive filters remain identical
	// for the list and CSV endpoints.
	if _, err := env.pool.Exec(ctx, `UPDATE form_submissions SET created_at=CASE WHEN id=$1 THEN $3::timestamptz ELSE $4::timestamptz END WHERE id=ANY($2::uuid[])`, one.ID, []uuid.UUID{one.ID, two.ID}, "2026-11-04T12:00:00Z", "2026-11-04T13:00:00Z"); err != nil {
		t.Fatal(err)
	}
	status, _, _ = env.request(http.MethodPatch, "/api/v1/admin/submissions/"+one.ID.String()+"/status", env.adminToken, map[string]any{"status": "processed"})
	if status != http.StatusOK {
		t.Fatalf("update submission status: %d", status)
	}
	period := "&created_from=2026-11-04T12:00:00Z&created_to=2026-11-04T13:00:00Z&sort=asc"
	status, periodResult, _ := env.request(http.MethodGet, listPath+"?status=processed"+period, env.adminToken, nil)
	if status != http.StatusOK || len(periodResult["items"].([]any)) != 1 || periodResult["items"].([]any)[0].(map[string]any)["id"] != one.ID.String() {
		t.Fatalf("status and half-open period filters: %d %v", status, periodResult)
	}
	status, periodResult, _ = env.request(http.MethodGet, listPath+"?status=new"+period, env.adminToken, nil)
	if status != http.StatusOK || len(periodResult["items"].([]any)) != 0 {
		t.Fatalf("status filter: %d %v", status, periodResult)
	}
	status, _, _ = env.request(http.MethodPost, "/api/v1/admin/submissions/"+one.ID.String()+"/archive", env.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("archive submission: %d", status)
	}
	status, periodResult, _ = env.request(http.MethodGet, listPath+"?status=processed"+period, env.adminToken, nil)
	if status != http.StatusOK || len(periodResult["items"].([]any)) != 0 {
		t.Fatalf("archived submission leaked into default list: %d %v", status, periodResult)
	}
	includeArchived := "&include_archived=true" + period
	status, periodResult, _ = env.request(http.MethodGet, listPath+"?status=processed"+includeArchived, env.adminToken, nil)
	if status != http.StatusOK || len(periodResult["items"].([]any)) != 1 {
		t.Fatalf("include_archived filter: %d %v", status, periodResult)
	}
	archiveReq, _ := http.NewRequest(http.MethodGet, env.server.URL+listPath+"/export?status=processed"+includeArchived, nil)
	archiveReq.Header.Set("Authorization", "Bearer "+env.adminToken)
	archiveResp, err := http.DefaultClient.Do(archiveReq)
	if err != nil {
		t.Fatal(err)
	}
	defer archiveResp.Body.Close()
	archiveCSV, _ := io.ReadAll(archiveResp.Body)
	archiveRows, err := csv.NewReader(strings.NewReader(string(archiveCSV))).ReadAll()
	if err != nil || archiveResp.StatusCode != http.StatusOK || len(archiveRows) != 2 || archiveRows[1][0] != one.ID.String() {
		t.Fatalf("filtered archived CSV: %d %s %v", archiveResp.StatusCode, archiveCSV, err)
	}
}

func TestAllFormsListSearchAndExportShareFilters(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.forms.SetSnapshotSchemaProvider(env.snapshots)
	var actor uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT id FROM access_tokens LIMIT 1`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	indexable := true
	branches, err := env.snapshots.RegisterBranches(ctx, []config.BranchConfig{{Name: "First", HTTPAddr: ":39981", CORSOrigins: []string{}, Indexable: &indexable}, {Name: "Second", HTTPAddr: ":39982", CORSOrigins: []string{}, Indexable: &indexable}})
	if err != nil {
		t.Fatal(err)
	}
	specs := []struct{ key, field string }{{"all-contacts", "email"}, {"all-support", "topic"}}
	for _, spec := range specs {
		manifest := testFeatureManifestFor(spec.key, "1.0.0")
		manifest["form"] = map[string]any{"submissionSchema": objectSchema(map[string]any{"name": map[string]any{"type": "string"}, spec.field: map[string]any{"type": "string"}}, "name", spec.field)}
		uploadFeatureBuild(t, env, manifest, "export default function Feature(){}")
	}
	raw, _ := json.Marshal(singlePageConfig("all-contacts", "1.0.0"))
	cfg, err := sitebuilder.ParseSiteConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Features = []sitebuilder.BuildRef{{FeatureKey: "all-contacts", Version: "1.0.0"}, {FeatureKey: "all-support", Version: "1.0.0"}}
	snapshot, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{Name: "All forms", Config: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range branches {
		if _, err := env.snapshots.AssignBranch(ctx, actor, branch.ID, snapshot.ID, branch.Revision); err != nil {
			t.Fatal(err)
		}
	}
	first := environment.Environment(branches[0].ID.String())
	second := environment.Environment(branches[1].ID.String())
	records := []struct {
		branch    environment.Environment
		key, data string
	}{{first, "all-contacts", `{"name":"А","email":"one@example.test"}`}, {first, "all-support", `{"name":"Б","topic":"Возврат"}`}, {second, "all-support", `{"name":"В","topic":"Доставка"}`}}
	for _, record := range records {
		if _, err := env.forms.Submit(ctx, record.branch, record.key, json.RawMessage(record.data)); err != nil {
			t.Fatal(err)
		}
	}
	status, result, _ := env.request(http.MethodGet, "/api/v1/admin/submissions?sort=asc", env.adminToken, nil)
	if status != 200 || len(result["items"].([]any)) != 3 {
		t.Fatalf("all forms list: %d %v", status, result)
	}
	fields := map[string]bool{}
	for _, raw := range result["fields"].([]any) {
		fields[raw.(map[string]any)["key"].(string)] = true
	}
	if !fields["email"] || !fields["topic"] || !fields["name"] {
		t.Fatalf("all-form fields: %v", fields)
	}
	// Same field, branch and search filters must apply to both table and CSV.
	query := "?branchId=" + branches[0].ID.String() + "&field=topic&q=" + url.QueryEscape("Возврат")
	status, result, _ = env.request(http.MethodGet, "/api/v1/admin/submissions"+query, env.adminToken, nil)
	if status != 200 || len(result["items"].([]any)) != 1 || result["items"].([]any)[0].(map[string]any)["feature_key"] != "all-support" {
		t.Fatalf("filtered all forms list: %d %v", status, result)
	}
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/api/v1/admin/submissions/export"+query, nil)
	req.Header.Set("Authorization", "Bearer "+env.adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	csvRows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || resp.StatusCode != 200 || len(csvRows) != 2 || !strings.Contains(string(body), "all-support") || strings.Contains(string(body), "all-contacts") {
		t.Fatalf("filtered all forms export: %d %s %v", resp.StatusCode, body, err)
	}
	if err := env.forms.SetLimits(200, 1); err != nil {
		t.Fatal(err)
	}
	status, limited, _ := env.request(http.MethodGet, "/api/v1/admin/submissions/export?sort=asc", env.adminToken, nil)
	exportError, _ := limited["error"].(map[string]any)
	if status != http.StatusRequestEntityTooLarge || exportError["code"] != "export_limit_exceeded" {
		t.Fatalf("export limit must reject rather than silently truncate: %d %v", status, limited)
	}
	status, _, _ = env.request(http.MethodGet, "/api/v1/admin/submissions", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous submissions listing should be denied: %d", status)
	}
	status, tokenResult, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken,
		map[string]any{"name": "submission operator", "role": "user"})
	if status != http.StatusCreated {
		t.Fatalf("create submission operator: %d %v", status, tokenResult)
	}
	userToken := tokenResult["token"].(string)
	status, userBranches, _ := env.request(http.MethodGet, "/api/v1/admin/submission-branches", userToken, nil)
	if status != http.StatusOK || len(userBranches["items"].([]any)) != 2 {
		t.Fatalf("user role branch choices: %d %v", status, userBranches)
	}
	for _, rawBranch := range userBranches["items"].([]any) {
		if _, exposesPort := rawBranch.(map[string]any)["httpAddr"]; exposesPort {
			t.Fatal("submission branch choices must not expose runtime configuration")
		}
	}
	matchedID := result["items"].([]any)[0].(map[string]any)["id"].(string)
	status, userList, _ := env.request(http.MethodGet, "/api/v1/admin/submissions"+query, userToken, nil)
	if status != http.StatusOK || len(userList["items"].([]any)) != 1 || userList["items"].([]any)[0].(map[string]any)["id"] != matchedID {
		t.Fatalf("user role list with filters: %d %v", status, userList)
	}
	exportReq, _ := http.NewRequest(http.MethodGet, env.server.URL+"/api/v1/admin/submissions/export"+query, nil)
	exportReq.Header.Set("Authorization", "Bearer "+userToken)
	exportResp, err := http.DefaultClient.Do(exportReq)
	if err != nil {
		t.Fatal(err)
	}
	userCSV, _ := io.ReadAll(exportResp.Body)
	exportResp.Body.Close()
	userRows, err := csv.NewReader(strings.NewReader(string(userCSV))).ReadAll()
	if err != nil || exportResp.StatusCode != http.StatusOK || len(userRows) != 2 {
		t.Fatalf("user role export with filters: %d %s %v", exportResp.StatusCode, userCSV, err)
	}
	status, beforeDeniedMutation, _ := env.request(http.MethodGet, "/api/v1/admin/submissions/"+matchedID, userToken, nil)
	if status != http.StatusOK {
		t.Fatalf("user role read before denied mutation: %d %v", status, beforeDeniedMutation)
	}
	originalStatus := beforeDeniedMutation["status"]
	status, _, _ = env.request(http.MethodPatch, "/api/v1/admin/submissions/"+matchedID+"/status", userToken, map[string]any{"status": "in_progress"})
	if status != http.StatusForbidden {
		t.Fatalf("user role status update must be forbidden: %d", status)
	}
	status, _, _ = env.request(http.MethodPost, "/api/v1/admin/submissions/"+matchedID+"/archive", userToken, nil)
	if status != http.StatusForbidden {
		t.Fatalf("user role archive must be forbidden: %d", status)
	}
	status, afterDeniedMutation, _ := env.request(http.MethodGet, "/api/v1/admin/submissions/"+matchedID, userToken, nil)
	if status != http.StatusOK || afterDeniedMutation["status"] != originalStatus || afterDeniedMutation["archived_at"] != nil {
		t.Fatalf("forbidden user mutations must not change submission: %d before=%v after=%v", status, beforeDeniedMutation, afterDeniedMutation)
	}
	status, _, _ = env.request(http.MethodDelete, "/api/v1/admin/submissions/"+matchedID, userToken, nil)
	if status != http.StatusForbidden {
		t.Fatalf("user role must not hard-delete submissions: %d", status)
	}
}
