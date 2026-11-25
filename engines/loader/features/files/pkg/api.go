// Package pkg contains the stable data contracts shared by Loader's application
// and presentation boundary. It has no transport or storage implementation.
package pkg

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/pkg/contracts"
)

var (
	ErrUnauthorized = contracts.ErrAdminFilesUnauthorized
	ErrForbidden    = contracts.ErrAdminFilesForbidden
	ErrNotFound     = contracts.ErrAdminFilesNotFound
	ErrConflict     = contracts.ErrAdminFilesConflict
	ErrInvalid      = contracts.ErrAdminFilesInvalid
	ErrTooLarge     = contracts.ErrAdminFilesTooLarge
	ErrUnavailable  = contracts.ErrAdminFilesUnavailable
)

type API interface {
	Authenticate(context.Context, string) (context.Context, error)
	AuthenticateSession(context.Context, string) (context.Context, error)
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

type BeginUploadInput = contracts.AdminFileBeginUploadInput
type UploadPart = contracts.AdminFileUploadPart
type UploadSession = contracts.AdminFileUploadSession
type FileMetadataPatch = contracts.AdminFileMetadataPatch
type File = contracts.AdminFile
type ListQuery = contracts.AdminFileListQuery
type FilePage = contracts.AdminFilePage
type CreateFileSetInput = contracts.AdminFileCreateFileSetInput
type FileSetMetadataPatch = contracts.AdminFileSetMetadataPatch
type FileSet = contracts.AdminFileSet
type SetListQuery = contracts.AdminFileSetListQuery
type FileSetPage = contracts.AdminFileSetPage

func ValidIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	return contracts.ValidAdminFileIdempotencyKey(value)
}
