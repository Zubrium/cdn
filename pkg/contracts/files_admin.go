package contracts

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrAdminFilesUnauthorized = errors.New("unauthorized")
	ErrAdminFilesForbidden    = errors.New("forbidden")
	ErrAdminFilesNotFound     = errors.New("not found")
	ErrAdminFilesConflict     = errors.New("conflict")
	ErrAdminFilesInvalid      = errors.New("invalid request")
	ErrAdminFilesTooLarge     = errors.New("request too large")
	ErrAdminFilesUnavailable  = errors.New("loader dependency unavailable")
)

func ValidAdminFileIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

type AdminFileBeginUploadInput struct {
	IdempotencyKey string            `json:"-"`
	Name           string            `json:"name"`
	ContentType    string            `json:"contentType"`
	Size           int64             `json:"size"`
	SHA256         string            `json:"sha256"`
	Groups         []string          `json:"groups"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type AdminFileUploadPart struct {
	Number int    `json:"number"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type AdminFileUploadSession struct {
	ID        string                `json:"id"`
	State     string                `json:"state"`
	PartSize  int64                 `json:"partSize"`
	Parts     []AdminFileUploadPart `json:"parts"`
	ExpiresAt time.Time             `json:"expiresAt"`
	FileID    string                `json:"fileId,omitempty"`
}

type AdminFileMetadataPatch struct {
	Name        *string            `json:"name,omitempty"`
	ContentType *string            `json:"contentType,omitempty"`
	Attributes  *map[string]string `json:"attributes,omitempty"`
}

type AdminFile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ContentType string            `json:"contentType"`
	Size        int64             `json:"size"`
	SHA256      string            `json:"sha256"`
	Revision    int64             `json:"revision"`
	Groups      []string          `json:"groups"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type AdminFileListQuery struct {
	Group  string
	Query  string
	Cursor string
	Limit  int
}

type AdminFilePage struct {
	Items      []AdminFile
	NextCursor string
}

type AdminFileCreateFileSetInput struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	ManagementGroup string `json:"managementGroup"`
}

type AdminFileSetMetadataPatch struct {
	Name *string `json:"name,omitempty"`
}

type AdminFileSet struct {
	ID              string    `json:"id"`
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	ManagementGroup string    `json:"managementGroup"`
	Revision        int64     `json:"revision"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type AdminFileSetListQuery struct {
	Namespace string
	Query     string
	Cursor    string
	Limit     int
}

type AdminFileSetPage struct {
	Items      []AdminFileSet `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type AdminFilesAPI interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
	BeginUpload(context.Context, AdminFileBeginUploadInput) (AdminFileUploadSession, error)
	UploadStatus(context.Context, string) (AdminFileUploadSession, error)
	PutUploadPart(context.Context, string, int, int64, string, io.Reader) (AdminFileUploadSession, error)
	CommitUpload(context.Context, string) (AdminFile, error)
	AbortUpload(context.Context, string) error
	ListFiles(context.Context, AdminFileListQuery) (AdminFilePage, error)
	StatFile(context.Context, string) (AdminFile, error)
	ReadFile(context.Context, string) (io.ReadCloser, AdminFile, error)
	ReadFileRange(context.Context, string, int64, int64) (io.ReadCloser, AdminFile, error)
	UpdateFile(context.Context, string, int64, AdminFileMetadataPatch) (AdminFile, error)
	DeleteFile(context.Context, string, int64) error
	AddFileToGroup(context.Context, string, string, int64) (AdminFile, error)
	RemoveFileFromGroup(context.Context, string, string, int64) (AdminFile, error)
	ListFileSets(context.Context, AdminFileSetListQuery) (AdminFileSetPage, error)
	CreateFileSet(context.Context, AdminFileCreateFileSetInput) (AdminFileSet, error)
	GetFileSet(context.Context, string) (AdminFileSet, error)
	UpdateFileSet(context.Context, string, int64, AdminFileSetMetadataPatch) (AdminFileSet, error)
	DeleteFileSet(context.Context, string, int64) error
	ListSetFiles(context.Context, string, AdminFileListQuery) (AdminFilePage, error)
	AddFileToSet(context.Context, string, string, int64) (AdminFileSet, error)
	RemoveFileFromSet(context.Context, string, string, int64) (AdminFileSet, error)
}

type AdminFilesSubjectRequest struct {
	Subject AuthorizationSubject `json:"subject"`
}

type AdminFilesIDRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	ID      string               `json:"id"`
}

type AdminFilesRevisionRequest struct {
	Subject          AuthorizationSubject `json:"subject"`
	ID               string               `json:"id"`
	ExpectedRevision int64                `json:"expected_revision"`
}

type AdminFilesNamedRevisionRequest struct {
	Subject          AuthorizationSubject `json:"subject"`
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	ExpectedRevision int64                `json:"expected_revision"`
}

type AdminFilesBeginUploadRequest struct {
	Subject        AuthorizationSubject      `json:"subject"`
	Input          AdminFileBeginUploadInput `json:"input"`
	IdempotencyKey string                    `json:"idempotency_key"`
}

type AdminFilesUploadPartRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	ID      string               `json:"id"`
	Number  int                  `json:"number"`
	Size    int64                `json:"size"`
	SHA256  string               `json:"sha256"`
	Body    StreamRef            `json:"body"`
}

type AdminFilesReadRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	ID      string               `json:"id"`
	Body    StreamRef            `json:"body"`
	Start   int64                `json:"start"`
	End     int64                `json:"end"`
	Range   bool                 `json:"range"`
}

type AdminFilesListRequest struct {
	Subject AuthorizationSubject `json:"subject"`
	Query   AdminFileListQuery   `json:"query"`
	SetID   string               `json:"set_id"`
}

type AdminFilesSetListRequest struct {
	Subject AuthorizationSubject  `json:"subject"`
	Query   AdminFileSetListQuery `json:"query"`
}

type AdminFilesCreateSetRequest struct {
	Subject AuthorizationSubject        `json:"subject"`
	Input   AdminFileCreateFileSetInput `json:"input"`
}

type AdminFilesSetUpdateRequest struct {
	Subject          AuthorizationSubject      `json:"subject"`
	ID               string                    `json:"id"`
	ExpectedRevision int64                     `json:"expected_revision"`
	Patch            AdminFileSetMetadataPatch `json:"patch"`
}

type AdminFilesFileUpdateRequest struct {
	Subject          AuthorizationSubject   `json:"subject"`
	ID               string                 `json:"id"`
	ExpectedRevision int64                  `json:"expected_revision"`
	Patch            AdminFileMetadataPatch `json:"patch"`
}

type AdminFilesUploadSessionResponse struct {
	Session AdminFileUploadSession `json:"session"`
}

type AdminFilesFileResponse struct {
	File AdminFile `json:"file"`
}

type AdminFilesFilePageResponse struct {
	Page AdminFilePage `json:"page"`
}

type AdminFilesReadResponse struct {
	File AdminFile `json:"file"`
}

type AdminFilesSetResponse struct {
	Set AdminFileSet `json:"set"`
}

type AdminFilesSetPageResponse struct {
	Page AdminFileSetPage `json:"page"`
}

func AdminFilesAuthenticateOperation() Operation[AdminAuthenticateRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateRequest, AdminAuthenticateResponse]("admin.files.authenticate", func(request AdminAuthenticateRequest) error {
		if request.Credential == "" {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesAuthenticateSessionOperation() Operation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse] {
	return adminOperation[AdminAuthenticateSessionRequest, AdminAuthenticateResponse]("admin.files.authenticate-session", func(request AdminAuthenticateSessionRequest) error {
		if request.Token == "" {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func adminFilesValidID(request AdminFilesIDRequest) error {
	if validAdminSubject(request.Subject) != nil || request.ID == "" {
		return ErrAdminFilesInvalid
	}
	return nil
}

func adminFilesValidRevision(request AdminFilesRevisionRequest) error {
	if validAdminSubject(request.Subject) != nil || request.ID == "" || request.ExpectedRevision < 0 {
		return ErrAdminFilesInvalid
	}
	return nil
}

func AdminFilesListFileSetsOperation() Operation[AdminFilesSetListRequest, AdminFilesSetPageResponse] {
	return adminOperation[AdminFilesSetListRequest, AdminFilesSetPageResponse]("admin.files.sets.list", func(request AdminFilesSetListRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Query.Limit < 1 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesCreateFileSetOperation() Operation[AdminFilesCreateSetRequest, AdminFilesSetResponse] {
	return adminOperation[AdminFilesCreateSetRequest, AdminFilesSetResponse]("admin.files.set.create", func(request AdminFilesCreateSetRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Input.Namespace == "" || request.Input.Name == "" || request.Input.ManagementGroup == "" {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesGetFileSetOperation() Operation[AdminFilesIDRequest, AdminFilesSetResponse] {
	return adminOperation[AdminFilesIDRequest, AdminFilesSetResponse]("admin.files.set.get", adminFilesValidID)
}

func AdminFilesUpdateFileSetOperation() Operation[AdminFilesSetUpdateRequest, AdminFilesSetResponse] {
	return adminOperation[AdminFilesSetUpdateRequest, AdminFilesSetResponse]("admin.files.set.update", func(request AdminFilesSetUpdateRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.ExpectedRevision < 0 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesDeleteFileSetOperation() Operation[AdminFilesRevisionRequest, AdminEmptyResponse] {
	return adminOperation[AdminFilesRevisionRequest, AdminEmptyResponse]("admin.files.set.delete", adminFilesValidRevision)
}

func AdminFilesListSetFilesOperation() Operation[AdminFilesListRequest, AdminFilesFilePageResponse] {
	return adminOperation[AdminFilesListRequest, AdminFilesFilePageResponse]("admin.files.set-files.list", func(request AdminFilesListRequest) error {
		if validAdminSubject(request.Subject) != nil || request.SetID == "" || request.Query.Limit < 1 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesAddToSetOperation() Operation[AdminFilesNamedRevisionRequest, AdminFilesSetResponse] {
	return adminOperation[AdminFilesNamedRevisionRequest, AdminFilesSetResponse]("admin.files.set-files.add", func(request AdminFilesNamedRevisionRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.Name == "" || request.ExpectedRevision < 0 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesRemoveFromSetOperation() Operation[AdminFilesNamedRevisionRequest, AdminFilesSetResponse] {
	return adminOperation[AdminFilesNamedRevisionRequest, AdminFilesSetResponse]("admin.files.set-files.remove", func(request AdminFilesNamedRevisionRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.Name == "" || request.ExpectedRevision < 0 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesBeginUploadOperation() Operation[AdminFilesBeginUploadRequest, AdminFilesUploadSessionResponse] {
	return adminOperation[AdminFilesBeginUploadRequest, AdminFilesUploadSessionResponse]("admin.files.upload.begin", func(request AdminFilesBeginUploadRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Input.Name == "" || request.Input.ContentType == "" || request.Input.SHA256 == "" {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesUploadStatusOperation() Operation[AdminFilesIDRequest, AdminFilesUploadSessionResponse] {
	return adminOperation[AdminFilesIDRequest, AdminFilesUploadSessionResponse]("admin.files.upload.status", adminFilesValidID)
}

func AdminFilesPutUploadPartOperation() Operation[AdminFilesUploadPartRequest, AdminFilesUploadSessionResponse] {
	return adminOperation[AdminFilesUploadPartRequest, AdminFilesUploadSessionResponse]("admin.files.upload.part", func(request AdminFilesUploadPartRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.Number < 1 || request.Size < 0 || request.SHA256 == "" || request.Body == "" {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesCommitUploadOperation() Operation[AdminFilesIDRequest, AdminFilesFileResponse] {
	return adminOperation[AdminFilesIDRequest, AdminFilesFileResponse]("admin.files.upload.commit", adminFilesValidID)
}

func AdminFilesAbortUploadOperation() Operation[AdminFilesIDRequest, AdminEmptyResponse] {
	return adminOperation[AdminFilesIDRequest, AdminEmptyResponse]("admin.files.upload.abort", adminFilesValidID)
}

func AdminFilesListFilesOperation() Operation[AdminFilesListRequest, AdminFilesFilePageResponse] {
	return adminOperation[AdminFilesListRequest, AdminFilesFilePageResponse]("admin.files.list", func(request AdminFilesListRequest) error {
		if validAdminSubject(request.Subject) != nil || request.Query.Limit < 1 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesStatFileOperation() Operation[AdminFilesIDRequest, AdminFilesFileResponse] {
	return adminOperation[AdminFilesIDRequest, AdminFilesFileResponse]("admin.files.stat", adminFilesValidID)
}

func AdminFilesReadOperation() Operation[AdminFilesReadRequest, AdminFilesReadResponse] {
	return adminOperation[AdminFilesReadRequest, AdminFilesReadResponse]("admin.files.read", func(request AdminFilesReadRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.Body == "" {
			return ErrAdminFilesInvalid
		}
		if request.Range && (request.Start < 0 || request.End < request.Start) {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesUpdateFileOperation() Operation[AdminFilesFileUpdateRequest, AdminFilesFileResponse] {
	return adminOperation[AdminFilesFileUpdateRequest, AdminFilesFileResponse]("admin.files.update", func(request AdminFilesFileUpdateRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.ExpectedRevision < 0 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesDeleteFileOperation() Operation[AdminFilesRevisionRequest, AdminEmptyResponse] {
	return adminOperation[AdminFilesRevisionRequest, AdminEmptyResponse]("admin.files.delete", adminFilesValidRevision)
}

func AdminFilesAddToGroupOperation() Operation[AdminFilesNamedRevisionRequest, AdminFilesFileResponse] {
	return adminOperation[AdminFilesNamedRevisionRequest, AdminFilesFileResponse]("admin.files.group.add", func(request AdminFilesNamedRevisionRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.Name == "" || request.ExpectedRevision < 0 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesRemoveFromGroupOperation() Operation[AdminFilesNamedRevisionRequest, AdminFilesFileResponse] {
	return adminOperation[AdminFilesNamedRevisionRequest, AdminFilesFileResponse]("admin.files.group.remove", func(request AdminFilesNamedRevisionRequest) error {
		if validAdminSubject(request.Subject) != nil || request.ID == "" || request.Name == "" || request.ExpectedRevision < 0 {
			return ErrAdminFilesInvalid
		}
		return nil
	})
}

func AdminFilesOperationKeys() []Key {
	return []Key{
		AdminFilesAuthenticateOperation().Key,
		AdminFilesAuthenticateSessionOperation().Key,
		AdminFilesListFileSetsOperation().Key,
		AdminFilesCreateFileSetOperation().Key,
		AdminFilesGetFileSetOperation().Key,
		AdminFilesUpdateFileSetOperation().Key,
		AdminFilesDeleteFileSetOperation().Key,
		AdminFilesListSetFilesOperation().Key,
		AdminFilesAddToSetOperation().Key,
		AdminFilesRemoveFromSetOperation().Key,
		AdminFilesBeginUploadOperation().Key,
		AdminFilesUploadStatusOperation().Key,
		AdminFilesPutUploadPartOperation().Key,
		AdminFilesCommitUploadOperation().Key,
		AdminFilesAbortUploadOperation().Key,
		AdminFilesListFilesOperation().Key,
		AdminFilesStatFileOperation().Key,
		AdminFilesReadOperation().Key,
		AdminFilesUpdateFileOperation().Key,
		AdminFilesDeleteFileOperation().Key,
		AdminFilesAddToGroupOperation().Key,
		AdminFilesRemoveFromGroupOperation().Key,
	}
}
