export function contentHref(path: string, snapshotId: string): string {
  return snapshotId ? `${path}?snapshot=${encodeURIComponent(snapshotId)}` : path;
}

export function entityPath(entityKey: string): string {
  return `/content/entities/${encodeURIComponent(entityKey)}`;
}

export function recordPath(entityKey: string, recordId: string): string {
  return `${entityPath(entityKey)}/records/${encodeURIComponent(recordId)}`;
}

export const placementsPath = "/content/placements";
