// Types mirror the authoritative Go API contract. Timestamps are RFC 3339
// strings in UTC, IDs are UUID strings and nullable fields may be null.

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export type Role = "admin" | "moderator" | "staff" | "user";
export type FeatureType = "collection" | "single" | "form";
export type FeatureStatus = "active" | "archived";
export type VersionStatus = "draft" | "published";
export type MediaStatus = "processing" | "ready" | "failed" | "deleted";
export type SubmissionStatus = "new" | "in_progress" | "processed" | "rejected";
export type Environment = "dev" | "prod";

export interface ApiErrorDetail {
  path?: string;
  code?: string;
  message: string;
}

export interface ApiErrorPayload {
  code: string;
  message: string;
  details?: ApiErrorDetail[];
  request_id?: string;
}

export interface ListResponse<T> {
  items: T[];
  next_cursor: string | null;
}

export interface Principal {
  id: string;
  name: string;
  role: Role;
  created_by?: string;
  expires_at: string | null;
  created_at: string;
}

export interface Token {
  id: string;
  name: string;
  role: Role;
  created_by: string | null;
  expires_at: string | null;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface CreatedToken {
  id: string;
  name: string;
  token: string;
  role: Role;
  expires_at: string | null;
  created_at: string;
}

export interface Feature {
  id: string;
  key: string;
  name: string;
  type: FeatureType;
  status: FeatureStatus;
  revision: number;
  draft_version_id: string | null;
  dev_version_id: string | null;
  prod_version_id: string | null;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface FeatureVersion {
  id: string;
  feature_id: string;
  version: number;
  schema: JsonObject;
  status: VersionStatus;
  created_by?: string;
  created_at: string;
  published_at: string | null;
}

export interface FeatureDetail extends Feature {
  draft: FeatureVersion | null;
  dev: FeatureVersion | null;
  prod: FeatureVersion | null;
}

export interface FeatureDraftState {
  revision: number;
  draft: FeatureVersion | null;
  dev: FeatureVersion | null;
  prod: FeatureVersion | null;
}

export interface ContentFeature {
  id: string;
  key: string;
  name: string;
  category?: string;
  draftRevision?: number;
  mode: FeatureType;
  version: string;
  buildDigest: string;
  schema: JsonObject;
  settingsSchema?: JsonObject;
  submissionSchema?: JsonObject;
}

export interface MediaRenditionContext {
  featureKey: string;
  buildVersion?: string;
  schemaKind: "content" | "settings" | "arguments";
  fieldPath: string;
  buildDigest: string;
}

export interface DesignFont {
  family: string;
  mediaId: string;
  weight: string;
  style: "normal" | "italic";
  unicodeRange?: string;
}

export interface DesignSystem {
  formatVersion: 1;
  tokens: Record<string, string>;
  mobileTokens: Record<string, string>;
  fonts: DesignFont[];
  fontRoles: Record<"body" | "display" | "header" | "hero", string>;
  brandLogoMediaId?: string;
}

export interface DesignSystemState {
  revision: number;
  draft: DesignSystem;
  updatedAt: string;
}

export interface DesignTokenDefinition {
  key: string;
  label: string;
  group: string;
  kind: "color" | "value";
  default: string;
  min?: number;
  max?: number;
  unit?: string;
  scope?: "mobile";
}

export interface MediaRenditionOutput {
  url: string;
  width?: number;
  height?: number;
  mimeType: string;
}

export interface MediaRenditionState {
  mediaId: string;
  profileHash?: string;
  buildDigest?: string;
  status: "pending" | "processing" | "ready" | "failed";
  outputs?: Record<string, MediaRenditionOutput>;
  error?: string;
}

export interface EntryDetail {
  id: string;
  feature_id: string;
  single_slot: boolean;
  sort_order: number;
  revision: number;
  draft_version_id: string | null;
  dev_version_id: string | null;
  prod_version_id: string | null;
  created_by?: string;
  created_at: string;
  updated_at: string;
  archived_at: string | null;
  feature_key: string;
  feature_type: FeatureType;
  draft: EntryVersion | null;
  dev: EntryVersion | null;
  prod: EntryVersion | null;
}

export interface EntryVersion {
  id: string;
  entry_id: string;
  version: number;
  schema_version_id: string;
  data: JsonObject;
  status: VersionStatus;
  created_by?: string;
  created_at: string;
  published_at: string | null;
}

export interface Submission {
  id: string;
  feature_id: string;
  feature_key: string;
  schema_version_id: string;
  environment: Environment;
  data: JsonObject;
  status: SubmissionStatus;
  processed_by: string | null;
  created_at: string;
  updated_at: string;
  archived_at?: string | null;
  deleted_at?: string | null;
}

export interface SubmissionField {
  key: string;
  label: string;
}

export type SubmissionListResponse = ListResponse<Submission> & {
  fields: SubmissionField[];
};

export interface Media {
  id: string;
  object_key: string;
  original_filename: string;
  content_type: string;
  size_bytes: number;
  status: MediaStatus;
  variants: Record<string, string>;
  uploaded_by?: string;
  created_at: string;
  updated_at: string;
}

export interface SubmissionFilters {
  environment?: Environment | "";
  status?: SubmissionStatus | "";
  created_from?: string;
  created_to?: string;
  q?: string;
  field?: string;
  include_archived?: boolean;
  sort?: "asc" | "desc";
}

export interface FeatureListParams {
  status?: FeatureStatus | "";
  type?: FeatureType | "";
  q?: string;
  limit?: number;
  cursor?: string | null;
}

export interface EntryListParams {
  include_archived?: boolean;
  q?: string;
  limit?: number;
  cursor?: string | null;
  sort?: "manual";
}

export interface MediaListParams {
  status?: MediaStatus | "";
  type?: "image" | "video" | "pdf" | "";
  q?: string;
  created_from?: string;
  created_to?: string;
  min_size?: number;
  max_size?: number;
  sort?: MediaSort | "";
  limit?: number;
  cursor?: string | null;
}

export type MediaSort = "new" | "old" | "name" | "size_desc" | "size_asc";

export interface CreateTokenInput {
  name: string;
  role: Role;
  expires_at?: string;
}

export interface OverviewCounts {
  features_active: number;
  features_archived: number;
  draft_features: number;
  draft_entries: number;
  new_submissions: number;
  media_ready: number;
  failed_media: number;
}

export interface OverviewDraftFeature {
  id: string;
  key: string;
  name: string;
  type: FeatureType;
  revision: number;
  updated_at: string;
}

export interface OverviewSubmission {
  id: string;
  feature_key: string;
  status: SubmissionStatus;
  created_at: string;
  data_preview: string;
}

export interface OverviewFailedMedia {
  id: string;
  original_filename: string;
  created_at: string;
}

export interface OverviewActivity {
  kind: "feature" | "entry" | "submission" | "media";
  title: string;
  feature_key: string | null;
  occurred_at: string;
}

export interface Overview {
  counts: OverviewCounts;
  attention: {
    draft_features: OverviewDraftFeature[];
    new_submissions: OverviewSubmission[];
    failed_media: OverviewFailedMedia[];
  };
  recent_activity: OverviewActivity[];
}

/** A content record that references a media object. */
export interface MediaUsage {
  kind: "entry" | "submission";
  id: string;
  feature_id: string;
  feature_key: string;
  version: number;
  status: string;
  occurred_at: string;
}

export interface SettingsLimits {
  max_json_bytes: number;
  max_upload_bytes: number;
  form_rate_limit: number;
  form_rate_window_seconds: number;
  admin_page_size: number;
  public_page_size: number;
  submission_export_max_rows: number;
}

export interface SettingsPublicAPI {
	runtime_path: string;
	collection_path: string;
  forms_path: string;
  admin_path: string;
  media_path: string;
  cors_enabled: boolean;
  cors_origins: string[];
}

export interface SettingsMedia {
  accepted_types: string[];
  max_upload_bytes: number;
  image_variants: string[];
}

export interface SettingsRolePermission {
  resource: string;
  actions: string[];
}

export interface SettingsRole {
  id: Role;
  label: string;
  summary: string;
  permissions: SettingsRolePermission[];
  notes: string[];
}

export interface Settings {
  limits: SettingsLimits;
  public_api: SettingsPublicAPI;
  media: SettingsMedia;
  roles: SettingsRole[];
}


export interface FeatureManifest {
  formatVersion: number;
  key: string;
  name: string;
  version: string;
  engineApi: string;
  entry: string;
  styles?: string[];
  sharedStyles?: string[];
  slots: string[];
  arguments: JsonObject;
  content: { mode: FeatureType; schema: JsonObject };
}

export interface FeatureBuild {
  id: string;
  featureId: string;
  key: string;
  name: string;
  version: string;
  schemaVersionId: string;
  digest: string;
  artifactSize: number;
  manifest: FeatureManifest;
  createdAt: string;
}

export interface FeatureArgumentBinding {
  source: "literal" | "path" | "query";
  value?: JsonValue;
  pathArg?: string;
  queryKey?: string;
}

export interface SitePlacement {
  id: string;
  build: { featureKey: string; version: string };
  slot: string;
  arguments?: Record<string, FeatureArgumentBinding>;
  collectionSource?: { featureKey: string; version: string; field: string; argument: string };
  detailLink?: { routeId: string; field: string; pathTemplate: string };
}

export interface SitePage {
  id: string;
  name: string;
  kind: "page" | "collection-list" | "detail";
  placements: SitePlacement[];
}

export interface RouteTreeNode {
  id: string;
  type: "site" | "path" | "index" | "static" | "param" | "regex" | "catchAll";
  path?: string;
  value?: string;
  name?: string;
  pattern?: string;
  pageId?: string;
  children?: RouteTreeNode[];
}

export interface SiteConfig {
  formatVersion: 2 | 3;
  pages: SitePage[];
  routeTree: RouteTreeNode;
}

export interface SiteDraftState {
  revision: number;
  config: SiteConfig;
  updatedAt: string;
}

export interface SiteSnapshot {
  id: string;
  parentSnapshotId?: string;
  sequence: number;
  name: string;
  digest: string;
  createdAt: string;
}

export interface SnapshotContentDraftItem {
  entryId: string;
  versionId?: string;
  version?: number;
  sortOrder: number;
  singleSlot?: boolean;
  data: JsonObject;
}

export interface SnapshotContentDraftInput {
  entryId: string;
  sortOrder: number;
  singleSlot?: boolean;
  data: JsonObject;
}

export interface SnapshotContentWorkspace {
  snapshot: SiteSnapshot;
  feature: ContentFeature;
  revision: number;
  items: SnapshotContentDraftItem[];
}

export interface SiteSnapshotList {
  items: SiteSnapshot[];
  active: Record<Environment, string>;
  activeSnapshots?: Record<Environment, SiteSnapshot | null>;
  events?: SnapshotEvent[];
  nextCursor?: number;
}

export interface SnapshotEvent {
  id: string;
  snapshotId: string;
  sequence: number;
  name: string;
  environment: Environment;
  action: string;
  createdAt: string;
}

export interface SiteMetadataOverride {
  siteName?: string | null;
  faviconMediaId?: string | null;
  description?: string | null;
  ogTitle?: string | null;
  ogDescription?: string | null;
  ogImageMediaId?: string | null;
}

export interface SiteMetadata {
  formatVersion: 1;
  siteName: string;
  faviconMediaId?: string;
  description?: string;
  ogTitle?: string;
  ogDescription?: string;
  ogImageMediaId?: string;
  pages?: Record<string, SiteMetadataOverride>;
}

export interface SiteMetadataPage {
  id: string;
  name: string;
}

export interface SiteMetadataState {
  revision: number;
  devRevision: number;
  prodRevision: number;
  pages: SiteMetadataPage[];
  draft: SiteMetadata;
  dev: SiteMetadata;
  prod: SiteMetadata;
  updatedAt: string;
}

export interface EnvironmentPatch {
  revision: number;
  name?: string;
  public_base_url?: string;
  cors_origins?: string[];
}

export interface SiteEnvironment {
  key: Environment;
  name: string;
  public_base_url: string;
  http_port: number;
  cors_origins: string[];
  active_snapshot_id?: string | null;
  revision: number;
  updated_at: string;
}
