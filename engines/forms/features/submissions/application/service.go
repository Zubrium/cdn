package application

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/forms/features/submissions/model"
	"github.com/Zubrium/cdn/engines/forms/features/submissions/repository"
)

type Files interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
	DeleteFile(context.Context, core.Session, string, int64) error
}

type Service struct {
	repository *repository.Repository
	files      Files
	maxPage    int
}

type Versioned struct {
	Submission model.Submission
	Revision   int64
}

func New(repository *repository.Repository, files Files, maxPage int) (*Service, error) {
	if repository == nil || files == nil || maxPage < 1 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Service{repository: repository, files: files, maxPage: maxPage}, nil
}

func (service *Service) Get(ctx context.Context, snapshotID, id string) (model.Submission, int64, error) {
	if !validIdentifier(snapshotID) || !validIdentifier(id) {
		return model.Submission{}, 0, core.ErrModuleDataInvalid
	}
	entry, err := service.repository.Get(ctx, snapshotID, id)
	return entry.Submission, entry.Record.Revision, err
}

func (service *Service) List(ctx context.Context, snapshotID, formID, state, after string, limit int) ([]Versioned, string, error) {
	if !validIdentifier(snapshotID) || (formID != "" && !validIdentifier(formID)) ||
		(len(state) > 64 || !utf8.ValidString(state) || strings.ContainsRune(state, '\x00')) || limit < 1 || limit > service.maxPage {
		return nil, "", core.ErrModuleDataInvalid
	}
	entries, next, err := service.repository.List(ctx, snapshotID, formID, state, after, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]Versioned, len(entries))
	for i := range entries {
		items[i] = Versioned{Submission: entries[i].Submission, Revision: entries[i].Record.Revision}
	}
	return items, next, nil
}

func (service *Service) SetState(ctx context.Context, snapshotID, id, state string, expectedRevision int64) (model.Submission, int64, error) {
	if !validIdentifier(snapshotID) || !validIdentifier(id) || !validStatus(state) || expectedRevision < 1 {
		return model.Submission{}, 0, core.ErrModuleDataInvalid
	}
	entry, err := service.repository.SetState(ctx, snapshotID, id, state, expectedRevision)
	return entry.Submission, entry.Record.Revision, err
}

func (service *Service) Delete(ctx context.Context, session core.Session, snapshotID, id string, expectedRevision int64) error {
	if !validIdentifier(snapshotID) || !validIdentifier(id) || expectedRevision < 1 {
		return core.ErrModuleDataInvalid
	}
	entry, err := service.repository.BeginDelete(ctx, snapshotID, id, expectedRevision)
	if err != nil {
		return err
	}
	for _, fileID := range entry.Attachments {
		removed := false
		for attempt := 0; attempt < 3; attempt++ {
			file, err := service.files.StatFile(ctx, session, fileID)
			if errors.Is(err, core.ErrFileNotFound) {
				removed = true
				break
			}
			if err != nil {
				return err
			}
			err = service.files.DeleteFile(ctx, session, fileID, file.Revision)
			if err == nil || errors.Is(err, core.ErrFileNotFound) {
				removed = true
				break
			}
			if !errors.Is(err, core.ErrRevisionConflict) {
				return err
			}
		}
		if !removed {
			return core.ErrRevisionConflict
		}
	}
	return service.repository.FinishDelete(ctx, snapshotID, id, entry.Record.Revision)
}

func validStatus(value string) bool {
	return value != "" && len(value) <= 64 && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
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
