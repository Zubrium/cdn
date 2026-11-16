package models

import "time"

// AccessToken is an API token row of the access_tokens table.
type AccessToken struct {
	ID         UUID       `gorm:"column:id;primaryKey;not null"`
	Name       string     `gorm:"column:name;not null"`
	TokenHash  []byte     `gorm:"column:token_hash;not null;uniqueIndex:uq_access_tokens_token_hash"`
	Role       string     `gorm:"column:role;not null"`
	CreatedBy  *UUID      `gorm:"column:created_by;index:idx_access_tokens_creator"`
	ExpiresAt  *time.Time `gorm:"column:expires_at"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;default:now()"`
}

// TableName implements the GORM Tabler interface.
func (AccessToken) TableName() string { return "access_tokens" }

// Feature is a row of the features table.
type Feature struct {
	ID        UUID      `gorm:"column:id;primaryKey;not null"`
	Key       string    `gorm:"column:key;not null;uniqueIndex:uq_features_key"`
	Name      string    `gorm:"column:name;not null"`
	Status    string    `gorm:"column:status;not null;default:active"`
	Revision  int64     `gorm:"column:revision;not null;default:1"`
	CreatedBy *UUID     `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now();autoUpdateTime"`
}

// TableName implements the GORM Tabler interface.
func (Feature) TableName() string { return "features" }

// FeatureVersion is a row of the feature_versions table.
type FeatureVersion struct {
	ID          UUID       `gorm:"column:id;primaryKey;not null;uniqueIndex:uq_feature_versions_identity,priority:1"`
	FeatureID   UUID       `gorm:"column:feature_id;not null;uniqueIndex:uq_feature_versions_feature_version,priority:1;uniqueIndex:uq_feature_versions_identity,priority:2;index:idx_feature_versions_feature,priority:1"`
	Version     int        `gorm:"column:version;not null;type:integer;uniqueIndex:uq_feature_versions_feature_version,priority:2;index:idx_feature_versions_feature,priority:2,sort:desc"`
	Schema      JSONB      `gorm:"column:schema;not null"`
	Status      string     `gorm:"column:status;not null;default:draft"`
	CreatedBy   *UUID      `gorm:"column:created_by"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;default:now()"`
	PublishedAt *time.Time `gorm:"column:published_at"`
}

// TableName implements the GORM Tabler interface.
func (FeatureVersion) TableName() string { return "feature_versions" }

// FormSubmission is a row of the form_submissions table.
type FormSubmission struct {
	ID              UUID       `gorm:"column:id;primaryKey;not null"`
	FeatureID       UUID       `gorm:"column:feature_id;not null"`
	SchemaVersionID UUID       `gorm:"column:schema_version_id;not null"`
	Data            JSONB      `gorm:"column:data;not null"`
	Status          string     `gorm:"column:status;not null;default:new"`
	ProcessedBy     *UUID      `gorm:"column:processed_by"`
	Environment     string     `gorm:"column:environment;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null;default:now();autoUpdateTime"`
	ArchivedAt      *time.Time `gorm:"column:archived_at"`
	DeletedAt       *time.Time `gorm:"column:deleted_at"`
}

// TableName implements the GORM Tabler interface.
func (FormSubmission) TableName() string { return "form_submissions" }

// MediaObject is a row of the media_objects table.
type MediaObject struct {
	ID               UUID      `gorm:"column:id;primaryKey;not null"`
	ObjectKey        string    `gorm:"column:object_key;not null;uniqueIndex:uq_media_objects_object_key"`
	OriginalFilename string    `gorm:"column:original_filename;not null"`
	ContentType      string    `gorm:"column:content_type;not null"`
	SizeBytes        int64     `gorm:"column:size_bytes;not null"`
	Status           string    `gorm:"column:status;not null;default:processing;index:idx_media_objects_status_created,priority:1"`
	Variants         JSONMap   `gorm:"column:variants;not null;default:'{}'"`
	UploadedBy       *UUID     `gorm:"column:uploaded_by"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:now();index:idx_media_objects_status_created,priority:2,sort:desc"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;default:now();autoUpdateTime"`
}

// TableName implements the GORM Tabler interface.
func (MediaObject) TableName() string { return "media_objects" }

// SiteSetting is a row of the singleton site_settings table.
type SiteSetting struct {
	ID        bool      `gorm:"column:id;primaryKey;not null;default:true"`
	Name      string    `gorm:"column:name;not null;default:Website"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;default:now();autoUpdateTime"`
}

// TableName implements the GORM Tabler interface.
func (SiteSetting) TableName() string { return "site_settings" }

// SiteEnvironment is a row of the site_environments table.
type SiteEnvironment struct {
	Key              string    `gorm:"column:key;primaryKey;not null"`
	Name             string    `gorm:"column:name;not null"`
	PublicBaseURL    string    `gorm:"column:public_base_url;not null;default:''"`
	HTTPPort         int       `gorm:"column:http_port;not null;type:integer"`
	CORSOrigins      TextArray `gorm:"column:cors_origins;not null;default:'{}'"`
	ActiveSnapshotID *UUID     `gorm:"column:active_snapshot_id"`
	Revision         int64     `gorm:"column:revision;not null;default:1"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;default:now();autoUpdateTime"`
}

// TableName implements the GORM Tabler interface.
func (SiteEnvironment) TableName() string { return "site_environments" }

// FeatureBuild is an immutable ESM artifact and its developer-authored manifest.
type FeatureBuild struct {
	ID                        UUID      `gorm:"column:id;primaryKey;not null;uniqueIndex:uq_feature_builds_identity,priority:1"`
	FeatureID                 UUID      `gorm:"column:feature_id;not null;uniqueIndex:uq_feature_builds_feature_version,priority:1;uniqueIndex:uq_feature_builds_identity,priority:2;index:idx_feature_builds_feature,priority:1"`
	Version                   string    `gorm:"column:version;not null;uniqueIndex:uq_feature_builds_feature_version,priority:2"`
	SubmissionSchemaVersionID *UUID     `gorm:"column:submission_schema_version_id"`
	Digest                    string    `gorm:"column:digest;not null;uniqueIndex:uq_feature_builds_digest"`
	ArtifactSize              int64     `gorm:"column:artifact_size;not null"`
	Manifest                  JSONB     `gorm:"column:manifest;not null"`
	Assets                    JSONB     `gorm:"column:assets;not null"`
	CreatedBy                 *UUID     `gorm:"column:created_by"`
	CreatedAt                 time.Time `gorm:"column:created_at;not null;default:now();index:idx_feature_builds_feature,priority:2,sort:desc"`
}

func (FeatureBuild) TableName() string { return "feature_builds" }

// SiteSnapshot is an immutable full deployment state for the single site.
type SiteSnapshot struct {
	ID        UUID      `gorm:"column:id;primaryKey;not null"`
	Sequence  int64     `gorm:"column:sequence;not null;uniqueIndex:uq_site_snapshots_sequence;autoIncrement"`
	Name      string    `gorm:"column:name;not null"`
	Digest    string    `gorm:"column:digest;not null;index:idx_site_snapshots_digest"`
	Payload   JSONB     `gorm:"column:payload;not null"`
	CreatedBy *UUID     `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now();index:idx_site_snapshots_created,priority:1,sort:desc"`
}

func (SiteSnapshot) TableName() string { return "site_snapshots" }

// SiteSnapshotEvent records snapshot deployments, promotion and rollback.
type SiteSnapshotEvent struct {
	ID           UUID      `gorm:"column:id;primaryKey;not null"`
	SnapshotID   UUID      `gorm:"column:snapshot_id;not null;index:idx_site_snapshot_events_env,priority:2"`
	Environment  string    `gorm:"column:environment;not null;index:idx_site_snapshot_events_env,priority:1"`
	Action       string    `gorm:"column:action;not null"`
	ActorTokenID *UUID     `gorm:"column:actor_token_id"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;default:now();index:idx_site_snapshot_events_env,priority:3,sort:desc"`
}

func (SiteSnapshotEvent) TableName() string { return "site_snapshot_events" }

// All returns every model managed by the engine in dependency order.
func All() []any {
	return []any{
		&AccessToken{},
		&Feature{},
		&FeatureVersion{},
		&FeatureBuild{},
		&FormSubmission{},
		&MediaObject{},
		&SiteSetting{},
		&SiteEnvironment{},
		&SiteSnapshot{},
		&SiteSnapshotEvent{},
	}
}
