package composition

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	platform "github.com/Zubrium/cdn/core/presentation"
	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	appconfig "github.com/Zubrium/cdn/main/internal/config"
	"github.com/Zubrium/cdn/pkg/contracts"
)

const contentTestSnapshotID = "11111111-1111-4111-8111-111111111111"

type contentTestContextKey struct{}

type fakeContentProjectionWebsite struct {
	snapshot    websiteapi.Snapshot
	snapshotErr error
	model       websiteapi.ContentModel
	page        websiteapi.PagePlacementProjection
	features    map[string]websiteapi.FeatureVersion
}

func (fake *fakeContentProjectionWebsite) GetSnapshot(ctx context.Context, id string) (websiteapi.Snapshot, error) {
	if err := contentTestTrustedContext(ctx); err != nil {
		return websiteapi.Snapshot{}, err
	}
	if fake.snapshotErr != nil {
		return websiteapi.Snapshot{}, fake.snapshotErr
	}
	if id != contentTestSnapshotID {
		return websiteapi.Snapshot{}, errors.New("unexpected snapshot")
	}
	return fake.snapshot, nil
}

func TestContentProjectionProviderNamesSnapshotLookupFailureStage(t *testing.T) {
	serviceCtx := context.WithValue(context.Background(), contentTestContextKey{}, "trusted")
	want := websiteapi.ErrNotFound
	fake := contentTestFixture()
	fake.snapshotErr = want
	provider, err := newContentProjectionProvider(fake, serviceCtx, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.GetContentProjections(context.Background(), contentTestSnapshotID)
	if !errors.Is(err, want) || err == nil || !strings.Contains(err.Error(), "load snapshot") {
		t.Fatalf("projection error = %v, want load snapshot context and original cause", err)
	}
}

func (fake *fakeContentProjectionWebsite) GetContentModel(ctx context.Context) (websiteapi.ContentModel, error) {
	if err := contentTestTrustedContext(ctx); err != nil {
		return websiteapi.ContentModel{}, err
	}
	return fake.model, nil
}

func (fake *fakeContentProjectionWebsite) GetPagePlacementProjection(ctx context.Context, pageID, snapshotID string) (websiteapi.PagePlacementProjection, error) {
	if err := contentTestTrustedContext(ctx); err != nil {
		return websiteapi.PagePlacementProjection{}, err
	}
	if pageID != fake.snapshot.PageID || snapshotID != contentTestSnapshotID {
		return websiteapi.PagePlacementProjection{}, errors.New("unexpected page scope")
	}
	return fake.page, nil
}

func (fake *fakeContentProjectionWebsite) GetFeatureVersion(ctx context.Context, key, version string) (websiteapi.FeatureVersion, error) {
	if err := contentTestTrustedContext(ctx); err != nil {
		return websiteapi.FeatureVersion{}, err
	}
	feature, ok := fake.features[key+"@"+version]
	if !ok {
		return websiteapi.FeatureVersion{}, errors.New("unknown feature version")
	}
	return feature, nil
}

func contentTestTrustedContext(ctx context.Context) error {
	if ctx == nil || ctx.Value(contentTestContextKey{}) != "trusted" {
		return errors.New("Website projection call used an untrusted context")
	}
	return nil
}

func contentTestFixture() *fakeContentProjectionWebsite {
	model := websiteapi.ContentModel{
		Version: websiteapi.ContentProjectionVersion, ModelRevision: 1,
		DataModel: json.RawMessage(`{"formatVersion":1,"entities":[]}`),
	}
	model.Digest, _ = model.ComputeDigest()
	page := websiteapi.PagePlacementProjection{
		Version: websiteapi.ContentProjectionVersion, PageID: "page-1", PageSnapshotID: contentTestSnapshotID,
		Revision: 1, State: "ready", Placements: []websiteapi.PagePlacement{{
			ID: "placement-1", PageID: "page-1", PageName: "Home", FeatureKey: "hero", FeatureName: "Hero", Version: "1.2.3",
			Bindings: []websiteapi.PlacementBinding{
				{InputKey: "title", EntityID: "entity-1", FieldID: "title-field"},
				{InputKey: "items", EntityID: "entity-2", Fields: []websiteapi.PlacementField{{InputPath: "title", FieldID: "items-title"}},
					Lookup:    &websiteapi.PlacementInputLookup{Argument: "slug", FieldID: "slug-field"},
					DetailSEO: &websiteapi.PlacementDetailSEO{TitleFieldID: "seo-title"}},
			},
		}},
	}
	page.Digest, _ = page.ComputeDigest()
	return &fakeContentProjectionWebsite{
		snapshot: websiteapi.Snapshot{ID: contentTestSnapshotID, PageID: "page-1", State: "ready",
			FeaturePins: map[string]string{"hero": "1.2.3"}},
		model: model,
		page:  page,
		features: map[string]websiteapi.FeatureVersion{
			"hero@1.2.3": {
				FeatureID: "hero", Version: "1.2.3", State: "ready", Manifest: contentTestManifest(),
				Files: map[string]string{"assets/app.js": "js", "assets/app.css": "css"},
			},
		},
	}
}

func contentTestManifest() []byte {
	return []byte(`{
		"formatVersion": 2,
		"key": "hero",
		"name": "Hero",
		"version": "1.2.3",
		"engineApi": "v1",
		"slots": ["main"],
		"arguments": {},
		"dataInputs": [
			{"key": "title", "kind": "scalar", "type": "text"},
			{"key": "items", "kind": "collection", "type": "text", "fields": [{"path": "title", "type": "text"}]}
		],
		"runtime": {"abi": "static-lifecycle", "entry": "assets/app.js", "styles": ["assets/app.css"]}
	}`)
}

func TestContentProjectionProviderConvertsTrustedWebsiteProjection(t *testing.T) {
	serviceCtx := context.WithValue(context.Background(), contentTestContextKey{}, "trusted")
	provider, err := newContentProjectionProvider(contentTestFixture(), serviceCtx, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	projections, err := provider.GetContentProjections(context.Background(), contentTestSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !projections.VerifyScope(contentTestSnapshotID) || projections.Locale != "en-US" {
		t.Fatalf("projection scope or locale is invalid: %+v", projections)
	}
	if len(projections.Page.Placements) != 1 || len(projections.Page.Placements[0].Inputs) != 2 {
		t.Fatalf("placements were not converted: %+v", projections.Page.Placements)
	}
	inputs := projections.Page.Placements[0].Inputs
	if inputs[0].Kind != "scalar" || len(inputs[0].Fields) != 1 || inputs[0].Fields[0].InputPath != "value" || inputs[0].Fields[0].FieldID != "title-field" {
		t.Fatalf("scalar input was not converted: %+v", inputs[0])
	}
	if inputs[1].Kind != "collection" || len(inputs[1].Fields) != 1 || inputs[1].Lookup == nil || inputs[1].Lookup.Argument != "slug" {
		t.Fatalf("collection input was not converted: %+v", inputs[1])
	}
	if _, err := provider.GetContentProjectionsForLocale(context.Background(), contentTestSnapshotID, "fr-FR"); err != nil {
		t.Fatalf("explicit locale projection failed: %v", err)
	}
}

func TestContentProjectionProviderFailsClosedOnInvalidProjections(t *testing.T) {
	serviceCtx := context.WithValue(context.Background(), contentTestContextKey{}, "trusted")
	cases := []struct {
		name   string
		mutate func(*fakeContentProjectionWebsite)
	}{
		{name: "unknown data input key", mutate: func(f *fakeContentProjectionWebsite) {
			f.page.Placements[0].Bindings[0].InputKey = "missing"
		}},
		{name: "feature pin mismatch", mutate: func(f *fakeContentProjectionWebsite) {
			f.snapshot.FeaturePins["hero"] = "9.9.9"
		}},
		{name: "invalid manifest", mutate: func(f *fakeContentProjectionWebsite) {
			feature := f.features["hero@1.2.3"]
			feature.Manifest = []byte(`{"formatVersion": 2}`)
			f.features["hero@1.2.3"] = feature
		}},
		{name: "snapshot not ready", mutate: func(f *fakeContentProjectionWebsite) {
			f.snapshot.State = "provisioning"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := contentTestFixture()
			tc.mutate(fake)
			provider, err := newContentProjectionProvider(fake, serviceCtx, "en-US")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.GetContentProjections(context.Background(), contentTestSnapshotID); err == nil {
				t.Fatal("invalid Website projection was accepted")
			}
		})
	}
}

type contentTestWebsiteAPI struct{ websiteapi.API }

func TestContentComponentFailsClosedWithoutCredentialOrRuntime(t *testing.T) {
	ctx := context.Background()
	var component *contentEngineComponent
	if err := component.Start(ctx); err == nil {
		t.Fatal("nil Content component started")
	}
	if component.Ready(ctx) == nil || component.IsReady() {
		t.Fatal("nil Content component reported ready")
	}
	if _, err := component.ReadPublishedContent(ctx, contentTestSnapshotID, "en-US"); err == nil {
		t.Fatal("nil Content component served published content")
	}
	assertSilentFailureStream(t, component.Errors())

	component = newContentEngineComponent(nil, appconfig.Resolved{}, nil)
	if err := component.Start(ctx); err == nil {
		t.Fatal("Content component started without Core runtime")
	}
	component = newContentEngineComponent(&coreComponent{}, appconfig.Resolved{}, &contentTestWebsiteAPI{})
	if err := component.Start(ctx); err == nil {
		t.Fatal("Content component started without Core runtime capability")
	}
	component = newContentEngineComponent(&coreComponent{runtime: &platform.Runtime{}}, appconfig.Resolved{}, &contentTestWebsiteAPI{})
	if err := component.Start(ctx); err == nil {
		t.Fatal("Content component started without a service credential")
	}
	if err := component.Stop(ctx); err != nil || component.IsReady() {
		t.Fatalf("stopping an uninitialized Content component: err=%v ready=%t", err, component.IsReady())
	}
	assertSilentFailureStream(t, component.Errors())
}

type publishedContentDispatcherStub struct {
	request contracts.PublishedContentReadRequest
	payload json.RawMessage
}

func (stub *publishedContentDispatcherStub) Dispatch(_ context.Context, envelope contracts.Envelope) (contracts.Envelope, error) {
	if envelope.Key() != contracts.PublishedContentReadOperation().Key {
		return contracts.Envelope{}, contracts.ErrInvalidOperation
	}
	request, err := contracts.DecodePayload[contracts.PublishedContentReadRequest](envelope.Payload)
	if err != nil {
		return contracts.Envelope{}, err
	}
	stub.request = request
	return contracts.Envelope{OperationID: envelope.OperationID, Version: envelope.Version, Payload: stub.payload}, nil
}

func TestPublishedContentReaderUsesSealedBusOperation(t *testing.T) {
	want := contentapi.PublishedContent{SnapshotID: contentTestSnapshotID, Locale: "en-US", ContentRevision: 7,
		Records: []contentapi.Record{}, Selections: []contentapi.Selection{}}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	responsePayload, err := json.Marshal(contracts.PublishedContentReadResponse{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &publishedContentDispatcherStub{payload: responsePayload}
	reader := busPublishedContentReader{dispatcher: dispatcher}
	got, err := reader.ReadPublishedContent(context.Background(), contentTestSnapshotID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotID != want.SnapshotID || got.Locale != want.Locale || got.ContentRevision != want.ContentRevision {
		t.Fatalf("published content = %+v", got)
	}
	if dispatcher.request.PageSnapshotID != contentTestSnapshotID || dispatcher.request.Locale != "en-US" {
		t.Fatalf("bus request = %+v", dispatcher.request)
	}
}
