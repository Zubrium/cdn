package sitebuilder

import (
	"context"
	"encoding/json"

	"websiteengine/pkg/environment"
)

// FormSchemasForListing collects form contracts across configured branches.
// Forms service additionally unions schemas from historical submissions.
func (s *SnapshotService) FormSchemasForListing(ctx context.Context, key string) ([]json.RawMessage, error) {
	return s.FormSchemasForBranchListing(ctx, "", key)
}

// FormSchemasForBranchListing returns current contracts for one configured
// branch, or all configured branches. An empty key includes all form features.
func (s *SnapshotService) FormSchemasForBranchListing(ctx context.Context, branch environment.Environment, key string) ([]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT s.payload FROM site_environments e JOIN site_snapshots s ON s.id=e.active_snapshot_id WHERE e.enabled AND ($1='' OR e.key=$1)`, branch.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemas := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var payload SnapshotPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		for _, build := range payload.Builds {
			if key != "" && build.Key != key {
				continue
			}
			var manifest Manifest
			if err := json.Unmarshal(build.Manifest, &manifest); err != nil {
				return nil, err
			}
			if manifest.Form == nil {
				continue
			}
			schema := manifest.SubmissionSchema()
			if len(schema) > 0 {
				schemas = append(schemas, schema)
			}
		}
	}
	return schemas, rows.Err()
}
