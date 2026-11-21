package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

const (
	contentModelsNamespace = "website/content-models"
	snapshotsNamespace     = websiterepo.SnapshotNamespace
)

type contentModelRecord struct {
	DataModel json.RawMessage `json:"data_model"`
	Digest    string          `json:"digest"`
}

func (s *Service) GetContentModel(ctx context.Context) (websiteapi.ContentModel, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.ContentModel{}, err
	}
	return s.readContentModel(ctx)
}

func (s *Service) SaveContentModel(ctx context.Context, request websiteapi.SaveContentModelRequest) (websiteapi.ContentModel, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.ContentModel{}, err
	}
	if request.ExpectedRevision < 0 {
		return websiteapi.ContentModel{}, websiteapi.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return websiteapi.ContentModel{}, err
	}
	model, err := canonicalJSONObject(request.DataModel)
	if err != nil || validateDataModel(model) != nil {
		return websiteapi.ContentModel{}, websiteapi.ErrInvalid
	}
	if len(model) > 1<<20 || len(model) > s.store.MaxPayloadBytes() {
		return websiteapi.ContentModel{}, websiteapi.ErrTooLarge
	}
	current, readErr := s.store.Get(ctx, contentModelKey())
	if readErr != nil && !errors.Is(readErr, core.ErrModuleDataNotFound) {
		return websiteapi.ContentModel{}, mapCoreError(readErr)
	}
	currentRevision := int64(0)
	if readErr == nil {
		if _, err := decodeContentModelRecord(current); err != nil {
			return websiteapi.ContentModel{}, err
		}
		currentRevision = current.Revision
	}
	if request.ExpectedRevision != currentRevision {
		return websiteapi.ContentModel{}, websiteapi.ErrConflict
	}
	if currentRevision == math.MaxInt64 {
		return websiteapi.ContentModel{}, websiteapi.ErrUnavailable
	}
	if err := s.validateModelBindings(ctx, model); err != nil {
		return websiteapi.ContentModel{}, err
	}
	schema := websiteapi.ContentModel{Version: websiteapi.ContentProjectionVersion, ModelRevision: currentRevision + 1, DataModel: model}
	schema.Digest, err = contentModelDigest(schema)
	if err != nil {
		return websiteapi.ContentModel{}, websiteapi.ErrUnavailable
	}
	payload, err := json.Marshal(contentModelRecord{DataModel: schema.DataModel, Digest: schema.Digest})
	if err != nil {
		return websiteapi.ContentModel{}, websiteapi.ErrUnavailable
	}
	if len(payload) > s.store.MaxPayloadBytes() {
		return websiteapi.ContentModel{}, websiteapi.ErrTooLarge
	}
	index, err := json.Marshal(map[string]any{"record_type": "content_model", "revision": schema.ModelRevision, "digest": schema.Digest})
	if err != nil {
		return websiteapi.ContentModel{}, websiteapi.ErrUnavailable
	}
	if err := s.store.Batch(ctx, []core.ModuleDataMutation{{
		Key: contentModelKey(), ExpectedRevision: currentRevision,
		Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: 1, IndexData: index},
	}}); err != nil {
		return websiteapi.ContentModel{}, mapCoreError(err)
	}
	return schema, nil
}

func (s *Service) readContentModel(ctx context.Context) (websiteapi.ContentModel, error) {
	record, err := s.store.Get(ctx, contentModelKey())
	if err != nil {
		return websiteapi.ContentModel{}, mapCoreError(err)
	}
	value, err := decodeContentModelRecord(record)
	if err != nil {
		return websiteapi.ContentModel{}, err
	}
	return value, nil
}

func decodeContentModelRecord(record core.ModuleDataRecord) (websiteapi.ContentModel, error) {
	var stored contentModelRecord
	var index struct {
		RecordType string `json:"record_type"`
		Revision   int64  `json:"revision"`
		Digest     string `json:"digest"`
	}
	if record.PayloadVersion != 1 || record.Revision < 1 || json.Unmarshal(record.Payload, &stored) != nil ||
		json.Unmarshal(record.IndexData, &index) != nil || index.RecordType != "content_model" ||
		index.Revision != record.Revision || !sha256HexPattern.MatchString(index.Digest) || index.Digest != stored.Digest ||
		!validJSONObject(stored.DataModel) || validateDataModel(stored.DataModel) != nil {
		return websiteapi.ContentModel{}, websiteapi.ErrUnavailable
	}
	schema := websiteapi.ContentModel{Version: websiteapi.ContentProjectionVersion, ModelRevision: record.Revision,
		Digest: stored.Digest, DataModel: stored.DataModel}
	expectedDigest, err := contentModelDigest(schema)
	if err != nil || expectedDigest != schema.Digest {
		return websiteapi.ContentModel{}, websiteapi.ErrUnavailable
	}
	return schema, nil
}

func contentModelKey() core.ModuleDataKey {
	return core.ModuleDataKey{Namespace: contentModelsNamespace, ScopeType: "global", ScopeID: "global", RecordKey: "model"}
}

func contentModelDigest(schema websiteapi.ContentModel) (string, error) {
	payload, err := json.Marshal(struct {
		Version       int             `json:"version"`
		ModelRevision int64           `json:"model_revision"`
		DataModel     json.RawMessage `json:"data_model"`
	}{schema.Version, schema.ModelRevision, schema.DataModel})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) GetPagePlacementProjection(ctx context.Context, pageID, snapshotID string) (websiteapi.PagePlacementProjection, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.PagePlacementProjection{}, err
	}
	if !snapshotIDPattern.MatchString(pageID) || !snapshotIDPattern.MatchString(snapshotID) {
		return websiteapi.PagePlacementProjection{}, websiteapi.ErrNotFound
	}
	if _, _, err := s.loadPage(ctx, pageID); err != nil {
		return websiteapi.PagePlacementProjection{}, err
	}
	snapshot, record, err := s.snapshotInPage(ctx, pageID, snapshotID)
	if err != nil {
		return websiteapi.PagePlacementProjection{}, err
	}
	schema, err := s.readContentModel(ctx)
	if err != nil {
		return websiteapi.PagePlacementProjection{}, err
	}
	page, err := snapshotPageFromConfig(snapshot.Payload.Config)
	if err != nil || (page.ID != "" && page.ID != pageID) {
		return websiteapi.PagePlacementProjection{}, websiteapi.ErrUnavailable
	}
	entities, err := dataModelIndex(schema.DataModel)
	if err != nil {
		return websiteapi.PagePlacementProjection{}, websiteapi.ErrUnavailable
	}
	projection := websiteapi.PagePlacementProjection{
		Version: websiteapi.ContentProjectionVersion, PageID: pageID, PageSnapshotID: snapshotID,
		Revision: record.Revision, State: snapshot.State, Placements: make([]websiteapi.PagePlacement, 0, len(page.Placements)),
	}
	pageName := page.Name
	if pageName == "" {
		pageName = snapshot.Name
	}
	for _, placement := range page.Placements {
		if placement.Build == nil || !identifierPattern.MatchString(placement.ID) || !identifierPattern.MatchString(placement.Build.FeatureKey) ||
			!featureVersionPattern.MatchString(placement.Build.Version) {
			return websiteapi.PagePlacementProjection{}, websiteapi.ErrUnavailable
		}
		bindings := make([]websiteapi.PlacementBinding, 0, len(placement.DataBindings))
		for _, binding := range placement.DataBindings {
			converted, err := convertPlacementBinding(binding, entities)
			if err != nil {
				return websiteapi.PagePlacementProjection{}, websiteapi.ErrUnavailable
			}
			bindings = append(bindings, converted)
		}
		projection.Placements = append(projection.Placements, websiteapi.PagePlacement{
			ID: placement.ID, PageID: pageID, PageName: pageName, FeatureKey: placement.Build.FeatureKey,
			FeatureName: placement.Build.FeatureKey, Version: placement.Build.Version, Bindings: bindings,
		})
	}
	projection.Digest, err = pagePlacementDigest(projection)
	if err != nil {
		return websiteapi.PagePlacementProjection{}, websiteapi.ErrUnavailable
	}
	return projection, nil
}

func pagePlacementDigest(projection websiteapi.PagePlacementProjection) (string, error) {
	payload, err := json.Marshal(struct {
		Version        int                        `json:"version"`
		PageID         string                     `json:"page_id"`
		PageSnapshotID string                     `json:"page_snapshot_id"`
		Revision       int64                      `json:"revision"`
		State          string                     `json:"state"`
		Placements     []websiteapi.PagePlacement `json:"placements"`
	}{projection.Version, projection.PageID, projection.PageSnapshotID, projection.Revision, projection.State, projection.Placements})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func dataModelIndex(raw json.RawMessage) (map[string]map[string]struct{}, error) {
	var model dataModelContract
	if err := json.Unmarshal(raw, &model); err != nil || model.Entities == nil {
		return nil, websiteapi.ErrInvalid
	}
	entities := make(map[string]map[string]struct{}, len(*model.Entities))
	for _, entity := range *model.Entities {
		fields := make(map[string]struct{}, len(*entity.Fields))
		for _, field := range *entity.Fields {
			fields[field.ID] = struct{}{}
		}
		entities[entity.ID] = fields
	}
	return entities, nil
}

func convertPlacementBinding(binding snapshotDataBinding, entities map[string]map[string]struct{}) (websiteapi.PlacementBinding, error) {
	fields, exists := entities[binding.EntityID]
	if !identifierPattern.MatchString(binding.InputKey) || !exists {
		return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
	}
	result := websiteapi.PlacementBinding{InputKey: binding.InputKey, EntityID: binding.EntityID}
	validateField := func(fieldID string) bool { _, ok := fields[fieldID]; return ok }
	if binding.FieldID != "" {
		if !validateField(binding.FieldID) || len(binding.Fields) > 0 || binding.Lookup != nil {
			return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
		}
		result.FieldID = binding.FieldID
	}
	if len(binding.Fields) > 0 {
		result.Fields = make([]websiteapi.PlacementField, 0, len(binding.Fields))
		paths := make(map[string]struct{}, len(binding.Fields))
		for _, field := range binding.Fields {
			if !validInputPath(field.InputPath) || !validateField(field.FieldID) {
				return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
			}
			if _, duplicate := paths[field.InputPath]; duplicate {
				return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
			}
			paths[field.InputPath] = struct{}{}
			result.Fields = append(result.Fields, websiteapi.PlacementField{InputPath: field.InputPath, FieldID: field.FieldID})
		}
	}
	if binding.Lookup != nil {
		if result.FieldID != "" || len(result.Fields) == 0 || !identifierPattern.MatchString(binding.Lookup.Argument) || !validateField(binding.Lookup.FieldID) {
			return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
		}
		result.Lookup = &websiteapi.PlacementInputLookup{Argument: binding.Lookup.Argument, FieldID: binding.Lookup.FieldID}
	}
	if binding.DetailSEO != nil {
		seo := &websiteapi.PlacementDetailSEO{
			TitleFieldID: binding.DetailSEO.TitleFieldID, DescriptionFieldID: binding.DetailSEO.DescriptionFieldID,
			OGTitleFieldID: binding.DetailSEO.OGTitleFieldID, OGDescriptionFieldID: binding.DetailSEO.OGDescriptionFieldID,
			OGImageFieldID: binding.DetailSEO.OGImageFieldID,
		}
		for _, fieldID := range []string{seo.TitleFieldID, seo.DescriptionFieldID, seo.OGTitleFieldID, seo.OGDescriptionFieldID, seo.OGImageFieldID} {
			if fieldID != "" && !validateField(fieldID) {
				return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
			}
		}
		result.DetailSEO = seo
	}
	if result.FieldID == "" && len(result.Fields) == 0 {
		return websiteapi.PlacementBinding{}, websiteapi.ErrInvalid
	}
	return result, nil
}

var inputPathPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*){0,15}$`)

func validInputPath(value string) bool { return inputPathPattern.MatchString(value) }

func (s *Service) validateModelBindings(ctx context.Context, raw json.RawMessage) error {
	entities, err := dataModelIndex(raw)
	if err != nil {
		return websiteapi.ErrInvalid
	}
	selectors := []core.ModuleDataSelector{{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}}}
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		keys, records, next, err := s.store.List(ctx, snapshotsNamespace, "global", "global", after, selectors, s.store.MaxPageSize())
		if err != nil {
			return mapCoreError(err)
		}
		if len(keys) != len(records) {
			return websiteapi.ErrUnavailable
		}
		for index, record := range records {
			snapshot, err := decodeSnapshotRecord(keys[index], record)
			if err != nil {
				return websiteapi.ErrUnavailable
			}
			if _, _, err := s.loadPage(ctx, snapshot.PageID); err != nil {
				if errors.Is(err, websiteapi.ErrNotFound) {
					return websiteapi.ErrUnavailable
				}
				return err
			}
			page, err := snapshotPageFromConfig(snapshot.Payload.Config)
			if err != nil {
				return websiteapi.ErrUnavailable
			}
			for _, placement := range page.Placements {
				for _, binding := range placement.DataBindings {
					if _, err := convertPlacementBinding(binding, entities); err != nil {
						return websiteapi.ErrConflict
					}
				}
			}
		}
		if next == "" {
			return nil
		}
		if next == after {
			return websiteapi.ErrUnavailable
		}
		after = next
	}
}

var _ websiteapi.ContentModelAPI = (*Service)(nil)
var _ websiteapi.ContentModelWriteAPI = (*Service)(nil)
