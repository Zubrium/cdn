package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"websiteengine/pkg/postgres"
)

// Config is the complete runtime configuration. All values come from the
// environment; startup fails fast when a value is invalid.
type Config struct {
	HTTPAddr             string
	MaxHeaderBytes       int
	Branches             []BranchConfig
	DatabaseURL          string
	Database             postgres.PoolSettings
	MigrationLockTimeout time.Duration
	MigrateOnStart       bool
	BootstrapAdminToken  string
	LogLevel             string
	TrustProxyHeaders    bool

	MaxJSONBytes          int64
	MaxUploadBytes        int64
	FormRateLimit         int
	FormRateWindow        time.Duration
	FormRateMaxClients    int
	FormRateSweepInterval time.Duration

	ReadHeaderTimeout       time.Duration
	ShutdownTimeout         time.Duration
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	ReadinessTimeout        time.Duration
	S3StartupTimeout        time.Duration
	S3RetryInterval         time.Duration
	MediaRenditionLease     time.Duration
	MediaWorkerPollInterval time.Duration
	MediaRedirectCacheTTL   time.Duration
	RuntimeAssetCacheTTL    time.Duration

	S3 S3Config

	MaxFeatureBuildBytes            int64
	FeatureBuildRegistrationTimeout time.Duration
	DesignPreviewTTL                time.Duration
}

type DatabaseConfig struct {
	URL                  string
	Pool                 postgres.PoolSettings
	MigrationLockTimeout time.Duration
}

func LoadDatabase() (DatabaseConfig, error) {
	var cfg DatabaseConfig
	cfg.URL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if cfg.URL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required (see .env.example)")
	}
	if _, err := pgxpool.ParseConfig(cfg.URL); err != nil {
		return cfg, fmt.Errorf("DATABASE_URL must be a valid PostgreSQL connection string (see .env.example)")
	}
	for _, item := range []struct {
		key    string
		target *int32
	}{{"DATABASE_MAX_CONNECTIONS", &cfg.Pool.MaxConns}, {"DATABASE_MIN_CONNECTIONS", &cfg.Pool.MinConns}} {
		raw := strings.TrimSpace(os.Getenv(item.key))
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 0 || (item.key == "DATABASE_MAX_CONNECTIONS" && value == 0) {
			if item.key == "DATABASE_MIN_CONNECTIONS" {
				return cfg, fmt.Errorf("%s must be explicitly configured as a non-negative connection count", item.key)
			}
			return cfg, fmt.Errorf("%s must be explicitly configured as a positive connection count", item.key)
		}
		*item.target = int32(value)
	}
	for _, item := range []struct {
		key    string
		target *time.Duration
	}{{"DATABASE_MAX_CONNECTION_LIFETIME", &cfg.Pool.MaxConnLifetime}, {"DATABASE_MAX_CONNECTION_IDLE_TIME", &cfg.Pool.MaxConnIdleTime}, {"DATABASE_HEALTH_CHECK_INTERVAL", &cfg.Pool.HealthCheckPeriod}, {"DATABASE_CONNECT_TIMEOUT", &cfg.Pool.ConnectTimeout}, {"MIGRATION_LOCK_TIMEOUT", &cfg.MigrationLockTimeout}} {
		value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(item.key)))
		if err != nil || value <= 0 {
			return cfg, fmt.Errorf("%s must be explicitly configured as a positive duration", item.key)
		}
		*item.target = value
	}
	if cfg.Pool.MinConns > cfg.Pool.MaxConns {
		return cfg, fmt.Errorf("DATABASE_MIN_CONNECTIONS must not exceed DATABASE_MAX_CONNECTIONS")
	}
	return cfg, nil
}

type S3Config struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	PublicBaseURL  string
	ForcePathStyle bool
	EnsureBucket   bool
}

// Load reads and validates configuration from the process environment.
func Load() (Config, error) {
	database, err := LoadDatabase()
	if err != nil {
		return Config{}, err
	}
	for _, key := range []string{"HTTP_ADDR", "HTTP_MAX_HEADER_BYTES", "BRANCHES", "MAX_FEATURE_BUILD_BYTES", "FEATURE_BUILD_REGISTRATION_TIMEOUT", "MIGRATE_ON_START", "LOG_LEVEL", "TRUST_PROXY_HEADERS", "MAX_JSON_BYTES", "MAX_UPLOAD_BYTES", "FORM_RATE_LIMIT", "FORM_RATE_WINDOW", "FORM_RATE_MAX_CLIENTS", "FORM_RATE_SWEEP_INTERVAL", "READ_HEADER_TIMEOUT", "SHUTDOWN_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "HEALTH_CHECK_TIMEOUT", "S3_STARTUP_TIMEOUT", "S3_RETRY_INTERVAL", "MEDIA_RENDITION_LEASE", "MEDIA_WORKER_POLL_INTERVAL", "MEDIA_REDIRECT_CACHE_TTL", "RUNTIME_ASSET_CACHE_TTL", "DESIGN_PREVIEW_TTL", "S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_PUBLIC_BASE_URL", "S3_FORCE_PATH_STYLE", "S3_ENSURE_BUCKET"} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			return Config{}, fmt.Errorf("%s is required (see .env.example)", key)
		}
	}
	integers := make(map[string]int64)
	for _, key := range []string{"MAX_FEATURE_BUILD_BYTES", "MAX_JSON_BYTES", "MAX_UPLOAD_BYTES"} {
		value, err := strconv.ParseInt(os.Getenv(key), 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("%s must be an integer", key)
		}
		integers[key] = value
	}
	intValues := make(map[string]int)
	for _, key := range []string{"HTTP_MAX_HEADER_BYTES", "FORM_RATE_LIMIT", "FORM_RATE_MAX_CLIENTS"} {
		value, err := strconv.Atoi(os.Getenv(key))
		if err != nil {
			return Config{}, fmt.Errorf("%s must be an integer in the supported platform range", key)
		}
		intValues[key] = value
	}
	booleans := make(map[string]bool)
	for _, key := range []string{"MIGRATE_ON_START", "TRUST_PROXY_HEADERS", "S3_FORCE_PATH_STYLE", "S3_ENSURE_BUCKET"} {
		value, err := strconv.ParseBool(os.Getenv(key))
		if err != nil {
			return Config{}, fmt.Errorf("%s must be a boolean", key)
		}
		booleans[key] = value
	}
	durations := make(map[string]time.Duration)
	for _, key := range []string{"FORM_RATE_WINDOW", "FORM_RATE_SWEEP_INTERVAL", "READ_HEADER_TIMEOUT", "SHUTDOWN_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "HEALTH_CHECK_TIMEOUT", "S3_STARTUP_TIMEOUT", "S3_RETRY_INTERVAL", "MEDIA_RENDITION_LEASE", "MEDIA_WORKER_POLL_INTERVAL", "MEDIA_REDIRECT_CACHE_TTL", "RUNTIME_ASSET_CACHE_TTL", "FEATURE_BUILD_REGISTRATION_TIMEOUT", "DESIGN_PREVIEW_TTL"} {
		value, err := time.ParseDuration(os.Getenv(key))
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", key)
		}
		durations[key] = value
	}
	cfg := Config{
		HTTPAddr:                        os.Getenv("HTTP_ADDR"),
		MaxHeaderBytes:                  intValues["HTTP_MAX_HEADER_BYTES"],
		MaxFeatureBuildBytes:            integers["MAX_FEATURE_BUILD_BYTES"],
		DatabaseURL:                     database.URL,
		Database:                        database.Pool,
		MigrationLockTimeout:            database.MigrationLockTimeout,
		MigrateOnStart:                  booleans["MIGRATE_ON_START"],
		BootstrapAdminToken:             strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_TOKEN")),
		LogLevel:                        strings.ToLower(os.Getenv("LOG_LEVEL")),
		TrustProxyHeaders:               booleans["TRUST_PROXY_HEADERS"],
		MaxJSONBytes:                    integers["MAX_JSON_BYTES"],
		MaxUploadBytes:                  integers["MAX_UPLOAD_BYTES"],
		FormRateLimit:                   intValues["FORM_RATE_LIMIT"],
		FormRateWindow:                  durations["FORM_RATE_WINDOW"],
		FormRateMaxClients:              intValues["FORM_RATE_MAX_CLIENTS"],
		FormRateSweepInterval:           durations["FORM_RATE_SWEEP_INTERVAL"],
		ReadHeaderTimeout:               durations["READ_HEADER_TIMEOUT"],
		ShutdownTimeout:                 durations["SHUTDOWN_TIMEOUT"],
		ReadTimeout:                     durations["HTTP_READ_TIMEOUT"],
		WriteTimeout:                    durations["HTTP_WRITE_TIMEOUT"],
		IdleTimeout:                     durations["HTTP_IDLE_TIMEOUT"],
		ReadinessTimeout:                durations["HEALTH_CHECK_TIMEOUT"],
		S3StartupTimeout:                durations["S3_STARTUP_TIMEOUT"],
		S3RetryInterval:                 durations["S3_RETRY_INTERVAL"],
		MediaRenditionLease:             durations["MEDIA_RENDITION_LEASE"],
		MediaWorkerPollInterval:         durations["MEDIA_WORKER_POLL_INTERVAL"],
		MediaRedirectCacheTTL:           durations["MEDIA_REDIRECT_CACHE_TTL"],
		RuntimeAssetCacheTTL:            durations["RUNTIME_ASSET_CACHE_TTL"],
		FeatureBuildRegistrationTimeout: durations["FEATURE_BUILD_REGISTRATION_TIMEOUT"],
		DesignPreviewTTL:                durations["DESIGN_PREVIEW_TTL"],
		S3: S3Config{
			Endpoint:       os.Getenv("S3_ENDPOINT"),
			Region:         os.Getenv("S3_REGION"),
			Bucket:         os.Getenv("S3_BUCKET"),
			AccessKey:      os.Getenv("S3_ACCESS_KEY"),
			SecretKey:      os.Getenv("S3_SECRET_KEY"),
			PublicBaseURL:  os.Getenv("S3_PUBLIC_BASE_URL"),
			ForcePathStyle: booleans["S3_FORCE_PATH_STYLE"],
			EnsureBucket:   booleans["S3_ENSURE_BUCKET"],
		},
	}
	cfg.S3.PublicBaseURL = strings.TrimRight(cfg.S3.PublicBaseURL, "/")
	cfg.S3.Endpoint = strings.TrimRight(cfg.S3.Endpoint, "/")
	cfg.Branches, err = ParseBranches(os.Getenv("BRANCHES"), cfg.HTTPAddr)
	if err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.HTTPAddr == "" {
		return errors.New("HTTP_ADDR is required")
	}
	if c.MaxHeaderBytes < 1024 || c.MaxHeaderBytes > 16<<20 {
		return errors.New("HTTP_MAX_HEADER_BYTES must be between 1 KiB and 16 MiB")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL must be debug, info, warn or error")
	}
	if c.BootstrapAdminToken != "" && len(c.BootstrapAdminToken) < 24 {
		return errors.New("BOOTSTRAP_ADMIN_TOKEN must be at least 24 characters")
	}
	if c.MaxJSONBytes < 1024 || c.MaxJSONBytes > 64<<20 {
		return errors.New("MAX_JSON_BYTES must be between 1 KiB and 64 MiB")
	}
	if c.MaxUploadBytes < 1024 || c.MaxUploadBytes > 5<<30 {
		return errors.New("MAX_UPLOAD_BYTES must be between 1 KiB and 5 GiB")
	}
	if c.MaxFeatureBuildBytes < 1<<20 || c.MaxFeatureBuildBytes > 2<<30 {
		return errors.New("MAX_FEATURE_BUILD_BYTES must be between 1 MiB and 2 GiB")
	}
	if c.FormRateLimit < 1 {
		return errors.New("FORM_RATE_LIMIT must be positive")
	}
	if c.FormRateWindow <= 0 {
		return errors.New("FORM_RATE_WINDOW must be positive")
	}
	if c.FormRateMaxClients < 1 {
		return errors.New("FORM_RATE_MAX_CLIENTS must be positive")
	}
	if c.FormRateSweepInterval <= 0 {
		return errors.New("FORM_RATE_SWEEP_INTERVAL must be positive")
	}
	if c.MediaRedirectCacheTTL < time.Second || c.MediaRedirectCacheTTL%time.Second != 0 {
		return errors.New("MEDIA_REDIRECT_CACHE_TTL must be a positive whole number of seconds")
	}
	if c.RuntimeAssetCacheTTL < time.Second || c.RuntimeAssetCacheTTL%time.Second != 0 {
		return errors.New("RUNTIME_ASSET_CACHE_TTL must be a positive whole number of seconds")
	}
	if c.S3.Endpoint == "" || c.S3.Bucket == "" || c.S3.AccessKey == "" || c.S3.SecretKey == "" {
		return errors.New("S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	if !bucketPattern.MatchString(c.S3.Bucket) {
		return errors.New("S3_BUCKET must be a valid S3 bucket name (3-63 lowercase letters, digits, dots and hyphens)")
	}
	if c.S3.Region == "" {
		return errors.New("S3_REGION must not be empty")
	}
	if err := validateURL(c.S3.Endpoint, "S3_ENDPOINT"); err != nil {
		return err
	}
	if err := validateURL(c.S3.PublicBaseURL, "S3_PUBLIC_BASE_URL"); err != nil {
		return err
	}
	return nil
}

func validateURL(raw, name string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s must be an absolute http(s) URL", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", name)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must not contain credentials, a query or a fragment", name)
	}
	return nil
}

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
