import type { SiteEditorDraft, SiteMetadata } from "@/types";

export interface LocalSiteDraft {
  version: 1;
  baseline: SiteEditorDraft;
  config: SiteEditorDraft;
  baselineMetadata?: SiteMetadata;
  metadata?: SiteMetadata;
}

export function siteDraftKey(userId: string, snapshotId: string): string {
  return `site-workspace-draft:v1:${userId}:${snapshotId || "root"}`;
}

export function hasCompetingSiteDraftChange(
  eventKey: string | null,
  targetKey: string,
  expectedRaw: string | null,
  nextRaw: string | null,
): boolean {
  if (eventKey !== null && eventKey !== targetKey) return false;
  return nextRaw !== expectedRaw;
}

function isSiteEditorDraft(value: unknown): value is SiteEditorDraft {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<SiteEditorDraft>;
  return (candidate.formatVersion === 2 || candidate.formatVersion === 3)
    && Array.isArray(candidate.pages) && candidate.routeTree !== null && typeof candidate.routeTree === "object";
}

export function parseSiteDraft(raw: string | null): LocalSiteDraft | null {
  if (!raw) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object") return null;
    const draft = value as Partial<LocalSiteDraft>;
    if (draft.version !== 1 || !isSiteEditorDraft(draft.baseline) || !isSiteEditorDraft(draft.config)) return null;
    return draft as LocalSiteDraft;
  } catch {
    return null;
  }
}
