export const queryKeys = {
  features: {
    all: ["features"] as const,
    list: (params: Record<string, unknown> = {}) => ["features", "list", params] as const,
    detail: (id: string) => ["features", "detail", id] as const,
    draft: (id: string) => ["features", "draft", id] as const,
    versions: (id: string) => ["features", "versions", id] as const,
  },
  content: {
    catalog: ["content", "catalog"] as const,
    settings: (featureId: string) => ["content", "settings", featureId] as const,
    snapshots: ["content", "snapshots"] as const,
    snapshotFeatures: (snapshotId: string) => ["content", "snapshot-features", snapshotId] as const,
    snapshotWorkspace: (snapshotId: string, featureKey: string) => ["content", "snapshot-workspace", snapshotId, featureKey] as const,
  },
  entries: {
    all: ["entries"] as const,
    list: (featureId: string, params: Record<string, unknown> = {}) =>
      ["entries", "list", featureId, params] as const,
    detail: (id: string) => ["entries", "detail", id] as const,
    versions: (id: string) => ["entries", "versions", id] as const,
  },
  submissions: {
    all: ["submissions"] as const,
    list: (formKey: string, params: Record<string, unknown> = {}) =>
      ["submissions", "list", formKey, params] as const,
    detail: (id: string) => ["submissions", "detail", id] as const,
  },
  media: {
    all: ["media"] as const,
    list: (params: Record<string, unknown> = {}) => ["media", "list", params] as const,
    detail: (id: string) => ["media", "detail", id] as const,
  },
  tokens: {
    all: ["tokens"] as const,
    list: () => ["tokens", "list"] as const,
    detail: (id: string) => ["tokens", "detail", id] as const,
  },
  overview: ["overview"] as const,
  health: ["health"] as const,
};
