package application

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zubrium/cdn/engines/forms/features/submissions/model"
	submissionpkg "github.com/Zubrium/cdn/engines/forms/features/submissions/pkg"
)

type Files interface {
	StatFile(context.Context, recorddata.AuthorizationSubject, string) (recorddata.File, error)
	DeleteFile(context.Context, recorddata.AuthorizationSubject, string, int64) error
}

type Service struct {
	repository Repository
	files      Files
	maxPage    int
}

type Repository interface {
	Get(context.Context, string) (model.StoredSubmission, error)
	Create(context.Context, submissionpkg.PayloadV1) (model.StoredSubmission, error)
	List(context.Context, string, string, string, string, int) ([]model.StoredSubmission, string, error)
	SetState(context.Context, string, string, int64) (model.StoredSubmission, error)
	BeginDelete(context.Context, string, int64) (model.StoredSubmission, error)
	FinishDelete(context.Context, string, int64) error
}

type Versioned struct {
	Submission model.Submission
	Revision   int64
}

type CreateInput struct {
	ID                     string
	FormID                 string
	FormKey                string
	SchemaVersion          int64
	Values                 map[string]any
	Attachments            []string
	IdempotencyKeyHash     string
	IdempotencyFingerprint string
	CreatedAt              time.Time
}

func New(repository Repository, files Files, maxPage int) (*Service, error) {
	if repository == nil || files == nil || maxPage < 1 {
		return nil, recorddata.ErrRecordUnavailable
	}
	return &Service{repository: repository, files: files, maxPage: maxPage}, nil
}

func (service *Service) Get(ctx context.Context, id string) (model.Submission, int64, error) {
	if !validIdentifier(id) {
		return model.Submission{}, 0, recorddata.ErrRecordInvalid
	}
	entry, err := service.repository.Get(ctx, id)
	return entry.Submission, entry.Revision, err
}

func (service *Service) CreateAccepted(ctx context.Context, input CreateInput) (model.Submission, int64, error) {
	if !validIdentifier(input.ID) || !validIdentifier(input.FormID) || input.SchemaVersion < 1 ||
		input.Values == nil || input.IdempotencyKeyHash == "" || input.IdempotencyFingerprint == "" || input.CreatedAt.IsZero() {
		return model.Submission{}, 0, recorddata.ErrRecordInvalid
	}
	seen := make(map[string]struct{}, len(input.Attachments))
	for _, id := range input.Attachments {
		if !validIdentifier(id) {
			return model.Submission{}, 0, recorddata.ErrRecordInvalid
		}
		if _, exists := seen[id]; exists {
			return model.Submission{}, 0, recorddata.ErrRecordInvalid
		}
		seen[id] = struct{}{}
	}
	stored, err := service.repository.Create(ctx, submissionpkg.PayloadV1{
		ID: input.ID, FormID: input.FormID, FormKey: input.FormKey, SchemaVersion: input.SchemaVersion, State: "new",
		Values: input.Values, CreatedAt: input.CreatedAt.UTC(), IdempotencyKeyHash: input.IdempotencyKeyHash,
		IdempotencyFingerprint: input.IdempotencyFingerprint, AttachmentIDs: append([]string(nil), input.Attachments...),
	})
	if err != nil {
		return model.Submission{}, 0, err
	}
	return stored.Submission, stored.Revision, nil
}

func (service *Service) List(ctx context.Context, formID, formKey, state, after string, limit int) ([]Versioned, string, error) {
	if !validIdentifier(formID) || (formKey != "" && !validText(formKey, 128)) ||
		(state != "" && !validStatus(state)) || limit < 1 || limit > service.maxPage {
		return nil, "", recorddata.ErrRecordInvalid
	}
	entries, next, err := service.repository.List(ctx, formID, formKey, state, after, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]Versioned, len(entries))
	for i := range entries {
		items[i] = Versioned{Submission: entries[i].Submission, Revision: entries[i].Revision}
	}
	return items, next, nil
}

func (service *Service) SetState(ctx context.Context, id, state string, expectedRevision int64) (model.Submission, int64, error) {
	if !validIdentifier(id) || !validStatus(state) || expectedRevision < 1 {
		return model.Submission{}, 0, recorddata.ErrRecordInvalid
	}
	entry, err := service.repository.SetState(ctx, id, state, expectedRevision)
	return entry.Submission, entry.Revision, err
}

func (service *Service) Delete(ctx context.Context, session recorddata.AuthorizationSubject, id string, expectedRevision int64) error {
	if !validIdentifier(id) || expectedRevision < 1 {
		return recorddata.ErrRecordInvalid
	}
	entry, err := service.repository.BeginDelete(ctx, id, expectedRevision)
	if err != nil {
		return err
	}
	for _, fileID := range entry.Attachments {
		removed := false
		for attempt := 0; attempt < 3; attempt++ {
			file, err := service.files.StatFile(ctx, session, fileID)
			if errors.Is(err, recorddata.ErrFileNotFound) {
				removed = true
				break
			}
			if err != nil {
				return err
			}
			err = service.files.DeleteFile(ctx, session, fileID, file.Revision)
			if err == nil || errors.Is(err, recorddata.ErrFileNotFound) {
				removed = true
				break
			}
			if !errors.Is(err, recorddata.ErrFileRevisionConflict) {
				return err
			}
		}
		if !removed {
			return recorddata.ErrFileRevisionConflict
		}
	}
	return service.repository.FinishDelete(ctx, id, entry.Revision)
}

func validStatus(value string) bool {
	return value != "" && len(value) <= 64 && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_.-", char) {
			continue
		}
		return false
	}
	return true
}
