package application

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	api "github.com/Zubrium/cdn/engines/loader/presentation"
	"github.com/google/uuid"
)

func (s *Service) request(ctx context.Context) (authenticated, error) {
	request, ok := ctx.Value(authKey{}).(authenticated)
	if !ok || request.principalID == "" {
		return authenticated{}, api.ErrUnauthorized
	}
	return request, nil
}

func (s *Service) loadUpload(ctx context.Context, request authenticated, id string) (storedUpload, recorddata.Record, error) {
	if _, err := uuid.Parse(id); err != nil {
		return storedUpload{}, recorddata.Record{}, api.ErrNotFound
	}
	record, err := s.data.Get(ctx, sessionDataKey(id))
	if err != nil {
		return storedUpload{}, recorddata.Record{}, mapDataError(err)
	}
	if record.PayloadVersion != 1 {
		return storedUpload{}, recorddata.Record{}, api.ErrUnavailable
	}
	if cached, ok := s.cache.get(id, request.principalID, record.Revision, s.now()); ok {
		if !s.now().Before(cached.ExpiresAt) {
			s.cache.remove(id)
			return storedUpload{}, recorddata.Record{}, api.ErrNotFound
		}
		return cached, record, nil
	}
	var upload storedUpload
	if json.Unmarshal(record.Payload, &upload) != nil || !s.validUpload(upload, id, request.principalID) {
		return storedUpload{}, recorddata.Record{}, api.ErrNotFound
	}
	if !s.now().Before(upload.ExpiresAt) {
		s.cache.remove(id)
		return storedUpload{}, recorddata.Record{}, api.ErrNotFound
	}
	s.cache.put(id, request.principalID, upload, record.Revision, int64(len(record.Payload)), s.now())
	return upload, record, nil
}

func (s *Service) chooseMode(size int64) string {
	switch s.cfg.Mode {
	case ModeStream:
		return ModeStream
	case ModeBuffered:
		if size <= s.cfg.MaxBufferedBytes {
			return ModeBuffered
		}
		return ModeStream
	default:
		if s.cfg.BufferThreshold > 0 && size <= s.cfg.BufferThreshold && size <= s.cfg.MaxBufferedBytes {
			return ModeBuffered
		}
		return ModeStream
	}
}

func (s *Service) commitReader(ctx context.Context, request authenticated, upload storedUpload, parts []storedPart) (io.Reader, func(), error) {
	if upload.Mode != ModeBuffered || upload.Input.Size > s.cfg.MaxBufferedBytes {
		return s.concatReader(ctx, request, parts), func() {}, nil
	}
	release, ok := s.reserveBuffer(upload.Input.Size)
	if !ok {
		// Buffer pressure affects performance only. Large or concurrent uploads
		// continue through the bounded streaming path.
		return s.concatReader(ctx, request, parts), func() {}, nil
	}
	reader := s.concatReader(ctx, request, parts)
	data, readErr := io.ReadAll(io.LimitReader(reader, upload.Input.Size+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) != upload.Input.Size || sha256HexBytes(data) != upload.Input.SHA256 {
		release()
		return nil, func() {}, api.ErrInvalid
	}
	return bytes.NewReader(data), release, nil
}

func (s *Service) reserveBuffer(size int64) (func(), bool) {
	if size < 0 || size > s.cfg.MaxBufferedBytes {
		return nil, false
	}
	s.bufferMu.Lock()
	if size > s.cfg.MaxBufferedBytes-s.buffered {
		s.bufferMu.Unlock()
		return nil, false
	}
	s.buffered += size
	s.bufferMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.bufferMu.Lock()
			s.buffered -= size
			s.bufferMu.Unlock()
		})
	}, true
}

func (s *Service) concatReader(ctx context.Context, request authenticated, parts []storedPart) *stagedReader {
	return &stagedReader{ctx: ctx, files: s.files, session: request.session, parts: parts}
}

func exposeFile(file core.File) api.File {
	name := file.Metadata["name"]
	attributes := clone(file.Metadata)
	delete(attributes, "name")
	if attributes == nil {
		attributes = map[string]string{}
	}
	return api.File{ID: file.ID, Name: name, ContentType: file.ContentType, Size: file.Size, SHA256: file.SHA256,
		Revision: file.Revision, Groups: append([]string(nil), file.Groups...), Attributes: attributes}
}

func moduleKey(record string) recorddata.RecordKey {
	return recorddata.RecordKey{Namespace: "loader", ScopeType: "global", ScopeID: "global", RecordKey: record}
}

func sessionDataKey(id string) recorddata.RecordKey { return moduleKey("session:" + id) }
func cleanupDataKey(id string) recorddata.RecordKey { return moduleKey("cleanup:" + id) }
func partDataKey(id string, number int) recorddata.RecordKey {
	return moduleKey(fmt.Sprintf("part:%s:%06d", id, number))
}
func idempotencyDataKey(principalID, key string) recorddata.RecordKey {
	return idempotencyDataKeyHash(principalID, sha256Hex(key))
}

func idempotencyDataKeyHash(principalID, keyHash string) recorddata.RecordKey {
	return moduleKey("idempotency:" + sha256Hex(principalID) + ":" + keyHash)
}

func dataValue(value, index any, ttl time.Duration) (recorddata.RecordValue, error) {
	if ttl < 0 {
		return recorddata.RecordValue{}, errors.New("negative module data TTL")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return recorddata.RecordValue{}, err
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		return recorddata.RecordValue{}, err
	}
	return recorddata.RecordValue{Payload: payload, PayloadVersion: 1, IndexData: indexData, TTL: ttl}, nil
}

func sessionIndex(upload storedUpload) map[string]string {
	return uploadIndex("session", upload.ID, upload.State, upload.PrincipalID)
}

func uploadIndex(kind, uploadID, state, principalID string) map[string]string {
	return map[string]string{"kind": kind, "upload_id": uploadID, "state": state, "principal_id": principalID}
}

func (s *Service) validUpload(upload storedUpload, id, principalID string) bool {
	if upload.ID != id || upload.PrincipalID != principalID || !validDigest(upload.InputHash) || !validDigest(upload.IdempotencyKeyHash) || upload.Input.Size < 0 || upload.ExpectedParts < 0 ||
		upload.Input.Size > s.cfg.MaxFileBytes || upload.ExpectedParts > s.cfg.MaxParts ||
		upload.ExpectedParts != expectedPartCount(upload.Input.Size, s.cfg.PartSize) || !validDigest(upload.Input.SHA256) ||
		upload.CreatedAt.IsZero() || upload.ExpiresAt.IsZero() || !upload.ExpiresAt.After(upload.CreatedAt) {
		return false
	}
	if upload.Mode != ModeStream && upload.Mode != ModeBuffered || len(upload.Input.Groups) == 0 || upload.Input.IdempotencyKey != "" {
		return false
	}
	inputHash, err := hashInput(upload.Input)
	if err != nil || inputHash != upload.InputHash || upload.Input.Name == "" || upload.Input.ContentType == "" {
		return false
	}
	if !validPrincipalID(upload.PrincipalID) {
		return false
	}
	for i, group := range upload.Input.Groups {
		if !core.ValidAccessGroupKey(group) || (i > 0 && upload.Input.Groups[i-1] >= group) {
			return false
		}
	}
	switch upload.State {
	case "uploading", "committing", "aborted":
		return upload.FileID == ""
	case "committed":
		return validPrincipalID(upload.FileID)
	default:
		return false
	}
}

func validPrincipalID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

func remainingTTL(expiresAt, now time.Time) (time.Duration, error) {
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0, api.ErrNotFound
	}
	return remaining, nil
}
