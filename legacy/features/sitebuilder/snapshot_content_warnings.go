package sitebuilder

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type snapshotWarningQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// snapshotContentWarnings reports only whether stored model data matches the
// immutable snapshot model. Feature manifests no longer define CMS schemas.
func snapshotContentWarnings(ctx context.Context, query snapshotWarningQueryer, snapshotID uuid.UUID, _ []PinnedBuild) ([]string, error) {
	var raw []byte
	if err := query.QueryRow(ctx, `SELECT payload FROM site_snapshots WHERE id=$1`, snapshotID).Scan(&raw); err != nil {
		return nil, err
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	state, err := readStoredModelContent(ctx, query, snapshotID, false)
	if err != nil {
		return nil, err
	}
	if payload.Config.DataModel == nil {
		if state.Exists || len(state.DraftRecords) > 0 || len(state.PublishedRecords) > 0 {
			return []string{"В снапшоте есть данные модели без описания модели"}, nil
		}
		return []string{}, nil
	}
	placements := modelValidationPlacements(payload)
	if err := ValidateModelContent(*payload.Config.DataModel, placements, state.DraftRecords, state.DraftSelections); err != nil {
		return []string{"Черновые записи не соответствуют текущей модели данных. Исправьте или удалите устаревшие данные в разделе «Контент»"}, nil
	}
	if err := ValidateModelContent(*payload.Config.DataModel, placements, state.PublishedRecords, state.PublishedSelection); err != nil {
		return []string{"Опубликованные записи не соответствуют текущей модели данных. Исправьте данные и опубликуйте новую ревизию в разделе «Контент»"}, nil
	}
	return []string{}, nil
}
