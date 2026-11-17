package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	coreplatform "github.com/Zubrium/cdn/core/platform"
	"github.com/google/uuid"
)

type fakePublicationStore struct {
	request       coreplatform.CoordinatorPublicationRequest
	scheduleCalls int
	activateCalls int
	activateErr   error
	activatedFile uuid.UUID
	activatedGen  int64
}

func (s *fakePublicationStore) SchedulePublication(_ context.Context, request coreplatform.CoordinatorPublicationRequest) (int64, coreplatform.CoordinatorJob, bool, error) {
	s.scheduleCalls++
	s.request = request
	return 3, coreplatform.CoordinatorJob{ID: request.JobID, Type: request.JobType, Payload: request.Payload}, true, nil
}

func (s *fakePublicationStore) ActivatePublication(_ context.Context, _ string, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ int64, _ int64, generation int64, fileID uuid.UUID) (coreplatform.CoordinatorRouteTarget, error) {
	s.activateCalls++
	s.activatedGen, s.activatedFile = generation, fileID
	return coreplatform.CoordinatorRouteTarget{FileID: fileID, PublicationGeneration: generation}, s.activateErr
}

func TestPublicationSchedulerUsesConfiguredBindingsAndEnforcesAutomaticPolicy(t *testing.T) {
	store := &fakePublicationStore{}
	first := PublicationBinding{SiteID: uuid.New(), ContentID: uuid.New(), RouteID: uuid.New(), AutoPublish: true}
	second := PublicationBinding{SiteID: uuid.New(), ContentID: uuid.New(), RouteID: uuid.New(), AutoPublish: false}
	configured := []PublicationBinding{first, second}
	scheduler, err := NewPublicationScheduler(PublicationSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 2, MaxAttempts: 4, Bindings: configured,
	})
	if err != nil {
		t.Fatal(err)
	}
	configured[0].SiteID = uuid.New()
	input := PublicationInput{SnapshotID: uuid.New(), SnapshotRevision: 7, ContentRevision: 11, ProviderVersion: "provider@2"}
	if _, _, _, err := scheduler.ScheduleAutomatic(context.Background(), second.RouteID, input); !errors.Is(err, ErrAutomaticPublicationDisabled) || store.scheduleCalls != 0 {
		t.Fatalf("disabled route scheduled automatic publication: calls=%d err=%v", store.scheduleCalls, err)
	}
	if _, _, _, err := scheduler.ScheduleAutomatic(context.Background(), uuid.New(), input); !errors.Is(err, ErrPublicationBindingMissing) || store.scheduleCalls != 0 {
		t.Fatalf("unconfigured route scheduled publication: calls=%d err=%v", store.scheduleCalls, err)
	}
	_, firstJob, created, err := scheduler.ScheduleAutomatic(context.Background(), first.RouteID, input)
	if err != nil || !created || firstJob.Type != SitePublicationJobType {
		t.Fatalf("configured route publication was not scheduled: job=%+v created=%v err=%v", firstJob, created, err)
	}
	var payload publicationJobPayload
	if err := json.Unmarshal(store.request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SiteID != first.SiteID.String() || payload.ContentID != first.ContentID.String() || payload.RouteID != first.RouteID.String() {
		t.Fatalf("scheduler did not use its immutable configured binding: %+v", payload)
	}
	firstFingerprint, firstKey := store.request.Fingerprint, store.request.IdempotencyKey
	if _, _, _, err := scheduler.ScheduleRebuild(context.Background(), second.RouteID, input); err != nil {
		t.Fatal(err)
	}
	if store.request.Fingerprint == firstFingerprint || store.request.IdempotencyKey == firstKey {
		t.Fatal("explicit rebuild reused the unrelated route's publication identity")
	}
}

func TestPublicationSchedulerRejectsInvalidOrDuplicateBindings(t *testing.T) {
	valid := PublicationBinding{SiteID: uuid.New(), ContentID: uuid.New(), RouteID: uuid.New(), AutoPublish: true}
	for name, bindings := range map[string][]PublicationBinding{
		"missing site":    {{ContentID: uuid.New(), RouteID: uuid.New()}},
		"duplicate route": {valid, {SiteID: uuid.New(), ContentID: uuid.New(), RouteID: valid.RouteID}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPublicationScheduler(PublicationSchedulerConfig{
				Store: &fakePublicationStore{}, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1,
				MaxAttempts: 3, Bindings: bindings,
			}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid bindings accepted: %v", err)
			}
		})
	}
}

func TestPublicationSchedulerRejectsInvalidUTF8ProviderVersion(t *testing.T) {
	binding := PublicationBinding{SiteID: uuid.New(), ContentID: uuid.New(), RouteID: uuid.New(), AutoPublish: true}
	store := &fakePublicationStore{}
	scheduler, err := NewPublicationScheduler(PublicationSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 1, MaxAttempts: 3, Bindings: []PublicationBinding{binding},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PublicationInput{SnapshotID: uuid.New(), SnapshotRevision: 1, ContentRevision: 1, ProviderVersion: string([]byte{0xff})}
	if _, _, _, err := scheduler.ScheduleAutomatic(context.Background(), binding.RouteID, input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed provider version was accepted: %v", err)
	}
	if store.scheduleCalls != 0 {
		t.Fatalf("malformed provider version reached Core: calls=%d", store.scheduleCalls)
	}
}

func TestPublicationSchedulerEpochIsPartOfPublicationIdentity(t *testing.T) {
	store := &fakePublicationStore{}
	binding := PublicationBinding{SiteID: uuid.New(), ContentID: uuid.New(), RouteID: uuid.New(), AutoPublish: true}
	input := PublicationInput{SnapshotID: uuid.New(), SnapshotRevision: 7, ContentRevision: 11, ProviderVersion: "provider@2"}
	firstScheduler, err := NewPublicationScheduler(PublicationSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 2, MaxAttempts: 4, Bindings: []PublicationBinding{binding},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := firstScheduler.ScheduleAutomatic(context.Background(), binding.RouteID, input); err != nil {
		t.Fatal(err)
	}
	firstFingerprint, firstKey := store.request.Fingerprint, store.request.IdempotencyKey
	secondScheduler, err := NewPublicationScheduler(PublicationSchedulerConfig{
		Store: store, SystemID: "test-system", Owner: uuid.New(), ConfigEpoch: 3, MaxAttempts: 4, Bindings: []PublicationBinding{binding},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := secondScheduler.ScheduleAutomatic(context.Background(), binding.RouteID, input); err != nil {
		t.Fatal(err)
	}
	if store.request.Fingerprint == firstFingerprint || store.request.IdempotencyKey == firstKey {
		t.Fatal("new deployment epoch reused the previous publication identity")
	}
}

type fakePublicationBuilder struct {
	baseCalls, renderCalls int
	baseKey, renderKey     string
	base, artifact         uuid.UUID
	baseErr, renderErr     error
}

func (b *fakePublicationBuilder) BuildSiteBase(_ context.Context, key string, _ uuid.UUID, _ uuid.UUID, _ int64, _ string) (uuid.UUID, error) {
	b.baseCalls++
	b.baseKey = key
	return b.base, b.baseErr
}

func (b *fakePublicationBuilder) RenderPublishedContent(_ context.Context, key string, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ int64) (uuid.UUID, error) {
	b.renderCalls++
	b.renderKey = key
	return b.artifact, b.renderErr
}

func publicationJob(t *testing.T, jobID, owner uuid.UUID, route, site, content, snapshot uuid.UUID) CoreJob {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"route_id": route.String(), "site_id": site.String(), "content_id": content.String(), "snapshot_id": snapshot.String(),
		"snapshot_revision": 7, "content_revision": 11, "provider_version": "provider@2", "generation": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	return CoreJob{ID: jobID, SystemID: "test-system", Type: SitePublicationJobType, Payload: payload, LeaseOwner: owner, ConfigEpoch: 2, FencingToken: 9}
}

func newTestPublicationHandler(t *testing.T, store PublicationStore, builder PublicationBuilder, systemID string, binding PublicationBinding) *PublicationHandler {
	t.Helper()
	handler, err := NewPublicationHandler(PublicationHandlerConfig{
		Store: store, Builder: builder, SystemID: systemID, Bindings: []PublicationBinding{binding},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestPublicationHandlerBuildsThenAtomicallyActivates(t *testing.T) {
	store := &fakePublicationStore{}
	owner, route, site, content, snapshot := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	builder := &fakePublicationBuilder{base: uuid.New(), artifact: uuid.New()}
	handler := newTestPublicationHandler(t, store, builder, "test-system", PublicationBinding{SiteID: site, ContentID: content, RouteID: route})
	job := publicationJob(t, uuid.New(), owner, route, site, content, snapshot)
	result, err := handler.Handle(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if builder.baseCalls != 1 || builder.renderCalls != 1 || builder.baseKey != job.ID.String()+":site-base" || builder.renderKey != job.ID.String()+":content-artifact" {
		t.Fatalf("publication stages did not use stable job idempotency keys: %+v", builder)
	}
	if store.activateCalls != 1 || store.activatedGen != 3 || store.activatedFile != builder.artifact || !strings.Contains(string(result), `"status":"published"`) {
		t.Fatalf("artifact was not activated through the fenced Core operation: result=%s store=%+v", result, store)
	}
}

func TestPublicationHandlerTreatsSupersededGenerationAsTerminal(t *testing.T) {
	store := &fakePublicationStore{activateErr: coreplatform.ErrCoordinatorStale}
	owner, route, site, content, snapshot := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	builder := &fakePublicationBuilder{base: uuid.New(), artifact: uuid.New()}
	handler := newTestPublicationHandler(t, store, builder, "test-system", PublicationBinding{SiteID: site, ContentID: content, RouteID: route})
	result, err := handler.Handle(context.Background(), publicationJob(t, uuid.New(), owner, route, site, content, snapshot))
	if err != nil || !strings.Contains(string(result), `"status":"stale"`) {
		t.Fatalf("stale build should be recorded without retry or target change: result=%s err=%v", result, err)
	}
}

func TestPublicationHandlerDoesNotPublishAfterBuildFailure(t *testing.T) {
	store := &fakePublicationStore{}
	builder := &fakePublicationBuilder{baseErr: errors.New("secret database address")}
	route, site, content := uuid.New(), uuid.New(), uuid.New()
	handler := newTestPublicationHandler(t, store, builder, "test-system", PublicationBinding{SiteID: site, ContentID: content, RouteID: route})
	_, err := handler.Handle(context.Background(), publicationJob(t, uuid.New(), uuid.New(), route, site, content, uuid.New()))
	var failure *TaskFailure
	if !errors.As(err, &failure) || failure.Code != "publication_build_failed" || store.activateCalls != 0 {
		t.Fatalf("build failure crossed the activation boundary: err=%v store=%+v", err, store)
	}
}

func TestPublicationHandlerDoesNotBuildAfterCancellation(t *testing.T) {
	store := &fakePublicationStore{}
	builder := &fakePublicationBuilder{base: uuid.New(), artifact: uuid.New()}
	route, site, content := uuid.New(), uuid.New(), uuid.New()
	handler := newTestPublicationHandler(t, store, builder, "test-system", PublicationBinding{SiteID: site, ContentID: content, RouteID: route})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := handler.Handle(ctx, publicationJob(t, uuid.New(), uuid.New(), route, site, content, uuid.New()))
	if !errors.Is(err, context.Canceled) || builder.baseCalls != 0 || builder.renderCalls != 0 || store.activateCalls != 0 {
		t.Fatalf("cancelled publication started work: err=%v builder=%+v store=%+v", err, builder, store)
	}
}

func TestPublicationHandlerRejectsInvalidSystemBeforeBuilding(t *testing.T) {
	store := &fakePublicationStore{}
	builder := &fakePublicationBuilder{base: uuid.New(), artifact: uuid.New()}
	_, err := NewPublicationHandler(PublicationHandlerConfig{
		Store: store, Builder: builder, SystemID: "invalid/system",
	})
	if !errors.Is(err, ErrInvalid) || builder.baseCalls != 0 || store.activateCalls != 0 {
		t.Fatalf("invalid system id was accepted: err=%v builder=%+v store=%+v", err, builder, store)
	}
}

func TestPublicationHandlerRejectsForeignOrUnfencedJobBeforeBuilding(t *testing.T) {
	for name, mutate := range map[string]func(*CoreJob){
		"foreign system":        func(job *CoreJob) { job.SystemID = "other-system" },
		"missing fencing token": func(job *CoreJob) { job.FencingToken = 0 },
		"invalid UTF-8 payload": func(job *CoreJob) {
			job.Payload = bytes.Replace(job.Payload, []byte("provider@2"), []byte{0xff}, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakePublicationStore{}
			builder := &fakePublicationBuilder{base: uuid.New(), artifact: uuid.New()}
			route, site, content := uuid.New(), uuid.New(), uuid.New()
			handler := newTestPublicationHandler(t, store, builder, "test-system", PublicationBinding{SiteID: site, ContentID: content, RouteID: route})
			job := publicationJob(t, uuid.New(), uuid.New(), route, site, content, uuid.New())
			mutate(&job)
			_, err := handler.Handle(context.Background(), job)
			var failure *TaskFailure
			if !errors.As(err, &failure) || failure.Code != "invalid_publication_job" || builder.baseCalls != 0 || store.activateCalls != 0 {
				t.Fatalf("invalid job reached publication side effects: err=%v builder=%+v store=%+v", err, builder, store)
			}
		})
	}
}

func TestPublicationHandlerRejectsPayloadFromAnotherConfiguredBindingBeforeBuilding(t *testing.T) {
	store := &fakePublicationStore{}
	builder := &fakePublicationBuilder{base: uuid.New(), artifact: uuid.New()}
	route, site, content := uuid.New(), uuid.New(), uuid.New()
	handler := newTestPublicationHandler(t, store, builder, "test-system", PublicationBinding{
		SiteID: uuid.New(), ContentID: content, RouteID: route,
	})
	job := publicationJob(t, uuid.New(), uuid.New(), route, site, content, uuid.New())
	_, err := handler.Handle(context.Background(), job)
	var failure *TaskFailure
	if !errors.As(err, &failure) || failure.Code != "invalid_publication_job" || builder.baseCalls != 0 || builder.renderCalls != 0 || store.activateCalls != 0 {
		t.Fatalf("foreign publication payload reached a side effect: err=%v builder=%+v store=%+v", err, builder, store)
	}
}
