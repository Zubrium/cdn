// Package application contains Website use cases backed by the scoped Core
// engine-data capability. It does not access SQL, S3, or another engine.
package application

import (
	"context"
	"errors"
	"io"
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

// ImportFilesCapability is the narrow, owner-independent Core file surface
// used by durable feature imports. Composition supplies it with a service
// identity; an import request cannot choose Core files or access groups.
type ImportFilesCapability interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
	ReadFile(context.Context, core.Session, string) (io.ReadCloser, core.File, error)
}

// ImportWorkflowClient is owner-scoped by composition. A request can only
// carry an opaque source file ID, never an operation, owner or task identity.
type ImportWorkflowClient interface {
	Start(context.Context, core.WorkflowStartRequest) (core.WorkflowReceipt, error)
	Get(context.Context, uuid.UUID) (core.Workflow, error)
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (core.TaskResult, error)
}

// MediaWorkflowClient is the owner-scoped Coordinator capability used by the
// durable Website media derivative workflow. Resolution of child tasks is
// required because Core assigns their IDs only when the root result commits.
type MediaWorkflowClient interface {
	Start(context.Context, core.WorkflowStartRequest) (core.WorkflowReceipt, error)
	Get(context.Context, uuid.UUID) (core.Workflow, error)
	GetTaskResult(context.Context, uuid.UUID, uuid.UUID) (core.TaskResult, error)
	ResolveTaskID(context.Context, uuid.UUID, string) (uuid.UUID, error)
}

// MediaFilesCapability is the narrow Core file surface used by media jobs.
// FileSet membership is enumerated through existing Core capabilities; Website
// never receives SQL, S3 or arbitrary storage paths.
type MediaFilesCapability interface {
	StatFile(context.Context, core.Session, string) (core.File, error)
	ReadFile(context.Context, core.Session, string) (io.ReadCloser, core.File, error)
	CreateFileVerified(context.Context, core.Session, string, []string, map[string]string, string, int64, string, io.Reader) (string, error)
	ListManagedFileSets(context.Context, core.Session, string, string, string, int) ([]core.FileSet, string, error)
	ListFileSet(context.Context, core.Session, string, string, int) ([]core.File, string, error)
}

// MediaSandboxProbe exposes the already verified OS isolation state of the
// composition-supplied media runner. It is not derived from request data.
type MediaSandboxProbe interface {
	MediaSandboxState() websiteapi.MediaSandboxState
}

type Config struct {
	AccessGroup                  string
	CoordinatorAccessGroup       string
	BuildWorkflow                BuildWorkflowClient
	LocaleCatalog                LocaleCatalogResolver
	BuildOperationID             string
	BuildContractVersion         int
	Files                        ImportFilesCapability
	Workflow                     ImportWorkflowClient
	Sessions                     core.SessionCapability
	FeatureImportOperationID     string
	FeatureImportContractVersion int
	FeatureImportFileSetID       string
	MediaWorkflow                MediaWorkflowClient
	MediaFiles                   MediaFilesCapability
	MediaOperationID             string
	MediaContractVersion         int
	MediaProcessorVersion        string
	MediaSandbox                 MediaSandboxProbe
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
	if strings.TrimSpace(config.FeatureImportOperationID) == "" {
		config.FeatureImportOperationID = importDefaultOperationID
	}
	if config.FeatureImportContractVersion == 0 {
		config.FeatureImportContractVersion = importContractVersion
	}
	return &Service{config: config, store: store, authority: authority, sessions: sessions}, nil
}

// MediaSandbox returns the verified isolation state of the configured media
// runner. It fails closed when composition did not supply a verified backend.
func (s *Service) MediaSandbox() (websiteapi.MediaSandboxState, error) {
	if s == nil || !validInterface(s.config.MediaSandbox) {
		return websiteapi.MediaSandboxState{}, websiteapi.ErrUnavailable
	}
	state := s.config.MediaSandbox.MediaSandboxState()
	if state.Backend == "" || !state.NetworkDenied || !state.ReadOnlyRootFS || !state.NoNewPrivs ||
		state.SeccompMode < 2 || state.CapEff == "" || state.CPUQuota == "" || state.MemoryLimitBytes <= 0 || state.PIDsLimit <= 0 || state.VerifiedAt.IsZero() {
		return websiteapi.MediaSandboxState{}, websiteapi.ErrUnavailable
	}
	return state, nil
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
	sessions := s.sessions
	if validInterface(s.config.Sessions) {
		sessions = s.config.Sessions
	}
	session, _, err := sessions.AuthenticateSession(ctx, token)
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
