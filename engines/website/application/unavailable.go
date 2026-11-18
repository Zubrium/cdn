package application

import (
	"context"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

// Mutations and job/catalog APIs stay unavailable until their durable worker
// contracts and Core capabilities are provided by composition. Returning a
// typed unavailable error keeps incomplete production profiles fail-closed.
func (s *Service) BeginFeatureImport(ctx context.Context, _ websiteapi.ImportFeatureRequest) (websiteapi.FeatureImport, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.FeatureImport{}, err
	}
	return websiteapi.FeatureImport{}, websiteapi.ErrUnavailable
}

func (s *Service) GetFeatureImport(ctx context.Context, _ string) (websiteapi.FeatureImport, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.FeatureImport{}, err
	}
	return websiteapi.FeatureImport{}, websiteapi.ErrUnavailable
}

func (s *Service) ListFeatureCatalog(ctx context.Context, _ string, _ int) (websiteapi.FeatureVersionPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.FeatureVersionPage{}, err
	}
	return websiteapi.FeatureVersionPage{}, websiteapi.ErrUnavailable
}

func (s *Service) ListFeatureVersions(ctx context.Context, _, _ string, _ int) (websiteapi.FeatureVersionPage, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.FeatureVersionPage{}, err
	}
	return websiteapi.FeatureVersionPage{}, websiteapi.ErrUnavailable
}

func (s *Service) GetFeatureVersion(ctx context.Context, _, _ string) (websiteapi.FeatureVersion, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.FeatureVersion{}, err
	}
	return websiteapi.FeatureVersion{}, websiteapi.ErrUnavailable
}

func (s *Service) BuildSnapshot(ctx context.Context, request websiteapi.BuildSnapshotRequest) (websiteapi.BuildJob, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.BuildJob{}, err
	}
	snapshot, err := s.GetSnapshot(ctx, request.SnapshotID)
	if err != nil {
		return websiteapi.BuildJob{}, err
	}
	if snapshot.State != "ready" {
		return websiteapi.BuildJob{}, websiteapi.ErrConflict
	}
	return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
}

func (s *Service) GetBuild(ctx context.Context, _ string) (websiteapi.BuildJob, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.BuildJob{}, err
	}
	return websiteapi.BuildJob{}, websiteapi.ErrUnavailable
}

func (s *Service) CreateMediaDerivative(ctx context.Context, _ websiteapi.CreateDerivativeRequest) (websiteapi.MediaJob, error) {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return websiteapi.MediaJob{}, err
	}
	return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
}

func (s *Service) GetMediaJob(ctx context.Context, _ string) (websiteapi.MediaJob, error) {
	if err := s.requireLevel(ctx, core.AccessLevelRead); err != nil {
		return websiteapi.MediaJob{}, err
	}
	return websiteapi.MediaJob{}, websiteapi.ErrUnavailable
}
