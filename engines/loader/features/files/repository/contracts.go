// Package repository defines the Core capabilities required by the Loader
// file feature. Implementations are supplied by composition, never SQL here.
package repository

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type ModuleData interface {
	Get(context.Context, contracts.RecordKey) (contracts.Record, error)
	GetExpired(context.Context, contracts.RecordKey) (contracts.Record, error)
	Put(context.Context, contracts.RecordKey, contracts.RecordValue, int64) (contracts.Record, error)
	Delete(context.Context, contracts.RecordKey, int64) error
	Batch(context.Context, []contracts.RecordMutation) error
	List(context.Context, string, string, string, string, []contracts.RecordSelector, int) ([]contracts.RecordKey, []contracts.Record, string, error)
	ListExpired(context.Context, string, string, string, string, int) ([]contracts.RecordKey, []contracts.Record, string, error)
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
	MaxIndexBytes() int
	MaxBatchBytes() int
}

type Identity interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
	AuthenticateSession(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
}

type Files interface {
	PutStagedObject(context.Context, contracts.AuthorizationSubject, string, []string, int64, string, io.Reader) error
	ReadStagedObject(context.Context, contracts.AuthorizationSubject, string) (io.ReadCloser, int64, string, error)
	PurgeStagedObject(context.Context, string) error
	ResolveUploadRequest(context.Context, string, string) (contracts.UploadRequestStatus, error)
	CreateFileVerified(context.Context, contracts.AuthorizationSubject, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
	ReadFile(context.Context, contracts.AuthorizationSubject, string) (io.ReadCloser, contracts.File, error)
	ReadFileRange(context.Context, contracts.AuthorizationSubject, string, int64, int64) (io.ReadCloser, contracts.File, error)
	StatFile(context.Context, contracts.AuthorizationSubject, string) (contracts.File, error)
	SearchFiles(context.Context, contracts.AuthorizationSubject, string, string, string, string, int) ([]contracts.File, string, error)
	UpdateFileWithContentType(context.Context, contracts.AuthorizationSubject, string, int64, map[string]string, string) error
	AddFileToGroup(context.Context, contracts.AuthorizationSubject, string, string, int64) error
	RemoveFileFromGroup(context.Context, contracts.AuthorizationSubject, string, string, int64) error
	DeleteFile(context.Context, contracts.AuthorizationSubject, string, int64) error
	CreateManagedFileSet(context.Context, contracts.AuthorizationSubject, string, string, string) (contracts.FileSet, error)
	GetManagedFileSet(context.Context, contracts.AuthorizationSubject, string) (contracts.FileSet, error)
	UpdateManagedFileSet(context.Context, contracts.AuthorizationSubject, string, int64, string) (contracts.FileSet, error)
	DeleteManagedFileSet(context.Context, contracts.AuthorizationSubject, string, int64) error
	AddFileToManagedSet(context.Context, contracts.AuthorizationSubject, string, string, int64) (contracts.FileSet, error)
	RemoveFileFromManagedSet(context.Context, contracts.AuthorizationSubject, string, string, int64) (contracts.FileSet, error)
	ListManagedFileSets(context.Context, contracts.AuthorizationSubject, string, string, string, int) ([]contracts.FileSet, string, error)
	ListFileSet(context.Context, contracts.AuthorizationSubject, string, string, int) ([]contracts.File, string, error)
}
