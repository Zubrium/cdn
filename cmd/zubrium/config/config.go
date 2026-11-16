package appconfig

import (
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"time"

	"github.com/Zubrium/cdn/core/access"
	"github.com/Zubrium/cdn/core/platform"

	"gopkg.in/yaml.v3"
)

type Core struct {
	DatabaseURLEnv          string                  `yaml:"database_url_env"`
	MigrationDatabaseURLEnv string                  `yaml:"migration_database_url_env"`
	S3Endpoint              string                  `yaml:"s3_endpoint"`
	S3Region                string                  `yaml:"s3_region"`
	S3Bucket                string                  `yaml:"s3_bucket"`
	S3AccessKeyEnv          string                  `yaml:"s3_access_key_env"`
	S3SecretKeyEnv          string                  `yaml:"s3_secret_key_env"`
	S3PathStyle             bool                    `yaml:"s3_path_style"`
	S3AllowInsecureHTTP     bool                    `yaml:"s3_allow_insecure_http"`
	BootstrapSecretEnv      string                  `yaml:"bootstrap_secret_env"`
	AtlasBinary             string                  `yaml:"atlas_binary"`
	MigrationLockTimeout    string                  `yaml:"migration_lock_timeout"`
	AccessGroups            map[string]access.Level `yaml:"access_groups"`
	MinConnections          int32                   `yaml:"min_connections"`
	MaxConnections          int32                   `yaml:"max_connections"`
	ConnectTimeout          string                  `yaml:"connect_timeout"`
	MaxLifetime             string                  `yaml:"max_lifetime"`
	MaxIdleTime             string                  `yaml:"max_idle_time"`
	HealthInterval          string                  `yaml:"health_interval"`
	CacheMaxBytes           int64                   `yaml:"cache_max_bytes"`
	CacheTTL                string                  `yaml:"cache_ttl"`
	MaxMetadataBytes        int64                   `yaml:"max_metadata_bytes"`
	MaxObjectBytes          int64                   `yaml:"max_object_bytes"`
	MaxCachedObjectBytes    int64                   `yaml:"max_cached_object_bytes"`
	MaintenanceInterval     string                  `yaml:"maintenance_interval"`
	MaintenanceTimeout      string                  `yaml:"maintenance_timeout"`
	PendingTTL              string                  `yaml:"pending_ttl"`
	MaintenanceBatchSize    int                     `yaml:"maintenance_batch_size"`
}

// Resolve reads secrets only from the configured environment variables.
func (c Config) Resolve(getenv func(string) string) (platform.RuntimeConfig, error) {
	if getenv == nil {
		return platform.RuntimeConfig{}, errors.New("missing environment lookup")
	}
	durations := []string{c.Core.ConnectTimeout, c.Core.MaxLifetime, c.Core.MaxIdleTime, c.Core.HealthInterval, c.Core.MigrationLockTimeout, c.Core.CacheTTL, c.Core.MaintenanceInterval, c.Core.MaintenanceTimeout, c.Core.PendingTTL}
	var parsed [9]time.Duration
	for i, value := range durations {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return platform.RuntimeConfig{}, errors.New("invalid database duration")
		}
		parsed[i] = duration
	}
	if c.Core.MinConnections < 0 || c.Core.MaxConnections < 2 || c.Core.MinConnections > c.Core.MaxConnections || c.Core.MaxObjectBytes <= 0 || c.Core.MaxObjectBytes > platform.MaxS3PutObjectBytes || c.Core.MaxMetadataBytes <= 0 || c.Core.MaxCachedObjectBytes < 0 || c.Core.MaxCachedObjectBytes > c.Core.MaxObjectBytes || c.Core.CacheMaxBytes <= 0 || c.Core.MaxCachedObjectBytes > c.Core.CacheMaxBytes || c.Core.MaintenanceBatchSize <= 0 || c.Core.AtlasBinary == "" {
		return platform.RuntimeConfig{}, errors.New("invalid Core cache, object, or database pool configuration")
	}
	for key, level := range c.Core.AccessGroups {
		if !access.ValidGroupKey(key) || level > access.LevelWrite {
			return platform.RuntimeConfig{}, errors.New("invalid configured access group")
		}
	}
	if c.Core.BootstrapSecretEnv != "" && !envName.MatchString(c.Core.BootstrapSecretEnv) {
		return platform.RuntimeConfig{}, errors.New("invalid bootstrap environment name")
	}
	if !envName.MatchString(c.Core.DatabaseURLEnv) || !envName.MatchString(c.Core.MigrationDatabaseURLEnv) || !envName.MatchString(c.Core.S3AccessKeyEnv) || !envName.MatchString(c.Core.S3SecretKeyEnv) {
		return platform.RuntimeConfig{}, errors.New("invalid secret environment name")
	}
	dbURL, migrationDBURL := getenv(c.Core.DatabaseURLEnv), getenv(c.Core.MigrationDatabaseURLEnv)
	accessKey, secretKey := getenv(c.Core.S3AccessKeyEnv), getenv(c.Core.S3SecretKeyEnv)
	if dbURL == "" || migrationDBURL == "" || accessKey == "" || secretKey == "" {
		return platform.RuntimeConfig{}, errors.New("required secret environment variable is empty")
	}
	if err := platform.ValidateDatabaseURLs(dbURL, migrationDBURL); err != nil {
		return platform.RuntimeConfig{}, err
	}
	if c.Core.AccessGroups == nil {
		return platform.RuntimeConfig{}, errors.New("Core access_groups must be configured explicitly")
	}
	var bootstrap string
	if c.Core.BootstrapSecretEnv != "" {
		bootstrap = getenv(c.Core.BootstrapSecretEnv)
		if bootstrap == "" {
			return platform.RuntimeConfig{}, errors.New("configured bootstrap secret is empty")
		}
	}
	return platform.RuntimeConfig{
		Database:             platform.DatabaseConfig{URL: dbURL, MinConnections: c.Core.MinConnections, MaxConnections: c.Core.MaxConnections, ConnectTimeout: parsed[0], MaxLifetime: parsed[1], MaxIdleTime: parsed[2], HealthInterval: parsed[3]},
		MigrationDatabaseURL: migrationDBURL,
		S3:                   platform.S3Config{Endpoint: c.Core.S3Endpoint, Region: c.Core.S3Region, Bucket: c.Core.S3Bucket, AccessKey: accessKey, SecretKey: secretKey, PathStyle: c.Core.S3PathStyle, AllowInsecureHTTP: c.Core.S3AllowInsecureHTTP},
		BootstrapSecret:      bootstrap, AtlasBinary: c.Core.AtlasBinary, MigrationLockTimeout: parsed[4], AccessGroups: c.Core.AccessGroups,
		CacheMaxBytes: c.Core.CacheMaxBytes, CacheTTL: parsed[5], MaxMetadataBytes: c.Core.MaxMetadataBytes,
		MaxObjectBytes: c.Core.MaxObjectBytes, MaxCachedObjectBytes: c.Core.MaxCachedObjectBytes,
		MaintenanceInterval: parsed[6], MaintenanceTimeout: parsed[7], PendingTTL: parsed[8], MaintenanceBatchSize: c.Core.MaintenanceBatchSize,
	}, nil
}

type Config struct {
	Core Core `yaml:"core"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Load rejects unknown keys and multiple documents. Sensitive values are
// referenced by environment variable name, never embedded in YAML.
func Load(input io.Reader) (Config, error) {
	decoder := yaml.NewDecoder(input)
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("expected exactly one YAML document")
	}
	u, err := url.Parse(cfg.Core.S3Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || cfg.Core.S3Region == "" || cfg.Core.S3Bucket == "" || !envName.MatchString(cfg.Core.DatabaseURLEnv) || !envName.MatchString(cfg.Core.S3AccessKeyEnv) || !envName.MatchString(cfg.Core.S3SecretKeyEnv) {
		return Config{}, errors.New("incomplete or invalid core configuration")
	}
	if !envName.MatchString(cfg.Core.MigrationDatabaseURLEnv) {
		return Config{}, errors.New("invalid migration database environment name")
	}
	if u.Scheme == "http" && (!cfg.Core.S3AllowInsecureHTTP || !isLoopback(u.Hostname())) {
		return Config{}, errors.New("unencrypted S3 endpoints must use loopback")
	}
	return cfg, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
