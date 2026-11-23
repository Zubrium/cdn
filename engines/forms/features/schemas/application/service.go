package application

import recorddata "github.com/Zubrium/cdn/pkg/contracts"

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Zubrium/cdn/engines/forms/features/schemas/model"
	"math"
	"strings"
	"unicode/utf8"
)

type Repository interface {
	GetByKey(context.Context, string) (model.StoredSchema, error)
	GetByID(context.Context, string) (model.StoredSchema, error)
	Create(context.Context, model.Schema) (int64, error)
	Update(context.Context, string, model.Schema, int64) (int64, error)
	List(context.Context, string, int) ([]model.StoredSchema, string, error)
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
		return nil, recorddata.ErrRecordUnavailable
	}
	return &Service{repository: repository, maxPage: maxPage}, nil
}

func (service *Service) Get(ctx context.Context, schemaID string) (model.Schema, int64, error) {
	if !validIdentifier(schemaID) {
		return model.Schema{}, 0, recorddata.ErrRecordInvalid
	}
	schema, err := service.repository.GetByID(ctx, schemaID)
	return schema.Schema, schema.Revision, err
}

func (service *Service) Save(ctx context.Context, schemaID, key string, fields map[string]any, expectedRevision int64) (model.Schema, int64, error) {
	if !validText(key, 128) || fields == nil || expectedRevision < 0 ||
		(schemaID == "" && expectedRevision != 0) || (schemaID != "" && (expectedRevision == 0 || !validIdentifier(schemaID))) {
		return model.Schema{}, 0, recorddata.ErrRecordInvalid
	}
	if _, err := model.ParseFieldDefinitions(fields); err != nil {
		return model.Schema{}, 0, recorddata.ErrRecordInvalid
	}

	if schemaID == "" {
		if existing, err := service.repository.GetByKey(ctx, key); err == nil {
			if equalJSON(existing.Schema.Fields, fields) {
				return existing.Schema, existing.Revision, nil
			}
			return model.Schema{}, 0, recorddata.ErrRecordConflict
		} else if !errors.Is(err, recorddata.ErrRecordNotFound) {
			return model.Schema{}, 0, err
		}
		id, err := newID()
		if err != nil {
			return model.Schema{}, 0, recorddata.ErrRecordUnavailable
		}
		schema := model.Schema{ID: id, Key: key, Version: 1, Fields: fields}
		revision, err := service.repository.Create(ctx, schema)
		if errors.Is(err, recorddata.ErrRecordConflict) {
			if existing, readErr := service.repository.GetByKey(ctx, key); readErr == nil && equalJSON(existing.Schema.Fields, fields) {
				return existing.Schema, existing.Revision, nil
			}
		}
		return schema, revision, err
	}

	current, err := service.repository.GetByID(ctx, schemaID)
	if err != nil {
		return model.Schema{}, 0, err
	}
	if current.Revision != expectedRevision {
		return model.Schema{}, 0, recorddata.ErrRecordConflict
	}
	if current.Schema.Version == math.MaxInt64 {
		return model.Schema{}, 0, recorddata.ErrRecordConflict
	}
	if key != current.Schema.Key {
		if _, err := service.repository.GetByKey(ctx, key); err == nil {
			return model.Schema{}, 0, recorddata.ErrRecordConflict
		} else if !errors.Is(err, recorddata.ErrRecordNotFound) {
			return model.Schema{}, 0, err
		}
	} else if equalJSON(current.Schema.Fields, fields) {
		return current.Schema, current.Revision, nil
	}
	next := model.Schema{ID: current.Schema.ID, Key: key, Version: current.Schema.Version + 1, Fields: fields}
	revision, err := service.repository.Update(ctx, current.RecordKey, next, expectedRevision)
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

func (service *Service) List(ctx context.Context, after string, limit int) ([]Versioned, string, error) {
	if limit < 1 || limit > service.maxPage {
		return nil, "", recorddata.ErrRecordInvalid
	}
	entries, next, err := service.repository.List(ctx, after, limit)
	if err != nil {
		return nil, "", err
	}
	items := make([]Versioned, len(entries))
	for i, entry := range entries {
		items[i] = Versioned{Schema: entry.Schema, Revision: entry.Revision}
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
