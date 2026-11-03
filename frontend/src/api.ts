import type {
  ApiErrorDetail,
  ApiErrorPayload,
  CreateTokenInput,
  CreatedToken,
  ContentFeature,
  DesignSystem,
  DesignSystemState,
  DesignTokenDefinition,
  EntryDetail,
  EntryListParams,
  EntryVersion,
  Environment,
  EnvironmentPatch,
  FeatureBuild,
  Feature,
  FeatureDetail,
  FeatureDraftState,
  FeatureListParams,
  FeatureVersion,
  JsonObject,
  ListResponse,
  Media,
  MediaListParams,
  MediaRenditionContext,
  MediaRenditionState,
  MediaStatus,
  MediaUsage,
  Overview,
  Principal,
  Settings,
  SiteEnvironment,
  SiteConfig,
  SiteDraftState,
  SiteMetadata,
  SiteMetadataState,
  SiteSnapshot,
  SiteSnapshotList,
  SnapshotEvent,
  SnapshotContentDraftInput,
  SnapshotContentWorkspace,
  Submission,
  SubmissionFilters,
  SubmissionListResponse,
  SubmissionStatus,
  Token,
} from "./types";

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

function normalizeSiteDraft(draft: SiteDraftState): SiteDraftState {
  const config = draft?.config;
  const legacyTree = normalizeRouteTree(config?.routeTree);
  const routeTree = normalizeSiteRouteTree(legacyTree);
  return {
    ...draft,
    config: {
      ...config,
      formatVersion: 3,
      pages: arrayOrEmpty(config?.pages).map((page) => ({
        ...page,
        placements: arrayOrEmpty(page?.placements),
      })),
      routeTree,
    },
  };
}

function normalizeRouteTree(node: SiteConfig["routeTree"] | null | undefined): SiteConfig["routeTree"] {
  if (!node || typeof node !== "object") {
    return { id: "site", type: "site", children: [{ id: "home-route", type: "path", path: "/", pageId: "home", children: [] }] };
  }
  return {
    ...node,
    children: arrayOrEmpty(node.children).map((child) => normalizeRouteTree(child)),
  };
}

function normalizeSiteRouteTree(tree: SiteConfig["routeTree"]): SiteConfig["routeTree"] {
  if (tree.type === "site") return tree;
  if (tree.type !== "index") return { id: "site", type: "site", children: [toRootPathNode(tree)] };

  // v2 treated / as a visible parent for every top-level URL segment. Those
  // segments are independent route roots in the canvas, but compile to exactly
  // the same URLs when promoted to siblings of the virtual site root.
  const roots = [
    ...(tree.pageId ? [{ id: tree.id, type: "path" as const, path: "/", pageId: tree.pageId, children: [] }] : []),
    ...(tree.children ?? []).map(toRootPathNode),
  ];
  return { id: "site", type: "site", children: roots };
}

function toRootPathNode(node: SiteConfig["routeTree"]): SiteConfig["routeTree"] {
  if (node.type === "path") {
    return { id: node.id, type: "path", path: node.path ?? "/", ...(node.pageId ? { pageId: node.pageId } : {}), children: arrayOrEmpty(node.children) };
  }
  return { id: node.id, type: "path", path: legacyRootSegment(node), ...(node.pageId ? { pageId: node.pageId } : {}), children: arrayOrEmpty(node.children) };
}

function legacyRootSegment(node: SiteConfig["routeTree"]): string {
  if (node.type === "path") return node.path ?? "/";
  if (node.type === "static") return `/${node.value ?? ""}`;
  if (node.type === "param") return `/:${node.name ?? "param"}`;
  if (node.type === "regex") return `/~${node.pattern ?? ""}`;
  if (node.type === "catchAll") return `/*${node.name ?? "path"}`;
  return "/";
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
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(options.body);
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
          draft_features: arrayOrEmpty(overview?.attention?.draft_features),
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
    get(id: string, signal?: AbortSignal): Promise<FeatureDetail> {
      return request<FeatureDetail>(`${ADMIN_ROOT}/features/${encoded(id)}`, {
        signal,
      });
    },
    updateName(id: string, name: string): Promise<Feature> {
      return request<Feature>(`${ADMIN_ROOT}/features/${encoded(id)}`, {
        method: "PATCH",
        body: { name },
      });
    },
    archive(id: string): Promise<Feature> {
      return request<Feature>(`${ADMIN_ROOT}/features/${encoded(id)}/archive`, {
        method: "POST",
      });
    },
    versions(id: string, signal?: AbortSignal): Promise<{ items: FeatureVersion[] }> {
      return requestItems<FeatureVersion>(
        `${ADMIN_ROOT}/features/${encoded(id)}/versions`,
        { signal },
      );
    },
    draft(id: string, signal?: AbortSignal): Promise<FeatureDraftState> {
      return request<FeatureDraftState>(
        `${ADMIN_ROOT}/features/${encoded(id)}/draft`,
        { signal },
      );
    },
    saveDraft(
      id: string,
      revision: number,
      schema: JsonObject,
    ): Promise<FeatureDraftState> {
      return request<FeatureDraftState>(
        `${ADMIN_ROOT}/features/${encoded(id)}/draft`,
        { method: "PUT", body: { revision, schema } },
      );
    },
    publish(id: string, revision: number): Promise<FeatureDetail> {
      return request<FeatureDetail>(
        `${ADMIN_ROOT}/features/${encoded(id)}/publish`,
        { method: "POST", body: { revision } },
      );
    },
    promote(id: string, revision: number): Promise<FeatureDetail> {
      return request<FeatureDetail>(
        `${ADMIN_ROOT}/features/${encoded(id)}/promote`,
        { method: "POST", body: { revision } },
      );
    },
    rollback(id: string, version: number, environment: Environment): Promise<FeatureDetail> {
      return request<FeatureDetail>(
        `${ADMIN_ROOT}/features/${encoded(id)}/rollback`,
        { method: "POST", body: { version, environment } },
      );
    },
  },

  entries: {
    list(
      featureId: string,
      params: EntryListParams = {},
      signal?: AbortSignal,
    ): Promise<ListResponse<EntryDetail>> {
      return requestList<EntryDetail>(
        `${ADMIN_ROOT}/features/${encoded(featureId)}/entries`,
        {
          signal,
          query: {
            include_archived: params.include_archived ? "true" : undefined,
            q: params.q,
            limit: params.limit,
            cursor: params.cursor,
            sort: params.sort,
          },
        },
      );
    },
    create(featureId: string, data: JsonObject): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/features/${encoded(featureId)}/entries`,
        { method: "POST", body: { data } },
      );
    },
    getSettings(featureId: string, signal?: AbortSignal): Promise<EntryDetail> {
      return request<EntryDetail>(`/api/v2/admin/features/${encoded(featureId)}/settings`, { signal });
    },
    createSettings(featureId: string, data: JsonObject): Promise<EntryDetail> {
      return request<EntryDetail>(`/api/v2/admin/features/${encoded(featureId)}/settings`, {
        method: "POST",
        body: { data },
      });
    },
    move(featureId: string, entryId: string, direction: "up" | "down"): Promise<void> {
      return request<void>(`/api/v2/admin/features/${encoded(featureId)}/entries/${encoded(entryId)}/move`, {
        method: "PUT",
        body: { direction },
      });
    },
    import(
      featureId: string,
      data: JsonObject | JsonObject[],
      entryId?: string,
      revision?: number,
    ): Promise<{ items: EntryDetail[]; imported: number }> {
      return request<{ items: EntryDetail[] | null; imported: number }>(
        `/api/v2/admin/features/${encoded(featureId)}/entries/import`,
        { method: "POST", body: { data, entryId, revision } },
      ).then((result) => ({ ...result, items: arrayOrEmpty(result?.items) }));
    },
    get(id: string, signal?: AbortSignal): Promise<EntryDetail> {
      return request<EntryDetail>(`${ADMIN_ROOT}/entries/${encoded(id)}`, {
        signal,
      });
    },
    versions(id: string, signal?: AbortSignal): Promise<{ items: EntryVersion[] }> {
      return requestItems<EntryVersion>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/versions`,
        { signal },
      );
    },
    saveDraft(id: string, revision: number, data: JsonObject): Promise<EntryDetail> {
      return request<EntryDetail>(`${ADMIN_ROOT}/entries/${encoded(id)}/draft`, {
        method: "PATCH",
        body: { revision, data },
      });
    },
    publish(id: string, revision: number): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/publish`,
        { method: "POST", body: { revision } },
      );
    },
    promote(id: string, revision: number): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/promote`,
        { method: "POST", body: { revision } },
      );
    },
    rollback(id: string, version: number, environment: Environment): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/rollback`,
        { method: "POST", body: { version, environment } },
      );
    },
    archive(id: string): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/archive`,
        { method: "POST" },
      );
    },
    unpublish(id: string, environment: Environment): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/unpublish`,
        { method: "POST", body: { environment } },
      );
    },
    discardDraft(id: string): Promise<EntryDetail> {
      return request<EntryDetail>(
        `${ADMIN_ROOT}/entries/${encoded(id)}/discard-draft`,
        { method: "POST" },
      );
    },
  },

  forms: {
    submissions(
      key: string,
      filters: SubmissionFilters,
      cursor?: string | null,
      limit = 50,
      signal?: AbortSignal,
    ): Promise<SubmissionListResponse> {
      return request<SubmissionListResponse | null>(
        `${ADMIN_ROOT}/forms/${encoded(key)}/submissions`,
        {
          signal,
          query: {
            limit,
            cursor,
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
      return requestBlob(`${ADMIN_ROOT}/forms/${encoded(key)}/submissions/export`, {
        query: {
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
    usage(id: string, signal?: AbortSignal): Promise<{ items: MediaUsage[] }> {
      return requestItems<MediaUsage>(
        `${ADMIN_ROOT}/media/${encoded(id)}/usage`,
        { signal },
      );
    },
    prepareRenditions(id: string, context: MediaRenditionContext): Promise<MediaRenditionState> {
      return request<MediaRenditionState>(
        `${ADMIN_V2_ROOT}/media/${encoded(id)}/renditions/prepare`,
        { method: "POST", body: context },
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
    remove(id: string): Promise<void> {
      return request<void>(`${ADMIN_ROOT}/media/${encoded(id)}`, {
        method: "DELETE",
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
    contentFeatures(signal?: AbortSignal): Promise<{ items: ContentFeature[] }> {
      return requestItems<ContentFeature>(`${ADMIN_V2_ROOT}/content/features`, { signal });
    },
    contentSnapshots(signal?: AbortSignal): Promise<{ items: SiteSnapshot[] }> {
      return requestItems<SiteSnapshot>(`${ADMIN_V2_ROOT}/content/snapshots`, { signal });
    },
    snapshotContentFeatures(snapshotId: string, signal?: AbortSignal): Promise<{ items: ContentFeature[] }> {
      return requestItems<ContentFeature>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/features`, { signal });
    },
    snapshotContentWorkspace(snapshotId: string, featureKey: string, signal?: AbortSignal): Promise<SnapshotContentWorkspace> {
      return request<SnapshotContentWorkspace>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/features/${encoded(featureKey)}`, { signal });
    },
    saveSnapshotContentWorkspace(snapshotId: string, featureKey: string, revision: number, items: SnapshotContentDraftInput[]): Promise<SnapshotContentWorkspace> {
      return request<SnapshotContentWorkspace>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/features/${encoded(featureKey)}`, {
        method: "PUT",
        body: { revision, items },
      });
    },
    forkSnapshotFromContent(snapshotId: string, name = ""): Promise<SiteSnapshot> {
      return request<SiteSnapshot>(`${ADMIN_V2_ROOT}/content/snapshots/${encoded(snapshotId)}/fork`, {
        method: "POST",
        body: { name },
      });
    },
    featureBuilds(signal?: AbortSignal): Promise<{ items: FeatureBuild[] }> {
      return requestItems<FeatureBuild>(`${ADMIN_V2_ROOT}/feature-builds`, { signal });
    },
    async draft(signal?: AbortSignal): Promise<SiteDraftState> {
      const draft = await request<SiteDraftState>(`${ADMIN_V2_ROOT}/site/draft`, { signal });
      return normalizeSiteDraft(draft);
    },
    async saveDraft(revision: number, config: SiteConfig): Promise<SiteDraftState> {
      const draft = await request<SiteDraftState>(`${ADMIN_V2_ROOT}/site/draft`, {
        method: "PUT",
        body: { revision, config },
      });
      return normalizeSiteDraft(draft);
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
    async snapshotEvents(environment: Environment, before?: string, signal?: AbortSignal): Promise<{ items: SnapshotEvent[]; nextCursor?: string }> {
      const result = await request<{ items: SnapshotEvent[]; nextCursor?: string | null }>(`${ADMIN_V2_ROOT}/site/snapshot-events`, {
        signal, query: { environment, before },
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

/**
 * Uploads multipart/form-data with progress reporting. fetch() cannot report
 * upload progress, so this one call intentionally uses XMLHttpRequest.
 */
export function uploadMediaFile(
  file: File,
  onProgress: (percent: number | null) => void,
  signal?: AbortSignal,
): Promise<Media> {
  return new Promise<Media>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", buildUrl(`${ADMIN_ROOT}/media`));
    xhr.responseType = "json";
    xhr.setRequestHeader("Accept", "application/json");
    const token = getStoredToken();
    if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);

    const onAbort = () => xhr.abort();
    if (signal) {
      if (signal.aborted) {
        reject(new DOMException("Aborted", "AbortError"));
        return;
      }
      signal.addEventListener("abort", onAbort, { once: true });
    }

    const cleanup = () => {
      if (signal) signal.removeEventListener("abort", onAbort);
    };

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) {
        onProgress(Math.min(100, Math.round((event.loaded / event.total) * 100)));
      } else {
        onProgress(null);
      }
    };
    xhr.upload.onload = () => onProgress(100);

    xhr.onerror = () => {
      cleanup();
      reject(
        new ApiError(
          0,
          "network_error",
          "Не удалось загрузить файл. Проверьте подключение.",
        ),
      );
    };
    xhr.onabort = () => {
      cleanup();
      reject(new DOMException("Aborted", "AbortError"));
    };
    xhr.onload = () => {
      cleanup();
      if (xhr.status === 401) {
        notifyUnauthorized("Сессия истекла. Войдите снова.");
      }
      const payload = xhr.response as
        | Media
        | { error?: ApiErrorPayload }
        | null;
      if (xhr.status >= 200 && xhr.status < 300 && payload) {
        resolve(payload as Media);
        return;
      }
      const envelope =
        payload && typeof payload === "object" && "error" in payload
          ? payload.error
          : undefined;
      reject(
        new ApiError(
          xhr.status,
          envelope?.code ?? "upload_failed",
          envelope?.message ??
            (xhr.status === 413
              ? "Файл превышает допустимый размер"
              : "Не удалось загрузить файл"),
          envelope?.details ?? [],
          envelope?.request_id,
        ),
      );
    };

    const form = new FormData();
    form.append("file", file, file.name);
    xhr.send(form);
  });
}

export const MEDIA_ACCEPTED_MIME = [
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
  "image/svg+xml",
  "application/pdf",
  "video/mp4",
  "font/woff2",
] as const;

export const MEDIA_MAX_BYTES = 20 * 1024 * 1024;

export function isUploadAccepted(file: File): boolean {
  const type = file.type.toLowerCase();
  if ((MEDIA_ACCEPTED_MIME as readonly string[]).includes(type)) return true;
  const ext = file.name.slice(file.name.lastIndexOf(".")).toLowerCase();
  return [".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".pdf", ".mp4", ".woff2"].includes(ext);
}

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
