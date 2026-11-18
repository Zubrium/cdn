package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	core "github.com/Zubrium/cdn/core/presentation"
	"github.com/Zubrium/cdn/engines/forms/features/schemas/model"
	schemarepo "github.com/Zubrium/cdn/engines/forms/features/schemas/repository"
)

type Repository interface {
	GetByKey(context.Context, string, string) (model.Schema, core.ModuleDataRecord, error)
	GetByID(context.Context, string, string) (model.Schema, core.ModuleDataRecord, string, error)
	Create(context.Context, string, model.Schema) (int64, error)
	Update(context.Context, string, string, model.Schema, int64) (int64, error)
	List(context.Context, string, string, int) ([]schemarepo.Entry, string, error)
}

type Versioned struct {
	Schema   model.Schema
	Revision int64
}

type Service struct {
	repository Repository
	maxPage    int
}

func New(repository Repository, maxPage int) (*Service, error) {
	if repository == nil || maxPage < 1 {
		return nil, core.ErrModuleDataUnavailable
	}
	return &Service{repository: repository, maxPage: maxPage}, nil
}

func (service *Service) Get(ctx context.Context, snapshotID, schemaID string) (model.Schema, int64, error) {
	if !validIdentifier(snapshotID) || !validIdentifier(schemaID) {
		return model.Schema{}, 0, core.ErrModuleDataInvalid
	}
	schema, record, _, err := service.repository.GetByID(ctx, snapshotID, schemaID)
	return schema, record.Revision, err
}

func (service *Service) Save(ctx context.Context, snapshotID, schemaID, key string, fields map[string]any, expectedRevision int64) (model.Schema, int64, error) {
	if !validIdentifier(snapshotID) || !validText(key, 128) || fields == nil || expectedRevision < 0 ||
		(schemaID == "" && expectedRevision != 0) || (schemaID != "" && (expectedRevision == 0 || !validIdentifier(schemaID))) {
		return model.Schema{}, 0, core.ErrModuleDataInvalid
	}

	if schemaID == "" {
		if existing, record, err := service.repository.GetByKey(ctx, snapshotID, key); err == nil {
			if equalJSON(existing.Fields, fields) {
				return existing, record.Revision, nil
			}
			return model.Schema{}, 0, core.ErrModuleDataConflict
		} else if !errors.Is(err, core.ErrModuleDataNotFound) {
			return model.Schema{}, 0, err
		}
		id, err := newID()
		if err != nil {
			return model.Schema{}, 0, core.ErrModuleDataUnavailable
		}
		schema := model.Schema{ID: id, Key: key, Version: 1, Fields: fields}
		revision, err := service.repository.Create(ctx, snapshotID, schema)
		if errors.Is(err, core.ErrModuleDataConflict) {
			if existing, record, readErr := service.repository.GetByKey(ctx, snapshotID, key); readErr == nil && equalJSON(existing.Fields, fields) {
				return existing, record.Revision, nil
			}
		}
		return schema, revision, err
	}

	current, record, oldRecordKey, err := service.repository.GetByID(ctx, snapshotID, schemaID)
	if err != nil {
		return model.Schema{}, 0, err
	}
	if record.Revision != expectedRevision {
		return model.Schema{}, 0, core.ErrModuleDataConflict
	}
	if key != current.Key {
		if _, _, err := service.repository.GetByKey(ctx, snapshotID, key); err == nil {
			return model.Schema{}, 0, core.ErrModuleDataConflict
		} else if !errors.Is(err, core.ErrModuleDataNotFound) {
			return model.Schema{}, 0, err
		}
	} else if equalJSON(current.Fields, fields) {
		return current, record.Revision, nil
	}
	next := model.Schema{ID: current.ID, Key: key, Version: current.Version + 1, Fields: fields}
	revision, err := service.repository.Update(ctx, snapshotID, oldRecordKey, next, expectedRevision)
	if err != nil {
		return model.Schema{}, 0, err
	}
	return next, revision, nil
}

func equalJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func (service *Service) List(ctx context.Context, snapshotID, after string, limit int) ([]Versioned, string, error) {
	if !validIdentifier(snapshotID) || limit < 1 || limit > service.maxPage {
		return nil, "", core.ErrModuleDataInvalid
	}
	entries, next, err := service.repository.List(ctx, snapshotID, after, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]Versioned, len(entries))
	for i, entry := range entries {
		items[i] = Versioned{Schema: entry.Schema, Revision: entry.Record.Revision}
	}
	return items, next, nil
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

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}
