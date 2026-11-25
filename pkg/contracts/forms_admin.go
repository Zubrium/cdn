package contracts

import (
	"context"
	"errors"
	"time"
)

var (
	ErrAdminFormsUnauthorized = errors.New("unauthorized")
	ErrAdminFormsForbidden    = errors.New("forbidden")
	ErrAdminFormsNotFound     = errors.New("not found")
	ErrAdminFormsConflict     = errors.New("revision conflict")
	ErrAdminFormsInvalid      = errors.New("invalid request")
	ErrAdminFormsTooLarge     = errors.New("request too large")
	ErrAdminFormsUnavailable  = errors.New("Forms dependency unavailable")
	ErrAdminFormsExpired      = errors.New("form session expired")
)

const (
	AdminFormsSchemaAccessGroup     = "forms_schemas"
	AdminFormsSubmissionAccessGroup = "forms_submissions"
)

type AdminFormsSchema struct {
	ID       string         `json:"id"`
	Key      string         `json:"key"`
	Version  int64          `json:"version"`
	Fields   map[string]any `json:"fields"`
	Revision int64          `json:"revision"`
}

type AdminFormsSchemaPage struct {
	Items      []AdminFormsSchema `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type AdminFormsSaveSchemaRequest struct {
	SchemaID         string         `json:"schema_id,omitempty"`
	Key              string         `json:"key"`
	Fields           map[string]any `json:"fields"`
	ExpectedRevision int64          `json:"expected_revision"`
}

type AdminFormsSubmission struct {
	ID            string         `json:"id"`
	FormID        string         `json:"form_id"`
	SchemaVersion int64          `json:"schema_version"`
	State         string         `json:"state"`
	Values        map[string]any `json:"values"`
	CreatedAt     time.Time      `json:"created_at"`
	Revision      int64          `json:"revision"`
}

type AdminFormsListSubmissionsRequest struct {
	FormID string `json:"form_id"`
	Key    string `json:"key,omitempty"`
	State  string `json:"state,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit"`
}

type AdminFormsSubmissionPage struct {
	Items      []AdminFormsSubmission `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type AdminFormsSetSubmissionStatusRequest struct {
	SubmissionID     string `json:"submission_id"`
	Status           string `json:"status"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type AdminFormsDeleteSubmissionRequest struct {
	SubmissionID     string `json:"submission_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type AdminFormsAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	ListSchemas(context.Context, string, int) (AdminFormsSchemaPage, error)
	GetSchema(context.Context, string) (AdminFormsSchema, error)
	SaveSchema(context.Context, AdminFormsSaveSchemaRequest) (AdminFormsSchema, error)
	ListSubmissions(context.Context, AdminFormsListSubmissionsRequest) (AdminFormsSubmissionPage, error)
	GetSubmission(context.Context, string) (AdminFormsSubmission, error)
	SetSubmissionStatus(context.Context, AdminFormsSetSubmissionStatusRequest) (AdminFormsSubmission, error)
	DeleteSubmission(context.Context, AdminFormsDeleteSubmissionRequest) error
}

type AdminFormsSchemaListRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	Cursor  string               `json:"cursor"`
	Limit   int                  `json:"limit"`
}

type AdminFormsSchemaIDRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	ID      string               `json:"id"`
}

type AdminFormsSchemaResponse struct {
	Schema AdminFormsSchema `json:"schema"`
}

type AdminFormsSchemaPageResponse struct {
	Page AdminFormsSchemaPage `json:"page"`
}

type AdminFormsSaveSchemaRequestMessage struct {
	Subject AuthorizationSubject        `json:"subject"`
	Request AdminFormsSaveSchemaRequest `json:"request"`
}

type AdminFormsListSubmissionsRequestMessage struct {
	Subject AuthorizationSubject             `json:"subject"`
	Request AdminFormsListSubmissionsRequest `json:"request"`
}

type AdminFormsSubmissionResponse struct {
	Submission AdminFormsSubmission `json:"submission"`
}

type AdminFormsSubmissionPageResponse struct {
	Page AdminFormsSubmissionPage `json:"page"`
}

type AdminFormsSetSubmissionStatusRequestMessage struct {
	Subject AuthorizationSubject                 `json:"subject"`
	Request AdminFormsSetSubmissionStatusRequest `json:"request"`
}

type AdminFormsDeleteSubmissionRequestMessage struct {
	Subject AuthorizationSubject              `json:"subject"`
	Request AdminFormsDeleteSubmissionRequest `json:"request"`
}

func AdminFormsAuthenticateOperation() Operation[AdminAuthenticateRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateRequest, AdminAuthenticateResponse]("admin.forms.authenticate", func(request AdminAuthenticateRequest) error {
		if request.Credential == "" {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsAuthenticateSessionOperation() Operation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse]("admin.forms.authenticate-session", func(request AdminAuthenticateSessionRequest) error {
		if request.Token == "" {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsListSchemasOperation() Operation[AdminFormsSchemaListRequest, AdminFormsSchemaPageResponse] {
	return adminOperation[AdminFormsSchemaListRequest, AdminFormsSchemaPageResponse]("admin.forms.schema.list", func(request AdminFormsSchemaListRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Limit < 1 {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsGetSchemaOperation() Operation[AdminFormsSchemaIDRequest, AdminFormsSchemaResponse] {
	return adminOperation[AdminFormsSchemaIDRequest, AdminFormsSchemaResponse]("admin.forms.schema.get", func(request AdminFormsSchemaIDRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsSaveSchemaOperation() Operation[AdminFormsSaveSchemaRequestMessage, AdminFormsSchemaResponse] {
	return adminOperation[AdminFormsSaveSchemaRequestMessage, AdminFormsSchemaResponse]("admin.forms.schema.save", func(request AdminFormsSaveSchemaRequestMessage) error {
		if validAdminSubject(request.Subject) != nil {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsListSubmissionsOperation() Operation[AdminFormsListSubmissionsRequestMessage, AdminFormsSubmissionPageResponse] {
	return adminOperation[AdminFormsListSubmissionsRequestMessage, AdminFormsSubmissionPageResponse]("admin.forms.submission.list", func(request AdminFormsListSubmissionsRequestMessage) error {
		if validAdminSubject(request.Subject) != nil || request.Request.Limit < 1 {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsGetSubmissionOperation() Operation[AdminFormsSchemaIDRequest, AdminFormsSubmissionResponse] {
	return adminOperation[AdminFormsSchemaIDRequest, AdminFormsSubmissionResponse]("admin.forms.submission.get", func(request AdminFormsSchemaIDRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsSetSubmissionStatusOperation() Operation[AdminFormsSetSubmissionStatusRequestMessage, AdminFormsSubmissionResponse] {
	return adminOperation[AdminFormsSetSubmissionStatusRequestMessage, AdminFormsSubmissionResponse]("admin.forms.submission.status", func(request AdminFormsSetSubmissionStatusRequestMessage) error {
		if validAdminSubject(request.Subject) != nil || request.Request.SubmissionID == "" {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsDeleteSubmissionOperation() Operation[AdminFormsDeleteSubmissionRequestMessage, AdminEmptyResponse] {
	return adminOperation[AdminFormsDeleteSubmissionRequestMessage, AdminEmptyResponse]("admin.forms.submission.delete", func(request AdminFormsDeleteSubmissionRequestMessage) error {
		if validAdminSubject(request.Subject) != nil || request.Request.SubmissionID == "" {
			return ErrAdminFormsInvalid
		}
		return nil
	})
}

func AdminFormsOperationKeys() []Key {
	return []Key{
		AdminFormsAuthenticateOperation().Key,
		AdminFormsAuthenticateSessionOperation().Key,
		AdminFormsListSchemasOperation().Key,
		AdminFormsGetSchemaOperation().Key,
		AdminFormsSaveSchemaOperation().Key,
		AdminFormsListSubmissionsOperation().Key,
		AdminFormsGetSubmissionOperation().Key,
		AdminFormsSetSubmissionStatusOperation().Key,
		AdminFormsDeleteSubmissionOperation().Key,
	}
}
