package application_test

import (
	"context"
	"errors"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"testing"
	"time"

	platform "github.com/Zubrium/cdn/engines/website/application/platform"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

func retireRequest(pageID string, revision int64, state, jobID string) websiteapi.RetirePageRequest {
	return websiteapi.RetirePageRequest{
		PageID: pageID, ExpectedRevision: revision,
		RetirementJobID: jobID, LifecycleState: state, IdempotencyKey: "retire-" + jobID,
	}
}

func putActivePageAtRevision(t *testing.T, store *memoryStore, pageID string, revision int64) {
	t.Helper()
	now := time.Now().UTC()
	page := websiteapi.Page{ID: pageID, Name: "Home", LifecycleState: "active", LatestSnapshotID: snapshotA, LatestSnapshotState: "ready", CreatedAt: now}
	key := websiterepo.PageKey(pageID)
	store.records[key] = recorddata.Record{
		Payload:        mustJSON(map[string]any{"page": page}),
		PayloadVersion: 1,
		IndexData:      mustJSON(map[string]any{"record_type": "page", "page_id": pageID, "snapshot_id": snapshotA, "state": "ready", "revision": revision}),
		Revision:       revision, CreatedAt: now, UpdatedAt: now,
	}
}

func TestRetirePageRequiresManageAccess(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, snapshotA, 1)
	putActivePageAtRevision(t, store, pageA, 2)
	service := newServiceWithLevels(t, store, platform.AccessLevelRead, platform.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.RetirePage(ctx, retireRequest(pageA, 2, "retiring", "11111111-1111-4111-8111-111111111111"))
	if !errors.Is(err, websiteapi.ErrForbidden) {
		t.Fatalf("RetirePage with write access = %v, want forbidden", err)
	}
}

func TestRetirePageTransitionsAndIsIdempotent(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, snapshotA, 1)
	putActivePageAtRevision(t, store, pageA, 2)
	service := newServiceWithLevels(t, store, 2, platform.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	jobID := "11111111-1111-4111-8111-111111111111"
	retiring, err := service.RetirePage(ctx, retireRequest(pageA, 2, "retiring", jobID))
	if err != nil {
		t.Fatalf("retiring: %v", err)
	}
	if retiring.LifecycleState != "retiring" || retiring.RetirementJobID != jobID {
		t.Fatalf("retiring page = %#v", retiring)
	}
	replay, err := service.RetirePage(ctx, retireRequest(pageA, 2, "retiring", jobID))
	if err != nil || replay.LifecycleState != "retiring" || replay.Revision != retiring.Revision {
		t.Fatalf("idempotent retiring replay = %#v, %v", replay, err)
	}
	retired, err := service.RetirePage(ctx, retireRequest(pageA, retiring.Revision, "retired", jobID))
	if err != nil {
		t.Fatalf("retired: %v", err)
	}
	if retired.LifecycleState != "retired" || retired.RetirementJobID != jobID {
		t.Fatalf("retired page = %#v", retired)
	}
	replay2, err := service.RetirePage(ctx, retireRequest(pageA, retired.Revision+1, "retired", jobID))
	if err != nil || replay2.LifecycleState != "retired" || replay2.Revision != retired.Revision {
		t.Fatalf("idempotent retired replay = %#v, %v", replay2, err)
	}
}

func TestRetirePageRejectsConflicts(t *testing.T) {
	store := newMemoryStore()
	putSnapshot(t, store, snapshotA, 1)
	putActivePageAtRevision(t, store, pageA, 2)
	service := newServiceWithLevels(t, store, 2, platform.AccessLevelWrite, websiteConfig())
	ctx, err := service.Authenticate(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	jobID := "11111111-1111-4111-8111-111111111111"
	if _, err := service.RetirePage(ctx, retireRequest(pageA, 2, "retiring", jobID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetirePage(ctx, retireRequest(pageA, 3, "retiring", "22222222-2222-4222-8222-222222222222")); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("different job while retiring = %v, want conflict", err)
	}
	if _, err := service.RetirePage(ctx, retireRequest(pageA, 2, "retired", jobID)); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("stale revision = %v, want conflict", err)
	}
}
