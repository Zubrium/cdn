package sitebuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *SnapshotService) snapshotPayload(ctx context.Context, id uuid.UUID) (SnapshotRecord, SnapshotPayload, error) {
	var record SnapshotRecord
	var raw []byte
	var parentSequence pgtype.Int8
	var parentName, createdByName pgtype.Text
	err := s.pool.QueryRow(ctx, `SELECT s.id,s.parent_snapshot_id,parent.sequence,parent.name,s.sequence,s.name,s.digest,s.created_at,s.content_revision,s.payload,creator.name
		FROM site_snapshots s
		LEFT JOIN site_snapshots parent ON parent.id=s.parent_snapshot_id
		LEFT JOIN access_tokens creator ON creator.id=s.created_by AND creator.revoked_at IS NULL
		WHERE s.id=$1`, id).
		Scan(&record.ID, &record.ParentID, &parentSequence, &parentName, &record.Sequence, &record.Name, &record.Digest, &record.CreatedAt, &record.ContentRevision, &raw, &createdByName)
	if errors.Is(err, pgx.ErrNoRows) {
		return SnapshotRecord{}, SnapshotPayload{}, ErrNotFound
	}
	if err != nil {
		return SnapshotRecord{}, SnapshotPayload{}, err
	}
	if parentSequence.Valid {
		record.ParentSequence = &parentSequence.Int64
	}
	if parentName.Valid {
		record.ParentName = &parentName.String
	}
	if createdByName.Valid {
		record.CreatedByName = &createdByName.String
	}
	var payload SnapshotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return SnapshotRecord{}, SnapshotPayload{}, err
	}
	return record, payload, nil
}

func pinnedBuildForKey(builds []PinnedBuild, key string) (PinnedBuild, bool) {
	for _, build := range builds {
		if build.Key == key {
			return build, true
		}
	}
	return PinnedBuild{}, false
}

func jsonEqual(left, right []byte) bool {
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	a, err := decode(left)
	if err != nil {
		return false
	}
	b, err := decode(right)
	return err == nil && reflect.DeepEqual(a, b)
}

func validateRecordIDAnnotations(raw json.RawMessage) error {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	var walk func(any) error
	walk = func(value any) error {
		switch node := value.(type) {
		case map[string]any:
			if annotation, exists := node["x-engine-record-id"]; exists {
				marked, ok := annotation.(bool)
				if !ok || !marked || node["type"] != "string" {
					return errors.New("x-engine-record-id is only valid on generated string record IDs")
				}
			}
			for _, child := range node {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root)
}
