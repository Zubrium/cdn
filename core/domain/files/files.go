// Package files contains file and indexing domain values and storage ports.
package files

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Zubrium/cdn/core/domain/access"
)

var (
	ErrNotFound         = errors.New("file not found")
	ErrRevisionConflict = errors.New("file metadata revision conflict")
	ErrFileSetConflict  = errors.New("file set name conflicts within its namespace")
	ErrRequestConflict  = errors.New("upload request key reused with different content")
	ErrUploadInProgress = errors.New("upload request is still in progress")
	ErrRequestDeleted   = errors.New("upload request refers to a deleted file")
	ErrUnavailable      = errors.New("file storage is unavailable")
	ErrChecksumMismatch = errors.New("file checksum does not match expected SHA-256")
	ErrInvalidSearch    = errors.New("invalid file search query")
	ErrInvalid          = errors.New("invalid file storage operation")
)

type Object struct {
	ID          string
	Key         string
	Groups      []string
	Metadata    map[string]string
	SHA256      string
	Revision    int64
	Size        int64
	ContentType string
	Ready       bool
	State       string
}

type FileSet struct {
	ID              string
	Namespace       string
	Name            string
	ManagementGroup string
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type UploadRequestStatus struct {
	State  string
	FileID string
}

// Repository ports isolate file use cases from SQL, S3 and cache adapters.
type MetadataReader interface {
	Find(context.Context, string) (Object, error)
	Authorize(context.Context, string, string, access.Action) (int64, []string, error)
	Assignments(context.Context, string) ([]access.GroupAssignment, error)
	CurrentPrincipal(context.Context, string) (access.Principal, error)
}

type ByteReader interface {
	Read(context.Context, string) (io.ReadCloser, error)
	ReadRange(context.Context, string, int64, int64) (io.ReadCloser, error)
}

type Creator interface {
	Create(context.Context, access.Principal, string, []string, map[string]string, string, int64, io.Reader) (string, error)
}

type VerifiedCreator interface {
	CreateVerified(context.Context, access.Principal, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
}

type MetadataWriter interface {
	Update(context.Context, access.Principal, string, int64, map[string]string) error
	AddToGroup(context.Context, access.Principal, string, string, int64) error
	RemoveFromGroup(context.Context, access.Principal, string, string, int64) error
}

type ContentTypeWriter interface {
	UpdateWithContentType(context.Context, access.Principal, string, int64, map[string]string, string) error
}

type Deleter interface {
	Delete(context.Context, access.Principal, string, int64) error
}

type ByteCache interface {
	Get(string) ([]byte, bool)
	Put(string, []byte, time.Duration)
	Delete(string)
}

// FileIndexRepository persists search sets and applies their group policy in
// the same transaction as each membership or metadata change.
type FileIndexRepository interface {
	ListVisible(context.Context, access.Principal, string, string, int) ([]Object, string, error)
	ListSet(context.Context, access.Principal, string, string, int) ([]Object, string, error)
	Search(context.Context, access.Principal, string, string, string, string, int) ([]Object, string, error)
	CreateSet(context.Context, access.Principal, string, string, string) (FileSet, error)
	GetSet(context.Context, access.Principal, string) (FileSet, error)
	UpdateSet(context.Context, access.Principal, string, int64, string) (FileSet, error)
	DeleteSet(context.Context, access.Principal, string, int64) error
	AddToSet(context.Context, access.Principal, string, string, int64) (FileSet, error)
	RemoveFromSet(context.Context, access.Principal, string, string, int64) (FileSet, error)
	ListSets(context.Context, access.Principal, string, string, string, int) ([]FileSet, string, error)
}

// StagingRepository owns opaque temporary bytes and upload-request idempotency
// status. Session state and chunk ordering stay outside this Core contract.
type StagingRepository interface {
	PutStaged(context.Context, access.Principal, string, []string, int64, string, io.Reader) error
	ReadStaged(context.Context, access.Principal, string) (io.ReadCloser, int64, string, error)
	DeleteStaged(context.Context, access.Principal, string) error
	PurgeStaged(context.Context, string) error
	FindUploadRequest(context.Context, string, string) (UploadRequestStatus, error)
}
