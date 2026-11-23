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
	publicationapp "github.com/Zubrium/cdn/main/internal/publication"
	"github.com/Zubrium/cdn/pkg/observability"
	"github.com/Zubrium/cdn/presentation/fileapi"
	"github.com/Zubrium/cdn/workers/archive"
	"github.com/google/uuid"
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
	S3InsecureAllowedHosts  []string                         `yaml:"s3_insecure_allowed_hosts"`
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
	CacheMaxBytes           int64                            `yaml:"-"`
	CacheTTL                string                           `yaml:"-"`
	MaxMetadataBytes        int64                            `yaml:"max_metadata_bytes"`
	MaxObjectBytes          int64                            `yaml:"max_object_bytes"`
	MaxCachedObjectBytes    int64                            `yaml:"-"`
	Cache                   CacheTarget                      `yaml:"cache"`
	Concurrency             CoreConcurrency                  `yaml:"concurrency"`
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
	PartSize             int64             `yaml:"part_size_bytes"`
	MaxFileBytes         int64             `yaml:"max_file_bytes"`
	MaxPartBytes         int64             `yaml:"max_part_bytes"`
	MaxParts             int               `yaml:"max_parts"`
	MaxConcurrentCommits int               `yaml:"-"`
	Concurrency          LoaderConcurrency `yaml:"concurrency"`
	SessionTTL           string            `yaml:"session_ttl"`
	CommitTimeout        string            `yaml:"commit_timeout"`
	Mode                 string            `yaml:"mode"`
	BufferThreshold      int64             `yaml:"buffer_threshold_bytes"`
	MaxBufferedBytes     int64             `yaml:"max_buffered_bytes"`
	SessionCacheEntries  int               `yaml:"session_cache_entries"`
	SessionCacheMaxBytes int64             `yaml:"session_cache_max_bytes"`
	SessionCacheTTL      string            `yaml:"session_cache_ttl"`
	CleanupInterval      string            `yaml:"cleanup_interval"`
	CleanupBatchSize     int               `yaml:"cleanup_batch_size"`
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

type ContentAPI struct {
	ServiceCredentialEnv string `yaml:"service_credential_env"`
	Locale               string `yaml:"locale"`
}

// PublicationBinding is a server-owned (route, page snapshot, locale) slot.
// Route and page identities are fixed by startup configuration; no HTTP client
// can supply or alter them.
type PublicationBinding struct {
	RouteID        string `yaml:"route_id"`
	PageSnapshotID string `yaml:"page_snapshot_id"`
	Locale         string `yaml:"locale"`
}

type Publication struct {
	Bindings             []PublicationBinding `yaml:"bindings"`
	WebsiteCredentialEnv string               `yaml:"website_credential_env"`
	BuildTimeout         string               `yaml:"build_timeout"`
	PollInterval         string               `yaml:"poll_interval"`
}

// RetirementBinding maps one server-owned page identity to the complete set
// of Router routes that must be withdrawn before that page becomes retired.
type RetirementBinding struct {
	PageID   string   `yaml:"page_id"`
	RouteIDs []string `yaml:"route_ids"`
}

type Retirement struct {
	Bindings             []RetirementBinding `yaml:"bindings"`
	WebsiteCredentialEnv string              `yaml:"website_credential_env"`
}

// FeatureImport supplies trusted, server-side inputs for the durable archive
// worker and the Website import API. It is never populated from an HTTP request.
type FeatureImport struct {
	TargetFileSetID      string   `yaml:"target_file_set_id"`
	OutputGroups         []string `yaml:"output_groups"`
	StagingDirectory     string   `yaml:"staging_directory"`
	ServiceCredentialEnv string   `yaml:"service_credential_env"`
	MaxArchiveBytes      int64    `yaml:"max_archive_bytes"`
	MaxEntries           int      `yaml:"max_entries"`
	MaxEntryBytes        int64    `yaml:"max_entry_bytes"`
	MaxExpandedBytes     int64    `yaml:"max_expanded_bytes"`
	MaxCompressionRatio  uint64   `yaml:"max_compression_ratio"`
}

// Media supplies trusted, server-side inputs for the supervised media worker
// and the Website derivative API. It is never populated from an HTTP request.
type Media struct {
	ServiceCredentialEnv string `yaml:"service_credential_env"`
	SandboxImage         string `yaml:"sandbox_image"`
	DockerBinary         string `yaml:"docker_binary"`
	StagingDirectory     string `yaml:"staging_directory"`
	ProcessorVersion     string `yaml:"processor_version"`
	CPUs                 string `yaml:"cpus"`
	MemoryLimitBytes     int64  `yaml:"memory_limit_bytes"`
	PIDsLimit            int64  `yaml:"pids_limit"`
	MaxInputBytes        int64  `yaml:"max_input_bytes"`
	MaxOutputBytes       int64  `yaml:"max_output_bytes"`
	MaxConcurrency       int    `yaml:"max_concurrency"`
	Timeout              string `yaml:"timeout"`
}

// Prerender supplies trusted, server-side inputs for the sandboxed Chromium
// renderer. No field can be set by an HTTP request.
type Prerender struct {
	SandboxImage     string `yaml:"sandbox_image"`
	SandboxPlatform  string `yaml:"sandbox_platform"`
	DockerBinary     string `yaml:"docker_binary"`
	StagingDirectory string `yaml:"staging_directory"`
	CPUs             string `yaml:"cpus"`
	MemoryLimitBytes int64  `yaml:"memory_limit_bytes"`
	PIDsLimit        int64  `yaml:"pids_limit"`
	MaxDocumentBytes int64  `yaml:"max_document_bytes"`
	MaxPlanBytes     int    `yaml:"max_plan_bytes"`
	MaxArtifacts     int    `yaml:"max_artifacts"`
	MaxArtifactBytes int64  `yaml:"max_artifact_bytes"`
	MaxTotalBytes    int64  `yaml:"max_total_bytes"`
	Timeout          string `yaml:"timeout"`
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
	WorkflowAccessGroup  string `yaml:"workflow_access_group"`
	WorkflowsPageDefault int    `yaml:"workflows_page_default"`
	WorkflowsPageMaximum int    `yaml:"workflows_page_maximum"`
	InstanceLease        string `yaml:"instance_lease"`
	HeartbeatInterval    string `yaml:"heartbeat_interval"`
	ExecutorConcurrency  int    `yaml:"-"`
	TaskLease            string `yaml:"task_lease"`
	TaskTimeout          string `yaml:"task_timeout"`
	PollInterval         string `yaml:"poll_interval"`
	RetryBase            string `yaml:"retry_base"`
	RetryMaximum         string `yaml:"retry_maximum"`
	MaxAttempts          int    `yaml:"max_attempts"`
	MaxWorkflowTasks     int    `yaml:"max_workflow_tasks"`
	MaxChildrenPerTask   int    `yaml:"max_children_per_task"`
	WorkflowRetention    string `yaml:"workflow_retention"`
}

type CoordinatorSettings struct {
	WorkflowAccessGroup  string
	WorkflowsPageDefault int
	WorkflowsPageMaximum int
	InstanceLease        time.Duration
	HeartbeatInterval    time.Duration
	ExecutorConcurrency  int
	TaskLease            time.Duration
	TaskTimeout          time.Duration
	PollInterval         time.Duration
	RetryBase            time.Duration
	RetryMaximum         time.Duration
	MaxAttempts          int
	MaxWorkflowTasks     int
	MaxChildrenPerTask   int
	WorkflowRetention    time.Duration
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

type ContentAPISettings struct {
	ServiceCredential string
	Locale            string
}

type PublicationSettings struct {
	Enabled           bool
	Targets           []publicationapp.Target
	WebsiteCredential string
	RouterCredential  string
	BuildTimeout      time.Duration
	PollInterval      time.Duration
}

type RetirementSettings struct {
	Enabled           bool
	Routes            map[string][]string
	WebsiteCredential string
	RouterCredential  string
}

type FeatureImportSettings struct {
	Enabled           bool
	TargetFileSetID   string
	OutputGroups      []string
	StagingDirectory  string
	ServiceCredential string
	Limits            archive.Limits
}

type MediaSettings struct {
	Enabled           bool
	ServiceCredential string
	SandboxImage      string
	DockerBinary      string
	StagingDirectory  string
	ProcessorVersion  string
	CPUs              string
	MemoryLimitBytes  int64
	PIDsLimit         int64
	MaxInputBytes     int64
	MaxOutputBytes    int64
	MaxConcurrency    int
	Timeout           time.Duration
}

type PrerenderSettings struct {
	Enabled          bool
	SandboxImage     string
	SandboxPlatform  string
	DockerBinary     string
	StagingDirectory string
	CPUs             string
	MemoryLimitBytes int64
	PIDsLimit        int64
	MaxDocumentBytes int64
	MaxPlanBytes     int
	MaxArtifacts     int
	MaxArtifactBytes int64
	MaxTotalBytes    int64
	Timeout          time.Duration
}

// ObservabilityFile configures the cheap stdlib-only JSONL backend.
type ObservabilityFile struct {
	Directory    string `yaml:"directory"`
	MaxFileBytes int64  `yaml:"max_file_bytes"`
	MaxFiles     int    `yaml:"max_files"`
}

// ObservabilityOTLP configures the OpenTelemetry OTLP/HTTP backend. Header
// values are read from the environment; credentials are never stored in YAML.
type ObservabilityOTLP struct {
	Endpoint   string `yaml:"endpoint"`
	Insecure   bool   `yaml:"insecure"`
	HeadersEnv string `yaml:"headers_env"`
	Timeout    string `yaml:"timeout"`
}

type ObservabilityConfig struct {
	Mode           string            `yaml:"mode"`
	MinLevel       string            `yaml:"min_level"`
	ServiceVersion string            `yaml:"service_version"`
	InstanceID     string            `yaml:"instance_id"`
	File           ObservabilityFile `yaml:"file"`
	OTLP           ObservabilityOTLP `yaml:"otlp"`
}

type Resolved struct {
	Core                   platform.RuntimeConfig
	Loader                 loaderapp.Config
	FileAPI                fileapi.Config
	AdminAPI               AdminAPISettings
	RouterAPI              RouterAPISettings
	FormsAPI               FormsAPISettings
	ContentAPI             ContentAPISettings
	Publication            PublicationSettings
	Retirement             RetirementSettings
	FeatureImport          FeatureImportSettings
	Media                  MediaSettings
	Prerender              PrerenderSettings
	HTTP                   HTTPServerConfig
	Coordinator            CoordinatorSettings
	CoordinatorFingerprint string
	Observability          observability.Config
	Scales                 ResolvedScales
}

var ErrCoordinatorProfileMismatch = errors.New("Coordinator settings do not match the compiled profile")

// ValidateBuildPlan verifies that the optional coordinator API is backed by a
// configured access group. Workflow inputs and component wiring stay engine-owned.
func (r Resolved) ValidateBuildPlan(plan platform.BuildPlan) error {
	selected := make(map[string]struct{}, len(plan.Components))
	for _, component := range plan.Components {
		selected[component] = struct{}{}
	}
	if _, hasCoordinatorAPI := selected["admin.coordinator"]; hasCoordinatorAPI {
		if !platform.ValidAccessGroupKey(r.Coordinator.WorkflowAccessGroup) {
			return ErrCoordinatorProfileMismatch
		}
		if _, configured := r.Core.AccessGroups[r.Coordinator.WorkflowAccessGroup]; !configured {
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
	if _, hasContentEngine := selected["engine.content"]; hasContentEngine {
		if _, hasWebsiteEngine := selected["engine.website"]; !hasWebsiteEngine {
			return errors.New("Content Engine requires engine.website")
		}
		if _, fixtures := selected["development.fixtures"]; !fixtures {
			if r.ContentAPI.ServiceCredential == "" || r.ContentAPI.Locale == "" {
				return errors.New("Content Engine requires an explicit service credential and locale")
			}
		}
	}
	if _, hasPublicContentRuntime := selected["public.content-runtime"]; hasPublicContentRuntime {
		if _, hasContentEngine := selected["engine.content"]; !hasContentEngine {
			return errors.New("public.content-runtime requires engine.content")
		}
		if _, hasPublicRouter := selected["public.router"]; !hasPublicRouter {
			return errors.New("public.content-runtime requires public.router")
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
	scales, err := validateScales(c)
	if err != nil {
		return Resolved{}, err
	}
	c.Core.CacheMaxBytes = scales.CoreCache.MaxBytes
	c.Core.CacheTTL = scales.CoreCache.TTL
	c.Core.MaxCachedObjectBytes = scales.CoreCache.MaxObjectBytes
	c.Loader.MaxConcurrentCommits = scales.LoaderConcurrency.MaxConcurrentCommits
	c.Coordinator.ExecutorConcurrency = configuredWorkerLimit(c.Worker)
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
	coordinatorDurationValues := []string{c.Coordinator.InstanceLease, c.Coordinator.HeartbeatInterval, c.Coordinator.TaskLease,
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
		c.Coordinator.ExecutorConcurrency < 1 || c.Coordinator.ExecutorConcurrency > 256 || c.Coordinator.MaxAttempts < 1 || c.Coordinator.MaxAttempts > 100 ||
		c.Coordinator.MaxWorkflowTasks < 1 || c.Coordinator.MaxWorkflowTasks > 100000 ||
		c.Coordinator.MaxChildrenPerTask < 0 || c.Coordinator.MaxChildrenPerTask > 256 ||
		coordinatorDurations[7] > 365*24*time.Hour ||
		c.Coordinator.WorkflowsPageDefault < 1 || c.Coordinator.WorkflowsPageMaximum < c.Coordinator.WorkflowsPageDefault || c.Coordinator.WorkflowsPageMaximum > 200 ||
		(c.Coordinator.WorkflowAccessGroup != "" && !platform.ValidAccessGroupKey(c.Coordinator.WorkflowAccessGroup)) {
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
	if c.Coordinator.WorkflowAccessGroup != "" {
		if _, exists := accessGroups[c.Coordinator.WorkflowAccessGroup]; !exists {
			return Resolved{}, errors.New("Coordinator workflow access group must exist in Core access_groups")
		}
	}
	if c.Loader.MaxFileBytes <= 0 || c.Loader.MaxFileBytes > c.Core.MaxObjectBytes || c.Loader.MaxPartBytes <= 0 || c.Loader.MaxPartBytes > c.Core.MaxObjectBytes ||
		c.Loader.PartSize <= 0 || c.Loader.PartSize > c.Loader.MaxPartBytes || c.Loader.MaxParts <= 0 || c.Loader.MaxParts > 1000 ||
		c.Loader.MaxConcurrentCommits <= 0 || c.Loader.MaxConcurrentCommits > 100 ||
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
	if c.ContentAPI.ServiceCredentialEnv != "" && (!envName.MatchString(c.ContentAPI.ServiceCredentialEnv) || len(c.ContentAPI.ServiceCredentialEnv) > 128) {
		return Resolved{}, errors.New("invalid Content service credential environment name")
	}
	// A missing content_api block is valid for profiles without engine.content.
	// Once either field is present, both values must be explicitly usable.
	if c.ContentAPI.ServiceCredentialEnv != "" || c.ContentAPI.Locale != "" {
		if !validContentLocale(c.ContentAPI.Locale) {
			return Resolved{}, errors.New("invalid Content locale")
		}
	}
	publicationConfigured := len(c.Publication.Bindings) > 0 || c.Publication.WebsiteCredentialEnv != "" ||
		c.Publication.BuildTimeout != "" || c.Publication.PollInterval != ""
	var publicationBuildTimeout, publicationPollInterval time.Duration
	publicationTargets := make([]publicationapp.Target, 0, len(c.Publication.Bindings))
	if publicationConfigured {
		if len(c.Publication.Bindings) == 0 || len(c.Publication.Bindings) > 256 ||
			!envName.MatchString(c.Publication.WebsiteCredentialEnv) || c.RouterAPI.ServiceCredentialEnv == "" {
			return Resolved{}, errors.New("publication requires trusted bindings and explicit Website and Router service credentials")
		}
		var err error
		publicationBuildTimeout, err = time.ParseDuration(c.Publication.BuildTimeout)
		if err != nil || publicationBuildTimeout <= 0 {
			return Resolved{}, errors.New("invalid publication build timeout")
		}
		publicationPollInterval, err = time.ParseDuration(c.Publication.PollInterval)
		if err != nil || publicationPollInterval <= 0 || publicationPollInterval >= publicationBuildTimeout {
			return Resolved{}, errors.New("invalid publication poll interval")
		}
		seen := make(map[string]struct{}, len(c.Publication.Bindings))
		for _, binding := range c.Publication.Bindings {
			routeID, routeErr := uuid.Parse(binding.RouteID)
			snapshotID, snapshotErr := uuid.Parse(binding.PageSnapshotID)
			if routeErr != nil || routeID == uuid.Nil || snapshotErr != nil || snapshotID == uuid.Nil ||
				!validContentLocale(binding.Locale) || binding.RouteID != routeID.String() || binding.PageSnapshotID != snapshotID.String() {
				return Resolved{}, errors.New("publication bindings require canonical UUID route/page snapshot identities and a valid locale")
			}
			key := binding.RouteID + "\x00" + binding.Locale
			if _, duplicate := seen[key]; duplicate {
				return Resolved{}, errors.New("duplicate publication binding")
			}
			seen[key] = struct{}{}
			publicationTargets = append(publicationTargets, publicationapp.Target{
				RouteID: binding.RouteID, PageSnapshotID: binding.PageSnapshotID, Locale: binding.Locale,
			})
		}
	}
	retirementConfigured := len(c.Retirement.Bindings) > 0 || c.Retirement.WebsiteCredentialEnv != ""
	retirementRoutes := make(map[string][]string, len(c.Retirement.Bindings))
	if retirementConfigured {
		if len(c.Retirement.Bindings) == 0 || len(c.Retirement.Bindings) > 256 ||
			!envName.MatchString(c.Retirement.WebsiteCredentialEnv) || c.RouterAPI.ServiceCredentialEnv == "" {
			return Resolved{}, errors.New("retirement requires trusted bindings and explicit Website and Router service credentials")
		}
		seen := make(map[string]struct{}, len(c.Retirement.Bindings))
		for _, binding := range c.Retirement.Bindings {
			pageID, pageErr := uuid.Parse(binding.PageID)
			if pageErr != nil || pageID == uuid.Nil || binding.PageID != pageID.String() ||
				len(binding.RouteIDs) == 0 || len(binding.RouteIDs) > 64 {
				return Resolved{}, errors.New("retirement bindings require a canonical page UUID and between 1 and 64 route IDs")
			}
			if _, duplicatePage := seen[binding.PageID]; duplicatePage {
				return Resolved{}, errors.New("duplicate retirement page binding")
			}
			seen[binding.PageID] = struct{}{}
			routes := make([]string, 0, len(binding.RouteIDs))
			routeSeen := make(map[string]struct{}, len(binding.RouteIDs))
			for _, routeIDValue := range binding.RouteIDs {
				routeID, routeErr := uuid.Parse(routeIDValue)
				if routeErr != nil || routeID == uuid.Nil || routeIDValue != routeID.String() {
					return Resolved{}, errors.New("retirement bindings require canonical route UUIDs")
				}
				if _, duplicateRoute := routeSeen[routeIDValue]; duplicateRoute {
					return Resolved{}, errors.New("duplicate retirement route binding")
				}
				routeSeen[routeIDValue] = struct{}{}
				routes = append(routes, routeIDValue)
			}
			retirementRoutes[binding.PageID] = routes
		}
	}
	featureImportConfigured := c.FeatureImport.TargetFileSetID != "" || c.FeatureImport.StagingDirectory != "" ||
		c.FeatureImport.ServiceCredentialEnv != "" || len(c.FeatureImport.OutputGroups) > 0 || c.FeatureImport.MaxArchiveBytes != 0 ||
		c.FeatureImport.MaxEntries != 0 || c.FeatureImport.MaxEntryBytes != 0 || c.FeatureImport.MaxExpandedBytes != 0 || c.FeatureImport.MaxCompressionRatio != 0
	if featureImportConfigured {
		settings := c.FeatureImport
		if !filepath.IsAbs(settings.StagingDirectory) || filepath.Clean(settings.StagingDirectory) != settings.StagingDirectory ||
			settings.StagingDirectory == string(filepath.Separator) || !envName.MatchString(settings.ServiceCredentialEnv) ||
			strings.TrimSpace(settings.TargetFileSetID) == "" || len(settings.OutputGroups) == 0 ||
			settings.MaxArchiveBytes <= 0 || settings.MaxEntries <= 0 || settings.MaxEntryBytes <= 0 ||
			settings.MaxExpandedBytes <= 0 || settings.MaxCompressionRatio == 0 {
			return Resolved{}, errors.New("feature_import requires an absolute staging directory, service credential, target FileSet, output groups, and explicit archive limits")
		}
		for _, group := range settings.OutputGroups {
			if !platform.ValidAccessGroupKey(group) {
				return Resolved{}, errors.New("feature_import output_groups contains an invalid group")
			}
		}
	}
	mediaConfigured := c.Media.ServiceCredentialEnv != "" || c.Media.SandboxImage != "" || c.Media.DockerBinary != "" ||
		c.Media.StagingDirectory != "" || c.Media.ProcessorVersion != "" || c.Media.CPUs != "" || c.Media.MemoryLimitBytes != 0 ||
		c.Media.PIDsLimit != 0 || c.Media.MaxInputBytes != 0 || c.Media.MaxOutputBytes != 0 || c.Media.MaxConcurrency != 0 || c.Media.Timeout != ""
	if mediaConfigured {
		settings := c.Media
		if !envName.MatchString(settings.ServiceCredentialEnv) || !strings.Contains(settings.SandboxImage, "@sha256:") ||
			strings.TrimSpace(settings.DockerBinary) == "" || !filepath.IsAbs(settings.StagingDirectory) ||
			filepath.Clean(settings.StagingDirectory) != settings.StagingDirectory || settings.StagingDirectory == string(filepath.Separator) ||
			settings.ProcessorVersion == "" || settings.CPUs == "" || settings.MemoryLimitBytes <= 0 || settings.PIDsLimit <= 0 ||
			settings.MaxInputBytes <= 0 || settings.MaxOutputBytes <= 0 || settings.MaxConcurrency <= 0 {
			return Resolved{}, errors.New("media requires a sandbox image digest, service credential, staging directory, processor version and explicit limits")
		}
	}
	prerenderConfigured := c.Prerender.SandboxImage != "" || c.Prerender.SandboxPlatform != "" || c.Prerender.DockerBinary != "" ||
		c.Prerender.StagingDirectory != "" || c.Prerender.CPUs != "" || c.Prerender.MemoryLimitBytes != 0 || c.Prerender.PIDsLimit != 0 ||
		c.Prerender.MaxDocumentBytes != 0 || c.Prerender.MaxPlanBytes != 0 || c.Prerender.MaxArtifacts != 0 ||
		c.Prerender.MaxArtifactBytes != 0 || c.Prerender.MaxTotalBytes != 0 || c.Prerender.Timeout != ""
	if prerenderConfigured {
		settings := c.Prerender
		if !strings.Contains(settings.SandboxImage, "@sha256:") ||
			(settings.SandboxPlatform != "linux/amd64" && settings.SandboxPlatform != "linux/arm64") ||
			strings.TrimSpace(settings.DockerBinary) == "" || !filepath.IsAbs(settings.StagingDirectory) ||
			filepath.Clean(settings.StagingDirectory) != settings.StagingDirectory || settings.StagingDirectory == string(filepath.Separator) ||
			settings.CPUs == "" || settings.MemoryLimitBytes <= 0 || settings.PIDsLimit <= 0 || settings.MaxDocumentBytes <= 0 ||
			settings.MaxPlanBytes <= 0 || settings.MaxArtifacts <= 0 || settings.MaxArtifactBytes <= 0 || settings.MaxTotalBytes < settings.MaxArtifactBytes {
			return Resolved{}, errors.New("prerender requires a digest-pinned image, platform, docker binary, private staging directory and explicit limits")
		}
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
	var publicationWebsiteCredential string
	if publicationConfigured {
		publicationWebsiteCredential = getenv(c.Publication.WebsiteCredentialEnv)
		if publicationWebsiteCredential == "" || strings.ContainsRune(publicationWebsiteCredential, '\x00') {
			return Resolved{}, errors.New("configured publication Website credential is empty or invalid")
		}
	}
	var retirementWebsiteCredential string
	if retirementConfigured {
		retirementWebsiteCredential = getenv(c.Retirement.WebsiteCredentialEnv)
		if retirementWebsiteCredential == "" || strings.ContainsRune(retirementWebsiteCredential, '\x00') {
			return Resolved{}, errors.New("configured retirement Website credential is empty or invalid")
		}
	}
	var formsFileServiceCredential string
	if c.FormsAPI.FileServiceCredentialEnv != "" {
		formsFileServiceCredential = getenv(c.FormsAPI.FileServiceCredentialEnv)
		if formsFileServiceCredential == "" || strings.ContainsRune(formsFileServiceCredential, '\x00') {
			return Resolved{}, errors.New("configured Forms file service credential is empty or invalid")
		}
	}
	var contentServiceCredential string
	if c.ContentAPI.ServiceCredentialEnv != "" {
		contentServiceCredential = getenv(c.ContentAPI.ServiceCredentialEnv)
		if contentServiceCredential == "" || strings.ContainsRune(contentServiceCredential, '\x00') {
			return Resolved{}, errors.New("configured Content service credential is empty or invalid")
		}
	}
	var featureImportCredential string
	if featureImportConfigured {
		featureImportCredential = getenv(c.FeatureImport.ServiceCredentialEnv)
		if featureImportCredential == "" || strings.ContainsRune(featureImportCredential, '\x00') {
			return Resolved{}, errors.New("configured FeatureImport service credential is empty or invalid")
		}
	}
	var prerenderTimeout time.Duration
	if prerenderConfigured {
		parsed, err := time.ParseDuration(c.Prerender.Timeout)
		if err != nil || parsed <= 0 {
			return Resolved{}, errors.New("prerender requires an explicit positive timeout")
		}
		prerenderTimeout = parsed
	}
	var mediaCredential string
	var mediaTimeout time.Duration
	if mediaConfigured {
		mediaCredential = getenv(c.Media.ServiceCredentialEnv)
		if mediaCredential == "" || strings.ContainsRune(mediaCredential, '\x00') {
			return Resolved{}, errors.New("configured media service credential is empty or invalid")
		}
		parsed, err := time.ParseDuration(c.Media.Timeout)
		if err != nil || parsed <= 0 {
			return Resolved{}, errors.New("media requires an explicit positive timeout")
		}
		mediaTimeout = parsed
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
		S3:                   platform.S3Config{Endpoint: c.Core.S3Endpoint, Region: c.Core.S3Region, Bucket: c.Core.S3Bucket, AccessKey: accessKey, SecretKey: secretKey, PathStyle: c.Core.S3PathStyle, AllowInsecureHTTP: c.Core.S3AllowInsecureHTTP, InsecureAllowedHosts: append([]string(nil), c.Core.S3InsecureAllowedHosts...)},
		BootstrapSecret:      bootstrap, AtlasBinary: c.Core.AtlasBinary, MigrationLockTimeout: parsed[4], AccessGroups: accessGroups,
		CacheMaxBytes: c.Core.CacheMaxBytes, CacheTTL: parsed[5], MaxMetadataBytes: c.Core.MaxMetadataBytes,
		MaxObjectBytes: c.Core.MaxObjectBytes, MaxCachedObjectBytes: c.Core.MaxCachedObjectBytes,
		CacheShards: scales.CoreConcurrency.Cache.Shards, CachePromotionInterval: scales.CoreConcurrency.Cache.LRUPromotionInterval,
		CoalesceEnabled: scales.CoreConcurrency.Cache.Coalesce.Enabled, CoalesceMaxWaiters: scales.CoreConcurrency.Cache.Coalesce.MaxWaiters,
		CoalesceTimeout: resolvedScalesTimeout(scales, "coalesce"), InvalidationBatchSize: scales.CoreConcurrency.Invalidation.BatchSize,
		InvalidationBatchWindow: resolvedScalesTimeout(scales, "invalidation"),
		ModuleData:              moduleDataLimits,
		MaintenanceInterval:     parsed[6], MaintenanceTimeout: parsed[7], PendingTTL: parsed[8], MaintenanceBatchSize: c.Core.MaintenanceBatchSize,
		SessionIdleTTL: parsed[9], SessionAbsoluteTTL: parsed[10],
	}
	loaderConfig := loaderapp.Config{PartSize: c.Loader.PartSize, MaxFileBytes: c.Loader.MaxFileBytes, MaxPartBytes: c.Loader.MaxPartBytes, MaxParts: c.Loader.MaxParts,
		MaxConcurrentCommits:        c.Loader.MaxConcurrentCommits,
		MaxConcurrentSessionLookups: scales.LoaderConcurrency.MaxConcurrentSessionLookups,
		SessionTTL:                  loaderDurations[0], CommitTimeout: loaderDurations[1], Mode: c.Loader.Mode, BufferThreshold: c.Loader.BufferThreshold,
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
	observabilityMode := observability.Mode(c.Observability.Mode)
	if observabilityMode == "" {
		observabilityMode = observability.ModeNoop
	}
	observabilityHeaders := make(map[string]string)
	if c.Observability.OTLP.HeadersEnv != "" {
		raw := getenv(c.Observability.OTLP.HeadersEnv)
		if raw == "" {
			return Resolved{}, errors.New("configured observability headers environment variable is empty")
		}
		for _, pair := range strings.Split(raw, ",") {
			key, value, found := strings.Cut(strings.TrimSpace(pair), "=")
			if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return Resolved{}, errors.New("observability headers must be comma-separated key=value pairs")
			}
			observabilityHeaders[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	var observabilityTimeout time.Duration
	if c.Observability.OTLP.Timeout != "" {
		observabilityTimeout, err = time.ParseDuration(c.Observability.OTLP.Timeout)
		if err != nil || observabilityTimeout <= 0 {
			return Resolved{}, errors.New("invalid observability OTLP timeout")
		}
	}
	observabilityConfig := observability.Config{
		Mode: observabilityMode, MinLevel: observability.Level(c.Observability.MinLevel),
		ServiceVersion: c.Observability.ServiceVersion, InstanceID: c.Observability.InstanceID,
		Directory: c.Observability.File.Directory, MaxFileBytes: c.Observability.File.MaxFileBytes,
		MaxFiles: c.Observability.File.MaxFiles,
		Endpoint: c.Observability.OTLP.Endpoint, Insecure: c.Observability.OTLP.Insecure,
		Headers: observabilityHeaders, Timeout: observabilityTimeout,
	}
	if err := observabilityConfig.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("invalid observability configuration: %w", err)
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
		ContentAPI: ContentAPISettings{ServiceCredential: contentServiceCredential, Locale: c.ContentAPI.Locale},
		Publication: PublicationSettings{Enabled: publicationConfigured, Targets: publicationTargets,
			BuildTimeout: publicationBuildTimeout, PollInterval: publicationPollInterval,
			WebsiteCredential: publicationWebsiteCredential, RouterCredential: routerServiceCredential},
		Retirement: RetirementSettings{Enabled: retirementConfigured, Routes: retirementRoutes,
			WebsiteCredential: retirementWebsiteCredential, RouterCredential: routerServiceCredential},
		FeatureImport: FeatureImportSettings{Enabled: featureImportConfigured,
			TargetFileSetID: c.FeatureImport.TargetFileSetID, OutputGroups: append([]string(nil), c.FeatureImport.OutputGroups...),
			StagingDirectory: c.FeatureImport.StagingDirectory, ServiceCredential: featureImportCredential,
			Limits: archive.Limits{MaxArchiveBytes: c.FeatureImport.MaxArchiveBytes, MaxEntries: c.FeatureImport.MaxEntries,
				MaxEntryBytes: c.FeatureImport.MaxEntryBytes, MaxExpandedBytes: c.FeatureImport.MaxExpandedBytes,
				MaxCompressionRatio: c.FeatureImport.MaxCompressionRatio}},
		Prerender: PrerenderSettings{Enabled: prerenderConfigured, SandboxImage: c.Prerender.SandboxImage,
			SandboxPlatform: c.Prerender.SandboxPlatform, DockerBinary: c.Prerender.DockerBinary,
			StagingDirectory: c.Prerender.StagingDirectory, CPUs: c.Prerender.CPUs,
			MemoryLimitBytes: c.Prerender.MemoryLimitBytes, PIDsLimit: c.Prerender.PIDsLimit,
			MaxDocumentBytes: c.Prerender.MaxDocumentBytes, MaxPlanBytes: c.Prerender.MaxPlanBytes,
			MaxArtifacts: c.Prerender.MaxArtifacts, MaxArtifactBytes: c.Prerender.MaxArtifactBytes,
			MaxTotalBytes: c.Prerender.MaxTotalBytes, Timeout: prerenderTimeout},
		Media: MediaSettings{Enabled: mediaConfigured, ServiceCredential: mediaCredential,
			SandboxImage: c.Media.SandboxImage, DockerBinary: c.Media.DockerBinary, StagingDirectory: c.Media.StagingDirectory,
			ProcessorVersion: c.Media.ProcessorVersion, CPUs: c.Media.CPUs, MemoryLimitBytes: c.Media.MemoryLimitBytes,
			PIDsLimit: c.Media.PIDsLimit, MaxInputBytes: c.Media.MaxInputBytes, MaxOutputBytes: c.Media.MaxOutputBytes,
			MaxConcurrency: c.Media.MaxConcurrency, Timeout: mediaTimeout},
		Coordinator: CoordinatorSettings{
			WorkflowAccessGroup:  c.Coordinator.WorkflowAccessGroup,
			WorkflowsPageDefault: c.Coordinator.WorkflowsPageDefault, WorkflowsPageMaximum: c.Coordinator.WorkflowsPageMaximum,
			InstanceLease: coordinatorDurations[0], HeartbeatInterval: coordinatorDurations[1],
			ExecutorConcurrency: c.Coordinator.ExecutorConcurrency, TaskLease: coordinatorDurations[2], TaskTimeout: coordinatorDurations[3],
			PollInterval: coordinatorDurations[4], RetryBase: coordinatorDurations[5], RetryMaximum: coordinatorDurations[6],
			MaxAttempts: c.Coordinator.MaxAttempts, MaxWorkflowTasks: c.Coordinator.MaxWorkflowTasks,
			MaxChildrenPerTask: c.Coordinator.MaxChildrenPerTask, WorkflowRetention: coordinatorDurations[7],
		}, CoordinatorFingerprint: coordinatorFingerprint, HTTP: HTTPServerConfig{ListenAddress: c.HTTP.ListenAddress,
			ReadHeaderTimeout: httpDurations[0], ReadTimeout: httpDurations[1], WriteTimeout: httpDurations[2], IdleTimeout: httpDurations[3],
			ShutdownTimeout: httpDurations[4], MaxHeaderBytes: c.HTTP.MaxHeaderBytes}, Observability: observabilityConfig, Scales: scales}, nil
}

type Config struct {
	Core          Core                `yaml:"core"`
	Loader        Loader              `yaml:"loader"`
	Admin         AdminConfig         `yaml:"admin"`
	Router        RouterConfig        `yaml:"router"`
	Bus           BusConfig           `yaml:"bus"`
	Worker        WorkerConfig        `yaml:"worker"`
	FileAPI       FileAPI             `yaml:"file_api"`
	AdminAPI      AdminAPI            `yaml:"admin_api"`
	RouterAPI     RouterAPI           `yaml:"router_api"`
	FormsAPI      FormsAPI            `yaml:"forms_api"`
	ContentAPI    ContentAPI          `yaml:"content_api"`
	Publication   Publication         `yaml:"publication"`
	Retirement    Retirement          `yaml:"retirement"`
	FeatureImport FeatureImport       `yaml:"feature_import"`
	Media         Media               `yaml:"media"`
	Prerender     Prerender           `yaml:"prerender"`
	HTTP          HTTP                `yaml:"http"`
	Coordinator   CoordinatorConfig   `yaml:"coordinator"`
	Observability ObservabilityConfig `yaml:"observability"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func configFingerprint(config Config, runtimeURL, migrationURL string) (string, error) {
	config.Core.DatabaseURLEnv = ""
	config.Core.MigrationDatabaseURLEnv = ""
	config.Core.S3AccessKeyEnv = ""
	config.Core.S3SecretKeyEnv = ""
	config.Core.S3InsecureAllowedHosts = nil
	config.Core.BootstrapSecretEnv = ""
	// Each public Router process serves exactly one configured branch. Branch
	// selection and its listener are deployment-local bindings, while
	// every shared Router policy (credentials, limits, staging, and APIs) must
	// remain identical for processes sharing the same Core Coordinator.
	config.RouterAPI.BranchID = ""
	config.RouterAPI.PublicListenAddress = ""
	config.Publication.WebsiteCredentialEnv = ""
	config.Retirement.WebsiteCredentialEnv = ""
	// Telemetry is deployment-local and does not change the shared Coordinator policy.
	config.Observability = ObservabilityConfig{}
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
	if u.Scheme == "http" && !cfg.Core.S3AllowInsecureHTTP {
		return Config{}, errors.New("unencrypted S3 endpoints require explicit opt-in")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) && !insecureHostAllowed(cfg.Core.S3InsecureAllowedHosts, u.Hostname()) {
		return Config{}, errors.New("unencrypted S3 endpoints must use loopback or an explicitly allowed host")
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

// insecureHostAllowed accepts only exact hostnames explicitly listed by the
// operator. Wildcards and IP ranges are deliberately not supported.
func insecureHostAllowed(hosts []string, host string) bool {
	want := strings.ToLower(strings.TrimSpace(host))
	for _, item := range hosts {
		candidate := strings.ToLower(strings.TrimSpace(item))
		if candidate != "" && candidate == want {
			return true
		}
	}
	return false
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

func validContentLocale(value string) bool {
	if value == "" || len(value) > 35 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
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
