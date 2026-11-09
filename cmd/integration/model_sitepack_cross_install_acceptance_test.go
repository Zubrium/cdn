package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/files"
	featuremedia "websiteengine/features/media"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/environment"
	s3store "websiteengine/pkg/storage/s3"
	httptransport "websiteengine/presentation/http"
	hfiles "websiteengine/presentation/http/handlers/files"
	hmedia "websiteengine/presentation/http/handlers/media"
	hsitebuilder "websiteengine/presentation/http/handlers/sitebuilder"
)

// TestModelSitepackCrossInstallAcceptance exercises the model-backed
// Sitepack contract across independent source and target databases. It uses
// the current Vitasil v2 catalog, detail, and homepage ZIPs, and requires a
// task-owned local MinIO bucket for target storage. It never targets the live
// Vitasil database, storage, or product stack.
func TestModelSitepackCrossInstallAcceptance(t *testing.T) {
	sourceURL := strings.TrimSpace(os.Getenv("TEST_SITEPACK_SOURCE_DATABASE_URL"))
	targetURL := strings.TrimSpace(os.Getenv("TEST_SITEPACK_TARGET_DATABASE_URL"))
	endpoint := strings.TrimSpace(os.Getenv("TEST_SITEPACK_S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("TEST_SITEPACK_S3_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("TEST_SITEPACK_S3_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("TEST_SITEPACK_S3_SECRET_KEY"))
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	if sourceURL == "" || targetURL == "" || endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" || featureDir == "" {
		t.Skip("set unique TEST_SITEPACK_SOURCE_DATABASE_URL, TEST_SITEPACK_TARGET_DATABASE_URL, TEST_SITEPACK_S3_* and TEST_FEATURE_DIR for cross-install acceptance")
	}

	sourceName := requireSitepackCrossInstallDatabase(t, sourceURL, "website_engine_sitepack_cross_install_qa_src")
	targetName := requireSitepackCrossInstallDatabase(t, targetURL, "website_engine_sitepack_cross_install_qa_dst")
	if sourceName == targetName || sourceURL == targetURL {
		t.Fatal("source and target must be separate disposable PostgreSQL databases")
	}
	requireSitepackCrossInstallMinIO(t, endpoint, bucket, accessKey, secretKey)
	// newTestEnv closes its server and pool during cleanup. Register the idle
	// checks first so they run after those cleanup callbacks.
	t.Cleanup(func() {
		assertSitepackCrossInstallDatabaseIdle(t, sourceURL, sourceName)
		assertSitepackCrossInstallDatabaseIdle(t, targetURL, targetName)
	})

	featureTemp := filepath.Join(t.TempDir(), "vitasil-v2-feature-zips")
	if err := os.MkdirAll(featureTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	archives := map[string][]byte{}
	manifests := map[string]sitebuilder.Manifest{}
	for _, key := range []string{"catalog", "catalog-detail", "main-data"} {
		archive := modelAcceptanceReadArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s ZIP: %v", key, err)
		}
		if verified.Manifest.FormatVersion != 2 || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule || verified.Manifest.DataInputs == nil {
			t.Fatalf("expected current Vitasil v2 typed data-input build for %s, got format=%d ABI=%q", key, verified.Manifest.FormatVersion, verified.Manifest.RuntimeABI())
		}
		if err := os.WriteFile(filepath.Join(featureTemp, key+".zip"), archive, 0o600); err != nil {
			t.Fatalf("stage Vitasil %s ZIP in unique temporary directory: %v", key, err)
		}
		archives[key], manifests[key] = archive, verified.Manifest
		t.Logf("staged current Vitasil v2 build %s@%s into %s", key, verified.Manifest.Version, featureTemp)
	}

	t.Setenv("TEST_DATABASE_URL", sourceURL)
	source := newTestEnv(t)
	ctx := context.Background()
	sourceRenditionMedia := featuremedia.NewServiceWithProcessor(featuremedia.NewRepository(source.pool), source.store, 20<<20, &sitepackCrossInstallRenditionProcessor{})
	configureMediaRenditionWorker(t, sourceRenditionMedia)
	source.snapshots.SetMediaService(sourceRenditionMedia)
	for _, key := range []string{"catalog", "catalog-detail", "main-data"} {
		status, body, _ := source.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", source.adminToken, bytes.NewReader(archives[key]), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register Vitasil source build %s@%s: status=%d body=%v", key, manifests[key].Version, status, body)
		}
	}

	model, configMap, pathKeys, ids := sitepackCrossInstallVitasilConfig(t, manifests)
	privatePNG := makeFileMediaPNG(t, color.RGBA{R: 180, G: 40, B: 50, A: 255})
	publicPNG := makeFileMediaPNG(t, color.RGBA{R: 30, G: 120, B: 220, A: 255})
	sourceFiles, err := files.NewService(source.pool, source.store, sitepackCrossInstallFilesConfig(t, "http://127.0.0.1:1", t.TempDir()))
	if err != nil {
		t.Fatalf("create source file service: %v", err)
	}
	if err := sourceFiles.InitOwners(ctx); err != nil {
		t.Fatalf("initialize source file owner: %v", err)
	}
	sourceFiles.SetReferenceManager(source.snapshots)
	privateFile, err := sourceFiles.UploadStream(ctx, sourceFiles.AdminOwner(), "sitepack-private.png", int64(len(privatePNG)), "private", bytes.NewReader(privatePNG))
	if err != nil {
		t.Fatalf("upload source private media: %v", err)
	}
	publicFile, err := sourceFiles.UploadStream(ctx, sourceFiles.AdminOwner(), "sitepack-public.png", int64(len(publicPNG)), "public", bytes.NewReader(publicPNG))
	if err != nil {
		t.Fatalf("upload source public media: %v", err)
	}

	parent := modelAcceptanceSaveAndDeploySnapshot(t, source, configMap, "Sitepack source parent")
	parentID := uuid.MustParse(parent["id"].(string))
	parentBase := "/api/v2/admin/content/snapshots/" + parentID.String() + "/model"
	records := sitepackCrossInstallInitialRecords(ids, pathKeys, publicFile.ID.String())
	status, firstDraft, _ := source.request(http.MethodPut, parentBase, source.adminToken,
		sitebuilder.ModelContentDraftInput{Revision: 0, Records: records})
	if status != http.StatusOK {
		t.Fatalf("save initial Vitasil model records: status=%d body=%v", status, firstDraft)
	}
	parentRecords := sitepackCrossInstallDecodeRecords(t, firstDraft["records"])
	parentRecordIDs, categoryRecordID := sitepackCrossInstallRecordIDs(t, parentRecords, ids, pathKeys)
	for i := range parentRecords {
		if parentRecords[i].ID == categoryRecordID {
			parentRecords[i].Relations = map[string][]string{ids["relation.category_products"]: []string{parentRecordIDs["product:pear"], parentRecordIDs["product:apple"]}}
		}
	}
	parentSelections := sitepackCrossInstallSelections(parentRecordIDs)
	status, parentDraft, _ := source.request(http.MethodPut, parentBase, source.adminToken,
		sitebuilder.ModelContentDraftInput{Revision: 1, Records: parentRecords, Selections: parentSelections})
	if status != http.StatusOK || parentDraft["revision"] != float64(2) {
		t.Fatalf("save Vitasil model relations and selections: status=%d body=%v", status, parentDraft)
	}
	sitepackCrossInstallPrepareRenditions(t, ctx, sourceRenditionMedia, manifests, []uuid.UUID{privateFile.ID, publicFile.ID})
	status, parentPublished, _ := source.request(http.MethodPost, parentBase+"/publish", source.adminToken, map[string]any{"revision": 2})
	if status != http.StatusOK || parentPublished["publishedRevision"] != float64(2) {
		t.Fatalf("publish source parent records: status=%d body=%v", status, parentPublished)
	}

	child := modelAcceptanceCreateChildSnapshot(t, source, parentID.String(), "Sitepack source export")
	childID := uuid.MustParse(child["id"].(string))
	childBase := "/api/v2/admin/content/snapshots/" + childID.String() + "/model"
	status, childWorkspace, _ := source.request(http.MethodGet, childBase, source.adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read copied child model workspace: status=%d body=%v", status, childWorkspace)
	}
	childRecords := sitepackCrossInstallDecodeRecords(t, childWorkspace["records"])
	childSelections := sitepackCrossInstallDecodeSelections(t, childWorkspace["selections"])
	childRecordIDs, _ := sitepackCrossInstallRecordIDs(t, childRecords, ids, pathKeys)
	for i := range childRecords {
		if childRecords[i].EntityID != ids["entity.product"] {
			continue
		}
		if childRecords[i].Values[pathKeys["product:/slug"]] == "pear" {
			childRecords[i].Values[pathKeys["product:/title"]] = "Груша: черновик ребёнка"
			childRecords[i].Values[pathKeys["product:/imageSrc"]] = privateFile.ID.String()
		}
	}
	childSelections = sitepackCrossInstallSetSelection(childSelections, "catalog-products", "products", "ids", []string{childRecordIDs["product:pear"], childRecordIDs["product:apple"]})
	status, childDraft, _ := source.request(http.MethodPut, childBase, source.adminToken,
		sitebuilder.ModelContentDraftInput{Revision: 2, Records: childRecords, Selections: childSelections})
	if status != http.StatusOK || childDraft["revision"] != float64(3) || childDraft["publishedRevision"] != float64(2) {
		t.Fatalf("save independent child draft: status=%d body=%v", status, childDraft)
	}

	parentBefore := sitepackCrossInstallStateSignature(t, source.pool, parentID)
	childBefore := sitepackCrossInstallStateSignature(t, source.pool, childID)
	branchBefore := sitepackCrossInstallBranchAssignment(t, source.pool, source.devBranch)
	if branchBefore == nil || *branchBefore != parentID {
		t.Fatalf("source branch should remain assigned to its published parent, got %v", branchBefore)
	}

	archive, exportHeader := sitepackCrossInstallExport(t, source.server.URL+"/api/v2/admin/site/snapshots/"+childID.String()+"/export", source.adminToken)
	if exportHeader.Get("Content-Type") != "application/zip" || !strings.Contains(exportHeader.Get("Cache-Control"), "no-store") {
		t.Fatalf("unexpected source export headers: %v", exportHeader)
	}
	if after := sitepackCrossInstallStateSignature(t, source.pool, parentID); after != parentBefore {
		t.Fatal("sitepack export changed parent snapshot or parent content")
	}
	if after := sitepackCrossInstallStateSignature(t, source.pool, childID); after != childBefore {
		t.Fatal("sitepack export changed source snapshot or source content")
	}
	if after := sitepackCrossInstallBranchAssignment(t, source.pool, source.devBranch); after == nil || *after != parentID {
		t.Fatalf("sitepack export changed source branch assignment: %v", after)
	}

	t.Setenv("TEST_DATABASE_URL", targetURL)
	target := newTestEnv(t)
	targetStore, err := s3store.New(ctx, config.S3Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: bucket,
		AccessKey: accessKey, SecretKey: secretKey, PublicBaseURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create isolated target MinIO client: %v", err)
	}
	if err := targetStore.EnsureBucket(ctx); err != nil {
		t.Fatalf("ensure unique target MinIO bucket: %v", err)
	}
	if err := targetStore.RequirePrivate(ctx); err != nil {
		t.Fatalf("target MinIO bucket is not private: %v", err)
	}
	importService := sitebuilder.NewSnapshotService(target.pool, configuredBuildService(t, target.pool, targetStore, 8<<20))
	if err := importService.SetDesignPreviewTTL(20 * time.Minute); err != nil {
		t.Fatal(err)
	}
	importService.SetMediaService(target.media)
	targetFiles, err := files.NewService(target.pool, targetStore, sitepackCrossInstallFilesConfig(t, endpoint, t.TempDir()))
	if err != nil {
		t.Fatalf("create target file service: %v", err)
	}
	if err := targetFiles.InitOwners(ctx); err != nil {
		t.Fatalf("initialize target file owner: %v", err)
	}
	targetFiles.SetReferenceManager(importService)
	targetDeps := target.routerDeps
	targetDeps.Files = hfiles.NewHandler(targetFiles, target.routerDeps.Log, 1<<20)
	targetMedia := hmedia.NewHandler(target.media, target.routerDeps.Log, 20<<20)
	if err := targetMedia.SetRedirectCacheTTL(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := targetMedia.SetPagination(20, 50); err != nil {
		t.Fatal(err)
	}
	targetMedia.SetProfileProvider(importService)
	targetMedia.SetFileHandler(targetDeps.Files)
	targetDeps.Media = targetMedia
	targetDeps.FeatureBuilds = hsitebuilder.NewBuildsHandler(configuredBuildService(t, target.pool, targetStore, 8<<20), target.routerDeps.Log, 8<<20)
	snapshotsHandler := hsitebuilder.NewSnapshotsHandler(importService, target.forms, target.routerDeps.Log, 1<<20)
	if err := snapshotsHandler.SetPagination(20, 50); err != nil {
		t.Fatal(err)
	}
	targetDeps.SiteSnapshots = snapshotsHandler
	targetDeps.Sitepacks = hsitebuilder.NewSitepackHandler(importService, sitepackCrossInstallOptions(t), target.routerDeps.Log)
	targetServer := httptest.NewServer(httptransport.NewRouter(targetDeps))
	t.Cleanup(targetServer.Close)
	targetPublicServer := httptest.NewServer(httptransport.NewPublicRouter(targetDeps, target.devBranch))
	t.Cleanup(targetPublicServer.Close)
	target.server, target.devServer, target.snapshots, target.routerDeps = targetServer, targetPublicServer, importService, targetDeps

	assertSitepackCrossInstallEmptyTarget(t, target.pool)
	invalidArchive := rewriteSitepackIndex(t, archive, func(index map[string]any) {
		payload := index["payload"].(map[string]any)
		configMap := payload["config"].(map[string]any)
		dataModel := configMap["dataModel"].(map[string]any)
		entities := dataModel["entities"].([]any)
		fields := entities[0].(map[string]any)["fields"].([]any)
		fields[0].(map[string]any)["id"] = "not-a-generated-uuid"
	})
	status, invalidBody, _ := target.rawRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", target.adminToken, bytes.NewReader(invalidArchive), "application/zip")
	if status != http.StatusBadRequest {
		t.Fatalf("invalid model preflight should reject before import: status=%d body=%v", status, invalidBody)
	}
	assertSitepackCrossInstallEmptyTarget(t, target.pool)
	for _, key := range sitepackCrossInstallBuildObjectKeys(t, archive) {
		if _, err := targetStore.Stat(ctx, key); err == nil {
			t.Fatalf("preflight rejection wrote object %q to target storage", key)
		}
	}

	status, importBody, _ := target.rawRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", target.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated {
		t.Fatalf("import current model-backed Vitasil sitepack: status=%d body=%v", status, importBody)
	}
	firstReport := sitepackCrossInstallDecodeReport(t, importBody)
	if firstReport.NewBuilds != 3 || firstReport.ReusedBuilds != 0 || firstReport.NewFiles != 2 || firstReport.ReusedFiles != 0 ||
		firstReport.ModelPublishedRecords != 6 || firstReport.ModelDraftRecords != 6 || firstReport.ModelHistoryRevisions != 1 ||
		firstReport.RemappedModelEntityIDs != len(model.Entities) || firstReport.RemappedModelFieldIDs != sitepackCrossInstallFieldCount(model) ||
		firstReport.RemappedModelRelationIDs != len(model.Relations) || firstReport.RemappedModelRecordIDs != 6 || len(firstReport.FileIDs) != 2 {
		t.Fatalf("first import report omitted required model, history, build, or media transfer: %+v", firstReport)
	}
	firstSnapshotID := firstReport.Snapshot.ID
	if firstSnapshotID == uuid.Nil || firstReport.Snapshot.ParentID != nil {
		t.Fatalf("sitepack import must create an independent root snapshot: %+v", firstReport.Snapshot)
	}
	assertSitepackCrossInstallTargetState(t, source, target, targetStore, parentID, childID, firstSnapshotID, model, pathKeys, ids, firstReport, privateFile.ID, publicFile.ID)
	if after := sitepackCrossInstallStateSignature(t, source.pool, parentID); after != parentBefore {
		t.Fatal("target import changed source parent snapshot or content")
	}
	if after := sitepackCrossInstallStateSignature(t, source.pool, childID); after != childBefore {
		t.Fatal("target import changed source snapshot or content")
	}
	if after := sitepackCrossInstallBranchAssignment(t, source.pool, source.devBranch); after == nil || *after != parentID {
		t.Fatalf("target import changed source branch assignment: %v", after)
	}

	status, repeatBody, _ := target.rawRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", target.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated {
		t.Fatalf("repeat Sitepack import should reuse immutable builds/files and create a new independent snapshot: status=%d body=%v", status, repeatBody)
	}
	repeatReport := sitepackCrossInstallDecodeReport(t, repeatBody)
	if repeatReport.Snapshot.ID == uuid.Nil || repeatReport.Snapshot.ID == firstSnapshotID || repeatReport.NewBuilds != 0 || repeatReport.ReusedBuilds != 3 || repeatReport.NewFiles != 0 || repeatReport.ReusedFiles != 2 {
		t.Fatalf("repeat import did not reuse matching target builds/files: %+v", repeatReport)
	}
	for sourceID, targetID := range firstReport.FileIDs {
		if repeatReport.FileIDs[sourceID] != targetID {
			t.Fatalf("repeat import failed to reuse target media %s: first=%s repeat=%s", sourceID, targetID, repeatReport.FileIDs[sourceID])
		}
	}
	var targetBuilds, targetSnapshots, targetFilesCount int
	if err := target.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM feature_builds),(SELECT count(*) FROM site_snapshots),(SELECT count(*) FROM media_objects WHERE status='ready')`).Scan(&targetBuilds, &targetSnapshots, &targetFilesCount); err != nil {
		t.Fatal(err)
	}
	if targetBuilds != 3 || targetSnapshots != 2 || targetFilesCount != 2 {
		t.Fatalf("repeat import duplicated reusable installation data: builds=%d snapshots=%d files=%d", targetBuilds, targetSnapshots, targetFilesCount)
	}

	conflictArchive := rewriteSitepackBuildArtifact(t, archive, "catalog")
	countsBeforeConflict := sitepackCrossInstallTableCounts(t, target.pool)
	status, conflictBody, _ := target.rawRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", target.adminToken, bytes.NewReader(conflictArchive), "application/zip")
	if status != http.StatusBadRequest {
		t.Fatalf("same Vitasil key/version with changed ZIP bytes must be rejected as a conflict: status=%d body=%v", status, conflictBody)
	}
	if after := sitepackCrossInstallTableCounts(t, target.pool); after != countsBeforeConflict {
		t.Fatalf("conflicting import changed target database: before=%v after=%v", countsBeforeConflict, after)
	}
	existingBuildObjects := map[string]struct{}{}
	for _, key := range sitepackCrossInstallBuildObjectKeys(t, archive) {
		existingBuildObjects[key] = struct{}{}
	}
	for _, key := range sitepackCrossInstallBuildObjectKeys(t, conflictArchive) {
		if _, existedBefore := existingBuildObjects[key]; existedBefore {
			continue
		}
		if _, err := targetStore.Stat(ctx, key); err == nil {
			t.Fatalf("conflicting import wrote unregistered build object %q", key)
		}
	}
	if assigned := sitepackCrossInstallBranchAssignment(t, target.pool, target.devBranch); assigned != nil {
		t.Fatalf("imported snapshots must never be assigned to target branches implicitly; found %s", assigned)
	}
	for _, snapshotID := range []uuid.UUID{firstReport.Snapshot.ID, repeatReport.Snapshot.ID} {
		var assignments int
		if err := target.pool.QueryRow(ctx, `SELECT count(*) FROM site_environments WHERE active_snapshot_id=$1`, snapshotID).Scan(&assignments); err != nil {
			t.Fatal(err)
		}
		if assignments != 0 {
			t.Fatalf("sitepack imported snapshot %s into %d branch assignment(s)", snapshotID, assignments)
		}
	}
	t.Logf("cross-install passed: source=%s target=%s MinIO=%s/%s; model ids and content/history remapped, typed media/ACL retained, preflight and version conflict were side-effect free", sourceName, targetName, endpoint, bucket)
}

func requireSitepackCrossInstallDatabase(t *testing.T, raw, prefix string) string {
	t.Helper()
	parsed, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatalf("invalid disposable PostgreSQL URL: %v", err)
	}
	if !strings.HasPrefix(parsed.Database, prefix) {
		t.Fatalf("refusing non-task-owned database %q; expected prefix %q", parsed.Database, prefix)
	}
	if ip := net.ParseIP(parsed.Host); parsed.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing non-loopback disposable PostgreSQL host %q", parsed.Host)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to isolated database %q: %v", parsed.Database, err)
	}
	defer connection.Close(context.Background())
	var current string
	var others int
	if err := connection.QueryRow(ctx, `SELECT current_database(),count(*) FILTER (WHERE pid<>pg_backend_pid()) OVER () FROM pg_stat_activity WHERE datname=current_database() LIMIT 1`).Scan(&current, &others); err != nil {
		t.Fatalf("verify isolated database %q has no active sessions: %v", parsed.Database, err)
	}
	if current != parsed.Database || others != 0 {
		t.Fatalf("refusing database reset: current=%q expected=%q otherSessions=%d", current, parsed.Database, others)
	}
	t.Logf("verified disposable database %s has no other active sessions", current)
	return current
}

func assertSitepackCrossInstallDatabaseIdle(t *testing.T, raw, expected string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Errorf("reconnect to verify closed disposable database %s: %v", expected, err)
		return
	}
	defer connection.Close(context.Background())
	var current string
	var others int
	if err := connection.QueryRow(ctx, `SELECT current_database(),count(*) FILTER (WHERE pid<>pg_backend_pid()) OVER () FROM pg_stat_activity WHERE datname=current_database() LIMIT 1`).Scan(&current, &others); err != nil {
		t.Errorf("verify idle disposable database %s: %v", expected, err)
		return
	}
	if current != expected || others != 0 {
		t.Errorf("disposable database cleanup left active sessions: current=%q expected=%q otherSessions=%d", current, expected, others)
	}
}

func requireSitepackCrossInstallMinIO(t *testing.T, endpoint, bucket, accessKey, secretKey string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		t.Fatalf("target MinIO endpoint must be an HTTP loopback endpoint: %q", endpoint)
	}
	if ip := net.ParseIP(parsed.Hostname()); parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing non-loopback target object storage %q", endpoint)
	}
	if !strings.HasPrefix(bucket, "sitepack-cross-install-qa-") || accessKey == "" || secretKey == "" {
		t.Fatal("target storage requires a task-unique sitepack-cross-install-qa-* bucket and explicit disposable credentials")
	}
	if strings.ContainsAny(bucket, "/\\") {
		t.Fatalf("invalid disposable bucket name %q", bucket)
	}
}

func sitepackCrossInstallFilesConfig(t *testing.T, publicBaseURL, tempDir string) config.FilesConfig {
	t.Helper()
	return config.FilesConfig{
		MaxBytes: 20 << 20, PartBytes: 20 << 20, UploadTTL: time.Hour, CleanupInterval: time.Minute,
		ProcessingTimeout: time.Minute, OwnerQuotaBytes: 64 << 20, MaxSessions: 8, ListLimit: 20,
		RateLimit: 1200, RateMaxClients: 100, RateWindow: time.Minute, LinkMaxTTL: 15 * time.Minute,
		PublicBaseURL: publicBaseURL, TempDir: tempDir, SigningKey: bytes.Repeat([]byte("sitepack-cross-install-signing-key"), 2),
	}
}

type sitepackCrossInstallRenditionProcessor struct{}

func (*sitepackCrossInstallRenditionProcessor) Process(_ context.Context, _ featuremedia.Media, profile featuremedia.FieldProfile, _ []byte) (map[string]featuremedia.ProcessedOutput, error) {
	outputs := make(map[string]featuremedia.ProcessedOutput, len(profile.Renditions))
	for _, rendition := range profile.Renditions {
		outputs[rendition.Name] = featuremedia.ProcessedOutput{
			Data: sitepackCrossInstallWebP(rendition.Name), MIMEType: "image/webp",
			Width: rendition.Width, Height: rendition.Height,
		}
	}
	return outputs, nil
}

func (*sitepackCrossInstallRenditionProcessor) Ready(context.Context) error { return nil }

func sitepackCrossInstallWebP(label string) []byte {
	// The classifier used by Sitepack checks the RIFF/WEBP signature. The
	// deterministic fixture is sufficient for storage/remap acceptance and
	// avoids using a machine-specific ffmpeg binary.
	data := []byte("RIFF\x00\x00\x00\x00WEBPVP8 " + "\x04\x00\x00\x00" + "\x00\x00\x00\x00" + label)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	return data
}

func sitepackCrossInstallPrepareRenditions(t *testing.T, ctx context.Context, service *featuremedia.Service, manifests map[string]sitebuilder.Manifest, fileIDs []uuid.UUID) {
	t.Helper()
	profilesByHash := map[string]featuremedia.FieldProfile{}
	for _, manifest := range manifests {
		for _, port := range manifest.DataInputs {
			for _, field := range port.Fields {
				if field.Type != "media" || field.Profile == nil {
					continue
				}
				hash, err := featuremedia.ProfileHash(*field.Profile)
				if err != nil {
					t.Fatalf("hash Vitasil media profile %s.%s: %v", manifest.Key, field.Path, err)
				}
				profilesByHash[hash] = *field.Profile
			}
		}
	}
	for _, id := range fileIDs {
		for _, profile := range profilesByHash {
			if _, err := service.Prepare(ctx, id, profile); err != nil {
				t.Fatalf("prepare Vitasil model media rendition for %s: %v", id, err)
			}
		}
	}
	for attempts := 0; attempts < len(fileIDs)*len(profilesByHash)+1; attempts++ {
		processed, err := service.ProcessOne(ctx)
		if err != nil {
			t.Fatalf("process Vitasil model media rendition: %v", err)
		}
		if !processed {
			break
		}
	}
	for _, id := range fileIDs {
		for _, profile := range profilesByHash {
			state, err := service.State(ctx, id, profile)
			if err != nil || state.Status != "ready" {
				t.Fatalf("Vitasil model media profile did not become ready: file=%s state=%+v err=%v", id, state, err)
			}
		}
	}
}

func sitepackCrossInstallOptions(t *testing.T) sitebuilder.SitepackOptions {
	t.Helper()
	return sitebuilder.SitepackOptions{TempDir: t.TempDir(), MaxBytes: 128 << 20, MaxExpandedBytes: 256 << 20, MaxEntries: 500, MaxIndexBytes: 16 << 20, MaxSVGBytes: 4 << 20, MaxBrowserBlobBytes: 128 << 20}
}

func sitepackCrossInstallVitasilConfig(t *testing.T, manifests map[string]sitebuilder.Manifest) (sitebuilder.DataModel, map[string]any, map[string]string, map[string]string) {
	t.Helper()
	catalog := modelAcceptancePort(t, manifests["catalog"], "products")
	detail := modelAcceptancePort(t, manifests["catalog-detail"], "product")
	mainProducts := modelAcceptancePort(t, manifests["main-data"], "products")
	modelAcceptanceAssertSameOutputShape(t, catalog, detail, "catalog-detail.product")
	modelAcceptanceAssertSameOutputShape(t, catalog, mainProducts, "main-data.products")
	model, product, productIDs, productKeys := modelAcceptanceEntity("product", "Товар", catalog, true)
	entityIDs := map[string]string{"entity.product": product.ID}
	ids := map[string]string{"entity.product": product.ID}
	pathKeys := map[string]string{}
	for path, id := range productIDs {
		ids["field.product:"+path] = id
	}
	for path, key := range productKeys {
		pathKeys["product:"+path] = key
	}
	category := sitebuilder.DataModelEntity{ID: uuid.NewString(), Key: "category", Name: "Категория", Fields: []sitebuilder.DataModelField{{ID: uuid.NewString(), Key: "name", Name: "Название", Type: "text"}}}
	model.Entities = append(model.Entities, category)
	entityIDs["entity.category"], ids["entity.category"], ids["field.category:/name"] = category.ID, category.ID, category.Fields[0].ID
	pathKeys["category:/name"] = "name"
	relation := sitebuilder.DataModelRelation{ID: uuid.NewString(), Name: "Товары категории", FromEntityID: category.ID, ToEntityID: product.ID, Cardinality: "one-to-many"}
	model.Relations = []sitebuilder.DataModelRelation{relation}
	ids["relation.category_products"] = relation.ID
	portIDs := map[string]map[string]string{}
	for _, portKey := range []string{"info", "advantages", "slider"} {
		port := modelAcceptancePort(t, manifests["main-data"], portKey)
		key := "main_data_" + portKey
		_, entity, pathIDs, fieldKeys := modelAcceptanceEntity(key, map[string]string{"info": "Информация главной", "advantages": "Преимущества", "slider": "Слайдер"}[portKey], port, false)
		model.Entities = append(model.Entities, entity)
		entityIDs["entity."+portKey] = entity.ID
		ids["entity."+portKey] = entity.ID
		for path, fieldID := range pathIDs {
			ids["field."+portKey+":"+path] = fieldID
			pathKeys[portKey+":"+path] = fieldKeys[path]
		}
		portIDs[portKey] = pathIDs
	}
	model.Positions = map[string]sitebuilder.DataModelPosition{}
	for index, entity := range model.Entities {
		model.Positions[entity.ID] = sitebuilder.DataModelPosition{X: float64(index * 240), Y: float64(index * 120)}
	}

	bindingsFor := func(port sitebuilder.DataInputPort, entityID string, pathIDs map[string]string) sitebuilder.DataInputBinding {
		return sitebuilder.DataInputBinding{InputKey: port.Key, EntityID: entityID, Fields: modelAcceptanceFieldBindings(port, pathIDs)}
	}
	mainBindings := make([]sitebuilder.DataInputBinding, 0, len(manifests["main-data"].DataInputs))
	for _, port := range manifests["main-data"].DataInputs {
		switch port.Key {
		case "products":
			mainBindings = append(mainBindings, bindingsFor(port, product.ID, productIDs))
		case "info", "advantages", "slider":
			mainBindings = append(mainBindings, bindingsFor(port, entityIDs["entity."+port.Key], portIDs[port.Key]))
		default:
			t.Fatalf("unexpected Vitasil main-data input %q", port.Key)
		}
	}
	catalogBinding := bindingsFor(catalog, product.ID, productIDs)
	detailBinding := bindingsFor(detail, product.ID, productIDs)
	detailBinding.Lookup = &sitebuilder.DataInputLookupBinding{Argument: "slug", FieldID: productIDs["/slug"]}
	configValue := sitebuilder.SiteConfig{
		FormatVersion: 3, DataModel: &model,
		Pages: []sitebuilder.Page{
			{ID: "home", Name: "Главная", Kind: "page", Placements: []sitebuilder.Placement{{ID: "home-data", Build: sitebuilder.BuildRef{FeatureKey: "main-data", Version: manifests["main-data"].Version}, Slot: "main", DataBindings: mainBindings}}},
			{ID: "catalog", Name: "Каталог", Kind: "collection-list", Placements: []sitebuilder.Placement{{ID: "catalog-products", Build: sitebuilder.BuildRef{FeatureKey: "catalog", Version: manifests["catalog"].Version}, Slot: "main", Arguments: map[string]sitebuilder.ArgumentBinding{
				"title":      {Source: "literal", Value: json.RawMessage(`"Каталог"`)},
				"breadcrumb": {Source: "literal", Value: json.RawMessage(`{"homeLabel":"Главная","currentLabel":"Каталог"}`)},
				"action":     {Source: "literal", Value: json.RawMessage(`{"label":"Задать вопрос","to":"/contacts"}`)},
			}, DataBindings: []sitebuilder.DataInputBinding{catalogBinding}, DetailLink: &sitebuilder.DetailLink{RouteID: "product-detail-route", Field: "slug", PathTemplate: "/catalog/{slug}"}}}},
			{ID: "product", Name: "Товар", Kind: "detail", Placements: []sitebuilder.Placement{{ID: "product-detail", Build: sitebuilder.BuildRef{FeatureKey: "catalog-detail", Version: manifests["catalog-detail"].Version}, Slot: "main", Arguments: map[string]sitebuilder.ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}}, DataBindings: []sitebuilder.DataInputBinding{detailBinding}}}},
		},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{
			{ID: "home-route", Type: "path", Path: "/", PageID: "home"},
			{ID: "catalog-route", Type: "path", Path: "/catalog", PageID: "catalog"},
			{ID: "product-detail-route", Type: "path", Path: "/catalog/:slug", PageID: "product"},
		}},
	}
	raw, err := json.Marshal(configValue)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return model, result, pathKeys, ids
}

func sitepackCrossInstallInitialRecords(ids, pathKeys map[string]string, publicID string) []sitebuilder.ModelContentRecord {
	return []sitebuilder.ModelContentRecord{
		{EntityID: ids["entity.product"], SortOrder: 0, Values: modelAcceptanceValues(sitepackCrossInstallProductKeys(pathKeys), map[string]any{"/slug": "pear", "/title": "Груша", "/category": "Фрукты", "/price": "100 ₽", "/imageSrc": publicID, "/description": "Спелая груша"})},
		{EntityID: ids["entity.product"], SortOrder: 1, Values: modelAcceptanceValues(sitepackCrossInstallProductKeys(pathKeys), map[string]any{"/slug": "apple", "/title": "Яблоко", "/category": "Фрукты", "/price": "90 ₽", "/imageSrc": publicID, "/description": "Сочное яблоко"})},
		{EntityID: ids["entity.category"], SortOrder: 0, Values: map[string]any{"name": "Фрукты"}},
		{EntityID: ids["entity.info"], SortOrder: 0, Values: modelAcceptanceValues(sitepackCrossInstallPortKeys(pathKeys, "info"), map[string]any{"/title": "Натуральная забота"})},
		{EntityID: ids["entity.advantages"], SortOrder: 0, Values: modelAcceptanceValues(sitepackCrossInstallPortKeys(pathKeys, "advantages"), map[string]any{"/icon": "quality", "/title": "Качество", "/description": "Натуральные ингредиенты"})},
		{EntityID: ids["entity.slider"], SortOrder: 0, Values: modelAcceptanceValues(sitepackCrossInstallPortKeys(pathKeys, "slider"), map[string]any{"/eyebrow": "Ассортимент", "/title": "Популярные продукты", "/previousLabel": "Назад", "/nextLabel": "Вперёд", "/itemsLabel": "Позиции", "/catalogActionLabel": "Весь каталог", "/catalogActionTo": "/catalog"})},
	}
}

func sitepackCrossInstallProductKeys(pathKeys map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range pathKeys {
		if strings.HasPrefix(key, "product:") {
			result[strings.TrimPrefix(key, "product:")] = value
		}
	}
	return result
}

func sitepackCrossInstallPortKeys(pathKeys map[string]string, port string) map[string]string {
	result := map[string]string{}
	for key, value := range pathKeys {
		if strings.HasPrefix(key, port+":") {
			result[strings.TrimPrefix(key, port+":")] = value
		}
	}
	return result
}

func sitepackCrossInstallDecodeRecords(t *testing.T, value any) []sitebuilder.ModelContentRecord {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var records []sitebuilder.ModelContentRecord
	if err := json.Unmarshal(encoded, &records); err != nil {
		t.Fatal(err)
	}
	return records
}

func sitepackCrossInstallDecodeSelections(t *testing.T, value any) []sitebuilder.ModelContentSelection {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var selections []sitebuilder.ModelContentSelection
	if err := json.Unmarshal(encoded, &selections); err != nil {
		t.Fatal(err)
	}
	return selections
}

func sitepackCrossInstallRecordIDs(t *testing.T, records []sitebuilder.ModelContentRecord, ids, pathKeys map[string]string) (map[string]string, string) {
	t.Helper()
	result := map[string]string{}
	category := ""
	for _, record := range records {
		if record.EntityID == ids["entity.product"] {
			slug := fmt.Sprint(record.Values[pathKeys["product:/slug"]])
			result["product:"+slug] = record.ID
		}
		if record.EntityID == ids["entity.category"] {
			category = record.ID
		}
		for _, entityKey := range []string{"info", "advantages", "slider"} {
			if record.EntityID == ids["entity."+entityKey] {
				result["entity:"+entityKey] = record.ID
			}
		}
	}
	if result["product:pear"] == "" || result["product:apple"] == "" || category == "" {
		t.Fatalf("model content response omitted generated Product or Category IDs: records=%+v", records)
	}
	return result, category
}

func sitepackCrossInstallSelections(recordIDs map[string]string) []sitebuilder.ModelContentSelection {
	return []sitebuilder.ModelContentSelection{
		{PlacementID: "home-data", InputKey: "info", Mode: "ids", RecordIDs: []string{recordIDs["entity:info"]}},
		{PlacementID: "home-data", InputKey: "advantages", Mode: "all"},
		{PlacementID: "home-data", InputKey: "slider", Mode: "ids", RecordIDs: []string{recordIDs["entity:slider"]}},
		{PlacementID: "home-data", InputKey: "products", Mode: "ids", RecordIDs: []string{recordIDs["product:pear"], recordIDs["product:apple"]}},
		{PlacementID: "catalog-products", InputKey: "products", Mode: "ids", RecordIDs: []string{recordIDs["product:apple"], recordIDs["product:pear"]}},
	}
}

func sitepackCrossInstallSetSelection(selections []sitebuilder.ModelContentSelection, placementID, inputKey, mode string, recordIDs []string) []sitebuilder.ModelContentSelection {
	for index := range selections {
		if selections[index].PlacementID == placementID && selections[index].InputKey == inputKey {
			selections[index].Mode, selections[index].RecordIDs = mode, append([]string(nil), recordIDs...)
			return selections
		}
	}
	return append(selections, sitebuilder.ModelContentSelection{PlacementID: placementID, InputKey: inputKey, Mode: mode, RecordIDs: append([]string(nil), recordIDs...)})
}

func sitepackCrossInstallExport(t *testing.T, url, token string) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("export model-backed source sitepack: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read source sitepack archive: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export model-backed source sitepack: status=%d body=%s", response.StatusCode, body)
	}
	return body, response.Header.Clone()
}

func sitepackCrossInstallDecodeReport(t *testing.T, raw map[string]any) sitebuilder.SitepackReport {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var report sitebuilder.SitepackReport
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

type sitepackCrossInstallWorkspace struct {
	Revision            int64
	PublishedRevision   int64
	DraftRecords        []sitebuilder.ModelContentRecord
	DraftSelections     []sitebuilder.ModelContentSelection
	PublishedRecords    []sitebuilder.ModelContentRecord
	PublishedSelections []sitebuilder.ModelContentSelection
}

func sitepackCrossInstallReadWorkspace(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, snapshotID uuid.UUID) sitepackCrossInstallWorkspace {
	t.Helper()
	var result sitepackCrossInstallWorkspace
	var draftRecords, draftSelections, publishedRecords, publishedSelections []byte
	if err := pool.QueryRow(context.Background(), `SELECT revision,published_revision,draft_records,draft_selections,published_records,published_selections FROM site_snapshot_model_workspaces WHERE snapshot_id=$1`, snapshotID).Scan(&result.Revision, &result.PublishedRevision, &draftRecords, &draftSelections, &publishedRecords, &publishedSelections); err != nil {
		t.Fatalf("read model workspace %s: %v", snapshotID, err)
	}
	for _, item := range []struct {
		label string
		raw   []byte
		out   any
	}{{"draft records", draftRecords, &result.DraftRecords}, {"draft selections", draftSelections, &result.DraftSelections}, {"published records", publishedRecords, &result.PublishedRecords}, {"published selections", publishedSelections, &result.PublishedSelections}} {
		if err := json.Unmarshal(item.raw, item.out); err != nil {
			t.Fatalf("decode %s for snapshot %s: %v", item.label, snapshotID, err)
		}
	}
	return result
}

func sitepackCrossInstallStateSignature(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, snapshotID uuid.UUID) string {
	t.Helper()
	var digest string
	var payload []byte
	var contentRevision int64
	if err := pool.QueryRow(context.Background(), `SELECT digest,payload,content_revision FROM site_snapshots WHERE id=$1`, snapshotID).Scan(&digest, &payload, &contentRevision); err != nil {
		t.Fatalf("read source snapshot signature: %v", err)
	}
	workspace := sitepackCrossInstallReadWorkspace(t, pool, snapshotID)
	history, err := pool.Query(context.Background(), `SELECT id,draft_revision,content_revision,records::text,selections::text,created_at FROM snapshot_model_content_publications WHERE snapshot_id=$1 ORDER BY content_revision`, snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	items := []map[string]any{}
	for history.Next() {
		var id uuid.UUID
		var draftRevision, contentRevision int64
		var records, selections string
		var createdAt time.Time
		if err := history.Scan(&id, &draftRevision, &contentRevision, &records, &selections, &createdAt); err != nil {
			t.Fatal(err)
		}
		items = append(items, map[string]any{"id": id, "draftRevision": draftRevision, "contentRevision": contentRevision, "records": records, "selections": selections, "createdAt": createdAt})
	}
	if err := history.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Digest          string
		Payload         json.RawMessage
		ContentRevision int64
		Workspace       sitepackCrossInstallWorkspace
		History         []map[string]any
	}{digest, payload, contentRevision, workspace, items})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sitepackCrossInstallBranchAssignment(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, branchID environment.Environment) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT active_snapshot_id FROM site_environments WHERE key=$1`, branchID.String()).Scan(&id); err != nil {
		t.Fatalf("read branch snapshot assignment: %v", err)
	}
	return id
}

func assertSitepackCrossInstallEmptyTarget(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) {
	t.Helper()
	counts := sitepackCrossInstallTableCounts(t, pool)
	if counts != (sitepackCrossInstallCounts{}) {
		t.Fatalf("preflight rejection changed the empty target database: %+v", counts)
	}
}

type sitepackCrossInstallCounts struct {
	Builds, Snapshots, Files, Workspaces, Publications int
}

func sitepackCrossInstallTableCounts(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) sitepackCrossInstallCounts {
	t.Helper()
	var counts sitepackCrossInstallCounts
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM feature_builds),
		(SELECT count(*) FROM site_snapshots),
		(SELECT count(*) FROM media_objects WHERE status='ready'),
		(SELECT count(*) FROM site_snapshot_model_workspaces),
		(SELECT count(*) FROM snapshot_model_content_publications)`).Scan(&counts.Builds, &counts.Snapshots, &counts.Files, &counts.Workspaces, &counts.Publications); err != nil {
		t.Fatal(err)
	}
	return counts
}

func sitepackCrossInstallBuildObjectKeys(t *testing.T, archive []byte) []string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reader.File {
		if entry.Name != "sitepack.json" {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		var index struct {
			Builds []struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"builds"`
		}
		err = json.NewDecoder(stream).Decode(&index)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries := make(map[string]*zip.File, len(reader.File))
		for _, candidate := range reader.File {
			entries[candidate.Name] = candidate
		}
		keys := make([]string, 0, len(index.Builds))
		for _, build := range index.Builds {
			keys = append(keys, filepath.ToSlash(filepath.Join("feature-builds", build.SHA256, "artifact.zip")))
			buildEntry := entries[build.Path]
			if buildEntry == nil {
				t.Fatalf("sitepack archive is missing build %q", build.Path)
			}
			buildStream, err := buildEntry.Open()
			if err != nil {
				t.Fatal(err)
			}
			buildBytes, err := io.ReadAll(buildStream)
			_ = buildStream.Close()
			if err != nil {
				t.Fatal(err)
			}
			verified, err := sitebuilder.VerifyArchive(buildBytes, int64(len(buildBytes)))
			if err != nil || verified.Digest != build.SHA256 {
				t.Fatalf("invalid archived build %q: digest=%s err=%v", build.Path, verified.Digest, err)
			}
			for _, asset := range verified.Assets {
				keys = append(keys, filepath.ToSlash(filepath.Join("feature-builds", build.SHA256, filepath.FromSlash(asset.Path))))
			}
		}
		return keys
	}
	t.Fatal("sitepack archive is missing sitepack.json")
	return nil
}

func assertSitepackCrossInstallTargetState(t *testing.T, source, target *testEnv, targetStore *s3store.Store, sourceParentID, sourceChildID, targetSnapshotID uuid.UUID, sourceModel sitebuilder.DataModel, pathKeys, sourceIDs map[string]string, report sitebuilder.SitepackReport, sourcePrivateFile, sourcePublicFile uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	sourceDetailStatus, sourceDetailBody, _ := source.request(http.MethodGet, "/api/v2/admin/site/snapshots/"+sourceChildID.String(), source.adminToken, nil)
	if sourceDetailStatus != http.StatusOK {
		t.Fatalf("read source snapshot model: status=%d body=%v", sourceDetailStatus, sourceDetailBody)
	}
	targetDetailStatus, targetDetailBody, _ := target.request(http.MethodGet, "/api/v2/admin/site/snapshots/"+targetSnapshotID.String(), target.adminToken, nil)
	if targetDetailStatus != http.StatusOK {
		t.Fatalf("read imported snapshot model: status=%d body=%v", targetDetailStatus, targetDetailBody)
	}
	decodeDetail := func(value map[string]any) sitebuilder.SnapshotDetail {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var detail sitebuilder.SnapshotDetail
		if err := json.Unmarshal(encoded, &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}
	sourceDetail, targetDetail := decodeDetail(sourceDetailBody), decodeDetail(targetDetailBody)
	if sourceDetail.Payload.Config.DataModel == nil || targetDetail.Payload.Config.DataModel == nil {
		t.Fatal("source/imported snapshot is missing its ER model")
	}
	entityMap, fieldMap, relationMap := sitepackCrossInstallAssertModelRemapped(t, *sourceDetail.Payload.Config.DataModel, *targetDetail.Payload.Config.DataModel)
	if len(entityMap) != len(sourceModel.Entities) || len(fieldMap) != sitepackCrossInstallFieldCount(sourceModel) || len(relationMap) != len(sourceModel.Relations) {
		t.Fatalf("snapshot schema remap did not cover every ID: entities=%d fields=%d relations=%d", len(entityMap), len(fieldMap), len(relationMap))
	}
	for pageIndex, sourcePage := range sourceDetail.Payload.Config.Pages {
		for placementIndex, sourcePlacement := range sourcePage.Placements {
			targetPlacement := targetDetail.Payload.Config.Pages[pageIndex].Placements[placementIndex]
			for bindingIndex, sourceBinding := range sourcePlacement.DataBindings {
				targetBinding := targetPlacement.DataBindings[bindingIndex]
				if targetBinding.EntityID != entityMap[sourceBinding.EntityID] || targetBinding.EntityID == sourceBinding.EntityID {
					t.Fatalf("data input %s did not use its remapped entity id", sourceBinding.InputKey)
				}
				if sourceBinding.Lookup != nil && (targetBinding.Lookup == nil || targetBinding.Lookup.FieldID != fieldMap[sourceBinding.Lookup.FieldID]) {
					t.Fatalf("lookup binding %s did not use its remapped field id", sourceBinding.InputKey)
				}
				for fieldIndex, fieldBinding := range sourceBinding.Fields {
					if targetBinding.Fields[fieldIndex].FieldID != fieldMap[fieldBinding.FieldID] {
						t.Fatalf("typed field binding %s%s did not use its remapped field id", sourceBinding.InputKey, fieldBinding.InputPath)
					}
				}
			}
		}
	}
	sourceState := sitepackCrossInstallReadWorkspace(t, source.pool, sourceChildID)
	targetState := sitepackCrossInstallReadWorkspace(t, target.pool, targetSnapshotID)
	if targetState.Revision != sourceState.Revision || targetState.PublishedRevision != sourceState.PublishedRevision || len(targetState.DraftRecords) != len(sourceState.DraftRecords) || len(targetState.PublishedRecords) != len(sourceState.PublishedRecords) {
		t.Fatalf("target content revisions or record sets differ from source: source=%+v target=%+v", sourceState, targetState)
	}
	recordMap := sitepackCrossInstallAssertRecords(t, sourceState.DraftRecords, targetState.DraftRecords, sourceModel, *targetDetail.Payload.Config.DataModel, entityMap, relationMap, report.FileIDs, pathKeys)
	sitepackCrossInstallAssertRecords(t, sourceState.PublishedRecords, targetState.PublishedRecords, sourceModel, *targetDetail.Payload.Config.DataModel, entityMap, relationMap, report.FileIDs, pathKeys)
	sitepackCrossInstallAssertSelections(t, sourceState.DraftSelections, targetState.DraftSelections, recordMap)
	sitepackCrossInstallAssertSelections(t, sourceState.PublishedSelections, targetState.PublishedSelections, recordMap)
	var sourceHistoryID, targetHistoryID uuid.UUID
	var sourceHistoryRecordsRaw, targetHistoryRecordsRaw []byte
	var sourceHistorySelectionsRaw, targetHistorySelectionsRaw []byte
	if err := source.pool.QueryRow(ctx, `SELECT id,records,selections FROM snapshot_model_content_publications WHERE snapshot_id=$1 ORDER BY content_revision DESC LIMIT 1`, sourceChildID).Scan(&sourceHistoryID, &sourceHistoryRecordsRaw, &sourceHistorySelectionsRaw); err != nil {
		t.Fatal(err)
	}
	if err := target.pool.QueryRow(ctx, `SELECT id,records,selections FROM snapshot_model_content_publications WHERE snapshot_id=$1 ORDER BY content_revision DESC LIMIT 1`, targetSnapshotID).Scan(&targetHistoryID, &targetHistoryRecordsRaw, &targetHistorySelectionsRaw); err != nil {
		t.Fatal(err)
	}
	if sourceHistoryID == targetHistoryID || targetHistoryID == uuid.Nil {
		t.Fatalf("publication history ID was not remapped: source=%s target=%s", sourceHistoryID, targetHistoryID)
	}
	var sourceHistoryRecords, targetHistoryRecords []sitebuilder.ModelContentRecord
	var sourceHistorySelections, targetHistorySelections []sitebuilder.ModelContentSelection
	if err := json.Unmarshal(sourceHistoryRecordsRaw, &sourceHistoryRecords); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(targetHistoryRecordsRaw, &targetHistoryRecords); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sourceHistorySelectionsRaw, &sourceHistorySelections); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(targetHistorySelectionsRaw, &targetHistorySelections); err != nil {
		t.Fatal(err)
	}
	historyRecordMap := sitepackCrossInstallAssertRecords(t, sourceHistoryRecords, targetHistoryRecords, sourceModel, *targetDetail.Payload.Config.DataModel, entityMap, relationMap, report.FileIDs, pathKeys)
	sitepackCrossInstallAssertSelections(t, sourceHistorySelections, targetHistorySelections, historyRecordMap)
	for oldID, newID := range report.FileIDs {
		if uuid.MustParse(oldID) == uuid.Nil || uuid.MustParse(newID) == uuid.Nil || oldID == newID {
			t.Fatalf("sitepack file ID was not remapped: %q -> %q", oldID, newID)
		}
	}
	if report.FileIDs[sourcePrivateFile.String()] == "" || report.FileIDs[sourcePublicFile.String()] == "" {
		t.Fatalf("sitepack file report omitted typed model media references: %v", report.FileIDs)
	}
	for _, pair := range []struct {
		sourceID   uuid.UUID
		visibility string
	}{{sourcePrivateFile, "private"}, {sourcePublicFile, "public"}} {
		targetID := uuid.MustParse(report.FileIDs[pair.sourceID.String()])
		var ownerID uuid.UUID
		var visibility, objectKey string
		if err := target.pool.QueryRow(ctx, `SELECT owner_id,visibility,object_key FROM media_objects WHERE id=$1 AND status='ready'`, targetID).Scan(&ownerID, &visibility, &objectKey); err != nil {
			t.Fatalf("read remapped target media metadata: %v", err)
		}
		if visibility != pair.visibility {
			t.Fatalf("media ACL changed during import: source=%s target=%s expected=%s actual=%s", pair.sourceID, targetID, pair.visibility, visibility)
		}
		var ownerKind string
		if err := target.pool.QueryRow(ctx, `SELECT kind FROM file_owners WHERE id=$1`, ownerID).Scan(&ownerKind); err != nil || ownerKind != "admin" {
			t.Fatalf("imported media must remain owned by the target admin library: owner=%s kind=%q err=%v", ownerID, ownerKind, err)
		}
		object, err := targetStore.Get(ctx, objectKey)
		if err != nil {
			t.Fatalf("read target MinIO media object: %v", err)
		}
		_, readErr := io.Copy(io.Discard, object)
		_ = object.Close()
		if readErr != nil {
			t.Fatalf("consume remapped target media bytes: %v", readErr)
		}
	}
	if err := targetStore.RequirePrivate(ctx); err != nil {
		t.Fatalf("target MinIO policy is not private after import: %v", err)
	}
	privateTargetID := report.FileIDs[sourcePrivateFile.String()]
	anonymousStatus, _ := sitepackCrossInstallPublicGET(t, target.devServer.URL+"/media/"+privateTargetID)
	if anonymousStatus != http.StatusNotFound {
		t.Fatalf("private media became anonymously accessible after import: status=%d", anonymousStatus)
	}
	publicStatus, publicBytes := sitepackCrossInstallPublicGET(t, target.devServer.URL+"/media/"+report.FileIDs[sourcePublicFile.String()])
	if publicStatus != http.StatusOK || len(publicBytes) == 0 {
		t.Fatalf("public media lost public access after import: status=%d bytes=%d", publicStatus, len(publicBytes))
	}
	var sourceParentUnchanged int
	if err := source.pool.QueryRow(ctx, `SELECT count(*) FROM site_snapshots WHERE id=$1 AND parent_snapshot_id IS NULL`, sourceParentID).Scan(&sourceParentUnchanged); err != nil || sourceParentUnchanged != 1 {
		t.Fatalf("source parent snapshot is missing or reparented after cross-install: rows=%d err=%v", sourceParentUnchanged, err)
	}
}

func sitepackCrossInstallAssertModelRemapped(t *testing.T, source, target sitebuilder.DataModel) (map[string]string, map[string]string, map[string]string) {
	t.Helper()
	entities, fields, relations := map[string]string{}, map[string]string{}, map[string]string{}
	targetEntities := map[string]sitebuilder.DataModelEntity{}
	for _, entity := range target.Entities {
		targetEntities[entity.Key] = entity
	}
	for _, sourceEntity := range source.Entities {
		targetEntity, ok := targetEntities[sourceEntity.Key]
		if !ok || targetEntity.ID == sourceEntity.ID || targetEntity.Name != sourceEntity.Name || len(targetEntity.Fields) != len(sourceEntity.Fields) {
			t.Fatalf("entity %q was not imported with a fresh generated ID: source=%+v target=%+v", sourceEntity.Key, sourceEntity, targetEntity)
		}
		entities[sourceEntity.ID] = targetEntity.ID
		fieldByKey := map[string]sitebuilder.DataModelField{}
		for _, field := range targetEntity.Fields {
			fieldByKey[field.Key] = field
		}
		for _, sourceField := range sourceEntity.Fields {
			targetField, ok := fieldByKey[sourceField.Key]
			if !ok || targetField.ID == sourceField.ID || targetField.Type != sourceField.Type || targetField.Options == nil && len(sourceField.Options) > 0 {
				t.Fatalf("field %q.%s was not imported with a fresh generated ID: source=%+v target=%+v", sourceEntity.Key, sourceField.Key, sourceField, targetField)
			}
			fields[sourceField.ID] = targetField.ID
		}
	}
	targetRelations := map[string]sitebuilder.DataModelRelation{}
	for _, relation := range target.Relations {
		targetRelations[relation.Name] = relation
	}
	for _, sourceRelation := range source.Relations {
		targetRelation, ok := targetRelations[sourceRelation.Name]
		if !ok || targetRelation.ID == sourceRelation.ID || targetRelation.FromEntityID != entities[sourceRelation.FromEntityID] || targetRelation.ToEntityID != entities[sourceRelation.ToEntityID] {
			t.Fatalf("relation %q was not remapped with its endpoint references: source=%+v target=%+v", sourceRelation.Name, sourceRelation, targetRelation)
		}
		relations[sourceRelation.ID] = targetRelation.ID
	}
	for entityID, position := range source.Positions {
		if target.Positions[entities[entityID]] != position {
			t.Fatalf("data-model canvas position did not follow remapped entity %s", entityID)
		}
	}
	return entities, fields, relations
}

func sitepackCrossInstallAssertRecords(t *testing.T, source, target []sitebuilder.ModelContentRecord, sourceModel, targetModel sitebuilder.DataModel, entityMap, relationMap, fileMap map[string]string, pathKeys map[string]string) map[string]string {
	t.Helper()
	if len(source) != len(target) {
		t.Fatalf("record count changed across sitepack import: source=%d target=%d", len(source), len(target))
	}
	entityKeys := map[string]string{}
	for _, entity := range sourceModel.Entities {
		entityKeys[entity.ID] = entity.Key
	}
	keyFor := func(record sitebuilder.ModelContentRecord, model sitebuilder.DataModel) string {
		entityKey := entityKeys[record.EntityID]
		if entityKey == "" {
			for _, entity := range model.Entities {
				if entity.ID == record.EntityID {
					entityKey = entity.Key
					break
				}
			}
		}
		field := ""
		switch entityKey {
		case "product":
			field = pathKeys["product:/slug"]
		case "category":
			field = "name"
		case "main_data_info":
			field = pathKeys["info:/title"]
		case "main_data_slider":
			field = pathKeys["slider:/title"]
		case "main_data_advantages":
			field = pathKeys["advantages:/title"]
		}
		if field == "" {
			return ""
		}
		return entityKey + ":" + fmt.Sprint(record.Values[field])
	}
	byKey := map[string]sitebuilder.ModelContentRecord{}
	for _, record := range target {
		key := keyFor(record, targetModel)
		if key == "" || byKey[key].ID != "" {
			t.Fatalf("target record has an empty/duplicate matching key: %+v", record)
		}
		byKey[key] = record
	}
	recordMap := map[string]string{}
	for _, old := range source {
		key := keyFor(old, sourceModel)
		current, ok := byKey[key]
		if !ok || old.ID == current.ID || current.EntityID != entityMap[old.EntityID] || old.SortOrder != current.SortOrder {
			t.Fatalf("record %s was not remapped with its entity and sort order: source=%+v target=%+v", key, old, current)
		}
		recordMap[old.ID] = current.ID
		expectedValues := make(map[string]any, len(old.Values))
		for fieldKey, value := range old.Values {
			expectedValues[fieldKey] = value
		}
		for _, entity := range sourceModel.Entities {
			if entity.ID != old.EntityID {
				continue
			}
			for _, field := range entity.Fields {
				if field.Type != "media" {
					continue
				}
				if raw, exists := expectedValues[field.Key]; exists && raw != nil && raw != "" {
					sourceFileID, isString := raw.(string)
					if !isString || fileMap[sourceFileID] == "" || current.Values[field.Key] != fileMap[sourceFileID] {
						t.Fatalf("typed media field %s was not remapped for record %s", field.Key, key)
					}
					expectedValues[field.Key] = fileMap[sourceFileID]
				}
			}
		}
		expectedRaw, _ := json.Marshal(expectedValues)
		actualRaw, _ := json.Marshal(current.Values)
		if !bytes.Equal(expectedRaw, actualRaw) {
			t.Fatalf("record values changed beyond generated media-ID remapping for %s: source=%s target=%s", key, expectedRaw, actualRaw)
		}
	}
	for _, old := range source {
		current := byKey[keyFor(old, sourceModel)]
		if len(old.Relations) != len(current.Relations) {
			t.Fatalf("record relation count changed during import for %s", old.ID)
		}
		for oldRelationID, oldTargetIDs := range old.Relations {
			newRelationID := relationMap[oldRelationID]
			newTargetIDs, exists := current.Relations[newRelationID]
			if !exists || len(newTargetIDs) != len(oldTargetIDs) {
				t.Fatalf("record relation %s was not imported under its remapped relation id", oldRelationID)
			}
			for index, oldTargetID := range oldTargetIDs {
				if newTargetIDs[index] != recordMap[oldTargetID] || newTargetIDs[index] == oldTargetID {
					t.Fatalf("record relation target %s was not remapped in stable order", oldTargetID)
				}
			}
		}
	}
	return recordMap
}

func sitepackCrossInstallPublicGET(t *testing.T, endpoint string) (int, []byte) {
	t.Helper()
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("GET target public media: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read target public media: %v", err)
	}
	return response.StatusCode, body
}

func sitepackCrossInstallAssertSelections(t *testing.T, source, target []sitebuilder.ModelContentSelection, recordMap map[string]string) {
	t.Helper()
	if len(source) != len(target) {
		t.Fatalf("editorial selection count changed across import: source=%d target=%d", len(source), len(target))
	}
	byKey := map[string]sitebuilder.ModelContentSelection{}
	for _, item := range target {
		byKey[item.PlacementID+"/"+item.InputKey] = item
	}
	for _, old := range source {
		current, ok := byKey[old.PlacementID+"/"+old.InputKey]
		if !ok || current.Mode != old.Mode || len(current.RecordIDs) != len(old.RecordIDs) {
			t.Fatalf("selection %s/%s changed during import: source=%+v target=%+v", old.PlacementID, old.InputKey, old, current)
		}
		for index, oldID := range old.RecordIDs {
			if current.RecordIDs[index] != recordMap[oldID] || current.RecordIDs[index] == oldID {
				t.Fatalf("selection order or generated record ID was not remapped for %s/%s", old.PlacementID, old.InputKey)
			}
		}
	}
}

func sitepackCrossInstallFieldCount(model sitebuilder.DataModel) int {
	count := 0
	for _, entity := range model.Entities {
		count += len(entity.Fields)
	}
	return count
}

func rewriteSitepackBuildArtifact(t *testing.T, source []byte, featureKey string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name], err = io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var index map[string]any
	if err := json.Unmarshal(contents["sitepack.json"], &index); err != nil {
		t.Fatal(err)
	}
	builds := index["builds"].([]any)
	selected := -1
	for position, raw := range builds {
		blob := raw.(map[string]any)
		oldPath := blob["path"].(string)
		archiveBytes := contents[oldPath]
		verified, err := sitebuilder.VerifyArchive(archiveBytes, int64(len(archiveBytes)))
		if err != nil {
			t.Fatal(err)
		}
		if verified.Manifest.Key != featureKey {
			continue
		}
		buildReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
		if err != nil {
			t.Fatal(err)
		}
		var changed bytes.Buffer
		buildWriter := zip.NewWriter(&changed)
		for _, buildFile := range buildReader.File {
			stream, err := buildFile.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(stream)
			_ = stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			if buildFile.Name == verified.Manifest.RuntimeEntry() {
				data = append(data, []byte("\n// cross-install immutability conflict\n")...)
			}
			entry, err := buildWriter.Create(buildFile.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := buildWriter.Close(); err != nil {
			t.Fatal(err)
		}
		conflicting, err := sitebuilder.VerifyArchive(changed.Bytes(), int64(changed.Len()))
		if err != nil || conflicting.Manifest.Key != verified.Manifest.Key || conflicting.Manifest.Version != verified.Manifest.Version || conflicting.Digest == verified.Digest {
			t.Fatalf("unable to create valid same-version archive conflict: manifest=%+v digest=%s err=%v", conflicting.Manifest, conflicting.Digest, err)
		}
		newPath := "builds/" + conflicting.Digest + ".zip"
		delete(contents, oldPath)
		contents[newPath] = changed.Bytes()
		blob["path"], blob["size"], blob["sha256"] = newPath, float64(changed.Len()), conflicting.Digest
		payload := index["payload"].(map[string]any)
		pinnedBuilds := payload["builds"].([]any)
		for _, rawPinned := range pinnedBuilds {
			pinned := rawPinned.(map[string]any)
			if pinned["key"] == featureKey {
				pinned["digest"] = conflicting.Digest
			}
		}
		selected = position
		break
	}
	if selected < 0 {
		t.Fatalf("sitepack has no %s build to conflict", featureKey)
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	contents["sitepack.json"] = indexBytes
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, data := range contents {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
