package application

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"context"
	"encoding/json"
	"errors"
	api "github.com/Zubrium/cdn/engines/loader/presentation"
	"sort"
	"time"
)

// persistRecoveredSession keeps the upload result and idempotency mapping in
// sync while Loader resolves a Core commit whose response was lost or delayed.
// A zero expected revision replaces an expired record through Core CAS.
func (s *Service) persistRecoveredSession(ctx context.Context, upload storedUpload, expectedRevision int64) error {
	remaining, err := remainingTTL(upload.ExpiresAt, s.now())
	if err != nil {
		return err
	}
	sessionValue, err := dataValue(upload, sessionIndex(upload), remaining)
	if err != nil {
		return api.ErrUnavailable
	}
	idempotencyMutation, err := s.idempotencyMutation(ctx, upload, remaining)
	if err != nil {
		return err
	}
	if err := s.data.Batch(ctx, []recorddata.RecordMutation{
		{Key: sessionDataKey(upload.ID), ExpectedRevision: expectedRevision, Value: &sessionValue},
		idempotencyMutation,
	}); err != nil {
		if errors.Is(err, recorddata.ErrRecordConflict) {
			current, readErr := s.data.Get(ctx, sessionDataKey(upload.ID))
			if readErr == nil {
				var latest storedUpload
				if json.Unmarshal(current.Payload, &latest) == nil && s.validUpload(latest, upload.ID, upload.PrincipalID) &&
					latest.State == upload.State && latest.FileID == upload.FileID && latest.ExpiresAt.After(s.now()) {
					return nil
				}
			}
			return api.ErrConflict
		}
		return mapDataError(err)
	}
	s.cache.remove(upload.ID)
	return nil
}

func (s *Service) idempotencyMutation(ctx context.Context, upload storedUpload, ttl time.Duration) (recorddata.RecordMutation, error) {
	key := idempotencyDataKeyHash(upload.PrincipalID, upload.IdempotencyKeyHash)
	expectedRevision := int64(0)
	if current, err := s.data.Get(ctx, key); err == nil {
		var existing idempotencyRecord
		if current.PayloadVersion != 1 || json.Unmarshal(current.Payload, &existing) != nil || existing.SessionID != upload.ID || existing.InputHash != upload.InputHash {
			return recorddata.RecordMutation{}, api.ErrUnavailable
		}
		expectedRevision = current.Revision
	} else if !errors.Is(err, recorddata.ErrRecordNotFound) {
		return recorddata.RecordMutation{}, mapDataError(err)
	}
	value, err := dataValue(idempotencyRecord{InputHash: upload.InputHash, SessionID: upload.ID},
		uploadIndex("idempotency", upload.ID, "active", upload.PrincipalID), ttl)
	if err != nil {
		return recorddata.RecordMutation{}, api.ErrUnavailable
	}
	return recorddata.RecordMutation{Key: key, ExpectedRevision: expectedRevision, Value: &value}, nil
}

func (s *Service) recoverCommittedSession(ctx context.Context, uploadID, fileID string) error {
	if !validPrincipalID(fileID) {
		return api.ErrUnavailable
	}
	key := sessionDataKey(uploadID)
	record, err := s.data.Get(ctx, key)
	expectedRevision := int64(0)
	if errors.Is(err, recorddata.ErrRecordNotFound) {
		record, err = s.data.GetExpired(ctx, key)
	} else if err == nil {
		expectedRevision = record.Revision
	}
	if err != nil {
		return mapDataError(err)
	}
	var upload storedUpload
	if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &upload) != nil || !s.validUpload(upload, uploadID, upload.PrincipalID) {
		return api.ErrUnavailable
	}
	if upload.State == "committed" {
		if upload.FileID == fileID {
			return nil
		}
		return api.ErrUnavailable
	}
	if upload.State != "committing" {
		return api.ErrUnavailable
	}
	if record.ExpiresAt == nil || !record.ExpiresAt.After(s.now()) {
		expectedRevision = 0
	}
	upload.State, upload.FileID = "committed", fileID
	upload.ExpiresAt = s.now().UTC().Add(s.cfg.SessionTTL)
	return s.persistRecoveredSession(ctx, upload, expectedRevision)
}

func (s *Service) recoverAbortedSession(ctx context.Context, uploadID string) error {
	key := sessionDataKey(uploadID)
	record, err := s.data.Get(ctx, key)
	expectedRevision := int64(0)
	if errors.Is(err, recorddata.ErrRecordNotFound) {
		record, err = s.data.GetExpired(ctx, key)
	} else if err == nil {
		expectedRevision = record.Revision
	}
	if err != nil {
		return mapDataError(err)
	}
	var upload storedUpload
	if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &upload) != nil || !s.validUpload(upload, uploadID, upload.PrincipalID) {
		return api.ErrUnavailable
	}
	if upload.State == "aborted" {
		return nil
	}
	if upload.State == "committed" {
		return api.ErrUnavailable
	}
	if record.ExpiresAt == nil || !record.ExpiresAt.After(s.now()) {
		expectedRevision = 0
	}
	upload.State, upload.FileID = "aborted", ""
	upload.ExpiresAt = s.now().UTC().Add(s.cfg.SessionTTL)
	return s.persistRecoveredSession(ctx, upload, expectedRevision)
}

func (s *Service) getPart(ctx context.Context, uploadID string, number int) (storedPart, recorddata.Record, error) {
	record, err := s.data.Get(ctx, partDataKey(uploadID, number))
	if err != nil {
		return storedPart{}, recorddata.Record{}, err
	}
	var part storedPart
	if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &part) != nil || part.Number != number || part.Number > s.cfg.MaxParts || part.Size <= 0 || part.Size > s.cfg.MaxPartBytes ||
		part.Stage != stageID(uploadID, number) || !validDigest(part.SHA256) || (part.State != "staging" && part.State != "ready") {
		return storedPart{}, recorddata.Record{}, api.ErrUnavailable
	}
	return part, record, nil
}

func (s *Service) partsForUpload(ctx context.Context, uploadID string) ([]storedPart, error) {
	pageSize := min(s.data.MaxPageSize(), s.cfg.MaxParts)
	if pageSize < 1 {
		return nil, api.ErrUnavailable
	}
	selectors := []recorddata.RecordSelector{{Field: "kind", Operator: recorddata.RecordSelectorEqual, Values: []any{"part"}},
		{Field: "upload_id", Operator: recorddata.RecordSelectorEqual, Values: []any{uploadID}}}
	cursor := ""
	parts := make([]storedPart, 0, s.cfg.MaxParts)
	for len(parts) <= s.cfg.MaxParts {
		keys, records, next, err := s.data.List(ctx, "loader", "global", "global", cursor, selectors, pageSize)
		if err != nil {
			return nil, mapDataError(err)
		}
		for i, record := range records {
			var part storedPart
			if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &part) != nil || part.Number < 1 || part.Number > s.cfg.MaxParts || part.Size <= 0 || part.Size > s.cfg.MaxPartBytes ||
				part.Stage != stageID(uploadID, part.Number) || !validDigest(part.SHA256) || (part.State != "staging" && part.State != "ready") {
				return nil, api.ErrUnavailable
			}
			if keys[i].RecordKey != partDataKey(uploadID, part.Number).RecordKey {
				return nil, api.ErrUnavailable
			}
			parts = append(parts, part)
			if len(parts) > s.cfg.MaxParts {
				return nil, api.ErrUnavailable
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	return parts, nil
}

func validateParts(upload storedUpload, parts []storedPart, partSize int64, maxParts int) error {
	count := expectedPartCount(upload.Input.Size, partSize)
	if count > maxParts || count != upload.ExpectedParts || len(parts) != count {
		return api.ErrConflict
	}
	var total int64
	for i, part := range parts {
		number := i + 1
		wantSize := min(partSize, upload.Input.Size-int64(number-1)*partSize)
		if part.Number != number || part.State != "ready" || part.Size != wantSize || !validDigest(part.SHA256) || part.Stage != stageID(upload.ID, number) {
			return api.ErrConflict
		}
		total += part.Size
	}
	if total != upload.Input.Size {
		return api.ErrConflict
	}
	return nil
}

func expectedPartCount(size, partSize int64) int {
	if size == 0 || partSize <= 0 {
		return 0
	}
	count := 1 + (size-1)/partSize
	maxInt := int64(^uint(0) >> 1)
	if count > maxInt {
		return int(maxInt)
	}
	return int(count)
}

func (s *Service) getCleanup(ctx context.Context, uploadID string) (cleanupRecord, recorddata.Record, error) {
	record, err := s.data.Get(ctx, cleanupDataKey(uploadID))
	if err != nil {
		return cleanupRecord{}, recorddata.Record{}, mapDataError(err)
	}
	var job cleanupRecord
	if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &job) != nil || job.UploadID != uploadID || job.ExpectedParts < 0 || job.ExpectedParts > s.cfg.MaxParts ||
		!validPrincipalID(job.PrincipalID) || (job.State != "waiting" && job.State != "pending") {
		return cleanupRecord{}, recorddata.Record{}, api.ErrUnavailable
	}
	return job, record, nil
}

func (s *Service) loadCleanupUpload(ctx context.Context, uploadID string) (storedUpload, recorddata.Record, error) {
	key := sessionDataKey(uploadID)
	record, err := s.data.Get(ctx, key)
	if errors.Is(err, recorddata.ErrRecordNotFound) {
		record, err = s.data.GetExpired(ctx, key)
	}
	if err != nil {
		return storedUpload{}, recorddata.Record{}, mapDataError(err)
	}
	var upload storedUpload
	if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &upload) != nil || !s.validUpload(upload, uploadID, upload.PrincipalID) {
		return storedUpload{}, recorddata.Record{}, api.ErrUnavailable
	}
	return upload, record, nil
}
