package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDisposableCoordinatorFencesLeaseAndRoutePublication(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}

	systemID := "coordinator-integration-" + uuid.NewString()
	fingerprint := strings.Repeat("a", 64)
	owner := uuid.New()
	epoch, err := RegisterCoordinatorInstance(ctx, pool, systemID, owner, fingerprint, time.Minute)
	if err != nil || epoch != 1 {
		t.Fatalf("register first instance: epoch=%d err=%v", epoch, err)
	}
	if _, err := RegisterCoordinatorInstance(ctx, pool, systemID, uuid.New(), strings.Repeat("b", 64), time.Minute); !errors.Is(err, ErrCoordinatorConfigDiff) {
		t.Fatalf("different config joined an active system: %v", err)
	}

	firstFile, secondFile := insertReadyCoordinatorTestFile(t, ctx, pool), insertReadyCoordinatorTestFile(t, ctx, pool)
	routeID := uuid.New()
	if _, err := CreateCoordinatorRouteTarget(ctx, pool, systemID, routeID, firstFile); err != nil {
		t.Fatalf("create initial route target: %v", err)
	}

	firstFingerprint := strings.Repeat("c", 64)
	publicationPayload, _ := json.Marshal(map[string]string{"route_id": routeID.String(), "snapshot_id": uuid.NewString()})
	invalidPublication := CoordinatorPublicationRequest{
		SystemID: systemID, RouteID: routeID, Owner: owner, ConfigEpoch: epoch, Fingerprint: firstFingerprint,
		JobID: uuid.New(), JobType: "site-publication/v1", IdempotencyKey: "invalid-publication",
		Payload: json.RawMessage(`{"route_id":"` + uuid.NewString() + `"}`), MaxAttempts: 3,
	}
	if _, _, _, err := ScheduleCoordinatorPublication(ctx, pool, invalidPublication); !errors.Is(err, ErrCoordinatorInvalid) {
		t.Fatalf("publication payload was allowed to target a different route: %v", err)
	}
	firstGeneration, firstJob, created, err := ScheduleCoordinatorPublication(ctx, pool, CoordinatorPublicationRequest{
		SystemID: systemID, RouteID: routeID, Owner: owner, ConfigEpoch: epoch, Fingerprint: firstFingerprint,
		JobID: uuid.New(), JobType: "site-publication/v1", IdempotencyKey: "first-publication",
		Payload: publicationPayload, MaxAttempts: 3,
	})
	if err != nil || !created || firstGeneration != 1 {
		t.Fatalf("schedule publication: generation=%d created=%v err=%v", firstGeneration, created, err)
	}
	claimed, err := ClaimCoordinatorJobs(ctx, pool, systemID, owner, epoch, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != firstJob.ID || claimed[0].ErrorCode != "" {
		t.Fatalf("claim first publication: jobs=%+v err=%v", claimed, err)
	}
	oldToken := claimed[0].FencingToken
	if _, err := pool.Exec(ctx, `UPDATE core.coordinator_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE job_id=$1`, firstJob.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = ClaimCoordinatorJobs(ctx, pool, systemID, owner, epoch, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].FencingToken <= oldToken {
		t.Fatalf("reclaim expired publication: jobs=%+v err=%v", claimed, err)
	}
	current := claimed[0]
	if _, err := ActivateCoordinatorPublication(ctx, pool, systemID, routeID, current.ID, owner, epoch, oldToken, firstGeneration, secondFile); !errors.Is(err, ErrCoordinatorJobNotOwned) {
		t.Fatalf("expired worker activated a route with stale fencing token: %v", err)
	}
	activated, err := ActivateCoordinatorPublication(ctx, pool, systemID, routeID, current.ID, owner, epoch, current.FencingToken, firstGeneration, secondFile)
	if err != nil || activated.FileID != secondFile || activated.PublicationGeneration != firstGeneration {
		t.Fatalf("current worker failed to activate: target=%+v err=%v", activated, err)
	}
	if err := CompleteCoordinatorJob(ctx, pool, systemID, current.ID, owner, epoch, current.FencingToken, json.RawMessage(`{"status":"published"}`)); err != nil {
		t.Fatalf("complete current publication: %v", err)
	}
	finished, err := GetCoordinatorJob(ctx, pool, systemID, current.ID)
	if err != nil || finished.State != "succeeded" || finished.ErrorCode != "" {
		t.Fatalf("read completed job with nullable error code: job=%+v err=%v", finished, err)
	}

	secondGeneration, newerJob, _, err := ScheduleCoordinatorPublication(ctx, pool, CoordinatorPublicationRequest{
		SystemID: systemID, RouteID: routeID, Owner: owner, ConfigEpoch: epoch, Fingerprint: strings.Repeat("d", 64),
		JobID: uuid.New(), JobType: "site-publication/v1", IdempotencyKey: "second-publication",
		Payload: json.RawMessage(`{"route_id":"` + routeID.String() + `","snapshot_revision":2}`), MaxAttempts: 3,
	})
	if err != nil || secondGeneration != firstGeneration+1 {
		t.Fatalf("schedule new publication generation: generation=%d err=%v", secondGeneration, err)
	}
	claimed, err = ClaimCoordinatorJobs(ctx, pool, systemID, owner, epoch, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != newerJob.ID {
		t.Fatalf("claim newer publication: jobs=%+v err=%v", claimed, err)
	}
	if _, err := ActivateCoordinatorPublication(ctx, pool, systemID, routeID, claimed[0].ID, owner, epoch, claimed[0].FencingToken, firstGeneration, firstFile); !errors.Is(err, ErrCoordinatorStale) {
		t.Fatalf("superseded generation activated: %v", err)
	}
	active, err := GetCoordinatorRouteTarget(ctx, pool, systemID, routeID)
	if err != nil || active.FileID != secondFile || active.PublicationGeneration != firstGeneration {
		t.Fatalf("stale generation changed active route: target=%+v err=%v", active, err)
	}
}

func TestDisposableCoordinatorJobEnqueueIsIdempotentAndEpochFenced(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_ENQUEUE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	systemID := "coordinator-enqueue-" + uuid.NewString()
	owner := uuid.New()
	fingerprint := strings.Repeat("e", 64)
	epoch, err := RegisterCoordinatorInstance(ctx, pool, systemID, owner, fingerprint, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	request := CoordinatorJobRequest{ID: uuid.New(), SystemID: systemID, Owner: owner, ConfigEpoch: epoch,
		Type: "worker.inspect/v1", IdempotencyKey: "same-input", Payload: json.RawMessage(`{"file_id":"x"}`), MaxAttempts: 2}
	reserved := request
	reserved.Type = coordinatorPublicationJobType
	reserved.IdempotencyKey = "generic-publication"
	reserved.Payload = json.RawMessage(`{"route_id":"` + uuid.NewString() + `"}`)
	if _, _, err := EnqueueCoordinatorJob(ctx, pool, reserved); !errors.Is(err, ErrCoordinatorInvalid) {
		t.Fatalf("generic enqueue bypassed publication generation: %v", err)
	}
	first, created, err := EnqueueCoordinatorJob(ctx, pool, request)
	if err != nil || !created {
		t.Fatalf("enqueue: created=%v err=%v", created, err)
	}
	request.ID = uuid.New()
	second, created, err := EnqueueCoordinatorJob(ctx, pool, request)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("idempotent enqueue: created=%v job=%+v err=%v", created, second, err)
	}
	request.Payload = json.RawMessage(`{"file_id":"different"}`)
	if _, _, err := EnqueueCoordinatorJob(ctx, pool, request); !errors.Is(err, ErrCoordinatorConflict) {
		t.Fatalf("idempotency key accepted different content: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core.coordinator_jobs SET state='failed',attempts=max_attempts,error_code='task_failed' WHERE job_id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := RetryCoordinatorJob(ctx, pool, systemID, owner, first.ID, epoch); err != nil {
		t.Fatalf("operator retry of current failed job: %v", err)
	}
	retried, err := GetCoordinatorJob(ctx, pool, systemID, first.ID)
	if err != nil || retried.State != "queued" || retried.Attempts != 0 || retried.ErrorCode != "" {
		t.Fatalf("manual retry did not reset the failed job: job=%+v err=%v", retried, err)
	}
	if err := ReleaseCoordinatorInstance(ctx, pool, systemID, owner, epoch); err != nil {
		t.Fatal(err)
	}
	newEpoch, err := RegisterCoordinatorInstance(ctx, pool, systemID, uuid.New(), strings.Repeat("f", 64), time.Minute)
	if err != nil || newEpoch != epoch+1 {
		t.Fatalf("configuration epoch transition: got=%d want=%d err=%v", newEpoch, epoch+1, err)
	}
	request.ID = uuid.New()
	if _, _, err := EnqueueCoordinatorJob(ctx, pool, request); !errors.Is(err, ErrCoordinatorLeaseLost) {
		t.Fatalf("previous epoch enqueued job after restart: %v", err)
	}
}

func TestDisposableCoordinatorManualRouteChangeInvalidatesSameInputPublication(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_MANUAL_ROUTE_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}

	systemID := "coordinator-manual-route-" + uuid.NewString()
	owner, routeID := uuid.New(), uuid.New()
	epoch, err := RegisterCoordinatorInstance(ctx, pool, systemID, owner, strings.Repeat("1", 64), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	initialFile := insertReadyCoordinatorTestFile(t, ctx, pool)
	manualFile := insertReadyCoordinatorTestFile(t, ctx, pool)
	buildFile := insertReadyCoordinatorTestFile(t, ctx, pool)
	target, err := CreateCoordinatorRouteTarget(ctx, pool, systemID, routeID, initialFile)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("2", 64)
	payload, _ := json.Marshal(map[string]string{"route_id": routeID.String(), "snapshot_id": uuid.New().String()})
	request := CoordinatorPublicationRequest{SystemID: systemID, RouteID: routeID, Owner: owner, ConfigEpoch: epoch,
		Fingerprint: fingerprint, JobID: uuid.New(), JobType: "site-publication/v1", IdempotencyKey: "same-input",
		Payload: payload, MaxAttempts: 3}
	firstGeneration, firstJob, created, err := ScheduleCoordinatorPublication(ctx, pool, request)
	if err != nil || !created || firstGeneration != 1 {
		t.Fatalf("schedule initial publication: generation=%d created=%v err=%v", firstGeneration, created, err)
	}
	claimed, err := ClaimCoordinatorJobs(ctx, pool, systemID, owner, epoch, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != firstJob.ID {
		t.Fatalf("claim initial publication before manual change: jobs=%+v err=%v", claimed, err)
	}
	if _, err := ReplaceCoordinatorRouteTarget(ctx, pool, systemID, routeID, target.Revision, manualFile); err != nil {
		t.Fatalf("manually replace active route: %v", err)
	}
	var priorJobCancelled bool
	if err := pool.QueryRow(ctx, `SELECT state='cancelled' AND lease_owner IS NULL AND lease_until IS NULL
		FROM core.coordinator_jobs WHERE job_id=$1`, firstJob.ID).Scan(&priorJobCancelled); err != nil || !priorJobCancelled {
		t.Fatalf("manual route change did not cancel and revoke the prior publication lease: cancelled=%v err=%v", priorJobCancelled, err)
	}
	request.JobID = uuid.New()
	secondGeneration, secondJob, created, err := ScheduleCoordinatorPublication(ctx, pool, request)
	if err != nil || !created || secondGeneration != firstGeneration+2 || secondJob.ID == firstJob.ID {
		t.Fatalf("same-input publication after manual change did not get a fresh generation: generation=%d created=%v job=%s err=%v",
			secondGeneration, created, secondJob.ID, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core.coordinator_jobs SET state='failed',lease_owner=NULL,lease_until=NULL,error_code='task_failed' WHERE job_id=$1`, firstJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := RetryCoordinatorJob(ctx, pool, systemID, owner, firstJob.ID, epoch); !errors.Is(err, ErrCoordinatorStale) {
		t.Fatalf("operator retry requeued a superseded publication: %v", err)
	}
	superseded, err := GetCoordinatorJob(ctx, pool, systemID, firstJob.ID)
	if err != nil || superseded.State != "failed" || superseded.ErrorCode != "task_failed" {
		t.Fatalf("superseded job state changed after blocked retry: job=%+v err=%v", superseded, err)
	}
	if _, err := ActivateCoordinatorPublication(ctx, pool, systemID, routeID, firstJob.ID, owner, epoch,
		claimed[0].FencingToken, firstGeneration, buildFile); !errors.Is(err, ErrCoordinatorStale) {
		t.Fatalf("superseded publication generation was not rejected: %v", err)
	}
	claimed, err = ClaimCoordinatorJobs(ctx, pool, systemID, owner, epoch, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != secondJob.ID {
		t.Fatalf("claim current publication: jobs=%+v err=%v", claimed, err)
	}
	activated, err := ActivateCoordinatorPublication(ctx, pool, systemID, routeID, secondJob.ID, owner, epoch,
		claimed[0].FencingToken, secondGeneration, buildFile)
	if err != nil || activated.FileID != buildFile || activated.PublicationGeneration != secondGeneration {
		t.Fatalf("current same-input publication failed to activate: target=%+v err=%v", activated, err)
	}
}

func TestDisposableCoordinatorJobListingPaginatesSafeSummaries(t *testing.T) {
	pool := openDisposablePool(t, "TEST_COORDINATOR_LIST_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := migrateForTest(t, ctx, pool); err != nil {
		t.Fatal(err)
	}
	systemID, owner := "coordinator-list-"+uuid.NewString(), uuid.New()
	epoch, err := RegisterCoordinatorInstance(ctx, pool, systemID, owner, strings.Repeat("3", 64), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := make(map[uuid.UUID]bool)
	for index := 0; index < 3; index++ {
		job, created, err := EnqueueCoordinatorJob(ctx, pool, CoordinatorJobRequest{ID: uuid.New(), SystemID: systemID, Owner: owner, ConfigEpoch: epoch,
			Type: "worker.inspect/v1", IdempotencyKey: uuid.NewString(), Payload: json.RawMessage(`{"secret":"must-not-appear"}`), MaxAttempts: 2})
		if err != nil || !created {
			t.Fatalf("enqueue listing fixture %d: created=%v err=%v", index, created, err)
		}
		wantIDs[job.ID] = true
	}
	listRequest := CoordinatorJobListRequest{SystemID: systemID, Owner: owner, ConfigEpoch: epoch, Type: "worker.inspect/v1", State: "queued", Limit: 2}
	first, err := ListCoordinatorJobs(ctx, pool, listRequest)
	if err != nil || len(first.Items) != 2 || first.Next == nil {
		t.Fatalf("first jobs page: page=%+v err=%v", first, err)
	}
	listRequest.Before = first.Next
	second, err := ListCoordinatorJobs(ctx, pool, listRequest)
	if err != nil || len(second.Items) != 1 || second.Next != nil {
		t.Fatalf("second jobs page: page=%+v err=%v", second, err)
	}
	for _, item := range append(first.Items, second.Items...) {
		if !wantIDs[item.ID] || item.SystemID != systemID || item.Type != "worker.inspect/v1" || item.State != "queued" || item.MaxAttempts != 2 {
			t.Fatalf("job listing returned an unexpected summary: %+v", item)
		}
		delete(wantIDs, item.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("job cursor skipped records: %v", wantIDs)
	}
	if _, err := ListCoordinatorJobs(ctx, pool, CoordinatorJobListRequest{SystemID: systemID, Owner: owner, ConfigEpoch: epoch, Limit: 0}); !errors.Is(err, ErrCoordinatorInvalid) {
		t.Fatalf("invalid job page size was accepted: %v", err)
	}
	if _, err := ListCoordinatorJobs(ctx, pool, CoordinatorJobListRequest{SystemID: systemID, Owner: owner, ConfigEpoch: epoch + 1, Limit: 2}); !errors.Is(err, ErrCoordinatorLeaseLost) {
		t.Fatalf("stale Coordinator epoch could list jobs: %v", err)
	}
	if err := ReleaseCoordinatorInstance(ctx, pool, systemID, owner, epoch); err != nil {
		t.Fatalf("release old listing instance: %v", err)
	}
	newOwner := uuid.New()
	newEpoch, err := RegisterCoordinatorInstance(ctx, pool, systemID, newOwner, strings.Repeat("4", 64), time.Minute)
	if err != nil || newEpoch != epoch+1 {
		t.Fatalf("register new listing epoch: epoch=%d err=%v", newEpoch, err)
	}
	if _, err := ListCoordinatorJobs(ctx, pool, listRequest); !errors.Is(err, ErrCoordinatorLeaseLost) {
		t.Fatalf("released Coordinator capability could still list jobs: %v", err)
	}
}

func insertReadyCoordinatorTestFile(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fileID, objectKey := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO core.core_objects(id,object_key,metadata,content_type,size_bytes,sha256,state)
		VALUES($1,$2,'{}'::jsonb,'text/html',0,decode(repeat('00',32),'hex'),'ready')`, fileID, objectKey)
	if err != nil {
		t.Fatal(err)
	}
	return fileID
}
