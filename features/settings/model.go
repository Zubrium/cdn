// Package settings exposes a read-only, sanitized view of the runtime
// configuration. The admin panel renders it in the "Настройки" section so
// operators see the real limits and the real permission matrix instead of
// hardcoded placeholders.
//
// No secret ever leaves this package: S3 credentials, database URLs and
// bootstrap tokens are intentionally absent.
package settings

// Limits mirrors the request and pagination limits enforced by the server.
type Limits struct {
	MaxJSONBytes            int64 `json:"max_json_bytes"`
	MaxUploadBytes          int64 `json:"max_upload_bytes"`
	FormRateLimit           int   `json:"form_rate_limit"`
	FormRateWindowSeconds   int   `json:"form_rate_window_seconds"`
	AdminPageSize           int   `json:"admin_page_size"`
	PublicPageSize          int   `json:"public_page_size"`
	SubmissionExportMaxRows int   `json:"submission_export_max_rows"`
}

// PublicAPI describes the publicly reachable API surface.
type PublicAPI struct {
	RuntimePath string   `json:"runtime_path"`
	FormsPath   string   `json:"forms_path"`
	AdminPath   string   `json:"admin_path"`
	MediaPath   string   `json:"media_path"`
	CORSEnabled bool     `json:"cors_enabled"`
	CORSOrigins []string `json:"cors_origins"`
}

// Media describes the accepted upload formats and the generated variants.
type Media struct {
	AcceptedTypes  []string `json:"accepted_types"`
	MaxUploadBytes int64    `json:"max_upload_bytes"`
	ImageVariants  []string `json:"image_variants"`
}

// RolePermission is a single row of the permission matrix.
type RolePermission struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

// Role describes what a role may do. The actions listed here mirror the
// checks implemented by the HTTP middleware and the domain services.
type Role struct {
	ID          string           `json:"id"`
	Label       string           `json:"label"`
	Summary     string           `json:"summary"`
	Permissions []RolePermission `json:"permissions"`
	Notes       []string         `json:"notes"`
}

// Settings is the payload of GET /api/v1/admin/settings.
type Settings struct {
	Limits    Limits    `json:"limits"`
	PublicAPI PublicAPI `json:"public_api"`
	Media     Media     `json:"media"`
	Roles     []Role    `json:"roles"`
}

// CORS is the effective public CORS configuration.
type CORS struct {
	Enabled bool     `json:"enabled"`
	Origins []string `json:"origins"`
}
