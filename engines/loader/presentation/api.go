// Package presentation exports Loader's typed facade to the HTTP adapters.
package presentation

import loaderpkg "github.com/Zubrium/cdn/engines/loader/features/files/pkg"

type API = loaderpkg.API

var (
	ErrUnauthorized = loaderpkg.ErrUnauthorized
	ErrForbidden    = loaderpkg.ErrForbidden
	ErrNotFound     = loaderpkg.ErrNotFound
	ErrConflict     = loaderpkg.ErrConflict
	ErrInvalid      = loaderpkg.ErrInvalid
	ErrTooLarge     = loaderpkg.ErrTooLarge
	ErrUnavailable  = loaderpkg.ErrUnavailable
)

type BeginUploadInput = loaderpkg.BeginUploadInput
type UploadPart = loaderpkg.UploadPart
type UploadSession = loaderpkg.UploadSession
type FileMetadataPatch = loaderpkg.FileMetadataPatch
type File = loaderpkg.File
type ListQuery = loaderpkg.ListQuery
type FilePage = loaderpkg.FilePage
type CreateFileSetInput = loaderpkg.CreateFileSetInput
type FileSetMetadataPatch = loaderpkg.FileSetMetadataPatch
type FileSet = loaderpkg.FileSet
type SetListQuery = loaderpkg.SetListQuery
type FileSetPage = loaderpkg.FileSetPage

func ValidIdempotencyKey(value string) bool { return loaderpkg.ValidIdempotencyKey(value) }
