// Package presentation exposes Forms' aggregate typed facade to the Admin API.
// Form lifecycle rules remain owned by the Forms engine.
package presentation

import (
	"context"
	"errors"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
	schemapodel "github.com/Zubrium/cdn/engines/forms/features/schemas/model"
)

const (
	SchemaAccessGroup     = "forms_schemas"
	SubmissionAccessGroup = "forms_submissions"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("revision conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrTooLarge     = errors.New("request too large")
	ErrUnavailable  = errors.New("Forms dependency unavailable")
	ErrExpired      = errors.New("form session expired")
)

type FieldDefinition = schemapodel.FieldDefinition

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
	ValidatePublicForm(context.Context, PublicFormBinding) error
	ValidatePublicUploadRoute(context.Context, PublicUploadRouteBinding) error
	CreateVisitorSession(context.Context, CreateVisitorSessionRequest) (VisitorSessionCredential, error)
	GetVisitorSession(context.Context, GetVisitorSessionRequest) (VisitorSession, error)
	BeginVisitorUpload(context.Context, BeginVisitorUploadRequest) (VisitorUploadReservation, error)
	CompleteVisitorUpload(context.Context, CompleteVisitorUploadRequest) (VisitorAttachment, error)
	AbortVisitorUpload(context.Context, AbortVisitorUploadRequest) error
	SubmitVisitorResponse(context.Context, SubmitVisitorResponseRequest) (VisitorSubmissionReceipt, error)
	CancelVisitorSession(context.Context, CancelVisitorSessionRequest) error
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
	ID            string         `json:"id"`
	FormID        string         `json:"form_id"`
	SchemaVersion int64          `json:"schema_version"`
	State         string         `json:"state"`
	Values        map[string]any `json:"values"`
	CreatedAt     time.Time      `json:"created_at"`
	Revision      int64          `json:"revision"`
}

// PublicFormBinding is copied from a server-side Router route configuration.
// Visitor requests cannot select or override any part of it.
type PublicFormBinding struct {
	SnapshotID    string `json:"snapshot_id"`
	FormID        string `json:"form_id"`
	SchemaVersion int64  `json:"schema_version"`
}

// PublicUploadRouteBinding binds an explicitly configured Router operation to
// the immutable form schema and its private Loader destination.
type PublicUploadRouteBinding struct {
	PublicFormBinding
	AccessGroup         string   `json:"access_group"`
	MaxBytes            int64    `json:"max_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types"`
}

func (binding PublicFormBinding) Validate() error {
	if binding.SnapshotID == "" || len(binding.SnapshotID) > 128 || binding.FormID == "" || len(binding.FormID) > 128 || binding.SchemaVersion < 1 {
		return ErrInvalid
	}
	for _, value := range []string{binding.SnapshotID, binding.FormID} {
		for _, char := range value {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '.' || char == '-' {
				continue
			}
			return ErrInvalid
		}
	}
	return nil
}

type VisitorSession struct {
	ID            string         `json:"id"`
	FormID        string         `json:"form_id"`
	SchemaVersion int64          `json:"schema_version"`
	Fields        map[string]any `json:"fields"`
	ExpiresAt     time.Time      `json:"expires_at"`
	Completed     bool           `json:"completed"`
}

type VisitorSessionCredential struct {
	Token   string         `json:"-"`
	Session VisitorSession `json:"session"`
}

type CreateVisitorSessionRequest struct {
	PublicFormBinding
}

type GetVisitorSessionRequest struct {
	PublicFormBinding
	Token string
}

type BeginVisitorUploadRequest struct {
	PublicFormBinding
	Token             string
	FieldID           string
	IdempotencyKey    string
	ContentType       string
	DeclaredSize      int64
	RouteMaxBytes     int64
	RouteContentTypes []string
	AccessGroup       string
}

type VisitorUploadReservation struct {
	ID           string
	FieldID      string
	MaxBytes     int64
	Completed    bool
	FileID       string `json:"-"`
	SHA256       string `json:"-"`
	AttachmentID string
	ContentType  string
	Size         int64
	AccessGroup  string
}

type CompleteVisitorUploadRequest struct {
	PublicFormBinding
	Token         string
	ReservationID string
	FileID        string
	SHA256        string
	ContentType   string
	Size          int64
	AccessGroup   string
}

type VisitorAttachment struct {
	ID      string `json:"id"`
	FieldID string `json:"field_id"`
}

type AbortVisitorUploadRequest struct {
	PublicFormBinding
	Token         string
	ReservationID string
}

type SubmitVisitorResponseRequest struct {
	PublicFormBinding
	Token          string
	IdempotencyKey string
	Values         map[string]any
}

type VisitorSubmissionReceipt struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type CancelVisitorSessionRequest struct {
	PublicFormBinding
	Token string
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
	return platform.ComponentDescriptor{
		ID: "engine.forms", Kind: "engine", Version: 1,
		AccessGroups: []string{SchemaAccessGroup, SubmissionAccessGroup},
	}
}

func (Descriptor) Operations() []platform.Operation {
	return []platform.Operation{
		{Name: "form-schema.list", Action: platform.AccessActionRead, ResourceType: "form-schema"},
		{Name: "form-schema.read", Action: platform.AccessActionRead, ResourceType: "form-schema"},
		{Name: "form-schema.write", Action: platform.AccessActionWrite, ResourceType: "form-schema"},
		{Name: "submission.list", Action: platform.AccessActionRead, ResourceType: "form-submission"},
		{Name: "submission.read", Action: platform.AccessActionRead, ResourceType: "form-submission"},
		{Name: "submission.export", Action: platform.AccessActionRead, ResourceType: "form-submission"},
		{Name: "submission.status.write", Action: platform.AccessActionWrite, ResourceType: "form-submission"},
		{Name: "submission.delete", Action: platform.AccessActionDelete, ResourceType: "form-submission"},
	}
}

var _ platform.Module = Descriptor{}
