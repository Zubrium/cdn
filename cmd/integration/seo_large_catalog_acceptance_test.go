package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/environment"
)

// TestVitasilPageIndexLargeCatalogAcceptance measures a cold PageIndex build
// with the protocol's 50,000-detail-URL scale and current Vitasil v2 builds.
// It is opt-in because it writes a large disposable content payload.
func TestVitasilPageIndexLargeCatalogAcceptance(t *testing.T) {
	if os.Getenv("RUN_LARGE_CATALOG_ACCEPTANCE") != "1" {
		t.Skip("set RUN_LARGE_CATALOG_ACCEPTANCE=1 to build the 50,000-record disposable sitemap fixture")
	}
	requireDisposableModelContentDatabase(t)
	featureDir := strings.TrimSpace(os.Getenv("TEST_FEATURE_DIR"))
	if featureDir == "" {
		t.Fatal("TEST_FEATURE_DIR must point to current Vitasil v2 ZIPs")
	}
	const recordCount = 50_000
	env := newTestEnv(t)
	manifests := make(map[string]sitebuilder.Manifest, 2)
	versions := make(map[string]string, 2)
	builds := make([]sitebuilder.BuildRef, 0, 2)
	for _, key := range []string{"catalog", "catalog-detail"} {
		archive, version := readExternalFeatureArchive(t, featureDir, key)
		verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
		if err != nil {
			t.Fatalf("verify current Vitasil %s archive: %v", key, err)
		}
		if verified.Manifest.FormatVersion != 2 || verified.Manifest.RuntimeABI() != sitebuilder.RuntimeABIStaticModule {
			t.Fatalf("Vitasil %s@%s does not use the current v2 static-module contract", key, version)
		}
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, strings.NewReader(string(archive)), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register current Vitasil %s@%s: status=%d body=%v", key, version, status, body)
		}
		manifests[key], versions[key] = verified.Manifest, version
		builds = append(builds, sitebuilder.BuildRef{FeatureKey: key, Version: version})
	}
	catalogPort := modelAcceptancePort(t, manifests["catalog"], "products")
	detailPort := modelAcceptancePort(t, manifests["catalog-detail"], "product")
	modelAcceptanceAssertSameOutputShape(t, catalogPort, detailPort, "catalog-large-catalog")
	model, product, fieldIDs, fieldKeys := modelAcceptanceEntity("product", "Product", catalogPort, true)
	catalogPlacement := sitebuilder.Placement{
		ID: "large-catalog", Build: sitebuilder.BuildRef{FeatureKey: "catalog", Version: versions["catalog"]}, Slot: "main",
		Arguments: map[string]sitebuilder.ArgumentBinding{
			"title":      {Source: "literal", Value: json.RawMessage(`"Performance catalog"`)},
			"action":     {Source: "literal", Value: json.RawMessage(`{"label":"Contact","to":"/contact"}`)},
		},
		DataBindings: []sitebuilder.DataInputBinding{{InputKey: catalogPort.Key, EntityID: product.ID, Fields: modelAcceptanceFieldBindings(catalogPort, fieldIDs)}},
		DetailLink:   &sitebuilder.DetailLink{RouteID: "large-product-detail-route", Field: "slug", PathTemplate: "/catalog/{slug}"},
	}
	detailPlacement := sitebuilder.Placement{
		ID: "large-detail", Build: sitebuilder.BuildRef{FeatureKey: "catalog-detail", Version: versions["catalog-detail"]}, Slot: "main",
		Arguments: map[string]sitebuilder.ArgumentBinding{"slug": {Source: "path", PathArg: "slug"}},
		DataBindings: []sitebuilder.DataInputBinding{{
			InputKey: detailPort.Key, EntityID: product.ID, Fields: modelAcceptanceFieldBindings(detailPort, fieldIDs),
			Lookup: &sitebuilder.DataInputLookupBinding{Argument: "slug", FieldID: fieldIDs["/slug"]},
		}},
	}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3, Features: builds, DataModel: &model,
		Pages: []sitebuilder.Page{
			{ID: "catalog", Name: "Catalog", Kind: "collection-list", Placements: []sitebuilder.Placement{catalogPlacement}},
			{ID: "product", Name: "Product", Kind: "detail", Placements: []sitebuilder.Placement{detailPlacement}},
		},
		RouteTree: sitebuilder.RouteTreeNode{ID: "site", Type: "site", Children: []sitebuilder.RouteTreeNode{{
			ID: "catalog-route", Type: "path", Path: "/catalog", PageID: "catalog", Children: []sitebuilder.RouteTreeNode{{
				ID: "large-product-detail-route", Type: "param", Name: "slug", PageID: "product",
			}},
		}}},
	}
	metadata := sitebuilder.DefaultSiteMetadata()
	metadata.CanonicalDomain = "https://large-catalog.vitasil.example.test"
	metadata.Language = "en"
	actor := modelAcceptanceActor(t, env)
	root, err := env.snapshots.CreateSnapshot(context.Background(), actor, sitebuilder.CreateSnapshotInput{
		Name: "50,000 Vitasil product sitemap acceptance", Config: &config, Metadata: &metadata,
	})
	if err != nil {
		t.Fatalf("create disposable large-catalog snapshot: %v", err)
	}
	branchID := uuid.MustParse(string(env.devBranch))
	branch, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil {
		t.Fatalf("read disposable branch before assignment: %v", err)
	}
	contentRevision := int64(0)
	if _, err := env.snapshots.AssignBranch(context.Background(), actor, branchID, root.ID, branch.Revision, &contentRevision); err != nil {
		t.Fatalf("assign disposable large-catalog snapshot: %v", err)
	}
	productValues := make(map[string]any, 2)
	productValues[fieldKeys["/slug"]] = ""
	productValues[fieldKeys["/title"]] = ""
	records := make([]sitebuilder.ModelContentRecord, recordCount)
	for i := range records {
		productValues[fieldKeys["/slug"]] = fmt.Sprintf("product-%05d", i)
		productValues[fieldKeys["/title"]] = fmt.Sprintf("Product %05d", i)
		values := make(map[string]any, len(productValues))
		for key, value := range productValues {
			values[key] = value
		}
		records[i] = sitebuilder.ModelContentRecord{EntityID: product.ID, SortOrder: i, Values: values}
	}
	workspace, err := env.snapshots.ModelContentWorkspace(context.Background(), root.ID)
	if err != nil {
		t.Fatalf("read initial large-catalog workspace: %v", err)
	}
	saved, err := env.snapshots.SaveModelContentWorkspace(context.Background(), actor, root.ID, sitebuilder.ModelContentDraftInput{
		Revision: workspace.Revision, Records: records,
	})
	if err != nil {
		t.Fatalf("save %d disposable Product records: %v", recordCount, err)
	}
	if _, err := env.snapshots.PublishModelContentWorkspace(context.Background(), actor, root.ID, saved.Revision); err != nil {
		t.Fatalf("publish %d disposable Product records: %v", recordCount, err)
	}
	branchEnv := environment.Environment(env.devBranch)
	coldStarted := time.Now()
	index, err := env.snapshots.PageIndex(context.Background(), branchEnv, true)
	coldDuration := time.Since(coldStarted)
	if err != nil {
		t.Fatalf("cold PageIndex for %d published Product records: %v", recordCount, err)
	}
	if len(index.URLs) != recordCount+1 || index.URLs[0] != metadata.CanonicalDomain+"/catalog" || index.URLs[len(index.URLs)-1] != metadata.CanonicalDomain+"/catalog/product-49999" {
		t.Fatalf("large sitemap URL set is incomplete or unsorted: count=%d first=%q last=%q", len(index.URLs), firstString(index.URLs), lastString(index.URLs))
	}
	warmStarted := time.Now()
	warmIndex, err := env.snapshots.PageIndex(context.Background(), branchEnv, true)
	warmDuration := time.Since(warmStarted)
	if err != nil || len(warmIndex.URLs) != len(index.URLs) || warmIndex.URLs[len(warmIndex.URLs)-1] != index.URLs[len(index.URLs)-1] {
		t.Fatalf("warm PageIndex differs from cold result: cold=%d warm=%d err=%v", len(index.URLs), len(warmIndex.URLs), err)
	}
	t.Logf("SEO_LARGE_CATALOG_EVIDENCE_JSON={\"featureVersions\":%q,\"records\":%d,\"urls\":%d,\"coldPageIndexMs\":%d,\"warmPageIndexMs\":%d}", versions, recordCount, len(index.URLs), coldDuration.Milliseconds(), warmDuration.Milliseconds())
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func lastString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}
