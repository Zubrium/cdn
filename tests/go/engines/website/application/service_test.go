package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapp "github.com/Zubrium/cdn/engines/website/application"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

const (
	siteA     = "site-a"
	siteB     = "site-b"
	pageA     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	pageB     = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	snapshotA = "33333333-3333-4333-8333-333333333333"
	snapshotB = "44444444-4444-4444-8444-444444444444"
)

func TestListConfiguredSitesRequiresReadAccessAndReturnsStableIDs(t *testing.T) {
	service := newService(t, newMemoryStore(), core.AccessLevelRead, websiteapp.Config{AccessGroup: "features", CoordinatorAccessGroup: "coordinator_jobs", Sites: []string{siteB, siteA}})
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ListConfiguredSites(ctx)
	if err != nil || len(result.Items) != 2 || result.Items[0].SiteID != siteA || result.Items[1].SiteID != siteB {
		t.Fatalf("configured sites = %+v, %v", result, err)
	}
	readOnly := newService(t, newMemoryStore(), core.AccessLevelNone, websiteConfig())
	unauthenticated, err := readOnly.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.ListConfiguredSites(unauthenticated); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("list without read access = %v, want forbidden", err)
	}
}

func TestBuildRejectsProvisioningSnapshot(t *testing.T) {
	store := newMemoryStore()
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.BuildSnapshot(ctx, websiteapi.BuildSnapshotRequest{SnapshotID: snapshotA})
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("provisioning snapshot build error = %v, want conflict", err)
	}
}

func TestMarkSnapshotReadyFailsClosedWithoutForkWorkflow(t *testing.T) {
	store := newMemoryStore()
	rootID := "66666666-6666-4666-8666-666666666666"
	putSnapshot(t, store, siteA, rootID, 1)
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	setSnapshotParent(t, store, siteA, snapshotA, rootID, "Home", 7)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.MarkSnapshotReady(ctx, snapshotA, 3)
	if !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("finalization without a durable fork workflow = %v, want unavailable", err)
	}
	stored := store.records[snapshotRecordKey(siteA, snapshotA)]
	if stored.Revision != 3 {
		t.Fatalf("fail-closed finalization changed revision to %d, want 3", stored.Revision)
	}
	snapshot, err := service.GetSnapshot(ctx, snapshotA)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "provisioning" {
		t.Fatalf("snapshot state = %q, want provisioning until fork completion", snapshot.State)
	}
}

func TestMarkSnapshotReadyRejectsStaleRevisionWithoutMutation(t *testing.T) {
	store := newMemoryStore()
	rootID := "66666666-6666-4666-8666-666666666666"
	putSnapshot(t, store, siteA, rootID, 1)
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	setSnapshotParent(t, store, siteA, snapshotA, rootID, "Home", 7)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.MarkSnapshotReady(ctx, snapshotA, 2)
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale finalization error = %v, want conflict", err)
	}
	if store.records[snapshotRecordKey(siteA, snapshotA)].Revision != 3 {
		t.Fatalf("stale finalization changed revision to %d", store.records[snapshotRecordKey(siteA, snapshotA)].Revision)
	}
}

func TestDeleteSnapshotReparentsChildrenInOneAtomicRevisionBatch(t *testing.T) {
	store := newMemoryStore()
	rootID := "66666666-6666-4666-8666-666666666666"
	targetID := "77777777-7777-4777-8777-777777777777"
	childID := "88888888-8888-4888-8888-888888888888"
	putSnapshot(t, store, siteA, rootID, 1)
	putSnapshot(t, store, siteA, targetID, 1)
	putSnapshot(t, store, siteA, childID, 1)
	setSnapshotParent(t, store, siteA, targetID, rootID, "Home", 7)
	setSnapshotParent(t, store, siteA, childID, targetID, "Home", 7)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteSnapshot(ctx, websiteapi.DeleteSnapshotRequest{SnapshotID: targetID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.records[snapshotRecordKey(siteA, targetID)]; exists {
		t.Fatal("target snapshot still exists after deletion")
	}
	child, err := service.GetSnapshot(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != rootID || child.ParentName != "Home" || child.ParentSequence != 7 || child.Revision != 2 {
		t.Fatalf("child after reparenting = %#v", child)
	}
	var index map[string]any
	if err := json.Unmarshal(store.records[snapshotRecordKey(siteA, childID)].IndexData, &index); err != nil {
		t.Fatal(err)
	}
	if index["parent_id"] != rootID || index["revision"] != float64(2) {
		t.Fatalf("child index after reparenting = %#v", index)
	}
}

func TestDeleteSnapshotRejectsStaleRevisionWithoutMutation(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSnapshot(ctx, websiteapi.DeleteSnapshotRequest{SnapshotID: snapshotA, ExpectedRevision: 2}); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale deletion error = %v, want conflict", err)
	}
	if store.records[snapshotRecordKey(siteA, snapshotA)].Revision != 3 {
		t.Fatal("stale deletion changed snapshot revision")
	}
}

func TestDeleteSnapshotRejectsCurrentLatestPageSnapshot(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 1)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSnapshot(ctx, websiteapi.DeleteSnapshotRequest{SnapshotID: snapshotA, ExpectedRevision: 1}); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("deleting the page's latest snapshot = %v, want conflict", err)
	}
	if page, err := service.GetPage(ctx, siteA, pageA); err != nil || page.LatestSnapshotID != snapshotA {
		t.Fatalf("page registry after rejected deletion = %#v, %v", page, err)
	}
}

func TestListSnapshotsPaginatesDeterministicallyAcrossConfiguredSites(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 2)
	putSnapshot(t, store, siteA, "55555555-5555-4555-8555-555555555555", 1)
	putSnapshot(t, store, siteB, snapshotB, 4)
	service := newService(t, store, core.AccessLevelRead, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	cursor := ""
	for pages := 0; pages < 4; pages++ {
		page, err := service.ListSnapshots(ctx, "", pageA, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			break
		}
		got = append(got, page.Items[0].ID)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	want := []string{"33333333-3333-4333-8333-333333333333", "55555555-5555-4555-8555-555555555555", "44444444-4444-4444-8444-444444444444"}
	if len(got) != len(want) {
		t.Fatalf("snapshot IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("snapshot IDs = %v, want %v", got, want)
		}
	}
}

func TestListSnapshotsFailsClosedForUnknownLifecycleState(t *testing.T) {
	store := newMemoryStore()
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "draft")
	service := newService(t, store, core.AccessLevelRead, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSnapshots(ctx, siteA, pageA, "", 10); !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("unknown lifecycle state error = %v, want unavailable", err)
	}
}

func TestSiteModelUsesIndependentSiteRevisionAndDigest(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.SaveSiteModel(ctx, websiteapi.SaveSiteModelRequest{SiteID: siteA, ExpectedRevision: 0, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if first.ModelRevision != 1 || first.SiteID != siteA || !first.VerifyDigest() {
		t.Fatalf("site model projection = %#v", first)
	}
	readCtx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.GetSiteContentSchema(readCtx, siteA)
	if err != nil || read.SiteID != first.SiteID || read.ModelRevision != first.ModelRevision || read.Digest != first.Digest || string(read.DataModel) != string(first.DataModel) {
		t.Fatalf("read site model = %#v, %v", read, err)
	}
	second, err := service.SaveSiteModel(ctx, websiteapi.SaveSiteModelRequest{SiteID: siteA, ExpectedRevision: 1, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[],"positions":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if second.ModelRevision != 2 || second.Digest == first.Digest {
		t.Fatalf("site model revision/digest did not advance independently: first=%#v second=%#v", first, second)
	}
	if _, err := service.SaveSiteModel(ctx, websiteapi.SaveSiteModelRequest{SiteID: siteA, ExpectedRevision: 1, DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`)}); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale site model revision error = %v, want conflict", err)
	}
}

func TestPagePlacementProjectionIsScopedToExactSiteAndPage(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	model := json.RawMessage(`{"formatVersion":1,"entities":[{"id":"66666666-6666-4666-8666-666666666666","key":"product","name":"Product","cardinality":"many","fields":[{"id":"77777777-7777-4777-8777-777777777777","key":"title","name":"Title","type":"text"}]}]}`)
	if _, err := service.SaveSiteModel(ctx, websiteapi.SaveSiteModelRequest{SiteID: siteA, DataModel: model}); err != nil {
		t.Fatal(err)
	}
	snapshot := websiteapi.Snapshot{ID: snapshotA, SiteID: siteA, PageID: pageA, Name: "Home", Sequence: 1, FeaturePins: map[string]string{"hero": "1.2.0"}, State: "ready",
		Payload: &websiteapi.SnapshotPayload{Config: json.RawMessage(`{"format_version":3,"page":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Home","placements":[{"id":"placement-1","build":{"featureKey":"hero","version":"1.2.0"},"dataBindings":[{"inputKey":"items","entityId":"66666666-6666-4666-8666-666666666666","fields":[{"inputPath":"title","fieldId":"77777777-7777-4777-8777-777777777777"}]}]}]}}`), Metadata: json.RawMessage(`{}`)}}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	key := snapshotRecordKey(siteA, snapshotA)
	store.records[key] = core.ModuleDataRecord{Payload: payload, PayloadVersion: 1, IndexData: mustJSON(map[string]any{"record_type": "snapshot", "snapshot_id": snapshotA, "page_id": pageA, "state": "ready", "revision": float64(1), "parent_id": ""}), Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	putPageRecord(t, store, websiteapi.Page{ID: pageA, SiteID: siteA, Name: "Home", LatestSnapshotID: snapshotA, LatestSnapshotState: "ready", CreatedAt: snapshot.CreatedAt})
	readCtx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := service.GetPagePlacementProjection(readCtx, siteA, pageA, snapshotA)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.VerifyDigest() || projection.Revision != 1 || len(projection.Placements) != 1 || projection.Placements[0].Bindings[0].Fields[0].FieldID != "77777777-7777-4777-8777-777777777777" {
		t.Fatalf("page placement projection = %#v", projection)
	}
	if _, err := service.GetPagePlacementProjection(readCtx, siteA, pageB, snapshotA); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("cross-page projection error = %v, want not found", err)
	}
	if _, err := service.GetPagePlacementProjection(readCtx, siteB, pageA, snapshotA); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("cross-site projection error = %v, want not found", err)
	}
}

func TestGetSnapshotConfigDoesNotContainSiteModel(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelRead, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.GetSnapshot(ctx, snapshotA)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 3 || snapshot.Payload == nil {
		t.Fatalf("snapshot revision or payload missing: %#v", snapshot)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(snapshot.Payload.Config, &config); err != nil {
		t.Fatal(err)
	}
	if _, exists := config["dataModel"]; exists {
		t.Fatalf("page snapshot contains site-wide dataModel: %s", snapshot.Payload.Config)
	}
}

func TestSnapshotReadRequiresReadAccessBeforeLookingUpTarget(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelNone, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GetSnapshot(ctx, snapshotA)
	if !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("snapshot read without level 1 error = %v, want forbidden", err)
	}
}

func TestSnapshotListRejectsUnknownSiteAndMalformedCursor(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelRead, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSnapshots(ctx, "unknown", pageA, "", 10); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("unknown site error = %v, want not found", err)
	}
	if _, err := service.ListSnapshots(ctx, siteA, pageA, "not-a-cursor", 10); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("malformed cursor error = %v, want invalid", err)
	}
}

func TestCreateSnapshotTreesAreIndependentBySiteAndPage(t *testing.T) {
	store := newMemoryStore()
	homeRoot := "99999999-9999-4999-8999-999999999999"
	contactRoot := "aaaaaaaa-1111-4111-8111-111111111111"
	putSnapshotForPageWithState(t, store, siteA, pageA, homeRoot, 1, "ready")
	putSnapshotForPageWithState(t, store, siteA, pageB, contactRoot, 1, "ready")
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	homeRequest := createSnapshotRequest("Home child", "idempotency-page-tree-home-001")
	homeRequest.ParentID, homeRequest.ExpectedRevision = homeRoot, 1
	home, err := service.CreateSnapshot(ctx, homeRequest)
	if err != nil {
		t.Fatal(err)
	}
	contactRequest := createSnapshotRequest("Contact child", "idempotency-page-tree-contact-01")
	contactRequest.PageID, contactRequest.ParentID, contactRequest.ExpectedRevision = pageB, contactRoot, 1
	contact, err := service.CreateSnapshot(ctx, contactRequest)
	if err != nil {
		t.Fatal(err)
	}
	if home.Sequence != 8 || contact.Sequence != 8 || home.PageID != pageA || contact.PageID != pageB {
		t.Fatalf("page snapshots were not independent: home=%#v contact=%#v", home, contact)
	}
	crossPageChild := createSnapshotRequest("Invalid cross-page child", "idempotency-cross-page-child-001")
	crossPageChild.PageID = pageB
	crossPageChild.ParentID = home.ID
	crossPageChild.ExpectedRevision = home.Revision
	if _, err := service.CreateSnapshot(ctx, crossPageChild); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("cross-page parent error = %v, want not found", err)
	}
	if _, exists := store.records[snapshotRecordKeyForPage(siteA, pageA, home.ID)]; !exists {
		t.Fatal("home snapshot was not stored in its page-specific key")
	}
	contactKey := snapshotRecordKeyForPage(siteA, pageB, contact.ID)
	if _, exists := store.records[contactKey]; !exists {
		t.Fatal("contact snapshot was not stored in its page-specific key")
	}

	readCtx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	homePage, err := service.ListSnapshots(readCtx, siteA, pageA, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	contactPage, err := service.ListSnapshots(readCtx, siteA, pageB, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(homePage.Items) != 2 || len(contactPage.Items) != 2 || !snapshotPageContains(homePage.Items, home.ID) || !snapshotPageContains(contactPage.Items, contact.ID) {
		t.Fatalf("page-scoped snapshot lists leaked across trees: home=%#v contact=%#v", homePage.Items, contactPage.Items)
	}
}

func snapshotPageContains(items []websiteapi.Snapshot, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestCreatePageRootAndSnapshotChildUseSequenceAndLifecycle(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	root, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{SiteID: siteA, Name: "Root", IdempotencyKey: "idempotency-root-page-001"})
	if err != nil {
		t.Fatal(err)
	}
	rootSnapshot, err := service.GetSnapshot(ctx, root.LatestSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if root.LatestSnapshotState != "ready" || rootSnapshot.Sequence != 1 || rootSnapshot.Revision != 1 || rootSnapshot.ParentID != "" {
		t.Fatalf("root snapshot = %#v", rootSnapshot)
	}
	if rootSnapshot.Name != root.Name || len(rootSnapshot.FeaturePins) != 0 {
		t.Fatalf("root snapshot identity/pins = %#v", rootSnapshot)
	}

	childRequest := createSnapshotRequest("Child", "idempotency-child-001")
	childRequest.PageID = root.ID
	childRequest.ParentID = root.LatestSnapshotID
	childRequest.ExpectedRevision = rootSnapshot.Revision
	childRequest.ExpectedContentRevision = 99 // Content owns this precondition.
	child, err := service.CreateSnapshot(ctx, childRequest)
	if err != nil {
		t.Fatal(err)
	}
	if child.State != "provisioning" || child.Sequence != 2 || child.ParentID != root.LatestSnapshotID || child.ParentName != root.Name || child.ParentSequence != rootSnapshot.Sequence {
		t.Fatalf("child snapshot = %#v", child)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(child.Payload.Config, &config); err != nil {
		t.Fatal(err)
	}
	if _, exists := config["dataModel"]; exists {
		t.Fatalf("child page snapshot contains site-wide dataModel: %s", child.Payload.Config)
	}
	if _, err := service.MarkSnapshotReady(ctx, child.ID, child.Revision); !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("child finalization without a durable fork workflow = %v, want unavailable", err)
	}
	stored, err := service.GetSnapshot(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "provisioning" || stored.Revision != child.Revision {
		t.Fatalf("child snapshot changed without fork completion: %#v", stored)
	}
}

func TestCreateChildSnapshotAcceptsUntouchedContentRevisionZero(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{SiteID: siteA, Name: "Root", IdempotencyKey: "idempotency-zero-root-page-001"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Child", "idempotency-zero-child-001")
	request.PageID = page.ID
	request.ParentID = root.ID
	request.ExpectedRevision = root.Revision
	request.ExpectedContentRevision = 0
	child, err := service.CreateSnapshot(ctx, request)
	if err != nil {
		t.Fatalf("create child from untouched Content workspace: %v", err)
	}
	if child.State != "provisioning" || child.ParentID != root.ID {
		t.Fatalf("child snapshot = %#v, want provisioning child", child)
	}
}

func TestCreatePageReplaysIdempotencyAndConflictsOnDifferentInput(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := websiteapi.CreatePageRequest{SiteID: siteA, Name: "Root", IdempotencyKey: "idempotency-page-replay-001"}
	first, err := service.CreatePage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreatePage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.LatestSnapshotID != second.LatestSnapshotID || len(store.records) != 4 {
		t.Fatalf("replay created another page: first=%#v second=%#v record_count=%d", first, second, len(store.records))
	}

	request.Name = "Different root"
	if _, err := service.CreatePage(ctx, request); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("reused idempotency key error = %v, want conflict", err)
	}
	if len(store.records) != 4 {
		t.Fatalf("conflicting idempotency input wrote records: %d", len(store.records))
	}
}

func TestCreateSnapshotRejectsConflictingPlacementVersionsWithoutWrites(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 1)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Invalid root", "idempotency-conflict-001")
	request.ParentID, request.ExpectedRevision = snapshotA, 1
	request.Payload.Config = json.RawMessage(`{"format_version":3,"page":{"placements":[{"id":"p1","build":{"featureKey":"hero","version":"1.0.0"}},{"id":"p2","build":{"featureKey":"hero","version":"2.0.0"}}]}}`)
	if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("conflicting placement versions error = %v, want invalid", err)
	}
	if len(store.records) != 2 {
		t.Fatalf("invalid config wrote records beyond its page fixture: %d", len(store.records))
	}
}

func TestCreateSnapshotRejectsSiteModelInsidePageConfig(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 1)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Invalid model placement", "idempotency-invalid-model-page-01")
	request.ParentID, request.ExpectedRevision = snapshotA, 1
	request.Payload.Config = json.RawMessage(`{"format_version":3,"dataModel":{"formatVersion":1,"entities":[]},"page":{"placements":[]}}`)
	if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("page config with site model error = %v, want invalid", err)
	}
	if len(store.records) != 2 {
		t.Fatalf("invalid config wrote records beyond its page fixture: %d", len(store.records))
	}
}

func TestCreateSnapshotRejectsStaleOrProvisioningParent(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 2)
	provisioningID := "55555555-5555-4555-8555-555555555555"
	putSnapshotWithState(t, store, siteA, provisioningID, 1, "provisioning")
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	initialRecordCount := len(store.records)
	stale := createSnapshotRequest("Stale child", "idempotency-stale-child-001")
	stale.ParentID, stale.ExpectedRevision, stale.ExpectedContentRevision = snapshotA, 1, 1
	if _, err := service.CreateSnapshot(ctx, stale); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale parent revision error = %v, want conflict", err)
	}
	notReady := createSnapshotRequest("Blocked child", "idempotency-blocked-child-01")
	notReady.ParentID, notReady.ExpectedRevision, notReady.ExpectedContentRevision = provisioningID, 1, 1
	if _, err := service.CreateSnapshot(ctx, notReady); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("provisioning parent error = %v, want conflict", err)
	}
	if len(store.records) != initialRecordCount {
		t.Fatalf("rejected child requests wrote %d records", len(store.records)-initialRecordCount)
	}
}

func TestCreateSnapshotRequiresZeroContentRevisionForRootsAndNonnegativeForChildren(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 1)
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	root := createSnapshotRequest("Root", "idempotency-root-content-revision")
	if _, err := service.CreateSnapshot(ctx, root); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("direct root creation error = %v, want invalid", err)
	}
	root.ExpectedContentRevision = 1
	if _, err := service.CreateSnapshot(ctx, root); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("root with a content revision error = %v, want invalid", err)
	}
	child := createSnapshotRequest("Child", "idempotency-child-content-revision")
	child.ParentID, child.ExpectedRevision, child.ExpectedContentRevision = snapshotA, 1, -1
	if _, err := service.CreateSnapshot(ctx, child); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("child with a negative content revision error = %v, want invalid", err)
	}
	if len(store.records) != 2 {
		t.Fatalf("invalid revisions wrote records beyond their page fixture: %d", len(store.records))
	}
}

func TestSnapshotOperationsRequireConfiguredSitePagePair(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Unconfigured page", "idempotency-unconfigured-page-001")
	request.PageID = "unknown-page"
	if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("unconfigured page create error = %v, want not found", err)
	}
	if _, err := service.ListSnapshots(ctx, siteA, "unknown-page", "", 10); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("unconfigured page list error = %v, want not found", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("unconfigured page operations wrote %d records", len(store.records))
	}
}

func TestCreatePageRegistersReadyRootAndSupportsIdempotentReplay(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := websiteapi.CreatePageRequest{SiteID: siteA, Name: "Landing", IdempotencyKey: "idempotency-create-page-001"}
	created, err := service.CreatePage(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	root, err := service.GetSnapshot(ctx, created.LatestSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "Landing" || root.PageID != created.ID || root.State != "ready" || root.Sequence != 1 || root.ParentID != "" {
		t.Fatalf("created page/root = %#v / %#v", created, root)
	}
	if page, err := service.GetPage(ctx, siteA, created.ID); err != nil || page.ID != created.ID {
		t.Fatalf("GetPage = %#v, %v", page, err)
	}
	listed, err := service.ListPages(ctx, siteA, "", 1)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("ListPages = %#v, %v", listed, err)
	}
	replayed, err := service.CreatePage(ctx, request)
	if err != nil || replayed.ID != created.ID || replayed.LatestSnapshotID != created.LatestSnapshotID {
		t.Fatalf("CreatePage replay = %#v, %v", replayed, err)
	}
	if len(store.records) != 4 {
		t.Fatalf("page create/replay stored %d records, want atomic root/page/tree/receipt set", len(store.records))
	}
	if _, err := service.GetPage(ctx, siteB, created.ID); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("cross-site GetPage = %v, want not found", err)
	}
}

func TestPageTemplatesCRUDAndApplyMaterializesExactRevision(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	configuration := websiteapi.PageTemplateConfiguration{SchemaVersion: 1, FeaturePins: map[string]string{}, PlacementSlots: []websiteapi.PlacementSlot{},
		Payload: websiteapi.SnapshotPayload{Config: json.RawMessage(`{"format_version":3,"page":{"name":"Starter","placements":[]}}`), Metadata: json.RawMessage(`{}`)}}
	createdTemplate, err := service.CreatePageTemplate(ctx, websiteapi.CreatePageTemplateRequest{SiteID: siteA, Name: "Starter template", Description: "Reusable layout", Configuration: configuration, IdempotencyKey: "idempotency-template-create-001"})
	if err != nil {
		t.Fatal(err)
	}
	if createdTemplate.Revision != 1 || len(createdTemplate.Digest) != 64 {
		t.Fatalf("created template = %#v", createdTemplate)
	}
	listed, err := service.ListPageTemplates(ctx, siteA, "", 10)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != createdTemplate.ID {
		t.Fatalf("ListPageTemplates = %#v, %v", listed, err)
	}
	loaded, err := service.GetPageTemplate(ctx, siteA, createdTemplate.ID)
	if err != nil || loaded.Digest != createdTemplate.Digest {
		t.Fatalf("GetPageTemplate = %#v, %v", loaded, err)
	}
	page, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{SiteID: siteA, Name: "Custom landing", TemplateID: createdTemplate.ID, TemplateRevision: 1, IdempotencyKey: "idempotency-page-from-template-01"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := service.GetSnapshot(ctx, page.LatestSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	var rootConfig struct {
		Page struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"page"`
	}
	if json.Unmarshal(root.Payload.Config, &rootConfig) != nil || rootConfig.Page.ID != page.ID || rootConfig.Page.Name != "Custom landing" {
		t.Fatalf("template root did not preserve generated page identity/name override: %s", root.Payload.Config)
	}
	var provenance map[string]map[string]any
	if json.Unmarshal(root.Payload.Metadata, &provenance) != nil || provenance["template_source"]["template_id"] != createdTemplate.ID || provenance["template_source"]["template_revision"] != float64(1) {
		t.Fatalf("template source provenance = %#v", provenance)
	}
	if _, err := service.CreatePage(ctx, websiteapi.CreatePageRequest{SiteID: siteA, Name: "Stale template", TemplateID: createdTemplate.ID, TemplateRevision: 2, IdempotencyKey: "idempotency-stale-template-page-01"}); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("non-current template revision create error = %v, want conflict", err)
	}

	configuration.Payload.Config = json.RawMessage(`{"format_version":3,"page":{"name":"Updated starter","placements":[]}}`)
	updated, err := service.UpdatePageTemplate(ctx, websiteapi.UpdatePageTemplateRequest{SiteID: siteA, TemplateID: createdTemplate.ID, ExpectedRevision: 1, Name: "Updated template", Description: "Updated layout", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Digest == createdTemplate.Digest || len(updated.Digest) != 64 {
		t.Fatalf("updated template revision/digest = %#v", updated)
	}
	if _, err := service.UpdatePageTemplate(ctx, websiteapi.UpdatePageTemplateRequest{SiteID: siteA, TemplateID: createdTemplate.ID, ExpectedRevision: 1, Name: "Stale", Configuration: configuration}); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale template update = %v, want conflict", err)
	}

	apply := websiteapi.ApplyPageTemplateRequest{SiteID: siteA, PageID: page.ID, TemplateID: updated.ID, TemplateRevision: updated.Revision,
		ExpectedSnapshotID: root.ID, ExpectedSnapshotRevision: root.Revision, ExpectedContentRevision: 0, Confirmed: true,
		IdempotencyKey: "idempotency-template-apply-001"}
	if _, err := service.ApplyPageTemplate(ctx, websiteapi.ApplyPageTemplateRequest{SiteID: siteA, PageID: page.ID, TemplateID: updated.ID, TemplateRevision: updated.Revision,
		ExpectedSnapshotID: root.ID, ExpectedSnapshotRevision: root.Revision, Confirmed: false, IdempotencyKey: "idempotency-unconfirmed-template-01"}); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("unconfirmed template application = %v, want invalid", err)
	}
	child, err := service.ApplyPageTemplate(ctx, apply)
	if err != nil {
		t.Fatal(err)
	}
	if child.State != "provisioning" || child.ParentID != root.ID || child.Sequence != 2 {
		t.Fatalf("template child snapshot = %#v", child)
	}
	var childPage struct {
		Page struct {
			Name string `json:"name"`
		} `json:"page"`
	}
	if json.Unmarshal(child.Payload.Config, &childPage) != nil || childPage.Page.Name != "Updated starter" {
		t.Fatalf("applied template config = %s", child.Payload.Config)
	}
	var childMetadata map[string]map[string]any
	if json.Unmarshal(child.Payload.Metadata, &childMetadata) != nil || childMetadata["template_application"]["selection_policy"] != "reset" || childMetadata["template_application"]["template_revision"] != float64(2) {
		t.Fatalf("template application provenance = %#v", childMetadata)
	}
	parentAfter, err := service.GetSnapshot(ctx, root.ID)
	if err != nil || parentAfter.State != root.State || string(parentAfter.Payload.Config) != string(root.Payload.Config) {
		t.Fatalf("template application mutated parent: %#v, %v", parentAfter, err)
	}
	replayed, err := service.ApplyPageTemplate(ctx, apply)
	if err != nil || replayed.ID != child.ID {
		t.Fatalf("template application replay = %#v, %v", replayed, err)
	}
	if err := service.DeletePageTemplate(ctx, websiteapi.DeletePageTemplateRequest{SiteID: siteA, TemplateID: updated.ID, ExpectedRevision: 1}); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale template deletion = %v, want conflict", err)
	}
	if err := service.DeletePageTemplate(ctx, websiteapi.DeletePageTemplateRequest{SiteID: siteA, TemplateID: updated.ID, ExpectedRevision: updated.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetPageTemplate(ctx, siteA, updated.ID); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("deleted template lookup = %v, want not found", err)
	}
}

func createSnapshotRequest(name, idempotencyKey string) websiteapi.CreateSnapshotRequest {
	return websiteapi.CreateSnapshotRequest{
		SiteID: siteA, PageID: pageA, Name: name, IdempotencyKey: idempotencyKey,
		Payload: websiteapi.SnapshotPayload{
			Config:   json.RawMessage(`{"format_version":3,"features":[{"featureKey":"stale","version":"9.9.9"}],"page":{"placements":[{"id":"placement-1","build":{"featureKey":"hero","version":"1.2.0"}},{"id":"placement-2","build":{"featureKey":"hero","version":"1.2.0"}}]}}`),
			Metadata: json.RawMessage(`{}`),
		},
	}
}

func newService(t *testing.T, store *memoryStore, level core.AccessLevel, config websiteapp.Config) *websiteapp.Service {
	return newServiceWithLevels(t, store, level, level, config)
}

func newServiceWithLevels(t *testing.T, store *memoryStore, level, coordinatorLevel core.AccessLevel, config websiteapp.Config) *websiteapp.Service {
	t.Helper()
	authority := &fakeAuthority{level: level, coordinatorLevel: coordinatorLevel}
	service, err := websiteapp.NewService(config, store, authority, fakeSessions{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestWorkerActionsRequireCoordinatorWriteAccess(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 1)
	service := newServiceWithLevels(t, store, core.AccessLevelWrite, core.AccessLevelRead, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.BeginFeatureImport(ctx, websiteapi.ImportFeatureRequest{}); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("feature import with coordinator read access = %v, want forbidden", err)
	}
	if _, err := service.CreateMediaDerivative(ctx, websiteapi.CreateDerivativeRequest{}); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("media derivative with coordinator read access = %v, want forbidden", err)
	}
	if _, err := service.BuildSnapshot(ctx, websiteapi.BuildSnapshotRequest{SnapshotID: snapshotA}); !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("snapshot build with coordinator read access = %v, want forbidden", err)
	}
}

func websiteConfig() websiteapp.Config {
	return websiteapp.Config{AccessGroup: "features", CoordinatorAccessGroup: "coordinator_jobs", Sites: []string{siteA, siteB}}
}

func putSnapshot(t *testing.T, store *memoryStore, siteID, id string, revision int64) {
	putSnapshotWithState(t, store, siteID, id, revision, "ready")
}

func putSnapshotWithState(t *testing.T, store *memoryStore, siteID, id string, revision int64, state string) {
	putSnapshotForPageWithState(t, store, siteID, pageA, id, revision, state)
}

func putSnapshotForPageWithState(t *testing.T, store *memoryStore, siteID, pageID, id string, revision int64, state string) {
	t.Helper()
	now := time.Now().UTC()
	snapshot := websiteapi.Snapshot{
		ID: id, SiteID: siteID, PageID: pageID, Name: "Home", Sequence: 7, FeaturePins: map[string]string{}, State: state, CreatedAt: now,
		Payload: &websiteapi.SnapshotPayload{Config: mustJSON(map[string]any{"format_version": 3, "page": map[string]any{"id": pageID, "name": "Home", "placements": []any{}}}), Metadata: json.RawMessage(`{}`)},
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	key := snapshotRecordKeyForPage(siteID, pageID, id)
	store.records[key] = core.ModuleDataRecord{Payload: payload, PayloadVersion: 1, IndexData: mustJSON(map[string]any{"record_type": "snapshot", "snapshot_id": id, "page_id": pageID, "state": state, "revision": revision, "parent_id": ""}), Revision: revision, CreatedAt: now, UpdatedAt: now}
	putPageRecord(t, store, websiteapi.Page{ID: pageID, SiteID: siteID, Name: snapshot.Name, LatestSnapshotID: id, LatestSnapshotState: state, CreatedAt: now})
}

func putPageRecord(t *testing.T, store *memoryStore, page websiteapi.Page) {
	t.Helper()
	key := websiterepo.PageKey(page.SiteID, page.ID)
	revision := store.records[key].Revision + 1
	if revision == 1 && page.CreatedAt.IsZero() {
		page.CreatedAt = time.Now().UTC()
	}
	store.records[key] = core.ModuleDataRecord{Payload: mustJSON(map[string]any{"page": page}), PayloadVersion: 1,
		IndexData: mustJSON(map[string]any{"record_type": "page", "page_id": page.ID, "snapshot_id": page.LatestSnapshotID, "state": page.LatestSnapshotState, "revision": revision}),
		Revision:  revision, CreatedAt: page.CreatedAt, UpdatedAt: time.Now().UTC()}
}

func snapshotRecordKey(siteID, id string) core.ModuleDataKey {
	return snapshotRecordKeyForPage(siteID, pageA, id)
}

func snapshotRecordKeyForPage(siteID, pageID, id string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: "website/snapshots", ScopeType: "site", ScopeID: siteID, RecordKey: "snapshot/" + pageID + "/" + id}
}

func setSnapshotParent(t *testing.T, store *memoryStore, siteID, id, parentID, parentName string, parentSequence int64) {
	t.Helper()
	key := snapshotRecordKey(siteID, id)
	record := store.records[key]
	var snapshot websiteapi.Snapshot
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.ParentID = parentID
	snapshot.ParentName = parentName
	snapshot.ParentSequence = parentSequence
	record.Payload = mustJSON(snapshot)
	var index map[string]any
	if err := json.Unmarshal(record.IndexData, &index); err != nil {
		t.Fatal(err)
	}
	index["parent_id"] = parentID
	record.IndexData = mustJSON(index)
	store.records[key] = record
}

func putRecord(t *testing.T, store *memoryStore, key core.ModuleDataKey, payload, index map[string]any) {
	t.Helper()
	store.records[key] = core.ModuleDataRecord{Payload: mustJSON(payload), PayloadVersion: 1, IndexData: mustJSON(index), Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

type fakeAuthority struct {
	level            core.AccessLevel
	coordinatorLevel core.AccessLevel
}

func (a *fakeAuthority) Authenticate(_ context.Context, credential string) (core.Session, core.Principal, error) {
	if credential != "credential" {
		return core.Session{}, core.Principal{}, core.ErrUnauthorized
	}
	return core.Session{}, core.Principal{ID: "principal-1", Active: true}, nil
}

func (a *fakeAuthority) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, minimum core.AccessLevel) error {
	if len(groups) != 1 {
		return errors.New("unexpected access group")
	}
	var level core.AccessLevel
	switch groups[0] {
	case "features":
		level = a.level
	case "coordinator_jobs":
		level = a.coordinatorLevel
	default:
		return errors.New("unexpected access group")
	}
	if level < minimum {
		return core.ErrForbidden
	}
	return nil
}

type fakeSessions struct{}

func (fakeSessions) CreateSession(context.Context, string) (core.CreatedSession, error) {
	return core.CreatedSession{}, core.ErrUnauthorized
}

func (fakeSessions) AuthenticateSession(_ context.Context, token string) (core.Session, core.Principal, error) {
	if token != "session" {
		return core.Session{}, core.Principal{}, core.ErrUnauthorized
	}
	return core.Session{}, core.Principal{ID: "principal-1", Active: true}, nil
}

func (fakeSessions) RevokeSession(context.Context, string) error { return nil }

type memoryStore struct {
	records      map[core.ModuleDataKey]core.ModuleDataRecord
	maxPageSize  int
	maxBatchSize int
	maxPayload   int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: map[core.ModuleDataKey]core.ModuleDataRecord{}, maxPageSize: 100, maxBatchSize: 100, maxPayload: 16 << 20}
}

func (s *memoryStore) MaxPageSize() int { return s.maxPageSize }

func (s *memoryStore) MaxBatchSize() int { return s.maxBatchSize }

func (s *memoryStore) MaxPayloadBytes() int { return s.maxPayload }

func (s *memoryStore) Get(_ context.Context, key core.ModuleDataKey) (core.ModuleDataRecord, error) {
	record, ok := s.records[key]
	if !ok {
		return core.ModuleDataRecord{}, core.ErrModuleDataNotFound
	}
	return record, nil
}

func (s *memoryStore) Batch(_ context.Context, mutations []core.ModuleDataMutation) error {
	if len(mutations) == 0 {
		return core.ErrModuleDataInvalid
	}
	first := mutations[0].Key
	seen := map[core.ModuleDataKey]struct{}{}
	for _, mutation := range mutations {
		if mutation.Key.Namespace != first.Namespace || mutation.Key.ScopeType != first.ScopeType || mutation.Key.ScopeID != first.ScopeID {
			return core.ErrModuleDataInvalid
		}
		if _, exists := seen[mutation.Key]; exists {
			return core.ErrModuleDataInvalid
		}
		seen[mutation.Key] = struct{}{}
		current := s.records[mutation.Key]
		if current.Revision != mutation.ExpectedRevision {
			return core.ErrModuleDataConflict
		}
		if mutation.Value == nil && mutation.ExpectedRevision == 0 {
			return core.ErrModuleDataInvalid
		}
	}
	for _, mutation := range mutations {
		if mutation.Value == nil {
			delete(s.records, mutation.Key)
			continue
		}
		current := s.records[mutation.Key]
		s.records[mutation.Key] = core.ModuleDataRecord{
			Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
			IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: current.Revision + 1,
			CreatedAt: current.CreatedAt, UpdatedAt: time.Now().UTC(),
		}
		if current.Revision == 0 {
			s.records[mutation.Key] = core.ModuleDataRecord{
				Payload: append(json.RawMessage(nil), mutation.Value.Payload...), PayloadVersion: mutation.Value.PayloadVersion,
				IndexData: append(json.RawMessage(nil), mutation.Value.IndexData...), Revision: 1,
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}
		}
	}
	return nil
}

func (s *memoryStore) List(_ context.Context, namespace, scopeType, scopeID, after string, selectors []core.ModuleDataSelector, limit int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error) {
	if limit > s.maxPageSize || limit < 1 {
		return nil, nil, "", core.ErrModuleDataLimitExceeded
	}
	keys := make([]core.ModuleDataKey, 0)
	for key, record := range s.records {
		if key.Namespace != namespace || key.ScopeType != scopeType || key.ScopeID != scopeID || key.RecordKey <= after || !matchesSelectors(record.IndexData, selectors) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].RecordKey < keys[j].RecordKey })
	if len(keys) <= limit {
		records := make([]core.ModuleDataRecord, len(keys))
		for i, key := range keys {
			records[i] = s.records[key]
		}
		return keys, records, "", nil
	}
	pageKeys := keys[:limit]
	records := make([]core.ModuleDataRecord, len(pageKeys))
	for i, key := range pageKeys {
		records[i] = s.records[key]
	}
	return pageKeys, records, pageKeys[len(pageKeys)-1].RecordKey, nil
}

func matchesSelectors(raw json.RawMessage, selectors []core.ModuleDataSelector) bool {
	var index map[string]any
	if json.Unmarshal(raw, &index) != nil {
		return false
	}
	for _, selector := range selectors {
		actual, ok := index[selector.Field]
		if !ok || selector.Operator != core.SelectorEqual || len(selector.Values) != 1 || actual != selector.Values[0] {
			return false
		}
	}
	return true
}
