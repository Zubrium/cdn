import type { BranchRecord, SnapshotDetail } from "@/types";
import type {
  ApiErrorDetail,
  ApiErrorPayload,
  CreateTokenInput,
  CreatedToken,
  DesignSystem,
  DesignSystemState,
  DesignTokenDefinition,
  Environment,
  EnvironmentPatch,
  FeatureBuild,
  Feature,
  FeatureListParams,
  FeatureVersion,
  ListResponse,
  Media,
  FileLimits, FileReference, FileUploadSession, FileVisibility,
  MediaListParams,
  MediaRenditionContext,
  MediaRenditionState,
  MediaStatus,
  Overview,
  Principal,
  Settings,
  SiteEnvironment,
  SiteConfig,
  SiteMetadata,
  SiteMetadataState,
  SiteSnapshot,
  SiteSnapshotList,
  SnapshotEvent,
  ModelContentHistoryResponse,
  ModelContentRecordInput,
  ModelContentSelection,
  ModelContentWorkspace,
  Submission,
  SubmissionFilters,
  SubmissionListResponse,
  SubmissionStatus,
  Token,
} from "./types";

import type { SnapshotDesignWorkspace, StructuredDesignSystem } from "@/domains/design-system/types";

export const TOKEN_STORAGE_KEY = "we_admin_token";
export const UNAUTHORIZED_EVENT = "we:unauthorized";

const API_ROOT = "/api/v1";
const ADMIN_ROOT = `${API_ROOT}/admin`;
const ADMIN_V2_ROOT = "/api/v2/admin";

type QueryValue = string | number | boolean | null | undefined;

interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Record<string, QueryValue>;
  signal?: AbortSignal;
  /** Login/me checks handle their own 401 state. */
  suppressUnauthorizedEvent?: boolean;
}

function arrayOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

// The API omits empty `recordIds` for `all` selections. Normalize optional
// collections at the boundary so editor components can use stable arrays.
function normalizeModelContentWorkspace(workspace: ModelContentWorkspace): ModelContentWorkspace {
  return {
    ...workspace,
    records: arrayOrEmpty(workspace?.records).map((record) => ({
      ...record,
      values: record.values ?? {},
      relations: record.relations ?? {},
    })),
    selections: arrayOrEmpty(workspace?.selections).map((selection) => ({
      ...selection,
      recordIds: arrayOrEmpty(selection?.recordIds),
    })),
    placements: arrayOrEmpty(workspace?.placements).map((placement) => ({
      ...placement,
      inputs: arrayOrEmpty(placement?.inputs).map((input) => ({
        ...input,
        fields: arrayOrEmpty(input?.fields),
      })),
    })),
  };
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

async function requestItems<T>(
  path: string,
  options: RequestOptions = {},
): Promise<{ items: T[] }> {
  const response = await request<{ items?: T[] | null } | null>(path, options);
  return { items: arrayOrEmpty(response?.items) };
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

export interface SitepackImportReport {
  snapshot: SiteSnapshot;
  newBuilds: number;
  reusedBuilds: number;
  newFiles: number;
  reusedFiles: number;
  modelPublishedRecords: number;
  modelDraftRecords: number;
  modelHistoryRevisions: number;
  remappedModelEntityIds: number;
  remappedModelFieldIds: number;
  remappedModelRelationIds: number;
  remappedModelRecordIds: number;
  fileIds: Record<string, string>;
  fileIdsTruncated?: boolean;
  items: SitepackReportItem[];
  itemsTruncated?: boolean;
}

export interface SitepackReportItem {
	kind: "build" | "file" | "model_entity" | "model_field" | "model_relation" | "model_record";
  path: string;
  action: string;
  sourceId?: string;
  targetId?: string;
  count?: number;
  message: string;
}

interface SitepackLimits {
  maxBytes: number;
  browserFallbackBytes: number;
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export function getStoredToken(): string | null {
  try {
    const token = window.localStorage.getItem(TOKEN_STORAGE_KEY);
    return token && token.trim() ? token.trim() : null;
  } catch {
    return null;
  }
}

export function setStoredToken(token: string | null): void {
  try {
    if (token) {
      window.localStorage.setItem(TOKEN_STORAGE_KEY, token);
    } else {
      window.localStorage.removeItem(TOKEN_STORAGE_KEY);
    }
  } catch {
    // Storage can be unavailable in hardened browser modes. The app still
    // works for the current tab until reload.
  }
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

function authHeaders(): Record<string, string> {
  const token = getStoredToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
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
        parsed.error &&
        typeof parsed.error === "object"
      ) {
        payload = (parsed as { error: ApiErrorPayload }).error;
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
    ...authHeaders(),
  };
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
      method: options.method ?? "GET",
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

async function requestBlob(
  path: string,
  options: RequestOptions = {},
): Promise<{ blob: Blob; filename: string }> {
  const response = await rawRequest(path, options);
  if (!response.ok) throw await parseErrorResponse(response);
  const blob = await response.blob();
  return {
    blob,
    filename: filenameFromDisposition(
      response.headers.get("Content-Disposition"),
      "export.csv",
    ),
  };
}

const encoded = (value: string) => encodeURIComponent(value);

export const api = {
  auth: {
    me(signal?: AbortSignal): Promise<Principal> {
      return request<Principal>(`${ADMIN_ROOT}/me`, {
        signal,
        suppressUnauthorizedEvent: true,
      });
    },
  },

  overview: {
    async get(signal?: AbortSignal): Promise<Overview> {
      const overview = await request<Overview>(`${ADMIN_ROOT}/overview`, { signal });
      return {
        ...overview,
        attention: {
          ...overview?.attention,
          draft_model_snapshots: arrayOrEmpty(overview?.attention?.draft_model_snapshots),
          new_submissions: arrayOrEmpty(overview?.attention?.new_submissions),
          failed_media: arrayOrEmpty(overview?.attention?.failed_media),
        },
        recent_activity: arrayOrEmpty(overview?.recent_activity),
      };
    },
  },

  tokens: {
    list(signal?: AbortSignal): Promise<ListResponse<Token>> {
      return requestList<Token>(`${ADMIN_ROOT}/tokens`, { signal });
    },
    get(id: string): Promise<Token> {
      return request<Token>(`${ADMIN_ROOT}/tokens/${encoded(id)}`);
    },
    create(input: CreateTokenInput): Promise<CreatedToken> {
      return request<CreatedToken>(`${ADMIN_ROOT}/tokens`, {
        method: "POST",
        body: input,
      });
    },
    revoke(id: string): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/tokens/${encoded(id)}`, {
        method: "DELETE",
      });
    },
  },

  features: {
    list(
      params: FeatureListParams = {},
      signal?: AbortSignal,
    ): Promise<ListResponse<Feature>> {
      return requestList<Feature>(`${ADMIN_ROOT}/features`, {
        signal,
        query: {
          status: params.status,
          type: params.type,
          q: params.q,
          limit: params.limit,
          cursor: params.cursor,
        },
      });
    },
    versions(id: string, signal?: AbortSignal): Promise<{ items: FeatureVersion[] }> {
      return requestItems<FeatureVersion>(
        `${ADMIN_ROOT}/features/${encoded(id)}/versions`,
        { signal },
      );
    },
  },

  forms: {
    branches(signal?: AbortSignal): Promise<{ items: Array<{ id: string; name: string; enabled: boolean }> }> {
      return request(`${ADMIN_ROOT}/submission-branches`, { signal });
    },
    submissions(
      key: string,
      filters: SubmissionFilters,
      cursor?: string | null,
      limit?: number,
      signal?: AbortSignal,
    ): Promise<SubmissionListResponse> {
      return request<SubmissionListResponse | null>(
        key ? `${ADMIN_ROOT}/forms/${encoded(key)}/submissions` : `${ADMIN_ROOT}/submissions`,
        {
          signal,
          query: {
            limit,
            cursor,
            branchId: filters.branchId,
            environment: filters.environment,
            status: filters.status,
            created_from: filters.created_from,
            created_to: filters.created_to,
            q: filters.q,
            field: filters.field,
            include_archived: filters.include_archived ? "true" : undefined,
            sort: filters.sort,
          },
        },
      ).then((response) => ({
        items: arrayOrEmpty(response?.items),
        next_cursor: response?.next_cursor ?? null,
        fields: arrayOrEmpty(response?.fields),
      }));
    },
    get(id: string, signal?: AbortSignal): Promise<Submission> {
      return request<Submission>(`${ADMIN_ROOT}/submissions/${encoded(id)}`, {
        signal,
      });
    },
    updateStatus(id: string, status: SubmissionStatus): Promise<Submission> {
      return request<Submission>(
        `${ADMIN_ROOT}/submissions/${encoded(id)}/status`,
        { method: "PATCH", body: { status } },
      );
    },
    archive(id: string): Promise<Submission> {
      return request<Submission>(
        `${ADMIN_ROOT}/submissions/${encoded(id)}/archive`,
        { method: "POST" },
      );
    },
    remove(id: string): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/submissions/${encoded(id)}`, {
        method: "DELETE",
      });
    },
    exportCsv(
      key: string,
      filters: SubmissionFilters,
    ): Promise<{ blob: Blob; filename: string }> {
      return requestBlob(key ? `${ADMIN_ROOT}/forms/${encoded(key)}/submissions/export` : `${ADMIN_ROOT}/submissions/export`, {
        query: {
          branchId: filters.branchId,
          environment: filters.environment,
          status: filters.status,
          created_from: filters.created_from,
          created_to: filters.created_to,
          q: filters.q,
          field: filters.field,
          include_archived: filters.include_archived ? "true" : undefined,
          sort: filters.sort,
        },
      });
    },
  },

  media: {
    limits(signal?: AbortSignal): Promise<FileLimits> {
      return request<FileLimits>(`${ADMIN_V2_ROOT}/files/limits`, { signal });
    },
    references(id: string, signal?: AbortSignal): Promise<{items:FileReference[];revision:string;externalReferencesDiscoverable:boolean}> {
      return request(`${ADMIN_V2_ROOT}/files/objects/${encoded(id)}/references`, {signal});
    },
    createUpload(input: {name:string;size:number;sha256:string;visibility:FileVisibility}): Promise<FileUploadSession> {
      return request(`${ADMIN_V2_ROOT}/files/uploads`,{method:"POST",body:input});
    },
    uploadStatus(id:string): Promise<FileUploadSession> {
      return request(`${ADMIN_V2_ROOT}/files/uploads/${encoded(id)}`);
    },
    commitUpload(id:string): Promise<{id:string}> {
      return request(`${ADMIN_V2_ROOT}/files/uploads/${encoded(id)}/commit`,{method:"POST"});
    },
    abortUpload(id:string): Promise<void> {
      return request(`${ADMIN_V2_ROOT}/files/uploads/${encoded(id)}`,{method:"DELETE"});
    },
    createLink(id:string,ttlSeconds:number):Promise<{url:string;expiresAt:string}> {
      return request(`${ADMIN_V2_ROOT}/files/objects/${encoded(id)}/links`,{method:"POST",body:{ttlSeconds}});
    },

    list(params: MediaListParams = {}, signal?: AbortSignal): Promise<ListResponse<Media>> {
      return requestList<Media>(`${ADMIN_ROOT}/media`, {
        signal,
        query: {
          status: params.status,
          type: params.type,
          q: params.q,
          created_from: params.created_from,
          created_to: params.created_to,
          min_size: params.min_size,
          max_size: params.max_size,
          sort: params.sort,
          limit: params.limit,
          cursor: params.cursor,
        },
      });
    },
    get(id: string, signal?: AbortSignal): Promise<Media> {
      return request<Media>(`${ADMIN_ROOT}/media/${encoded(id)}`, { signal });
    },
    rename(id: string, filename: string): Promise<Media> {
      return request<Media>(`${ADMIN_ROOT}/media/${encoded(id)}`, {
        method: "PATCH",
        body: { filename },
      });
    },
    prepareRenditions(id: string, context: MediaRenditionContext): Promise<MediaRenditionState> {
      return request<MediaRenditionState>(
        `${ADMIN_V2_ROOT}/media/${encoded(id)}/renditions/prepare`,
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
    renditionState(
      id: string,
      context: MediaRenditionContext,
      signal?: AbortSignal,
    ): Promise<MediaRenditionState> {
      return request<MediaRenditionState>(
        `${ADMIN_V2_ROOT}/media/${encoded(id)}/renditions/state`,
        {
          signal,
          query: {
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
    retry(id: string): Promise<Media> {
      return request<Media>(`${ADMIN_ROOT}/media/${encoded(id)}/retry`, {
        method: "POST",
      });
    },
    remove(id: string, confirm = false, referencesRevision?: string): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/media/${encoded(id)}`, {
        method: "DELETE", query: { confirm: confirm ? "true" : undefined, referencesRevision },
      });
    },
  },

  settings: {
    async get(signal?: AbortSignal): Promise<Settings> {
      const settings = await request<Settings>(`${ADMIN_ROOT}/settings`, { signal });
      return {
        ...settings,
        public_api: {
          ...settings?.public_api,
          cors_origins: arrayOrEmpty(settings?.public_api?.cors_origins),
        },
        media: {
          ...settings?.media,
          accepted_types: arrayOrEmpty(settings?.media?.accepted_types),
          image_variants: arrayOrEmpty(settings?.media?.image_variants),
        },
        roles: arrayOrEmpty(settings?.roles)
          .filter((role) => role && typeof role === "object")
          .map((role) => ({
            ...role,
            notes: arrayOrEmpty(role.notes),
            permissions: arrayOrEmpty(role.permissions)
              .filter((permission) => permission && typeof permission === "object")
              .map((permission) => ({
                ...permission,
                actions: arrayOrEmpty(permission.actions),
              })),
          })),
      };
    },
  },

  environments: {
    list(signal?: AbortSignal): Promise<{ items: SiteEnvironment[] }> {
      return requestItems<SiteEnvironment>(`${ADMIN_ROOT}/site/environments`, { signal });
    },
    update(key: Environment, patch: EnvironmentPatch): Promise<SiteEnvironment> {
      return request<SiteEnvironment>(`${ADMIN_ROOT}/site/environments/${encoded(key)}`, {
        method: "PATCH",
        body: patch,
      });
    },
  },

  siteBuilder: {
    importSitepack(file: File, signal?: AbortSignal): Promise<SitepackImportReport> {
      // The server identifies the archive by bytes and validates every entry;
      // browser-specific file MIME labels do not change the API contract.
      const body = new Blob([file], { type: "application/zip" });
      return request<SitepackImportReport>(`${ADMIN_V2_ROOT}/site/snapshots/import`, { method: "POST", body, signal });
    },
    async exportSitepack(snapshotId: string, destination?: WritableStream<Uint8Array>, signal?: AbortSignal): Promise<{ blob?: Blob; filename: string }> {
      const limits = destination ? undefined : await request<SitepackLimits>(`${ADMIN_V2_ROOT}/sitepack/limits`, { signal });
      let response: Response;
      try {
        response = await rawRequest(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(snapshotId)}/export`, { signal });
        if (!response.ok) throw await parseErrorResponse(response);
        if (!response.body) throw new ApiError(0, "missing_stream", "Браузер не поддерживает потоковое скачивание.");
      } catch (error) {
        if (destination) await destination.abort(error);
        throw error;
      }
      const filename = filenameFromDisposition(response.headers.get("Content-Disposition"), `snapshot-${snapshotId}.sitepack`);
      if (destination) {
        // The browser writes each chunk directly to the chosen file. pipeTo
        // closes it only after a complete response and aborts it on failure.
        await response.body.pipeTo(destination, { signal });
        return { filename };
      }
      const limit = limits?.browserFallbackBytes ?? 0;
      if (!Number.isSafeInteger(limit) || limit <= 0) throw new ApiError(0, "invalid_download_limit", "Не удалось получить допустимый размер скачивания.");
      const reader = response.body.getReader();
      const chunks: Uint8Array[] = [];
      let size = 0;
      try {
        for (;;) {
          const next = await reader.read();
          if (next.done) break;
          size += next.value.byteLength;
          if (size > limit) {
            await reader.cancel();
            throw new ApiError(0, "download_too_large", `Архив превышает лимит скачивания в этом браузере (${Math.floor(limit / 1048576)} МиБ). Используйте браузер с потоковым сохранением, например Chrome или Edge.`);
          }
          chunks.push(next.value);
        }
      } finally {
        reader.releaseLock();
      }
      return { blob: new Blob(chunks, { type: "application/zip" }), filename };
    },
    branches(signal?: AbortSignal): Promise<{items: BranchRecord[]}> {return requestItems<BranchRecord>(`${ADMIN_V2_ROOT}/branches`,{signal});},
    assignBranch(id: string,snapshotId: string,revision: number,contentRevision: number): Promise<BranchRecord> {return request<BranchRecord>(`${ADMIN_V2_ROOT}/branches/${encoded(id)}/snapshot`,{method:"PUT",body:{snapshotId,revision,contentRevision}});},
    snapshot(id: string,signal?: AbortSignal): Promise<SnapshotDetail> {return request<SnapshotDetail>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(id)}`,{signal});},
    createSnapshotFrom(input: {name: string;parentSnapshotId?: string;expectedParentContentRevision?: number;config?: SiteConfig;metadata?: SiteMetadata;designSystem?: StructuredDesignSystem}): Promise<SiteSnapshot> {return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/site/snapshots`,{method:"POST",body:input});},
    deleteSnapshot(id: string): Promise<void> {return request<void>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(id)}`,{method:"DELETE"});},

    async contentSnapshots(signal?: AbortSignal): Promise<{ items: SiteSnapshot[] }> {
      const items: SiteSnapshot[] = [];
      const seen = new Set<number>();
      let cursor: number | undefined;
      do {
        const page = await request<{ items?: SiteSnapshot[]; nextCursor?: number | null }>(`${ADMIN_V2_ROOT}/content/snapshots`, { signal, query: { beforeSequence: cursor } });
        items.push(...arrayOrEmpty(page.items));
        if (page.nextCursor == null) break;
        if (seen.has(page.nextCursor)) throw new Error("Сервер вернул повторный указатель списка снапшотов");
        seen.add(page.nextCursor);
        cursor = page.nextCursor;
      } while (cursor !== undefined);
      return { items };
    },
    async modelContentWorkspace(snapshotId: string, signal?: AbortSignal): Promise<ModelContentWorkspace> {
      const workspace = await request<ModelContentWorkspace>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/model`, { signal });
      return normalizeModelContentWorkspace(workspace);
    },
    saveModelContentWorkspace(
      snapshotId: string,
      revision: number,
      records: ModelContentRecordInput[],
      selections: ModelContentSelection[],
    ): Promise<ModelContentWorkspace> {
      return request<ModelContentWorkspace>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/model`, {
        method: "PUT",
        body: { revision, records, selections },
      }).then(normalizeModelContentWorkspace);
    },
    publishModelContent(snapshotId: string, revision: number): Promise<ModelContentWorkspace> {
      return request<ModelContentWorkspace>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/model/publish`, {
        method: "POST",
        body: { revision },
      }).then(normalizeModelContentWorkspace);
    },
    modelContentHistory(snapshotId: string, signal?: AbortSignal): Promise<ModelContentHistoryResponse> {
      return request<ModelContentHistoryResponse>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/model/history`, { signal });
    },
    restoreModelContent(snapshotId: string, publicationId: string, revision: number): Promise<ModelContentWorkspace> {
      return request<ModelContentWorkspace>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/model/history/${encoded(publicationId)}/restore`, {
        method: "POST",
        body: { revision },
      }).then(normalizeModelContentWorkspace);
    },
    featureBuilds(signal?: AbortSignal): Promise<{ items: FeatureBuild[] }> {
      return requestItems<FeatureBuild>(`${ADMIN_V2_ROOT}/feature-builds`, { signal });
    },
    designSnapshot(snapshotId: string, signal?: AbortSignal): Promise<SnapshotDesignWorkspace> {
      return request<SnapshotDesignWorkspace>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(snapshotId)}/design-system`, { signal });
    },
    saveDesignSnapshot(snapshotId: string, designSystem: StructuredDesignSystem, name: string): Promise<SiteSnapshot> {
      return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(snapshotId)}/design-system`, { method: "POST", body: { name, designSystem } });
    },
    metadata(signal?: AbortSignal): Promise<SiteMetadataState> {
      return request<SiteMetadataState>(`${ADMIN_V2_ROOT}/site/metadata`, { signal });
    },
    saveMetadata(revision: number, metadata: SiteMetadata): Promise<SiteMetadataState> {
      return request<SiteMetadataState>(`${ADMIN_V2_ROOT}/site/metadata`, {
        method: "PUT",
        body: { revision, metadata },
      });
    },
    publishMetadata(): Promise<SiteMetadataState> {
      return request<SiteMetadataState>(`${ADMIN_V2_ROOT}/site/metadata/publish`, { method: "POST", body: {} });
    },
    promoteMetadata(): Promise<SiteMetadataState> {
      return request<SiteMetadataState>(`${ADMIN_V2_ROOT}/site/metadata/promote`, { method: "POST", body: {} });
    },
    designSystem(signal?: AbortSignal): Promise<{ state: DesignSystemState; definitions: DesignTokenDefinition[] }> {
      return request(`${ADMIN_V2_ROOT}/site/design-system`, { signal });
    },
    saveDesignSystem(revision: number, designSystem: DesignSystem): Promise<{ state: DesignSystemState; definitions: DesignTokenDefinition[] }> {
      return request(`${ADMIN_V2_ROOT}/site/design-system`, { method: "PUT", body: { revision, designSystem } });
    },
    previewDesignSystem(designSystem: DesignSystem): Promise<{ token: string }> {
      return request(`${ADMIN_V2_ROOT}/site/design-system/preview`, { method: "POST", body: { designSystem } });
    },
    async snapshots(beforeSequence?: number, signal?: AbortSignal): Promise<SiteSnapshotList> {
      const snapshots = await request<SiteSnapshotList>(`${ADMIN_V2_ROOT}/site/snapshots`, {
        signal,
        query: { beforeSequence },
      });
      return {
        ...snapshots,
        items: arrayOrEmpty(snapshots?.items),
        active: snapshots?.active ?? { dev: "", prod: "" },
        activeSnapshots: snapshots?.activeSnapshots ?? { dev: null, prod: null },
        nextCursor: snapshots?.nextCursor ?? undefined,
      };
    },
    async snapshotEvents(branchId: string, before?: string, signal?: AbortSignal): Promise<{ items: SnapshotEvent[]; nextCursor?: string }> {
      const result = await request<{ items: SnapshotEvent[]; nextCursor?: string | null }>(`${ADMIN_V2_ROOT}/site/snapshot-events`, {
        signal, query: { branchId, before },
      });
      return { items: arrayOrEmpty(result.items), nextCursor: result.nextCursor ?? undefined };
    },
    createSnapshot(name: string): Promise<SiteSnapshot> {
      return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/site/snapshots`, {
        method: "POST",
        body: { name },
      });
    },
    deploySnapshot(id: string, environment: Environment): Promise<SiteSnapshot> {
      return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(id)}/deploy`, {
        method: "POST",
        body: { environment },
      });
    },
    promote(id: string): Promise<SiteSnapshot> {
      return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(id)}/promote`, {
        method: "POST",
        body: {},
      });
    },
    rollback(id: string, environment: Environment): Promise<SiteSnapshot> {
      return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/site/snapshots/${encoded(id)}/rollback`, {
        method: "POST",
        body: { environment },
      });
    },
    async importFeatureBuild(file: File): Promise<FeatureBuild> {
      const headers: Record<string, string> = {
        Accept: "application/json",
        "Content-Type": "application/zip",
        ...authHeaders(),
      };
      let response: Response;
      try {
        response = await fetch(`${ADMIN_V2_ROOT}/feature-builds`, {
          method: "POST",
          headers,
          body: file,
        });
      } catch {
        throw new ApiError(0, "network_error", "Не удалось загрузить сборку фичи");
      }
      if (!response.ok) throw await parseErrorResponse(response);
      return response.json() as Promise<FeatureBuild>;
    },
  },

};

export function publicMediaPath(id: string, variant?: string): string {
  const suffix = variant ? `?variant=${encodeURIComponent(variant)}` : "";
  return `/media/${id}${suffix}`;
}

export function absolutePublicMediaUrl(id: string, variant?: string): string {
  return `${window.location.origin}${publicMediaPath(id, variant)}`;
}

// Browser uploads use the same resumable owner-scoped API as server clients.
// The worker hashes 1 MiB blocks and never reads the whole file into memory.
type HashResult = {sha256:string;parts:string[]};
type PendingUpload = {id:string;sha256:string;partBytes:number;expiresAt:string};
function pendingKey(file:File,visibility:FileVisibility):string {
  return `we:upload:${encodeURIComponent(file.name)}:${file.size}:${file.lastModified}:${visibility}`;
}
function savedUpload(key:string):PendingUpload|null {
  try { const value=JSON.parse(localStorage.getItem(key) ?? "null") as PendingUpload|null;
    return value && typeof value.id==="string" && typeof value.sha256==="string" && typeof value.partBytes==="number" && typeof value.expiresAt==="string" ? value : null;
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
    const xhr=new XMLHttpRequest();xhr.open("PUT",buildUrl(`${ADMIN_V2_ROOT}/files/uploads/${encoded(id)}/parts/${number}`));
    xhr.responseType="json";xhr.setRequestHeader("Content-Type","application/octet-stream");xhr.setRequestHeader("X-Content-SHA256",sha256);
    const token=getStoredToken();if(token)xhr.setRequestHeader("Authorization",`Bearer ${token}`);
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
export async function uploadMediaFile(file:File,onProgress:(percent:number|null)=>void,signal?:AbortSignal,visibility:FileVisibility="public"):Promise<Media> {
  const limits=await api.media.limits(signal);
  if(file.size<1||file.size>limits.maxBytes)throw new Error("Размер файла превышает разрешённый сервером или файл пуст");
  const key=pendingKey(file,visibility);const saved=savedUpload(key);
  const partBytes=saved && Date.parse(saved.expiresAt)>Date.now() ? saved.partBytes : limits.partBytes;
  if(!Number.isSafeInteger(partBytes)||partBytes<1||partBytes>limits.maxBytes)throw new Error("Сервер вернул некорректный размер части");
  let session:FileUploadSession|undefined;
  try {
    onProgress(0);const hashes=await hashFile(file,partBytes,fraction=>onProgress(Math.floor(fraction*20)),signal);
    if(saved&&saved.sha256===hashes.sha256&&saved.partBytes===partBytes&&Date.parse(saved.expiresAt)>Date.now()){
      try {session=await api.media.uploadStatus(saved.id);}catch(error){if(!(error instanceof ApiError)||![404,410].includes(error.status))throw error;rememberUpload(key,null);}
      if(session&&(session.sha256!==hashes.sha256||session.size!==file.size||session.visibility!==visibility||session.partSize!==partBytes||session.state==="cancelled"))session=undefined;
    }
    if(!session)session=await api.media.createUpload({name:file.name,size:file.size,sha256:hashes.sha256,visibility});
    rememberUpload(key,{id:session.id,sha256:hashes.sha256,partBytes:session.partSize,expiresAt:session.expiresAt});
    if(session.state!=="complete") {
      const completed=new Map(session.parts.filter(part=>part.ready).map(part=>[part.number,part]));
      let transferred=0;
      for(let index=0;index<hashes.parts.length;index++){
        if(signal?.aborted)throw new DOMException("Aborted","AbortError");
        const start=index*partBytes,end=Math.min(file.size,start+partBytes),expected=hashes.parts[index]!;
        const existing=completed.get(index+1);
        if(existing && (existing.sha256!==expected||existing.size!==end-start))throw new Error("Сохранённая часть не совпадает с файлом. Отмените загрузку и начните заново.");
        if(!existing) {
          const refreshed = await uploadPart(session.id,index+1,file.slice(start,end),expected,fraction=>onProgress(20+Math.round(75*(transferred+fraction*(end-start))/file.size)),signal);
          rememberUpload(key,{id:session.id,sha256:hashes.sha256,partBytes:session.partSize,expiresAt:refreshed.expiresAt});
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

export const submissionStatusOptions: SubmissionStatus[] = [
  "new",
  "in_progress",
  "processed",
  "rejected",
];

export const mediaStatusValues: MediaStatus[] = [
  "processing",
  "ready",
  "failed",
  "deleted",
];
