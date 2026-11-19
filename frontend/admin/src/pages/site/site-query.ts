export function siteContextQuery(snapshotID: string, pageID?: string, siteID?: string): string {
  const current = typeof window === "undefined" ? new URLSearchParams() : new URLSearchParams(window.location.search);
  const effectivePageID = pageID ?? current.get("page_id") ?? "";
  const effectiveSiteID = siteID ?? current.get("site_id") ?? "";
  const query = new URLSearchParams();
  if (snapshotID) query.set("snapshot", snapshotID);
  if (effectiveSiteID) query.set("site_id", effectiveSiteID);
  if (effectivePageID) query.set("page_id", effectivePageID);
  return query.size ? `?${query}` : "";
}
