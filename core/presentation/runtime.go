package presentation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	appcoord "github.com/Zubrium/cdn/core/application/coordination"
	appidentity "github.com/Zubrium/cdn/core/application/identity"
	appmaintenance "github.com/Zubrium/cdn/core/application/maintenance"
	"github.com/Zubrium/cdn/core/application/storage"
	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/Zubrium/cdn/core/domain/engine_data"
	"github.com/Zubrium/cdn/core/infrastructure/cache"
	"github.com/Zubrium/cdn/core/infrastructure/identity"
	"github.com/Zubrium/cdn/core/infrastructure/objectstore"
	"github.com/Zubrium/cdn/core/infrastructure/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RuntimeConfig struct {
	Database             DatabaseConfig
	MigrationDatabaseURL string
	S3                   S3Config
	BootstrapSecret      string
	AtlasBinary          string
	MigrationLockTimeout time.Duration
	AccessGroups         map[string]access.Level
	CacheMaxBytes        int64
	CacheTTL             time.Duration
	MaxMetadataBytes     int64
	MaxObjectBytes       int64
	MaxCachedObjectBytes int64
	MaintenanceInterval  time.Duration
	MaintenanceTimeout   time.Duration
	PendingTTL           time.Duration
	MaintenanceBatchSize int
	SessionIdleTTL       time.Duration
	SessionAbsoluteTTL   time.Duration
	ModuleData           ModuleDataLimits
}

const MaxS3PutObjectBytes = objectstore.MaxPutObjectBytes

// MinRuntimeDatabaseConnections leaves one connection for LISTEN and two for
// one concurrent streaming upload's staged read and final object write.
const MinRuntimeDatabaseConnections int32 = 3

// Session stores only an authentication digest and deliberately formats as redacted.
type Session struct {
	hash      []byte
	persisted bool
}

func (Session) String() string                 { return "[redacted session]" }
func (Session) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte("[redacted session]")) }

func (r *Runtime) Authenticate(ctx context.Context, secret string) (Session, error) {
	session, _, err := r.authenticatePrincipal(ctx, secret)
	return session, err
}

// CreatedSession is the one-time result of exchanging a Core credential for
// a revocable session bound to that exact credential.
type CreatedSession struct {
	Token             string
	Principal         Principal
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

func (r *Runtime) CreateSession(ctx context.Context, credential string) (CreatedSession, error) {
	if err := r.valid(); err != nil {
		return CreatedSession{}, err
	}
	if r.sessions == nil {
		return CreatedSession{}, ErrUnavailable
	}
	created, err := r.sessions.Create(ctx, credential)
	if err != nil {
		return CreatedSession{}, err
	}
	return CreatedSession{Token: created.Token, Principal: created.Session.Principal,
		IdleExpiresAt: created.Session.IdleExpiresAt, AbsoluteExpiresAt: created.Session.AbsoluteExpires}, nil
}

func (r *Runtime) AuthenticateSession(ctx context.Context, token string) (Session, Principal, error) {
	if err := r.valid(); err != nil {
		return Session{}, Principal{}, err
	}
	if r.sessions == nil {
		return Session{}, Principal{}, ErrUnavailable
	}
	resolved, err := r.sessions.Authenticate(ctx, token)
	if err != nil {
		return Session{}, Principal{}, err
	}
	return Session{hash: identity.HashSecret(token), persisted: true}, resolved.Principal, nil
}

func (r *Runtime) RevokeSession(ctx context.Context, token string) error {
	if err := r.valid(); err != nil {
		return err
	}
	if r.sessions == nil {
		return ErrUnavailable
	}
	return r.sessions.Revoke(ctx, token)
}

func (r *Runtime) authenticatePrincipal(ctx context.Context, secret string) (Session, access.Principal, error) {
	if err := r.valid(); err != nil {
		return Session{}, access.Principal{}, err
	}
	if r.identities == nil {
		return Session{}, access.Principal{}, ErrUnavailable
	}
	principal, err := r.identities.Authenticate(ctx, secret)
	if err != nil {
		return Session{}, access.Principal{}, err
	}
	return Session{hash: identity.HashSecret(secret)}, principal, nil
}

func (r *Runtime) actor(ctx context.Context, session Session) (access.Principal, error) {
	if err := r.valid(); err != nil {
		return access.Principal{}, err
	}
	if r.identities == nil {
		return access.Principal{}, ErrUnavailable
	}
	if session.persisted {
		if r.sessions == nil {
			return access.Principal{}, ErrUnavailable
		}
		resolved, err := r.sessions.ResolveHash(ctx, session.hash)
		if err != nil {
			return access.Principal{}, err
		}
		return resolved.Principal, nil
	}
	return r.identities.AuthenticateHash(ctx, session.hash)
}

type DatabaseConfig struct {
	URL                                                      string
	MinConnections, MaxConnections                           int32
	ConnectTimeout, MaxLifetime, MaxIdleTime, HealthInterval time.Duration
}

type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	PathStyle                                      bool
	AllowInsecureHTTP                              bool
}

type Runtime struct {
	database                   *pgxpool.Pool
	objects                    *objectstore.S3
	files                      storage.Service
	fileIndex                  storage.IndexService
	staging                    storage.StagingService
	engineDataRepository       engine_data.Repository
	identities                 *appidentity.Service
	sessions                   *appidentity.SessionService
	maintenance                *appmaintenance.Service
	byteCache                  *cache.Cache
	Registry                   *Registry
	cancel                     context.CancelFunc
	done                       chan struct{}
	notifyDone                 chan struct{}
	errors                     chan error
	closeMu                    sync.Mutex
	closed                     bool
	moduleDataMu               sync.Mutex
	moduleData                 map[string]struct{}
	moduleDataLimits           ModuleDataLimits
	coordinatorMu              sync.RWMutex
	coordinator                *appcoord.Service
	coordinatorStarting        bool
	coordinatorShutdownTimeout time.Duration
}

// Start verifies the private storage boundary, initializes a fresh Atlas schema
// if necessary, and creates the bootstrap account only after migration succeeds.
func Start(ctx context.Context, cfg RuntimeConfig) (*Runtime, string, error) {
	if err := validateRuntimeConfig(cfg); err != nil {
		return nil, "", err
	}
	objects, err := objectstore.NewS3(ctx, objectstore.S3Config(cfg.S3))
	if err != nil {
		return nil, "", err
	}
	if err := objects.RequirePrivate(ctx); err != nil {
		return nil, "", err
	}
	migrationDatabase := cfg.Database
	migrationDatabase.URL = cfg.MigrationDatabaseURL
	migrationDatabase.MinConnections = 0
	migrationDatabase.MaxConnections = 2
	migrationPool, err := postgres.Open(ctx, postgres.PoolConfig(migrationDatabase))
	if err != nil {
		return nil, "", fmt.Errorf("open Core migration database: %w", err)
	}
	if err := postgres.Migrate(ctx, migrationPool, cfg.AtlasBinary, cfg.MigrationLockTimeout); err != nil {
		migrationPool.Close()
		return nil, "", fmt.Errorf("migrate Core schema with Atlas: %w", err)
	}
	migrationPool.Close()
	pool, err := postgres.Open(ctx, postgres.PoolConfig(cfg.Database))
	if err != nil {
		return nil, "", err
	}
	if err := postgres.ValidateRuntimeAccess(ctx, pool); err != nil {
		pool.Close()
		return nil, "", err
	}
	failed := true
	defer func() {
		if failed {
			pool.Close()
		}
	}()
	identities, err := appidentity.NewService(postgres.IdentityRepository{Pool: pool})
	if err != nil {
		pool.Close()
		return nil, "", fmt.Errorf("initialize Core identity service: %w", err)
	}
	sessions, err := appidentity.NewSessionService(postgres.IdentityRepository{Pool: pool}, cfg.SessionIdleTTL, cfg.SessionAbsoluteTTL)
	if err != nil {
		pool.Close()
		return nil, "", fmt.Errorf("initialize Core sessions service: %w", err)
	}
	secret, _, err := identities.Bootstrap(ctx, cfg.BootstrapSecret, cfg.AccessGroups)
	if err != nil {
		return nil, "", fmt.Errorf("bootstrap Core: %w", err)
	}
	byteCache := cache.NewByteBudget(int(cfg.CacheMaxBytes), time.Now)
	metadata := postgres.Objects{Pool: pool}
	writer := postgres.Creator{Pool: pool, Store: objects, MaxMetadataBytes: cfg.MaxMetadataBytes}
	files := storage.NewService(metadata, objects, writer, writer, writer, byteCache,
		cfg.MaxObjectBytes, cfg.MaxMetadataBytes, cfg.MaxCachedObjectBytes, cfg.CacheTTL)
	fileIndex := storage.NewIndexService(postgres.FileIndexRepository{Pool: pool})
	staging := storage.NewStagingService(postgres.StagingRepository{Pool: pool, Store: objects, MaxBytes: cfg.MaxObjectBytes}, cfg.MaxObjectBytes)
	engineDataRepository := postgres.EngineDataRepository{Pool: pool}
	maintenanceService, err := appmaintenance.NewService(
		postgres.MaintenanceRepository{Pool: pool, Store: objects}, cfg.PendingTTL, cfg.MaintenanceBatchSize)
	if err != nil {
		return nil, "", fmt.Errorf("initialize Core maintenance service: %w", err)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	r := &Runtime{
		database: pool, objects: objects, files: files, fileIndex: fileIndex, staging: staging,
		engineDataRepository: engineDataRepository,
		identities:           identities, sessions: sessions, maintenance: maintenanceService,
		byteCache:        byteCache,
		moduleDataLimits: cfg.ModuleData,
		Registry:         NewRegistry(), cancel: cancel, done: make(chan struct{}), notifyDone: make(chan struct{}), errors: make(chan error, 8),
	}
	go r.maintain(workerCtx, cfg)
	go r.listenInvalidations(workerCtx)
	failed = false
	return r, secret, nil
}

func validateRuntimeConfig(cfg RuntimeConfig) error {
	if cfg.MigrationDatabaseURL == "" || cfg.AtlasBinary == "" || cfg.MigrationLockTimeout <= 0 || cfg.CacheMaxBytes <= 0 || int64(int(cfg.CacheMaxBytes)) != cfg.CacheMaxBytes || cfg.CacheTTL <= 0 ||
		cfg.MaxMetadataBytes <= 0 || cfg.MaxMetadataBytes > 16<<20 || cfg.MaxObjectBytes <= 0 || cfg.MaxCachedObjectBytes < 0 || cfg.MaxCachedObjectBytes > cfg.MaxObjectBytes || cfg.MaxCachedObjectBytes > cfg.CacheMaxBytes ||
		cfg.MaxObjectBytes > MaxS3PutObjectBytes || cfg.Database.MaxConnections < MinRuntimeDatabaseConnections || cfg.MaintenanceInterval <= 0 || cfg.MaintenanceTimeout <= 0 || cfg.PendingTTL <= 0 || cfg.MaintenanceBatchSize <= 0 ||
		cfg.SessionIdleTTL <= 0 || cfg.SessionAbsoluteTTL < cfg.SessionIdleTTL || cfg.SessionAbsoluteTTL > 365*24*time.Hour || !ValidModuleDataLimits(cfg.ModuleData) {
		return errors.New("invalid core storage, Atlas, or maintenance configuration")
	}
	if err := ValidateDatabaseURLs(cfg.Database.URL, cfg.MigrationDatabaseURL); err != nil {
		return err
	}
	if cfg.AccessGroups == nil {
		return errors.New("core access_groups must be configured explicitly")
	}
	for key, level := range cfg.AccessGroups {
		if !ValidAccessGroupKey(key) || level > access.LevelWrite {
			return errors.New("invalid configured access group")
		}
	}
	return nil
}

// ValidateDatabaseURLs keeps runtime DML and Atlas schema privileges on
// separate PostgreSQL identities while ensuring both credentials target the
// same database name.
func ValidateDatabaseURLs(runtimeURL, migrationURL string) error {
	runtimeDB, runtimeErr := pgxpool.ParseConfig(runtimeURL)
	migrationDB, migrationErr := pgxpool.ParseConfig(migrationURL)
	if runtimeErr != nil || migrationErr != nil || runtimeDB.ConnConfig.Database == "" ||
		runtimeDB.ConnConfig.Database != migrationDB.ConnConfig.Database ||
		runtimeDB.ConnConfig.Host != migrationDB.ConnConfig.Host || runtimeDB.ConnConfig.Port != migrationDB.ConnConfig.Port ||
		runtimeDB.ConnConfig.User == migrationDB.ConnConfig.User {
		return errors.New("core runtime and Atlas migration URLs must target the same PostgreSQL server and database with separate users")
	}
	return nil
}

func (r *Runtime) Close() error {
	if r == nil {
		return errors.New("invalid core runtime")
	}
	timeout := r.coordinatorShutdownDuration()
	if timeout <= 0 {
		return r.CloseContext(context.Background())
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.CloseContext(ctx)
}

// CloseContext stops the Coordinator and all Core background processes before
// closing PostgreSQL. A timed out shutdown leaves the pool open so cooperative
// task handlers can finish safely and a later call can await cleanup.
func (r *Runtime) CloseContext(ctx context.Context) error {
	if r == nil || r.database == nil || ctx == nil {
		return errors.New("invalid core runtime")
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return nil
	}
	if err := r.closeCoordinator(ctx); err != nil {
		return err
	}
	if r.cancel != nil {
		r.cancel()
		for _, done := range []<-chan struct{}{r.done, r.notifyDone} {
			if done == nil {
				continue
			}
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	r.database.Close()
	r.closed = true
	return nil
}

func (r *Runtime) coordinatorShutdownDuration() time.Duration {
	if r == nil {
		return 0
	}
	r.coordinatorMu.RLock()
	defer r.coordinatorMu.RUnlock()
	return r.coordinatorShutdownTimeout
}

func (r *Runtime) valid() error {
	if r == nil || r.database == nil {
		return errors.New("invalid core runtime")
	}
	return nil
}

// RecoverBootstrap is an operator-only recovery utility. It assumes the fresh
// database schema is already installed and never runs DDL itself.
func RecoverBootstrap(ctx context.Context, cfg DatabaseConfig) (string, error) {
	pool, err := postgres.Open(ctx, postgres.PoolConfig(cfg))
	if err != nil {
		return "", err
	}
	defer pool.Close()
	identities, err := appidentity.NewService(postgres.IdentityRepository{Pool: pool})
	if err != nil {
		return "", err
	}
	return identities.RecoverBootstrapCredential(ctx)
}
