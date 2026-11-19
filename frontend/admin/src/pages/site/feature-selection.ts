import type { SiteEditorDraft } from "@/types";

export interface FeatureReference {
  featureKey: string;
  version: string;
}

function validReference(reference: FeatureReference | undefined): reference is FeatureReference {
  return Boolean(reference?.featureKey.trim() && reference.version.trim());
}

/** The snapshot composition is the unique set of builds used by page placements. */
export function deriveFeatureComposition(config: SiteEditorDraft): FeatureReference[] {
  const references = new Map<string, string>();
  for (const page of config.pages) {
    for (const placement of page.placements) {
      if (validReference(placement.build) && !references.has(placement.build.featureKey)) {
        references.set(placement.build.featureKey, placement.build.version);
      }
    }
  }
  return Array.from(references, ([featureKey, version]) => ({ featureKey, version }));
}

/** Replaces legacy/manual selections with the placement-derived composition. */
export function normalizeFeatureComposition(config: SiteEditorDraft): SiteEditorDraft {
  const features = deriveFeatureComposition(config);
  if (JSON.stringify(config.features ?? []) === JSON.stringify(features)) return config;
  return { ...config, features };
}

/** Uses only placement-derived references; legacy config.features is not authoritative. */
export function selectedFeatures(config: SiteEditorDraft): Map<string, string> {
  return new Map(deriveFeatureComposition(config).map(({ featureKey, version }) => [featureKey, version]));
}

/** Selecting a pinned version updates each placement that uses the build. */
export function selectFeature(config: SiteEditorDraft, key: string, version: string): SiteEditorDraft {
  if (!selectedFeatures(config).has(key)) return config;
  return normalizeFeatureComposition({
    ...config,
    pages: config.pages.map((page) => ({
      ...page,
      placements: page.placements.map((placement) => ({
        ...placement,
        build: placement.build.featureKey === key ? { ...placement.build, version } : placement.build,
      })),
    })),
  });
}

export function featureVersionChanges(previous: SiteEditorDraft, next: SiteEditorDraft): Array<{ featureKey: string; fromVersion: string; toVersion: string }> {
  const before = selectedFeatures(previous);
  const after = selectedFeatures(next);
  return [...after].flatMap(([featureKey, toVersion]) => {
    const fromVersion = before.get(featureKey);
    return fromVersion && fromVersion !== toVersion ? [{ featureKey, fromVersion, toVersion }] : [];
  });
}

export function removePlacement(config: SiteEditorDraft, pageId: string, placementId: string): SiteEditorDraft {
  const page = config.pages.find((item) => item.id === pageId);
  if (!page || !page.placements.some((placement) => placement.id === placementId)) return config;
  return normalizeFeatureComposition({
    ...config,
    pages: config.pages.map((item) => item.id === pageId
      ? { ...item, placements: item.placements.filter((placement) => placement.id !== placementId) }
      : item),
  });
}

export function featureIsPlaced(config: SiteEditorDraft, key: string): boolean {
  return selectedFeatures(config).has(key);
}
