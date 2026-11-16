// Package presentation defines Loader Engine's typed in-process contract.
package presentation

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrTooLarge     = errors.New("request too large")
	ErrUnavailable  = errors.New("loader dependency unavailable")
)

// API is the only in-process dependency of presentation/fileapi. Implementations
// authenticate and authorize through Core and never accept caller-supplied IDs.
type API interface {
	Authenticate(context.Context, string) (context.Context, error)

	BeginUpload(context.Context, BeginUploadInput) (UploadSession, error)
	UploadStatus(context.Context, string) (UploadSession, error)
	PutUploadPart(context.Context, string, int, int64, string, io.Reader) (UploadSession, error)
	CommitUpload(context.Context, string) (File, error)
	AbortUpload(context.Context, string) error

	ListFiles(context.Context, ListQuery) (FilePage, error)
	StatFile(context.Context, string) (File, error)
	ReadFile(context.Context, string) (io.ReadCloser, File, error)
	ReadFileRange(context.Context, string, int64, int64) (io.ReadCloser, File, error)
	UpdateFile(context.Context, string, int64, FileMetadataPatch) (File, error)
	DeleteFile(context.Context, string, int64) error
	AddFileToGroup(context.Context, string, string, int64) (File, error)
	RemoveFileFromGroup(context.Context, string, string, int64) (File, error)
}

type BeginUploadInput struct {
	IdempotencyKey string            `json:"-"`
	Name           string            `json:"name"`
	ContentType    string            `json:"contentType"`
	Size           int64             `json:"size"`
	SHA256         string            `json:"sha256"`
	Groups         []string          `json:"groups"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type UploadPart struct {
	Number int    `json:"number"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type UploadSession struct {
	ID        string       `json:"id"`
	State     string       `json:"state"`
	PartSize  int64        `json:"partSize"`
	Parts     []UploadPart `json:"parts"`
	ExpiresAt time.Time    `json:"expiresAt"`
	FileID    string       `json:"fileId,omitempty"`
}

type FileMetadataPatch struct {
	Name        *string            `json:"name,omitempty"`
	ContentType *string            `json:"contentType,omitempty"`
	Attributes  *map[string]string `json:"attributes,omitempty"`
}

// File deliberately has no owner, visibility flag, role or content version.
type File struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ContentType string            `json:"contentType"`
	Size        int64             `json:"size"`
	SHA256      string            `json:"sha256"`
	Revision    int64             `json:"revision"`
	Groups      []string          `json:"groups"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type ListQuery struct {
	Group  string
	Query  string
	Cursor string
	Limit  int
}

type FilePage struct {
	Items      []File
	NextCursor string
}
