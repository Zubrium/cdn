package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
)

// Core assigns revisions from a database-wide sequence. A build record's next
// revision therefore cannot be derived from its previous revision; every CAS
// must use the revision Core actually returned.
type globalRevisionBuildStore struct {
	records      map[recorddata.RecordKey]recorddata.Record
	nextRevision int64
}

func (s *globalRevisionBuildStore) Get(_ context.Context, key recorddata.RecordKey) (recorddata.Record, error) {
	record, ok := s.records[key]
	if !ok {
		return recorddata.Record{}, recorddata.ErrRecordNotFound
	}
	return record, nil
}

func (s *globalRevisionBuildStore) List(context.Context, string, string, string, string, []recorddata.RecordSelector, int) ([]recorddata.RecordKey, []recorddata.Record, string, error) {
	return nil, nil, "", nil
}

func (s *globalRevisionBuildStore) Batch(_ context.Context, mutations []recorddata.RecordMutation) error {
	for _, mutation := range mutations {
		current := s.records[mutation.Key]
		if current.Revision != mutation.ExpectedRevision {
			return recorddata.ErrRecordConflict
		}
	}
	for _, mutation := range mutations {
		s.nextRevision++
		s.records[mutation.Key] = recorddata.Record{
			Payload:        append(json.RawMessage(nil), mutation.Value.Payload...),
			PayloadVersion: mutation.Value.PayloadVersion,
			IndexData:      append(json.RawMessage(nil), mutation.Value.IndexData...),
			Revision:       s.nextRevision,
		}
	}
	return nil
}

func (s *globalRevisionBuildStore) MaxPageSize() int     { return 100 }
func (s *globalRevisionBuildStore) MaxBatchSize() int    { return 100 }
func (s *globalRevisionBuildStore) MaxPayloadBytes() int { return 1 << 20 }

func TestAdvanceBuildUsesCoreIssuedRevisionBetweenTransitions(t *testing.T) {
	buildID := "22222222-2222-4222-8222-222222222222"
	key := websiterepo.BuildKey(buildID)
	store := &globalRevisionBuildStore{records: map[recorddata.RecordKey]recorddata.Record{}, nextRevision: 20}
	initial := buildRecord{ID: buildID, SnapshotID: buildID, State: buildStateQueued, Locale: "en-US", InputDigest: strings.Repeat("a", 64)}
	payload, index, err := buildModuleDataValue(initial, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Other module records have already consumed revisions 8..20 since this
	// build record was written; Core will issue 21 on the next CAS.
	store.records[key] = recorddata.Record{Payload: payload, PayloadVersion: 1, IndexData: index, Revision: 7}

	service := &Service{store: store}
	current := buildState{record: initial, key: key, revision: 7}
	current, err = service.advanceBuild(context.Background(), current, buildStateResolving, nil)
	if err != nil {
		t.Fatalf("first transition: %v", err)
	}
	current, err = service.advanceBuild(context.Background(), current, buildStatePrerender, nil)
	if err != nil {
		t.Fatalf("second transition after a global-revision CAS: %v", err)
	}
	if current.record.State != buildStatePrerender {
		t.Fatalf("state = %q, want %q", current.record.State, buildStatePrerender)
	}
}

type fixedContentBuildProjection struct{ revision int64 }

func (f fixedContentBuildProjection) ResolveContentBuildProjection(context.Context, string, string) (ContentBuildProjection, error) {
	return ContentBuildProjection{PublicationRevision: f.revision, SelectionDigest: strings.Repeat("c", 64)}, nil
}

func TestModuleBuildInputResolverRejectsStalePinnedContentRevision(t *testing.T) {
	store := &globalRevisionBuildStore{records: map[recorddata.RecordKey]recorddata.Record{}, nextRevision: 20}
	dataModel := json.RawMessage(`{"formatVersion":1,"entities":[]}`)
	model := websiteapi.ContentModel{Version: websiteapi.ContentProjectionVersion, ModelRevision: 1, DataModel: dataModel}
	digest, err := contentModelDigest(model)
	if err != nil {
		t.Fatal(err)
	}
	model.Digest = digest
	payload, err := json.Marshal(contentModelRecord{DataModel: dataModel, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(map[string]any{"record_type": "content_model", "revision": 1, "digest": digest})
	if err != nil {
		t.Fatal(err)
	}
	store.records[contentModelKey()] = recorddata.Record{Payload: payload, PayloadVersion: 1, IndexData: index, Revision: 21}

	featurePayload, err := json.Marshal(map[string]any{
		"record_type": "feature_version", "manifest": json.RawMessage(`{}`),
		"files":  map[string]string{"index.html": strings.Repeat("a", 64)},
		"digest": strings.Repeat("b", 64), "state": "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.records[websiterepo.FeatureVersionKey("feature", "1.0.0")] = recorddata.Record{Payload: featurePayload, PayloadVersion: 1, Revision: 22}

	resolver, err := NewModuleBuildInputResolver(store, fixedContentBuildProjection{revision: 1}, "sha256:"+strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := websiteapi.Snapshot{
		ID: "33333333-3333-4333-8333-333333333333", PageID: "11111111-1111-4111-8111-111111111111",
		Revision: 1, Digest: strings.Repeat("e", 64), State: "ready",
		FeaturePins: map[string]string{"feature": "1.0.0"},
		Payload:     &websiteapi.SnapshotPayload{Config: json.RawMessage(`{"formatVersion":1}`), Metadata: json.RawMessage(`{}`)},
	}
	if _, err := resolver.ResolveBuildInputPinned(context.Background(), snapshot, "en-US", 1, 2); err == nil {
		t.Fatal("build input accepted a Content projection from revision 1 while revision 2 was pinned")
	}
	if _, err := resolver.ResolveBuildInputPinned(context.Background(), snapshot, "en-US", 1, 1); err != nil {
		t.Fatalf("matching pinned revision was rejected: %v", err)
	}
}
