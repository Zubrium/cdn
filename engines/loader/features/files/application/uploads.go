package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	api "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/google/uuid"
)

func (s *Service) BeginUpload(ctx context.Context, input api.BeginUploadInput) (api.UploadSession, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.UploadSession{}, err
	}
	if !api.ValidIdempotencyKey(input.IdempotencyKey) || input.Size < 0 || input.Size > s.cfg.MaxFileBytes || len(input.Groups) == 0 || input.Name == "" || input.ContentType == "" || !validDigest(input.SHA256) {
		return api.UploadSession{}, api.ErrInvalid
	}
	idempotencyKey := input.IdempotencyKey
	seenGroups := make(map[string]struct{}, len(input.Groups))
	for _, group := range input.Groups {
		if !contracts.ValidAccessGroupKey(group) {
			return api.UploadSession{}, api.ErrInvalid
		}
		if _, exists := seenGroups[group]; exists {
			return api.UploadSession{}, api.ErrInvalid
		}
		seenGroups[group] = struct{}{}
	}
	if err := s.identity.RequireGroupLevel(ctx, request.subject, input.Groups, contracts.AccessLevelWrite); err != nil {
		return api.UploadSession{}, mapCoreError(err)
	}
	partCount := expectedPartCount(input.Size, s.cfg.PartSize)
	if partCount > s.cfg.MaxParts {
		return api.UploadSession{}, api.ErrTooLarge
	}
	input.SHA256 = strings.ToLower(input.SHA256)
	input.Groups = append([]string(nil), input.Groups...)
	sort.Strings(input.Groups)
	input.Attributes = clone(input.Attributes)
	digest, err := hashInput(input)
	if err != nil {
		return api.UploadSession{}, api.ErrInvalid
	}
	input.IdempotencyKey = ""
	key := idempotencyDataKey(request.principalID, idempotencyKey)
	if old, err := s.data.Get(ctx, key); err == nil {
		var stored idempotencyRecord
		if json.Unmarshal(old.Payload, &stored) != nil || stored.InputHash != digest {
			return api.UploadSession{}, api.ErrConflict
		}
		return s.uploadStatus(ctx, request, stored.SessionID)
	} else if !errors.Is(err, contracts.ErrRecordNotFound) {
		return api.UploadSession{}, mapDataError(err)
	}
	mode := s.chooseMode(input.Size)
	id, err := uuid.NewRandom()
	if err != nil {
		return api.UploadSession{}, api.ErrUnavailable
	}
	now := s.now().UTC()
	upload := storedUpload{ID: id.String(), PrincipalID: request.principalID, Input: input, InputHash: digest,
		IdempotencyKeyHash: sha256Hex(idempotencyKey), Mode: mode, State: "uploading", ExpectedParts: partCount,
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.SessionTTL)}
	idemRecord := idempotencyRecord{InputHash: digest, SessionID: upload.ID}
	sessionValue, err := dataValue(upload, sessionIndex(upload), s.cfg.SessionTTL)
	if err != nil {
		return api.UploadSession{}, api.ErrInvalid
	}
	idemValue, err := dataValue(idemRecord, uploadIndex("idempotency", upload.ID, "active", request.principalID), s.cfg.SessionTTL)
	if err != nil {
		return api.UploadSession{}, api.ErrInvalid
	}
	cleanup := cleanupRecord{UploadID: upload.ID, PrincipalID: request.principalID, ExpectedParts: partCount, State: "waiting"}
	cleanupValue, err := dataValue(cleanup, uploadIndex("cleanup", upload.ID, "waiting", request.principalID), 0)
	if err != nil {
		return api.UploadSession{}, api.ErrInvalid
	}
	if err := s.data.Batch(ctx, []contracts.RecordMutation{
		{Key: sessionDataKey(upload.ID), ExpectedRevision: 0, Value: &sessionValue},
		{Key: key, ExpectedRevision: 0, Value: &idemValue},
		{Key: cleanupDataKey(upload.ID), ExpectedRevision: 0, Value: &cleanupValue},
	}); err != nil {
		if errors.Is(err, contracts.ErrRecordConflict) {
			if old, readErr := s.data.Get(ctx, key); readErr == nil {
				var saved idempotencyRecord
				if json.Unmarshal(old.Payload, &saved) == nil && saved.InputHash == digest {
					return s.uploadStatus(ctx, request, saved.SessionID)
				}
			}
			return api.UploadSession{}, api.ErrConflict
		}
		return api.UploadSession{}, mapDataError(err)
	}
	return exposeUpload(upload, nil, s.cfg.PartSize), nil
}

func (s *Service) UploadStatus(ctx context.Context, id string) (api.UploadSession, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.UploadSession{}, err
	}
	return s.uploadStatus(ctx, request, id)
}

func (s *Service) uploadStatus(ctx context.Context, request authenticated, id string) (api.UploadSession, error) {
	upload, _, err := s.loadUpload(ctx, request, id)
	if err != nil {
		return api.UploadSession{}, err
	}
	parts, err := s.partsForUpload(ctx, upload.ID)
	if err != nil {
		return api.UploadSession{}, err
	}
	return exposeUpload(upload, parts, s.cfg.PartSize), nil
}

func (s *Service) PutUploadPart(ctx context.Context, id string, number int, size int64, checksum string, body io.Reader) (api.UploadSession, error) {
	request, err := s.request(ctx)
	if err != nil {
		return api.UploadSession{}, err
	}
	upload, record, err := s.loadUpload(ctx, request, id)
	if err != nil {
		return api.UploadSession{}, err
	}
	if upload.State != "uploading" || number < 1 || number > upload.ExpectedParts || size <= 0 || size > s.cfg.MaxPartBytes || body == nil || !validDigest(checksum) {
		return api.UploadSession{}, api.ErrInvalid
	}
	wantSize := min(s.cfg.PartSize, upload.Input.Size-int64(number-1)*s.cfg.PartSize)
	if wantSize <= 0 || size != wantSize || size > s.cfg.MaxPartBytes {
		return api.UploadSession{}, api.ErrInvalid
	}
	partKey := partDataKey(upload.ID, number)
	part, _, partErr := s.getPart(ctx, upload.ID, number)
	if partErr == nil {
		if part.Size != size || part.SHA256 != strings.ToLower(checksum) {
			return api.UploadSession{}, api.ErrConflict
		}
		if part.State == "ready" {
			parts, listErr := s.partsForUpload(ctx, upload.ID)
			if listErr != nil {
				return api.UploadSession{}, listErr
			}
			return exposeUpload(upload, parts, s.cfg.PartSize), nil
		}
		if part.State != "staging" {
			return api.UploadSession{}, api.ErrConflict
		}
	} else if !errors.Is(partErr, contracts.ErrRecordNotFound) {
		return api.UploadSession{}, partErr
	} else {
		part = storedPart{Number: number, Size: size, SHA256: strings.ToLower(checksum), Stage: stageID(upload.ID, number), State: "staging"}
		partValue, valueErr := dataValue(part, uploadIndex("part", upload.ID, "staging", upload.PrincipalID), 0)
		if valueErr != nil {
			return api.UploadSession{}, api.ErrInvalid
		}
		remaining, ttlErr := remainingTTL(upload.ExpiresAt, s.now())
		if ttlErr != nil {
			return api.UploadSession{}, api.ErrNotFound
		}
		uploadRecord, valueErr := dataValue(upload, sessionIndex(upload), remaining)
		if valueErr != nil {
			return api.UploadSession{}, api.ErrConflict
		}
		if err := s.data.Batch(ctx, []contracts.RecordMutation{
			{Key: sessionDataKey(upload.ID), ExpectedRevision: record.Revision, Value: &uploadRecord},
			{Key: partKey, ExpectedRevision: 0, Value: &partValue},
		}); err != nil {
			if errors.Is(err, contracts.ErrRecordConflict) {
				latest, latestRecord, loadErr := s.loadUpload(ctx, request, id)
				if loadErr == nil {
					part, _, partErr = s.getPart(ctx, latest.ID, number)
					if partErr == nil && part.Size == size && part.SHA256 == strings.ToLower(checksum) && part.State == "ready" {
						parts, listErr := s.partsForUpload(ctx, latest.ID)
						if listErr != nil {
							return api.UploadSession{}, listErr
						}
						return exposeUpload(latest, parts, s.cfg.PartSize), nil
					}
					if partErr == nil && part.State == "staging" {
						upload, record = latest, latestRecord
					}
				}
				if partErr != nil || part.State != "staging" {
					return api.UploadSession{}, api.ErrConflict
				}
			} else {
				return api.UploadSession{}, mapDataError(err)
			}
		} else {
			part, _, err = s.getPart(ctx, upload.ID, number)
			if err != nil {
				return api.UploadSession{}, err
			}
		}
	}
	if err := s.identity.RequireGroupLevel(ctx, request.subject, upload.Input.Groups, contracts.AccessLevelWrite); err != nil {
		return api.UploadSession{}, mapCoreError(err)
	}
	hash := sha256.New()
	counted := &countingReader{reader: io.LimitReader(body, size+1), hash: hash}
	if err := s.files.PutStagedObject(ctx, request.subject, part.Stage, upload.Input.Groups, size, part.SHA256, counted); err != nil {
		return api.UploadSession{}, mapCoreError(err)
	}
	// Core's idempotent staging replay may return an existing verified handle
	// without consuming this request body. If it consumed bytes, verify them.
	if counted.count != 0 && (counted.count != size || hex.EncodeToString(hash.Sum(nil)) != part.SHA256) {
		return api.UploadSession{}, api.ErrInvalid
	}
	readyUpload, err := s.markPartReady(ctx, request, upload.ID, number, size, part.SHA256)
	if err != nil {
		return api.UploadSession{}, err
	}
	parts, err := s.partsForUpload(ctx, readyUpload.ID)
	if err != nil {
		return api.UploadSession{}, err
	}
	return exposeUpload(readyUpload, parts, s.cfg.PartSize), nil
}

func (s *Service) markPartReady(ctx context.Context, request authenticated, uploadID string, number int, size int64, checksum string) (storedUpload, error) {
	upload, sessionRecord, err := s.loadUpload(ctx, request, uploadID)
	if err != nil {
		return storedUpload{}, err
	}
	if upload.State != "uploading" {
		return storedUpload{}, api.ErrConflict
	}
	part, partRecord, err := s.getPart(ctx, uploadID, number)
	if err != nil {
		return storedUpload{}, mapDataError(err)
	}
	if part.Size != size || part.SHA256 != checksum {
		return storedUpload{}, api.ErrConflict
	}
	if part.State == "ready" {
		return upload, nil
	}
	if part.State != "staging" {
		return storedUpload{}, api.ErrConflict
	}
	part.State = "ready"
	partValue, err := dataValue(part, uploadIndex("part", uploadID, "ready", upload.PrincipalID), 0)
	if err != nil {
		return storedUpload{}, api.ErrUnavailable
	}
	remaining, err := remainingTTL(upload.ExpiresAt, s.now())
	if err != nil {
		return storedUpload{}, err
	}
	sessionValue, err := dataValue(upload, sessionIndex(upload), remaining)
	if err != nil {
		return storedUpload{}, api.ErrUnavailable
	}
	if err := s.data.Batch(ctx, []contracts.RecordMutation{
		{Key: sessionDataKey(uploadID), ExpectedRevision: sessionRecord.Revision, Value: &sessionValue},
		{Key: partDataKey(uploadID, number), ExpectedRevision: partRecord.Revision, Value: &partValue},
	}); err != nil {
		if !errors.Is(err, contracts.ErrRecordConflict) {
			return storedUpload{}, mapDataError(err)
		}
		latestUpload, _, uploadErr := s.loadUpload(ctx, request, uploadID)
		latestPart, _, partErr := s.getPart(ctx, uploadID, number)
		if uploadErr == nil && partErr == nil && latestUpload.State == "uploading" && latestPart.State == "ready" &&
			latestPart.Size == size && latestPart.SHA256 == checksum {
			return latestUpload, nil
		}
		return storedUpload{}, api.ErrConflict
	}
	s.cache.remove(uploadID)
	return upload, nil
}

func (s *Service) CommitUpload(ctx context.Context, id string) (api.File, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, s.cfg.CommitTimeout)
	defer cancel()
	releaseCommit, err := s.acquireCommitSlot(ctx)
	if err != nil {
		return api.File{}, err
	}
	defer releaseCommit()
	request, err := s.request(ctx)
	if err != nil {
		return api.File{}, err
	}
	upload, record, err := s.loadUpload(ctx, request, id)
	if err != nil {
		return api.File{}, err
	}
	if upload.State == "committed" {
		file, err := s.files.StatFile(ctx, request.subject, upload.FileID)
		if err != nil {
			return api.File{}, mapCoreError(err)
		}
		return exposeFile(file), nil
	}
	if upload.State != "uploading" && upload.State != "committing" {
		return api.File{}, api.ErrConflict
	}
	if upload.State == "uploading" {
		parts, listErr := s.partsForUpload(ctx, upload.ID)
		if listErr != nil {
			return api.File{}, listErr
		}
		if err := validateParts(upload, parts, s.cfg.PartSize, s.cfg.MaxParts); err != nil {
			return api.File{}, err
		}
		upload.State = "committing"
		upload.ExpiresAt = s.now().UTC().Add(s.cfg.CommitTimeout)
		job, jobRecord, jobErr := s.getCleanup(ctx, upload.ID)
		if jobErr != nil {
			return api.File{}, jobErr
		}
		if job.PrincipalID != upload.PrincipalID || job.ExpectedParts != upload.ExpectedParts {
			return api.File{}, api.ErrUnavailable
		}
		job.CommitAttempt = true
		jobValue, valueErr := dataValue(job, uploadIndex("cleanup", upload.ID, job.State, upload.PrincipalID), 0)
		if valueErr != nil {
			return api.File{}, api.ErrUnavailable
		}
		remaining, ttlErr := remainingTTL(upload.ExpiresAt, s.now())
		if ttlErr != nil {
			return api.File{}, api.ErrUnavailable
		}
		sessionValue, valueErr := dataValue(upload, sessionIndex(upload), remaining)
		if valueErr != nil {
			return api.File{}, api.ErrUnavailable
		}
		idempotencyMutation, valueErr := s.idempotencyMutation(ctx, upload, remaining)
		if valueErr != nil {
			return api.File{}, valueErr
		}
		if err := s.data.Batch(ctx, []contracts.RecordMutation{
			{Key: sessionDataKey(upload.ID), ExpectedRevision: record.Revision, Value: &sessionValue},
			{Key: cleanupDataKey(upload.ID), ExpectedRevision: jobRecord.Revision, Value: &jobValue},
			idempotencyMutation,
		}); err != nil {
			return api.File{}, mapDataError(err)
		}
		upload, record, err = s.loadUpload(ctx, request, id)
		if err != nil {
			return api.File{}, err
		}
	}
	parts, err := s.partsForUpload(ctx, upload.ID)
	if err != nil {
		return api.File{}, err
	}
	if err := validateParts(upload, parts, s.cfg.PartSize, s.cfg.MaxParts); err != nil {
		return api.File{}, err
	}
	if err := s.identity.RequireGroupLevel(ctx, request.subject, upload.Input.Groups, contracts.AccessLevelWrite); err != nil {
		return api.File{}, mapCoreError(err)
	}
	fileBody, release, err := s.commitReader(ctx, request, upload, parts)
	if err != nil {
		return api.File{}, err
	}
	defer release()
	metadata := clone(upload.Input.Attributes)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["name"] = upload.Input.Name
	fileID, err := s.files.CreateFileVerified(ctx, request.subject, upload.ID, upload.Input.Groups, metadata, upload.Input.ContentType, upload.Input.Size, upload.Input.SHA256, fileBody)
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	file, err := s.files.StatFile(ctx, request.subject, fileID)
	if err != nil {
		return api.File{}, mapCoreError(err)
	}
	upload.FileID, upload.State = fileID, "committed"
	upload.ExpiresAt = s.now().UTC().Add(s.cfg.SessionTTL)
	job, jobRecord, err := s.getCleanup(ctx, upload.ID)
	if err != nil {
		return api.File{}, err
	}
	job.State = "pending"
	jobValue, err := dataValue(job, uploadIndex("cleanup", upload.ID, "pending", upload.PrincipalID), 0)
	if err != nil {
		return api.File{}, api.ErrUnavailable
	}
	remaining, ttlErr := remainingTTL(upload.ExpiresAt, s.now())
	if ttlErr != nil {
		return api.File{}, api.ErrUnavailable
	}
	sessionValue, err := dataValue(upload, sessionIndex(upload), remaining)
	if err != nil {
		return api.File{}, api.ErrUnavailable
	}
	idempotencyMutation, err := s.idempotencyMutation(ctx, upload, remaining)
	if err != nil {
		return api.File{}, err
	}
	if err := s.data.Batch(ctx, []contracts.RecordMutation{
		{Key: sessionDataKey(upload.ID), ExpectedRevision: record.Revision, Value: &sessionValue},
		{Key: cleanupDataKey(upload.ID), ExpectedRevision: jobRecord.Revision, Value: &jobValue},
		idempotencyMutation,
	}); err != nil {
		if errors.Is(err, contracts.ErrRecordConflict) {
			latest, _, readErr := s.loadUpload(ctx, request, id)
			if readErr == nil && latest.State == "committed" && latest.FileID == fileID {
				return exposeFile(file), nil
			}
			return api.File{}, api.ErrConflict
		}
		return api.File{}, mapDataError(err)
	}
	return exposeFile(file), nil
}

// acquireCommitSlot reserves a Loader commit's two-connection PostgreSQL
// working set: one connection streams the final upload while another reads
// each staged part. The listener has its own reserved connection in the same
// pool, so config validation matches the slot count to that capacity.
func (s *Service) acquireCommitSlot(ctx context.Context) (func(), error) {
	if s == nil || s.commitSlots == nil {
		return nil, api.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: commit capacity wait ended", api.ErrUnavailable)
	}
	select {
	case s.commitSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.commitSlots
			return nil, fmt.Errorf("%w: commit capacity wait ended", api.ErrUnavailable)
		}
		var once sync.Once
		return func() { once.Do(func() { <-s.commitSlots }) }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: commit capacity wait ended", api.ErrUnavailable)
	}
}

func (s *Service) AbortUpload(ctx context.Context, id string) error {
	request, err := s.request(ctx)
	if err != nil {
		return err
	}
	upload, record, err := s.loadUpload(ctx, request, id)
	if err != nil {
		return err
	}
	if upload.State == "aborted" {
		return nil
	}
	if upload.State == "committed" || upload.State == "committing" {
		return api.ErrConflict
	}
	upload.State = "aborted"
	job, jobRecord, err := s.getCleanup(ctx, upload.ID)
	if err != nil {
		return err
	}
	if job.PrincipalID != upload.PrincipalID || job.ExpectedParts != upload.ExpectedParts {
		return api.ErrUnavailable
	}
	job.State = "pending"
	jobValue, err := dataValue(job, uploadIndex("cleanup", upload.ID, "pending", upload.PrincipalID), 0)
	if err != nil {
		return api.ErrUnavailable
	}
	remaining, ttlErr := remainingTTL(upload.ExpiresAt, s.now())
	if ttlErr != nil {
		return api.ErrNotFound
	}
	sessionValue, err := dataValue(upload, sessionIndex(upload), remaining)
	if err != nil {
		return api.ErrConflict
	}
	if err := s.data.Batch(ctx, []contracts.RecordMutation{
		{Key: sessionDataKey(upload.ID), ExpectedRevision: record.Revision, Value: &sessionValue},
		{Key: cleanupDataKey(upload.ID), ExpectedRevision: jobRecord.Revision, Value: &jobValue},
	}); err != nil {
		if errors.Is(err, contracts.ErrRecordConflict) {
			latest, _, loadErr := s.loadUpload(ctx, request, id)
			if loadErr == nil && latest.State == "aborted" {
				return nil
			}
			return api.ErrConflict
		}
		return mapDataError(err)
	}
	return nil
}
