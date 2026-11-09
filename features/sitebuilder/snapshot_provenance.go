package sitebuilder

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	snapshotOriginRoot        = "root"
	snapshotOriginChild       = "child"
	snapshotOriginContentFork = "content_fork"
	snapshotOriginSitepack    = "sitepack_import"
)

// SnapshotCreationSummary is an immutable, value-free account of how a
// snapshot was created. It is stored inside the existing snapshot payload.
type SnapshotCreationSummary struct {
	Origin                string                         `json:"origin"`
	Changes               []string                       `json:"changes,omitempty"`
	FeatureChanges        []SnapshotFeatureChange        `json:"featureChanges,omitempty"`
	InheritedModelContent []SnapshotModelContentTransfer `json:"inheritedModelContent,omitempty"`
}

type SnapshotFeatureChange struct {
	FeatureKey      string `json:"featureKey"`
	Action          string `json:"action"`
	PreviousVersion string `json:"previousVersion,omitempty"`
	Version         string `json:"version,omitempty"`
}

type SnapshotModelContentTransfer struct {
	EntityKey        string `json:"entityKey"`
	PublishedRecords int64  `json:"publishedRecords"`
	DraftRecords     int64  `json:"draftRecords"`
}

type snapshotSummaryQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type snapshotSummaryRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func snapshotActorName(ctx context.Context, query snapshotSummaryRowQuerier, actor uuid.UUID) (*string, error) {
	if actor == uuid.Nil {
		return nil, nil
	}
	var name string
	err := query.QueryRow(ctx, `SELECT name FROM access_tokens WHERE id=$1 AND revoked_at IS NULL`, actor).Scan(&name)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &name, nil
}

func snapshotModelContentTransfers(ctx context.Context, query snapshotSummaryRowQuerier, snapshotID uuid.UUID, model *DataModel) ([]SnapshotModelContentTransfer, error) {
	if model == nil {
		return nil, nil
	}
	state, err := readStoredModelContent(ctx, query, snapshotID, false)
	if err != nil {
		return nil, err
	}
	countByEntity := func(records []ModelContentRecord) map[string]int64 {
		counts := map[string]int64{}
		for _, record := range records {
			counts[record.EntityID]++
		}
		return counts
	}
	draftCounts := countByEntity(state.DraftRecords)
	publishedCounts := countByEntity(state.PublishedRecords)
	items := make([]SnapshotModelContentTransfer, 0, len(model.Entities))
	for _, entity := range model.Entities {
		item := SnapshotModelContentTransfer{EntityKey: entity.Key, PublishedRecords: publishedCounts[entity.ID], DraftRecords: draftCounts[entity.ID]}
		if item.PublishedRecords > 0 || item.DraftRecords > 0 {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].EntityKey < items[j].EntityKey })
	return items, nil
}

func snapshotCreationSummary(parent *SnapshotPayload, next SnapshotPayload) SnapshotCreationSummary {
	if parent == nil {
		return SnapshotCreationSummary{Origin: snapshotOriginRoot}
	}

	result := SnapshotCreationSummary{Origin: snapshotOriginChild}
	previousConfig, nextConfig := parent.Config, next.Config
	previousConfig.Features = nil
	nextConfig.Features = nil
	if !snapshotJSONEqual(previousConfig, nextConfig) {
		result.Changes = append(result.Changes, "pages_routes")
	}
	featureChanges := snapshotFeatureChanges(parent.Builds, next.Builds)
	if len(featureChanges) > 0 || !snapshotJSONEqual(parent.Config.Features, next.Config.Features) {
		result.Changes = append(result.Changes, "features")
		result.FeatureChanges = featureChanges
	}
	if !snapshotJSONEqual(parent.Metadata, next.Metadata) {
		result.Changes = append(result.Changes, "site_metadata")
	}
	if !snapshotJSONEqual(parent.DesignSystem, next.DesignSystem) || parent.DesignSystemRevision != next.DesignSystemRevision {
		result.Changes = append(result.Changes, "design_system")
	}
	return result
}

func snapshotFeatureChanges(previous, next []PinnedBuild) []SnapshotFeatureChange {
	before := make(map[string]PinnedBuild, len(previous))
	after := make(map[string]PinnedBuild, len(next))
	for _, build := range previous {
		before[build.Key] = build
	}
	for _, build := range next {
		after[build.Key] = build
	}
	keys := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	changes := make([]SnapshotFeatureChange, 0)
	for _, key := range ordered {
		oldBuild, hadOld := before[key]
		newBuild, hasNew := after[key]
		switch {
		case !hadOld && hasNew:
			changes = append(changes, SnapshotFeatureChange{FeatureKey: key, Action: "added", Version: newBuild.Version})
		case hadOld && !hasNew:
			changes = append(changes, SnapshotFeatureChange{FeatureKey: key, Action: "removed", PreviousVersion: oldBuild.Version})
		case hadOld && hasNew && (oldBuild.Version != newBuild.Version || oldBuild.Digest != newBuild.Digest):
			changes = append(changes, SnapshotFeatureChange{FeatureKey: key, Action: "updated", PreviousVersion: oldBuild.Version, Version: newBuild.Version})
		}
	}
	return changes
}

func snapshotJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func decodeSnapshotCreationSummary(raw []byte) *SnapshotCreationSummary {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var summary SnapshotCreationSummary
	if json.Unmarshal(raw, &summary) != nil || summary.Origin == "" {
		return nil
	}
	return &summary
}
