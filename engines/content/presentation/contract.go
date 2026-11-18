// Package presentation exposes Content's aggregate typed facade to the Admin
// API. The engine owns validation, drafts, publication, and revisions.
package presentation

import (
	"context"
	"errors"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("revision conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrTooLarge     = errors.New("request too large")
	ErrUnavailable  = errors.New("Content dependency unavailable")
)

type Contract interface {
	platform.Module
	API
}

type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListModels(context.Context, string, string, int) (ModelPage, error)
	GetModel(context.Context, string, string) (Model, error)
	SaveModel(context.Context, SaveModelRequest) (Model, error)
	ListRecords(context.Context, ListRecordsRequest) (RecordPage, error)
	SaveRecordDraft(context.Context, SaveRecordRequest) (Record, error)
	PublishRecord(context.Context, PublishRecordRequest) (Record, error)
	RestoreRecord(context.Context, RestoreRecordRequest) (Record, error)
	GetBindings(context.Context, string, string) (Bindings, error)
	SaveBindings(context.Context, SaveBindingsRequest) (Bindings, error)
}

type Model struct {
	ID          string       `json:"id"`
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Cardinality string       `json:"cardinality"`
	Fields      []ModelField `json:"fields"`
	Revision    int64        `json:"revision"`
}

type ModelField struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type ModelPage struct {
	Items      []Model `json:"items"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type SaveModelRequest struct {
	SnapshotID       string       `json:"snapshot_id"`
	ModelID          string       `json:"model_id,omitempty"`
	Key              string       `json:"key"`
	Name             string       `json:"name"`
	Cardinality      string       `json:"cardinality"`
	Fields           []ModelField `json:"fields"`
	ExpectedRevision int64        `json:"expected_revision"`
}

type Record struct {
	ID                string         `json:"id"`
	ModelID           string         `json:"model_id"`
	Values            map[string]any `json:"values"`
	Revision          int64          `json:"revision"`
	PublishedRevision int64          `json:"published_revision"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type ListRecordsRequest struct {
	SnapshotID string `json:"snapshot_id"`
	ModelID    string `json:"model_id"`
	Cursor     string `json:"cursor,omitempty"`
	Limit      int    `json:"limit"`
}

type RecordPage struct {
	Items      []Record `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type SaveRecordRequest struct {
	SnapshotID       string         `json:"snapshot_id"`
	ModelID          string         `json:"model_id"`
	RecordID         string         `json:"record_id,omitempty"`
	Values           map[string]any `json:"values"`
	ExpectedRevision int64          `json:"expected_revision"`
}

type PublishRecordRequest struct {
	SnapshotID       string `json:"snapshot_id"`
	RecordID         string `json:"record_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type RestoreRecordRequest struct {
	SnapshotID       string `json:"snapshot_id"`
	RecordID         string `json:"record_id"`
	HistoryID        string `json:"history_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type Bindings struct {
	SnapshotID string         `json:"snapshot_id"`
	FeatureID  string         `json:"feature_id"`
	Values     map[string]any `json:"values"`
	Revision   int64          `json:"revision"`
}

type SaveBindingsRequest struct {
	SnapshotID       string         `json:"snapshot_id"`
	FeatureID        string         `json:"feature_id"`
	Values           map[string]any `json:"values"`
	ExpectedRevision int64          `json:"expected_revision"`
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.content", Kind: "engine", Version: 1}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "model.list", Action: platform.AccessActionRead, ResourceType: "content-model"},
		{Name: "model.read", Action: platform.AccessActionRead, ResourceType: "content-model"},
		{Name: "model.write", Action: platform.AccessActionWrite, ResourceType: "content-model"},
		{Name: "record.list", Action: platform.AccessActionRead, ResourceType: "content-record"},
		{Name: "record.write", Action: platform.AccessActionWrite, ResourceType: "content-record"},
		{Name: "record.publish", Action: platform.AccessActionWrite, ResourceType: "content-record"},
		{Name: "record.restore", Action: platform.AccessActionWrite, ResourceType: "content-record"},
		{Name: "binding.read", Action: platform.AccessActionRead, ResourceType: "content-binding"},
		{Name: "binding.write", Action: platform.AccessActionWrite, ResourceType: "content-binding"},
	}
}

var _ platform.Module = Descriptor{}
