package files

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"strconv"
	"strings"
	"websiteengine/pkg/storage"
)

// ValidateReferencesTx verifies media presence without waiting for renditions.
// Callers coordinate with deletion using websiteengine_file_references lock.
func ValidateReferencesTx(ctx context.Context, tx pgx.Tx, schema, data json.RawMessage, requirePublic bool) error {
	var root, value any
	if e := json.Unmarshal(schema, &root); e != nil {
		return e
	}
	if e := json.Unmarshal(data, &value); e != nil {
		return e
	}
	var walk func(any, any) error
	walk = func(schema, value any) error {
		node, ok := schema.(map[string]any)
		if !ok {
			return nil
		}
		if node["format"] == "media" {
			text, ok := value.(string)
			if !ok || text == "" {
				return nil
			}
			id, e := uuid.Parse(text)
			if e != nil {
				return fmt.Errorf("%w: invalid media identifier", ErrInvalid)
			}
			var status, visibility string
			e = tx.QueryRow(ctx, `SELECT status,visibility FROM media_objects WHERE id=$1`, id).Scan(&status, &visibility)
			if e != nil || status != "ready" {
				return fmt.Errorf("%w: media %s is unavailable", ErrInvalid, id)
			}
			if requirePublic && visibility != "public" {
				return fmt.Errorf("%w: private media %s needs an authorized temporary link", ErrInvalid, id)
			}
			return nil
		}
		if object, ok := value.(map[string]any); ok {
			properties, _ := node["properties"].(map[string]any)
			for key, child := range properties {
				if e := walk(child, object[key]); e != nil {
					return e
				}
			}
		}
		if list, ok := value.([]any); ok {
			for _, item := range list {
				if e := walk(node["items"], item); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(root, value)
}

type Object struct {
	Key         string
	ContentType string
	Size        int64
}

func (s *Service) ResolveObject(ctx context.Context, f File, profile, variant string) (Object, error) {
	if profile == "" && (variant == "" || variant == "original") {
		return Object{f.ObjectKey, f.ContentType, f.Size}, nil
	}
	var key, mime string
	if profile != "" {
		if !validHash(profile) {
			return Object{}, ErrNotFound
		}
		e := s.pool.QueryRow(ctx, `SELECT outputs->$3->>'objectKey',outputs->$3->>'mimeType' FROM media_renditions WHERE media_id=$1 AND profile_hash=$2 AND status='ready'`, f.ID, profile, variant).Scan(&key, &mime)
		if e != nil {
			return Object{}, ErrNotFound
		}
	} else {
		e := s.pool.QueryRow(ctx, `SELECT variants->>$2 FROM media_objects WHERE id=$1`, f.ID, variant).Scan(&key)
		if e != nil {
			return Object{}, ErrNotFound
		}
	}
	if key == "" {
		return Object{}, ErrNotFound
	}
	if ranged, ok := s.store.(storage.RangeStore); ok {
		info, e := ranged.Stat(ctx, key)
		if e != nil {
			return Object{}, e
		}
		return Object{key, info.ContentType, info.Size}, nil
	}
	return Object{key, mime, -1}, nil
}
func (s *Service) Open(ctx context.Context, object Object, start, end int64) (io.ReadCloser, error) {
	if ranged, ok := s.store.(storage.RangeStore); ok && start >= 0 && end >= start {
		return ranged.GetRange(ctx, object.Key, start, end)
	}
	reader, e := s.store.Get(ctx, object.Key)
	if e != nil {
		return nil, e
	}
	if start > 0 {
		if _, e = io.CopyN(io.Discard, reader, start); e != nil {
			_ = reader.Close()
			return nil, e
		}
	}
	return reader, nil
}

// ParseRange supports a single byte range, including suffix/open ranges.
func ParseRange(raw string, size int64) (start, end int64, partial bool, err error) {
	if raw == "" {
		return 0, size - 1, false, nil
	}
	if size <= 0 || !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, false, ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false, ErrInvalid
	}
	if parts[0] == "" {
		suffix, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || suffix <= 0 {
			return 0, 0, false, ErrInvalid
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	}
	start, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || start < 0 || start >= size {
		return 0, 0, false, ErrInvalid
	}
	end = size - 1
	if parts[1] != "" {
		end, e = strconv.ParseInt(parts[1], 10, 64)
		if e != nil || end < start {
			return 0, 0, false, ErrInvalid
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, nil
}
func InlineSafe(contentType string) bool {
	switch strings.Split(contentType, ";")[0] {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif", "video/mp4", "video/webm", "audio/mpeg", "audio/ogg", "font/woff2", "font/woff", "application/pdf":
		return true
	}
	return false
}
func ConditionalMatch(r *http.Request, etag string) bool {
	// Commas are legal inside the quoted opaque-tag, so splitting the field
	// directly on commas can miss a matching validator. Split only at list
	// separators outside a quoted entity-tag.
	raw := r.Header.Get("If-None-Match")
	start := 0
	quoted := false
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				if entityTagMatches(raw[start:i], etag) {
					return true
				}
				start = i + 1
			}
		}
	}
	return entityTagMatches(raw[start:], etag)
}

func entityTagMatches(candidate, etag string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "*" {
		return true
	}
	if strings.HasPrefix(candidate, "W/") {
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "W/"))
	}
	return candidate == etag
}
