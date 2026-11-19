import type {
  ApiErrorDetail,
  ApiErrorPayload,
  AccessGroup,
  CoordinatorJob,
  CoordinatorJobPermissions,
  CreatedPrincipal,
  Credential,
  GroupLevel,
  IssuedCredential,
  FeatureBuild,
  FeatureManifest,
  FormSchema,
  FormFieldDefinition,
  FormSubmission,
  ListResponse,
  Media,
  FileLimits, FileUploadSession,
  FileSet,
  MediaListParams,
  MediaRenditionContext,
  MediaRenditionState,
  MediaStatus,
  Principal,
  RouterRoute,
  RouterRouteMatch,
  RouterRoutePage,
  RouterRouteTarget,
  RouterPublicOperation,
  RouterPublicOperationPage,
  SiteConfig,
  SiteEditorDraft,
  SiteMetadata,
  SiteSnapshot,
  ModelContentHistoryResponse,
  ModelContentRecord,
  ModelContentRecordInput,
  ModelContentSelection,
  ModelContentWorkspace,
  ModelContentHistoryItem,
  ModelContentPlacement,
  DataModelFieldType,
  DataModel,
  WebsiteFeatureImport,
  WebsiteFeatureVersion,
  WebsiteFeatureVersionPage,
  WebsiteBuild,
  WebsiteMediaJob,
  WebsiteSnapshot,
  WebsiteSnapshotPage,
  WebsiteSnapshotPayload,
  WebsitePage,
  WebsitePagePage,
  WebsitePageTemplate,
  WebsitePageTemplatePage,
  WebsitePageTemplateConfiguration,
  SiteContentSchema,
} from "./types";

import { hasControlCharacters, isActiveFormContentType } from "@/domains/forms/schema-draft";

export const UNAUTHORIZED_EVENT = "we:unauthorized";
const syntheticDataModeListeners = new Set<() => void>();
let syntheticDataMode = false;

export function getSyntheticDataMode(): boolean {
  return syntheticDataMode;
}

export function subscribeSyntheticDataMode(listener: () => void): () => void {
  syntheticDataModeListeners.add(listener);
  return () => syntheticDataModeListeners.delete(listener);
}

function observeResponseDataMode(response: Response): void {
  if (syntheticDataMode || response.headers.get("X-Zubrium-Data-Mode") !== "synthetic") return;
  syntheticDataMode = true;
  for (const listener of syntheticDataModeListeners) listener();
}

const ADMIN_ROOT = "/api/admin";

type QueryValue = string | number | boolean | null | undefined;

interface RequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  query?: Record<string, QueryValue>;
  signal?: AbortSignal;
  /** Login/me checks handle their own 401 state. */
  suppressUnauthorizedEvent?: boolean;
}

function arrayOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function containsCodePoint(value: string, predicate: (codePoint: number) => boolean): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0);
    if (codePoint !== undefined && predicate(codePoint)) return true;
  }
  return false;
}

function hasAsciiControlOrSpace(value: string): boolean {
  return containsCodePoint(value, (codePoint) => codePoint <= 0x20 || codePoint === 0x7f);
}

function hasWhitespaceOrAsciiControl(value: string): boolean {
  return /\s/u.test(value) || containsCodePoint(value, (codePoint) => codePoint <= 0x1f || codePoint === 0x7f);
}

function hasAsciiControl(value: string): boolean {
  return containsCodePoint(value, (codePoint) => codePoint <= 0x1f || codePoint === 0x7f);
}

function invalidFeatureCatalog(): ApiError {
  return new ApiError(502, "invalid_response", "Каталог фич содержит некорректные данные");
}

const uuidValuePattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function invalidRouterRoute(): ApiError {
  return new ApiError(502, "invalid_response", "Сервер вернул некорректный публичный маршрут");
}

function invalidRouterOperation(): ApiError {
  return new ApiError(502, "invalid_response", "Сервер вернул некорректную публичную операцию");
}

function validateRouterPublicOperation(value: unknown): RouterPublicOperation {
  const methods = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"];
  if (!isRecord(value) || typeof value.id !== "string" || !/^public\.[A-Za-z0-9._-]{1,121}$/.test(value.id) ||
    !Number.isSafeInteger(value.contract_version) || Number(value.contract_version) < 1 ||
    !Array.isArray(value.methods) || value.methods.length < 1 || value.methods.length > 6 ||
    !value.methods.every((method) => typeof method === "string" && methods.includes(method)) || new Set(value.methods).size !== value.methods.length ||
    !Number.isSafeInteger(value.max_request_bytes) || Number(value.max_request_bytes) < 1 ||
    !Array.isArray(value.allowed_content_types) || value.allowed_content_types.length < 1 || value.allowed_content_types.length > 32 ||
    !value.allowed_content_types.every((type) => typeof type === "string" && /^[A-Za-z0-9!#$&^_.+-]+\/[A-Za-z0-9!#$&^_.+-]+$/.test(type)) ||
    new Set(value.allowed_content_types.map((type) => typeof type === "string" ? type.toLowerCase() : "")).size !== value.allowed_content_types.length) {
    throw invalidRouterOperation();
  }
  if (value.id === "public.form-upload" && value.allowed_content_types.some((type) => typeof type === "string" && isActiveFormContentType(type))) {
    throw invalidRouterOperation();
  }
  return value as unknown as RouterPublicOperation;
}

function invalidFileSet(): ApiError {
  return new ApiError(502, "invalid_response", "Сервер вернул некорректный набор файлов");
}

function invalidFormSchema(): ApiError {
  return new ApiError(502, "invalid_response", "Сервер вернул некорректную схему формы");
}

function validFormFieldDefinition(value: unknown): value is FormFieldDefinition {
  if (!isRecord(value) || typeof value.required !== "boolean" || typeof value.type !== "string") return false;
  if (value.type === "text" || value.type === "email") {
    return hasOnlyKeys(value, ["type", "required", "min_length", "max_length", "pattern"]) &&
      (value.min_length === undefined || (Number.isSafeInteger(value.min_length) && Number(value.min_length) >= 0 && Number(value.min_length) <= 1 << 20)) &&
      (value.max_length === undefined || (Number.isSafeInteger(value.max_length) && Number(value.max_length) >= 1 && Number(value.max_length) <= 1 << 20)) &&
      !(value.min_length !== undefined && value.max_length !== undefined && Number(value.min_length) > Number(value.max_length)) &&
      (value.pattern === undefined || (typeof value.pattern === "string" && value.pattern.length <= 256));
  }
  if (value.type === "number") {
    return hasOnlyKeys(value, ["type", "required", "minimum", "maximum"]) &&
      (value.minimum === undefined || (typeof value.minimum === "number" && Number.isFinite(value.minimum))) &&
      (value.maximum === undefined || (typeof value.maximum === "number" && Number.isFinite(value.maximum))) &&
      !(value.minimum !== undefined && value.maximum !== undefined && Number(value.minimum) > Number(value.maximum));
  }
  if (value.type === "boolean") return hasOnlyKeys(value, ["type", "required"]);
  if (value.type === "select") {
    return hasOnlyKeys(value, ["type", "required", "options"]) && Array.isArray(value.options) &&
      value.options.length >= 1 && value.options.length <= 128 && value.options.every((option) =>
        typeof option === "string" && option.length > 0 && option.length <= 256 && option.trim() === option && !hasControlCharacters(option)) &&
      new Set(value.options).size === value.options.length;
  }
  if (value.type === "file") {
    if (!hasOnlyKeys(value, ["type", "required", "max_files", "max_file_bytes", "allowed_content_types"]) ||
      !Number.isSafeInteger(value.max_files) || Number(value.max_files) < 1 || Number(value.max_files) > 20 ||
      !Number.isSafeInteger(value.max_file_bytes) || Number(value.max_file_bytes) < 1 ||
      !Array.isArray(value.allowed_content_types) || value.allowed_content_types.length < 1 || value.allowed_content_types.length > 32) return false;
    const types = value.allowed_content_types;
    return types.every((type) => typeof type === "string" && /^[A-Za-z0-9!#$&^_.+-]+\/[A-Za-z0-9!#$&^_.+-]+$/.test(type) &&
      !isActiveFormContentType(type)) &&
      new Set(types.map((type) => typeof type === "string" ? type.toLowerCase() : "")).size === types.length;
  }
  return false;
}

function parseFormSchema(value: unknown): FormSchema {
  if (!isRecord(value) || !hasOnlyKeys(value, ["id", "key", "version", "fields", "revision"]) ||
    typeof value.id !== "string" || !uuidValuePattern.test(value.id) || value.id !== value.id.toLowerCase() ||
    typeof value.key !== "string" || value.key.length < 1 || value.key.length > 128 || value.key.trim() !== value.key || hasControlCharacters(value.key) ||
    !Number.isSafeInteger(value.version) || Number(value.version) < 1 || !Number.isSafeInteger(value.revision) || Number(value.revision) < 1 ||
    !isRecord(value.fields) || Object.keys(value.fields).length < 1 || Object.keys(value.fields).length > 128) {
    throw invalidFormSchema();
  }
  for (const [name, field] of Object.entries(value.fields)) {
    if (!/^[a-z][a-z0-9_]{0,63}$/.test(name) || !validFormFieldDefinition(field)) throw invalidFormSchema();
  }
  return value as unknown as FormSchema;
}

function parseFormSchemaPage(value: unknown): ListResponse<FormSchema> {
  if (!isRecord(value) || !Array.isArray(value.items) ||
    (value.next_cursor !== undefined && value.next_cursor !== null && typeof value.next_cursor !== "string")) {
    throw invalidFormSchema();
  }
  return { items: value.items.map(parseFormSchema), next_cursor: typeof value.next_cursor === "string" ? value.next_cursor : null };
}

function validateFileSet(value: unknown): FileSet {
  if (!isRecord(value) || typeof value.id !== "string" || !uuidValuePattern.test(value.id) ||
    typeof value.namespace !== "string" || !/^[a-z][a-z0-9_-]*(?:\/[a-z][a-z0-9_-]*)*$/.test(value.namespace) || value.namespace.length > 64 ||
    typeof value.name !== "string" || value.name.trim() !== value.name || value.name.length < 1 || value.name.length > 255 ||
    hasControlCharacters(value.name) ||
    typeof value.managementGroup !== "string" || !/^[a-z][a-z0-9_-]{0,63}$/.test(value.managementGroup) ||
    !Number.isSafeInteger(value.revision) || Number(value.revision) < 1 ||
    typeof value.createdAt !== "string" || !value.createdAt.includes("T") || !Number.isFinite(Date.parse(value.createdAt)) ||
    typeof value.updatedAt !== "string" || !value.updatedAt.includes("T") || !Number.isFinite(Date.parse(value.updatedAt))) {
    throw invalidFileSet();
  }
  return value as unknown as FileSet;
}

function validPublicFormBindingConfig(config: unknown): config is Record<string, unknown> {
  if (!isRecord(config) || !hasOnlyKeys(config, ["enabled", "snapshot_id", "form_id", "schema_version"])) return false;
  return config.enabled === true && typeof config.snapshot_id === "string" && uuidValuePattern.test(config.snapshot_id) &&
    config.snapshot_id === config.snapshot_id.toLowerCase() &&
    typeof config.form_id === "string" && uuidValuePattern.test(config.form_id) &&
    config.form_id === config.form_id.toLowerCase() &&
    Number.isSafeInteger(config.schema_version) && Number(config.schema_version) >= 1;
}

function validPublicUploadRouteConfig(config: Record<string, unknown>): boolean {
  const allowedKeys = new Set(["enabled", "access_group", "file_set_id", "max_bytes", "allowed_content_types", "form"]);
  if (Object.keys(config).some((key) => !allowedKeys.has(key)) || config.enabled !== true ||
    typeof config.access_group !== "string" || !/^[a-z][a-z0-9_-]{0,63}$/.test(config.access_group) ||
    (config.file_set_id !== undefined && (typeof config.file_set_id !== "string" || !uuidValuePattern.test(config.file_set_id))) ||
    !Number.isSafeInteger(config.max_bytes) || Number(config.max_bytes) < 1 ||
    !Array.isArray(config.allowed_content_types) || config.allowed_content_types.length < 1 || config.allowed_content_types.length > 32) {
    return false;
  }
  const seen = new Set<string>();
  for (const value of config.allowed_content_types) {
    if (typeof value !== "string" || !/^[A-Za-z0-9!#$&^_.+-]+\/[A-Za-z0-9!#$&^_.+-]+$/.test(value)) return false;
    const normalized = value.toLowerCase();
    if (isActiveFormContentType(normalized) || seen.has(normalized)) {
      return false;
    }
    seen.add(normalized);
  }
  return config.form === undefined || validPublicFormBindingConfig(config.form);
}

function validateRouterRoute(value: unknown): RouterRoute {
  if (!isRecord(value) || typeof value.id !== "string" || !uuidValuePattern.test(value.id) ||
    typeof value.branch_id !== "string" || value.branch_id.length === 0 || value.branch_id.length > 128 ||
    hasAsciiControlOrSpace(value.branch_id) ||
    typeof value.host !== "string" || value.host.length === 0 || value.host.length > 253 ||
    value.host.trim() !== value.host || hasWhitespaceOrAsciiControl(value.host) ||
    typeof value.path !== "string" || !value.path.startsWith("/") || value.path.length > 2048 ||
    value.path.trim() !== value.path || hasAsciiControl(value.path) || value.path.includes("?") || value.path.includes("#") ||
    (value.match !== "exact" && value.match !== "prefix") || typeof value.state !== "string" || value.state.length === 0 ||
    !Number.isSafeInteger(value.revision) || Number(value.revision) < 1 ||
    typeof value.created_at !== "string" || !value.created_at.includes("T") || !Number.isFinite(Date.parse(value.created_at)) ||
    typeof value.updated_at !== "string" || !value.updated_at.includes("T") || !Number.isFinite(Date.parse(value.updated_at)) ||
    !isRecord(value.target)) {
    throw invalidRouterRoute();
  }

  if (value.target.kind === "core-file" || value.target.kind === "static-site-artifact") {
    if (typeof value.target.file_id !== "string" || !uuidValuePattern.test(value.target.file_id) ||
      (value.target.kind === "core-file" && value.match !== "exact") ||
      (value.match === "prefix" && value.target.kind !== "static-site-artifact") ||
      !hasOnlyKeys(value.target, ["kind", "file_id"])) {
      throw invalidRouterRoute();
    }
  } else if (value.target.kind === "registered-operation") {
    if (typeof value.target.operation_id !== "string" || !/^public\.[A-Za-z0-9._-]{1,121}$/.test(value.target.operation_id) ||
      !isRecord(value.target.operation_config) || !hasOnlyKeys(value.target, ["kind", "operation_id", "operation_config"])) {
      throw invalidRouterRoute();
    }
    if (value.target.operation_id === "public.form-upload" && !validPublicUploadRouteConfig(value.target.operation_config)) {
      throw invalidRouterRoute();
    }
    if ((value.target.operation_id === "public.form-session" || value.target.operation_id === "public.form-submit") &&
      !validPublicFormBindingConfig(value.target.operation_config)) {
      throw invalidRouterRoute();
    }
  } else {
    throw invalidRouterRoute();
  }

  return value as unknown as RouterRoute;
}

function hasOnlyKeys(value: Record<string, unknown>, allowed: string[]): boolean {
  const allow = new Set(allowed);
  return Object.keys(value).every((key) => allow.has(key));
}

type SnapshotDetail = {
  snapshot: SiteSnapshot;
  payload: { config: SiteConfig; metadata?: SiteMetadata };
};

function siteSnapshotFromWebsite(snapshot: WebsiteSnapshot): SiteSnapshot {
  const valid = validateWebsiteSnapshot(snapshot);
  return {
    id: valid.id,
    siteId: valid.site_id,
    pageId: valid.page_id,
    revision: valid.revision,
    parentSnapshotId: valid.parent_id,
    parentSequence: valid.parent_sequence,
    parentName: valid.parent_name,
    state: valid.state,
    sequence: valid.sequence,
    name: valid.name,
    digest: valid.digest ?? "",
    contentRevision: valid.content_revision,
    createdAt: valid.created_at ?? "",
    warnings: valid.warnings,
    createdByName: valid.created_by_name,
    creationSummary: valid.creation_summary,
  };
}

function validateWebsiteSnapshot(value: unknown): WebsiteSnapshot {
  if (!isRecord(value) || typeof value.id !== "string" || !value.id || typeof value.site_id !== "string" || !value.site_id ||
    typeof value.page_id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value.page_id) ||
    typeof value.name !== "string" || !value.name || typeof value.sequence !== "number" || !Number.isSafeInteger(value.sequence) || value.sequence < 1 ||
    typeof value.revision !== "number" || !Number.isSafeInteger(value.revision) || value.revision < 1 ||
    (value.state !== "provisioning" && value.state !== "ready" && value.state !== "failed") ||
    !isRecord(value.feature_pins) || !Object.values(value.feature_pins).every((version) => typeof version === "string") ||
    (value.parent_id !== undefined && typeof value.parent_id !== "string") ||
    (value.parent_name !== undefined && typeof value.parent_name !== "string") ||
    (value.parent_sequence !== undefined && (typeof value.parent_sequence !== "number" || !Number.isSafeInteger(value.parent_sequence) || value.parent_sequence < 1)) ||
    (value.content_revision !== undefined && (typeof value.content_revision !== "number" || !Number.isSafeInteger(value.content_revision) || value.content_revision < 0)) ||
    (value.digest !== undefined && typeof value.digest !== "string") ||
    (value.created_at !== undefined && typeof value.created_at !== "string") ||
    (value.created_by_name !== undefined && typeof value.created_by_name !== "string") ||
    (value.warnings !== undefined && (!Array.isArray(value.warnings) || !value.warnings.every((warning) => typeof warning === "string"))) ||
    (value.payload !== undefined && !isRecord(value.payload))) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректный снапшот");
  }
  return value as unknown as WebsiteSnapshot;
}

function snapshotPayloadFromWebsite(payload: { config: unknown; metadata: unknown }, expectedPageID?: string, request = false): SnapshotDetail["payload"] {
  const invalid = (message: string): never => {
    throw new ApiError(request ? 400 : 502, request ? "invalid_snapshot_payload" : "invalid_response", message);
  };
  const config = payload.config;
  if (!isRecord(config) || !isRecord(payload.metadata) || payload.metadata.formatVersion !== 1) {
    return invalid("Конфигурация снапшота должна соответствовать page-snapshot контракту");
  }
  const formatVersion = config.format_version ?? (request ? config.formatVersion : undefined);
  if (![2, 3].includes(Number(formatVersion)) || (!request && (("dataModel" in config) || ("routeTree" in config)))) {
    return invalid("Конфигурация снапшота должна соответствовать page-snapshot контракту и не содержать dataModel или маршруты");
  }
  if (config.page !== undefined && config.pages !== undefined) return invalid("Снапшот должен содержать одну страницу");
  const pages: Record<string, unknown>[] = isRecord(config.page)
    ? [config.page]
    : Array.isArray(config.pages) && config.pages.every(isRecord) ? config.pages : [];
  const page: Record<string, unknown> | null = expectedPageID === undefined || !request
    ? pages.length === 1 ? pages[0]! : null
    : pages.find((candidate) => candidate.id === expectedPageID) ?? null;
  if (!page || typeof page.id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(page.id) ||
    (expectedPageID !== undefined && page.id !== expectedPageID) || !Array.isArray(page.placements)) {
    return invalid("Снапшот должен содержать ровно одну страницу, совпадающую с page_id");
  }
  const cleanConfig = Object.fromEntries(Object.entries(config).filter(([key]) =>
    !["formatVersion", "page", "pages", "routeTree", "dataModel", "features", "systemPages"].includes(key),
  ));
  return {
    config: { ...cleanConfig, format_version: Number(formatVersion), pages: [page] } as unknown as SiteConfig,
    metadata: payload.metadata as unknown as SiteMetadata,
  };
}


function siteSnapshotDetailFromWebsite(snapshot: WebsiteSnapshot): SnapshotDetail {
  if (!snapshot.payload) throw new ApiError(502, "invalid_response", "Сервер не вернул payload снапшота");
  return { snapshot: siteSnapshotFromWebsite(snapshot), payload: snapshotPayloadFromWebsite(snapshot.payload, snapshot.page_id) };
}

function decodeFeatureManifest(encoded: string): FeatureManifest {
  if (encoded.length === 0 || encoded.length > 1_400_000) throw invalidFeatureCatalog();
  try {
    const binary = atob(encoded);
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
    if (!isRecord(value) || value.formatVersion !== 2 ||
      typeof value.key !== "string" || !value.key ||
      typeof value.name !== "string" ||
      typeof value.version !== "string" ||
      typeof value.engineApi !== "string" ||
      !Array.isArray(value.slots) || !value.slots.every((slot) => typeof slot === "string") ||
      !isRecord(value.arguments) || !Array.isArray(value.dataInputs)) {
      throw invalidFeatureCatalog();
    }
    return value as unknown as FeatureManifest;
  } catch (error) {
    if (error instanceof ApiError) throw error;
    throw invalidFeatureCatalog();
  }
}

function featureBuildFromCatalog(item: WebsiteFeatureVersion): FeatureBuild {
  if (!item || typeof item.feature_id !== "string" || !item.feature_id ||
    typeof item.version !== "string" || !item.version ||
    typeof item.file_set_id !== "string" ||
    typeof item.digest !== "string" || !/^[a-f0-9]{64}$/i.test(item.digest) ||
    !Number.isSafeInteger(item.artifact_size) || item.artifact_size < 0 ||
    typeof item.created_at !== "string" || item.state !== "ready" || !isRecord(item.files) ||
    !Object.values(item.files).every((fileID) => typeof fileID === "string")) {
    throw invalidFeatureCatalog();
  }
  const manifest = decodeFeatureManifest(item.manifest);
  if (manifest.version !== item.version) throw invalidFeatureCatalog();
  return {
    id: `${item.feature_id}@${item.version}`,
    featureId: item.feature_id,
    key: manifest.key,
    name: manifest.name,
    version: item.version,
    schemaVersionId: item.file_set_id,
    digest: item.digest,
    artifactSize: item.artifact_size,
    manifest,
    createdAt: item.created_at,
  };
}

function parseFeatureImport(value: unknown): WebsiteFeatureImport {
  if (!isRecord(value) || typeof value.id !== "string" || !value.id ||
    typeof value.source_file_id !== "string" || !value.source_file_id ||
    !["queued", "running", "succeeded", "failed", "cancelled"].includes(String(value.state)) ||
    typeof value.source_deleted !== "boolean" ||
    typeof value.created_at !== "string" || typeof value.updated_at !== "string" ||
    (value.feature_id !== undefined && typeof value.feature_id !== "string") ||
    (value.version !== undefined && typeof value.version !== "string") ||
    (value.file_set_id !== undefined && typeof value.file_set_id !== "string") ||
    (value.error_code !== undefined && typeof value.error_code !== "string")) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректное состояние импорта фичи");
  }
  return value as unknown as WebsiteFeatureImport;
}

function parseSiteContentSchema(value: unknown, siteID: string): SiteContentSchema {
  if (!isRecord(value) || value.version !== 1 || value.site_id !== siteID ||
    !Number.isSafeInteger(value.model_revision) || Number(value.model_revision) < 1 ||
    typeof value.digest !== "string" || !/^[a-f0-9]{64}$/i.test(value.digest) ||
    !isRecord(value.data_model) || value.data_model.formatVersion !== 1 || !Array.isArray(value.data_model.entities)) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректную модель данных сайта");
  }
  return {
    version: 1,
    siteId: siteID,    modelRevision: Number(value.model_revision),
    digest: value.digest,
    dataModel: value.data_model as unknown as DataModel,
  };
}

function parseContentWorkspace(value: unknown, snapshotID: string): ModelContentWorkspace {
  if (!isRecord(value) || value.page_snapshot_id !== snapshotID || typeof value.site_id !== "string" || !value.site_id ||
    typeof value.page_id !== "string" || !value.page_id || !Number.isSafeInteger(value.model_revision) || Number(value.model_revision) < 1 ||
    typeof value.model_digest !== "string" || !Number.isSafeInteger(value.page_revision) || Number(value.page_revision) < 1 ||
    typeof value.page_digest !== "string" || !Number.isSafeInteger(value.revision) || Number(value.revision) < 0 ||
    !Number.isSafeInteger(value.published_revision) || Number(value.published_revision) < 0 ||
    !Array.isArray(value.records) || !Array.isArray(value.selections) || !Array.isArray(value.placements)) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректную рабочую область контента");
  }
  let dataModel: DataModel | null = null;
  if (value.data_model !== null) {
    if (!isRecord(value.data_model) || value.data_model.formatVersion !== 1 || !Array.isArray(value.data_model.entities)) {
      throw new ApiError(502, "invalid_response", "Сервер вернул некорректную модель данных");
    }
    dataModel = value.data_model as unknown as DataModel;
  }
  const modelFieldTypes = new Map<string, DataModelFieldType>();
  for (const entity of dataModel?.entities ?? []) {
    for (const field of entity.fields) modelFieldTypes.set(field.id, field.type);
  }
  const records = value.records.map((record): ModelContentRecord => {
    if (!isRecord(record) || typeof record.id !== "string" || typeof record.entity_id !== "string" ||
      !Number.isSafeInteger(record.sort_order) || Number(record.sort_order) < 0 || !isRecord(record.values)) {
      throw new ApiError(502, "invalid_response", "Сервер вернул некорректную запись контента");
    }
    return { id: record.id, entityId: record.entity_id, sortOrder: Number(record.sort_order), values: record.values as ModelContentRecord["values"] };
  });
  const selections = value.selections.map((selection): ModelContentSelection => {
    if (!isRecord(selection) || typeof selection.placement_id !== "string" || typeof selection.input_key !== "string" ||
      (selection.mode !== "all" && selection.mode !== "ids") ||
      (selection.record_ids !== undefined && (!Array.isArray(selection.record_ids) || selection.record_ids.some((id) => typeof id !== "string")))) {
      throw new ApiError(502, "invalid_response", "Сервер вернул некорректный выбор записей");
    }
    return { placementId: selection.placement_id, inputKey: selection.input_key, mode: selection.mode, recordIds: arrayOrEmpty(selection.record_ids as string[] | undefined) };
  });
  const placements = value.placements.map((placement): ModelContentPlacement => {
    if (!isRecord(placement) || typeof placement.id !== "string" || typeof placement.page_id !== "string" ||
      typeof placement.page_name !== "string" || typeof placement.feature_key !== "string" ||
      typeof placement.feature_name !== "string" || typeof placement.version !== "string" || !Array.isArray(placement.inputs)) {
      throw new ApiError(502, "invalid_response", "Сервер вернул некорректное описание размещения");
    }
    const inputs = placement.inputs.map((input) => {
      if (!isRecord(input) || typeof input.input_key !== "string" ||
        (input.kind !== "scalar" && input.kind !== "record" && input.kind !== "collection") ||
        typeof input.entity_id !== "string" || !Array.isArray(input.fields)) {
        throw new ApiError(502, "invalid_response", "Сервер вернул некорректный вход размещения");
      }
      const fields = input.fields.map((field) => {
        if (!isRecord(field) || typeof field.input_path !== "string" || typeof field.field_id !== "string") {
          throw new ApiError(502, "invalid_response", "Сервер вернул некорректное сопоставление поля");
        }
        const type = modelFieldTypes.get(field.field_id);
        if (!type) throw new ApiError(502, "invalid_response", "Сервер связал вход фичи с отсутствующим полем модели");
        return { inputPath: field.input_path, fieldId: field.field_id, type };
      });
      let lookup: ModelContentPlacement["inputs"][number]["lookup"];
      if (input.lookup !== undefined) {
        if (!isRecord(input.lookup) || typeof input.lookup.argument !== "string" || typeof input.lookup.field_id !== "string") {
          throw new ApiError(502, "invalid_response", "Сервер вернул некорректный поиск записи");
        }
        lookup = { argument: input.lookup.argument, fieldId: input.lookup.field_id };
      }
      return { inputKey: input.input_key, kind: input.kind as ModelContentPlacement["inputs"][number]["kind"], entityId: input.entity_id, fields, ...(lookup ? { lookup } : {}) };
    });
    return { id: placement.id, pageId: placement.page_id, pageName: placement.page_name, featureKey: placement.feature_key,
      featureName: placement.feature_name, version: placement.version, inputs };
  });
  const clientRecordIDs = isRecord(value.client_record_ids) && Object.entries(value.client_record_ids).every(([key, id]) => key.length > 0 && typeof id === "string")
    ? value.client_record_ids as Record<string, string>
    : undefined;
  if (value.client_record_ids !== undefined && clientRecordIDs === undefined) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректные идентификаторы записей");
  }
  return {
    snapshot: { id: snapshotID, site_id: value.site_id, page_id: value.page_id },
    siteId: value.site_id,
    pageSnapshotId: snapshotID,
    pageId: value.page_id,
    modelRevision: Number(value.model_revision),
    modelDigest: value.model_digest,
    pageRevision: Number(value.page_revision),
    pageDigest: value.page_digest,
    dataModel,
    revision: Number(value.revision),
    publishedRevision: Number(value.published_revision),
    records,
    selections,
    placements,
    ...(clientRecordIDs ? { clientRecordIDs } : {}),
  };
}

function parseContentHistory(value: unknown): ModelContentHistoryResponse {
  if (!isRecord(value) || !Array.isArray(value.items) ||
    (value.next_cursor !== undefined && (typeof value.next_cursor !== "string" || value.next_cursor.length > 1024))) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректную историю контента");
  }
  const items = value.items.map((item): ModelContentHistoryItem => {
    if (!isRecord(item) || typeof item.id !== "string" || !Number.isSafeInteger(item.draft_revision) ||
      !Number.isSafeInteger(item.content_revision) || typeof item.created_at !== "string" ||
      !Number.isSafeInteger(item.record_count) || Number(item.record_count) < 0) {
      throw new ApiError(502, "invalid_response", "Сервер вернул некорректную запись истории контента");
    }
    return { id: item.id, draftRevision: Number(item.draft_revision), contentRevision: Number(item.content_revision),
      createdAt: item.created_at, recordCount: Number(item.record_count) };
  });
  return { items, ...(typeof value.next_cursor === "string" && value.next_cursor ? { nextCursor: value.next_cursor } : {}) };
}

async function requestList<T>(
  path: string,
  options: RequestOptions = {},
): Promise<ListResponse<T>> {
  const response = await request<{
    items?: T[] | null;
    next_cursor?: string | null;
  } | null>(path, options);
  return {
    items: arrayOrEmpty(response?.items),
    next_cursor: response?.next_cursor ?? null,
  };
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: ApiErrorDetail[];
  readonly requestId?: string;

  constructor(
    status: number,
    code: string,
    message: string,
    details: ApiErrorDetail[] = [],
    requestId?: string,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.requestId = requestId;
  }

  get isConflict(): boolean {
    return this.status === 409;
  }

  get isForbidden(): boolean {
    return this.status === 403;
  }

  get isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function notifyUnauthorized(message: string): void {
  window.dispatchEvent(
    new CustomEvent<string>(UNAUTHORIZED_EVENT, { detail: message }),
  );
}

function buildUrl(
  path: string,
  query?: Record<string, QueryValue>,
): string {
  const url = new URL(path, window.location.origin);
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined || value === null || value === "") continue;
      url.searchParams.set(key, String(value));
    }
  }
  return `${url.pathname}${url.search}`;
}

function csrfToken(): string | null {
  if (typeof document === "undefined") return null;
  const values = document.cookie.split(";").map((part) => part.trim())
    .filter((part) => part.startsWith("zubrium_admin_csrf="));
  if (values.length !== 1) return null;
  const value = values[0]!.slice("zubrium_admin_csrf=".length);
  return /^[A-Za-z0-9_-]{43}$/.test(value) ? value : null;
}

async function parseErrorResponse(response: Response): Promise<ApiError> {
  let payload: ApiErrorPayload | null = null;
  try {
    const text = await response.text();
    if (text) {
      const parsed: unknown = JSON.parse(text);
      if (
        parsed &&
        typeof parsed === "object" &&
        "error" in parsed &&
        parsed.error
      ) {
        const error = (parsed as { error: unknown }).error;
        if (typeof error === "object") payload = error as ApiErrorPayload;
        else if (typeof error === "string") payload = { code: error, message: error };
      }
    }
  } catch {
    // A malformed or empty error body falls back to the generic message.
  }

  const fallback =
    response.status === 401
      ? "Требуется авторизация"
      : response.status === 403
        ? "Недостаточно прав"
        : response.status === 404
          ? "Запрошенный ресурс не найден"
          : response.status >= 500
            ? "Внутренняя ошибка сервера"
            : `Запрос завершился с ошибкой (${response.status})`;

  return new ApiError(
    response.status,
    payload?.code ?? "http_error",
    payload?.message ?? fallback,
    payload?.details ?? [],
    payload?.request_id,
  );
}

async function rawRequest(
  path: string,
  options: RequestOptions = {},
): Promise<Response> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...options.headers,
  };
  const method = (options.method ?? "GET").toUpperCase();
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    const csrf = csrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  let body: BodyInit | undefined;
  if (options.body !== undefined) {
    if (options.body instanceof Blob) {
      headers["Content-Type"] = options.body.type || "application/octet-stream";
      body = options.body;
    } else {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(options.body);
    }
  }

  let response: Response;
  try {
    response = await fetch(buildUrl(path, options.query), {
      method,
      headers,
      body,
      signal: options.signal,
      credentials: "same-origin",
    });
  } catch (error) {
    if (isAbortError(error)) throw error;
    throw new ApiError(
      0,
      "network_error",
      "Не удалось связаться с сервером. Проверьте подключение и повторите попытку.",
    );
  }

  observeResponseDataMode(response);

  if (response.status === 401 && !options.suppressUnauthorizedEvent) {
    notifyUnauthorized("Сессия истекла. Войдите снова.");
  }
  return response;
}

async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const response = await rawRequest(path, options);
  if (!response.ok) throw await parseErrorResponse(response);
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(
      response.status,
      "invalid_response",
      "Сервер вернул некорректный ответ",
    );
  }
}

function filenameFromDisposition(header: string | null, fallback: string): string {
  if (!header) return fallback;
  const utf8 = /filename\*=UTF-8''([^;]+)/i.exec(header);
  if (utf8?.[1]) {
    try {
      return decodeURIComponent(utf8[1]);
    } catch {
      return fallback;
    }
  }
  const plain = /filename="?([^";]+)"?/i.exec(header);
  if (plain?.[1]) return plain[1];
  return fallback;
}

const encoded = (value: string) => encodeURIComponent(value);

interface CoreFile {
  id: string;
  name: string;
  contentType: string;
  size: number;
  sha256: string;
  revision: number;
  groups: string[];
  attributes?: Record<string, string>;
}

function toMedia(file: CoreFile): Media {
  return {
    id: file.id,
    revision: file.revision,
    sha256: file.sha256,
    groups: arrayOrEmpty(file.groups),
    original_filename: file.name,
    content_type: file.contentType,
    size_bytes: file.size,
    status: "ready",
    variants: {},
    created_at: "",
    updated_at: "",
  };
}

function parseWebsitePage(value: unknown, siteID: string): WebsitePage {
  if (!isRecord(value) || typeof value.id !== "string" || !uuidValuePattern.test(value.id) ||
    value.site_id !== siteID || typeof value.name !== "string" || typeof value.latest_snapshot_id !== "string" ||
    !uuidValuePattern.test(value.latest_snapshot_id) || !["provisioning", "ready", "failed"].includes(String(value.latest_snapshot_state)) ||
    typeof value.created_at !== "string" || !Number.isFinite(Date.parse(value.created_at))) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректную страницу сайта");
  }
  return value as unknown as WebsitePage;
}

function parseWebsitePageTemplate(value: unknown, siteID: string): WebsitePageTemplate {
  if (!isRecord(value) || typeof value.id !== "string" || !uuidValuePattern.test(value.id) || value.site_id !== siteID ||
    typeof value.name !== "string" || typeof value.description !== "string" || !Number.isSafeInteger(value.revision) ||
    (value.revision as number) < 1 || typeof value.digest !== "string" || !/^[a-f0-9]{64}$/i.test(value.digest) ||
    typeof value.updated_at !== "string" || !isRecord(value.configuration) || value.configuration.schema_version !== 1 ||
    !isRecord(value.configuration.payload) || !isRecord(value.configuration.payload.config) || !isRecord(value.configuration.payload.metadata) ||
    !isRecord(value.configuration.feature_pins) || (value.configuration.placement_slots !== undefined && !Array.isArray(value.configuration.placement_slots))) {
    throw new ApiError(502, "invalid_response", "Сервер вернул некорректный шаблон страницы");
  }
  return value as unknown as WebsitePageTemplate;
}

export const api = {
  website: {
    async siteModel(siteID: string, signal?: AbortSignal): Promise<SiteContentSchema> {
      return parseSiteContentSchema(
        await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/model`, { signal }),
        siteID,
      );
    },
    async saveSiteModel(siteID: string, expectedRevision: number, dataModel: DataModel): Promise<SiteContentSchema> {
      return parseSiteContentSchema(
        await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/model`, {
          method: "PUT",
          body: { expected_revision: expectedRevision, data_model: dataModel },
        }),
        siteID,
      );
    },
    async pages(siteID: string, cursor?: string, limit?: number, signal?: AbortSignal): Promise<WebsitePagePage> {
      const page = await requestList<WebsitePage>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/pages`, { signal, query: { cursor, limit } });
      return { ...page, items: page.items.map((item) => parseWebsitePage(item, siteID)) };
    },
    async page(siteID: string, pageID: string, signal?: AbortSignal): Promise<WebsitePage> {
      return parseWebsitePage(await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/pages/${encoded(pageID)}`, { signal }), siteID);
    },
    async createPage(siteID: string, input: { name: string; template_id?: string; template_revision?: number }, idempotencyKey: string): Promise<WebsitePage> {
      return parseWebsitePage(await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/pages`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: input,
      }), siteID);
    },
    async pageTemplates(siteID: string, cursor?: string, limit?: number, signal?: AbortSignal): Promise<WebsitePageTemplatePage> {
      const page = await requestList<WebsitePageTemplate>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/templates`, { signal, query: { cursor, limit } });
      return { ...page, items: page.items.map((item) => parseWebsitePageTemplate(item, siteID)) };
    },
    async pageTemplate(siteID: string, templateID: string, signal?: AbortSignal): Promise<WebsitePageTemplate> {
      return parseWebsitePageTemplate(await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/templates/${encoded(templateID)}`, { signal }), siteID);
    },
    async createPageTemplate(siteID: string, input: { name: string; description: string; configuration: WebsitePageTemplateConfiguration }, idempotencyKey: string): Promise<WebsitePageTemplate> {
      return parseWebsitePageTemplate(await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/templates`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: input,
      }), siteID);
    },
    async updatePageTemplate(siteID: string, templateID: string, revision: number, input: { name: string; description: string; configuration: WebsitePageTemplateConfiguration }): Promise<WebsitePageTemplate> {
      return parseWebsitePageTemplate(await request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/templates/${encoded(templateID)}`, {
        method: "PUT", headers: { "If-Match": `"${revision}"` }, body: input,
      }), siteID);
    },
    deletePageTemplate(siteID: string, templateID: string, revision: number): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/templates/${encoded(templateID)}`, {
        method: "DELETE", headers: { "If-Match": `"${revision}"` },
      });
    },
    applyPageTemplate(siteID: string, pageID: string, input: { template_id: string; template_revision: number; expected_snapshot_id: string; expected_snapshot_revision: number; expected_content_revision: number; confirmed: true }, idempotencyKey: string): Promise<WebsiteSnapshot> {
      return request<unknown>(`${ADMIN_ROOT}/sites/${encoded(siteID)}/pages/${encoded(pageID)}/template-applications`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: input,
      }).then(validateWebsiteSnapshot);
    },
    snapshots(siteID?: string, pageID?: string, cursor?: string, limit?: number, signal?: AbortSignal): Promise<WebsiteSnapshotPage> {
      return requestList<WebsiteSnapshot>(`${ADMIN_ROOT}/sites/snapshots`, { signal, query: { site_id: siteID, page_id: pageID, cursor, limit } });
    },
    async allSnapshots(signal?: AbortSignal, siteID?: string, pageID?: string): Promise<WebsiteSnapshot[]> {
      const items: WebsiteSnapshot[] = [];
      const seenCursors = new Set<string>();
      let cursor: string | undefined;
      for (let pageNumber = 0; pageNumber < 200; pageNumber += 1) {
        const page = await requestList<WebsiteSnapshot>(`${ADMIN_ROOT}/sites/snapshots`, { signal, query: { site_id: siteID, page_id: pageID, cursor } });
        items.push(...page.items.map(validateWebsiteSnapshot));
        const nextCursor = page.next_cursor || undefined;
        if (!nextCursor) return items;
        if (seenCursors.has(nextCursor)) throw new ApiError(502, "invalid_pagination", "Сервер вернул повторный указатель списка снапшотов");
        seenCursors.add(nextCursor);
        cursor = nextCursor;
      }
      throw new ApiError(502, "snapshot_list_too_large", "Список снапшотов превышает допустимый размер");
    },
    featureCatalog(cursor?: string, limit?: number, signal?: AbortSignal): Promise<WebsiteFeatureVersionPage> {
      return request<WebsiteFeatureVersionPage>(`${ADMIN_ROOT}/sites/features`, { signal, query: { cursor, limit } });
    },
    featureVersions(featureID: string, cursor?: string, limit?: number, signal?: AbortSignal): Promise<WebsiteFeatureVersionPage> {
      return request<WebsiteFeatureVersionPage>(`${ADMIN_ROOT}/sites/features/${encoded(featureID)}/versions`, { signal, query: { cursor, limit } });
    },
    featureVersion(featureID: string, version: string, signal?: AbortSignal): Promise<WebsiteFeatureVersion> {
      return request<WebsiteFeatureVersion>(`${ADMIN_ROOT}/sites/features/${encoded(featureID)}/versions/${encoded(version)}`, { signal });
    },
    snapshot(id: string, signal?: AbortSignal): Promise<WebsiteSnapshot> {
      return request<unknown>(`${ADMIN_ROOT}/sites/snapshots/${encoded(id)}`, { signal }).then((value) => {
        const snapshot = validateWebsiteSnapshot(value);
        if (snapshot.id !== id) throw new ApiError(502, "invalid_response", "Сервер вернул другой снапшот");
        return snapshot;
      });
    },
    createSnapshot(input: { site_id: string; page_id: string; parent_id?: string; name: string; expected_revision: number; expected_content_revision: number; payload: WebsiteSnapshotPayload }, idempotencyKey: string): Promise<WebsiteSnapshot> {
      const payload = snapshotPayloadFromWebsite(input.payload, input.page_id, true);
      return request<unknown>(`${ADMIN_ROOT}/sites/snapshots`, {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: { ...input, payload },
      }).then(validateWebsiteSnapshot);
    },
    deleteSnapshot(id: string, revision: number): Promise<void> {
      if (!Number.isSafeInteger(revision) || revision < 1) throw new ApiError(400, "invalid_revision", "Для удаления требуется актуальная ревизия снапшота");
      return request<void>(`${ADMIN_ROOT}/sites/snapshots/${encoded(id)}`, { method: "DELETE", headers: { "If-Match": `"${revision}"` } });
    },
    buildSnapshot(snapshotID: string, idempotencyKey: string): Promise<WebsiteBuild> {
      return request<WebsiteBuild>(`${ADMIN_ROOT}/sites/builds`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: { snapshot_id: snapshotID },
      });
    },
    build(id: string, signal?: AbortSignal): Promise<WebsiteBuild> {
      return request<WebsiteBuild>(`${ADMIN_ROOT}/sites/builds/${encoded(id)}`, { signal });
    },
    createMediaDerivative(input: { source_file_id: string; feature_id: string; profile: string }, idempotencyKey: string): Promise<WebsiteMediaJob> {
      return request<WebsiteMediaJob>(`${ADMIN_ROOT}/sites/media/derivatives`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: input,
      });
    },
    mediaJob(id: string, signal?: AbortSignal): Promise<WebsiteMediaJob> {
      return request<WebsiteMediaJob>(`${ADMIN_ROOT}/sites/media/jobs/${encoded(id)}`, { signal });
    },
    async featureBuilds(signal?: AbortSignal): Promise<{ items: FeatureBuild[] }> {
      const items: FeatureBuild[] = [];
      const seenCursors = new Set<string>();
      let cursor: string | undefined;
      for (let pageNumber = 0; pageNumber < 500; pageNumber += 1) {
        const page = await request<WebsiteFeatureVersionPage>(`${ADMIN_ROOT}/sites/features`, { signal, query: { cursor } });
        if (!page || !Array.isArray(page.items)) throw invalidFeatureCatalog();
        items.push(...page.items.map(featureBuildFromCatalog));
        if (page.next_cursor != null && typeof page.next_cursor !== "string") throw invalidFeatureCatalog();
        const nextCursor = page.next_cursor || undefined;
        if (!nextCursor) return { items };
        if (seenCursors.has(nextCursor)) throw invalidFeatureCatalog();
        seenCursors.add(nextCursor);
        cursor = nextCursor;
      }
      throw new ApiError(502, "catalog_too_large", "Каталог фич превышает допустимый размер");
    },
    beginFeatureImport(sourceFileID: string, idempotencyKey: string, expectedSHA256?: string): Promise<WebsiteFeatureImport> {
      return request<unknown>(`${ADMIN_ROOT}/sites/feature-imports`, {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: { source_file_id: sourceFileID, ...(expectedSHA256 ? { expected_sha256: expectedSHA256 } : {}) },
      }).then(parseFeatureImport);
    },
    featureImport(id: string, signal?: AbortSignal): Promise<WebsiteFeatureImport> {
      return request<unknown>(`${ADMIN_ROOT}/sites/feature-imports/${encoded(id)}`, { signal }).then(parseFeatureImport);
    },
  },

  contentEngine: {
    workspace(snapshotID: string, signal?: AbortSignal): Promise<ModelContentWorkspace> {
      return request<unknown>(`${ADMIN_ROOT}/content/snapshots/${encoded(snapshotID)}/workspace`, { signal })
        .then((value) => parseContentWorkspace(value, snapshotID));
    },
    saveWorkspaceDraft(
      snapshotID: string,
      expectedRevision: number,
      records: ModelContentRecordInput[],
      selections: ModelContentSelection[],
      idempotencyKey: string,
    ): Promise<ModelContentWorkspace> {
      const body = {
        expected_revision: expectedRevision,
        records: records.map((record) => ({
          ...(record.id ? { id: record.id } : {}),
          ...(record.clientKey ? { client_key: record.clientKey } : {}),
          entity_id: record.entityId,
          sort_order: record.sortOrder,
          values: record.values,
        })),
        selections: selections.map((selection) => ({
          placement_id: selection.placementId,
          input_key: selection.inputKey,
          mode: selection.mode,
          ...(selection.mode === "ids" ? { record_ids: selection.recordIds } : {}),
        })),
      };
      return request<unknown>(`${ADMIN_ROOT}/content/snapshots/${encoded(snapshotID)}/workspace`, {
        method: "PUT", headers: { "Idempotency-Key": idempotencyKey }, body,
      }).then((value) => parseContentWorkspace(value, snapshotID));
    },
    publishWorkspace(snapshotID: string, expectedRevision: number, idempotencyKey: string): Promise<ModelContentWorkspace> {
      return request<unknown>(`${ADMIN_ROOT}/content/snapshots/${encoded(snapshotID)}/workspace/publish`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: { expected_revision: expectedRevision },
      }).then((value) => parseContentWorkspace(value, snapshotID));
    },
    history(snapshotID: string, cursor?: string, limit?: number, signal?: AbortSignal): Promise<ModelContentHistoryResponse> {
      return request<unknown>(`${ADMIN_ROOT}/content/snapshots/${encoded(snapshotID)}/history`, { signal, query: { cursor, limit } })
        .then(parseContentHistory);
    },
    restoreHistory(snapshotID: string, historyID: string, expectedRevision: number, idempotencyKey: string): Promise<ModelContentWorkspace> {
      return request<unknown>(`${ADMIN_ROOT}/content/snapshots/${encoded(snapshotID)}/history/${encoded(historyID)}/restore`, {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: { expected_revision: expectedRevision },
      }).then((value) => parseContentWorkspace(value, snapshotID));
    },
  },

  formsEngine: {
    schemas(snapshotID: string, cursor?: string, limit?: number, signal?: AbortSignal) {
      return request<unknown>(`${ADMIN_ROOT}/forms/schemas`, { signal, query: { snapshot_id: snapshotID, cursor, limit } })
        .then(parseFormSchemaPage);
    },
    schema(snapshotID: string, schemaID: string, signal?: AbortSignal): Promise<FormSchema> {
      return request<unknown>(`${ADMIN_ROOT}/forms/schemas/${encoded(schemaID)}`, { signal, query: { snapshot_id: snapshotID } })
        .then(parseFormSchema);
    },
    saveSchema(input: { snapshot_id: string; schema_id?: string; key: string; fields: Record<string, FormFieldDefinition>; expected_revision: number }): Promise<FormSchema> {
      return request<unknown>(`${ADMIN_ROOT}/forms/schemas`, { method: "PUT", body: input }).then(parseFormSchema);
    },
    submissions(snapshotID: string, filters: { form_id?: string; state?: string } = {}, cursor?: string, limit?: number, signal?: AbortSignal) {
      return requestList<FormSubmission>(`${ADMIN_ROOT}/forms/submissions`, { signal, query: { snapshot_id: snapshotID, ...filters, cursor, limit } });
    },
    async exportSubmissions(snapshotID: string, filters: { form_id?: string; state?: string } = {}, signal?: AbortSignal): Promise<{ blob: Blob; filename: string }> {
      const response = await rawRequest(`${ADMIN_ROOT}/forms/submissions/export`, {
        signal, headers: { Accept: "text/csv" }, query: { snapshot_id: snapshotID, ...filters },
      });
      if (!response.ok) throw await parseErrorResponse(response);
      if (response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase() !== "text/csv") {
        throw new ApiError(502, "invalid_response", "Сервер вернул некорректный CSV-экспорт");
      }
      const filename = filenameFromDisposition(response.headers.get("Content-Disposition"), `forms-${snapshotID}-submissions.csv`);
      return { blob: await response.blob(), filename };
    },
    submission(snapshotID: string, id: string, signal?: AbortSignal): Promise<FormSubmission> {
      return request<FormSubmission>(`${ADMIN_ROOT}/forms/submissions/${encoded(id)}`, { signal, query: { snapshot_id: snapshotID } });
    },
    async setSubmissionStatus(snapshotID: string, id: string, status: string, expectedRevision: number): Promise<FormSubmission> {
      if (!Number.isSafeInteger(expectedRevision) || expectedRevision < 1) {
        throw new ApiError(400, "invalid_revision", "Для изменения статуса требуется актуальная ревизия заявки");
      }
      return request<FormSubmission>(`${ADMIN_ROOT}/forms/submissions/${encoded(id)}/status`, { method: "PATCH", body: { snapshot_id: snapshotID, status, expected_revision: expectedRevision } });
    },
    async deleteSubmission(snapshotID: string, id: string, revision: number): Promise<void> {
      if (!Number.isSafeInteger(revision) || revision < 1) {
        throw new ApiError(400, "invalid_revision", "Для удаления требуется актуальная ревизия заявки");
      }
      return request(`${ADMIN_ROOT}/forms/submissions/${encoded(id)}`, {
        method: "DELETE", query: { snapshot_id: snapshotID }, headers: { "If-Match": `"r${revision}"` },
      });
    },
  },

  router: {
    operations: {
      async list(signal?: AbortSignal): Promise<RouterPublicOperationPage> {
        const response = await request<unknown>(`${ADMIN_ROOT}/router/operations`, { signal });
        if (!isRecord(response) || !Array.isArray(response.items)) throw invalidRouterOperation();
        return { items: response.items.map(validateRouterPublicOperation) };
      },
    },
    routes: {
      async list(params: { host?: string; cursor?: string; limit?: number } = {}, signal?: AbortSignal): Promise<RouterRoutePage> {
        const page = await requestList<unknown>(`${ADMIN_ROOT}/router/routes`, { signal, query: params });
        return { items: page.items.map(validateRouterRoute), next_cursor: page.next_cursor ?? undefined };
      },
      get(id: string, signal?: AbortSignal): Promise<RouterRoute> {
        return request<unknown>(`${ADMIN_ROOT}/router/routes/${encoded(id)}`, { signal }).then((value) => {
          const route = validateRouterRoute(value);
          if (route.id !== id) throw invalidRouterRoute();
          return route;
        });
      },
      create(input: { host: string; path: string; match: RouterRouteMatch; target: RouterRouteTarget }, idempotencyKey: string): Promise<RouterRoute> {
        return request<unknown>(`${ADMIN_ROOT}/router/routes`, { method: "POST", headers: { "Idempotency-Key": idempotencyKey }, body: input }).then(validateRouterRoute);
      },
      replaceTarget(id: string, target: RouterRouteTarget, revision: number): Promise<RouterRoute> {
        return request<unknown>(`${ADMIN_ROOT}/router/routes/${encoded(id)}/target`, { method: "PUT", headers: { "If-Match": `"${revision}"` }, body: { target } }).then((value) => {
          const route = validateRouterRoute(value);
          if (route.id !== id || route.revision <= revision) throw invalidRouterRoute();
          return route;
        });
      },
      delete(id: string, revision: number): Promise<void> {
        return request(`${ADMIN_ROOT}/router/routes/${encoded(id)}`, { method: "DELETE", headers: { "If-Match": `"${revision}"` } });
      },
    },
  },

  coordinator: {
    jobs: {
      list(params: { type?: string; state?: string; limit?: number; before?: string }, signal?: AbortSignal) {
        return requestList<CoordinatorJob>("/api/coordinator/jobs", {
          signal,
          query: { type: params.type, state: params.state, limit: params.limit, before: params.before },
        });
      },
      permissions(signal?: AbortSignal): Promise<CoordinatorJobPermissions> {
        return request<CoordinatorJobPermissions>("/api/coordinator/jobs/permissions", { signal });
      },
      retry(id: string): Promise<void> {
        return request<void>(`/api/coordinator/jobs/${encoded(id)}/retry`, { method: "POST" });
      },
    },
  },

  auth: {
    me(signal?: AbortSignal): Promise<Principal> {
      return request<{ principal: Principal }>(`${ADMIN_ROOT}/session`, {
        signal,
        suppressUnauthorizedEvent: true,
      }).then((response) => response.principal);
    },
    async login(credential: string): Promise<Principal> {
      const response = await request<{ principal: Principal }>(`${ADMIN_ROOT}/session`, {
        method: "POST",
        body: { credential },
        suppressUnauthorizedEvent: true,
      });
      return response.principal;
    },
    logout(): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/session`, { method: "DELETE", suppressUnauthorizedEvent: true });
    },
  },

  principals: {
    list(signal?: AbortSignal): Promise<Principal[]> {
      return request<Principal[]>(`${ADMIN_ROOT}/principals`, { signal });
    },
    create(input: { name: string; can_manage_administrators: boolean; group_access: Record<string, GroupLevel>; expires_at?: string }): Promise<CreatedPrincipal> {
      return request<CreatedPrincipal>(`${ADMIN_ROOT}/principals`, { method: "POST", body: input });
    },
    deactivate(id: string): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/principals/${encoded(id)}`, { method: "DELETE" });
    },
    setAdministratorManagement(id: string, enabled: boolean): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/principals/${encoded(id)}/management`, { method: "PATCH", body: { enabled } });
    },
    credentials(id: string, signal?: AbortSignal): Promise<Credential[]> {
      return request<Credential[]>(`${ADMIN_ROOT}/principals/${encoded(id)}/credentials`, { signal });
    },
    issueCredential(id: string, expires_at?: string): Promise<IssuedCredential> {
      return request<IssuedCredential>(`${ADMIN_ROOT}/principals/${encoded(id)}/credentials`, { method: "POST", body: { expires_at } });
    },
    revokeCredential(id: string): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/credentials/${encoded(id)}`, { method: "DELETE" });
    },
    groups(signal?: AbortSignal): Promise<AccessGroup[]> {
      return request<AccessGroup[]>(`${ADMIN_ROOT}/access-groups`, { signal });
    },
    groupAccess(principalId: string, group: string, signal?: AbortSignal): Promise<{ principal_id: string; group: string; level: GroupLevel }> {
      return request(`${ADMIN_ROOT}/principals/${encoded(principalId)}/groups/${encoded(group)}`, { signal });
    },
    setGroupAccess(principalId: string, group: string, level: GroupLevel): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/principals/${encoded(principalId)}/groups/${encoded(group)}`, { method: "PUT", body: { level } });
    },
  },

  media: {
    limits(signal?: AbortSignal): Promise<FileLimits> {
      return request<FileLimits>("/api/files/limits", { signal });
    },
    createUpload(input: {name:string;size:number;sha256:string;contentType:string;groups:string[]}, idempotencyKey: string): Promise<FileUploadSession> {
      return request("/api/files/uploads",{method:"POST",body:input,headers:{"Idempotency-Key":idempotencyKey}});
    },
    uploadStatus(id:string): Promise<FileUploadSession> {
      return request(`/api/files/uploads/${encoded(id)}`);
    },
    commitUpload(id:string): Promise<{id:string}> {
      return request(`/api/files/uploads/${encoded(id)}/commit`,{method:"POST"});
    },
    abortUpload(id:string): Promise<void> {
      return request(`/api/files/uploads/${encoded(id)}`,{method:"DELETE"});
    },

    async list(params: MediaListParams = {}, signal?: AbortSignal): Promise<ListResponse<Media>> {
      const response = await request<{items?: CoreFile[] | null; nextCursor?: string | null}>("/api/files", {
        signal,
        query: {
          q: params.q,
          limit: params.limit,
          cursor: params.cursor,
        },
      });
      return {items: arrayOrEmpty(response?.items).map(toMedia), next_cursor: response?.nextCursor ?? null};
    },
    async fileSets(params: { namespace?: string; query?: string; limit?: number; cursor?: string } = {}, signal?: AbortSignal): Promise<ListResponse<FileSet>> {
      const response = await request<{ items?: unknown[] | null; nextCursor?: string | null } | null>("/api/files/sets", {
        signal,
        query: { namespace: params.namespace, q: params.query, limit: params.limit, cursor: params.cursor },
      });
      const nextCursor = response && response.nextCursor;
      if (!isRecord(response) || !Array.isArray(response.items) ||
        (nextCursor != null && (typeof nextCursor !== "string" || nextCursor.length > 2048))) {
        throw invalidFileSet();
      }
      return { items: response.items.map(validateFileSet), next_cursor: typeof nextCursor === "string" ? nextCursor : null };
    },
    async get(id: string, signal?: AbortSignal): Promise<Media> {
      return toMedia(await request<CoreFile>(`/api/files/objects/${encoded(id)}`, { signal }));
    },
    async rename(id: string, filename: string, revision: number): Promise<Media> {
      return toMedia(await request<CoreFile>(`/api/files/objects/${encoded(id)}`, {
        method: "PATCH",
        body: { name: filename },
        headers: { "If-Match": `"${id}-r${revision}"` },
      }));
    },
    prepareRenditions(id: string, context: MediaRenditionContext): Promise<MediaRenditionState> {
      return request<MediaRenditionState>(
        `${ADMIN_ROOT}/sites/media/${encoded(id)}/renditions/prepare`,
        {
          method: "POST",
          body: {
            snapshotId: context.snapshotId,
            featureKey: context.featureKey,
            buildVersion: context.buildVersion,
            schemaKind: context.schemaKind,
            fieldPath: context.fieldPath,
            buildDigest: context.buildDigest,
          },
        },
      );
    },
    remove(id: string, revision: number): Promise<void> {
      return request<void>(`/api/files/objects/${encoded(id)}`, {
        method: "DELETE",
        headers: { "If-Match": `"${id}-r${revision}"` },
      });
    },
  },

  siteBuilder: {
    async snapshot(id: string, signal?: AbortSignal): Promise<SnapshotDetail> {
      return siteSnapshotDetailFromWebsite(await api.website.snapshot(id, signal));
    },
    async createSnapshotFrom(input: {
      name: string;
      siteID?: string;
      pageID?: string;
      parentSnapshotId?: string;
      expectedParentRevision?: number;
      expectedParentContentRevision?: number;
      idempotencyKey?: string;
      config?: SiteConfig | SiteEditorDraft;
      metadata?: SiteMetadata;
    }): Promise<SiteSnapshot> {
      const parent = input.parentSnapshotId ? await api.website.snapshot(input.parentSnapshotId) : undefined;
      if (input.parentSnapshotId && (!parent?.payload || input.expectedParentRevision === undefined || input.expectedParentContentRevision === undefined)) {
        throw new ApiError(400, "invalid_snapshot_parent", "Для создания потомка обновите снапшот и повторите действие");
      }
      const config = input.config ?? parent?.payload?.config;
      const metadata = input.metadata ?? parent?.payload?.metadata;
      const siteID = input.siteID ?? parent?.site_id;
      const pageID = input.pageID ?? parent?.page_id;
      if (!siteID || !pageID || !config || !metadata) {
        throw new ApiError(400, "invalid_snapshot_scope", "Для корневого снапшота нужны site_id и page_id из конфигурации запуска");
      }
      const payload = snapshotPayloadFromWebsite({ config, metadata }, pageID, true);
      const snapshotName = input.name.trim();
      const created = await api.website.createSnapshot({
        site_id: siteID,
        page_id: pageID,
        ...(input.parentSnapshotId ? { parent_id: input.parentSnapshotId } : {}),
        name: snapshotName,
        expected_revision: input.expectedParentRevision ?? 0,
        expected_content_revision: input.expectedParentContentRevision ?? 0,
        payload: { config: payload.config, metadata },      }, input.idempotencyKey ?? `snapshot-${crypto.randomUUID()}`);
      if (created.site_id !== siteID || created.page_id !== pageID || created.parent_id !== input.parentSnapshotId || created.name !== snapshotName || !created.payload) {
        throw new ApiError(502, "invalid_response", "Сервер создал снапшот с неожиданными данными");
      }
      snapshotPayloadFromWebsite(created.payload, created.page_id);
      return siteSnapshotFromWebsite(created);
    },
    deleteSnapshot(id: string, revision: number): Promise<void> {
      return api.website.deleteSnapshot(id, revision);
    },
    async contentSnapshots(signal?: AbortSignal, pageID?: string, siteID?: string): Promise<{ items: SiteSnapshot[] }> {
      return { items: (await api.website.allSnapshots(signal, siteID, pageID)).map(siteSnapshotFromWebsite) };
    },
  },

};

export function publicMediaPath(id: string, variant?: string): string {
  const suffix = variant ? `?variant=${encodeURIComponent(variant)}` : "";
  return `/api/files/objects/${encodeURIComponent(id)}/content${suffix}`;
}

export function absolutePublicMediaUrl(id: string, variant?: string): string {
  return `${window.location.origin}${publicMediaPath(id, variant)}`;
}

// Browser uploads use the same resumable owner-scoped API as server clients.
// The worker hashes 1 MiB blocks and never reads the whole file into memory.
type HashResult = {sha256:string;parts:string[]};
type PendingUpload = {id?:string;idempotencyKey:string;sha256:string;partBytes:number;expiresAt:string};
function pendingKey(file:File,groups:string[]):string {
  return `we:upload:v2:${file.size}:${file.lastModified}:${[...groups].sort().join(",")}`;
}
function savedUpload(key:string):PendingUpload|null {
  try { const value=JSON.parse(localStorage.getItem(key) ?? "null") as PendingUpload|null;
    return value && (value.id===undefined || typeof value.id==="string") && typeof value.idempotencyKey==="string" && typeof value.sha256==="string" && typeof value.partBytes==="number" && typeof value.expiresAt==="string" ? value : null;
  } catch { return null; }
}
function rememberUpload(key:string,value:PendingUpload|null):void {
  try { if(value) localStorage.setItem(key,JSON.stringify(value)); else localStorage.removeItem(key); } catch { /* storage may be disabled; upload still works */ }
}
function hashFile(file:File,partBytes:number,onProgress:(progress:number)=>void,signal?:AbortSignal):Promise<HashResult> {
  return new Promise((resolve,reject)=>{
    if(signal?.aborted){reject(new DOMException("Aborted","AbortError"));return;}
    const worker=new Worker(new URL("./domains/media/sha256-worker.ts",import.meta.url),{type:"module"});
    const cleanup=()=>{worker.terminate();signal?.removeEventListener("abort",abort);};
    const abort=()=>{cleanup();reject(new DOMException("Aborted","AbortError"));};
    signal?.addEventListener("abort",abort,{once:true});
    worker.onmessage=(event:MessageEvent<{type:string;bytes?:number;total?:number;sha256?:string;parts?:string[];message?:string}>)=>{
      const msg=event.data;
      if(msg.type==="progress"&&msg.bytes!==undefined&&msg.total)onProgress(msg.bytes/msg.total);
      else if(msg.type==="result"&&msg.sha256&&msg.parts){cleanup();resolve({sha256:msg.sha256,parts:msg.parts});}
      else if(msg.type==="error"){cleanup();reject(new Error(msg.message??"Не удалось проверить файл"));}
    };
    worker.onerror=()=>{cleanup();reject(new Error("Не удалось проверить файл"));};
    worker.postMessage({file,partBytes});
  });
}
function uploadPart(id:string,number:number,blob:Blob,sha256:string,onProgress:(fraction:number)=>void,signal?:AbortSignal):Promise<FileUploadSession> {
  return new Promise((resolve,reject)=>{
    if(signal?.aborted){reject(new DOMException("Aborted","AbortError"));return;}
    const xhr=new XMLHttpRequest();xhr.open("PUT",buildUrl(`/api/files/uploads/${encoded(id)}/parts/${number}`));
    xhr.responseType="json";xhr.setRequestHeader("Content-Type","application/octet-stream");xhr.setRequestHeader("X-Content-SHA256",sha256);
    xhr.withCredentials=true;
    const csrf=csrfToken();if(csrf)xhr.setRequestHeader("X-CSRF-Token",csrf);
    const abort=()=>xhr.abort();signal?.addEventListener("abort",abort,{once:true});
    const cleanup=()=>signal?.removeEventListener("abort",abort);
    xhr.upload.onprogress=event=>{if(event.lengthComputable&&event.total>0)onProgress(event.loaded/event.total);};
    xhr.onerror=()=>{cleanup();reject(new ApiError(0,"network_error","Не удалось передать часть. Проверьте подключение и повторите загрузку."));};
    xhr.onabort=()=>{cleanup();reject(new DOMException("Aborted","AbortError"));};
    xhr.onload=()=>{cleanup();if(xhr.status>=200&&xhr.status<300){resolve(xhr.response as FileUploadSession);return;}if(xhr.status===401)notifyUnauthorized("Сессия истекла. Войдите снова.");
      const payload=xhr.response as {error?:ApiErrorPayload}|null;reject(new ApiError(xhr.status,payload?.error?.code??"upload_failed",payload?.error?.message??"Не удалось загрузить часть файла",payload?.error?.details??[],payload?.error?.request_id));};
    xhr.send(blob);
  });
}
export async function uploadMediaFile(file:File,groups:string[],onProgress:(percent:number|null)=>void,signal?:AbortSignal):Promise<Media> {
  if(groups.length===0||groups.some(group=>!/^[a-z][a-z0-9_-]{0,63}$/.test(group)))throw new Error("Выберите группу с правом записи");
  const limits=await api.media.limits(signal);
  if(file.size<1||file.size>limits.maxBytes)throw new Error("Размер файла превышает разрешённый сервером или файл пуст");
  const key=pendingKey(file,groups);const saved=savedUpload(key);
  const partBytes=saved && Date.parse(saved.expiresAt)>Date.now() ? saved.partBytes : limits.partBytes;
  if(!Number.isSafeInteger(partBytes)||partBytes<1||partBytes>limits.maxBytes)throw new Error("Сервер вернул некорректный размер части");
  let session:FileUploadSession|undefined;
  try {
    onProgress(0);const hashes=await hashFile(file,partBytes,fraction=>onProgress(Math.floor(fraction*20)),signal);
    const resumeSaved=saved&&saved.sha256===hashes.sha256&&saved.partBytes===partBytes&&Date.parse(saved.expiresAt)>Date.now()?saved:null;
    const idempotencyKey=resumeSaved?.idempotencyKey??`upload-${crypto.randomUUID()}`;
    if(resumeSaved?.id){
      try {session=await api.media.uploadStatus(resumeSaved.id);}catch(error){if(!(error instanceof ApiError)||![404,410].includes(error.status))throw error;rememberUpload(key,null);}
      if(session&&(session.partSize!==partBytes||session.state==="aborted"))session=undefined;
    }
    if(!session){
      rememberUpload(key,{idempotencyKey,sha256:hashes.sha256,partBytes,expiresAt:new Date(Date.now()+15*60_000).toISOString()});
      session=await api.media.createUpload({name:file.name,size:file.size,sha256:hashes.sha256,contentType:file.type||"application/octet-stream",groups},idempotencyKey);
    }
    rememberUpload(key,{id:session.id,idempotencyKey,sha256:hashes.sha256,partBytes:session.partSize,expiresAt:session.expiresAt});
    if(session.state==="committed"&&session.fileId){const result=await api.media.get(session.fileId,signal);rememberUpload(key,null);onProgress(100);return result;}
    if(session.state!=="committed") {
      const completed=new Map(session.parts.map(part=>[part.number,part]));
      let transferred=0;
      for(let index=0;index<hashes.parts.length;index++){
        if(signal?.aborted)throw new DOMException("Aborted","AbortError");
        const start=index*partBytes,end=Math.min(file.size,start+partBytes),expected=hashes.parts[index]!;
        const existing=completed.get(index+1);
        if(existing && (existing.sha256!==expected||existing.size!==end-start))throw new Error("Сохранённая часть не совпадает с файлом. Отмените загрузку и начните заново.");
        if(!existing) {
          const refreshed = await uploadPart(session.id,index+1,file.slice(start,end),expected,fraction=>onProgress(20+Math.round(75*(transferred+fraction*(end-start))/file.size)),signal);
          rememberUpload(key,{id:session.id,idempotencyKey,sha256:hashes.sha256,partBytes:session.partSize,expiresAt:refreshed.expiresAt});
        }
        transferred+=end-start;onProgress(20+Math.round(75*transferred/file.size));
      }
    }
    onProgress(96);const committed=await api.media.commitUpload(session.id);const result=await api.media.get(committed.id,signal);rememberUpload(key,null);onProgress(100);return result;
  } catch(error) {
    if(signal?.aborted && session){try{await api.media.abortUpload(session.id);}catch{/* TTL handles disconnected cancellation */}rememberUpload(key,null);}
    throw error;
  }
}

export function isUploadAccepted(file:File):boolean {return file.size>0;}

export const mediaStatusValues: MediaStatus[] = [
  "processing",
  "ready",
  "failed",
  "deleted",
];
