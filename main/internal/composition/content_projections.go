package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/google/uuid"
)

// websiteProjectionAPI is the narrow read-only Website capability used by the
// trusted Content projection provider. All calls use the authenticated service
// context captured at composition startup, never a request context.
type websiteProjectionAPI interface {
	GetSnapshot(context.Context, string) (websiteapi.Snapshot, error)
	GetContentModel(context.Context) (websiteapi.ContentModel, error)
	GetPagePlacementProjection(context.Context, string, string) (websiteapi.PagePlacementProjection, error)
	GetFeatureVersion(context.Context, string, string) (websiteapi.FeatureVersion, error)
}

type contentProjectionProvider struct {
	website websiteProjectionAPI
	ctx     context.Context
	locale  string
}

func newContentProjectionProvider(website websiteProjectionAPI, ctx context.Context, locale string) (*contentProjectionProvider, error) {
	if website == nil || ctx == nil || locale == "" {
		return nil, errors.New("Content projection dependencies are not initialized")
	}
	return &contentProjectionProvider{website: website, ctx: ctx, locale: locale}, nil
}

func (provider *contentProjectionProvider) GetContentProjections(ctx context.Context, pageSnapshotID string) (contentapi.Projections, error) {
	return provider.GetContentProjectionsForLocale(ctx, pageSnapshotID, provider.locale)
}

func (provider *contentProjectionProvider) GetContentProjectionsForLocale(ctx context.Context, pageSnapshotID, locale string) (contentapi.Projections, error) {
	return provider.getContentProjectionsForLocaleAllowState(ctx, pageSnapshotID, locale, false)
}

func (provider *contentProjectionProvider) getContentProjectionsForLocaleAllowState(ctx context.Context, pageSnapshotID, locale string, allowProvisioning bool) (contentapi.Projections, error) {
	if provider == nil || provider.website == nil || provider.ctx == nil || ctx == nil || locale == "" {
		return contentapi.Projections{}, contentapi.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return contentapi.Projections{}, err
	}
	if err := provider.ctx.Err(); err != nil {
		return contentapi.Projections{}, err
	}
	parsed, err := uuid.Parse(pageSnapshotID)
	if err != nil || parsed.String() != pageSnapshotID {
		return contentapi.Projections{}, fmt.Errorf("%w: non-canonical page snapshot ID", contentapi.ErrInvalid)
	}
	snapshot, err := provider.website.GetSnapshot(provider.ctx, pageSnapshotID)
	if err != nil {
		return contentapi.Projections{}, err
	}
	if snapshot.PageID == "" || (snapshot.State != "ready" && !(allowProvisioning && snapshot.State == "provisioning")) {
		return contentapi.Projections{}, fmt.Errorf("%w: page snapshot is not ready", contentapi.ErrNotFound)
	}
	if err := ctx.Err(); err != nil {
		return contentapi.Projections{}, err
	}
	model, err := provider.website.GetContentModel(provider.ctx)
	if err != nil {
		return contentapi.Projections{}, err
	}
	contentModel := contentapi.ContentModelProjection{
		Version: model.Version, ModelRevision: model.ModelRevision, Digest: model.Digest, DataModel: model.DataModel,
	}
	if !contentModel.VerifyDigest() {
		return contentapi.Projections{}, fmt.Errorf("%w: invalid content model projection", contentapi.ErrUnavailable)
	}
	page, err := provider.website.GetPagePlacementProjection(provider.ctx, snapshot.PageID, pageSnapshotID)
	if err != nil {
		return contentapi.Projections{}, err
	}
	if page.PageSnapshotID != pageSnapshotID || page.PageID != snapshot.PageID || (page.State != "ready" && !(allowProvisioning && page.State == "provisioning")) {
		return contentapi.Projections{}, fmt.Errorf("%w: page placement projection scope or state", contentapi.ErrNotFound)
	}
	placements, err := provider.buildPlacements(ctx, page.Placements, snapshot.FeaturePins)
	if err != nil {
		return contentapi.Projections{}, err
	}
	pageProjection := contentapi.PagePlacementProjection{
		Version: page.Version, PageID: page.PageID, PageSnapshotID: page.PageSnapshotID,
		Revision: page.Revision, Digest: page.Digest, State: page.State, Placements: placements,
	}
	digest, err := pageProjection.ComputeDigest()
	if err != nil {
		return contentapi.Projections{}, err
	}
	pageProjection.Digest = digest
	projections := contentapi.Projections{ContentModel: contentModel, Page: pageProjection, Locale: locale}
	if !projections.VerifyScope(pageSnapshotID) {
		return contentapi.Projections{}, fmt.Errorf("%w: projected scope verification failed", contentapi.ErrUnavailable)
	}
	return projections, nil
}

func (provider *contentProjectionProvider) buildPlacements(ctx context.Context, source []websiteapi.PagePlacement, pins map[string]string) ([]contentapi.Placement, error) {
	placements := make([]contentapi.Placement, 0, len(source))
	for _, placement := range source {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pin, pinned := pins[placement.FeatureKey]
		if !pinned || pin == "" || placement.Version != pin {
			return nil, fmt.Errorf("%w: placement %s is not pinned to its feature version", contentapi.ErrInvalid, placement.FeatureKey)
		}
		feature, err := provider.website.GetFeatureVersion(provider.ctx, placement.FeatureKey, pin)
		if err != nil {
			return nil, err
		}
		if feature.FeatureID != placement.FeatureKey || feature.Version != pin || feature.State != "ready" {
			return nil, fmt.Errorf("%w: feature version %s@%s is not ready", contentapi.ErrNotFound, placement.FeatureKey, pin)
		}
		files := make(map[string]struct{}, len(feature.Files))
		for name := range feature.Files {
			files[name] = struct{}{}
		}
		manifest, err := featureruntime.ValidateManifest(feature.Manifest, files)
		if err != nil {
			return nil, fmt.Errorf("%w: %s@%s manifest: %v", contentapi.ErrUnavailable, placement.FeatureKey, pin, err)
		}
		if manifest.Key != placement.FeatureKey || manifest.Version != pin {
			return nil, fmt.Errorf("%w: manifest identity does not match %s@%s", contentapi.ErrInvalid, placement.FeatureKey, pin)
		}
		kinds, err := dataInputKinds(manifest.DataInputs)
		if err != nil {
			return nil, err
		}
		inputs := make([]contentapi.PlacementInput, 0, len(placement.Bindings))
		for _, binding := range placement.Bindings {
			kind, known := kinds[binding.InputKey]
			if !known || binding.EntityID == "" {
				return nil, fmt.Errorf("%w: unknown or unbound data input %q", contentapi.ErrInvalid, binding.InputKey)
			}
			input := contentapi.PlacementInput{InputKey: binding.InputKey, Kind: kind, EntityID: binding.EntityID}
			switch kind {
			case "scalar":
				if binding.FieldID == "" || len(binding.Fields) != 0 || binding.Lookup != nil {
					return nil, fmt.Errorf("%w: scalar input %q must bind one direct field", contentapi.ErrInvalid, binding.InputKey)
				}
				input.Fields = []contentapi.PlacementField{{InputPath: "value", FieldID: binding.FieldID}}
			case "record", "collection":
				if binding.FieldID != "" || len(binding.Fields) == 0 {
					return nil, fmt.Errorf("%w: %s input %q requires field bindings", contentapi.ErrInvalid, kind, binding.InputKey)
				}
				input.Fields = make([]contentapi.PlacementField, 0, len(binding.Fields))
				for _, field := range binding.Fields {
					if field.InputPath == "" || field.FieldID == "" {
						return nil, fmt.Errorf("%w: incomplete field binding for %q", contentapi.ErrInvalid, binding.InputKey)
					}
					input.Fields = append(input.Fields, contentapi.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID})
				}
				if binding.Lookup != nil {
					input.Lookup = &contentapi.PlacementInputLookup{Argument: binding.Lookup.Argument, FieldID: binding.Lookup.FieldID}
				}
			default:
				return nil, fmt.Errorf("%w: unsupported data input kind %q", contentapi.ErrInvalid, kind)
			}
			if binding.DetailSEO != nil && binding.FieldID == "" && len(binding.Fields) == 0 && binding.Lookup == nil {
				return nil, fmt.Errorf("%w: detail SEO binding for %q has no converted input", contentapi.ErrInvalid, binding.InputKey)
			}
			inputs = append(inputs, input)
		}
		placements = append(placements, contentapi.Placement{
			ID: placement.ID, PageID: placement.PageID, PageName: placement.PageName,
			FeatureKey: placement.FeatureKey, FeatureName: placement.FeatureName,
			Version: placement.Version, Inputs: inputs,
		})
	}
	return placements, nil
}

func dataInputKinds(rawInputs []json.RawMessage) (map[string]string, error) {
	kinds := make(map[string]string, len(rawInputs))
	for _, raw := range rawInputs {
		var input struct {
			Key  string `json:"key"`
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("%w: dataInput metadata: %v", contentapi.ErrInvalid, err)
		}
		if input.Key == "" {
			return nil, fmt.Errorf("%w: dataInput key is empty", contentapi.ErrInvalid)
		}
		if input.Kind != "scalar" && input.Kind != "record" && input.Kind != "collection" {
			return nil, fmt.Errorf("%w: dataInput %q has unsupported kind %q", contentapi.ErrInvalid, input.Key, input.Kind)
		}
		if _, exists := kinds[input.Key]; exists {
			return nil, fmt.Errorf("%w: duplicate dataInput key %q", contentapi.ErrInvalid, input.Key)
		}
		kinds[input.Key] = input.Kind
	}
	return kinds, nil
}

var _ contentapi.ProjectionsProvider = (*contentProjectionProvider)(nil)
