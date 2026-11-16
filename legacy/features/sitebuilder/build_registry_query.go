package sitebuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"sort"
)

type featureBuildQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func registryForConfigQuery(ctx context.Context, q featureBuildQuery, cfg SiteConfig) (buildRegistry, error) {
	refs := configBuildRefs(cfg)
	registry := buildRegistry{items: make(map[string]BuildInfo, len(refs))}
	for _, key := range sortedBuildKeys(refs) {
		version := refs[key]
		record, found, err := buildQuery(ctx, q, key, version)
		if err != nil {
			return registry, err
		}
		if !found {
			return registry, fmt.Errorf("feature build %s@%s was not found", key, version)
		}
		var manifest Manifest
		if err := json.Unmarshal(record.Manifest, &manifest); err != nil {
			return registry, err
		}
		assetDigests := make(map[string]string, len(record.Assets))
		for path, asset := range record.Assets {
			assetDigests[path] = asset.SHA256
		}
		registry.items[buildID(key, version)] = BuildInfo{Manifest: manifest, Digest: record.Digest, Version: record.Version, AssetDigests: assetDigests}
	}
	return registry, nil
}

func pinnedBuildsQuery(ctx context.Context, q featureBuildQuery, cfg SiteConfig) ([]PinnedBuild, error) {
	refs := configBuildRefs(cfg)
	keys := sortedBuildKeys(refs)
	out := make([]PinnedBuild, 0, len(keys))
	for _, key := range keys {
		record, found, err := buildQuery(ctx, q, key, refs[key])
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("feature build %s@%s was not found", key, refs[key])
		}
		assets := make(map[string]PinnedAsset, len(record.Assets))
		for path, asset := range record.Assets {
			assets[path] = PinnedAsset{ContentType: asset.ContentType, Size: asset.Size, SHA256: asset.SHA256}
		}
		out = append(out, PinnedBuild{
			Key: record.Key, Name: record.Name, Version: record.Version,
			SubmissionSchemaVersionID: record.SubmissionSchemaVersionID, Digest: record.Digest,
			Manifest: json.RawMessage(record.Manifest), Assets: assets,
		})
	}
	return out, nil
}

func sortedBuildKeys(refs map[string]string) []string {
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func configBuildRefs(cfg SiteConfig) map[string]string {
	refs := make(map[string]string, len(cfg.Features))
	for _, ref := range cfg.Features {
		refs[ref.FeatureKey] = ref.Version
	}
	for _, page := range cfg.Pages {
		for _, placement := range page.Placements {
			refs[placement.Build.FeatureKey] = placement.Build.Version
		}
	}
	return refs
}

func buildID(key, version string) string { return key + "@" + version }
