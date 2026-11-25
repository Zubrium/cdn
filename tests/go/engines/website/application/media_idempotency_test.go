package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	contracts "github.com/Zubrium/cdn/pkg/contracts"
)

type receiptStore struct {
	RecordStore
	records     map[contracts.RecordKey]contracts.Record
	beforeBatch func()
}

func (s *receiptStore) Get(_ context.Context, key contracts.RecordKey) (contracts.Record, error) {
	if v, ok := s.records[key]; ok {
		return v, nil
	}
	return contracts.Record{}, contracts.ErrRecordNotFound
}
func (s *receiptStore) Batch(_ context.Context, changes []contracts.RecordMutation) error {
	if s.beforeBatch != nil {
		hook := s.beforeBatch
		s.beforeBatch = nil
		hook()
	}
	for _, change := range changes {
		if _, ok := s.records[change.Key]; ok {
			return contracts.ErrRecordConflict
		}
	}
	for _, change := range changes {
		v := change.Value
		s.records[change.Key] = contracts.Record{Payload: v.Payload, PayloadVersion: v.PayloadVersion, IndexData: v.IndexData, Revision: 1}
	}
	return nil
}
func testReceipt(t *testing.T, store *receiptStore, key contracts.RecordKey, jobID, fp, profile string, caller bool) {
	t.Helper()
	record := mediaJobRecord{ID: jobID, SourceFileID: "source", Fingerprint: fp, ProfileDigest: profile, ProcessorVersion: "v1", State: "queued", Outputs: []string{"original"}}
	payload, index, err := mediaModuleDataValue(record, 1)
	if err != nil {
		t.Fatal(err)
	}
	store.records[websiterepo.MediaJobKey(jobID)] = contracts.Record{Payload: payload, IndexData: index, PayloadVersion: 1, Revision: 1}
	storedFP := fp
	if caller {
		storedFP = ""
	}
	value, err := mediaReceiptValue(storedFP, jobID, profile, "v1")
	if err != nil {
		t.Fatal(err)
	}
	store.records[key] = contracts.Record{Payload: value.Payload, IndexData: value.IndexData, PayloadVersion: 1, Revision: 1}
}
func TestMediaIdempotencyBindsCallerBeforeReusingFingerprint(t *testing.T) {
	ctx := context.Background()
	profile := strings.Repeat("a", 64)
	first := strings.Repeat("b", 64)
	second := strings.Repeat("c", 64)
	caller := websiterepo.MediaIdempotencyKey("caller/key-one")
	fingerprint := websiterepo.MediaIdempotencyKey("fingerprint/" + second)
	store := &receiptStore{records: map[contracts.RecordKey]contracts.Record{}}
	testReceipt(t, store, caller, "11111111-1111-4111-8111-111111111111", first, profile, true)
	testReceipt(t, store, fingerprint, "22222222-2222-4222-8222-222222222222", second, profile, false)
	service := &Service{store: store}
	if _, _, err := service.lookupMediaRequest(ctx, caller, fingerprint, second, profile, "v1"); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("reused caller key must conflict even if another job matches: %v", err)
	}
}
func TestMediaIdempotencyBindsNewCallerToExistingJob(t *testing.T) {
	ctx := context.Background()
	profile := strings.Repeat("a", 64)
	fp := strings.Repeat("b", 64)
	caller := websiterepo.MediaIdempotencyKey("caller/new-key")
	fingerprint := websiterepo.MediaIdempotencyKey("fingerprint/" + fp)
	store := &receiptStore{records: map[contracts.RecordKey]contracts.Record{}}
	testReceipt(t, store, fingerprint, "11111111-1111-4111-8111-111111111111", fp, profile, false)
	service := &Service{store: store}
	id, found, err := service.lookupMediaRequest(ctx, caller, fingerprint, fp, profile, "v1")
	if err != nil || !found || id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("reuse = %q %v %v", id, found, err)
	}
	if _, ok := store.records[caller]; !ok {
		t.Fatal("new caller key was not bound to reused job")
	}
	other := strings.Repeat("c", 64)
	otherKey := websiterepo.MediaIdempotencyKey("fingerprint/" + other)
	testReceipt(t, store, otherKey, "22222222-2222-4222-8222-222222222222", other, profile, false)
	if _, _, err := service.lookupMediaRequest(ctx, caller, otherKey, other, profile, "v1"); !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("rebound caller key accepted different media job: %v", err)
	}
}

func TestMediaCallerKeyConflictsWhenProcessorVersionChanges(t *testing.T) {
	ctx := context.Background()
	profile := strings.Repeat("a", 64)
	fingerprint := strings.Repeat("b", 64)
	caller := websiterepo.MediaIdempotencyKey("caller/key-one")
	store := &receiptStore{records: map[contracts.RecordKey]contracts.Record{}}
	testReceipt(t, store, caller, "11111111-1111-4111-8111-111111111111", fingerprint, profile, true)
	service := &Service{store: store}
	_, _, err := service.lookupMediaRequest(ctx, caller, websiterepo.MediaIdempotencyKey("fingerprint/"+fingerprint), fingerprint, profile, "v2")
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("changed processor must conflict, got %v", err)
	}
}

func TestMediaIdempotencyRejectsConcurrentCallerKeyCollision(t *testing.T) {
	ctx := context.Background()
	profile := strings.Repeat("a", 64)
	first := strings.Repeat("b", 64)
	second := strings.Repeat("c", 64)
	caller := websiterepo.MediaIdempotencyKey("caller/concurrent")
	fingerprint := websiterepo.MediaIdempotencyKey("fingerprint/" + second)
	store := &receiptStore{records: map[contracts.RecordKey]contracts.Record{}}
	testReceipt(t, store, fingerprint, "22222222-2222-4222-8222-222222222222", second, profile, false)
	store.beforeBatch = func() { testReceipt(t, store, caller, "11111111-1111-4111-8111-111111111111", first, profile, true) }
	_, _, err := (&Service{store: store}).lookupMediaRequest(ctx, caller, fingerprint, second, profile, "v1")
	if !errors.Is(err, websiteapi.ErrConflict) {
		t.Fatalf("concurrent caller collision must conflict, got %v", err)
	}
}
