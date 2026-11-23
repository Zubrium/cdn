package application

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	core "github.com/Zubrium/cdn/core/presentation"
	api "github.com/Zubrium/cdn/engines/loader/presentation"
)

// CleanupOnce is bounded and safe to run concurrently on multiple Loader
// instances. Stage deletion and all CAS deletes are idempotent.
func (s *Service) CleanupOnce(ctx context.Context) error {
	limit := min(s.cfg.CleanupBatchSize, s.data.MaxPageSize())
	if limit < 1 {
		return api.ErrUnavailable
	}
	if err := s.markExpiredSessions(ctx, limit); err != nil {
		return err
	}
	if err := s.processCleanupJobs(ctx, limit); err != nil {
		return err
	}
	return s.deleteExpiredRecords(ctx, limit)
}

func (s *Service) RunCleanup(ctx context.Context) <-chan error {
	errorsOut := make(chan error, 1)
	go func() {
		defer close(errorsOut)
		ticker := time.NewTicker(s.cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			if err := s.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
				select {
				case errorsOut <- err:
				default:
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return errorsOut
}

func (s *Service) markExpiredSessions(ctx context.Context, limit int) error {
	cursor := ""
	remaining := limit
	for remaining > 0 {
		page := min(remaining, s.data.MaxPageSize())
		keys, records, next, err := s.data.ListExpired(ctx, "loader", "global", "global", cursor, page)
		if err != nil {
			return mapDataError(err)
		}
		for i, key := range keys {
			if !strings.HasPrefix(key.RecordKey, "session:") || records[i].PayloadVersion != 1 {
				continue
			}
			var upload storedUpload
			if json.Unmarshal(records[i].Payload, &upload) != nil || upload.ID == "" || key.RecordKey != sessionDataKey(upload.ID).RecordKey || !s.validUpload(upload, upload.ID, upload.PrincipalID) {
				return api.ErrUnavailable
			}
			if upload.State == "committed" || upload.State == "aborted" {
				continue
			}
			job, jobRecord, jobErr := s.getCleanup(ctx, upload.ID)
			if errors.Is(jobErr, api.ErrNotFound) {
				job = cleanupRecord{UploadID: upload.ID, PrincipalID: upload.PrincipalID, ExpectedParts: upload.ExpectedParts, State: "waiting"}
				if upload.State == "committing" {
					job.CommitAttempt = true
				}
				jobRecord = recorddata.Record{}
			} else if jobErr != nil {
				return jobErr
			}
			if job.PrincipalID != upload.PrincipalID || job.ExpectedParts != upload.ExpectedParts {
				return api.ErrUnavailable
			}
			if upload.State == "committing" {
				if !job.CommitAttempt {
					return api.ErrUnavailable
				}
				status, statusErr := s.files.ResolveUploadRequest(ctx, upload.PrincipalID, upload.ID)
				switch {
				case statusErr == nil && status.State == "pending":
					upload.ExpiresAt = s.now().UTC().Add(s.cfg.SessionTTL)
					if err := s.persistRecoveredSession(ctx, upload, 0); err != nil {
						return err
					}
				case statusErr == nil && status.State == "ready":
					if !validPrincipalID(status.FileID) {
						return api.ErrUnavailable
					}
					upload.State, upload.FileID = "committed", status.FileID
					upload.ExpiresAt = s.now().UTC().Add(s.cfg.SessionTTL)
					if err := s.persistRecoveredSession(ctx, upload, 0); err != nil {
						return err
					}
				case errors.Is(statusErr, core.ErrFileNotFound), statusErr == nil && (status.State == "failed" || status.State == "deleted"):
					upload.State = "aborted"
					upload.ExpiresAt = s.now().UTC().Add(s.cfg.SessionTTL)
					if err := s.persistRecoveredSession(ctx, upload, 0); err != nil {
						return err
					}
				case statusErr != nil:
					return mapCoreError(statusErr)
				default:
					return api.ErrUnavailable
				}
			}
			if job.State == "waiting" {
				job.State = "pending"
				value, valueErr := dataValue(job, uploadIndex("cleanup", upload.ID, "pending", upload.PrincipalID), 0)
				if valueErr != nil {
					return api.ErrUnavailable
				}
				if _, err := s.data.Put(ctx, cleanupDataKey(upload.ID), value, jobRecord.Revision); err != nil && !errors.Is(err, recorddata.ErrRecordConflict) {
					return mapDataError(err)
				}
			}
		}
		remaining -= len(keys)
		if next == "" || len(keys) == 0 {
			break
		}
		cursor = next
	}
	return nil
}

func (s *Service) processCleanupJobs(ctx context.Context, limit int) error {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	cursor := s.cleanupCursor
	remaining := limit
	selectors := []recorddata.RecordSelector{{Field: "kind", Operator: recorddata.RecordSelectorEqual, Values: []any{"cleanup"}},
		{Field: "state", Operator: recorddata.RecordSelectorEqual, Values: []any{"pending"}}}
	for remaining > 0 {
		pageSize := min(remaining, s.data.MaxPageSize())
		keys, records, next, err := s.data.List(ctx, "loader", "global", "global", cursor, selectors, pageSize)
		if err != nil {
			return mapDataError(err)
		}
		for i, record := range records {
			var job cleanupRecord
			if record.PayloadVersion != 1 || json.Unmarshal(record.Payload, &job) != nil || !validPrincipalID(job.PrincipalID) || !validPrincipalID(job.UploadID) || job.ExpectedParts < 0 || job.ExpectedParts > s.cfg.MaxParts || job.State != "pending" || keys[i].RecordKey != cleanupDataKey(job.UploadID).RecordKey {
				return api.ErrUnavailable
			}
			upload, sessionRecord, sessionErr := s.loadCleanupUpload(ctx, job.UploadID)
			if sessionErr != nil || upload.PrincipalID != job.PrincipalID || upload.ExpectedParts != job.ExpectedParts {
				return api.ErrUnavailable
			}
			if job.CommitAttempt {
				if upload.State != "committing" && upload.State != "committed" && upload.State != "aborted" {
					return api.ErrUnavailable
				}
			} else if upload.State != "aborted" && !(upload.State == "uploading" && sessionRecord.ExpiresAt != nil && !sessionRecord.ExpiresAt.After(s.now())) {
				return api.ErrUnavailable
			}
			if job.CommitAttempt {
				status, statusErr := s.files.ResolveUploadRequest(ctx, job.PrincipalID, job.UploadID)
				if statusErr != nil && !errors.Is(statusErr, core.ErrFileNotFound) {
					return mapCoreError(statusErr)
				}
				if statusErr == nil && status.State == "pending" {
					continue
				}
				if statusErr == nil && status.State != "ready" && status.State != "failed" && status.State != "deleted" {
					return api.ErrUnavailable
				}
				if statusErr == nil && status.State == "ready" {
					if err := s.recoverCommittedSession(ctx, job.UploadID, status.FileID); err != nil {
						return err
					}
				}
				if errors.Is(statusErr, core.ErrFileNotFound) || statusErr == nil && (status.State == "failed" || status.State == "deleted") {
					if err := s.recoverAbortedSession(ctx, job.UploadID); err != nil {
						return err
					}
				}
			}
			for number := 1; number <= job.ExpectedParts; number++ {
				if err := s.files.PurgeStagedObject(ctx, stageID(job.UploadID, number)); err != nil {
					return mapCoreError(err)
				}
			}
			if err := s.deleteUploadParts(ctx, job.UploadID, limit); err != nil {
				return err
			}
			if err := s.data.Delete(ctx, cleanupDataKey(job.UploadID), record.Revision); err != nil {
				if errors.Is(err, recorddata.ErrRecordNotFound) {
					continue
				}
				if errors.Is(err, recorddata.ErrRecordConflict) {
					if _, readErr := s.data.Get(ctx, cleanupDataKey(job.UploadID)); errors.Is(readErr, recorddata.ErrRecordNotFound) {
						continue
					} else if readErr != nil {
						return mapDataError(readErr)
					}
				}
				return mapDataError(err)
			}
		}
		remaining -= len(keys)
		if next == "" || len(keys) == 0 {
			s.cleanupCursor = ""
			return nil
		}
		cursor = next
		s.cleanupCursor = next
	}
	return nil
}

func (s *Service) deleteUploadParts(ctx context.Context, uploadID string, limit int) error {
	selectors := []recorddata.RecordSelector{{Field: "kind", Operator: recorddata.RecordSelectorEqual, Values: []any{"part"}},
		{Field: "upload_id", Operator: recorddata.RecordSelectorEqual, Values: []any{uploadID}}}
	cursor := ""
	for {
		keys, records, next, err := s.data.List(ctx, "loader", "global", "global", cursor, selectors, min(limit, s.data.MaxPageSize()))
		if err != nil {
			return mapDataError(err)
		}
		for i := range keys {
			if err := s.data.Delete(ctx, keys[i], records[i].Revision); err != nil && !errors.Is(err, recorddata.ErrRecordConflict) && !errors.Is(err, recorddata.ErrRecordNotFound) {
				return mapDataError(err)
			}
		}
		if next == "" || len(keys) == 0 {
			return nil
		}
		cursor = next
	}
}

func (s *Service) deleteExpiredRecords(ctx context.Context, limit int) error {
	cursor := ""
	remaining := limit
	for remaining > 0 {
		page := min(remaining, s.data.MaxPageSize())
		keys, records, next, err := s.data.ListExpired(ctx, "loader", "global", "global", cursor, page)
		if err != nil {
			return mapDataError(err)
		}
		for i := range keys {
			if err := s.data.Delete(ctx, keys[i], records[i].Revision); err != nil && !errors.Is(err, recorddata.ErrRecordConflict) && !errors.Is(err, recorddata.ErrRecordNotFound) {
				return mapDataError(err)
			}
		}
		remaining -= len(keys)
		if next == "" || len(keys) == 0 {
			break
		}
		cursor = next
	}
	return nil
}
