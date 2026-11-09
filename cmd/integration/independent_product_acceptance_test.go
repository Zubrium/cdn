package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
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
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	publicruntime "websiteengine/pkg/runtime"
	"websiteengine/pkg/storage"
	s3store "websiteengine/pkg/storage/s3"
)

// These ESM builds are intentionally local fixtures. This acceptance must not
// depend on TEST_FEATURE_DIR, a feature registry, or a product-specific site.
//
//go:embed fixtures/independent_product/*
var independentProductFixtures embed.FS

const independentProductDBPrefix = "website_engine_independent_product_qa_"

type independentProductIDs struct {
	category         string
	categoryName     string
	product          string
	productSlug      string
	productTitle     string
	productPrice     string
	currency         string
	currencySymbol   string
	categoryProducts string
	productCategory  string
}

// TestIndependentProductSyntheticAcceptance exercises the format-2 runtime and
// model-backed content contract using three synthetic, locally assembled ZIPs.
// It intentionally requires its own local QA database because newTestEnv
// migrates and truncates integration tables.
func TestIndependentProductSyntheticAcceptance(t *testing.T) {
	runIndependentProductSyntheticAcceptance(t, nil)
}

func TestIndependentProductSyntheticAcceptanceWithS3(t *testing.T) {
	store := independentProductS3Store(t)
	runIndependentProductSyntheticAcceptance(t, store)
}

func runIndependentProductSyntheticAcceptance(t *testing.T, objectStore storage.ObjectStore) {
	requireUniqueIndependentProductDatabase(t)
	env := newTestEnvWithObjectStore(t, objectStore)
	ctx := context.Background()
	independentProductEnableIndexing(t, env)

	mediaID := independentProductUploadMedia(t, env)
	var objectKey string
	if err := env.pool.QueryRow(ctx, `SELECT object_key FROM media_objects WHERE id=$1::uuid`, mediaID).Scan(&objectKey); err != nil {
		t.Fatalf("find stored synthetic media object: %v", err)
	}
	stored, err := env.objectStore.Get(ctx, objectKey)
	if err != nil {
		t.Fatalf("read synthetic media from configured object storage: %v", err)
	}
	storedBytes, err := io.ReadAll(stored)
	_ = stored.Close()
	if err != nil || !bytes.Equal(storedBytes, minimalPNG(t)) {
		t.Fatalf("uploaded synthetic media did not round-trip through object storage: bytes=%d err=%v", len(storedBytes), err)
	}
	if _, ok := env.objectStore.(*fakeStore); ok {
		t.Log("synthetic media upload round-tripped through the integration fakeStore")
	} else {
		t.Log("synthetic media upload round-tripped through the configured disposable S3-compatible object store")
	}

	ids := independentProductIDs{
		category: uuid.NewString(), categoryName: uuid.NewString(),
		product: uuid.NewString(), productSlug: uuid.NewString(), productTitle: uuid.NewString(), productPrice: uuid.NewString(),
		currency: uuid.NewString(), currencySymbol: uuid.NewString(),
		categoryProducts: uuid.NewString(), productCategory: uuid.NewString(),
	}
	manifests := independentProductManifests()
	for _, key := range []string{"qa-catalog", "qa-product-detail", "qa-featured-products"} {
		archive := independentProductArchive(t, key, manifests[key])
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify locally assembled %s ZIP: %v", key, err)
		}
		if verified.Manifest.FormatVersion != 2 || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
			t.Fatalf("synthetic feature %s does not use format 2/static-module-v1: %+v", key, verified.Manifest)
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register synthetic feature %s: status=%d body=%v", key, status, body)
		}
		if body["key"] != key || body["version"] != "1.0.0" {
			t.Fatalf("Engine registered an unexpected synthetic build identity: %v", body)
		}
	}

	config := independentProductSiteConfig(ids, mediaID)
	metadata := sitebuilder.SiteMetadata{
		FormatVersion: 1, SiteName: "Independent Product QA", Description: "Synthetic Engine acceptance",
		CanonicalDomain: "https://independent-product.example.test", Language: "en",
		Pages: map[string]sitebuilder.SiteMetadataOverride{},
	}
	status, root, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, sitebuilder.CreateSnapshotInput{
		Name: "Synthetic Product Model", Config: &config, Metadata: &metadata,
	})
	if status != http.StatusCreated {
		t.Fatalf("create root synthetic snapshot: status=%d body=%v", status, root)
	}
	rootID := root["id"].(string)
	assignSnapshotToBranch(t, env, string(env.devBranch), rootID)
	branchID := uuid.MustParse(string(env.devBranch))
	rootUUID := uuid.MustParse(rootID)
	branch, err := env.snapshots.Branch(ctx, branchID)
	if err != nil || branch.SnapshotID == nil || *branch.SnapshotID != rootUUID {
		t.Fatalf("explicit initial branch assignment was not retained: branch=%+v err=%v", branch, err)
	}
	prodBranch, err := env.snapshots.Branch(ctx, uuid.MustParse(string(env.prodBranch)))
	if err != nil || prodBranch.SnapshotID != nil {
		t.Fatalf("second branch should remain unassigned: branch=%+v err=%v", prodBranch, err)
	}

	// A rejected structural save must be atomic. Each invalid child keeps the
	// original parent payload and the root branch assignment untouched.
	invalidBindingCases := []struct {
		name   string
		mutate func(*sitebuilder.SiteConfig)
	}{
		{
			name: "wrong field type",
			mutate: func(candidate *sitebuilder.SiteConfig) {
				placement := independentProductPlacement(candidate, "qa-catalog-placement")
				for index := range placement.DataBindings[0].Fields {
					if placement.DataBindings[0].Fields[index].InputPath == "/name" {
						placement.DataBindings[0].Fields[index].FieldID = ids.productPrice
						return
					}
				}
			},
		},
		{
			name: "relation traversed backwards",
			mutate: func(candidate *sitebuilder.SiteConfig) {
				placement := independentProductPlacement(candidate, "qa-product-detail-placement")
				for index := range placement.DataBindings[0].Fields {
					if placement.DataBindings[0].Fields[index].InputPath == "/category/name" {
						placement.DataBindings[0].Fields[index].RelationIDs = []string{ids.categoryProducts}
						return
					}
				}
			},
		},
		{
			name: "wildcard without a multi-target relation",
			mutate: func(candidate *sitebuilder.SiteConfig) {
				placement := independentProductPlacement(candidate, "qa-catalog-placement")
				for index := range placement.DataBindings[0].Fields {
					if placement.DataBindings[0].Fields[index].InputPath == "/products/*/title" {
						placement.DataBindings[0].Fields[index].RelationIDs = nil
						return
					}
				}
			},
		},
	}
	baseSnapshotCount := independentProductSnapshotCount(t, env)
	for _, invalid := range invalidBindingCases {
		t.Run("rejects "+invalid.name, func(t *testing.T) {
			candidate := independentProductCloneConfig(t, config)
			invalid.mutate(&candidate)
			parentID := rootUUID
			status, response, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, sitebuilder.CreateSnapshotInput{
				Name: "Invalid " + invalid.name, ParentID: &parentID, Config: &candidate,
			})
			if status != http.StatusBadRequest {
				t.Fatalf("invalid binding should be rejected before snapshot creation: status=%d body=%v", status, response)
			}
			if count := independentProductSnapshotCount(t, env); count != baseSnapshotCount {
				t.Fatalf("rejected binding partially created a snapshot: before=%d after=%d", baseSnapshotCount, count)
			}
			assertIndependentProductBranch(t, env, rootUUID)
		})
	}

	userStatus, userBody, _ := env.request(http.MethodPost, "/api/v1/admin/tokens", env.adminToken, map[string]any{
		"name": "independent product editor", "role": "user",
	})
	if userStatus != http.StatusCreated {
		t.Fatalf("create content user: status=%d body=%v", userStatus, userBody)
	}
	userToken := userBody["token"].(string)
	base := "/api/v2/admin/content/snapshots/" + rootID + "/model"
	status, empty, _ := env.request(http.MethodGet, base, userToken, nil)
	if status != http.StatusOK || empty["revision"] != float64(0) {
		t.Fatalf("new model workspace: status=%d body=%v", status, empty)
	}

	pearCategory := sitebuilder.ModelContentRecord{EntityID: ids.category, SortOrder: 0, Values: map[string]any{"name": "Pear category"}}
	appleCategory := sitebuilder.ModelContentRecord{EntityID: ids.category, SortOrder: 1, Values: map[string]any{"name": "Apple category"}}
	pear := sitebuilder.ModelContentRecord{EntityID: ids.product, SortOrder: 0, Values: map[string]any{"slug": "pear", "title": "Pear", "price": 2.5}}
	apple := sitebuilder.ModelContentRecord{EntityID: ids.product, SortOrder: 1, Values: map[string]any{"slug": "apple", "title": "Apple", "price": 3.0}}
	currency := sitebuilder.ModelContentRecord{EntityID: ids.currency, SortOrder: 0, Values: map[string]any{"symbol": "€"}}
	status, firstDraft, _ := env.request(http.MethodPut, base, userToken, sitebuilder.ModelContentDraftInput{
		Revision: 0, Records: []sitebuilder.ModelContentRecord{pearCategory, appleCategory, pear, apple, currency},
	})
	if status != http.StatusOK || firstDraft["revision"] != float64(1) || firstDraft["publishedRevision"] != float64(0) {
		t.Fatalf("save independent model draft: status=%d body=%v", status, firstDraft)
	}
	records := independentProductDecodeRecords(t, firstDraft["records"])
	categoryByName := map[string]string{}
	productBySlug := map[string]string{}
	currencyID := ""
	for _, record := range records {
		if record.EntityID == ids.category {
			categoryByName[record.Values["name"].(string)] = record.ID
		} else if record.EntityID == ids.product {
			productBySlug[record.Values["slug"].(string)] = record.ID
		} else if record.EntityID == ids.currency {
			currencyID = record.ID
		}
	}
	if categoryByName["Pear category"] == "" || categoryByName["Apple category"] == "" || productBySlug["pear"] == "" || productBySlug["apple"] == "" || currencyID == "" {
		t.Fatalf("content workspace did not generate stable record IDs: %+v", records)
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
	selections := []sitebuilder.ModelContentSelection{{
		PlacementID: "qa-featured-products-placement", InputKey: "products", Mode: "ids",
		RecordIDs: []string{productBySlug["apple"], productBySlug["pear"]},
	}, {
		PlacementID: "qa-catalog-placement", InputKey: "currency", Mode: "ids", RecordIDs: []string{currencyID},
	}}
	status, linkedDraft, _ := env.request(http.MethodPut, base, userToken, sitebuilder.ModelContentDraftInput{
		Revision: 1, Records: records, Selections: selections,
	})
	if status != http.StatusOK || linkedDraft["revision"] != float64(2) || linkedDraft["publishedRevision"] != float64(0) {
		t.Fatalf("save nested relations and editorial selection: status=%d body=%v", status, linkedDraft)
	}

	// Neither the browser runtime API nor the SEO index may expose draft values.
	assertIndependentProductRuntimeDraftHidden(t, env, "/")
	branchForSEO, err := env.snapshots.Branch(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	branchForSEO.Indexable = true // HTTP policy for this isolated read-only QA server only.
	seoServer := seoPublicServer(t, env, branchForSEO, nil)
	status, prePublishSitemap, _ := seoGet(t, seoServer, "/sitemap.xml")
	if status != http.StatusOK || strings.Contains(prePublishSitemap, "/products/apple") || strings.Contains(prePublishSitemap, "/products/pear") {
		t.Fatalf("sitemap exposed draft detail records: status=%d body=%s", status, prePublishSitemap)
	}

	status, published, _ := env.request(http.MethodPost, base+"/publish", userToken, map[string]any{"revision": 2})
	if status != http.StatusOK || published["publishedRevision"] != float64(2) {
		t.Fatalf("explicitly publish model content as user: status=%d body=%v", status, published)
	}
	assertIndependentProductPublishedRuntime(t, env, mediaID)
	status, sitemap, _ := seoGet(t, seoServer, "/sitemap.xml")
	if status != http.StatusOK || !strings.Contains(sitemap, "https://independent-product.example.test/products/apple") || !strings.Contains(sitemap, "https://independent-product.example.test/products/pear") {
		t.Fatalf("published unique slugs are not represented in sitemap: status=%d body=%s", status, sitemap)
	}
	status, canonicalDetail, headers := seoGet(t, seoServer, "/products/apple")
	if status != http.StatusOK || !strings.Contains(canonicalDetail, "https://independent-product.example.test/products/apple") || headers.Get("ETag") == "" {
		t.Fatalf("published detail page SEO response is incomplete: status=%d headers=%v body=%s", status, headers, canonicalDetail)
	}
	initialETag := headers.Get("ETag")
	if status, notModified := independentProductConditionalGet(t, seoServer, "/products/apple", initialETag); status != http.StatusNotModified || notModified.Get("ETag") != initialETag {
		t.Fatalf("unchanged detail page should return 304: status=%d headers=%v", status, notModified)
	}
	queryStatus, _, queryHeaders := seoGet(t, seoServer, "/products/apple?source=qa")
	if queryStatus != http.StatusOK || queryHeaders.Get("ETag") != "" || queryHeaders.Get("Cache-Control") != "no-store" {
		t.Fatalf("query variant should not receive a public validator: status=%d headers=%v", queryStatus, queryHeaders)
	}

	// Saving a newer draft leaves public output and cache identity unchanged.
	changedDraftRecords := independentProductCloneRecords(t, linkedDraft["records"])
	independentProductRecordByID(t, changedDraftRecords, productBySlug["pear"]).Values["title"] = "Draft Pear"
	status, thirdDraft, _ := env.request(http.MethodPut, base, userToken, sitebuilder.ModelContentDraftInput{
		Revision: 2, Records: changedDraftRecords, Selections: selections,
	})
	if status != http.StatusOK || thirdDraft["revision"] != float64(3) {
		t.Fatalf("save newer unpublished draft: status=%d body=%v", status, thirdDraft)
	}
	assertIndependentProductRuntimeText(t, env, "/", "Pear", "Draft Pear")
	_, unchangedDraftPage, unchangedDraftHeaders := seoGet(t, seoServer, "/products/apple")
	if unchangedDraftHeaders.Get("ETag") != initialETag || strings.Contains(unchangedDraftPage, "Draft Pear") {
		t.Fatalf("draft changed the public HTML validator or leaked into the shell: headers=%v body=%s", unchangedDraftHeaders, unchangedDraftPage)
	}
	status, secondPublish, _ := env.request(http.MethodPost, base+"/publish", userToken, map[string]any{"revision": 3})
	if status != http.StatusOK || secondPublish["publishedRevision"] != float64(3) {
		t.Fatalf("publish updated model revision: status=%d body=%v", status, secondPublish)
	}
	assertIndependentProductRuntimeText(t, env, "/", "Draft Pear", "Pear")
	_, updatedDetailPage, updatedDetailHeaders := seoGet(t, seoServer, "/products/apple")
	if updatedDetailHeaders.Get("ETag") == initialETag || !strings.Contains(updatedDetailPage, "Independent Product QA") {
		t.Fatalf("publish did not invalidate the public HTML validator: headers=%v body=%s", updatedDetailHeaders, updatedDetailPage)
	}

	// Restore the first publication into a new draft, then publish separately.
	status, history, _ := env.request(http.MethodGet, base+"/history", userToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read content history: status=%d body=%v", status, history)
	}
	historyItems, _ := history["items"].([]any)
	if len(historyItems) != 2 {
		t.Fatalf("expected two independent publications in history, got %v", history)
	}
	firstPublicationID := historyItems[len(historyItems)-1].(map[string]any)["id"].(string)
	status, restored, _ := env.request(http.MethodPost, base+"/history/"+firstPublicationID+"/restore", userToken, map[string]any{"revision": 3})
	if status != http.StatusOK || restored["revision"] != float64(4) || restored["publishedRevision"] != float64(3) {
		t.Fatalf("history restore must create an unpublished draft: status=%d body=%v", status, restored)
	}
	assertIndependentProductRuntimeText(t, env, "/", "Draft Pear", "Pear")
	status, rollbackPublish, _ := env.request(http.MethodPost, base+"/publish", userToken, map[string]any{"revision": 4})
	if status != http.StatusOK || rollbackPublish["publishedRevision"] != float64(4) {
		t.Fatalf("publish restored content revision: status=%d body=%v", status, rollbackPublish)
	}
	assertIndependentProductRuntimeText(t, env, "/", "Pear", "Draft Pear")
	_, rolledBackPage, rolledBackHeaders := seoGet(t, seoServer, "/products/apple")
	if rolledBackHeaders.Get("ETag") == updatedDetailHeaders.Get("ETag") || !strings.Contains(rolledBackPage, "Independent Product QA") {
		t.Fatalf("content rollback did not refresh the public page validator: headers=%v body=%s", rolledBackHeaders, rolledBackPage)
	}

	// A structural fork copies draft and published content but stays independent
	// and does not silently assign the child to either configured branch.
	child := modelAcceptanceCreateChildSnapshot(t, env, rootID, "Independent Product Child")
	childID := child["id"].(string)
	childBase := "/api/v2/admin/content/snapshots/" + childID + "/model"
	status, childWorkspace, _ := env.request(http.MethodGet, childBase, userToken, nil)
	if status != http.StatusOK || childWorkspace["revision"] != float64(4) || childWorkspace["publishedRevision"] != float64(4) {
		t.Fatalf("child snapshot did not copy both model revisions: status=%d body=%v", status, childWorkspace)
	}
	childRecords := independentProductCloneRecords(t, childWorkspace["records"])
	independentProductRecordByID(t, childRecords, productBySlug["pear"]).Values["title"] = "Child Pear"
	childSelections := independentProductDecodeSelections(t, childWorkspace["selections"])
	status, childDraft, _ := env.request(http.MethodPut, childBase, userToken, sitebuilder.ModelContentDraftInput{
		Revision: 4, Records: childRecords, Selections: childSelections,
	})
	if status != http.StatusOK || childDraft["revision"] != float64(5) {
		t.Fatalf("edit child snapshot records: status=%d body=%v", status, childDraft)
	}
	status, childPublished, _ := env.request(http.MethodPost, childBase+"/publish", userToken, map[string]any{"revision": 5})
	if status != http.StatusOK || childPublished["publishedRevision"] != float64(5) {
		t.Fatalf("publish child snapshot records: status=%d body=%v", status, childPublished)
	}
	status, parentAfterFork, _ := env.request(http.MethodGet, base, userToken, nil)
	if status != http.StatusOK || parentAfterFork["revision"] != float64(4) || parentAfterFork["publishedRevision"] != float64(4) {
		t.Fatalf("child edits changed parent workspace revision: status=%d body=%v", status, parentAfterFork)
	}
	if got := independentProductRecordByID(t, independentProductDecodeRecords(t, parentAfterFork["records"]), productBySlug["pear"]).Values["title"]; got != "Pear" {
		t.Fatalf("child record edit leaked into parent: %v", got)
	}
	if err := assertIndependentProductBranch(t, env, rootUUID); err != nil {
		t.Fatal(err)
	}
	prodBranch, err = env.snapshots.Branch(ctx, uuid.MustParse(string(env.prodBranch)))
	if err != nil || prodBranch.SnapshotID != nil {
		t.Fatalf("fork or content publication changed the unassigned second branch: branch=%+v err=%v", prodBranch, err)
	}

	assetServer := seoPublicServer(t, env, branchForSEO, nil)
	independentProductAssertFeatureAssetsServe(t, assetServer)

	if node, module, browser := independentProductPrerenderTools(); node != "" && module != "" && browser != "" {
		independentProductAssertPrerender(t, env, branchForSEO, node, module, browser)
	} else {
		t.Log("Chromium prerender subcase skipped: set PRERENDER_TEST_NODE, PRERENDER_TEST_MODULE, and PRERENDER_TEST_BROWSER")
	}
}

func independentProductManifests() map[string]map[string]any {
	productFields := []sitebuilder.DataInputField{
		{Path: "/slug", Type: "text"}, {Path: "/title", Type: "text"}, {Path: "/price", Type: "number"},
	}
	return map[string]map[string]any{
		"qa-catalog": {
			"formatVersion": 2, "key": "qa-catalog", "name": "QA Catalog", "version": "1.0.0", "engineApi": "2",
			"runtime": map[string]any{"abi": "static-module-v1", "entry": "dist/qa-catalog.mjs", "styles": []string{"dist/qa-catalog.css"}},
			"slots":   []string{"main"},
			"arguments": independentProductObjectSchema(map[string]any{
				"heading": map[string]any{"type": "string"}, "pageSize": map[string]any{"type": "integer", "minimum": 1},
				"showPrices": map[string]any{"type": "boolean"}, "variant": map[string]any{"type": "string", "enum": []string{"light", "dark"}},
				"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"badgeImage": map[string]any{"type": "string", "format": "media"},
			}, "heading", "pageSize", "showPrices", "variant", "tags", "badgeImage"),
			"dataInputs": []sitebuilder.DataInputPort{
				{Key: "categories", Kind: "collection", Fields: []sitebuilder.DataInputField{
					{Path: "/name", Type: "text"}, {Path: "/products/*/slug", Type: "text"},
					{Path: "/products/*/title", Type: "text"}, {Path: "/products/*/price", Type: "number"},
				}},
				{Key: "currency", Kind: "scalar", Type: "text"},
			},
		},
		"qa-product-detail": {
			"formatVersion": 2, "key": "qa-product-detail", "name": "QA Product Detail", "version": "1.0.0", "engineApi": "2",
			"runtime":   map[string]any{"abi": "static-module-v1", "entry": "dist/qa-product-detail.mjs", "styles": []string{"dist/qa-product-detail.css"}},
			"slots":     []string{"main"},
			"arguments": independentProductObjectSchema(map[string]any{"slug": map[string]any{"type": "string"}}, "slug"),
			"dataInputs": []sitebuilder.DataInputPort{{Key: "product", Kind: "record", Fields: []sitebuilder.DataInputField{
				{Path: "/slug", Type: "text"}, {Path: "/title", Type: "text"}, {Path: "/price", Type: "number"},
				{Path: "/category/name", Type: "text"},
			}}},
		},
		"qa-featured-products": {
			"formatVersion": 2, "key": "qa-featured-products", "name": "QA Featured Products", "version": "1.0.0", "engineApi": "2",
			"runtime":    map[string]any{"abi": "static-module-v1", "entry": "dist/qa-featured-products.mjs", "styles": []string{"dist/qa-featured-products.css"}},
			"slots":      []string{"main"},
			"arguments":  independentProductObjectSchema(map[string]any{"heading": map[string]any{"type": "string"}}, "heading"),
			"dataInputs": []sitebuilder.DataInputPort{{Key: "products", Kind: "collection", Fields: productFields}},
		},
	}
}

func independentProductObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) != 0 {
		schema["required"] = required
	}
	return schema
}

func independentProductArchive(t *testing.T, key string, manifest map[string]any) []byte {
	t.Helper()
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := independentProductFixtures.ReadFile("fixtures/independent_product/" + key + ".mjs")
	if err != nil {
		t.Fatalf("read synthetic %s module fixture: %v", key, err)
	}
	style, err := independentProductFixtures.ReadFile("fixtures/independent_product/" + key + ".css")
	if err != nil {
		t.Fatalf("read synthetic %s stylesheet fixture: %v", key, err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, item := range []struct {
		name string
		data []byte
	}{
		{name: "manifest.json", data: rawManifest},
		{name: "dist/" + key + ".mjs", data: entry},
		{name: "dist/" + key + ".css", data: style},
	} {
		file, err := writer.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func independentProductModel(ids independentProductIDs) sitebuilder.DataModel {
	return sitebuilder.DataModel{
		FormatVersion: 1,
		Entities: []sitebuilder.DataModelEntity{
			{ID: ids.category, Key: "category", Name: "Category", Fields: []sitebuilder.DataModelField{{ID: ids.categoryName, Key: "name", Name: "Name", Type: "text"}}},
			{ID: ids.product, Key: "product", Name: "Product", Fields: []sitebuilder.DataModelField{
				{ID: ids.productSlug, Key: "slug", Name: "Slug", Type: "text", Unique: true},
				{ID: ids.productTitle, Key: "title", Name: "Title", Type: "text"},
				{ID: ids.productPrice, Key: "price", Name: "Price", Type: "number"},
			}},
			{ID: ids.currency, Key: "currency", Name: "Currency", Fields: []sitebuilder.DataModelField{{ID: ids.currencySymbol, Key: "symbol", Name: "Symbol", Type: "text"}}},
		},
		Relations: []sitebuilder.DataModelRelation{
			{ID: ids.categoryProducts, Name: "Category products", FromEntityID: ids.category, ToEntityID: ids.product, Cardinality: "one-to-many"},
			{ID: ids.productCategory, Name: "Product category", FromEntityID: ids.product, ToEntityID: ids.category, Cardinality: "one-to-one"},
		},
	}
}

func independentProductSiteConfig(ids independentProductIDs, mediaID string) sitebuilder.SiteConfig {
	return sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     func() *sitebuilder.DataModel { model := independentProductModel(ids); return &model }(),
		Pages: []sitebuilder.Page{
			{ID: "home", Name: "Independent home", Kind: "page", Placements: []sitebuilder.Placement{
				{
					ID: "qa-catalog-placement", Build: sitebuilder.BuildRef{FeatureKey: "qa-catalog", Version: "1.0.0"}, Slot: "main",
					Arguments: map[string]sitebuilder.ArgumentBinding{
						"heading":    {Source: "literal", Value: independentProductJSON("Independent fruit")},
						"pageSize":   {Source: "literal", Value: independentProductJSON(8)},
						"showPrices": {Source: "literal", Value: independentProductJSON(true)},
						"variant":    {Source: "literal", Value: independentProductJSON("light")},
						"tags":       {Source: "literal", Value: independentProductJSON([]string{"fresh", "local"})},
						"badgeImage": {Source: "literal", Value: independentProductJSON(mediaID)},
					},
					DataBindings: []sitebuilder.DataInputBinding{
						{InputKey: "categories", EntityID: ids.category, Fields: []sitebuilder.DataInputFieldBinding{
							{InputPath: "/name", FieldID: ids.categoryName},
							{InputPath: "/products/*/slug", FieldID: ids.productSlug, RelationIDs: []string{ids.categoryProducts}},
							{InputPath: "/products/*/title", FieldID: ids.productTitle, RelationIDs: []string{ids.categoryProducts}},
							{InputPath: "/products/*/price", FieldID: ids.productPrice, RelationIDs: []string{ids.categoryProducts}},
						}},
						{InputKey: "currency", EntityID: ids.currency, FieldID: ids.currencySymbol},
					},
				},
				{
					ID: "qa-featured-products-placement", Build: sitebuilder.BuildRef{FeatureKey: "qa-featured-products", Version: "1.0.0"}, Slot: "main",
					Arguments: map[string]sitebuilder.ArgumentBinding{"heading": {Source: "literal", Value: independentProductJSON("Featured order")}},
					DataBindings: []sitebuilder.DataInputBinding{{InputKey: "products", EntityID: ids.product, Fields: []sitebuilder.DataInputFieldBinding{
						{InputPath: "/slug", FieldID: ids.productSlug}, {InputPath: "/title", FieldID: ids.productTitle}, {InputPath: "/price", FieldID: ids.productPrice},
					}}},
					DetailLink: &sitebuilder.DetailLink{RouteID: "product-detail-route", Field: "slug", PathTemplate: "/products/{slug}"},
				},
			}},
			{ID: "product-detail", Name: "Product detail", Kind: "detail", Placements: []sitebuilder.Placement{{
				ID: "qa-product-detail-placement", Build: sitebuilder.BuildRef{FeatureKey: "qa-product-detail", Version: "1.0.0"}, Slot: "main",
				Arguments: map[string]sitebuilder.ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}},
				DataBindings: []sitebuilder.DataInputBinding{{
					InputKey: "product", EntityID: ids.product,
					Fields: []sitebuilder.DataInputFieldBinding{
						{InputPath: "/slug", FieldID: ids.productSlug}, {InputPath: "/title", FieldID: ids.productTitle}, {InputPath: "/price", FieldID: ids.productPrice},
						{InputPath: "/category/name", FieldID: ids.categoryName, RelationIDs: []string{ids.productCategory}},
					},
					Lookup: &sitebuilder.DataInputLookupBinding{Argument: "slug", FieldID: ids.productSlug},
				}},
			}}},
		},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{
			{ID: "home-route", Type: "path", Path: "/", PageID: "home"},
			{ID: "product-detail-route", Type: "path", Path: "/products/:slug", PageID: "product-detail"},
		}},
	}
}

func independentProductJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func independentProductUploadMedia(t *testing.T, env *testEnv) string {
	t.Helper()
	imageData := minimalPNG(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "independent-product.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(imageData); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	status, response, _ := env.rawRequest(http.MethodPost, "/api/v1/admin/media", env.adminToken, &body, writer.FormDataContentType())
	if status != http.StatusCreated || response["id"] == nil || response["status"] != "ready" {
		t.Fatalf("upload independent fixture media: status=%d body=%v", status, response)
	}
	return response["id"].(string)
}

func independentProductEnableIndexing(t *testing.T, env *testEnv) {
	t.Helper()
	indexable := true
	previewOnly := false
	branches, err := env.snapshots.RegisterBranches(context.Background(), []config.BranchConfig{
		{Name: "Integration Development", HTTPAddr: ":18081", CORSOrigins: []string{"https://site.example"}, Indexable: &indexable},
		{Name: "Integration Production", HTTPAddr: ":18082", CORSOrigins: []string{"https://site.example"}, Indexable: &previewOnly},
	})
	if err != nil || len(branches) != 2 {
		t.Fatalf("configure explicit indexable QA branches: branches=%+v err=%v", branches, err)
	}
	if branches[0].ID.String() != string(env.devBranch) || branches[1].ID.String() != string(env.prodBranch) {
		t.Fatalf("QA branch registration changed branch identities: branches=%+v", branches)
	}
	if branches[0].SnapshotID != nil || branches[1].SnapshotID != nil {
		t.Fatalf("indexability configuration unexpectedly assigned a snapshot: branches=%+v", branches)
	}
}

func independentProductCloneConfig(t *testing.T, config sitebuilder.SiteConfig) sitebuilder.SiteConfig {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var cloned sitebuilder.SiteConfig
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func independentProductPlacement(config *sitebuilder.SiteConfig, id string) *sitebuilder.Placement {
	for pageIndex := range config.Pages {
		for placementIndex := range config.Pages[pageIndex].Placements {
			if config.Pages[pageIndex].Placements[placementIndex].ID == id {
				return &config.Pages[pageIndex].Placements[placementIndex]
			}
		}
	}
	panic("synthetic placement not found: " + id)
}

func independentProductSnapshotCount(t *testing.T, env *testEnv) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM site_snapshots`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertIndependentProductBranch(t *testing.T, env *testEnv, snapshotID uuid.UUID) error {
	t.Helper()
	branch, err := env.snapshots.Branch(context.Background(), uuid.MustParse(string(env.devBranch)))
	if err != nil {
		return err
	}
	if branch.SnapshotID == nil || *branch.SnapshotID != snapshotID {
		t.Fatalf("snapshot creation/content operation changed branch assignment: branch=%+v want=%s", branch, snapshotID)
	}
	return nil
}

func independentProductDecodeRecords(t *testing.T, raw any) []sitebuilder.ModelContentRecord {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var records []sitebuilder.ModelContentRecord
	if err := json.Unmarshal(encoded, &records); err != nil {
		t.Fatalf("decode model content records: %v", err)
	}
	return records
}

func independentProductCloneRecords(t *testing.T, raw any) []sitebuilder.ModelContentRecord {
	return independentProductDecodeRecords(t, raw)
}

func independentProductDecodeSelections(t *testing.T, raw any) []sitebuilder.ModelContentSelection {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var selections []sitebuilder.ModelContentSelection
	if err := json.Unmarshal(encoded, &selections); err != nil {
		t.Fatalf("decode editorial selections: %v", err)
	}
	return selections
}

func independentProductRecordByID(t *testing.T, records []sitebuilder.ModelContentRecord, id string) *sitebuilder.ModelContentRecord {
	t.Helper()
	for index := range records {
		if records[index].ID == id {
			return &records[index]
		}
	}
	t.Fatalf("model record %s not found in %+v", id, records)
	return nil
}

func independentProductRuntime(t *testing.T, env *testEnv, path string) map[string]any {
	t.Helper()
	target := env.devServer.URL + "/api/v2/runtime?path=" + url.QueryEscape(path)
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get synthetic runtime %q: status=%d body=%s", path, response.StatusCode, raw)
	}
	var composition map[string]any
	if err := json.Unmarshal(raw, &composition); err != nil {
		t.Fatal(err)
	}
	return composition
}

func independentProductFindPlacement(t *testing.T, composition map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range composition["placements"].([]any) {
		placement := raw.(map[string]any)
		if placement["id"] == id {
			return placement
		}
	}
	t.Fatalf("runtime placement %s not found: %v", id, composition["placements"])
	return nil
}

func assertIndependentProductRuntimeDraftHidden(t *testing.T, env *testEnv, path string) {
	t.Helper()
	composition := independentProductRuntime(t, env, path)
	catalog := independentProductFindPlacement(t, composition, "qa-catalog-placement")
	if got := len(catalog["content"].(map[string]any)["categories"].([]any)); got != 0 {
		t.Fatalf("new snapshot exposed draft categories before publication: %v", catalog["content"])
	}
}

func assertIndependentProductPublishedRuntime(t *testing.T, env *testEnv, mediaID string) {
	t.Helper()
	composition := independentProductRuntime(t, env, "/")
	catalog := independentProductFindPlacement(t, composition, "qa-catalog-placement")
	catalogContent := catalog["content"].(map[string]any)
	categories := catalogContent["categories"].([]any)
	if len(categories) != 2 || categories[0].(map[string]any)["products"].([]any)[0].(map[string]any)["title"] != "Pear" {
		t.Fatalf("published nested category/product relation was not projected: %v", catalogContent)
	}
	arguments := catalog["arguments"].(map[string]any)
	if arguments["heading"] != "Independent fruit" || arguments["pageSize"] != float64(8) || arguments["showPrices"] != true || arguments["variant"] != "light" {
		t.Fatalf("typed feature arguments were not projected: %v", arguments)
	}
	tags, ok := arguments["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "fresh" {
		t.Fatalf("simple-array feature argument was not preserved: %v", arguments["tags"])
	}
	badge, ok := arguments["badgeImage"].(map[string]any)
	if !ok || badge["id"] != mediaID || !strings.HasPrefix(badge["src"].(string), "/media/") {
		t.Fatalf("selected media argument was not hydrated from fakeStore media: %v", arguments["badgeImage"])
	}
	if catalogContent["currency"] != "€" {
		t.Fatalf("typed scalar input was not projected: %v", catalogContent)
	}

	featured := independentProductFindPlacement(t, composition, "qa-featured-products-placement")
	featuredProducts := featured["content"].(map[string]any)["products"].([]any)
	if len(featuredProducts) != 2 || featuredProducts[0].(map[string]any)["slug"] != "apple" || featuredProducts[1].(map[string]any)["slug"] != "pear" {
		t.Fatalf("editorial selection order was not preserved: %v", featuredProducts)
	}
	detailComposition := independentProductRuntime(t, env, "/products/apple")
	detail := independentProductFindPlacement(t, detailComposition, "qa-product-detail-placement")
	detailContent := detail["content"].(map[string]any)["product"].(map[string]any)
	if detailContent["slug"] != "apple" || detailContent["title"] != "Apple" || detailContent["category"].(map[string]any)["name"] != "Apple category" {
		t.Fatalf("unique slug lookup or nested one-to-one relation failed: %v", detailContent)
	}
	detailArgs := detail["arguments"].(map[string]any)
	if detailArgs["slug"] != "apple" {
		t.Fatalf("typed route argument was not resolved: %v", detailArgs)
	}
}

func assertIndependentProductRuntimeText(t *testing.T, env *testEnv, path, present, absent string) {
	t.Helper()
	composition := independentProductRuntime(t, env, path)
	raw, err := json.Marshal(composition["placements"])
	if err != nil {
		t.Fatal(err)
	}
	presentValue := []byte(`"title":"` + present + `"`)
	absentValue := []byte(`"title":"` + absent + `"`)
	if !bytes.Contains(raw, presentValue) || bytes.Contains(raw, absentValue) {
		t.Fatalf("runtime content presence mismatch: need %q, omit %q, content=%s", present, absent, raw)
	}
}

func independentProductConditionalGet(t *testing.T, server *httptest.Server, path, etag string) (int, http.Header) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("If-None-Match", etag)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, response.Header
}

func independentProductPrerenderTools() (node, module, browser string) {
	return strings.TrimSpace(os.Getenv("PRERENDER_TEST_NODE")), strings.TrimSpace(os.Getenv("PRERENDER_TEST_MODULE")), strings.TrimSpace(os.Getenv("PRERENDER_TEST_BROWSER"))
}

func independentProductAssertPrerender(t *testing.T, env *testEnv, branch sitebuilder.BranchRecord, node, module, browser string) {
	t.Helper()
	workerPath, err := filepath.Abs("../../scripts/prerender-worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	options := publicruntime.PrerenderOptions{
		NodePath: node, WorkerPath: workerPath, PlaywrightModule: module, BrowserPath: browser,
		Timeout: 30 * time.Second, AttemptTimeout: 15 * time.Second, MaxAttempts: 1,
		QuietInterval: 100 * time.Millisecond, CacheTTL: time.Minute, Concurrency: 1,
		QueueSize: 2, CacheEntries: 4, MaxHTMLBytes: 1 << 20, HeapMB: 128,
		ViewportWidth: 1280, ViewportHeight: 900, MaxRequests: 100, MaxNetworkBytes: 16 << 20,
	}
	branch.Indexable = true
	server, worker := seoPublicServerWithWorker(t, env, branch, &options)
	var workerDiagnostics bytes.Buffer
	worker.SetLogger(slog.New(slog.NewJSONHandler(&workerDiagnostics, &slog.HandlerOptions{Level: slog.LevelDebug})))
	status, html, headers := seoGet(t, server, "/")
	if status != http.StatusOK || headers.Get("X-Engine-Prerender") != "hit" || !strings.Contains(html, "Independent fruit") || !strings.Contains(html, "Pear") {
		t.Fatalf("synthetic static-module features did not prerender: status=%d headers=%v worker=%s html=%s", status, headers, workerDiagnostics.String(), html)
	}
	if headers.Get("ETag") == "" {
		t.Fatalf("successfully prerendered indexable HTML should be cacheable: %v", headers)
	}
	worker.SetLogger(nil)
}

func independentProductAssertFeatureAssetsServe(t *testing.T, server *httptest.Server) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(server.URL + "/api/v2/runtime?path=%2F")
	if err != nil {
		t.Fatalf("fetch synthetic runtime asset manifest: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetch synthetic runtime asset manifest: status=%d", response.StatusCode)
	}
	var composition struct {
		Placements []struct {
			EntryURL  string   `json:"entryUrl"`
			StyleURLs []string `json:"styleUrls"`
		} `json:"placements"`
	}
	if err := json.NewDecoder(response.Body).Decode(&composition); err != nil {
		t.Fatalf("decode synthetic runtime asset manifest: %v", err)
	}
	assetURLs := make([]string, 0, len(composition.Placements)*2)
	for _, placement := range composition.Placements {
		assetURLs = append(assetURLs, placement.EntryURL)
		assetURLs = append(assetURLs, placement.StyleURLs...)
	}
	if len(assetURLs) < 2 {
		t.Fatalf("synthetic runtime should expose module and stylesheet assets, got %d", len(assetURLs))
	}
	for _, assetURL := range assetURLs {
		if !strings.HasPrefix(assetURL, "/feature-assets/") {
			t.Fatalf("synthetic runtime returned a non-engine feature asset URL")
		}
		asset, err := client.Get(server.URL + assetURL)
		if err != nil {
			t.Fatalf("fetch synthetic feature asset: %v", err)
		}
		if asset.StatusCode != http.StatusOK {
			body, readErr := io.ReadAll(io.LimitReader(asset.Body, 2048))
			closeErr := asset.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read synthetic feature asset error response: read=%v close=%v", readErr, closeErr)
			}
			t.Fatalf("synthetic feature asset was not served: status=%d class=%s", asset.StatusCode, independentProductAssetErrorClass(body))
		}
		assetBytes, readErr := io.Copy(io.Discard, asset.Body)
		closeErr := asset.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read synthetic feature asset: read=%v close=%v", readErr, closeErr)
		}
		if asset.StatusCode != http.StatusOK || assetBytes == 0 {
			t.Fatalf("synthetic feature asset was not served: status=%d bytes=%d", asset.StatusCode, assetBytes)
		}
	}
}

func independentProductAssetErrorClass(body []byte) string {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "non_json"
	}
	message := strings.ToLower(payload.Error.Message)
	switch {
	case strings.Contains(message, "invalid feature asset path"):
		return "invalid_asset_path"
	case strings.Contains(message, "nosuchkey"), strings.Contains(message, "notfound"):
		return "object_not_found"
	case strings.Contains(message, "accessdenied"), strings.Contains(message, "forbidden"):
		return "object_access_denied"
	case strings.Contains(message, "signaturedoesnotmatch"), strings.Contains(message, "invalidaccesskeyid"):
		return "object_authentication"
	case strings.Contains(message, "get s3 object"):
		return "object_store_get"
	case strings.Contains(message, "database"), strings.Contains(message, "no rows"):
		return "asset_catalog"
	default:
		if payload.Error.Code != "" {
			return "api_error"
		}
		return "unknown"
	}
}

func requireUniqueIndependentProductDatabase(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	parsed, err := url.Parse(raw)
	if raw == "" || err != nil {
		t.Skip("set TEST_DATABASE_URL to a unique local website_engine_independent_product_qa_* disposable database")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.HasPrefix(databaseName, independentProductDBPrefix) || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Skip("independent product acceptance requires a local website_engine_independent_product_qa_* database; refusing any other target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatalf("connect to disposable independent product database: %v", err)
	}
	defer connection.Close(context.Background())
	var currentDatabase string
	var sessions int
	if err := connection.QueryRow(ctx, `SELECT current_database(), (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid())`).Scan(&currentDatabase, &sessions); err != nil {
		t.Fatalf("verify disposable independent product database: %v", err)
	}
	if currentDatabase != databaseName {
		t.Fatalf("connected database %q does not match requested disposable database %q", currentDatabase, databaseName)
	}
	if sessions != 0 {
		t.Fatalf("refusing integration TRUNCATE: disposable database %q has %d other active sessions", databaseName, sessions)
	}
	t.Logf("verified local disposable database %s has no other active sessions", databaseName)
}

func independentProductS3Store(t *testing.T) storage.ObjectStore {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("TEST_INDEPENDENT_PRODUCT_S3_SECRET_KEY"))
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("set TEST_INDEPENDENT_PRODUCT_S3_ENDPOINT, TEST_INDEPENDENT_PRODUCT_S3_BUCKET, TEST_INDEPENDENT_PRODUCT_S3_ACCESS_KEY and TEST_INDEPENDENT_PRODUCT_S3_SECRET_KEY for disposable MinIO acceptance")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		t.Fatalf("independent synthetic acceptance requires an HTTP loopback MinIO endpoint, got %q", endpoint)
	}
	if parsed.Hostname() != "localhost" {
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsLoopback() {
			t.Fatalf("refusing non-loopback object storage endpoint %q", endpoint)
		}
	}
	if !strings.HasPrefix(bucket, "independent-product-qa-") || strings.TrimPrefix(bucket, "independent-product-qa-") == "" {
		t.Fatalf("disposable synthetic object storage bucket must have a unique independent-product-qa-* name, got %q", bucket)
	}
	store, err := s3store.New(context.Background(), config.S3Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: accessKey,
		SecretKey: secretKey, PublicBaseURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create disposable synthetic S3 client: %v", err)
	}
	if err := store.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("ensure disposable synthetic S3 bucket: %v", err)
	}
	if err := store.RequirePrivate(context.Background()); err != nil {
		t.Fatalf("synthetic S3 bucket must remain private: %v", err)
	}
	t.Logf("verified loopback disposable MinIO bucket %s is private", bucket)
	return store
}
