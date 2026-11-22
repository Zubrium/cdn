package application

import (
	"context"
	"unicode"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

// Mutations and read-only catalog APIs stay unavailable until their durable
// worker contracts and Core capabilities are provided by composition. Returning
// a typed unavailable error keeps incomplete production profiles fail-closed.
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
