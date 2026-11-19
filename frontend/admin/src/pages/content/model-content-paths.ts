export function contentHref(path: string, snapshotId: string): string {
  if (!snapshotId) return path;
  const current = typeof window === "undefined" ? new URLSearchParams() : new URLSearchParams(window.location.search);
  const siteId = current.get("site_id");
  const pageId = current.get("page_id");
  const query = new URLSearchParams({ snapshot: snapshotId });
  if (siteId) query.set("site_id", siteId);
  if (pageId) query.set("page_id", pageId);
  return `${path}?${query}`;
}

export function entityPath(entityKey: string): string {
  return `/content/entities/${encodeURIComponent(entityKey)}`;
}

export function recordPath(entityKey: string, recordId: string): string {
  return `${entityPath(entityKey)}/records/${encodeURIComponent(recordId)}`;
}

export const placementsPath = "/content/placements";
