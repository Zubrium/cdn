// Package application contains Website use cases backed by the scoped Core
// engine-data capability. It does not access SQL, S3, or another engine.
package application

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	"github.com/google/uuid"
)

var errConfigInvalid = errors.New("invalid Website configuration")

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var snapshotIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var featureVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)

type RecordStore interface {
	Get(context.Context, core.ModuleDataKey) (core.ModuleDataRecord, error)
	List(context.Context, string, string, string, string, []core.ModuleDataSelector, int) ([]core.ModuleDataKey, []core.ModuleDataRecord, string, error)
	Batch(context.Context, []core.ModuleDataMutation) error
	MaxPageSize() int
	MaxBatchSize() int
	MaxPayloadBytes() int
}

// BuildWorkflowClient is the owner-scoped Coordinator capability used by
// durable Website builds. It is supplied by composition; no request can choose
// an operation, owner or task identity.
type BuildWorkflowClient interface {
	Start(context.Context, core.WorkflowStartRequest) (core.WorkflowReceipt, error)
	Get(context.Context, uuid.UUID) (core.Workflow, error)
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (core.TaskResult, error)
	ResolveTaskID(context.Context, uuid.UUID, string) (uuid.UUID, error)
}

// LocaleCatalogResolver pins the locale catalog revision into a build input so
// a prerender result cannot be activated after the catalog changed.
type LocaleCatalogResolver interface {
	LocaleCatalogRevision(context.Context, string) (int64, error)
}

type Config struct {
	AccessGroup            string
	CoordinatorAccessGroup string
	BuildWorkflow          BuildWorkflowClient
	LocaleCatalog          LocaleCatalogResolver
	BuildOperationID       string
	BuildContractVersion   int
}

type Service struct {
	config    Config
	store     RecordStore
	authority core.GroupAuthorizationCapability
	sessions  core.SessionCapability
}

type actor struct {
	session core.Session
}

type actorContextKey struct{}

func NewService(config Config, store RecordStore, authority core.GroupAuthorizationCapability, sessions core.SessionCapability) (*Service, error) {
	if !validInterface(store) || !validInterface(authority) || !validInterface(sessions) ||
		!core.ValidAccessGroupKey(config.AccessGroup) || !core.ValidAccessGroupKey(config.CoordinatorAccessGroup) || store.MaxPageSize() < 1 || store.MaxBatchSize() < 1 || store.MaxPayloadBytes() < 1 {
		return nil, errConfigInvalid
	}
	return &Service{config: config, store: store, authority: authority, sessions: sessions}, nil
}

func (s *Service) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if s == nil || ctx == nil || strings.TrimSpace(credential) == "" {
		return nil, websiteapi.ErrUnauthorized
	}
	session, _, err := s.authority.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, actorContextKey{}, actor{session: session}), nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if s == nil || ctx == nil || strings.TrimSpace(token) == "" {
		return nil, websiteapi.ErrUnauthorized
	}
	session, _, err := s.sessions.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapCoreError(err)
	}
	return context.WithValue(ctx, actorContextKey{}, actor{session: session}), nil
}

func (s *Service) requireLevel(ctx context.Context, minimum core.AccessLevel) error {
	if s == nil || ctx == nil {
		return websiteapi.ErrUnauthorized
	}
	current, ok := ctx.Value(actorContextKey{}).(actor)
	if !ok {
		return websiteapi.ErrUnauthorized
	}
	if err := s.authority.RequireGroupLevel(ctx, current.session, []string{s.config.AccessGroup}, minimum); err != nil {
		return mapCoreError(err)
	}
	return nil
}

func (s *Service) requireCoordinatorWrite(ctx context.Context) error {
	if s == nil || ctx == nil {
		return websiteapi.ErrUnauthorized
	}
	current, ok := ctx.Value(actorContextKey{}).(actor)
	if !ok {
		return websiteapi.ErrUnauthorized
	}
	if err := s.authority.RequireGroupLevel(ctx, current.session, []string{s.config.CoordinatorAccessGroup}, core.AccessLevelWrite); err != nil {
		return mapCoreError(err)
	}
	return nil
}

func mapCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrUnauthorized):
		return websiteapi.ErrUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return websiteapi.ErrForbidden
	case errors.Is(err, core.ErrModuleDataNotFound), errors.Is(err, core.ErrFileNotFound):
		return websiteapi.ErrNotFound
	case errors.Is(err, core.ErrModuleDataConflict), errors.Is(err, core.ErrRevisionConflict), errors.Is(err, core.ErrFileSetConflict):
		return websiteapi.ErrConflict
	case errors.Is(err, core.ErrModuleDataInvalid):
		return websiteapi.ErrInvalid
	case errors.Is(err, core.ErrModuleDataLimitExceeded):
		return websiteapi.ErrTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return websiteapi.ErrUnavailable
	}
}

func validInterface(value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !reflected.IsNil()
	default:
		return true
	}
}

var _ websiteapi.API = (*Service)(nil)
