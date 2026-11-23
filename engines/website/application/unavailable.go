package application

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	platform "github.com/Zubrium/cdn/engines/website/application/platform"
	featureruntime "github.com/Zubrium/cdn/engines/website/features/runtime"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
	recorddata "github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

// Mutations and read-only catalog APIs stay unavailable until their durable
// worker contracts and Core capabilities are provided by composition. Returning
// a typed unavailable error keeps incomplete production profiles fail-closed.
func (s *Service) ListFeatureCatalog(ctx context.Context, cursor string, limit int) (websiteapi.FeatureVersionPage, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelRead); err != nil {
		return websiteapi.FeatureVersionPage{}, err
	}
	return s.listFeatureVersions(ctx, "", cursor, limit)
}

func (s *Service) ListFeatureVersions(ctx context.Context, featureID, cursor string, limit int) (websiteapi.FeatureVersionPage, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelRead); err != nil {
		return websiteapi.FeatureVersionPage{}, err
	}
	if !identifierPattern.MatchString(featureID) {
		return websiteapi.FeatureVersionPage{}, websiteapi.ErrInvalid
	}
	return s.listFeatureVersions(ctx, featureID, cursor, limit)
}

func (s *Service) GetFeatureVersion(ctx context.Context, featureID, version string) (websiteapi.FeatureVersion, error) {
	if err := s.requireLevel(ctx, platform.AccessLevelRead); err != nil {
		return websiteapi.FeatureVersion{}, err
	}
	if !identifierPattern.MatchString(featureID) || !featureVersionPattern.MatchString(version) {
		return websiteapi.FeatureVersion{}, websiteapi.ErrInvalid
	}
	record, err := s.store.Get(ctx, websiterepo.FeatureVersionKey(featureID, version))
	if err != nil {
		return websiteapi.FeatureVersion{}, mapCoreError(err)
	}
	value, err := decodeFeatureVersionRecord(record, featureID, version)
	if err != nil {
		return websiteapi.FeatureVersion{}, err
	}
	return value, nil
}

func (s *Service) listFeatureVersions(ctx context.Context, featureID, cursor string, limit int) (websiteapi.FeatureVersionPage, error) {
	if s == nil || s.store == nil || len(cursor) > 1024 || limit < 1 || limit > s.store.MaxPageSize() {
		return websiteapi.FeatureVersionPage{}, websiteapi.ErrInvalid
	}
	selectors := []recorddata.RecordSelector{
		{Field: "record_type", Operator: recorddata.RecordSelectorEqual, Values: []any{"feature_version"}},
		{Field: "state", Operator: recorddata.RecordSelectorEqual, Values: []any{importStateReady}},
	}
	if featureID != "" {
		selectors = append(selectors, recorddata.RecordSelector{
			Field: "feature_id", Operator: recorddata.RecordSelectorEqual, Values: []any{featureID},
		})
	}
	keys, records, next, err := s.store.List(ctx, websiterepo.FeatureCatalogNS, "global", websiterepo.CatalogScopeID, cursor, selectors, limit)
	if err != nil {
		return websiteapi.FeatureVersionPage{}, mapCoreError(err)
	}
	if len(keys) != len(records) {
		return websiteapi.FeatureVersionPage{}, websiteapi.ErrUnavailable
	}
	items := make([]websiteapi.FeatureVersion, len(records))
	for index, record := range records {
		value, decodeErr := decodeFeatureVersionRecord(record, featureID, "")
		if decodeErr != nil {
			return websiteapi.FeatureVersionPage{}, decodeErr
		}
		items[index] = value
	}
	return websiteapi.FeatureVersionPage{Items: items, NextCursor: next}, nil
}

func decodeFeatureVersionRecord(record recorddata.Record, expectedFeatureID, expectedVersion string) (websiteapi.FeatureVersion, error) {
	var stored featureVersionRecord
	var index struct {
		RecordType string `json:"record_type"`
		FeatureID  string `json:"feature_id"`
		Version    string `json:"version"`
		State      string `json:"state"`
	}
	if record.PayloadVersion != importPayloadVersion || json.Unmarshal(record.Payload, &stored) != nil ||
		json.Unmarshal(record.IndexData, &index) != nil || stored.RecordType != "feature_version" ||
		index.RecordType != stored.RecordType || index.FeatureID != stored.FeatureID || index.Version != stored.Version || index.State != stored.State ||
		!identifierPattern.MatchString(stored.FeatureID) || !featureVersionPattern.MatchString(stored.Version) || stored.State != importStateReady ||
		(expectedFeatureID != "" && !strings.EqualFold(stored.FeatureID, expectedFeatureID)) ||
		(expectedVersion != "" && !strings.EqualFold(stored.Version, expectedVersion)) ||
		!sha256HexPattern.MatchString(stored.Digest) || stored.ArtifactSize < 0 || stored.CreatedAt.IsZero() ||
		!json.Valid(stored.Manifest) || len(stored.Files) == 0 {
		return websiteapi.FeatureVersion{}, websiteapi.ErrUnavailable
	}
	fileNames := make(map[string]struct{}, len(stored.Files))
	for name, fileID := range stored.Files {
		if name == "" || fileID == "" {
			return websiteapi.FeatureVersion{}, websiteapi.ErrUnavailable
		}
		fileNames[name] = struct{}{}
	}
	manifest, err := featureruntime.ValidateManifest(stored.Manifest, fileNames)
	if err != nil || manifest.Key != stored.FeatureID || manifest.Version != stored.Version {
		return websiteapi.FeatureVersion{}, websiteapi.ErrUnavailable
	}
	if _, err := uuid.Parse(stored.FileSetID); err != nil {
		return websiteapi.FeatureVersion{}, websiteapi.ErrUnavailable
	}
	files := make(map[string]string, len(stored.Files))
	for name, fileID := range stored.Files {
		if _, err := uuid.Parse(fileID); err != nil {
			return websiteapi.FeatureVersion{}, websiteapi.ErrUnavailable
		}
		files[name] = fileID
	}
	return websiteapi.FeatureVersion{
		FeatureID: stored.FeatureID, Version: stored.Version, Manifest: append([]byte(nil), stored.Manifest...),
		FileSetID: stored.FileSetID, Files: files, Digest: stored.Digest, ArtifactSize: stored.ArtifactSize,
		CreatedAt: stored.CreatedAt, State: stored.State,
	}, nil
}

func validBuildLocale(locale string) bool {
	if len(locale) < 2 || len(locale) > 64 {
		return false
	}
	for _, char := range locale {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}
