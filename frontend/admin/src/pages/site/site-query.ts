export function siteContextQuery(snapshotID: string, siteID: string): string {
  if (snapshotID) return `?snapshot=${encodeURIComponent(snapshotID)}`;
  return siteID ? `?site_id=${encodeURIComponent(siteID)}` : "";
}
