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

export interface DesignFont {
  family: string;
  mediaId: string;
  weight: string;
  style: "normal" | "italic";
  unicodeRange?: string;
}

export type GroupLevel = 0 | 1 | 2;
export type MediaStatus = "processing" | "ready" | "failed" | "deleted";

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

export type CoordinatorJobState = "queued" | "running" | "succeeded" | "failed" | "cancelled";
export type FeatureImportState = CoordinatorJobState;

export interface WebsiteFeatureImport {
  id: string;
  source_file_id: string;
  feature_id?: string;
  version?: string;
  file_set_id?: string;
  state: FeatureImportState;
  error_code?: string;
  source_deleted: boolean;
  created_at: string;
  updated_at: string;
}

export interface WebsiteFeatureVersion {
  feature_id: string;
  version: string;
  /** JSON manifest encoded as base64 by Go's []byte JSON representation. */
  manifest: string;
  file_set_id: string;
  files: Record<string, string>;
  digest: string;
  artifact_size: number;
  created_at: string;
  state: string;
}

export interface WebsiteFeatureVersionPage {
  items: WebsiteFeatureVersion[];
  next_cursor?: string | null;
}

export type WebsiteSnapshotState = "provisioning" | "ready" | "failed";

export interface WebsiteSnapshot {
  id: string;
  site_id: string;
  page_id: string;
  parent_id?: string;
  parent_name?: string;
  parent_sequence?: number;
  name: string;
  sequence: number;
  content_revision?: number;
  digest?: string;
  created_at?: string;
  created_by_name?: string;
  warnings?: string[];
  creation_summary?: SnapshotCreationSummary;
  revision: number;
  feature_pins: Record<string, string>;
  state: WebsiteSnapshotState;
  payload?: WebsiteSnapshotPayload;
}

export interface WebsiteSnapshotPayload {
  config: SiteConfig;
  metadata: SiteMetadata;
}

export interface WebsiteSnapshotPage {
  items: WebsiteSnapshot[];
  next_cursor: string | null;
}

export interface WebsitePage {
  id: string;
  site_id: string;
  name: string;
  latest_snapshot_id: string;
  latest_snapshot_state: WebsiteSnapshotState;
  created_at: string;
}

export interface WebsitePagePage {
  items: WebsitePage[];
  next_cursor?: string | null;
}

export interface WebsitePageTemplateConfiguration {
  schema_version: 1;
  payload: WebsiteSnapshotPayload;
  feature_pins: Record<string, string>;
  placement_slots?: Array<{ id: string; label: string }>;
}

export interface WebsitePageTemplate {
  id: string;
  site_id: string;
  name: string;
  description: string;
  revision: number;
  digest: string;
  configuration: WebsitePageTemplateConfiguration;
  updated_at: string;
}

export interface WebsitePageTemplatePage {
  items: WebsitePageTemplate[];
  next_cursor?: string | null;
}

export interface WebsiteBuild {
  id: string;
  snapshot_id: string;
  state: string;
  artifact_manifest_file_id?: string;
  error_code?: string;
  created_at: string;
  updated_at: string;
}

export interface WebsiteMediaJob {
  id: string;
  source_file_id: string;
  derivative_file_id?: string;
  profile: string;
  state: string;
  error_code?: string;
  created_at: string;
  updated_at: string;
}

export type FormFieldDefinition =
  | { type: "text" | "email"; required: boolean; min_length?: number; max_length?: number; pattern?: string }
  | { type: "number"; required: boolean; minimum?: number; maximum?: number }
  | { type: "boolean"; required: boolean }
  | { type: "select"; required: boolean; options: string[] }
  | { type: "file"; required: boolean; max_files: number; max_file_bytes: number; allowed_content_types: string[] };

export interface FormSchema {
  id: string;
  key: string;
  version: number;
  fields: Record<string, FormFieldDefinition>;
  revision: number;
}

export interface FormSubmission {
  id: string;
  form_id: string;
  schema_version: number;
  state: string;
  values: Record<string, unknown>;
  created_at: string;
  revision: number;
}

export interface RouterCoreFileRouteTarget {
  kind: "core-file";
  file_id: string;
}

export interface RouterStaticSiteArtifactRouteTarget {
  kind: "static-site-artifact";
  file_id: string;
}

export type RouterRouteTargetFile = RouterCoreFileRouteTarget | RouterStaticSiteArtifactRouteTarget;

export interface RouterRouteTargetOperation {
  kind: "registered-operation";
  operation_id: string;
  operation_config: Record<string, unknown>;
}

export type RouterRouteTarget = RouterRouteTargetFile | RouterRouteTargetOperation;
export type RouterRouteMatch = "exact" | "prefix";

export interface RouterRoute {
  id: string;
  branch_id: string;
  host: string;
  path: string;
  match: RouterRouteMatch;
  target: RouterRouteTarget;
  state: string;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface RouterRoutePage {
  items: RouterRoute[];
  next_cursor?: string;
}

export interface RouterPublicOperation {
  id: string;
  contract_version: number;
  methods: string[];
  max_request_bytes: number;
  allowed_content_types: string[];
}

export interface RouterPublicOperationPage {
  items: RouterPublicOperation[];
}

export interface CoordinatorJob {
  id: string;
  type: string;
  state: CoordinatorJobState;
  attempts: number;
  max_attempts: number;
  available_at: string;
  lease_until?: string;
  error_code?: string;
  created_at: string;
  updated_at: string;
}

export interface CoordinatorJobPage {
  items: CoordinatorJob[];
  next_cursor: string | null;
}

export interface CoordinatorJobPermissions {
  can_retry: boolean;
}

export interface Principal {
  id: string;
  name: string;
  active: boolean;
  can_manage_administrators: boolean;
  group_access: Record<string, GroupLevel>;
}

export interface Credential {
  id: string;
  principal_id: string;
  created_at: string;
  expires_at: string | null;
  revoked_at: string | null;
}

export interface CreatedPrincipal {
  id: string;
  name: string;
  credential: string;
}

export interface IssuedCredential {
  id: string;
  principal_id: string;
  credential: string;
  expires_at?: string | null;
}

export interface AccessGroup {
  key: string;
  active: boolean;
  level: GroupLevel;
}

export interface MediaRenditionContext {
  snapshotId: string;
  featureKey: string;
  buildVersion: string;
  schemaKind: "arguments" | "dataInputs";
  fieldPath: string;
  buildDigest: string;
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

export interface FileLimits { maxBytes: number; partBytes: number; maxUploadParts: number }
export interface FileUploadPart { number: number; size: number; sha256: string }
export interface FileUploadSession { id: string; fileId?: string; partSize: number; state: "uploading" | "committing" | "committed" | "aborted"; expiresAt: string; parts: FileUploadPart[] }
export interface FileSet {
  id: string;
  namespace: string;
  name: string;
  managementGroup: string;
  revision: number;
  createdAt: string;
  updatedAt: string;
}
export interface Media {
  revision: number;
  sha256?: string;
  groups?: string[];
  id: string;
  object_key?: string;
  original_filename: string;
  content_type: string;
  size_bytes: number;
  status: MediaStatus;
  variants: Record<string, string>;
  uploaded_by?: string;
  created_at: string;
  updated_at: string;
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

export interface FeatureManifest {
  formatVersion: 2;
  key: string;
  name: string;
  version: string;
  engineApi: string;
  runtime?: JsonObject;
  slots: string[];
  arguments: JsonObject;
  dataInputs: DataInputPort[];
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

export interface DataModel {
  formatVersion: 1;
  entities: DataModelEntity[];
  positions?: Record<string, { x: number; y: number }>;
}

export interface SiteContentSchema {
  version: 1;
  siteId: string;
  modelRevision: number;
  digest: string;
  dataModel: DataModel;
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
  fileId?: string;
  fileDisposition?: "inline" | "attachment";
  children?: RouteTreeNode[];
}

/** Wire contract for one immutable Website page snapshot. */
export interface SiteConfig {
  format_version: 2 | 3;
  pages: [SitePage];
}

/** Local editor draft; Router topology and Content data models are not serialized here. */
export interface SiteEditorDraft {
  features?: { featureKey: string; version: string }[];
  systemPages?: Record<string, string>;
  formatVersion: 2 | 3;
  pages: SitePage[];
  routeTree: RouteTreeNode;
}

export interface SiteSnapshot {
  state: WebsiteSnapshotState;
  contentRevision?: number;
  id: string;
  siteId: string;
  pageId: string;
  revision?: number;
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
  origin: "root" | "child" | "content_fork" | string;
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

export interface ModelContentPlacementField {
  inputPath: string;
  fieldId: string;
  type: DataModelFieldType;
  preparations?: ModelContentFieldProjection["preparations"];
}

export interface ModelContentPlacementInput {
  inputKey: string;
  kind: ModelContentInputKind;
  entityId: string;
  fields: ModelContentPlacementField[];
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
}

export interface ModelContentRecordInput {
  id?: string;
  clientKey?: string;
  entityId: string;
  sortOrder: number;
  values: JsonObject;
}

export interface ModelContentSelection {
  placementId: string;
  inputKey: string;
  mode: "all" | "ids";
  recordIds: string[];
}

export interface ModelContentWorkspace {
  snapshot: Pick<WebsiteSnapshot, "id" | "site_id" | "page_id">;
  siteId: string;
  pageSnapshotId: string;
  pageId: string;
  modelRevision: number;
  modelDigest: string;
  pageRevision: number;
  pageDigest: string;
  dataModel: DataModel | null;
  revision: number;
  publishedRevision: number;
  records: ModelContentRecord[];
  selections: ModelContentSelection[];
  placements: ModelContentPlacement[];
  clientRecordIDs?: Record<string, string>;
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
  nextCursor?: string;
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
