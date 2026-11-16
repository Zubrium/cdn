package platform

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/cache"
	"github.com/Zubrium/cdn/core/identity"
	"github.com/Zubrium/cdn/core/internal/objectstore"
	"github.com/Zubrium/cdn/core/internal/postgres"
	"github.com/Zubrium/cdn/core/internal/storage"
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
}

const MaxS3PutObjectBytes = objectstore.MaxPutObjectBytes

// Session stores only a credential digest and deliberately formats as redacted.
type Session struct{ hash []byte }

func (Session) String() string                 { return "[redacted session]" }
func (Session) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte("[redacted session]")) }

func (r *Runtime) Authenticate(ctx context.Context, secret string) (Session, error) {
	if err := r.valid(); err != nil {
		return Session{}, err
	}
	if _, err := postgres.Authenticate(ctx, r.database, secret); err != nil {
		return Session{}, err
	}
	return Session{hash: identity.HashSecret(secret)}, nil
}

func (r *Runtime) actor(ctx context.Context, session Session) (access.Principal, error) {
	if err := r.valid(); err != nil {
		return access.Principal{}, err
	}
	return postgres.AuthenticateHash(ctx, r.database, session.hash)
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
	database       *pgxpool.Pool
	objects        *objectstore.S3
	files          storage.Service
	maxObjectBytes int64
	byteCache      *cache.Cache
	Registry       *Registry
	cancel         context.CancelFunc
	done           chan struct{}
	notifyDone     chan struct{}
	errors         chan error
	closeOnce      sync.Once
	closeErr       error
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
	secret, _, err := postgres.BootstrapWithGroups(ctx, pool, cfg.BootstrapSecret, cfg.AccessGroups)
	if err != nil {
		return nil, "", fmt.Errorf("bootstrap Core: %w", err)
	}
	byteCache := cache.NewByteBudget(int(cfg.CacheMaxBytes), time.Now)
	metadata := postgres.Objects{Pool: pool}
	writer := postgres.Creator{Pool: pool, Store: objects, MaxMetadataBytes: cfg.MaxMetadataBytes}
	files := storage.NewService(metadata, objects, writer, writer, writer, byteCache,
		cfg.MaxObjectBytes, cfg.MaxMetadataBytes, cfg.MaxCachedObjectBytes, cfg.CacheTTL)
	workerCtx, cancel := context.WithCancel(context.Background())
	r := &Runtime{
		database: pool, objects: objects, files: files, maxObjectBytes: cfg.MaxObjectBytes, byteCache: byteCache,
		Registry: NewRegistry(), cancel: cancel, done: make(chan struct{}), notifyDone: make(chan struct{}), errors: make(chan error, 8),
	}
	go r.maintain(workerCtx, cfg)
	go r.listenInvalidations(workerCtx)
	failed = false
	return r, secret, nil
}

func validateRuntimeConfig(cfg RuntimeConfig) error {
	if cfg.MigrationDatabaseURL == "" || cfg.AtlasBinary == "" || cfg.MigrationLockTimeout <= 0 || cfg.CacheMaxBytes <= 0 || int64(int(cfg.CacheMaxBytes)) != cfg.CacheMaxBytes || cfg.CacheTTL <= 0 ||
		cfg.MaxMetadataBytes <= 0 || cfg.MaxObjectBytes <= 0 || cfg.MaxCachedObjectBytes < 0 || cfg.MaxCachedObjectBytes > cfg.MaxObjectBytes || cfg.MaxCachedObjectBytes > cfg.CacheMaxBytes ||
		cfg.MaxObjectBytes > MaxS3PutObjectBytes || cfg.Database.MaxConnections < 2 || cfg.MaintenanceInterval <= 0 || cfg.MaintenanceTimeout <= 0 || cfg.PendingTTL <= 0 || cfg.MaintenanceBatchSize <= 0 {
		return errors.New("invalid core storage, Atlas, or maintenance configuration")
	}
	if err := ValidateDatabaseURLs(cfg.Database.URL, cfg.MigrationDatabaseURL); err != nil {
		return err
	}
	if cfg.AccessGroups == nil {
		return errors.New("core access_groups must be configured explicitly")
	}
	for key, level := range cfg.AccessGroups {
		if !storage.ValidGroupKey(key) || level > access.LevelWrite {
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
	if r == nil || r.database == nil {
		return errors.New("invalid core runtime")
	}
	r.closeOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
			if r.done != nil {
				<-r.done
			}
			if r.notifyDone != nil {
				<-r.notifyDone
			}
		}
		r.database.Close()
	})
	return r.closeErr
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
	_, secret, err := postgres.RecoverBootstrapCredential(ctx, pool)
	return secret, err
}
