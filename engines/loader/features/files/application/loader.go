package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zubrium/cdn/engines/loader/features/files/model"
	api "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	"github.com/Zubrium/cdn/engines/loader/features/files/repository"
	"github.com/Zubrium/cdn/pkg/contracts"
	"github.com/Zubrium/cdn/pkg/observability"
)

const moduleID = "loader"

const (
	ModeAuto                     = "auto"
	ModeStream                   = "streaming"
	ModeBuffered                 = "buffered"
	MinimumModuleDataIndexBytes  = 256
	ModuleDataBatchOverheadBytes = 2048
)

type Config struct {
	PartSize                    int64
	MaxFileBytes                int64
	MaxPartBytes                int64
	MaxParts                    int
	MaxConcurrentCommits        int
	MaxConcurrentSessionLookups int
	SessionTTL                  time.Duration
	CommitTimeout               time.Duration
	Mode                        string
	BufferThreshold             int64
	MaxBufferedBytes            int64
	SessionCacheEntries         int
	SessionCacheMaxBytes        int64
	SessionCacheTTL             time.Duration
	CleanupInterval             time.Duration
	CleanupBatchSize            int
}

type Identity = repository.Identity
type Files = repository.Files
type ModuleData = repository.ModuleData

type authKey struct{}

type authenticated struct {
	subject     contracts.AuthorizationSubject
	principalID string
}

type Service struct {
	identity       Identity
	files          Files
	data           ModuleData
	cfg            Config
	now            func() time.Time
	cache          *sessionCache
	bufferMu       sync.Mutex
	buffered       int64
	commitSlots    chan struct{}
	sessionLookups chan struct{}
	inFlight       atomic.Int64
	observer       *observability.Observer
	cleanupMu      sync.Mutex
	cleanupCursor  string
}

type storedPart = model.UploadPart
type storedUpload = model.Upload
type idempotencyRecord = model.IdempotencyRecord
type cleanupRecord = model.CleanupRecord

// RecordDescriptor is registered once by the composition root. Upload
// sessions, individual parts, idempotency records and cleanup jobs share one
// loader-owned namespace and one global scope so lifecycle changes can batch.
func RecordDescriptor() contracts.RecordDescriptor {
	return contracts.RecordDescriptor{OwnerID: moduleID, Namespaces: []contracts.RecordNamespace{{
		Name: "loader", ScopeTypes: []string{"global"}, Fields: map[string]contracts.RecordSelectorField{
			"kind": {Type: contracts.RecordSelectorString}, "upload_id": {Type: contracts.RecordSelectorString},
			"principal_id": {Type: contracts.RecordSelectorString}, "state": {Type: contracts.RecordSelectorString},
		},
	}}}
}

func New(identity Identity, files Files, data ModuleData, cfg Config) (*Service, error) {
	if identity == nil || files == nil || data == nil || cfg.PartSize <= 0 || cfg.MaxFileBytes <= 0 || cfg.MaxPartBytes <= 0 || cfg.PartSize > cfg.MaxPartBytes || cfg.MaxParts <= 0 || cfg.MaxParts > 1000 || cfg.SessionTTL <= 0 || cfg.CommitTimeout <= 0 || cfg.SessionTTL > time.Duration(1<<63-1)-cfg.CommitTimeout ||
		cfg.MaxConcurrentCommits <= 0 || cfg.MaxConcurrentCommits > 100 ||
		cfg.MaxConcurrentSessionLookups <= 0 || cfg.MaxConcurrentSessionLookups > 100 ||
		cfg.BufferThreshold < 0 || cfg.MaxBufferedBytes < 0 || cfg.SessionCacheEntries <= 0 || cfg.SessionCacheEntries > 100000 ||
		cfg.SessionCacheMaxBytes <= 0 || cfg.SessionCacheMaxBytes > 256<<20 || cfg.SessionCacheTTL <= 0 || cfg.CleanupInterval <= 0 || cfg.CleanupBatchSize <= 0 || data.MaxPageSize() <= 0 {
		return nil, errors.New("invalid Loader configuration")
	}
	if data.MaxBatchSize() < 3 || data.MaxPayloadBytes() <= 0 || data.MaxIndexBytes() < MinimumModuleDataIndexBytes ||
		data.MaxBatchBytes() < data.MaxPayloadBytes()+ModuleDataBatchOverheadBytes {
		return nil, errors.New("core module-data limits cannot store Loader transactions")
	}
	if cfg.Mode != ModeAuto && cfg.Mode != ModeStream && cfg.Mode != ModeBuffered {
		return nil, errors.New("invalid Loader upload mode")
	}
	if cfg.Mode == ModeBuffered && cfg.MaxBufferedBytes == 0 {
		return nil, errors.New("buffered Loader mode requires an explicit memory budget")
	}
	service := &Service{identity: identity, files: files, data: data, cfg: cfg, now: time.Now,
		commitSlots:    make(chan struct{}, cfg.MaxConcurrentCommits),
		sessionLookups: make(chan struct{}, cfg.MaxConcurrentSessionLookups),
		cache:          newSessionCache(cfg.SessionCacheEntries, cfg.SessionCacheMaxBytes, cfg.SessionCacheTTL)}
	return service, nil
}

// WithObserver attaches the process observer to the Loader service.
func (s *Service) WithObserver(observer *observability.Observer) *Service {
	if s != nil {
		s.observer = observer
	}
	return s
}

func (s *Service) observe(ctx context.Context, operationID string, fn func(context.Context) error) error {
	if s == nil || s.observer == nil {
		return fn(ctx)
	}
	return s.observer.Operation(ctx, observability.Identity{
		ModuleKind: observability.ModuleKindEngine, ModuleID: "loader", ComponentID: "files", OperationID: operationID,
	}, "loader."+operationID, nil, fn)
}

// concurrencyContext drops operation and trace attribution inherited from the
// caller so admission metrics stay low-cardinality.
func (s *Service) concurrencyContext() context.Context {
	return s.observer.WithIdentity(context.Background(), observability.Identity{
		ModuleKind: observability.ModuleKindEngine, ModuleID: moduleID, ComponentID: "files",
	})
}

func (s *Service) setInFlight(delta int64) {
	if s == nil || s.observer == nil {
		return
	}
	current := s.inFlight.Add(delta)
	s.observer.SetGauge(s.concurrencyContext(), "zubrium_concurrency_in_flight", float64(current), observability.String("scope", "LOADER"))
}

func (s *Service) reject(reason string) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.IncCounter(s.concurrencyContext(), "zubrium_concurrency_rejected_total", 1,
		observability.String("scope", "LOADER"), observability.String("reason", reason))
}

// acquireSessionLookupSlot reserves process-local session LRU capacity without
// waiting. A rejected lookup must leave the upload session untouched.
func (s *Service) acquireSessionLookupSlot(ctx context.Context) (func(), error) {
	if s == nil || s.sessionLookups == nil || ctx == nil {
		return nil, api.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		s.reject("GLOBAL")
		return nil, fmt.Errorf("%w: session lookup capacity unavailable", api.ErrUnavailable)
	}
	select {
	case s.sessionLookups <- struct{}{}:
	default:
		s.reject("GLOBAL")
		return nil, fmt.Errorf("%w: session lookup capacity exhausted", api.ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		<-s.sessionLookups
		return nil, fmt.Errorf("%w: session lookup capacity unavailable", api.ErrUnavailable)
	}
	s.setInFlight(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			s.setInFlight(-1)
			<-s.sessionLookups
		})
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	subject, principal, err := s.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	if !principal.Active || !validPrincipalID(principal.ID) {
		return nil, api.ErrUnauthorized
	}
	return context.WithValue(ctx, authKey{}, authenticated{subject: subject, principalID: principal.ID}), nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if s == nil || s.identity == nil || ctx == nil || token == "" {
		return nil, api.ErrUnauthorized
	}
	subject, principal, err := s.identity.AuthenticateSession(ctx, token)
	if err != nil {
		return nil, mapCoreError(err)
	}
	if !principal.Active || !validPrincipalID(principal.ID) {
		return nil, api.ErrUnauthorized
	}
	return context.WithValue(ctx, authKey{}, authenticated{subject: subject, principalID: principal.ID}), nil
}

type countingReader struct {
	reader io.Reader
	hash   io.Writer
	count  int64
}

var _ api.API = (*Service)(nil)
