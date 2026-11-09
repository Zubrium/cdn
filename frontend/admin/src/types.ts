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
export type FeatureType = "feature" | "form";
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

export interface MediaRenditionContext {
  snapshotId: string;
  featureKey: string;
  buildVersion: string;
  schemaKind: "arguments" | "dataInputs";
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
  branchId: string;
  id: string;
  feature_id: string;
  feature_key: string;
  schema_version_id: string;
  environment: string;
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

export interface FileLimits { maxBytes: number; partBytes: number; linkMaxTTLSeconds: number }
export interface FileReference { kind: string; id: string; description: string }
export type FileVisibility = "public" | "private";
export interface FileUploadPart { number: number; size: number; sha256: string; ready: boolean }
export interface FileUploadSession { id: string; fileId: string; name: string; size: number; sha256: string; visibility: FileVisibility; partSize: number; state: "uploading" | "complete" | "cancelled"; expiresAt: string; parts: FileUploadPart[] }
export interface Media {
  ownerId?: string;
  visibility?: FileVisibility;
  sha256?: string;
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
  branchId?: string;
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
  draft_model_snapshots: number;
  draft_model_records: number;
  new_submissions: number;
  media_ready: number;
  failed_media: number;
}

export interface OverviewDraftModelSnapshot {
  snapshot_id: string;
  name: string;
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
  kind: "feature" | "content" | "submission" | "media";
  title: string;
  feature_key: string | null;
  occurred_at: string;
}

export interface Overview {
  counts: OverviewCounts;
  attention: {
    draft_model_snapshots: OverviewDraftModelSnapshot[];
    new_submissions: OverviewSubmission[];
    failed_media: OverviewFailedMedia[];
  };
  recent_activity: OverviewActivity[];
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
  formatVersion: 2;
  key: string;
  name: string;
  version: string;
  engineApi: string;
  entry?: string;
  styles?: string[];
  sharedStyles?: string[];
  runtime?: {
    abi: "static-module-v1" | "static-html-v1";
    entry: string;
    styles?: string[];
  };
  slots: string[];
  arguments: JsonObject;
  dataInputs: DataInputPort[];
  requests?: FeatureRequestSlot[];
}

export type DataModelFieldType = "text" | "longText" | "integer" | "number" | "boolean" | "date" | "dateTime" | "url" | "email" | "enum" | "media";

export interface DataInputField {
  path: string;
  type: DataModelFieldType;
  values?: string[];
  profile?: Record<string, JsonValue>;
}

export interface DataInputPort {
  key: string;
  kind: "scalar" | "record" | "collection";
  type?: DataModelFieldType;
  values?: string[];
  profile?: Record<string, JsonValue>;
  fields?: DataInputField[];
}

export interface DataInputFieldBinding {
  inputPath: string;
  fieldId: string;
  relationIds?: string[];
}

export interface DataInputLookupBinding {
  argument: string;
  fieldId: string;
}

export interface DetailSEOFieldBindings {
  titleFieldId?: string;
  descriptionFieldId?: string;
  ogTitleFieldId?: string;
  ogDescriptionFieldId?: string;
  ogImageFieldId?: string;
}

export interface DataInputBinding {
  inputKey: string;
  entityId: string;
  fieldId?: string;
  relationIds?: string[];
  fields?: DataInputFieldBinding[];
  lookup?: DataInputLookupBinding;
  detailSEO?: DetailSEOFieldBindings;
}

export interface DataModelField {
  id: string;
  key: string;
  name: string;
  type: DataModelFieldType;
  unique?: boolean;
  options?: string[];
}

export interface DataModelEntity {
  id: string;
  key: string;
  name: string;
  cardinality?: "many" | "singleton";
  fields: DataModelField[];
}

export interface DataModelRelation {
  id: string;
  name: string;
  fromEntityId: string;
  toEntityId: string;
  cardinality: "one-to-one" | "one-to-many" | "many-to-many";
}

export interface DataModel {
  formatVersion: 1;
  entities: DataModelEntity[];
  relations?: DataModelRelation[];
  positions?: Record<string, { x: number; y: number }>;
}

export type BackendRequestValueType = "string" | "integer" | "number" | "boolean" | "array";
export type BackendRequestSourceKind = "route" | "query" | "argument" | "constant";
export type BackendRequestDestinationKind = "path" | "query" | "body" | "header";

export interface FeatureRequestDestination {
  kind: BackendRequestDestinationKind;
  key: string;
  label: string;
  type: BackendRequestValueType;
  itemsType?: "string" | "integer" | "number" | "boolean";
  sources: BackendRequestSourceKind[];
  required?: boolean;
}

export interface FeatureRequestSlot {
  key: string;
  label: string;
  argument: string;
  pathLabel?: string;
  defaultPath: string;
  destinations: FeatureRequestDestination[];
}

export interface PublicBackend {
  id: string;
  name: string;
  baseUrl: string;
}

export type PublicConstantType = "string" | "integer" | "number" | "boolean" | "string-array";
export interface PublicSnapshotConstant {
  key: string;
  label: string;
  type: PublicConstantType;
  value: JsonValue;
}

export interface BackendValueBinding {
  destination: { kind: BackendRequestDestinationKind; key: string };
  source: { kind: BackendRequestSourceKind; key: string };
}

export interface PlacementBackendRequest {
  backendId?: string;
  path: string;
  bindings?: BackendValueBinding[];
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
  detailLink?: { routeId: string; field: string; pathTemplate: string };
  dataBindings?: DataInputBinding[];
  backendRequests?: Record<string, PlacementBackendRequest>;
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
 features?: {featureKey: string; version: string}[];
 systemPages?: Record<string,string>;
 backends?: PublicBackend[];
 publicConstants?: PublicSnapshotConstant[];
  dataModel?: DataModel;
  formatVersion: 2 | 3;
  pages: SitePage[];
  routeTree: RouteTreeNode;
}

export interface SiteSnapshot {
  contentRevision?: number;
  id: string;
  parentSnapshotId?: string;
  parentSequence?: number;
  parentName?: string;
  sequence: number;
  name: string;
  digest: string;
  createdAt: string;
  warnings?: string[];
  createdByName?: string;
  creationSummary?: SnapshotCreationSummary;
}

export interface SnapshotCreationSummary {
  origin: "root" | "child" | "content_fork" | "sitepack_import" | string;
  changes?: Array<"pages_routes" | "features" | "site_metadata" | "design_system" | string>;
  featureChanges?: Array<{
    featureKey: string;
    action: "added" | "removed" | "updated" | string;
    previousVersion?: string;
    version?: string;
  }>;
  inheritedModelContent?: Array<{
    entityKey: string;
    publishedRecords: number;
    draftRecords: number;
  }>;
}

export type ModelContentInputKind = "scalar" | "record" | "collection";

export interface ModelContentInputLookup {
  argument: string;
  fieldId: string;
}

export interface ModelContentFieldProjection {
  path: string;
  fieldId: string;
  type: DataModelFieldType;
  preparations?: Array<Omit<MediaRenditionContext, "snapshotId"> & { accept: string }>;
}

export interface ModelContentPlacementInput {
  inputKey: string;
  kind: ModelContentInputKind;
  entityId: string;
  fields: ModelContentFieldProjection[];
  lookup?: ModelContentInputLookup;
}

export interface ModelContentPlacement {
  id: string;
  pageId: string;
  pageName: string;
  featureKey: string;
  featureName: string;
  version: string;
  inputs: ModelContentPlacementInput[];
}

export interface ModelContentRecord {
  id: string;
  entityId: string;
  sortOrder: number;
  values: JsonObject;
  relations?: Record<string, string[]>;
}

export interface ModelContentRecordInput {
  id?: string;
  entityId: string;
  sortOrder: number;
  values: JsonObject;
  relations?: Record<string, string[]>;
}

export interface ModelContentSelection {
  placementId: string;
  inputKey: string;
  mode: "all" | "ids";
  recordIds: string[];
}

export interface ModelContentWorkspace {
  snapshot: SiteSnapshot;
  dataModel: DataModel | null;
  revision: number;
  publishedRevision: number;
  records: ModelContentRecord[];
  selections: ModelContentSelection[];
  placements: ModelContentPlacement[];
}

export interface ModelContentHistoryItem {
  id: string;
  draftRevision: number;
  contentRevision: number;
  createdAt: string;
  recordCount: number;
}

export interface ModelContentHistoryResponse {
  items: ModelContentHistoryItem[];
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
 noIndex?: boolean;
  siteName?: string | null;
  faviconMediaId?: string | null;
  description?: string | null;
  ogTitle?: string | null;
  ogDescription?: string | null;
  ogImageMediaId?: string | null;
}

export interface SiteMetadata {
 canonicalDomain?: string;
 language?: string;
 noIndex?: boolean;
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


export interface BranchRecord {
 id: string;
 name: string;
 httpAddr: string;
 corsOrigins: string[];
 indexable: boolean;
 snapshotId: string | null;
 revision: number;
 enabled: boolean;
 updatedAt: string;
}

export interface SnapshotDetail {
 snapshot: SiteSnapshot;
 payload: {config: SiteConfig; metadata?: SiteMetadata; designSystem?: DesignSystem};
}
