declare module "virtual:feature-lab/registry" {
  export type FeatureDesignToken = {
    key: string;
    kind: string;
    default?: {
      number?: number;
      unit?: string;
      color?: { hex?: string; opacity?: number };
      font?: string;
      shadow?: { x: number; y: number; blur: number; spread: number; color: { hex: string; opacity: number }; inset?: boolean };
    };
  };

  export type FeatureManifest = {
    formatVersion: 2;
    key: string;
    name?: string;
    version?: string;
    engineApi?: string;
    slots?: string[];
    runtime?: {
      abi?: string;
      entry?: string;
      styles?: string[];
    };
    arguments?: Record<string, unknown>;
    dataInputs: Array<
      | { key: string; kind: "scalar"; type: string; values?: string[]; profile?: FeatureMediaProfile }
      | { key: string; kind: "record"; fields: FeatureDataInputField[] }
      | { key: string; kind: "collection"; fields: FeatureDataInputField[] }
    >;
    designTokens?: FeatureDesignToken[];
    form?: { submissionSchema?: Record<string, unknown> };
  };

  export type FeatureDataInputField = {
    path: string;
    type: string;
    values?: string[];
    profile?: FeatureMediaProfile;
  };

  export type FeatureMediaProfile = {
    accept: string;
    renditions?: Array<{ name: string; width: number; height: number; fit: string }>;
    video?: { maxWidth: number; maxHeight: number; poster: { name: string; width: number; height: number; fit: string } };
  };

  export type FeatureEntry = {
    key: string;
    manifest: FeatureManifest | null;
    error: string;
    runtimeAbi: "legacy-react" | "static-module-v1" | "static-html-v1";
    entryPath: string;
    load: (() => Promise<unknown>) | null;
  };

  export const vitasilSourceRoot: string;
  export const features: FeatureEntry[];
}
