// Package presentation exposes Forms' aggregate typed facade to the Admin API.
// Form lifecycle rules remain owned by the Forms engine.
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
	ErrUnavailable  = errors.New("Forms dependency unavailable")
)

type Contract interface {
	platform.Module
	API
}

type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListSchemas(context.Context, string, string, int) (SchemaPage, error)
	GetSchema(context.Context, string, string) (Schema, error)
	SaveSchema(context.Context, SaveSchemaRequest) (Schema, error)
	ListSubmissions(context.Context, ListSubmissionsRequest) (SubmissionPage, error)
	GetSubmission(context.Context, string, string) (Submission, error)
	SetSubmissionStatus(context.Context, SetSubmissionStatusRequest) (Submission, error)
	DeleteSubmission(context.Context, DeleteSubmissionRequest) error
}

type Schema struct {
	ID       string         `json:"id"`
	Key      string         `json:"key"`
	Version  int64          `json:"version"`
	Fields   map[string]any `json:"fields"`
	Revision int64          `json:"revision"`
}

type SchemaPage struct {
	Items      []Schema `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type SaveSchemaRequest struct {
	SnapshotID       string         `json:"snapshot_id"`
	SchemaID         string         `json:"schema_id,omitempty"`
	Key              string         `json:"key"`
	Fields           map[string]any `json:"fields"`
	ExpectedRevision int64          `json:"expected_revision"`
}

type Submission struct {
	ID        string         `json:"id"`
	FormID    string         `json:"form_id"`
	State     string         `json:"state"`
	Values    map[string]any `json:"values"`
	CreatedAt time.Time      `json:"created_at"`
	Revision  int64          `json:"revision"`
}

type ListSubmissionsRequest struct {
	SnapshotID string `json:"snapshot_id"`
	FormID     string `json:"form_id,omitempty"`
	State      string `json:"state,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	Limit      int    `json:"limit"`
}

type SubmissionPage struct {
	Items      []Submission `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type SetSubmissionStatusRequest struct {
	SnapshotID       string `json:"snapshot_id"`
	SubmissionID     string `json:"submission_id"`
	Status           string `json:"status"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type DeleteSubmissionRequest struct {
	SnapshotID       string `json:"snapshot_id"`
	SubmissionID     string `json:"submission_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() platform.ComponentDescriptor {
	return platform.ComponentDescriptor{ID: "engine.forms", Kind: "engine", Version: 1}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "form-schema.list", Action: platform.AccessActionRead, ResourceType: "form-schema"},
		{Name: "form-schema.read", Action: platform.AccessActionRead, ResourceType: "form-schema"},
		{Name: "form-schema.write", Action: platform.AccessActionWrite, ResourceType: "form-schema"},
		{Name: "submission.list", Action: platform.AccessActionRead, ResourceType: "form-submission"},
		{Name: "submission.read", Action: platform.AccessActionRead, ResourceType: "form-submission"},
		{Name: "submission.status.write", Action: platform.AccessActionWrite, ResourceType: "form-submission"},
		{Name: "submission.delete", Action: platform.AccessActionDelete, ResourceType: "form-submission"},
	}
}

var _ platform.Module = Descriptor{}
