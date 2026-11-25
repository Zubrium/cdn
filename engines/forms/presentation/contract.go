// Package presentation exposes Forms' aggregate typed facade to the Admin API.
// Form lifecycle rules remain owned by the Forms engine.
package presentation

import (
	"context"
	"time"

	"github.com/Zubrium/cdn/pkg/contracts"
	schemapodel "github.com/Zubrium/cdn/engines/forms/features/schemas/model"
)

const (
	SchemaAccessGroup     = "forms_schemas"
	SubmissionAccessGroup = "forms_submissions"
)

var (
	ErrUnauthorized = contracts.ErrAdminFormsUnauthorized
	ErrForbidden    = contracts.ErrAdminFormsForbidden
	ErrNotFound     = contracts.ErrAdminFormsNotFound
	ErrConflict     = contracts.ErrAdminFormsConflict
	ErrInvalid      = contracts.ErrAdminFormsInvalid
	ErrTooLarge     = contracts.ErrAdminFormsTooLarge
	ErrUnavailable  = contracts.ErrAdminFormsUnavailable
	ErrExpired      = contracts.ErrAdminFormsExpired
)

type FieldDefinition = schemapodel.FieldDefinition

type Schema = contracts.AdminFormsSchema
type SchemaPage = contracts.AdminFormsSchemaPage
type SaveSchemaRequest = contracts.AdminFormsSaveSchemaRequest
type Submission = contracts.AdminFormsSubmission
type ListSubmissionsRequest = contracts.AdminFormsListSubmissionsRequest
type SubmissionPage = contracts.AdminFormsSubmissionPage
type SetSubmissionStatusRequest = contracts.AdminFormsSetSubmissionStatusRequest
type DeleteSubmissionRequest = contracts.AdminFormsDeleteSubmissionRequest

type Contract interface {
	API
}

type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListSchemas(context.Context, string, int) (SchemaPage, error)
	GetSchema(context.Context, string) (Schema, error)
	SaveSchema(context.Context, SaveSchemaRequest) (Schema, error)
	ListSubmissions(context.Context, ListSubmissionsRequest) (SubmissionPage, error)
	GetSubmission(context.Context, string) (Submission, error)
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

// PublicFormBinding is copied from a server-side Router route configuration.
// Visitor requests cannot select or override any part of it.
type PublicFormBinding struct {
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
	if binding.FormID == "" || len(binding.FormID) > 128 || binding.SchemaVersion < 1 {
		return ErrInvalid
	}
	for _, value := range []string{binding.FormID} {
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

type Descriptor struct{}

func (Descriptor) ComponentDescriptor() contracts.ComponentDescriptor {
	return contracts.ComponentDescriptor{
		ID: "engine.forms", Kind: "engine", Version: 1,
		AccessGroups: []string{SchemaAccessGroup, SubmissionAccessGroup},
	}
}
