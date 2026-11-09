package sitebuilder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CreateSnapshotInput struct {
	Name                          string        `json:"name"`
	ParentID                      *uuid.UUID    `json:"parentSnapshotId,omitempty"`
	ExpectedParentContentRevision *int64        `json:"expectedParentContentRevision,omitempty"`
	Config                        *SiteConfig   `json:"config,omitempty"`
	Metadata                      *SiteMetadata `json:"metadata,omitempty"`
	DesignSystem                  *DesignSystem `json:"designSystem,omitempty"`
}
type SnapshotDetail struct {
	Snapshot SnapshotRecord  `json:"snapshot"`
	Payload  SnapshotPayload `json:"payload"`
}

// SnapshotDigest is independent of JSON key order and mutable editorial data.
func SnapshotDigest(payload SnapshotPayload) (string, error) {
	// Creation provenance is stored in the existing payload for auditability,
	// but it is descriptive metadata rather than part of the site's structure.
	payload.CreationSummary = nil
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func lockFileReferences(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_file_references'))`)
	return err
}
func (s *SnapshotService) Snapshot(ctx context.Context, id uuid.UUID) (SnapshotDetail, error) {
	record, payload, err := s.snapshotPayload(ctx, id)
	if err != nil {
		return SnapshotDetail{}, err
	}
	// Provenance is administration metadata; return it alongside the snapshot
	// record and keep the immutable site payload limited to runtime structure.
	record.CreationSummary = payload.CreationSummary
	payload.CreationSummary = nil
	record.Warnings, err = snapshotContentWarnings(ctx, s.pool, id, payload.Builds)
	if err != nil {
		return SnapshotDetail{}, err
	}
	return SnapshotDetail{Snapshot: record, Payload: payload}, nil
}

func (s *SnapshotService) CreateSnapshot(ctx context.Context, actor uuid.UUID, in CreateSnapshotInput) (SnapshotRecord, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 160 {
		return SnapshotRecord{}, fmt.Errorf("%w: name must contain 1–160 characters", ErrInvalid)
	}
	if in.ParentID == nil && in.ExpectedParentContentRevision != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: expected parent content revision requires a parent snapshot", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SnapshotRecord{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return SnapshotRecord{}, err
	}
	if err := lockSnapshotTree(ctx, tx); err != nil {
		return SnapshotRecord{}, err
	}
	payload := SnapshotPayload{FormatVersion: 4}
	var parentPayload *SnapshotPayload
	var parentSequence *int64
	var parentName *string
	var contentRevision int64
	if in.ParentID != nil {
		var raw []byte
		var sequence int64
		var name string
		err := tx.QueryRow(ctx, `SELECT payload,content_revision,sequence,name FROM site_snapshots WHERE id=$1 FOR UPDATE`, *in.ParentID).Scan(&raw, &contentRevision, &sequence, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return SnapshotRecord{}, ErrNotFound
		}
		if err != nil {
			return SnapshotRecord{}, err
		}
		if in.ExpectedParentContentRevision != nil && *in.ExpectedParentContentRevision != contentRevision {
			return SnapshotRecord{}, fmt.Errorf("%w: содержимое родительского снапшота изменилось после проверки; обновите отчёт и повторите действие", ErrConflict)
		}
		parentSequence, parentName = &sequence, &name
		if err := json.Unmarshal(raw, &payload); err != nil {
			return SnapshotRecord{}, err
		}
		parentCopyRaw, err := json.Marshal(payload)
		if err != nil {
			return SnapshotRecord{}, err
		}
		var parentCopy SnapshotPayload
		if err := json.Unmarshal(parentCopyRaw, &parentCopy); err != nil {
			return SnapshotRecord{}, err
		}
		parentPayload = &parentCopy
		payload.CreationSummary = nil
		payload.FormatVersion = 4
	}
	if in.Config != nil {
		payload.Config = *in.Config
	}
	if in.Metadata != nil {
		payload.Metadata = in.Metadata
	}
	if in.DesignSystem != nil {
		payload.DesignSystem = in.DesignSystem
	}
	if in.ParentID == nil && in.Config == nil {
		return SnapshotRecord{}, fmt.Errorf("%w: config is required for a root snapshot", ErrInvalid)
	}
	registry, err := registryForConfigQuery(ctx, tx, payload.Config)
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := payload.Config.Validate(registry); err != nil {
		return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	oldBuilds := payload.Builds
	payload.Builds, err = pinnedBuildsQuery(ctx, tx, payload.Config)
	if err != nil {
		return SnapshotRecord{}, err
	}
	var designWarnings []string
	if in.ParentID != nil && in.Config != nil && in.DesignSystem == nil && payload.DesignSystem != nil {
		design, warnings, reconcileErr := ReconcileSnapshotDesignSystem(*payload.DesignSystem, oldBuilds, payload.Builds)
		if reconcileErr != nil {
			return SnapshotRecord{}, fmt.Errorf("%w: design system: %v", ErrInvalid, reconcileErr)
		}
		payload.DesignSystem = &design
		designWarnings = warnings
	}
	if payload.Metadata != nil {
		if err := payload.Metadata.Validate(); err != nil {
			return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if err := s.validateMetadataMedia(ctx, tx, *payload.Metadata); err != nil {
			return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	if payload.DesignSystem == nil {
		value := NewDesignSystem()
		payload.DesignSystem = &value
	}
	if payload.DesignSystem != nil {
		normalized, err := NormalizeSnapshotDesignSystem(*payload.DesignSystem, payload.Builds)
		if err != nil {
			return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		payload.DesignSystem = &normalized
		if err := validateDesignSystemMedia(ctx, tx, normalized); err != nil {
			return SnapshotRecord{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	creationSummary := snapshotCreationSummary(parentPayload, payload)
	if in.ParentID != nil {
		creationSummary.InheritedModelContent, err = snapshotModelContentTransfers(ctx, tx, *in.ParentID, payload.Config.DataModel)
		if err != nil {
			return SnapshotRecord{}, err
		}
		if err := validateInheritedModelContent(ctx, tx, *in.ParentID, *parentPayload, payload); err != nil {
			return SnapshotRecord{}, err
		}
	}
	payload.CreationSummary = &creationSummary
	digest, err := SnapshotDigest(payload)
	if err != nil {
		return SnapshotRecord{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return SnapshotRecord{}, err
	}
	record := SnapshotRecord{ID: uuid.New(), ParentID: in.ParentID, ParentSequence: parentSequence, ParentName: parentName, Name: in.Name, Digest: digest, ContentRevision: contentRevision, Warnings: designWarnings, CreationSummary: &creationSummary}
	record.CreatedByName, err = snapshotActorName(ctx, tx, actor)
	if err != nil {
		return SnapshotRecord{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO site_snapshots(id,parent_snapshot_id,name,digest,payload,created_by,content_revision) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7) RETURNING sequence,created_at`, record.ID, in.ParentID, in.Name, digest, string(raw), actor, contentRevision).Scan(&record.Sequence, &record.CreatedAt)
	if err != nil {
		return SnapshotRecord{}, err
	}
	if payload.Config.DataModel != nil {
		state := storedModelContentWorkspace{
			DraftRecords: []ModelContentRecord{}, DraftSelections: []ModelContentSelection{},
			PublishedRecords: []ModelContentRecord{}, PublishedSelection: []ModelContentSelection{},
		}
		if in.ParentID != nil {
			state, err = readStoredModelContent(ctx, tx, *in.ParentID, false)
			if err != nil {
				return SnapshotRecord{}, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO snapshot_model_content_publications(id,snapshot_id,draft_revision,content_revision,records,selections,actor_token_id,created_at) SELECT gen_random_uuid(),$1,draft_revision,content_revision,records,selections,actor_token_id,created_at FROM snapshot_model_content_publications WHERE snapshot_id=$2`, record.ID, *in.ParentID); err != nil {
				return SnapshotRecord{}, err
			}
		}
		state, err = prepareSnapshotModelContentState(*payload.Config.DataModel, state)
		if err != nil {
			return SnapshotRecord{}, err
		}
		if err := writeModelContentWorkspace(ctx, tx, actor, record.ID, state,
			state.Revision, state.PublishedRevision, state.DraftRecords, state.DraftSelections,
			state.PublishedRecords, state.PublishedSelection,
		); err != nil {
			return SnapshotRecord{}, err
		}
	}
	// Keep incompatible values intact and return actionable warnings. Publication
	// validates them against the chosen build; importing a build never edits data.
	warnings, err := snapshotContentWarnings(ctx, tx, record.ID, payload.Builds)
	if err != nil {
		return SnapshotRecord{}, err
	}
	record.Warnings = append(record.Warnings, warnings...)
	if err := tx.Commit(ctx); err != nil {
		return SnapshotRecord{}, err
	}
	return record, nil
}

func prepareSnapshotModelContentState(model DataModel, state storedModelContentWorkspace) (storedModelContentWorkspace, error) {
	draftRecords, seeded, err := seedSingletonDraftRecords(model, state.DraftRecords)
	if err != nil {
		return storedModelContentWorkspace{}, err
	}
	if seeded {
		if state.Revision == 1<<63-1 {
			return storedModelContentWorkspace{}, fmt.Errorf("%w: model-content revision is exhausted", ErrInvalid)
		}
		state.Revision++
	}
	state.Exists = false
	state.DraftRecords = draftRecords
	return state, nil
}

func validateInheritedModelContent(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, parentID uuid.UUID, previous, next SnapshotPayload) error {
	state, err := readStoredModelContent(ctx, query, parentID, false)
	if err != nil {
		return err
	}
	if next.Config.DataModel == nil {
		if state.Exists || len(state.DraftRecords) > 0 || len(state.PublishedRecords) > 0 || len(state.DraftSelections) > 0 || len(state.PublishedSelection) > 0 {
			return fmt.Errorf("%w: clear the snapshot model data before removing its data model", ErrInvalid)
		}
	} else {
		if err := validateSnapshotSingletonTransition(previous.Config.DataModel, next.Config.DataModel, state.DraftRecords, state.PublishedRecords); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	// Structural edits may make copied records or selections incompatible with
	// the new model. Preserve them in the child so the editor can show the exact
	// stale data and require an explicit cleanup or model repair. snapshotContentWarnings
	// reports the mismatch, while content save/publication remain validation-gated.
	// Removing the model entirely stays blocked above because there would be no
	// model-backed editor capable of repairing the inherited content.
	return nil
}

func validateSnapshotSingletonTransition(previous, next *DataModel, draftRecords, publishedRecords []ModelContentRecord) error {
	if next == nil {
		return nil
	}
	previousEntities := make(map[string]DataModelEntity)
	if previous != nil {
		for _, entity := range previous.Entities {
			previousEntities[entity.ID] = entity
		}
	}
	for _, entity := range next.Entities {
		if entity.Cardinality != DataModelEntityCardinalitySingleton {
			continue
		}
		previousEntity, existed := previousEntities[entity.ID]
		for _, state := range []struct {
			label   string
			records []ModelContentRecord
		}{
			{label: "draft", records: draftRecords},
			{label: "published", records: publishedRecords},
		} {
			count := 0
			for _, record := range state.records {
				if record.EntityID == entity.ID {
					count++
				}
			}
			if count <= 1 {
				continue
			}
			if existed && previousEntity.Cardinality != DataModelEntityCardinalitySingleton {
				return fmt.Errorf("entity %q cannot change from many to singleton while its parent %s state has %d records; remove the extras and publish the parent content in /content first", entity.Name, state.label, count)
			}
			return fmt.Errorf("singleton entity %q has %d parent %s records; remove the extras and publish the parent content in /content first", entity.Name, count, state.label)
		}
	}
	return nil
}

func (s *SnapshotService) DeleteSnapshot(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return err
	}
	if err := lockSnapshotTree(ctx, tx); err != nil {
		return err
	}
	var parent *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT parent_snapshot_id FROM site_snapshots WHERE id=$1 FOR UPDATE`, id).Scan(&parent)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var protected bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM site_environments e JOIN site_snapshots s ON s.id=e.active_snapshot_id WHERE e.enabled AND (s.id=$1 OR s.parent_snapshot_id=$1))`, id).Scan(&protected)
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("%w: snapshot is assigned to a branch or is its immediate parent", ErrConflict)
	}
	if _, err := tx.Exec(ctx, `UPDATE site_snapshots SET parent_snapshot_id=$2 WHERE parent_snapshot_id=$1`, id, parent); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM site_snapshots WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
