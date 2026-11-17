package application

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	core "github.com/Zubrium/cdn/core/platform"
	"github.com/Zubrium/cdn/engines/loader/features/files/model"
	api "github.com/Zubrium/cdn/engines/loader/features/files/pkg"
	"github.com/Zubrium/cdn/engines/loader/features/files/repository"
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
	PartSize             int64
	MaxFileBytes         int64
	MaxPartBytes         int64
	MaxParts             int
	MaxConcurrentCommits int
	SessionTTL           time.Duration
	CommitTimeout        time.Duration
	Mode                 string
	BufferThreshold      int64
	MaxBufferedBytes     int64
	SessionCacheEntries  int
	SessionCacheMaxBytes int64
	SessionCacheTTL      time.Duration
	CleanupInterval      time.Duration
	CleanupBatchSize     int
}

type Identity = repository.Identity
type Files = repository.Files
type ModuleData = repository.ModuleData

type authKey struct{}

type authenticated struct {
	session     core.Session
	principalID string
}

type Service struct {
	identity      Identity
	files         Files
	data          ModuleData
	cfg           Config
	now           func() time.Time
	cache         *sessionCache
	bufferMu      sync.Mutex
	buffered      int64
	commitSlots   chan struct{}
	cleanupMu     sync.Mutex
	cleanupCursor string
}

type storedPart = model.UploadPart
type storedUpload = model.Upload
type idempotencyRecord = model.IdempotencyRecord
type cleanupRecord = model.CleanupRecord

// ModuleDataDescriptor is registered once by the composition root. Upload
// sessions, individual parts, idempotency records and cleanup jobs share one
// loader-owned namespace and one global scope so lifecycle changes can batch.
func ModuleDataDescriptor() core.ModuleDataDescriptor {
	return core.ModuleDataDescriptor{ModuleID: moduleID, Namespaces: []core.ModuleDataNamespace{{
		Name: "loader", ScopeTypes: []string{"global"}, Fields: map[string]core.SelectorField{
			"kind": {Type: core.SelectorString}, "upload_id": {Type: core.SelectorString},
			"principal_id": {Type: core.SelectorString}, "state": {Type: core.SelectorString},
		},
	}}}
}

func New(identity Identity, files Files, data ModuleData, cfg Config) (*Service, error) {
	if identity == nil || files == nil || data == nil || cfg.PartSize <= 0 || cfg.MaxFileBytes <= 0 || cfg.MaxPartBytes <= 0 || cfg.PartSize > cfg.MaxPartBytes || cfg.MaxParts <= 0 || cfg.MaxParts > 1000 || cfg.SessionTTL <= 0 || cfg.CommitTimeout <= 0 || cfg.SessionTTL > time.Duration(1<<63-1)-cfg.CommitTimeout ||
		cfg.MaxConcurrentCommits <= 0 || cfg.MaxConcurrentCommits > 100 || cfg.BufferThreshold < 0 || cfg.MaxBufferedBytes < 0 || cfg.SessionCacheEntries <= 0 || cfg.SessionCacheEntries > 100000 ||
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
	return &Service{identity: identity, files: files, data: data, cfg: cfg, now: time.Now, commitSlots: make(chan struct{}, cfg.MaxConcurrentCommits),
		cache: newSessionCache(cfg.SessionCacheEntries, cfg.SessionCacheMaxBytes, cfg.SessionCacheTTL)}, nil
}

func (s *Service) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	session, principal, err := s.identity.Authenticate(ctx, credential)
	if err != nil {
		return nil, mapCoreError(err)
	}
	if !principal.Active || !validPrincipalID(principal.ID) {
		return nil, api.ErrUnauthorized
	}
	return context.WithValue(ctx, authKey{}, authenticated{session: session, principalID: principal.ID}), nil
}

type countingReader struct {
	reader io.Reader
	hash   io.Writer
	count  int64
}

var _ api.API = (*Service)(nil)
