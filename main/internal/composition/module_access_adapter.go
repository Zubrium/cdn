package composition

import (
	"context"
	"io"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type moduleCoreIdentity struct{ access contracts.CoreAccess }

func (adapter moduleCoreIdentity) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	principal, err := adapter.access.Authenticate(ctx, credential)
	return contracts.AuthorizationSubject{Credential: credential}, principal, err
}

type moduleCoreSessions struct{ access contracts.CoreAccess }

func (adapter moduleCoreSessions) AuthenticateSession(ctx context.Context, token string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	principal, err := adapter.access.AuthenticateSession(ctx, token)
	return contracts.AuthorizationSubject{SessionToken: token}, principal, err
}

type moduleCoreAuthorization struct{ access contracts.CoreAccess }

func (adapter moduleCoreAuthorization) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, groups []string, level contracts.AccessLevel) error {
	return adapter.access.RequireGroupLevel(ctx, subject, groups, level)
}

type moduleCoreGroupAuthorization struct{ access contracts.CoreAccess }

func (adapter moduleCoreGroupAuthorization) Authenticate(ctx context.Context, credential string) (contracts.AuthorizationSubject, contracts.Principal, error) {
	principal, err := adapter.access.Authenticate(ctx, credential)
	return contracts.AuthorizationSubject{Credential: credential}, principal, err
}

func (adapter moduleCoreGroupAuthorization) RequireGroupLevel(ctx context.Context, subject contracts.AuthorizationSubject, groups []string, level contracts.AccessLevel) error {
	return adapter.access.RequireGroupLevel(ctx, subject, groups, level)
}

type moduleCoreFileStat struct{ dispatcher contracts.Dispatcher }

func (adapter moduleCoreFileStat) StatFile(ctx context.Context, subject contracts.AuthorizationSubject, fileID string) (contracts.File, error) {
	return contracts.CallFileStatForSubject(ctx, adapter.dispatcher, subject, fileID)
}

// moduleCoreFiles adapts a module-scoped CoreFiles client to Website's
// subject-carrying ports. The subject was already authenticated through
// core.access.*; file capabilities are selected by the DataBus module scope,
// never by the caller.
type moduleCoreFiles struct{ files contracts.CoreFiles }

func (adapter moduleCoreFiles) StatFile(ctx context.Context, _ contracts.AuthorizationSubject, fileID string) (contracts.File, error) {
	if adapter.files == nil {
		return contracts.File{}, contracts.ErrDispatcherUnavailable
	}
	return adapter.files.StatFile(ctx, fileID)
}

func (adapter moduleCoreFiles) ReadFile(ctx context.Context, _ contracts.AuthorizationSubject, fileID string) (io.ReadCloser, contracts.File, error) {
	if adapter.files == nil {
		return nil, contracts.File{}, contracts.ErrDispatcherUnavailable
	}
	return adapter.files.ReadFile(ctx, fileID)
}

func (adapter moduleCoreFiles) CreateFileVerified(ctx context.Context, _ contracts.AuthorizationSubject, requestKey string, groups []string, metadata map[string]string, contentType string, size int64, checksum string, body io.Reader) (string, error) {
	if adapter.files == nil {
		return "", contracts.ErrDispatcherUnavailable
	}
	return adapter.files.CreateFileVerified(ctx, requestKey, groups, metadata, contentType, size, checksum, body)
}

func (adapter moduleCoreFiles) ListManagedFileSets(ctx context.Context, _ contracts.AuthorizationSubject, namespace, query, cursor string, limit int) ([]contracts.FileSet, string, error) {
	if adapter.files == nil {
		return nil, "", contracts.ErrDispatcherUnavailable
	}
	return adapter.files.ListManagedFileSets(ctx, namespace, query, cursor, limit)
}

func (adapter moduleCoreFiles) ListFileSet(ctx context.Context, _ contracts.AuthorizationSubject, setID, cursor string, limit int) ([]contracts.File, string, error) {
	if adapter.files == nil {
		return nil, "", contracts.ErrDispatcherUnavailable
	}
	return adapter.files.ListFileSet(ctx, setID, cursor, limit)
}

type formFileOperations struct{ files contracts.CoreFiles }

func (adapter formFileOperations) StatFile(ctx context.Context, _ contracts.AuthorizationSubject, fileID string) (contracts.File, error) {
	if adapter.files == nil {
		return contracts.File{}, contracts.ErrDispatcherUnavailable
	}
	return adapter.files.StatFile(ctx, fileID)
}

func (adapter formFileOperations) DeleteFile(ctx context.Context, _ contracts.AuthorizationSubject, fileID string, revision int64) error {
	if adapter.files == nil {
		return contracts.ErrDispatcherUnavailable
	}
	return adapter.files.DeleteFile(ctx, fileID, revision)
}

var _ interface {
	Authenticate(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
} = moduleCoreIdentity{}
var _ interface {
	AuthenticateSession(context.Context, string) (contracts.AuthorizationSubject, contracts.Principal, error)
} = moduleCoreSessions{}
var _ interface {
	RequireGroupLevel(context.Context, contracts.AuthorizationSubject, []string, contracts.AccessLevel) error
} = moduleCoreAuthorization{}
var _ interface {
	StatFile(context.Context, contracts.AuthorizationSubject, string) (contracts.File, error)
} = moduleCoreFileStat{}
var _ interface {
	StatFile(context.Context, contracts.AuthorizationSubject, string) (contracts.File, error)
	DeleteFile(context.Context, contracts.AuthorizationSubject, string, int64) error
} = formFileOperations{}
