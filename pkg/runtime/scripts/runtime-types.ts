export type RuntimePlacement = {
  id: string;
  slot: "background" | "header" | "main" | "footer";
  featureKey: string;
  version: string;
  mode: "single" | "collection" | "form";
  runtimeAbi?: "legacy-react-v1" | "static-module-v1" | "static-html-v1";
  entryUrl: string;
  styleUrls?: string[];
  arguments: Record<string, unknown>;
  content?: Record<string, unknown>;
  detailLink?: { routeId: string; field: string; pathTemplate: string };
};

export type Composition = {
  snapshot: { id: string; sequence: number; name: string; contentRevision?: number };
  page: { id: string; name: string; kind: string };
  metadata: { title: string; description: string; faviconUrl: string; ogTitle: string; ogDescription: string; ogImageUrl: string; canonicalUrl?: string; robots?: string; language?: string };
  sharedStyleUrls?: string[];
  themeStyleUrl?: string;
  systemAssets?: { logoUrl?: string };
  placements: RuntimePlacement[];
  routeArgs?: Record<string, string>;
};

export type RuntimeSDK = {
  submitForm: (featureKey: string, data: Record<string, unknown>) => Promise<{ id: string; status: string }>;
};

export type FeatureRouteContext = { path: string; params: Record<string, string>; search: string };

export type FeatureContext = {
  placementId: string;
  featureKey: string;
  version: string;
  arguments: Record<string, unknown>;
  content: Record<string, unknown>;
  route: FeatureRouteContext;
  systemAssets: { logoUrl?: string };
  detailLink?: RuntimePlacement["detailLink"];
  sdk: RuntimeSDK;
  signal: AbortSignal;
};

export type FeatureInstance = {
  ready: Promise<void>;
  update: (next: FeatureContext) => void | Promise<void>;
  unmount: () => void | Promise<void>;
};

export type ModuleFeature = { mount: (root: HTMLElement, context: FeatureContext) => FeatureInstance | Promise<FeatureInstance> };

export type LegacyFeatureProps = {
  content: Record<string, unknown>;
  arguments: Record<string, unknown>;
  featureKey: string;
  sdk: RuntimeSDK;
  detailLink?: RuntimePlacement["detailLink"];
  systemAssets?: Composition["systemAssets"];
};

export type LegacyFeatureInstance = {
  ready: Promise<void>;
  update: (props: LegacyFeatureProps) => Promise<void>;
  unmount: () => void;
};

export type MountedFeature = {
  abi: RuntimePlacement["runtimeAbi"];
  identity: string;
  root: HTMLDivElement;
  controller: AbortController;
  instance?: FeatureInstance | LegacyFeatureInstance;
  contextKey: string;
  ready: boolean;
  failed: boolean;
};

export type RuntimeBootstrap = { composition: Composition | null; error: string | null };
