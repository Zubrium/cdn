export function siteContextQuery(snapshotID: string, pageID?: string): string {
  const current = typeof window === "undefined" ? new URLSearchParams() : new URLSearchParams(window.location.search);
  const effectivePageID = pageID ?? current.get("page_id") ?? "";
  const query = new URLSearchParams();
  if (snapshotID) query.set("snapshot", snapshotID);
  if (effectivePageID) query.set("page_id", effectivePageID);
  return query.size ? `?${query}` : "";
}
