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
)

const (
	siteA     = "site-a"
	siteB     = "site-b"
	branchA   = "production"
	snapshotA = "33333333-3333-4333-8333-333333333333"
	snapshotB = "44444444-4444-4444-8444-444444444444"
)

func TestListBranchesCombinesStaticTopologyWithPersistedAssignment(t *testing.T) {
	store := newMemoryStore()
	putRecord(t, store, core.ModuleDataKey{Namespace: "website/snapshots", ScopeType: "site", ScopeID: siteA, RecordKey: "branch/" + branchA}, map[string]any{
		"id": branchA, "snapshot_id": snapshotA, "updated_at": time.Now().UTC(),
	}, map[string]any{"record_type": "branch", "branch_id": branchA, "snapshot_id": snapshotA, "revision": float64(1)})
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.ListBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("branch count = %d, want 1", len(page.Items))
	}
	got := page.Items[0]
	if got.ID != branchA || got.SiteID != siteA || got.ListenAddress != "127.0.0.1:8081" || got.SnapshotID == nil || *got.SnapshotID != snapshotA || got.Revision != 1 {
		t.Fatalf("branch = %#v", got)
	}
	if !got.Indexable || !got.Enabled || got.Name != "Production" {
		t.Fatalf("static branch policy was not preserved: %#v", got)
	}
}

func TestAssignBranchUsesRevisionCASAndPersistsEventAtomically(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	assigned, err := service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotA, ExpectedRevision: 0, ExpectedSnapshotRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if assigned.SnapshotID == nil || *assigned.SnapshotID != snapshotA || assigned.Revision != 1 {
		t.Fatalf("assignment = %#v", assigned)
	}

	events, err := service.ListBranchEvents(ctx, branchA, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("event count = %d, want 1", len(events.Items))
	}
	event := events.Items[0]
	if event.BranchID != branchA || event.SnapshotID != snapshotA || event.SnapshotName != "Home" || event.SnapshotSequence != 7 || event.Action != "assigned" {
		t.Fatalf("event = %#v", event)
	}
	if event.CreatedAt.IsZero() {
		t.Fatal("assignment event must have a timestamp")
	}
}

func TestAssignBranchRejectsCrossSiteSnapshotWithoutPartialWrites(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteB, snapshotB, 2)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotB, ExpectedRevision: 0, ExpectedSnapshotRevision: 2,
	})
	if !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("cross-site snapshot assignment error = %v, want not found", err)
	}
	if len(store.records) != 1 {
		t.Fatalf("cross-site rejection left %d records; want only the original snapshot", len(store.records))
	}
}

func TestAssignBranchRejectsStaleSnapshotRevisionWithoutAssignmentOrEvent(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotA, ExpectedRevision: 0, ExpectedSnapshotRevision: 2,
	})
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale snapshot revision error = %v, want conflict", err)
	}
	if len(store.records) != 1 {
		t.Fatalf("stale assignment left %d records; want only the original snapshot", len(store.records))
	}
}

func TestAssignBranchRejectsProvisioningSnapshotWithoutPartialWrites(t *testing.T) {
	store := newMemoryStore()
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: snapshotA, ExpectedRevision: 0, ExpectedSnapshotRevision: 3,
	})
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("provisioning snapshot assignment error = %v, want conflict", err)
	}
	if len(store.records) != 1 {
		t.Fatalf("rejected assignment left %d records; want only the provisioning snapshot", len(store.records))
	}
}

func TestBuildRejectsProvisioningSnapshot(t *testing.T) {
	store := newMemoryStore()
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.BuildSnapshot(ctx, websiteapi.BuildSnapshotRequest{SnapshotID: snapshotA})
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("provisioning snapshot build error = %v, want conflict", err)
	}
}

func TestMarkSnapshotReadyUsesRevisionCASAndIsIdempotent(t *testing.T) {
	store := newMemoryStore()
	rootID := "66666666-6666-4666-8666-666666666666"
	putSnapshot(t, store, siteA, rootID, 1)
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	setSnapshotParent(t, store, siteA, snapshotA, rootID, "Home", 7)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	ready, err := service.MarkSnapshotReady(ctx, snapshotA, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != "ready" || ready.Revision != 4 {
		t.Fatalf("finalized snapshot = %#v, want ready revision 4", ready)
	}
	retried, err := service.MarkSnapshotReady(ctx, snapshotA, 3)
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != "ready" || retried.Revision != 4 {
		t.Fatalf("idempotent retry changed snapshot: %#v", retried)
	}
	if store.records[snapshotRecordKey(siteA, snapshotA)].Revision != 4 {
		t.Fatalf("stored revision = %d, want 4", store.records[snapshotRecordKey(siteA, snapshotA)].Revision)
	}
}

func TestMarkSnapshotReadyRejectsStaleRevisionWithoutMutation(t *testing.T) {
	store := newMemoryStore()
	rootID := "66666666-6666-4666-8666-666666666666"
	putSnapshot(t, store, siteA, rootID, 1)
	putSnapshotWithState(t, store, siteA, snapshotA, 3, "provisioning")
	setSnapshotParent(t, store, siteA, snapshotA, rootID, "Home", 7)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
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
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignBranch(ctx, websiteapi.AssignBranchRequest{BranchID: branchA, SnapshotID: rootID, ExpectedRevision: 0, ExpectedSnapshotRevision: 1}); err != nil {
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

func TestDeleteSnapshotProtectsBranchAssignmentAndItsImmediateParent(t *testing.T) {
	for _, assignedID := range []string{"77777777-7777-4777-8777-777777777777", "88888888-8888-4888-8888-888888888888"} {
		t.Run("assigned-"+assignedID[:8], func(t *testing.T) {
			store := newMemoryStore()
			rootID := "66666666-6666-4666-8666-666666666666"
			targetID := "77777777-7777-4777-8777-777777777777"
			childID := "88888888-8888-4888-8888-888888888888"
			putSnapshot(t, store, siteA, rootID, 1)
			putSnapshot(t, store, siteA, targetID, 1)
			putSnapshot(t, store, siteA, childID, 1)
			setSnapshotParent(t, store, siteA, targetID, rootID, "Home", 7)
			setSnapshotParent(t, store, siteA, childID, targetID, "Home", 7)
			service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
			ctx, err := service.Authenticate(context.Background(), "credential")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.AssignBranch(ctx, websiteapi.AssignBranchRequest{BranchID: branchA, SnapshotID: assignedID, ExpectedRevision: 0, ExpectedSnapshotRevision: 1}); err != nil {
				t.Fatal(err)
			}
			before := len(store.records)

			err = service.DeleteSnapshot(ctx, websiteapi.DeleteSnapshotRequest{SnapshotID: targetID, ExpectedRevision: 1})
			if !errors.Is(err, websiteapi.ErrConflict) {
				t.Fatalf("protected deletion error = %v, want conflict", err)
			}
			if len(store.records) != before || store.records[snapshotRecordKey(siteA, childID)].Revision != 1 {
				t.Fatal("protected deletion partially changed snapshot records")
			}
		})
	}
}

func TestDeleteSnapshotRejectsStaleRevisionWithoutMutation(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
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

func TestListBranchEventsPaginatesWithBranchScopedOpaqueCursor(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 1)
	secondSnapshotID := "55555555-5555-4555-8555-555555555555"
	putSnapshot(t, store, siteA, secondSnapshotID, 1)
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	for i, snapshotID := range []string{snapshotA, secondSnapshotID, snapshotA} {
		if _, err := service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
			BranchID: branchA, SnapshotID: snapshotID, ExpectedRevision: int64(i), ExpectedSnapshotRevision: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var ids []string
	cursor := ""
	for i := 0; i < 4; i++ {
		page, err := service.ListBranchEvents(ctx, branchA, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			break
		}
		ids = append(ids, page.Items[0].ID)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(ids) != 3 || ids[0] == ids[1] || ids[1] == ids[2] || ids[0] == ids[2] {
		t.Fatalf("event IDs = %v, want three distinct events", ids)
	}
	if _, err := service.ListBranchEvents(ctx, "unknown", cursor, 1); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("unknown branch error = %v, want not found", err)
	}
}

func TestAssignBranchRequiresWriteLevelBeforeLookingUpTarget(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.AssignBranch(ctx, websiteapi.AssignBranchRequest{
		BranchID: branchA, SnapshotID: "missing", ExpectedRevision: 0, ExpectedSnapshotRevision: 0,
	})
	if !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("write without level 2 error = %v, want forbidden", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("denied assignment wrote %d records", len(store.records))
	}
}

func TestListSnapshotsPaginatesDeterministicallyAcrossConfiguredSites(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 2)
	putSnapshot(t, store, siteA, "55555555-5555-4555-8555-555555555555", 1)
	putSnapshot(t, store, siteB, snapshotB, 4)
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	cursor := ""
	for pages := 0; pages < 4; pages++ {
		page, err := service.ListSnapshots(ctx, "", cursor, 1)
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
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSnapshots(ctx, siteA, "", 10); !errors.Is(err, websiteapi.ErrUnavailable) {
		t.Fatalf("unknown lifecycle state error = %v, want unavailable", err)
	}
}

func TestGetSnapshotPreservesSnapshotScopedDataModel(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 3)
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
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
	if string(config["dataModel"]) != `{"formatVersion":1,"entities":[]}` {
		t.Fatalf("snapshot config lost its dataModel: %s", snapshot.Payload.Config)
	}
}

func TestSnapshotReadRequiresReadAccessBeforeLookingUpTarget(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelNone, oneBranchConfig())
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
	service := newService(t, store, core.AccessLevelRead, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListSnapshots(ctx, "unknown", "", 10); !errors.Is(err, websiteapi.ErrNotFound) {
		t.Fatalf("unknown site error = %v, want not found", err)
	}
	if _, err := service.ListSnapshots(ctx, siteA, "not-a-cursor", 10); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("malformed cursor error = %v, want invalid", err)
	}
}

func TestCreateSnapshotRootAndChildUseSequenceAndLifecycle(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}

	rootRequest := createSnapshotRequest("Root", "idempotency-root-001")
	root, err := service.CreateSnapshot(ctx, rootRequest)
	if err != nil {
		t.Fatal(err)
	}
	if root.State != "ready" || root.Sequence != 1 || root.Revision != 1 || root.ParentID != "" {
		t.Fatalf("root snapshot = %#v", root)
	}
	if root.FeaturePins["hero"] != "1.2.0" || len(root.FeaturePins) != 1 {
		t.Fatalf("feature pins were not derived only from placements: %#v", root.FeaturePins)
	}

	childRequest := createSnapshotRequest("Child", "idempotency-child-001")
	childRequest.ParentID = root.ID
	childRequest.ExpectedRevision = root.Revision
	childRequest.ExpectedContentRevision = 99 // Content owns this precondition.
	child, err := service.CreateSnapshot(ctx, childRequest)
	if err != nil {
		t.Fatal(err)
	}
	if child.State != "provisioning" || child.Sequence != 2 || child.ParentID != root.ID || child.ParentName != root.Name || child.ParentSequence != root.Sequence {
		t.Fatalf("child snapshot = %#v", child)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(child.Payload.Config, &config); err != nil || len(config["dataModel"]) == 0 {
		t.Fatalf("child snapshot lost snapshot-scoped dataModel: %s", child.Payload.Config)
	}
	ready, err := service.MarkSnapshotReady(ctx, child.ID, child.Revision)
	if err != nil || ready.State != "ready" || ready.Revision != child.Revision+1 {
		t.Fatalf("child finalization = %#v, error = %v", ready, err)
	}
}

func TestCreateChildSnapshotAcceptsUntouchedContentRevisionZero(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	root, err := service.CreateSnapshot(ctx, createSnapshotRequest("Root", "idempotency-zero-root-001"))
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Child", "idempotency-zero-child-001")
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

func TestCreateSnapshotReplaysIdempotencyAndConflictsOnDifferentInput(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Root", "idempotency-replay-001")
	first, err := service.CreateSnapshot(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateSnapshot(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(store.records) != 3 {
		t.Fatalf("replay created another snapshot: first=%s second=%s record_count=%d", first.ID, second.ID, len(store.records))
	}

	request.Name = "Different root"
	if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("reused idempotency key error = %v, want conflict", err)
	}
	if len(store.records) != 3 {
		t.Fatalf("conflicting idempotency input wrote records: %d", len(store.records))
	}
}

func TestCreateSnapshotRejectsConflictingPlacementVersionsWithoutWrites(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Invalid root", "idempotency-conflict-001")
	request.Payload.Config = json.RawMessage(`{"pages":[{"placements":[{"build":{"featureKey":"hero","version":"1.0.0"}},{"build":{"featureKey":"hero","version":"2.0.0"}}]}]}`)
	if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("conflicting placement versions error = %v, want invalid", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("invalid config wrote %d records", len(store.records))
	}
}

func TestCreateSnapshotRejectsDataModelPositionForFieldID(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	request := createSnapshotRequest("Invalid position", "idempotency-invalid-position-01")
	request.Payload.Config = json.RawMessage(`{"dataModel":{"formatVersion":1,"entities":[{"id":"66666666-6666-4666-8666-666666666666","key":"model_product","name":"Product","cardinality":"many","fields":[{"id":"77777777-7777-4777-8777-777777777777","key":"field_name","name":"Name","type":"text"}]}],"positions":{"77777777-7777-4777-8777-777777777777":{"x":1,"y":2}}}}`)
	if _, err := service.CreateSnapshot(ctx, request); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("field keyed position error = %v, want invalid", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("invalid data model wrote %d records", len(store.records))
	}
}

func TestCreateSnapshotRejectsStaleOrProvisioningParent(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, siteA, snapshotA, 2)
	provisioningID := "55555555-5555-4555-8555-555555555555"
	putSnapshotWithState(t, store, siteA, provisioningID, 1, "provisioning")
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
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
	if len(store.records) != 2 {
		t.Fatalf("rejected child requests wrote %d records", len(store.records)-2)
	}
}

func TestCreateSnapshotRequiresZeroContentRevisionForRootsAndNonnegativeForChildren(t *testing.T) {
	store := newMemoryStore()
	service := newService(t, store, core.AccessLevelWrite, oneBranchConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	root := createSnapshotRequest("Root", "idempotency-root-content-revision")
	root.ExpectedContentRevision = 1
	if _, err := service.CreateSnapshot(ctx, root); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("root with a content revision error = %v, want invalid", err)
	}
	child := createSnapshotRequest("Child", "idempotency-child-content-revision")
	child.ParentID, child.ExpectedRevision, child.ExpectedContentRevision = snapshotA, 1, -1
	if _, err := service.CreateSnapshot(ctx, child); !errors.Is(err, websiteapi.ErrInvalid) {
		t.Fatalf("child with a negative content revision error = %v, want invalid", err)
	}
	if len(store.records) != 0 {
		t.Fatalf("invalid revisions wrote %d records", len(store.records))
	}
}

func createSnapshotRequest(name, idempotencyKey string) websiteapi.CreateSnapshotRequest {
	return websiteapi.CreateSnapshotRequest{
		SiteID: siteA, Name: name, IdempotencyKey: idempotencyKey,
		Payload: websiteapi.SnapshotPayload{
			Config:   json.RawMessage(`{"dataModel":{"formatVersion":1,"entities":[]},"features":[{"featureKey":"stale","version":"9.9.9"}],"pages":[{"placements":[{"build":{"featureKey":"hero","version":"1.2.0"}},{"build":{"featureKey":"hero","version":"1.2.0"}}]}]}`),
			Metadata: json.RawMessage(`{}`), DesignSystem: json.RawMessage(`{}`),
		},
	}
}

func newService(t *testing.T, store *memoryStore, level core.AccessLevel, config websiteapp.Config) *websiteapp.Service {
	t.Helper()
	authority := &fakeAuthority{level: level}
	service, err := websiteapp.NewService(config, store, authority, fakeSessions{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func oneBranchConfig() websiteapp.Config {
	return websiteapp.Config{
		AccessGroup: "features",
		Sites:       []string{siteA, siteB},
		Branches: []websiteapp.BranchConfig{{
			ID: branchA, Name: "Production", SiteID: siteA, ListenAddress: "127.0.0.1:8081",
			CORSOrigins: []string{"https://www.example.test"}, Indexable: true, Enabled: true,
		}},
	}
}

func putSnapshot(t *testing.T, store *memoryStore, siteID, id string, revision int64) {
	putSnapshotWithState(t, store, siteID, id, revision, "ready")
}

func putSnapshotWithState(t *testing.T, store *memoryStore, siteID, id string, revision int64, state string) {
	t.Helper()
	snapshot := websiteapi.Snapshot{
		ID: id, SiteID: siteID, Name: "Home", Sequence: 7, FeaturePins: map[string]string{}, State: state,
		Payload: &websiteapi.SnapshotPayload{Config: json.RawMessage(`{"dataModel":{"formatVersion":1,"entities":[]}}`), Metadata: json.RawMessage(`{}`), DesignSystem: json.RawMessage(`{}`)},
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	key := snapshotRecordKey(siteID, id)
	store.records[key] = core.ModuleDataRecord{Payload: payload, PayloadVersion: 1, IndexData: mustJSON(map[string]any{"record_type": "snapshot", "state": state, "revision": float64(revision)}), Revision: revision, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
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

func snapshotRecordKey(siteID, id string) core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: "website/snapshots", ScopeType: "site", ScopeID: siteID, RecordKey: "snapshot/" + id}
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

type fakeAuthority struct{ level core.AccessLevel }

func (a *fakeAuthority) Authenticate(_ context.Context, credential string) (core.Session, core.Principal, error) {
	if credential != "credential" {
		return core.Session{}, core.Principal{}, core.ErrUnauthorized
	}
	return core.Session{}, core.Principal{ID: "principal-1", Active: true}, nil
}

func (a *fakeAuthority) RequireGroupLevel(_ context.Context, _ core.Session, groups []string, minimum core.AccessLevel) error {
	if len(groups) != 1 || groups[0] != "features" {
		return errors.New("unexpected access group")
	}
	if a.level < minimum {
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
