package sitebuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/files"
)

func (s *SnapshotService) References(ctx context.Context, id uuid.UUID) ([]files.Reference, error) {
	return findFileReferences(ctx, s.pool, id)
}

func (s *SnapshotService) ReferencesTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) ([]files.Reference, error) {
	return findFileReferences(ctx, tx, id)
}

type referenceQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func findFileReferences(ctx context.Context, queryer referenceQueryer, id uuid.UUID) ([]files.Reference, error) {
	target := id.String()
	out := []files.Reference{}
	add := func(kind, key string) {
		out = append(out, files.Reference{Kind: kind, ID: key, Description: fmt.Sprintf("%s: %s", kind, key)})
	}
	// The LIKE predicate is only a coarse prefilter; typed manifest traversal
	// decides whether an actual media reference exists.
	rows, err := queryer.Query(ctx, `SELECT id::text,payload FROM site_snapshots WHERE payload::text LIKE '%'||$1||'%'`, target)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var payload SnapshotPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			rows.Close()
			return nil, err
		}
		refs := newPackReferences(nil)
		refs.clearTarget = target
		index := sitepackIndex{Payload: payload}
		if err := refs.transform(&index); err != nil {
			rows.Close()
			return nil, err
		}
		if refs.cleared {
			add("snapshot", key)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, spec := range []struct{ kind, sql string }{
		{"submission", `SELECT c.id::text,v.schema,c.data FROM form_submissions c JOIN feature_versions v ON v.id=c.schema_version_id WHERE c.data::text LIKE '%'||$1||'%'`},
	} {
		rows, err := queryer.Query(ctx, spec.sql, target)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var key string
			var schema, data []byte
			if err := rows.Scan(&key, &schema, &data); err != nil {
				rows.Close()
				return nil, err
			}
			refs := newPackReferences(nil)
			refs.clearTarget = target
			if _, err := refs.rewriteSchemaValue(schema, data); err != nil {
				rows.Close()
				return nil, err
			}
			if refs.cleared && !seen[key] {
				add(spec.kind, key)
				seen[key] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	modelRefs, err := findModelFileReferences(ctx, queryer, target)
	if err != nil {
		return nil, err
	}
	out = append(out, modelRefs...)
	return out, nil
}

func modelRecordMediaSchema(model DataModel, entityID string) (json.RawMessage, bool, error) {
	var entity *DataModelEntity
	for i := range model.Entities {
		if model.Entities[i].ID == entityID {
			entity = &model.Entities[i]
			break
		}
	}
	if entity == nil {
		return nil, false, nil
	}
	properties := make(map[string]any)
	for _, field := range entity.Fields {
		if field.Type == "media" {
			properties[field.Key] = map[string]any{"type": "string", "format": "media"}
		}
	}
	if len(properties) == 0 {
		return nil, false, nil
	}
	raw, err := json.Marshal(map[string]any{"type": "object", "properties": properties})
	return raw, true, err
}

func rewriteModelRecordMedia(model DataModel, records []ModelContentRecord, refs *packReferences) ([]ModelContentRecord, bool, error) {
	changed := false
	for i := range records {
		schema, hasMedia, err := modelRecordMediaSchema(model, records[i].EntityID)
		if err != nil {
			return nil, false, err
		}
		if !hasMedia {
			continue
		}
		data, err := json.Marshal(records[i].Values)
		if err != nil {
			return nil, false, err
		}
		updated, err := refs.rewriteSchemaValue(schema, data)
		if err != nil {
			return nil, false, err
		}
		if bytes.Equal(data, updated) {
			continue
		}
		values := map[string]any{}
		decoder := json.NewDecoder(bytes.NewReader(updated))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return nil, false, err
		}
		records[i].Values = values
		changed = true
	}
	return records, changed, nil
}

func findModelFileReferences(ctx context.Context, queryer referenceQueryer, target string) ([]files.Reference, error) {
	out := []files.Reference{}
	add := func(kind, key string) {
		out = append(out, files.Reference{Kind: kind, ID: key, Description: fmt.Sprintf("%s: %s", kind, key)})
	}
	rows, err := queryer.Query(ctx, `SELECT w.snapshot_id::text,s.payload,w.revision,w.published_revision,w.draft_records,w.published_records FROM site_snapshot_model_workspaces w JOIN site_snapshots s ON s.id=w.snapshot_id WHERE w.draft_records::text LIKE '%'||$1||'%' OR w.published_records::text LIKE '%'||$1||'%'`, target)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var snapshotID string
		var payloadRaw, draftRaw, publishedRaw []byte
		var revision, publishedRevision int64
		if err := rows.Scan(&snapshotID, &payloadRaw, &revision, &publishedRevision, &draftRaw, &publishedRaw); err != nil {
			rows.Close()
			return nil, err
		}
		var payload SnapshotPayload
		var draft, published []ModelContentRecord
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			rows.Close()
			return nil, err
		}
		if payload.Config.DataModel == nil {
			continue
		}
		if err := json.Unmarshal(draftRaw, &draft); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(publishedRaw, &published); err != nil {
			rows.Close()
			return nil, err
		}
		refs := newPackReferences(nil)
		refs.clearTarget = target
		_, draftChanged, err := rewriteModelRecordMedia(*payload.Config.DataModel, draft, refs)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if draftChanged {
			add("model-content-draft", fmt.Sprintf("%s@%d", snapshotID, revision))
		}
		refs = newPackReferences(nil)
		refs.clearTarget = target
		_, publishedChanged, err := rewriteModelRecordMedia(*payload.Config.DataModel, published, refs)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if publishedChanged {
			add("model-content-published", fmt.Sprintf("%s@%d", snapshotID, publishedRevision))
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = queryer.Query(ctx, `SELECT h.id::text,s.payload,h.records FROM snapshot_model_content_publications h JOIN site_snapshots s ON s.id=h.snapshot_id WHERE h.records::text LIKE '%'||$1||'%'`, target)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var payloadRaw, recordsRaw []byte
		if err := rows.Scan(&id, &payloadRaw, &recordsRaw); err != nil {
			rows.Close()
			return nil, err
		}
		var payload SnapshotPayload
		var records []ModelContentRecord
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			rows.Close()
			return nil, err
		}
		if payload.Config.DataModel == nil {
			continue
		}
		if err := json.Unmarshal(recordsRaw, &records); err != nil {
			rows.Close()
			return nil, err
		}
		refs := newPackReferences(nil)
		refs.clearTarget = target
		_, changed, err := rewriteModelRecordMedia(*payload.Config.DataModel, records, refs)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if changed {
			add("model-content-history", id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return out, nil
}

// RemoveReferences clears only fields identified by their pinned schemas or
// explicit metadata/design asset fields. Plain strings that happen to equal a
// file UUID are deliberately preserved.
func (s *SnapshotService) RemoveReferences(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	target := id.String()
	if _, err := tx.Exec(ctx, `SELECT set_config('websiteengine.prune_file',$1,true)`, target); err != nil {
		return err
	}
	type payloadChange struct {
		id       uuid.UUID
		payload  SnapshotPayload
		original []byte
	}
	rows, err := tx.Query(ctx, `SELECT id,payload FROM site_snapshots WHERE payload::text LIKE '%'||$1||'%' ORDER BY id FOR UPDATE`, target)
	if err != nil {
		return err
	}
	var changes []payloadChange
	for rows.Next() {
		var item payloadChange
		var raw []byte
		if err := rows.Scan(&item.id, &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &item.payload); err != nil {
			rows.Close()
			return err
		}
		refs := newPackReferences(nil)
		refs.clearTarget = target
		index := sitepackIndex{Payload: item.payload}
		if err := refs.transform(&index); err != nil {
			rows.Close()
			return err
		}
		if refs.cleared {
			changes = append(changes, payloadChange{item.id, index.Payload, raw})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range changes {
		raw, err := json.Marshal(item.payload)
		if err != nil {
			return err
		}
		// Preserve the original JSON shape: omitempty on typed metadata would
		// otherwise drop cleared keys, which also violates snapshot immutability.
		var before, after any
		beforeDecoder := json.NewDecoder(bytes.NewReader(item.original))
		beforeDecoder.UseNumber()
		if err := beforeDecoder.Decode(&before); err != nil {
			return err
		}
		afterDecoder := json.NewDecoder(bytes.NewReader(raw))
		afterDecoder.UseNumber()
		if err := afterDecoder.Decode(&after); err != nil {
			return err
		}
		raw, err = json.Marshal(preserveClearedShape(before, after, target))
		if err != nil {
			return err
		}
		digest, err := SnapshotDigest(item.payload)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE site_snapshots SET payload=$2::jsonb,digest=$3,content_revision=content_revision+1 WHERE id=$1`, item.id, string(raw), digest); err != nil {
			return err
		}
	}
	// Form submissions retain the submission schema from their feature build.
	// Feature content records are no longer stored per build; their media is
	// traversed through the snapshot model workspace below.
	{
		sql := `SELECT c.ctid::text,NULL::uuid,v.schema,c.data FROM form_submissions c JOIN feature_versions v ON v.id=c.schema_version_id WHERE c.data::text LIKE '%'||$1||'%' FOR UPDATE OF c`
		rows, err := tx.Query(ctx, sql, target)
		if err != nil {
			return err
		}
		type rowChange struct {
			key        string
			snapshotID *uuid.UUID
			data       []byte
		}
		var updates []rowChange
		for rows.Next() {
			var item rowChange
			var schema []byte
			if err := rows.Scan(&item.key, &item.snapshotID, &schema, &item.data); err != nil {
				rows.Close()
				return err
			}
			refs := newPackReferences(nil)
			refs.clearTarget = target
			item.data, err = refs.rewriteSchemaValue(schema, item.data)
			if err != nil {
				rows.Close()
				return err
			}
			if refs.cleared {
				updates = append(updates, item)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range updates {
			if _, err := tx.Exec(ctx, `UPDATE form_submissions SET data=$2::jsonb,updated_at=now() WHERE ctid=$1::tid`, item.key, string(item.data)); err != nil {
				return err
			}
		}
	}
	if err := removeModelFileReferences(ctx, tx, target); err != nil {
		return err
	}
	return nil
}

func removeModelFileReferences(ctx context.Context, tx pgx.Tx, target string) error {
	rows, err := tx.Query(ctx, `SELECT w.snapshot_id,s.payload,w.revision,w.draft_records,w.published_records FROM site_snapshot_model_workspaces w JOIN site_snapshots s ON s.id=w.snapshot_id WHERE w.draft_records::text LIKE '%'||$1||'%' OR w.published_records::text LIKE '%'||$1||'%' ORDER BY w.snapshot_id FOR UPDATE OF w`, target)
	if err != nil {
		return err
	}
	type workspaceChange struct {
		snapshotID       uuid.UUID
		revision         int64
		draft            []ModelContentRecord
		published        []ModelContentRecord
		publishedChanged bool
	}
	var changes []workspaceChange
	for rows.Next() {
		var item workspaceChange
		var payloadRaw, draftRaw, publishedRaw []byte
		if err := rows.Scan(&item.snapshotID, &payloadRaw, &item.revision, &draftRaw, &publishedRaw); err != nil {
			rows.Close()
			return err
		}
		var payload SnapshotPayload
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			rows.Close()
			return err
		}
		if payload.Config.DataModel == nil {
			continue
		}
		if err := json.Unmarshal(draftRaw, &item.draft); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(publishedRaw, &item.published); err != nil {
			rows.Close()
			return err
		}
		refs := newPackReferences(nil)
		refs.clearTarget = target
		_, draftChanged, err := rewriteModelRecordMedia(*payload.Config.DataModel, item.draft, refs)
		if err != nil {
			rows.Close()
			return err
		}
		refs = newPackReferences(nil)
		refs.clearTarget = target
		_, publishedChanged, err := rewriteModelRecordMedia(*payload.Config.DataModel, item.published, refs)
		if err != nil {
			rows.Close()
			return err
		}
		if draftChanged || publishedChanged {
			item.publishedChanged = publishedChanged
			changes = append(changes, item)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range changes {
		draftRaw, err := json.Marshal(nonNilModelRecords(item.draft))
		if err != nil {
			return err
		}
		publishedRaw, err := json.Marshal(nonNilModelRecords(item.published))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE site_snapshot_model_workspaces SET revision=revision+1,draft_records=$2::jsonb,published_records=$3::jsonb,updated_at=now() WHERE snapshot_id=$1`, item.snapshotID, string(draftRaw), string(publishedRaw)); err != nil {
			return err
		}
		if item.publishedChanged {
			if _, err := tx.Exec(ctx, `UPDATE site_snapshots SET content_revision=content_revision+1 WHERE id=$1`, item.snapshotID); err != nil {
				return err
			}
		}
	}
	// Publication history is retained but media references to a file being
	// explicitly deleted are cleared from each immutable record snapshot.
	rows, err = tx.Query(ctx, `SELECT h.id,s.payload,h.records FROM snapshot_model_content_publications h JOIN site_snapshots s ON s.id=h.snapshot_id WHERE h.records::text LIKE '%'||$1||'%' ORDER BY h.id FOR UPDATE OF h`, target)
	if err != nil {
		return err
	}
	type historyChange struct {
		id      uuid.UUID
		records []ModelContentRecord
	}
	var history []historyChange
	for rows.Next() {
		var item historyChange
		var payloadRaw, recordsRaw []byte
		if err := rows.Scan(&item.id, &payloadRaw, &recordsRaw); err != nil {
			rows.Close()
			return err
		}
		var payload SnapshotPayload
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			rows.Close()
			return err
		}
		if payload.Config.DataModel == nil {
			continue
		}
		if err := json.Unmarshal(recordsRaw, &item.records); err != nil {
			rows.Close()
			return err
		}
		refs := newPackReferences(nil)
		refs.clearTarget = target
		_, changed, err := rewriteModelRecordMedia(*payload.Config.DataModel, item.records, refs)
		if err != nil {
			rows.Close()
			return err
		}
		if changed {
			history = append(history, item)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range history {
		raw, err := json.Marshal(nonNilModelRecords(item.records))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE snapshot_model_content_publications SET records=$2::jsonb WHERE id=$1`, item.id, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func preserveClearedShape(before, after any, target string) any {
	if value, ok := before.(string); ok && value == target {
		if next, present := after.(string); !present || next == "" {
			return ""
		}
		return before
	}
	switch old := before.(type) {
	case map[string]any:
		next, ok := after.(map[string]any)
		if !ok {
			return before
		}
		for key, value := range old {
			old[key] = preserveClearedShape(value, next[key], target)
		}
	case []any:
		next, ok := after.([]any)
		if !ok || len(old) != len(next) {
			return before
		}
		for i := range old {
			old[i] = preserveClearedShape(old[i], next[i], target)
		}
	}
	return before
}
