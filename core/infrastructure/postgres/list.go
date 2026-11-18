package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	"github.com/Zubrium/cdn/core/domain/access"
	storage "github.com/Zubrium/cdn/core/domain/files"
	"github.com/google/uuid"
)

func (r Objects) ListVisible(ctx context.Context, actor access.Principal, group, cursor string, limit int) ([]storage.Object, string, error) {
	return r.searchVisible(ctx, actor, "", group, cursor, "", limit)
}

func (r Objects) ListFileSet(ctx context.Context, actor access.Principal, setID, cursor string, limit int) ([]storage.Object, string, error) {
	return r.searchVisible(ctx, actor, "", "", cursor, setID, limit)
}

func (r Objects) SearchFiles(ctx context.Context, actor access.Principal, query, group, cursor, setID string, limit int) ([]storage.Object, string, error) {
	return r.searchVisible(ctx, actor, query, group, cursor, setID, limit)
}

func (r Objects) searchVisible(ctx context.Context, actor access.Principal, query, group, cursor, setID string, limit int) ([]storage.Object, string, error) {
	if r.Pool == nil {
		return nil, "", errors.New("postgres: nil pool")
	}
	if !actor.Active || actor.ID == "" || limit < 1 || limit > 1000 || len(query) > 256 || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		return nil, "", storage.ErrInvalidSearch
	}
	actorID, err := uuid.Parse(actor.ID)
	if err != nil || actorID == uuid.Nil {
		return nil, "", ErrUnauthorized
	}
	if group != "" && !appaccess.ValidGroupKey(group) {
		return nil, "", storage.ErrInvalidSearch
	}
	var cursorUUID uuid.UUID
	if cursor != "" {
		cursorUUID, err = uuid.Parse(cursor)
		if err != nil || cursorUUID == uuid.Nil {
			return nil, "", storage.ErrInvalidSearch
		}
	}
	var setUUID uuid.UUID
	if setID != "" {
		setUUID, err = uuid.Parse(setID)
		if err != nil || setUUID == uuid.Nil {
			return nil, "", storage.ErrInvalidSearch
		}
	}
	rows, err := r.Pool.Query(ctx, `SELECT o.id::text,o.object_key::text,o.metadata,o.content_type,o.size_bytes,o.sha256,o.revision,o.state,
		COALESCE(array_agg(fg.group_key ORDER BY fg.group_key) FILTER (WHERE fg.group_key IS NOT NULL AND EXISTS(
			SELECT 1 FROM access.groups visible_group JOIN access.grants visible_access
			ON visible_access.group_key=visible_group.group_key
			WHERE visible_group.group_key=fg.group_key AND visible_group.active
			AND visible_access.user_id=$1 AND visible_access.access_level>=1)),ARRAY[]::text[])
		FROM files.objects o
		LEFT JOIN files.memberships fg ON fg.object_id=o.id
		WHERE o.state='ready'
		AND EXISTS(SELECT 1 FROM access.users ap WHERE ap.id=$1 AND ap.active)
		AND ($3::uuid IS NULL OR o.id>$3)
		AND ($4='' OR strpos(lower(o.metadata::text),lower($4))>0)
		AND ($5::uuid IS NULL OR EXISTS(SELECT 1 FROM file_index.members sm WHERE sm.object_id=o.id AND sm.set_id=$5))
		AND ($5::uuid IS NULL OR EXISTS(SELECT 1 FROM file_index.sets s
			JOIN access.groups sg ON sg.group_key=s.management_group AND sg.active
			JOIN access.grants sga ON sga.group_key=sg.group_key AND sga.user_id=$1 AND sga.access_level>=1
			WHERE s.id=$5))
		AND EXISTS(SELECT 1 FROM files.memberships vf JOIN access.groups vg ON vg.group_key=vf.group_key AND vg.active
			JOIN access.grants vga ON vga.group_key=vf.group_key AND vga.user_id=$1 AND vga.access_level>=1
			WHERE vf.object_id=o.id AND ($2='' OR vf.group_key=$2))
		GROUP BY o.id ORDER BY o.id LIMIT $6`, actorID, group, nullableUUID(cursor, cursorUUID), query, nullableUUID(setID, setUUID), limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	objects := make([]storage.Object, 0, limit+1)
	for rows.Next() {
		var object storage.Object
		var rawMetadata, checksum []byte
		if err := rows.Scan(&object.ID, &object.Key, &rawMetadata, &object.ContentType, &object.Size, &checksum, &object.Revision, &object.State, &object.Groups); err != nil {
			return nil, "", err
		}
		if err := json.Unmarshal(rawMetadata, &object.Metadata); err != nil {
			return nil, "", err
		}
		if object.Metadata == nil {
			object.Metadata = map[string]string{}
		}
		object.SHA256 = hex.EncodeToString(checksum)
		object.Ready = true
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	// GROUP BY gives one row per file with all memberships. Hidden rows never
	// affect pagination because access filtering happens in PostgreSQL first.
	if len(objects) > limit {
		objects = objects[:limit]
		return objects, objects[len(objects)-1].ID, nil
	}
	return objects, "", nil
}

func nullableUUID(raw string, id uuid.UUID) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return id
}
