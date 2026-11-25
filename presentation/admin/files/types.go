package fileapi

import (
	"errors"

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

type Loader = contracts.AdminFilesAPI
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

type Config struct {
	MaxFileBytes      int64
	MaxPartBytes      int64
	MaxUploadParts    int
	MaxJSONBytes      int64
	MaxAttributeBytes int64
	MaxGroupsPerFile  int
	DefaultPageSize   int
	MaxPageSize       int
	AllowedOrigins    []string
}

func (c Config) validate() error {
	if c.MaxFileBytes <= 0 || c.MaxPartBytes <= 0 || c.MaxUploadParts <= 0 ||
		c.MaxJSONBytes <= 0 || c.MaxAttributeBytes <= 0 || c.MaxGroupsPerFile <= 0 ||
		c.MaxGroupsPerFile > 64 || c.DefaultPageSize <= 0 || c.MaxPageSize < c.DefaultPageSize || c.MaxPageSize > 1000 {
		return errors.New("invalid File API limits")
	}
	return nil
}

func (c Config) Validate() error { return c.validate() }
