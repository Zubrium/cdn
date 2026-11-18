package fileapi

import (
	"errors"

	loaderapi "github.com/Zubrium/cdn/engines/loader/presentation"
)

var (
	ErrUnauthorized = loaderapi.ErrUnauthorized
	ErrForbidden    = loaderapi.ErrForbidden
	ErrNotFound     = loaderapi.ErrNotFound
	ErrConflict     = loaderapi.ErrConflict
	ErrInvalid      = loaderapi.ErrInvalid
	ErrTooLarge     = loaderapi.ErrTooLarge
	ErrUnavailable  = loaderapi.ErrUnavailable
)

type Loader = loaderapi.API
type BeginUploadInput = loaderapi.BeginUploadInput
type UploadPart = loaderapi.UploadPart
type UploadSession = loaderapi.UploadSession
type FileMetadataPatch = loaderapi.FileMetadataPatch
type File = loaderapi.File
type ListQuery = loaderapi.ListQuery
type FilePage = loaderapi.FilePage
type CreateFileSetInput = loaderapi.CreateFileSetInput
type FileSetMetadataPatch = loaderapi.FileSetMetadataPatch
type FileSet = loaderapi.FileSet
type SetListQuery = loaderapi.SetListQuery
type FileSetPage = loaderapi.FileSetPage
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
