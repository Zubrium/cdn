import type { MediaSort, MediaStatus } from "@/types";

export type MediaGalleryType = "all" | "image" | "video" | "pdf";
export type MediaGalleryStatus = "all" | Exclude<MediaStatus, "deleted">;
export type MediaGalleryView = "grid" | "list";
export type MediaGalleryResultsState =
  | "invalid-range"
  | "loading"
  | "error"
  | "empty-filtered"
  | "empty"
  | "results"
  | "results-with-error";

export type MediaGalleryFilters = {
  q: string;
  type: MediaGalleryType;
  status: MediaGalleryStatus;
  sort: MediaSort;
  from: string;
  to: string;
  min: string;
  max: string;
  view: MediaGalleryView;
};

export type MediaGalleryParam = keyof MediaGalleryFilters;
export type MediaGalleryParamUpdates = Partial<
  Record<MediaGalleryParam, string | null>
>;

export const DEFAULT_MEDIA_GALLERY_FILTERS: MediaGalleryFilters = {
  q: "",
  type: "all",
  status: "all",
  sort: "new",
  from: "",
  to: "",
  min: "",
  max: "",
  view: "grid",
};

const mediaTypes = new Set<MediaGalleryType>(["all", "image", "video", "pdf"]);
const mediaStatuses = new Set<MediaGalleryStatus>([
  "all",
  "ready",
  "processing",
  "failed",
]);
const mediaSorts = new Set<MediaSort>([
  "new",
  "old",
  "name",
  "size_desc",
  "size_asc",
]);
const mediaViews = new Set<MediaGalleryView>(["grid", "list"]);

function asMember<T extends string>(value: string | null, values: Set<T>, fallback: T): T {
  return value && values.has(value as T) ? (value as T) : fallback;
}

function validCalendarDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const parsed = new Date(`${value}T00:00:00.000Z`);
  return Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value;
}

function validMegabytes(value: string): boolean {
  const normalized = value.trim().replace(",", ".");
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(normalized)) return false;
  const numeric = Number(normalized);
  return Number.isFinite(numeric) && Number.isSafeInteger(Math.round(numeric * 1024 * 1024));
}

export function megabytesToBytes(value: string): number | undefined {
  if (!validMegabytes(value)) return undefined;
  return Math.round(Number(value.trim().replace(",", ".")) * 1024 * 1024);
}

export function parseMediaGalleryFilters(params: URLSearchParams): MediaGalleryFilters {
  const date = (key: "from" | "to") => {
    const value = params.get(key)?.trim() ?? "";
    return validCalendarDate(value) ? value : "";
  };
  const size = (key: "min" | "max") => {
    const value = params.get(key)?.trim() ?? "";
    if (value === "" || !validMegabytes(value)) return "";
    return value.replace(",", ".");
  };

  return {
    q: params.get("q")?.trim() ?? "",
    type: asMember(params.get("type"), mediaTypes, "all"),
    status: asMember(params.get("status"), mediaStatuses, "all"),
    sort: asMember(params.get("sort"), mediaSorts, "new"),
    from: date("from"),
    to: date("to"),
    min: size("min"),
    max: size("max"),
    view: asMember(params.get("view"), mediaViews, "grid"),
  };
}

const defaults: Record<MediaGalleryParam, string> = {
  ...DEFAULT_MEDIA_GALLERY_FILTERS,
};

export function mergeMediaGalleryParams(
  current: URLSearchParams,
  updates: MediaGalleryParamUpdates,
): URLSearchParams {
  const next = new URLSearchParams(current);
  for (const [rawKey, value] of Object.entries(updates)) {
    const key = rawKey as MediaGalleryParam;
    if (!(key in defaults)) continue;
    if (value === null || value === "" || value === defaults[key]) next.delete(key);
    else next.set(key, value);
  }
  return next;
}

/** Removes invalid or non-canonical gallery parameters without touching other routes' query state. */
export function normalizeMediaGalleryParams(current: URLSearchParams): URLSearchParams {
  const filters = parseMediaGalleryFilters(current);
  return mergeMediaGalleryParams(current, filters);
}

export function mediaGalleryRangeError(filters: Pick<MediaGalleryFilters, "from" | "to" | "min" | "max">): string | null {
  if (filters.from && filters.to && filters.from > filters.to) {
    return "Дата начала не может быть позже даты окончания.";
  }
  const min = megabytesToBytes(filters.min);
  const max = megabytesToBytes(filters.max);
  if (min !== undefined && max !== undefined && min > max) {
    return "Минимальный размер не может превышать максимальный.";
  }
  return null;
}

export function hasActiveMediaGalleryFilters(filters: MediaGalleryFilters): boolean {
  return Boolean(filters.q || filters.from || filters.to || filters.min || filters.max)
    || filters.type !== "all"
    || filters.status !== "all"
    || filters.sort !== "new";
}

export function mediaGalleryResultsState(input: {
  invalidRange: boolean;
  loading: boolean;
  error: boolean;
  itemCount: number;
  filtersActive: boolean;
}): MediaGalleryResultsState {
  if (input.invalidRange) return "invalid-range";
  if (input.loading) return "loading";
  if (input.error && input.itemCount === 0) return "error";
  if (input.itemCount === 0) return input.filtersActive ? "empty-filtered" : "empty";
  return input.error ? "results-with-error" : "results";
}
