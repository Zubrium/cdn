package composition

import (
	"encoding/json"

	contentapi "github.com/Zubrium/cdn/engines/content/presentation"
	"github.com/Zubrium/cdn/pkg/contracts"
)

func encodeContentProjection(projection contentapi.Projections) contracts.ContentProjection {
	return contracts.ContentProjection{
		ContentModel: contracts.ContentModelProjection{
			Version: projection.ContentModel.Version, ModelRevision: projection.ContentModel.ModelRevision,
			Digest: projection.ContentModel.Digest, DataModel: append(json.RawMessage(nil), projection.ContentModel.DataModel...),
		},
		Page: contracts.PagePlacementProjection{
			Version: projection.Page.Version, PageSnapshotID: projection.Page.PageSnapshotID,
			PageID: projection.Page.PageID, Revision: projection.Page.Revision, Digest: projection.Page.Digest,
			State: projection.Page.State, Placements: encodeContentPlacements(projection.Page.Placements),
		},
		Locale: projection.Locale,
	}
}

func decodeContentProjection(projection contracts.ContentProjection) contentapi.Projections {
	return contentapi.Projections{
		ContentModel: contentapi.ContentModelProjection{
			Version: projection.ContentModel.Version, ModelRevision: projection.ContentModel.ModelRevision,
			Digest: projection.ContentModel.Digest, DataModel: append(json.RawMessage(nil), projection.ContentModel.DataModel...),
		},
		Page: contentapi.PagePlacementProjection{
			Version: projection.Page.Version, PageSnapshotID: projection.Page.PageSnapshotID,
			PageID: projection.Page.PageID, Revision: projection.Page.Revision, Digest: projection.Page.Digest,
			State: projection.Page.State, Placements: decodeContentPlacements(projection.Page.Placements),
		},
		Locale: projection.Locale,
	}
}

func encodeContentPlacements(source []contentapi.Placement) []contracts.ContentPlacement {
	result := make([]contracts.ContentPlacement, 0, len(source))
	for _, placement := range source {
		result = append(result, contracts.ContentPlacement{
			ID: placement.ID, PageID: placement.PageID, PageName: placement.PageName,
			FeatureKey: placement.FeatureKey, FeatureName: placement.FeatureName, Version: placement.Version,
			Inputs: encodeContentPlacementInputs(placement.Inputs),
		})
	}
	return result
}

func decodeContentPlacements(source []contracts.ContentPlacement) []contentapi.Placement {
	result := make([]contentapi.Placement, 0, len(source))
	for _, placement := range source {
		result = append(result, contentapi.Placement{
			ID: placement.ID, PageID: placement.PageID, PageName: placement.PageName,
			FeatureKey: placement.FeatureKey, FeatureName: placement.FeatureName, Version: placement.Version,
			Inputs: decodeContentPlacementInputs(placement.Inputs),
		})
	}
	return result
}

func encodeContentPlacementInputs(source []contentapi.PlacementInput) []contracts.ContentPlacementInput {
	result := make([]contracts.ContentPlacementInput, 0, len(source))
	for _, input := range source {
		encoded := contracts.ContentPlacementInput{
			InputKey: input.InputKey, Kind: input.Kind, EntityID: input.EntityID,
			Fields: make([]contracts.ContentPlacementField, 0, len(input.Fields)),
		}
		for _, field := range input.Fields {
			encoded.Fields = append(encoded.Fields, contracts.ContentPlacementField{InputPath: field.InputPath, FieldID: field.FieldID, MediaAccept: field.MediaAccept})
		}
		if input.Lookup != nil {
			encoded.Lookup = &contracts.ContentPlacementLookup{Argument: input.Lookup.Argument, FieldID: input.Lookup.FieldID}
		}
		result = append(result, encoded)
	}
	return result
}

func decodeContentPlacementInputs(source []contracts.ContentPlacementInput) []contentapi.PlacementInput {
	result := make([]contentapi.PlacementInput, 0, len(source))
	for _, input := range source {
		decoded := contentapi.PlacementInput{
			InputKey: input.InputKey, Kind: input.Kind, EntityID: input.EntityID,
			Fields: make([]contentapi.PlacementField, 0, len(input.Fields)),
		}
		for _, field := range input.Fields {
			decoded.Fields = append(decoded.Fields, contentapi.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID, MediaAccept: field.MediaAccept})
		}
		if input.Lookup != nil {
			decoded.Lookup = &contentapi.PlacementInputLookup{Argument: input.Lookup.Argument, FieldID: input.Lookup.FieldID}
		}
		result = append(result, decoded)
	}
	return result
}
