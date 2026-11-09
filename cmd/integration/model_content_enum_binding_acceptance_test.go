package integration

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

// TestModelEnumBindingStructuralSaveIsAtomic verifies that the structural API
// accepts a model enum that is narrower than its feature input and rejects a
// model option outside that input without changing snapshots or assignments.
func TestModelEnumBindingStructuralSaveIsAtomic(t *testing.T) {
	requireUniqueIndependentProductDatabase(t)
	env := newTestEnv(t)
	independentProductEnableIndexing(t, env)
	mediaID := independentProductUploadMedia(t, env)

	ids := independentProductIDs{
		category: uuid.NewString(), categoryName: uuid.NewString(),
		product: uuid.NewString(), productSlug: uuid.NewString(), productTitle: uuid.NewString(), productPrice: uuid.NewString(),
		currency: uuid.NewString(), currencySymbol: uuid.NewString(),
		categoryProducts: uuid.NewString(), productCategory: uuid.NewString(),
	}
	manifests := independentProductManifests()
	catalogInputs := manifests["qa-catalog"]["dataInputs"].([]sitebuilder.DataInputPort)
	for index := range catalogInputs {
		if catalogInputs[index].Key == "currency" {
			catalogInputs[index].Type = "enum"
			catalogInputs[index].Values = []string{"USD", "EUR", "GBP"}
		}
	}
	manifests["qa-catalog"]["dataInputs"] = catalogInputs
	for _, key := range []string{"qa-catalog", "qa-product-detail", "qa-featured-products"} {
		archive := independentProductArchive(t, key, manifests[key])
		status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, bytes.NewReader(archive), "application/zip")
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("register synthetic feature %s: status=%d body=%v", key, status, body)
		}
	}

	config := independentProductSiteConfig(ids, mediaID)
	for entityIndex := range config.DataModel.Entities {
		entity := &config.DataModel.Entities[entityIndex]
		if entity.ID != ids.currency {
			continue
		}
		for fieldIndex := range entity.Fields {
			if entity.Fields[fieldIndex].ID == ids.currencySymbol {
				entity.Fields[fieldIndex].Type = "enum"
				entity.Fields[fieldIndex].Options = []string{"USD", "EUR"}
			}
		}
	}
	metadata := sitebuilder.SiteMetadata{
		FormatVersion: 1, SiteName: "Enum binding QA", Description: "Structural enum acceptance",
		CanonicalDomain: "https://enum-binding.example.test", Language: "en",
	}
	status, root, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, sitebuilder.CreateSnapshotInput{
		Name: "Enum binding root", Config: &config, Metadata: &metadata,
	})
	if status != http.StatusCreated {
		t.Fatalf("create root with a model enum subset: status=%d body=%v", status, root)
	}
	rootID := root["id"].(string)
	rootUUID := uuid.MustParse(rootID)
	assignSnapshotToBranch(t, env, string(env.devBranch), rootID)
	branchID := uuid.MustParse(string(env.devBranch))
	branchBefore, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil {
		t.Fatalf("read branch before rejected structural save: %v", err)
	}
	var revisionBefore int64
	var digestBefore string
	if err := env.pool.QueryRow(context.Background(), `SELECT content_revision,digest FROM site_snapshots WHERE id=$1::uuid`, rootID).Scan(&revisionBefore, &digestBefore); err != nil {
		t.Fatalf("read root revision before rejected structural save: %v", err)
	}
	snapshotCountBefore := independentProductSnapshotCount(t, env)

	candidate := independentProductCloneConfig(t, config)
	for entityIndex := range candidate.DataModel.Entities {
		entity := &candidate.DataModel.Entities[entityIndex]
		if entity.ID != ids.currency {
			continue
		}
		for fieldIndex := range entity.Fields {
			if entity.Fields[fieldIndex].ID == ids.currencySymbol {
				entity.Fields[fieldIndex].Options = append(entity.Fields[fieldIndex].Options, "CAD")
			}
		}
	}
	parentID := rootUUID
	status, response, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, sitebuilder.CreateSnapshotInput{
		Name: "Invalid enum child", ParentID: &parentID, Config: &candidate,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("model option outside feature enum should be rejected: status=%d body=%v", status, response)
	}
	if !strings.Contains(fmt.Sprint(response), "model enum option") {
		t.Fatalf("enum contract error should identify the unsupported model option: %v", response)
	}
	if snapshotCount := independentProductSnapshotCount(t, env); snapshotCount != snapshotCountBefore {
		t.Fatalf("rejected enum binding partially created a snapshot: before=%d after=%d", snapshotCountBefore, snapshotCount)
	}
	var revisionAfter int64
	var digestAfter string
	if err := env.pool.QueryRow(context.Background(), `SELECT content_revision,digest FROM site_snapshots WHERE id=$1::uuid`, rootID).Scan(&revisionAfter, &digestAfter); err != nil {
		t.Fatalf("read root revision after rejected structural save: %v", err)
	}
	if revisionAfter != revisionBefore || digestAfter != digestBefore {
		t.Fatalf("rejected enum binding changed the root snapshot: revision %d→%d, digest changed=%v", revisionBefore, revisionAfter, digestAfter != digestBefore)
	}
	branchAfter, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil {
		t.Fatalf("read branch after rejected structural save: %v", err)
	}
	if branchAfter.SnapshotID == nil || *branchAfter.SnapshotID != rootUUID || branchAfter.Revision != branchBefore.Revision {
		t.Fatalf("rejected enum binding changed the branch assignment or revision: before=%+v after=%+v", branchBefore, branchAfter)
	}
}
