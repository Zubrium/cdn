package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/environment"
)

const (
	independentSitepackSourceDB = "website_engine_ind_sitepack_qa_20261108_root01_src"
	independentSitepackTargetDB = "website_engine_ind_sitepack_qa_20261108_root01_dst"
	independentSitepackBucket   = "independent-product-qa-sitepack-20261108-root01"
	independentSitepackEndpoint = "http://127.0.0.1:35573"
)

// TestIndependentProductSitepackCrossInstallAcceptance proves that a
// model-backed snapshot, synthetic feature builds, published and draft model
// content, ordered selections, and referenced media survive a cross-install
// Sitepack transfer. It intentionally has no dependency on Vitasil or external
// feature build directories.
func TestIndependentProductSitepackCrossInstallAcceptance(t *testing.T) {
	sourceURL := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_SITEPACK_SOURCE_DATABASE_URL"))
	targetURL := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_SITEPACK_TARGET_DATABASE_URL"))
	endpoint := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_SECRET_KEY"))
	if sourceURL == "" || targetURL == "" || endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("set the exact TEST_INDEPENDENT_SITEPACK_* database URLs and disposable TEST_INDEPENDENT_PRODUCT_S3_* MinIO settings")
	}
	if endpoint != independentSitepackEndpoint || bucket != independentSitepackBucket || accessKey != "qaindsitepack" || secretKey != "independent-product-sitepack-only-qa-secret" {
		t.Fatalf("refusing storage outside the task-owned loopback MinIO instance and bucket")
	}
	sourceName := requireIndependentSitepackDatabase(t, sourceURL, independentSitepackSourceDB)
	targetName := requireIndependentSitepackDatabase(t, targetURL, independentSitepackTargetDB)
	if sourceURL == targetURL || sourceName == targetName {
		t.Fatal("source and target must use separate PostgreSQL databases")
	}
	if accessKey != "qaindsitepack" {
		t.Fatal("refusing unexpected MinIO credentials")
	}
	// This helper ensures the unique bucket exists and enforces private ACL.
	targetStore := independentProductS3Store(t)
	t.Cleanup(func() {
		assertSitepackCrossInstallDatabaseIdle(t, sourceURL, sourceName)
		assertSitepackCrossInstallDatabaseIdle(t, targetURL, targetName)
	})

	ctx := context.Background()
	sourceStore := newFakeStore()
	t.Setenv("TEST_DATABASE_URL", sourceURL)
	source := newTestEnvWithObjectStore(t, sourceStore)

	manifests := independentProductManifests()
	verifiedManifests := map[string]sitebuilder.Manifest{}
	for _, key := range []string{"qa-catalog", "qa-product-detail", "qa-featured-products"} {
		archive := independentProductArchive(t, key, manifests[key])
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify locally assembled synthetic feature %s: %v", key, err)
		}
		if verified.Manifest.FormatVersion != 2 || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule || verified.Manifest.DataInputs == nil {
			t.Fatalf("synthetic feature %s is not an explicit format-2/static-module-v1 build: %+v", key, verified.Manifest)
		}
		status, body, _ := source.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", source.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register synthetic source feature %s: status=%d body=%v", key, status, body)
		}
		verifiedManifests[key] = verified.Manifest
	}
	if len(verifiedManifests) != 3 {
		t.Fatalf("expected three synthetic feature roles, registered %d", len(verifiedManifests))
	}

	mediaID := independentProductUploadMedia(t, source)
	var sourceObjectKey string
	if err := source.pool.QueryRow(ctx, `SELECT object_key FROM media_objects WHERE id=$1::uuid AND status='ready'`, mediaID).Scan(&sourceObjectKey); err != nil {
		t.Fatalf("read source synthetic media object key: %v", err)
	}
	sourceMediaReader, err := sourceStore.Get(ctx, sourceObjectKey)
	if err != nil {
		t.Fatalf("verify referenced media was uploaded to source fakeStore: %v", err)
	}
	sourceMedia, err := io.ReadAll(sourceMediaReader)
	_ = sourceMediaReader.Close()
	if err != nil || !bytes.Equal(sourceMedia, minimalPNG(t)) {
		t.Fatalf("source fakeStore media bytes differ from fixture: bytes=%d err=%v", len(sourceMedia), err)
	}

	ids := independentProductIDs{
		category: uuid.NewString(), categoryName: uuid.NewString(),
		product: uuid.NewString(), productSlug: uuid.NewString(), productTitle: uuid.NewString(), productPrice: uuid.NewString(),
		currency: uuid.NewString(), currencySymbol: uuid.NewString(),
		categoryProducts: uuid.NewString(), productCategory: uuid.NewString(),
	}
	siteConfig := independentProductSiteConfig(ids, mediaID)
	metadata := sitebuilder.SiteMetadata{
		FormatVersion: 1, SiteName: "Independent Sitepack QA", Description: "Synthetic cross-install acceptance",
		CanonicalDomain: "https://sitepack-independent.example.test", Language: "en",
		Pages: map[string]sitebuilder.SiteMetadataOverride{},
	}
	status, created, _ := source.request(http.MethodPost, "/api/v2/admin/site/snapshots", source.adminToken, sitebuilder.CreateSnapshotInput{
		Name: "Synthetic Sitepack source", Config: &siteConfig, Metadata: &metadata,
	})
	if status != http.StatusCreated {
		t.Fatalf("create synthetic source snapshot: status=%d body=%v", status, created)
	}
	sourceSnapshotID, err := uuid.Parse(created["id"].(string))
	if err != nil {
		t.Fatalf("parse source snapshot id: %v", err)
	}
	if got := sitepackCrossInstallBranchAssignment(t, source.pool, source.devBranch); got != nil {
		t.Fatalf("creating a source snapshot changed a branch assignment: %s", got)
	}
	contentURL := "/api/v2/admin/content/snapshots/" + sourceSnapshotID.String() + "/model"
	initialRecords := []sitebuilder.ModelContentRecord{
		{EntityID: ids.category, SortOrder: 0, Values: map[string]any{"name": "Pear category"}},
		{EntityID: ids.category, SortOrder: 1, Values: map[string]any{"name": "Apple category"}},
		{EntityID: ids.product, SortOrder: 0, Values: map[string]any{"slug": "pear", "title": "Pear", "price": 2.5}},
		{EntityID: ids.product, SortOrder: 1, Values: map[string]any{"slug": "apple", "title": "Apple", "price": 3.0}},
		{EntityID: ids.currency, SortOrder: 0, Values: map[string]any{"symbol": "€"}},
	}
	status, firstDraft, _ := source.request(http.MethodPut, contentURL, source.adminToken, sitebuilder.ModelContentDraftInput{Revision: 0, Records: initialRecords})
	if status != http.StatusOK || firstDraft["revision"] != float64(1) || firstDraft["publishedRevision"] != float64(0) {
		t.Fatalf("save initial synthetic records: status=%d body=%v", status, firstDraft)
	}
	records := independentProductDecodeRecords(t, firstDraft["records"])
	categoryByName := map[string]string{}
	productBySlug := map[string]string{}
	currencyID := ""
	for _, record := range records {
		switch record.EntityID {
		case ids.category:
			categoryByName[record.Values["name"].(string)] = record.ID
		case ids.product:
			productBySlug[record.Values["slug"].(string)] = record.ID
		case ids.currency:
			currencyID = record.ID
		}
	}
	if categoryByName["Pear category"] == "" || categoryByName["Apple category"] == "" || productBySlug["pear"] == "" || productBySlug["apple"] == "" || currencyID == "" {
		t.Fatalf("engine did not generate all synthetic record IDs: %+v", records)
	}
	for index := range records {
		if records[index].EntityID == ids.category {
			if records[index].Values["name"] == "Pear category" {
				records[index].Relations = map[string][]string{ids.categoryProducts: {productBySlug["pear"]}}
			} else {
				records[index].Relations = map[string][]string{ids.categoryProducts: {productBySlug["apple"]}}
			}
		}
		if records[index].EntityID == ids.product {
			if records[index].Values["slug"] == "pear" {
				records[index].Relations = map[string][]string{ids.productCategory: {categoryByName["Pear category"]}}
			} else {
				records[index].Relations = map[string][]string{ids.productCategory: {categoryByName["Apple category"]}}
			}
		}
	}
	selections := []sitebuilder.ModelContentSelection{
		{PlacementID: "qa-featured-products-placement", InputKey: "products", Mode: "ids", RecordIDs: []string{productBySlug["apple"], productBySlug["pear"]}},
		{PlacementID: "qa-catalog-placement", InputKey: "currency", Mode: "ids", RecordIDs: []string{currencyID}},
	}
	status, linkedDraft, _ := source.request(http.MethodPut, contentURL, source.adminToken, sitebuilder.ModelContentDraftInput{Revision: 1, Records: records, Selections: selections})
	if status != http.StatusOK || linkedDraft["revision"] != float64(2) {
		t.Fatalf("save synthetic relationships and selections: status=%d body=%v", status, linkedDraft)
	}
	status, published, _ := source.request(http.MethodPost, contentURL+"/publish", source.adminToken, map[string]any{"revision": 2})
	if status != http.StatusOK || published["publishedRevision"] != float64(2) {
		t.Fatalf("publish synthetic content revision: status=%d body=%v", status, published)
	}
	// Keep a separate unpublished edit and a different draft selection order.
	for index := range records {
		if records[index].EntityID == ids.product && records[index].Values["slug"] == "pear" {
			records[index].Values["title"] = "Pear draft only"
		}
	}
	draftSelections := []sitebuilder.ModelContentSelection{
		{PlacementID: "qa-featured-products-placement", InputKey: "products", Mode: "ids", RecordIDs: []string{productBySlug["pear"], productBySlug["apple"]}},
		{PlacementID: "qa-catalog-placement", InputKey: "currency", Mode: "ids", RecordIDs: []string{currencyID}},
	}
	status, draft, _ := source.request(http.MethodPut, contentURL, source.adminToken, sitebuilder.ModelContentDraftInput{Revision: 2, Records: records, Selections: draftSelections})
	if status != http.StatusOK || draft["revision"] != float64(3) || draft["publishedRevision"] != float64(2) {
		t.Fatalf("save unpublished synthetic draft: status=%d body=%v", status, draft)
	}
	if assigned := sitepackCrossInstallBranchAssignment(t, source.pool, source.devBranch); assigned != nil {
		t.Fatalf("source content writes assigned a branch unexpectedly: %s", assigned)
	}

	archive, headers := sitepackCrossInstallExport(t, source.server.URL+"/api/v2/admin/site/snapshots/"+sourceSnapshotID.String()+"/export", source.adminToken)
	if headers.Get("Content-Type") != "application/zip" || !strings.Contains(headers.Get("Cache-Control"), "no-store") {
		t.Fatalf("unexpected synthetic export headers: %v", headers)
	}

	t.Setenv("TEST_DATABASE_URL", targetURL)
	target := newTestEnvWithObjectStore(t, targetStore)
	for _, assignment := range []environmentBranchForIndependentSitepack{{target.devBranch, "development"}, {target.prodBranch, "production"}} {
		if got := sitepackCrossInstallBranchAssignment(t, target.pool, assignment.branch); got != nil {
			t.Fatalf("fresh target %s branch unexpectedly has an assignment: %s", assignment.name, got)
		}
	}
	status, importBody, _ := target.rawRequest(http.MethodPost, "/api/v2/admin/site/snapshots/import", target.adminToken, bytes.NewReader(archive), "application/zip")
	if status != http.StatusCreated {
		t.Fatalf("import synthetic sitepack into separate target: status=%d body=%v", status, importBody)
	}
	report := sitepackCrossInstallDecodeReport(t, importBody)
	if report.Snapshot.ID == uuid.Nil || report.Snapshot.ParentID != nil || report.NewBuilds != 3 || report.ReusedBuilds != 0 || report.NewFiles != 1 || report.ReusedFiles != 0 ||
		report.ModelPublishedRecords != len(initialRecords) || report.ModelDraftRecords != len(initialRecords) || report.ModelHistoryRevisions != 1 ||
		report.RemappedModelEntityIDs != len(siteConfig.DataModel.Entities) || report.RemappedModelRelationIDs != len(siteConfig.DataModel.Relations) || report.RemappedModelRecordIDs != len(initialRecords) || len(report.FileIDs) != 1 {
		t.Fatalf("sitepack report omitted synthetic builds/model/content/media or identity remapping: %+v", report)
	}
	if report.RemappedModelFieldIDs != independentSitepackFieldCount(*siteConfig.DataModel) {
		t.Fatalf("sitepack did not remap all model field IDs: %+v", report)
	}
	if _, ok := report.FileIDs[mediaID]; !ok || report.FileIDs[mediaID] == mediaID {
		t.Fatalf("referenced media file ID was not remapped: %v", report.FileIDs)
	}
	if assigned := sitepackCrossInstallBranchAssignment(t, target.pool, target.devBranch); assigned != nil {
		t.Fatalf("Sitepack import automatically assigned target development branch: %s", assigned)
	}
	if assigned := sitepackCrossInstallBranchAssignment(t, target.pool, target.prodBranch); assigned != nil {
		t.Fatalf("Sitepack import automatically assigned target production branch: %s", assigned)
	}

	targetSnapshotID := report.Snapshot.ID
	var targetPayloadRaw []byte
	if err := target.pool.QueryRow(ctx, `SELECT payload FROM site_snapshots WHERE id=$1`, targetSnapshotID).Scan(&targetPayloadRaw); err != nil {
		t.Fatalf("read imported snapshot payload: %v", err)
	}
	var targetPayload sitebuilder.SnapshotPayload
	if err := json.Unmarshal(targetPayloadRaw, &targetPayload); err != nil || targetPayload.Config.DataModel == nil {
		t.Fatalf("decode imported model payload: err=%v payload=%s", err, targetPayloadRaw)
	}
	if !independentSitepackModelIDsWereRemapped(*siteConfig.DataModel, *targetPayload.Config.DataModel) {
		t.Fatal("target model entity, field, or relation identifiers overlap source identities or change shape")
	}
	workspace := sitepackCrossInstallReadWorkspace(t, target.pool, targetSnapshotID)
	if workspace.Revision != 3 || workspace.PublishedRevision != 2 || len(workspace.DraftRecords) != len(initialRecords) || len(workspace.PublishedRecords) != len(initialRecords) || len(workspace.DraftSelections) != len(draftSelections) || len(workspace.PublishedSelections) != len(selections) {
		t.Fatalf("imported draft/published workspace state differs from source: %+v", workspace)
	}
	if !independentSitepackContentIDsWereRemapped(records, workspace.DraftRecords, ids) {
		t.Fatal("target content retained source record, entity, or relation IDs")
	}
	if independentSitepackTitleBySlug(workspace.DraftRecords, "pear") != "Pear draft only" || independentSitepackTitleBySlug(workspace.PublishedRecords, "pear") != "Pear" {
		t.Fatalf("published and draft values were not independently preserved: draft=%+v published=%+v", workspace.DraftRecords, workspace.PublishedRecords)
	}
	if !independentSitepackSelectionOrder(workspace.PublishedSelections, "qa-featured-products-placement", "products", "apple", "pear", workspace.PublishedRecords) ||
		!independentSitepackSelectionOrder(workspace.DraftSelections, "qa-featured-products-placement", "products", "pear", "apple", workspace.DraftRecords) {
		t.Fatalf("published and draft selection orders were not independently preserved: draft=%+v published=%+v", workspace.DraftSelections, workspace.PublishedSelections)
	}

	targetMediaID := report.FileIDs[mediaID]
	var targetObjectKey, targetDigest string
	if err := target.pool.QueryRow(ctx, `SELECT object_key,sha256 FROM media_objects WHERE id=$1::uuid AND status='ready'`, targetMediaID).Scan(&targetObjectKey, &targetDigest); err != nil {
		t.Fatalf("read imported target media row: %v", err)
	}
	targetMediaReader, err := targetStore.Get(ctx, targetObjectKey)
	if err != nil {
		t.Fatalf("read imported media from target private MinIO bucket: %v", err)
	}
	targetMediaBytes, err := io.ReadAll(targetMediaReader)
	_ = targetMediaReader.Close()
	actualDigest := sha256.Sum256(targetMediaBytes)
	if err != nil || !bytes.Equal(targetMediaBytes, sourceMedia) || targetDigest != hex.EncodeToString(actualDigest[:]) {
		t.Fatalf("target media bytes/checksum differ from source: size=%d digest=%s err=%v", len(targetMediaBytes), targetDigest, err)
	}

	// A snapshot is assigned only by the existing explicit branch API.
	assignSnapshotToBranch(t, target, string(target.devBranch), targetSnapshotID.String())
	if assigned := sitepackCrossInstallBranchAssignment(t, target.pool, target.devBranch); assigned == nil || *assigned != targetSnapshotID {
		t.Fatalf("explicit branch API assignment did not persist: %v", assigned)
	}
	if assigned := sitepackCrossInstallBranchAssignment(t, target.pool, target.prodBranch); assigned != nil {
		t.Fatalf("explicitly assigning development also changed production: %s", assigned)
	}
	assertIndependentProductPublishedRuntime(t, target, targetMediaID)
	assertIndependentSitepackDetailProjection(t, target, targetSnapshotID)
	t.Logf("synthetic Sitepack cross-install passed: source=%s target=%s targetBucket=%s; %d builds, %d records in each revision, %d file remapped; runtime passed after explicit branch API assignment", sourceName, targetName, bucket, report.NewBuilds, report.ModelDraftRecords, report.NewFiles)
}

type environmentBranchForIndependentSitepack struct {
	branch environment.Environment
	name   string
}

func requireIndependentSitepackDatabase(t *testing.T, rawURL, expected string) string {
	t.Helper()
	parsed, err := pgx.ParseConfig(rawURL)
	if err != nil {
		t.Fatalf("parse disposable database URL: %v", err)
	}
	ip := net.ParseIP(parsed.Host)
	if parsed.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing non-loopback Sitepack QA database host %q", parsed.Host)
	}
	if parsed.Database != expected {
		t.Fatalf("refusing database %q; this test owns only %q", parsed.Database, expected)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatalf("connect to exact disposable database %s: %v", expected, err)
	}
	defer connection.Close(context.Background())
	var current string
	var sessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(),(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&current, &sessions); err != nil {
		t.Fatalf("verify disposable database identity and sessions: %v", err)
	}
	if current != expected || sessions != 0 {
		t.Fatalf("refusing database reset: current=%q expected=%q otherSessions=%d", current, expected, sessions)
	}
	t.Logf("verified exact disposable database %s has zero other sessions", expected)
	return current
}

func independentSitepackFieldCount(model sitebuilder.DataModel) int {
	count := 0
	for _, entity := range model.Entities {
		count += len(entity.Fields)
	}
	return count
}

func independentSitepackModelIDsWereRemapped(source, target sitebuilder.DataModel) bool {
	if len(source.Entities) != len(target.Entities) || len(source.Relations) != len(target.Relations) || independentSitepackFieldCount(source) != independentSitepackFieldCount(target) {
		return false
	}
	targetIDs := map[string]bool{}
	for _, entity := range target.Entities {
		targetIDs[entity.ID] = true
		for _, field := range entity.Fields {
			targetIDs[field.ID] = true
		}
	}
	for _, relation := range target.Relations {
		targetIDs[relation.ID] = true
	}
	for _, entity := range source.Entities {
		if targetIDs[entity.ID] {
			return false
		}
		for _, field := range entity.Fields {
			if targetIDs[field.ID] {
				return false
			}
		}
	}
	for _, relation := range source.Relations {
		if targetIDs[relation.ID] {
			return false
		}
	}
	return true
}

func independentSitepackContentIDsWereRemapped(source, target []sitebuilder.ModelContentRecord, sourceIDs independentProductIDs) bool {
	if len(source) != len(target) {
		return false
	}
	sourceRecordIDs := map[string]bool{}
	for _, record := range source {
		sourceRecordIDs[record.ID] = true
	}
	targetEntities := map[string]bool{}
	for _, record := range target {
		if record.ID == "" || sourceRecordIDs[record.ID] || record.EntityID == sourceIDs.category || record.EntityID == sourceIDs.product || record.EntityID == sourceIDs.currency {
			return false
		}
		targetEntities[record.EntityID] = true
		for relationID, ids := range record.Relations {
			if relationID == sourceIDs.categoryProducts || relationID == sourceIDs.productCategory {
				return false
			}
			if len(ids) == 0 {
				return false
			}
		}
	}
	return len(targetEntities) == 3
}

func independentSitepackTitleBySlug(records []sitebuilder.ModelContentRecord, slug string) string {
	for _, record := range records {
		if record.Values["slug"] == slug {
			if title, ok := record.Values["title"].(string); ok {
				return title
			}
		}
	}
	return ""
}

func independentSitepackSelectionOrder(selections []sitebuilder.ModelContentSelection, placementID, inputKey, firstSlug, secondSlug string, records []sitebuilder.ModelContentRecord) bool {
	for _, selection := range selections {
		if selection.PlacementID != placementID || selection.InputKey != inputKey {
			continue
		}
		if len(selection.RecordIDs) != 2 {
			return false
		}
		byID := map[string]string{}
		for _, record := range records {
			if slug, ok := record.Values["slug"].(string); ok {
				byID[record.ID] = slug
			}
		}
		return byID[selection.RecordIDs[0]] == firstSlug && byID[selection.RecordIDs[1]] == secondSlug
	}
	return false
}

func assertIndependentSitepackDetailProjection(t *testing.T, env *testEnv, snapshotID uuid.UUID) {
	t.Helper()
	// The shared synthetic assertion covers catalog, nested relationships,
	// selected order, typed arguments, and the media file ID after import.
	_ = snapshotID
	composition := independentProductRuntime(t, env, "/products/apple")
	placement := independentProductFindPlacement(t, composition, "qa-product-detail-placement")
	content, ok := placement["content"].(map[string]any)["product"].(map[string]any)
	if !ok || content["title"] != "Apple" || content["category"].(map[string]any)["name"] != "Apple category" {
		t.Fatalf("imported detail runtime did not resolve the remapped Product and Category: %v", placement)
	}
}
