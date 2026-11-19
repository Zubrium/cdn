package appconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	platform "github.com/Zubrium/cdn/core/presentation"
	loaderapp "github.com/Zubrium/cdn/engines/loader/features/files/application"
	routermodel "github.com/Zubrium/cdn/engines/router/features/routes/model"
	"github.com/Zubrium/cdn/presentation/fileapi"
	"gopkg.in/yaml.v3"
)

type Core struct {
	DatabaseURLEnv          string                           `yaml:"database_url_env"`
	MigrationDatabaseURLEnv string                           `yaml:"migration_database_url_env"`
	S3Endpoint              string                           `yaml:"s3_endpoint"`
	S3Region                string                           `yaml:"s3_region"`
	S3Bucket                string                           `yaml:"s3_bucket"`
	S3AccessKeyEnv          string                           `yaml:"s3_access_key_env"`
	S3SecretKeyEnv          string                           `yaml:"s3_secret_key_env"`
	S3PathStyle             bool                             `yaml:"s3_path_style"`
	S3AllowInsecureHTTP     bool                             `yaml:"s3_allow_insecure_http"`
	BootstrapSecretEnv      string                           `yaml:"bootstrap_secret_env"`
	AtlasBinary             string                           `yaml:"atlas_binary"`
	MigrationLockTimeout    string                           `yaml:"migration_lock_timeout"`
	AccessGroups            map[string]*platform.AccessLevel `yaml:"access_groups"`
	MinConnections          int32                            `yaml:"min_connections"`
	MaxConnections          int32                            `yaml:"max_connections"`
	ConnectTimeout          string                           `yaml:"connect_timeout"`
	MaxLifetime             string                           `yaml:"max_lifetime"`
	MaxIdleTime             string                           `yaml:"max_idle_time"`
	HealthInterval          string                           `yaml:"health_interval"`
	CacheMaxBytes           int64                            `yaml:"cache_max_bytes"`
	CacheTTL                string                           `yaml:"cache_ttl"`
	MaxMetadataBytes        int64                            `yaml:"max_metadata_bytes"`
	MaxObjectBytes          int64                            `yaml:"max_object_bytes"`
	MaxCachedObjectBytes    int64                            `yaml:"max_cached_object_bytes"`
	MaintenanceInterval     string                           `yaml:"maintenance_interval"`
	MaintenanceTimeout      string                           `yaml:"maintenance_timeout"`
	PendingTTL              string                           `yaml:"pending_ttl"`
	MaintenanceBatchSize    int                              `yaml:"maintenance_batch_size"`
	SessionIdleTTL          string                           `yaml:"session_idle_ttl"`
	SessionAbsoluteTTL      string                           `yaml:"session_absolute_ttl"`
	ModuleData              ModuleData                       `yaml:"module_data"`
}

type ModuleData struct {
	MaxPayloadBytes    int `yaml:"max_payload_bytes"`
	MaxIndexBytes      int `yaml:"max_index_bytes"`
	MaxBatchBytes      int `yaml:"max_batch_bytes"`
	MaxPageBytes       int `yaml:"max_page_bytes"`
	MaxSelectors       int `yaml:"max_selectors"`
	MaxSelectorValues  int `yaml:"max_selector_values"`
	MaxPageSize        int `yaml:"max_page_size"`
	MaxBatchOperations int `yaml:"max_batch_operations"`
}

type Loader struct {
	PartSize             int64  `yaml:"part_size_bytes"`
	MaxFileBytes         int64  `yaml:"max_file_bytes"`
	MaxPartBytes         int64  `yaml:"max_part_bytes"`
	MaxParts             int    `yaml:"max_parts"`
	MaxConcurrentCommits int    `yaml:"max_concurrent_commits"`
	SessionTTL           string `yaml:"session_ttl"`
	CommitTimeout        string `yaml:"commit_timeout"`
	Mode                 string `yaml:"mode"`
	BufferThreshold      int64  `yaml:"buffer_threshold_bytes"`
	MaxBufferedBytes     int64  `yaml:"max_buffered_bytes"`
	SessionCacheEntries  int    `yaml:"session_cache_entries"`
	SessionCacheMaxBytes int64  `yaml:"session_cache_max_bytes"`
	SessionCacheTTL      string `yaml:"session_cache_ttl"`
	CleanupInterval      string `yaml:"cleanup_interval"`
	CleanupBatchSize     int    `yaml:"cleanup_batch_size"`
}

type FileAPI struct {
	MaxJSONBytes      int64 `yaml:"max_json_bytes"`
	MaxAttributeBytes int64 `yaml:"max_attribute_bytes"`
	MaxGroupsPerFile  int   `yaml:"max_groups_per_file"`
	DefaultPageSize   int   `yaml:"default_page_size"`
	MaxPageSize       int   `yaml:"max_page_size"`
}

type AdminAPI struct {
	MaxJSONBytes        int64    `yaml:"max_json_bytes"`
	DefaultPageSize     int      `yaml:"default_page_size"`
	MaxPageSize         int      `yaml:"max_page_size"`
	SessionCookieSecure *bool    `yaml:"session_cookie_secure"`
	AllowedOrigins      []string `yaml:"allowed_origins"`
}

// RouterAPI carries shared Router policy plus the process-local binding for
// the single branch served by this process. BranchID and PublicListenAddress
// are intentionally excluded from the shared Coordinator fingerprint.
type RouterAPI struct {
	MaxJSONBytes              int64    `yaml:"max_json_bytes"`
	DefaultPageSize           int      `yaml:"default_page_size"`
	MaxPageSize               int      `yaml:"max_page_size"`
	BranchID                  string   `yaml:"branch_id"`
	ServiceCredentialEnv      string   `yaml:"service_credential_env"`
	PublicListenAddress       string   `yaml:"public_listen_address"`
	UploadStagingDirectory    string   `yaml:"upload_staging_directory"`
	UploadMaxFileBytes        int64    `yaml:"upload_max_file_bytes"`
	UploadMaxConcurrent       int      `yaml:"upload_max_concurrent"`
	UploadMaxStagedBytes      int64    `yaml:"upload_max_staged_bytes"`
	UploadAllowedContentTypes []string `yaml:"upload_allowed_content_types"`
	FormSessionCookieSecure   *bool    `yaml:"form_session_cookie_secure"`
}

type FormsAPI struct {
	FileServiceCredentialEnv   string `yaml:"file_service_credential_env"`
	VisitorSessionTTL          string `yaml:"visitor_session_ttl"`
	VisitorSessionCleanupEvery string `yaml:"visitor_session_cleanup_interval"`
	ExportMaxConcurrent        int    `yaml:"export_max_concurrent"`
	ExportMaxRows              int    `yaml:"export_max_rows"`
	ExportMaxBytes             int64  `yaml:"export_max_bytes"`
}

type Website struct {
	SiteIDs []string `yaml:"site_ids"`
}

type HTTP struct {
	ListenAddress     string `yaml:"listen_address"`
	ReadHeaderTimeout string `yaml:"read_header_timeout"`
	ReadTimeout       string `yaml:"read_timeout"`
	WriteTimeout      string `yaml:"write_timeout"`
	IdleTimeout       string `yaml:"idle_timeout"`
	ShutdownTimeout   string `yaml:"shutdown_timeout"`
	MaxHeaderBytes    int    `yaml:"max_header_bytes"`
}

type HTTPServerConfig struct {
	ListenAddress     string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
}

type CoordinatorConfig struct {
	JobAccessGroup     string `yaml:"job_access_group"`
	JobsPageDefault    int    `yaml:"jobs_page_default"`
	JobsPageMaximum    int    `yaml:"jobs_page_maximum"`
	InstanceLease      string `yaml:"instance_lease"`
	HeartbeatInterval  string `yaml:"heartbeat_interval"`
	WorkerConcurrency  int    `yaml:"worker_concurrency"`
	JobLease           string `yaml:"job_lease"`
	TaskTimeout        string `yaml:"task_timeout"`
	PollInterval       string `yaml:"poll_interval"`
	RetryBase          string `yaml:"retry_base"`
	RetryMaximum       string `yaml:"retry_maximum"`
	MaxAttempts        int    `yaml:"max_attempts"`
	MaxWorkflowTasks   int    `yaml:"max_workflow_tasks"`
	MaxChildrenPerTask int    `yaml:"max_children_per_task"`
	WorkflowRetention  string `yaml:"workflow_retention"`
}

type CoordinatorSettings struct {
	JobAccessGroup     string
	JobsPageDefault    int
	JobsPageMaximum    int
	InstanceLease      time.Duration
	HeartbeatInterval  time.Duration
	WorkerConcurrency  int
	JobLease           time.Duration
	TaskTimeout        time.Duration
	PollInterval       time.Duration
	RetryBase          time.Duration
	RetryMaximum       time.Duration
	MaxAttempts        int
	MaxWorkflowTasks   int
	MaxChildrenPerTask int
	WorkflowRetention  time.Duration
}

type AdminAPISettings struct {
	MaxJSONBytes        int64
	DefaultPageSize     int
	MaxPageSize         int
	SessionCookieSecure bool
	AllowedOrigins      []string
}

type RouterAPISettings struct {
	MaxJSONBytes               int64
	DefaultPageSize            int
	MaxPageSize                int
	BranchID                   string
	ServiceCredential          string
	PublicListenAddress        string
	UploadStagingDirectory     string
	UploadMaxFileBytes         int64
	UploadMaxConcurrent        int
	UploadMaxStagedBytes       int64
	UploadAllowedContentTypes  []string
	FormSessionCookieSecure    bool
	FormSessionCookieSecureSet bool
}

type FormsAPISettings struct {
	FileServiceCredential      string
	VisitorSessionTTL          time.Duration
	VisitorSessionCleanupEvery time.Duration
	ExportMaxConcurrent        int
	ExportMaxRows              int
	ExportMaxBytes             int64
}

type WebsiteSettings struct {
	Sites []string
}

type Resolved struct {
	Core                   platform.RuntimeConfig
	Loader                 loaderapp.Config
	FileAPI                fileapi.Config
	AdminAPI               AdminAPISettings
	RouterAPI              RouterAPISettings
	FormsAPI               FormsAPISettings
	Website                WebsiteSettings
	HTTP                   HTTPServerConfig
	Coordinator            CoordinatorSettings
	CoordinatorFingerprint string
}

var ErrCoordinatorProfileMismatch = errors.New("Coordinator settings do not match the compiled profile")

// ValidateBuildPlan verifies that the optional coordinator API is backed by a
// configured access group. Workflow inputs and component wiring stay engine-owned.
func (r Resolved) ValidateBuildPlan(plan platform.BuildPlan) error {
	selected := make(map[string]struct{}, len(plan.Components))
	for _, component := range plan.Components {
		selected[component] = struct{}{}
	}
	if _, hasWebsite := selected["engine.website"]; hasWebsite {
		if _, fixtures := selected["development.fixtures"]; !fixtures && (len(r.Website.Sites) == 0 || !validWebsiteSiteIDs(r.Website.Sites)) {
			return errors.New("Website Engine requires explicit static site_ids")
		}
	}
	if _, hasCoordinatorAPI := selected["admin.coordinator"]; hasCoordinatorAPI {
		if !platform.ValidAccessGroupKey(r.Coordinator.JobAccessGroup) {
			return ErrCoordinatorProfileMismatch
		}
		if _, configured := r.Core.AccessGroups[r.Coordinator.JobAccessGroup]; !configured {
			return ErrCoordinatorProfileMismatch
		}
	}
	if _, hasAdminRouter := selected["admin.router"]; hasAdminRouter {
		if _, hasRouterEngine := selected["engine.router"]; !hasRouterEngine {
			return errors.New("admin.router requires engine.router")
		}
	}
	if _, hasRouterEngine := selected["engine.router"]; hasRouterEngine && r.Core.ModuleData.MaxSelectorValues < routermodel.MaxRoutePathSelectorValues {
		return fmt.Errorf("Router requires Core module_data.max_selector_values >= %d for bounded path resolution", routermodel.MaxRoutePathSelectorValues)
	}
	if _, hasRouterEngine := selected["engine.router"]; hasRouterEngine {
		if !validBranchID(r.RouterAPI.BranchID) || r.RouterAPI.ServiceCredential == "" {
			return errors.New("Router Engine requires a static branch_id and service credential")
		}
	}
	if _, hasPublicRouter := selected["public.router"]; hasPublicRouter {
		if _, hasRouterEngine := selected["engine.router"]; !hasRouterEngine {
			return errors.New("public Router requires engine.router")
		}
	}
	if _, hasFormsAdminAPI := selected["admin.forms"]; hasFormsAdminAPI {
		settings := r.FormsAPI
		if settings.ExportMaxConcurrent < 1 || settings.ExportMaxConcurrent > 16 || settings.ExportMaxRows < 1 || settings.ExportMaxRows > 10000 ||
			settings.ExportMaxBytes < 1024 || settings.ExportMaxBytes > 64<<20 {
			return errors.New("Forms Admin API requires explicit CSV export concurrency, row and byte limits")
		}
	}
	if _, hasFormsEngine := selected["engine.forms"]; hasFormsEngine {
		settings := r.FormsAPI
		if _, fixtures := selected["development.fixtures"]; !fixtures {
			if settings.FileServiceCredential == "" || settings.VisitorSessionTTL < time.Minute || settings.VisitorSessionTTL > 24*time.Hour ||
				settings.VisitorSessionCleanupEvery <= 0 || settings.VisitorSessionCleanupEvery > 24*time.Hour {
				return errors.New("Forms Engine requires an explicit file service credential, visitor session TTL, and cleanup interval")
			}
		}
	}
	if _, hasPublicFormSession := selected["public.form-session"]; hasPublicFormSession {
		if _, hasForms := selected["engine.forms"]; !hasForms {
			return errors.New("public.form-session requires engine.forms")
		}
		if _, hasPublicRouter := selected["public.router"]; !hasPublicRouter {
			return errors.New("public.form-session requires public.router")
		}
		if !r.RouterAPI.FormSessionCookieSecureSet {
			return errors.New("public.form-session requires an explicit cookie security setting")
		}
	}
	if _, hasPublicFormSubmit := selected["public.form-submit"]; hasPublicFormSubmit {
		if _, hasForms := selected["engine.forms"]; !hasForms {
			return errors.New("public.form-submit requires engine.forms")
		}
		if _, hasPublicRouter := selected["public.router"]; !hasPublicRouter {
			return errors.New("public.form-submit requires public.router")
		}
	}
	_, hasPublicFormSession := selected["public.form-session"]
	_, hasPublicFormSubmit := selected["public.form-submit"]
	if hasPublicFormSession != hasPublicFormSubmit {
		return errors.New("public.form-session and public.form-submit must be selected together")
	}
	if _, hasPublicUpload := selected["public.form-upload"]; hasPublicUpload {
		if _, hasPublicRouter := selected["public.router"]; !hasPublicRouter {
			return errors.New("public.form-upload requires public.router")
		}
		settings := r.RouterAPI
		if settings.UploadStagingDirectory == "" || !filepath.IsAbs(settings.UploadStagingDirectory) || filepath.Clean(settings.UploadStagingDirectory) != settings.UploadStagingDirectory || settings.UploadStagingDirectory == string(filepath.Separator) ||
			settings.UploadMaxFileBytes <= 0 || settings.UploadMaxFileBytes > r.Loader.MaxFileBytes || settings.UploadMaxConcurrent <= 0 || settings.UploadMaxConcurrent > 1024 ||
			settings.UploadMaxStagedBytes <= 0 || settings.UploadMaxFileBytes > settings.UploadMaxStagedBytes/int64(settings.UploadMaxConcurrent) ||
			!validUploadContentTypes(settings.UploadAllowedContentTypes) {
			return errors.New("public.form-upload requires explicit staging, size, concurrency, and MIME limits within Loader capacity")
		}
	}
	return nil
}

// WithBuildPlan binds the compiled component set into the system fingerprint
// so processes built from different profiles cannot join the same Coordinator
// system. Router branch selection and its listener are process-local; all
// shared operational settings and the input order remain fingerprinted.
func (r Resolved) WithBuildPlan(plan platform.BuildPlan) (Resolved, error) {
	if err := r.ValidateBuildPlan(plan); err != nil {
		return Resolved{}, err
	}
	if len(plan.Components) == 0 || len(r.CoordinatorFingerprint) != 64 {
		return Resolved{}, ErrCoordinatorProfileMismatch
	}
	if _, err := hex.DecodeString(r.CoordinatorFingerprint); err != nil {
		return Resolved{}, ErrCoordinatorProfileMismatch
	}
	components := append([]string(nil), plan.Components...)
	sort.Strings(components)
	for index, component := range components {
		if component == "" || (index > 0 && components[index-1] == component) {
			return Resolved{}, ErrCoordinatorProfileMismatch
		}
	}
	canonical, err := json.Marshal(struct {
		RuntimeFingerprint string   `json:"runtime_fingerprint"`
		Components         []string `json:"components"`
	}{r.CoordinatorFingerprint, components})
	if err != nil {
		return Resolved{}, ErrCoordinatorProfileMismatch
	}
	digest := sha256.Sum256(canonical)
	r.CoordinatorFingerprint = hex.EncodeToString(digest[:])
	return r, nil
}

// Resolve reads secrets only from the configured environment variables and
// validates every operational limit required to start the active Core/File API profile.
func (c Config) Resolve(getenv func(string) string) (Resolved, error) {
	if getenv == nil {
		return Resolved{}, errors.New("missing environment lookup")
	}
	if !validWebsiteSiteIDs(c.Website.SiteIDs) {
		return Resolved{}, errors.New("invalid Website site_ids")
	}
	durations := []string{c.Core.ConnectTimeout, c.Core.MaxLifetime, c.Core.MaxIdleTime, c.Core.HealthInterval, c.Core.MigrationLockTimeout, c.Core.CacheTTL, c.Core.MaintenanceInterval, c.Core.MaintenanceTimeout, c.Core.PendingTTL, c.Core.SessionIdleTTL, c.Core.SessionAbsoluteTTL}
	var parsed [11]time.Duration
	for i, value := range durations {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Resolved{}, errors.New("invalid Core duration")
		}
		parsed[i] = duration
	}
	if parsed[9] > parsed[10] || parsed[10] > 365*24*time.Hour {
		return Resolved{}, errors.New("invalid Core session lifetime configuration")
	}
	loaderDurationValues := []string{c.Loader.SessionTTL, c.Loader.CommitTimeout, c.Loader.SessionCacheTTL, c.Loader.CleanupInterval}
	var loaderDurations [4]time.Duration
	for i, value := range loaderDurationValues {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Resolved{}, errors.New("invalid Loader duration")
		}
		loaderDurations[i] = duration
	}
	if loaderDurations[0] > time.Duration(1<<63-1)-loaderDurations[1] {
		return Resolved{}, errors.New("Loader session and commit timeouts overflow")
	}
	coordinatorDurationValues := []string{c.Coordinator.InstanceLease, c.Coordinator.HeartbeatInterval, c.Coordinator.JobLease,
		c.Coordinator.TaskTimeout, c.Coordinator.PollInterval, c.Coordinator.RetryBase, c.Coordinator.RetryMaximum,
		c.Coordinator.WorkflowRetention}
	var coordinatorDurations [8]time.Duration
	for i, value := range coordinatorDurationValues {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Resolved{}, errors.New("invalid Coordinator duration")
		}
		coordinatorDurations[i] = duration
	}
	if coordinatorDurations[0] < 3*time.Second || coordinatorDurations[0] > time.Hour ||
		coordinatorDurations[1] >= coordinatorDurations[0]/3 || coordinatorDurations[2] < 3*time.Second || coordinatorDurations[2] > time.Hour ||
		coordinatorDurations[3] > 24*time.Hour || coordinatorDurations[4] >= coordinatorDurations[2] ||
		coordinatorDurations[5] > coordinatorDurations[6] || coordinatorDurations[6] > 24*time.Hour ||
		c.Coordinator.WorkerConcurrency < 1 || c.Coordinator.WorkerConcurrency > 256 || c.Coordinator.MaxAttempts < 1 || c.Coordinator.MaxAttempts > 100 ||
		c.Coordinator.MaxWorkflowTasks < 1 || c.Coordinator.MaxWorkflowTasks > 100000 ||
		c.Coordinator.MaxChildrenPerTask < 0 || c.Coordinator.MaxChildrenPerTask > 256 ||
		coordinatorDurations[7] > 365*24*time.Hour ||
		c.Coordinator.JobsPageDefault < 1 || c.Coordinator.JobsPageMaximum < c.Coordinator.JobsPageDefault || c.Coordinator.JobsPageMaximum > 200 ||
		(c.Coordinator.JobAccessGroup != "" && !platform.ValidAccessGroupKey(c.Coordinator.JobAccessGroup)) {
		return Resolved{}, errors.New("invalid Coordinator configuration")
	}
	var formsSessionTTL, formsCleanupInterval time.Duration
	if c.FormsAPI.VisitorSessionTTL != "" || c.FormsAPI.VisitorSessionCleanupEvery != "" {
		var err error
		formsSessionTTL, err = time.ParseDuration(c.FormsAPI.VisitorSessionTTL)
		if err != nil || formsSessionTTL < time.Minute || formsSessionTTL > 24*time.Hour {
			return Resolved{}, errors.New("invalid Forms visitor session TTL")
		}
		formsCleanupInterval, err = time.ParseDuration(c.FormsAPI.VisitorSessionCleanupEvery)
		if err != nil || formsCleanupInterval <= 0 || formsCleanupInterval > 24*time.Hour {
			return Resolved{}, errors.New("invalid Forms visitor session cleanup interval")
		}
	}
	httpDurationValues := []string{c.HTTP.ReadHeaderTimeout, c.HTTP.ReadTimeout, c.HTTP.WriteTimeout, c.HTTP.IdleTimeout, c.HTTP.ShutdownTimeout}
	var httpDurations [5]time.Duration
	for i, value := range httpDurationValues {
		duration, err := time.ParseDuration(value)
		if err != nil || (i == 2 && duration < 0) || (i != 2 && duration <= 0) {
			return Resolved{}, errors.New("invalid HTTP server duration")
		}
		httpDurations[i] = duration
	}
	moduleDataLimits := platform.ModuleDataLimits{
		MaxPayloadBytes: c.Core.ModuleData.MaxPayloadBytes, MaxIndexBytes: c.Core.ModuleData.MaxIndexBytes,
		MaxBatchBytes: c.Core.ModuleData.MaxBatchBytes, MaxPageBytes: c.Core.ModuleData.MaxPageBytes,
		MaxSelectors: c.Core.ModuleData.MaxSelectors, MaxSelectorValues: c.Core.ModuleData.MaxSelectorValues,
		MaxPageSize: c.Core.ModuleData.MaxPageSize, MaxBatchOperations: c.Core.ModuleData.MaxBatchOperations,
	}
	if c.Core.MinConnections < 0 || c.Core.MaxConnections < platform.MinRuntimeDatabaseConnections || c.Core.MinConnections > c.Core.MaxConnections || c.Core.MaxObjectBytes <= 0 || c.Core.MaxObjectBytes > platform.MaxS3PutObjectBytes ||
		c.Core.MaxMetadataBytes <= 0 || c.Core.MaxMetadataBytes > 16<<20 || c.Core.MaxCachedObjectBytes < 0 || c.Core.MaxCachedObjectBytes > c.Core.MaxObjectBytes ||
		c.Core.CacheMaxBytes <= 0 || c.Core.MaxCachedObjectBytes > c.Core.CacheMaxBytes || c.Core.MaintenanceBatchSize <= 0 || c.Core.AtlasBinary == "" || !platform.ValidModuleDataLimits(moduleDataLimits) {
		return Resolved{}, errors.New("invalid Core storage, cache, database pool capacity for Loader and Coordinator, or module-data configuration")
	}
	accessGroups := make(map[string]platform.AccessLevel, len(c.Core.AccessGroups))
	for key, configuredLevel := range c.Core.AccessGroups {
		level := platform.AccessLevelWrite
		if configuredLevel != nil {
			level = *configuredLevel
		}
		if !platform.ValidAccessGroupKey(key) || level > platform.AccessLevelWrite {
			return Resolved{}, errors.New("invalid configured access group")
		}
		accessGroups[key] = level
	}
	if c.Coordinator.JobAccessGroup != "" {
		if _, exists := accessGroups[c.Coordinator.JobAccessGroup]; !exists {
			return Resolved{}, errors.New("Coordinator job access group must exist in Core access_groups")
		}
	}
	if c.Loader.MaxFileBytes <= 0 || c.Loader.MaxFileBytes > c.Core.MaxObjectBytes || c.Loader.MaxPartBytes <= 0 || c.Loader.MaxPartBytes > c.Core.MaxObjectBytes ||
		c.Loader.PartSize <= 0 || c.Loader.PartSize > c.Loader.MaxPartBytes || c.Loader.MaxParts <= 0 || c.Loader.MaxParts > 1000 ||
		c.Loader.MaxConcurrentCommits <= 0 || c.Loader.MaxConcurrentCommits > 100 ||
		int64(c.Core.MaxConnections) < 1+2*int64(c.Loader.MaxConcurrentCommits)+2*int64(c.Coordinator.WorkerConcurrency) ||
		c.Loader.BufferThreshold < 0 || c.Loader.MaxBufferedBytes < 0 || c.Loader.SessionCacheEntries <= 0 || c.Loader.SessionCacheEntries > 100000 ||
		c.Loader.SessionCacheMaxBytes <= 0 || c.Loader.SessionCacheMaxBytes > 256<<20 || c.Loader.CleanupBatchSize <= 0 ||
		(c.Loader.Mode != loaderapp.ModeAuto && c.Loader.Mode != loaderapp.ModeStream && c.Loader.Mode != loaderapp.ModeBuffered) ||
		(c.Loader.Mode == loaderapp.ModeBuffered && c.Loader.MaxBufferedBytes == 0) || moduleDataLimits.MaxBatchOperations < 3 ||
		moduleDataLimits.MaxIndexBytes < loaderapp.MinimumModuleDataIndexBytes ||
		moduleDataLimits.MaxBatchBytes < moduleDataLimits.MaxPayloadBytes+loaderapp.ModuleDataBatchOverheadBytes {
		return Resolved{}, errors.New("invalid Loader limits or incompatible Core module-data settings")
	}
	if c.FileAPI.MaxJSONBytes <= 0 || c.FileAPI.MaxJSONBytes > 16<<20 || c.FileAPI.MaxAttributeBytes <= 0 || c.FileAPI.MaxAttributeBytes > 16<<20 || c.FileAPI.MaxAttributeBytes > c.Core.MaxMetadataBytes ||
		c.FileAPI.MaxGroupsPerFile <= 0 || c.FileAPI.MaxGroupsPerFile > 64 || c.FileAPI.DefaultPageSize <= 0 || c.FileAPI.MaxPageSize < c.FileAPI.DefaultPageSize || c.FileAPI.MaxPageSize > 1000 {
		return Resolved{}, errors.New("invalid File API request limits")
	}
	if c.AdminAPI.MaxJSONBytes <= 0 || c.AdminAPI.MaxJSONBytes > 1<<20 ||
		c.AdminAPI.DefaultPageSize <= 0 || c.AdminAPI.MaxPageSize < c.AdminAPI.DefaultPageSize || c.AdminAPI.MaxPageSize > 500 ||
		c.AdminAPI.SessionCookieSecure == nil || !validAdminOrigins(c.AdminAPI.AllowedOrigins) {
		return Resolved{}, errors.New("Admin API request limits, session cookie, and allowed origins must be explicitly configured")
	}
	if c.RouterAPI.MaxJSONBytes <= 0 || c.RouterAPI.MaxJSONBytes > 1<<20 ||
		c.RouterAPI.DefaultPageSize <= 0 || c.RouterAPI.MaxPageSize < c.RouterAPI.DefaultPageSize || c.RouterAPI.MaxPageSize > 100 {
		return Resolved{}, errors.New("Router API request and pagination limits must be explicitly configured")
	}
	if c.RouterAPI.ServiceCredentialEnv != "" && (!envName.MatchString(c.RouterAPI.ServiceCredentialEnv) || len(c.RouterAPI.ServiceCredentialEnv) > 128) {
		return Resolved{}, errors.New("invalid Router service credential environment name")
	}
	if c.FormsAPI.FileServiceCredentialEnv != "" && (!envName.MatchString(c.FormsAPI.FileServiceCredentialEnv) || len(c.FormsAPI.FileServiceCredentialEnv) > 128) {
		return Resolved{}, errors.New("invalid Forms file service credential environment name")
	}
	if int64(moduleDataLimits.MaxPayloadBytes) < c.FileAPI.MaxAttributeBytes+int64(c.FileAPI.MaxGroupsPerFile*68)+4096 {
		return Resolved{}, errors.New("Core module-data payload limit is too small for configured Loader sessions")
	}
	if !validListenAddress(c.HTTP.ListenAddress) || c.HTTP.MaxHeaderBytes < 1024 || c.HTTP.MaxHeaderBytes > 1<<20 {
		return Resolved{}, errors.New("invalid HTTP listen address or header limit")
	}
	if c.Core.BootstrapSecretEnv != "" && !envName.MatchString(c.Core.BootstrapSecretEnv) {
		return Resolved{}, errors.New("invalid bootstrap environment name")
	}
	if !envName.MatchString(c.Core.DatabaseURLEnv) || !envName.MatchString(c.Core.MigrationDatabaseURLEnv) || !envName.MatchString(c.Core.S3AccessKeyEnv) || !envName.MatchString(c.Core.S3SecretKeyEnv) {
		return Resolved{}, errors.New("invalid secret environment name")
	}
	dbURL, migrationDBURL := getenv(c.Core.DatabaseURLEnv), getenv(c.Core.MigrationDatabaseURLEnv)
	accessKey, secretKey := getenv(c.Core.S3AccessKeyEnv), getenv(c.Core.S3SecretKeyEnv)
	if dbURL == "" || migrationDBURL == "" || accessKey == "" || secretKey == "" {
		return Resolved{}, errors.New("required secret environment variable is empty")
	}
	if err := platform.ValidateDatabaseURLs(dbURL, migrationDBURL); err != nil {
		return Resolved{}, err
	}
	var routerServiceCredential string
	if c.RouterAPI.ServiceCredentialEnv != "" {
		routerServiceCredential = getenv(c.RouterAPI.ServiceCredentialEnv)
		if routerServiceCredential == "" || strings.ContainsRune(routerServiceCredential, '\x00') {
			return Resolved{}, errors.New("configured Router service credential is empty or invalid")
		}
	}
	var formsFileServiceCredential string
	if c.FormsAPI.FileServiceCredentialEnv != "" {
		formsFileServiceCredential = getenv(c.FormsAPI.FileServiceCredentialEnv)
		if formsFileServiceCredential == "" || strings.ContainsRune(formsFileServiceCredential, '\x00') {
			return Resolved{}, errors.New("configured Forms file service credential is empty or invalid")
		}
	}
	if c.Core.AccessGroups == nil {
		return Resolved{}, errors.New("Core access_groups must be configured explicitly")
	}
	var bootstrap string
	if c.Core.BootstrapSecretEnv != "" {
		bootstrap = getenv(c.Core.BootstrapSecretEnv)
		if bootstrap == "" {
			return Resolved{}, errors.New("configured bootstrap secret is empty")
		}
	}
	coreConfig := platform.RuntimeConfig{
		Database:             platform.DatabaseConfig{URL: dbURL, MinConnections: c.Core.MinConnections, MaxConnections: c.Core.MaxConnections, ConnectTimeout: parsed[0], MaxLifetime: parsed[1], MaxIdleTime: parsed[2], HealthInterval: parsed[3]},
		MigrationDatabaseURL: migrationDBURL,
		S3:                   platform.S3Config{Endpoint: c.Core.S3Endpoint, Region: c.Core.S3Region, Bucket: c.Core.S3Bucket, AccessKey: accessKey, SecretKey: secretKey, PathStyle: c.Core.S3PathStyle, AllowInsecureHTTP: c.Core.S3AllowInsecureHTTP},
		BootstrapSecret:      bootstrap, AtlasBinary: c.Core.AtlasBinary, MigrationLockTimeout: parsed[4], AccessGroups: accessGroups,
		CacheMaxBytes: c.Core.CacheMaxBytes, CacheTTL: parsed[5], MaxMetadataBytes: c.Core.MaxMetadataBytes,
		MaxObjectBytes: c.Core.MaxObjectBytes, MaxCachedObjectBytes: c.Core.MaxCachedObjectBytes,
		ModuleData:          moduleDataLimits,
		MaintenanceInterval: parsed[6], MaintenanceTimeout: parsed[7], PendingTTL: parsed[8], MaintenanceBatchSize: c.Core.MaintenanceBatchSize,
		SessionIdleTTL: parsed[9], SessionAbsoluteTTL: parsed[10],
	}
	loaderConfig := loaderapp.Config{PartSize: c.Loader.PartSize, MaxFileBytes: c.Loader.MaxFileBytes, MaxPartBytes: c.Loader.MaxPartBytes, MaxParts: c.Loader.MaxParts,
		MaxConcurrentCommits: c.Loader.MaxConcurrentCommits,
		SessionTTL:           loaderDurations[0], CommitTimeout: loaderDurations[1], Mode: c.Loader.Mode, BufferThreshold: c.Loader.BufferThreshold,
		MaxBufferedBytes: c.Loader.MaxBufferedBytes, SessionCacheEntries: c.Loader.SessionCacheEntries,
		SessionCacheMaxBytes: c.Loader.SessionCacheMaxBytes, SessionCacheTTL: loaderDurations[2],
		CleanupInterval: loaderDurations[3], CleanupBatchSize: c.Loader.CleanupBatchSize}
	fileAPIConfig := fileapi.Config{MaxFileBytes: c.Loader.MaxFileBytes, MaxPartBytes: c.Loader.MaxPartBytes, MaxUploadParts: c.Loader.MaxParts,
		MaxJSONBytes: c.FileAPI.MaxJSONBytes, MaxAttributeBytes: c.FileAPI.MaxAttributeBytes, MaxGroupsPerFile: c.FileAPI.MaxGroupsPerFile,
		DefaultPageSize: c.FileAPI.DefaultPageSize, MaxPageSize: c.FileAPI.MaxPageSize}
	if err := fileAPIConfig.Validate(); err != nil {
		return Resolved{}, err
	}
	coordinatorFingerprint, err := configFingerprint(c, dbURL, migrationDBURL)
	if err != nil {
		return Resolved{}, errors.New("cannot fingerprint Coordinator configuration")
	}
	return Resolved{Core: coreConfig, Loader: loaderConfig, FileAPI: fileAPIConfig,
		AdminAPI: AdminAPISettings{MaxJSONBytes: c.AdminAPI.MaxJSONBytes, DefaultPageSize: c.AdminAPI.DefaultPageSize, MaxPageSize: c.AdminAPI.MaxPageSize,
			SessionCookieSecure: *c.AdminAPI.SessionCookieSecure,
			AllowedOrigins:      append([]string(nil), c.AdminAPI.AllowedOrigins...)},
		RouterAPI: RouterAPISettings{MaxJSONBytes: c.RouterAPI.MaxJSONBytes, DefaultPageSize: c.RouterAPI.DefaultPageSize, MaxPageSize: c.RouterAPI.MaxPageSize,
			BranchID: c.RouterAPI.BranchID, ServiceCredential: routerServiceCredential, PublicListenAddress: c.RouterAPI.PublicListenAddress,
			UploadStagingDirectory: c.RouterAPI.UploadStagingDirectory, UploadMaxFileBytes: c.RouterAPI.UploadMaxFileBytes,
			UploadMaxConcurrent: c.RouterAPI.UploadMaxConcurrent, UploadMaxStagedBytes: c.RouterAPI.UploadMaxStagedBytes,
			UploadAllowedContentTypes:  append([]string(nil), c.RouterAPI.UploadAllowedContentTypes...),
			FormSessionCookieSecure:    c.RouterAPI.FormSessionCookieSecure != nil && *c.RouterAPI.FormSessionCookieSecure,
			FormSessionCookieSecureSet: c.RouterAPI.FormSessionCookieSecure != nil},
		FormsAPI: FormsAPISettings{FileServiceCredential: formsFileServiceCredential, VisitorSessionTTL: formsSessionTTL,
			VisitorSessionCleanupEvery: formsCleanupInterval, ExportMaxConcurrent: c.FormsAPI.ExportMaxConcurrent,
			ExportMaxRows: c.FormsAPI.ExportMaxRows, ExportMaxBytes: c.FormsAPI.ExportMaxBytes},
		Website: WebsiteSettings{Sites: append([]string(nil), c.Website.SiteIDs...)},
		Coordinator: CoordinatorSettings{
			JobAccessGroup:  c.Coordinator.JobAccessGroup,
			JobsPageDefault: c.Coordinator.JobsPageDefault, JobsPageMaximum: c.Coordinator.JobsPageMaximum,
			InstanceLease: coordinatorDurations[0], HeartbeatInterval: coordinatorDurations[1],
			WorkerConcurrency: c.Coordinator.WorkerConcurrency, JobLease: coordinatorDurations[2], TaskTimeout: coordinatorDurations[3],
			PollInterval: coordinatorDurations[4], RetryBase: coordinatorDurations[5], RetryMaximum: coordinatorDurations[6],
			MaxAttempts: c.Coordinator.MaxAttempts, MaxWorkflowTasks: c.Coordinator.MaxWorkflowTasks,
			MaxChildrenPerTask: c.Coordinator.MaxChildrenPerTask, WorkflowRetention: coordinatorDurations[7],
		}, CoordinatorFingerprint: coordinatorFingerprint, HTTP: HTTPServerConfig{ListenAddress: c.HTTP.ListenAddress,
			ReadHeaderTimeout: httpDurations[0], ReadTimeout: httpDurations[1], WriteTimeout: httpDurations[2], IdleTimeout: httpDurations[3],
			ShutdownTimeout: httpDurations[4], MaxHeaderBytes: c.HTTP.MaxHeaderBytes}}, nil
}

type Config struct {
	Core        Core              `yaml:"core"`
	Loader      Loader            `yaml:"loader"`
	FileAPI     FileAPI           `yaml:"file_api"`
	AdminAPI    AdminAPI          `yaml:"admin_api"`
	RouterAPI   RouterAPI         `yaml:"router_api"`
	FormsAPI    FormsAPI          `yaml:"forms_api"`
	Website     Website           `yaml:"website"`
	HTTP        HTTP              `yaml:"http"`
	Coordinator CoordinatorConfig `yaml:"coordinator"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var websiteSiteIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func configFingerprint(config Config, runtimeURL, migrationURL string) (string, error) {
	config.Core.DatabaseURLEnv = ""
	config.Core.MigrationDatabaseURLEnv = ""
	config.Core.S3AccessKeyEnv = ""
	config.Core.S3SecretKeyEnv = ""
	config.Core.BootstrapSecretEnv = ""
	// Each public Router process serves exactly one configured branch. Branch
	// selection and its listener are deployment-local bindings, while
	// every shared Router policy (credentials, limits, staging, and APIs) must
	// remain identical for processes sharing the same Core Coordinator.
	config.RouterAPI.BranchID = ""
	config.RouterAPI.PublicListenAddress = ""
	databaseIdentity := func(value string) (string, error) {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || parsed.Path == "" {
			return "", errors.New("invalid database endpoint")
		}
		user := ""
		if parsed.User != nil {
			user = parsed.User.Username()
		}
		query := parsed.Query()
		for key := range query {
			switch strings.ToLower(key) {
			case "password", "passfile", "sslpassword":
				delete(query, key)
			}
		}
		queryText := query.Encode()
		if queryText != "" {
			queryText = "?" + queryText
		}
		return parsed.Scheme + "://" + user + "@" + parsed.Host + parsed.EscapedPath() + queryText, nil
	}
	runtimeIdentity, err := databaseIdentity(runtimeURL)
	if err != nil {
		return "", err
	}
	migrationIdentity, err := databaseIdentity(migrationURL)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(struct {
		Config            Config `json:"config"`
		RuntimeDatabase   string `json:"runtime_database"`
		MigrationDatabase string `json:"migration_database"`
	}{config, runtimeIdentity, migrationIdentity})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// Load rejects unknown keys and multiple documents. Sensitive values are
// referenced by environment variable name, never embedded in YAML.
func Load(input io.Reader) (Config, error) {
	if input == nil {
		return Config{}, errors.New("missing configuration input")
	}
	data, err := io.ReadAll(io.LimitReader(input, 1<<20+1))
	if err != nil {
		return Config{}, errors.New("cannot read configuration")
	}
	if len(data) > 1<<20 {
		return Config{}, errors.New("configuration exceeds size limit")
	}
	if err := validateConfigYAML(data); err != nil {
		return Config{}, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
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

func validateConfigYAML(data []byte) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode configuration: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("configuration must contain exactly one YAML document")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("configuration root must be a mapping")
	}
	return validateConfigNode(document.Content[0])
}

func validateConfigNode(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		return errors.New("YAML aliases are not supported in configuration")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.Tag == "!!merge" {
				return errors.New("configuration mapping keys must be plain scalars")
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("duplicate configuration key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateConfigNode(child); err != nil {
			return err
		}
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validListenAddress(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	_, portText, err := net.SplitHostPort(value)
	if err != nil || portText == "" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port > 0 && port <= 65535
}

func validBranchID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char == 0x7f || char == '/' || char == '\\' {
			return false
		}
	}
	return true
}

func validWebsiteSiteIDs(values []string) bool {
	if len(values) > 256 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !websiteSiteIDPattern.MatchString(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validUploadContentTypes(values []string) bool {
	if len(values) == 0 || len(values) > 32 {
		return false
	}
	active := map[string]struct{}{
		"application/ecmascript": {}, "application/javascript": {}, "application/x-httpd-php": {}, "application/xhtml+xml": {},
		"application/xml": {}, "image/svg+xml": {}, "text/css": {}, "text/html": {}, "text/javascript": {}, "text/xml": {},
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		mediaType, parameters, err := mime.ParseMediaType(value)
		if err != nil || value == "" || value != strings.TrimSpace(value) || len(parameters) != 0 || strings.Contains(mediaType, "*") {
			return false
		}
		if _, blocked := active[mediaType]; blocked {
			return false
		}
		if _, duplicate := seen[mediaType]; duplicate {
			return false
		}
		seen[mediaType] = struct{}{}
	}
	return true
}

func validAdminOrigins(values []string) bool {
	if len(values) == 0 || len(values) > 32 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || value == "" || len(value) > 512 || value != strings.TrimSpace(value) ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.Host != strings.ToLower(parsed.Host) || strings.ContainsAny(parsed.Host, "*%,") || strings.HasSuffix(parsed.Host, ":") ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.RawFragment != "" || parsed.Opaque != "" || value != parsed.Scheme+"://"+parsed.Host {
			return false
		}
		if port := parsed.Port(); port != "" {
			portNumber, err := strconv.Atoi(port)
			if err != nil || portNumber < 1 || portNumber > 65535 ||
				(parsed.Scheme == "http" && portNumber == 80) || (parsed.Scheme == "https" && portNumber == 443) {
				return false
			}
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
