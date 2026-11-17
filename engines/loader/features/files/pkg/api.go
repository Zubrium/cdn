// Package pkg contains the stable data contracts shared by Loader's application
// and presentation boundary. It has no transport or storage implementation.
package pkg

import (
	"context"
	"errors"
	"io"
	"strings"
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
	ListFileSets(context.Context, SetListQuery) (FileSetPage, error)
	CreateFileSet(context.Context, CreateFileSetInput) (FileSet, error)
	GetFileSet(context.Context, string) (FileSet, error)
	UpdateFileSet(context.Context, string, int64, FileSetMetadataPatch) (FileSet, error)
	DeleteFileSet(context.Context, string, int64) error
	ListSetFiles(context.Context, string, ListQuery) (FilePage, error)
	AddFileToSet(context.Context, string, string, int64) (FileSet, error)
	RemoveFileFromSet(context.Context, string, string, int64) (FileSet, error)
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

// File deliberately has no owner, visibility, role, or content version.
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

type CreateFileSetInput struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	ManagementGroup string `json:"managementGroup"`
}

type FileSetMetadataPatch struct {
	Name *string `json:"name,omitempty"`
}

type FileSet struct {
	ID              string    `json:"id"`
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	ManagementGroup string    `json:"managementGroup"`
	Revision        int64     `json:"revision"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type SetListQuery struct {
	Namespace string
	Query     string
	Cursor    string
	Limit     int
}

type FileSetPage struct {
	Items      []FileSet `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

func ValidIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
			continue
		}
		return false
	}
	return true
}
