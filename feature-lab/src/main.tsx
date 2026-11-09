import { Component, useCallback, useEffect, useMemo, useRef, useState, type ComponentType, type CSSProperties, type FormEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { createRoot } from "react-dom/client";
import "@/core/styles/index.css";
import "./styles.css";
import { features, vitasilSourceRoot, type FeatureEntry, type FeatureManifest, type FeatureDataInputField } from "virtual:feature-lab/registry";

interface JsonObject {
  [key: string]: JsonValue;
}
type JsonValue = string | number | boolean | null | JsonObject | JsonValue[];
type JSONState<T> = { text: string; value: T; error: string };
type FixtureSet = {
  arguments: JsonObject;
  content: JsonObject;
  records: JsonObject[];
  runtime: JsonObject;
};
type SDKEvent = { id: number; name: string; detail: string; time: string };
type FeatureDesignToken = {
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
type FeatureSDK = {
  listCollection: (
    endpoint: string,
    options?: { field?: string; value?: string; offset?: number; limit?: number },
  ) => Promise<{ items: Array<{ entryId: string; data: JsonObject }>; total: number; nextOffset?: number }>;
  submitForm: (featureKey: string, data: Record<string, unknown>) => Promise<{ id: string; status: string }>;
};
type FrameworkNeutralContext = {
  placementId: string;
  featureKey: string;
  version: string;
  arguments: JsonObject;
  content: JsonObject;
  route: { path: string; params: Record<string, string>; search: string };
  systemAssets: Record<string, string>;
  sdk: FeatureSDK;
  signal: AbortSignal;
};
type FrameworkNeutralInstance = {
  ready: Promise<void>;
  update: (next: FrameworkNeutralContext) => void | Promise<void>;
  unmount: () => void | Promise<void>;
};
type FrameworkNeutralModule = {
  mount: (root: HTMLElement, context: FrameworkNeutralContext) => FrameworkNeutralInstance | Promise<FrameworkNeutralInstance>;
};
type LoadedPreview =
  | { kind: "react"; component: ComponentType<Record<string, unknown>> }
  | { kind: "module"; module: FrameworkNeutralModule }
  | { kind: "html"; html: string };
type FeatureRoute = FrameworkNeutralContext["route"];

const SAMPLE_IMAGE = "/sample-media.svg";
const PREVIEW_DOCUMENT = "<!doctype html><html lang=\"ru\"><head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"><meta name=\"color-scheme\" content=\"light\"></head><body><div id=\"feature-preview-root\"></div></body></html>";

const CSS_LENGTH_UNITS = new Set(["px", "rem", "em", "%", "vw", "vh", "svh", "ch"]);
const SYSTEM_FONT_FAMILIES = new Set(["system-ui", "serif", "sans-serif", "monospace"]);

function colorTokenCSS(color: { hex?: string; opacity?: number } | undefined): string | null {
  if (!color || !/^#[\da-f]{6}$/i.test(color.hex ?? "")) return null;
  const raw = Number.parseInt(color.hex!.slice(1), 16);
  const opacity = color.opacity ?? 100;
  if (!Number.isFinite(opacity) || opacity < 0 || opacity > 100) return null;
  return `rgb(${raw >> 16} ${(raw >> 8) & 255} ${raw & 255} / ${opacity}%)`;
}

function designTokenCSS(token: FeatureDesignToken): string | null {
  const value = token.default;
  if (!value) return null;
  if (token.kind === "color") return colorTokenCSS(value.color);
  if (token.kind === "number") {
    return typeof value.number === "number" && Number.isFinite(value.number) && !value.unit
      ? String(value.number)
      : null;
  }
  if (token.kind === "length") {
    return typeof value.number === "number" && Number.isFinite(value.number) && CSS_LENGTH_UNITS.has(value.unit ?? "")
      ? `${value.number}${value.unit}`
      : null;
  }
  if (token.kind === "font") {
    return value.font && SYSTEM_FONT_FAMILIES.has(value.font) ? value.font : null;
  }
  if (token.kind === "shadow" && value.shadow) {
    const { x, y, blur, spread, color, inset } = value.shadow;
    const colorValue = colorTokenCSS(color);
    if (![x, y, blur, spread].every(Number.isFinite) || blur < 0 || !colorValue) return null;
    return `${inset ? "inset " : ""}${x}px ${y}px ${blur}px ${spread}px ${colorValue}`;
  }
  return null;
}

function makeJSONState<T>(value: T): JSONState<T> {
  return { text: JSON.stringify(value, null, 2), value, error: "" };
}

function isJsonObject(value: unknown): value is JsonObject {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function parseType(schema: JsonObject): string {
  if (Array.isArray(schema.type)) return schema.type.find((value: unknown): value is string => typeof value === "string" && value !== "null") ?? "string";
  return typeof schema.type === "string" ? schema.type : "object";
}

function sampleValue(schema: JsonObject | undefined, key = "value", depth = 0, sequence = 1): JsonValue {
  if (!schema || depth > 7) return "";
  if (Object.prototype.hasOwnProperty.call(schema, "default")) return JSON.parse(JSON.stringify(schema.default)) as JsonValue;
  if (Array.isArray(schema.enum) && schema.enum.length > 0) return schema.enum[0];

  const type = parseType(schema);
  const normalizedKey = key.toLocaleLowerCase("en");
  if (schema["x-engine-record-id"] === true) return `feature-lab-record-${sequence}`;
  if (schema.format === "media" || schema.format === "file" || (isJsonObject(schema["x-ui"]) && schema["x-ui"].widget === "media")) return SAMPLE_IMAGE;

  if (type === "object") {
    const properties = isJsonObject(schema.properties) ? schema.properties : {};
    return Object.fromEntries(Object.entries(properties).map(([childKey, childSchema]) => [
      childKey,
      sampleValue(isJsonObject(childSchema) ? childSchema : undefined, childKey, depth + 1, sequence),
    ])) as JsonObject;
  }
  if (type === "array") {
    const itemSchema = isJsonObject(schema.items) ? schema.items : {};
    const minimumItems = typeof schema.minItems === "number" ? schema.minItems : 0;
    const itemCount = Number.isInteger(minimumItems) && minimumItems > 0
      ? Math.min(minimumItems, 5)
      : parseType(itemSchema) === "object" ? 2 : 1;
    return Array.from({ length: itemCount }, (_, index) => sampleValue(itemSchema, key, depth + 1, index + 1));
  }
  if (type === "boolean") return true;
  if (type === "integer" || type === "number") return typeof schema.minimum === "number" ? schema.minimum : 1;
  if (schema.format === "email") return "dev@example.test";
  if (/(?:title|name|heading|label|eyebrow)$/.test(normalizedKey)) return `Example ${key}`;
  if (schema.format === "uri" || schema.format === "url" || /(?:url|path|href|to)$/.test(normalizedKey)) {
    return sequence > 1 ? `/example-${sequence}` : "/example";
  }
  if (normalizedKey.includes("email")) return "dev@example.test";
  if (/slug$/.test(normalizedKey)) return `sample-item-${sequence}`;
  if (/(?:description|summary|subtitle)$/.test(normalizedKey)) return `Sample ${key} for local feature preview.`;
  if (normalizedKey === "price") return "12.50";
  return "Example text";
}

type FixtureDataInput = FeatureManifest["dataInputs"][number];

function pointerParts(pointer: string): string[] {
  if (!pointer.startsWith("/")) return [];
  return pointer.slice(1).split("/").map((part) => part.replace(/~1/g, "/").replace(/~0/g, "~"));
}

function dataInputSample(field: FeatureDataInputField, sequence: number): JsonValue {
  if (field.type === "enum") return field.values?.[0] ?? "sample";
  const key = pointerParts(field.path).at(-1)?.toLocaleLowerCase("en") ?? "value";
  if (field.type === "media") return SAMPLE_IMAGE;
  if (field.type === "boolean") return true;
  if (field.type === "integer" || field.type === "number") return sequence;
  if (field.type === "date") return "2026-01-15";
  if (field.type === "dateTime") return "2026-01-15T12:00:00Z";
  if (field.type === "email" || key.includes("email")) return "dev@example.test";
  if (field.type === "url" || /(?:url|path|href|to)$/.test(key)) return sequence > 1 ? `/example-${sequence}` : "/example";
  if (/slug$/.test(key)) return `sample-item-${sequence}`;
  if (/(?:title|name|heading|label|eyebrow)$/.test(key)) return `Example ${key}`;
  if (/(?:description|summary|subtitle)$/.test(key)) return `Sample ${key} for local feature preview.`;
  if (key === "price") return "12.50";
  return "Example text";
}

function setInputPath(target: JsonObject, parts: string[], field: FeatureDataInputField, sequence: number): void {
  if (parts.length === 0) return;
  const [part, ...remaining] = parts;
  if (part === "*") return;
  if (remaining.length === 0) {
    target[part] = dataInputSample(field, sequence);
    return;
  }
  if (remaining[0] === "*") {
    const rows = Array.from({ length: 2 }, (_, index) => ({} as JsonObject));
    target[part] = rows;
    for (const [index, row] of rows.entries()) setInputPath(row, remaining.slice(1), field, sequence * 10 + index + 1);
    return;
  }
  if (!isJsonObject(target[part])) target[part] = {};
  setInputPath(target[part] as JsonObject, remaining, field, sequence);
}

function sampleInputRecord(input: Extract<FixtureDataInput, { kind: "record" | "collection" }>, sequence: number): JsonObject {
  const result: JsonObject = {};
  for (const field of input.fields) {
    const parts = pointerParts(field.path);
    if (parts.length === 0) continue;
    setInputPath(result, parts, field, sequence);
  }
  // Engine model records always have an identity even though record IDs are
  // deliberately not part of a feature's dataInputs field mapping.
  result.id = `feature-lab-record-${sequence}`;
  if ("slug" in result) result.slug = `sample-item-${sequence}`;
  if (typeof result.title === "string") result.title = `Example item ${sequence}`;
  if (typeof result.name === "string") result.name = `Example item ${sequence}`;
  return result;
}

function sampleInputRecords(input: Extract<FixtureDataInput, { kind: "collection" }> | undefined): JsonObject[] {
  if (!input) return [];
  return [1, 2, 3].map((sequence) => sampleInputRecord(input, sequence));
}

function sampleInputValue(input: FixtureDataInput, sequence: number): JsonValue {
  if (input.kind === "scalar") return dataInputSample({ path: `/${input.key}`, type: input.type, values: input.values }, sequence);
  return input.kind === "collection" ? sampleInputRecords(input) : sampleInputRecord(input, sequence);
}

function fixturesFor(feature: FeatureEntry): FixtureSet {
  const manifest = feature.manifest;
  if (!manifest) {
    return {
      arguments: {},
      content: {},
      records: [],
      runtime: { systemAssets: { logoUrl: SAMPLE_IMAGE }, collectionEndpoint: "" },
    };
  }
  const dataInputs = manifest.dataInputs;
  const collectionInput = dataInputs.find((input): input is Extract<FixtureDataInput, { kind: "collection" }> => input.kind === "collection");
  const content = Object.fromEntries(dataInputs.map((input) => [
    input.key,
    sampleInputValue(input, 1),
  ])) as JsonObject;
  return {
    arguments: manifest.arguments ? sampleValue(manifest.arguments as JsonObject, "arguments") as JsonObject : {},
    content,
    records: sampleInputRecords(collectionInput),
    runtime: {
      systemAssets: { logoUrl: SAMPLE_IMAGE },
      collectionEndpoint: collectionInput ? `/${feature.key}` : "",
      ...(collectionInput?.fields.some((field) => field.path === "/slug") ? { detailLink: { routeId: `${feature.key}-detail`, field: "slug", pathTemplate: `/${feature.key}/{slug}` } } : {}),
    },
  };
}

function useJSONState<T>(initial: T) {
  const [state, setState] = useState(() => makeJSONState(initial));
  const reset = useCallback((value: T) => setState(makeJSONState(value)), []);
  const update = useCallback((text: string, expected: "object" | "array") => {
    setState((previous) => {
      try {
        const value: unknown = JSON.parse(text);
        const valid = expected === "array" ? Array.isArray(value) : Boolean(value && typeof value === "object" && !Array.isArray(value));
        if (!valid) return { ...previous, text, error: expected === "array" ? "Нужно передать JSON-массив." : "Нужно передать JSON-объект." };
        return { text, value: value as T, error: "" };
      } catch (reason) {
        return { ...previous, text, error: reason instanceof Error ? reason.message : "Некорректный JSON." };
      }
    });
  }, []);
  return [state, update, reset] as const;
}

class PreviewErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error) {
    console.error("Feature Lab preview error", error);
  }

  render() {
    if (this.state.error) {
      return <div role="alert" style={{ display: "grid", minHeight: "40vh", alignContent: "center", gap: 10, padding: 24, overflowWrap: "anywhere", background: "#fff1ee", color: "#6e2d24", font: "14px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace" }}><strong>Фича завершилась с ошибкой</strong><pre style={{ maxHeight: 360, overflow: "auto", whiteSpace: "pre-wrap" }}>{this.state.error.stack ?? this.state.error.message}</pre></div>;
    }
    return this.props.children;
  }
}

function pathFromLink(href: string): string | null {
  try {
    const target = new URL(href, window.location.href);
    if (target.origin !== window.location.origin) return null;
    return `${target.pathname}${target.search}${target.hash}`;
  } catch {
    return null;
  }
}

function routeFromLocation(): FeatureRoute {
  const current = new URL(window.location.href);
  const search = new URLSearchParams(current.search);
  search.delete("feature");
  const query = search.toString();
  return {
    path: current.pathname || "/",
    params: {},
    search: query ? `?${query}` : "",
  };
}

function staticHTMLValidationError(markup: string): string {
  const template = document.createElement("template");
  template.innerHTML = markup;
  if (template.content.querySelector("script, iframe, object, embed")) {
    return "static-html-v1 cannot include scripts or embedded documents.";
  }

  for (const element of Array.from(template.content.querySelectorAll("*"))) {
    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.name.toLowerCase();
      if (name.startsWith("on")) return `static-html-v1 cannot include inline event handler ${name}.`;
      if (["href", "src", "action", "formaction", "xlink:href"].includes(name)) {
        const value = Array.from(attribute.value).filter((character) => character.charCodeAt(0) > 0x20).join("").toLowerCase();
        if (value.startsWith("javascript:")) return "static-html-v1 cannot include javascript: URLs.";
      }
    }
  }
  return "";
}

function updatePath(path: string): void {
  const current = new URL(window.location.href);
  const next = new URL(path.startsWith("/") ? path : `/${path}`, current);
  const selectedFeature = current.searchParams.get("feature");
  if (selectedFeature && !next.searchParams.has("feature")) next.searchParams.set("feature", selectedFeature);
  window.history.replaceState({}, "", `${next.pathname}${next.search}${next.hash}`);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

function App() {
  const [selectedKey, setSelectedKey] = useState(() => {
    const queryKey = new URLSearchParams(window.location.search).get("feature");
    return features.some((feature) => feature.key === queryKey) ? queryKey! : features[0]?.key ?? "";
  });
  const selected = features.find((feature) => feature.key === selectedKey);
  const [argumentsJSON, updateArgumentsJSON, resetArgumentsJSON] = useJSONState<JsonObject>({});
  const [contentJSON, updateContentJSON, resetContentJSON] = useJSONState<JsonObject>({});
  const [recordsJSON, updateRecordsJSON, resetRecordsJSON] = useJSONState<JsonObject[]>([]);
  const [runtimeJSON, updateRuntimeJSON, resetRuntimeJSON] = useJSONState<JsonObject>({});
  const [loadedPreview, setLoadedPreview] = useState<LoadedPreview | null>(null);
  const [loadError, setLoadError] = useState("");
  const [moduleError, setModuleError] = useState("");
  const [moduleReady, setModuleReady] = useState(false);
  const [moduleMountRoot, setModuleMountRoot] = useState<HTMLElement | null>(null);
  const [events, setEvents] = useState<SDKEvent[]>([]);
  const [viewport, setViewport] = useState(1280);
  const [routeDraft, setRouteDraft] = useState(window.location.pathname || "/");
  const [routeError, setRouteError] = useState("");
  const [previewRoute, setPreviewRoute] = useState<FeatureRoute>(routeFromLocation);
  const [previewDocument, setPreviewDocument] = useState<Document | null>(null);
  const [previewRoot, setPreviewRoot] = useState<HTMLElement | null>(null);
  const [renderVersion, setRenderVersion] = useState(0);
  const previewFrameRef = useRef<HTMLIFrameElement | null>(null);
  const activeModuleRef = useRef<{
    feature: LoadedPreview;
    instance: FrameworkNeutralInstance;
    controller: AbortController;
    root: HTMLElement;
    context: Omit<FrameworkNeutralContext, "signal">;
    updates: Promise<void>;
  } | null>(null);
  const featureContextRef = useRef<Omit<FrameworkNeutralContext, "signal"> | null>(null);

  const addEvent = useCallback((name: string, detail: string) => {
    setEvents((current) => [{
      id: Date.now() + Math.random(),
      name,
      detail,
      time: new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(new Date()),
    }, ...current].slice(0, 30));
  }, []);

  const defaults = useMemo(() => selected ? fixturesFor(selected) : null, [selected]);

  const resetInputs = useCallback((fixture: FixtureSet | null) => {
    resetArgumentsJSON(fixture?.arguments ?? {});
    resetContentJSON(fixture?.content ?? {});
    resetRecordsJSON(fixture?.records ?? []);
    resetRuntimeJSON(fixture?.runtime ?? {});
  }, [resetArgumentsJSON, resetContentJSON, resetRecordsJSON, resetRuntimeJSON]);

  const featureTokenStyles = useMemo(() => {
    const tokens = selected?.manifest?.designTokens ?? [];
    return Object.fromEntries(tokens.flatMap((token) => {
      const value = designTokenCSS(token);
      return value === null ? [] : [[`--feature-${selected!.key}-${token.key}`, value]];
    }));
  }, [selected]);

  const sdk = useMemo(() => ({
    listCollection: async (endpoint: string, options: { field?: string; value?: string; offset?: number; limit?: number } = {}) => {
      addEvent("sdk.listCollection", `${endpoint} · offset ${options.offset ?? 0} · limit ${options.limit ?? "all"}`);
      const rows = recordsJSON.value.map((record, index) => {
        const fixture = isJsonObject(record) ? record : {};
        return {
          entryId: typeof fixture.entryId === "string" ? fixture.entryId : `fixture-entry-${index + 1}`,
          data: isJsonObject(fixture.data) ? fixture.data : fixture,
        };
      });
      const filtered = options.field && options.value !== undefined
        ? rows.filter(({ data }) => String(data[options.field!] ?? "") === options.value)
        : rows;
      const offset = Math.max(0, options.offset ?? 0);
      const limit = Math.max(0, options.limit ?? filtered.length);
      const items = filtered.slice(offset, offset + limit);
      return {
        items,
        total: filtered.length,
        ...(offset + items.length < filtered.length ? { nextOffset: offset + items.length } : {}),
      };
    },
    submitForm: async (featureKey: string, data: Record<string, unknown>) => {
      addEvent("sdk.submitForm", `${featureKey} · ${JSON.stringify(data)}`);
      return { id: `feature-lab-${Date.now()}`, status: "received" };
    },
  }), [addEvent, recordsJSON.value]);

  const runtimeContext = runtimeJSON.value;
  const systemAssets = useMemo(() => {
    if (!isJsonObject(runtimeContext.systemAssets)) return {};
    return Object.fromEntries(Object.entries(runtimeContext.systemAssets).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
  }, [runtimeContext.systemAssets]);
  const featureContext = useMemo<Omit<FrameworkNeutralContext, "signal">>(() => ({
    placementId: `feature-lab-${selected?.key ?? "unselected"}`,
    featureKey: selected?.key ?? "",
    version: selected?.manifest?.version ?? "",
    arguments: argumentsJSON.value,
    content: contentJSON.value,
    route: previewRoute,
    systemAssets,
    sdk,
  }), [argumentsJSON.value, contentJSON.value, previewRoute, sdk, selected, systemAssets]);
  featureContextRef.current = featureContext;

  useEffect(() => {
    if (!previewDocument) return;
    const sourceRoot = vitasilSourceRoot.replaceAll("\\", "/").replace(/\/$/, "");
    const syncFeatureStyles = () => {
      const sourceStyles = Array.from(document.head.querySelectorAll<HTMLStyleElement>("style[data-vite-dev-id]"))
        .filter((style) => {
          const id = style.getAttribute("data-vite-dev-id")?.split("?", 1)[0].replaceAll("\\", "/") ?? "";
          return id.startsWith(`${sourceRoot}/`);
        });
      const activeIds = new Set<string>();

      for (const sourceStyle of sourceStyles) {
        const id = sourceStyle.getAttribute("data-vite-dev-id");
        if (!id) continue;
        activeIds.add(id);
        let frameStyle = Array.from(previewDocument.head.querySelectorAll<HTMLStyleElement>("style[data-feature-lab-style]"))
          .find((style) => style.getAttribute("data-feature-lab-style") === id);
        if (!frameStyle) {
          frameStyle = previewDocument.createElement("style");
          frameStyle.setAttribute("data-feature-lab-style", id);
          previewDocument.head.append(frameStyle);
        }
        if (frameStyle.textContent !== sourceStyle.textContent) frameStyle.textContent = sourceStyle.textContent;
        frameStyle.media = sourceStyle.media;
      }

      for (const frameStyle of Array.from(previewDocument.head.querySelectorAll<HTMLStyleElement>("style[data-feature-lab-style]"))) {
        if (!activeIds.has(frameStyle.getAttribute("data-feature-lab-style") ?? "")) frameStyle.remove();
      }
    };

    syncFeatureStyles();
    const observer = new MutationObserver(syncFeatureStyles);
    observer.observe(document.head, { childList: true, characterData: true, subtree: true });
    return () => observer.disconnect();
  }, [previewDocument]);

  const chooseFeature = (key: string) => {
    setSelectedKey(key);
    const target = features.find((feature) => feature.key === key);
    resetInputs(target ? fixturesFor(target) : null);
    setEvents([]);
    setLoadError("");
    setRouteError("");
    setLoadedPreview(null);
    setModuleError("");
    setModuleReady(false);
    const next = new URL(window.location.href);
    next.searchParams.set("feature", key);
    window.history.replaceState({}, "", `${next.pathname}${next.search}${next.hash}`);
  };

  useEffect(() => {
    resetInputs(defaults);
  }, [defaults, resetInputs]);

  useEffect(() => {
    if (!selected?.load) {
      setLoadedPreview(null);
      setLoadError(selected?.error ?? "В репозитории нет фич для отображения.");
      return;
    }
    let disposed = false;
    setLoadedPreview(null);
    setLoadError("");
    setModuleError("");
    setModuleReady(false);
    selected.load().then((loaded) => {
      if (disposed) return;

      if (selected.runtimeAbi === "legacy-react") {
        const component = (loaded as { default?: unknown } | null)?.default;
        if (typeof component !== "function") throw new Error("Legacy React source entry must export a default component.");
        setLoadedPreview({ kind: "react", component: component as ComponentType<Record<string, unknown>> });
        return;
      }

      if (selected.runtimeAbi === "static-module-v1") {
        const mount = (loaded as { mount?: unknown } | null)?.mount;
        if (typeof mount !== "function") throw new Error("static-module-v1 entry must export mount(root, context).");
        setLoadedPreview({ kind: "module", module: { mount: mount as FrameworkNeutralModule["mount"] } });
        return;
      }

      const html = (loaded as { html?: unknown } | null)?.html;
      if (typeof html !== "string") throw new Error("static-html-v1 entry did not provide an HTML fragment.");
      const htmlError = staticHTMLValidationError(html);
      if (htmlError) throw new Error(htmlError);
      setLoadedPreview({ kind: "html", html });
    }).catch((reason) => {
      if (!disposed) setLoadError(reason instanceof Error ? reason.stack ?? reason.message : String(reason));
    });
    return () => { disposed = true; };
  }, [selected]);

  useEffect(() => {
    if (loadedPreview?.kind !== "module" || !moduleMountRoot || !featureContextRef.current) return;
    const controller = new AbortController();
    let cancelled = false;
    let instance: FrameworkNeutralInstance | null = null;
    const root = moduleMountRoot;
    root.replaceChildren();
    setModuleError("");
    setModuleReady(false);

    const mountFeature = async () => {
      try {
        const initialContext = featureContextRef.current!;
        const mounted = await loadedPreview.module.mount(root, { ...initialContext, signal: controller.signal });
        if (!mounted || typeof mounted !== "object" || typeof mounted.update !== "function" || typeof mounted.unmount !== "function" || !mounted.ready || typeof mounted.ready.then !== "function") {
          throw new Error("static-module-v1 mount() must return { ready, update, unmount }.");
        }
        instance = mounted;
        if (cancelled) {
          await mounted.unmount();
          return;
        }

        const active = {
          feature: loadedPreview,
          instance: mounted,
          controller,
          root,
          context: initialContext,
          updates: Promise.resolve(),
        };
        activeModuleRef.current = active;
        await mounted.ready;
        if (cancelled) return;

        const latestContext = featureContextRef.current;
        if (latestContext && latestContext !== initialContext) {
          await mounted.update({ ...latestContext, signal: controller.signal });
          active.context = latestContext;
        }
        if (cancelled) return;
        setModuleReady(true);
      } catch (reason) {
        if (!cancelled) setModuleError(reason instanceof Error ? reason.stack ?? reason.message : String(reason));
      }
    };

    void mountFeature();
    return () => {
      cancelled = true;
      controller.abort();
      if (activeModuleRef.current?.instance === instance) activeModuleRef.current = null;
      if (instance) {
        void Promise.resolve(instance.unmount())
          .catch((reason) => console.error("Feature Lab module unmount error", reason))
          .finally(() => root.replaceChildren());
      } else {
        root.replaceChildren();
      }
    };
  }, [loadedPreview, moduleMountRoot, renderVersion]);

  useEffect(() => {
    const active = activeModuleRef.current;
    if (!moduleReady || !active || loadedPreview?.kind !== "module" || active.feature !== loadedPreview || active.context === featureContext) return;
    let cancelled = false;
    active.context = featureContext;
    active.updates = active.updates
      .then(() => {
        if (activeModuleRef.current !== active || active.controller.signal.aborted) return;
        return active.instance.update({ ...featureContext, signal: active.controller.signal });
      })
      .then(() => {
        if (!cancelled && activeModuleRef.current === active) setModuleError("");
      })
      .catch((reason) => {
        if (!cancelled && activeModuleRef.current === active) {
          setModuleError(reason instanceof Error ? reason.stack ?? reason.message : String(reason));
        }
      });
    return () => { cancelled = true; };
  }, [featureContext, loadedPreview, moduleReady]);

  const validInputs = !argumentsJSON.error && !contentJSON.error && !recordsJSON.error && !runtimeJSON.error;
  const previewBusy = !loadedPreview || !previewRoot || (loadedPreview.kind === "module" && !moduleReady);
  const handlePreviewClick = (event: ReactMouseEvent<HTMLDivElement>) => {
    const target = event.target;
    const frameWindow = event.currentTarget.ownerDocument.defaultView;
    if (!frameWindow || !(target instanceof frameWindow.Element)) return;
    const anchor = target.closest<HTMLAnchorElement>("a[href]");
    if (!anchor || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || anchor.download || (anchor.target && anchor.target !== "_self")) return;
    const nextPath = pathFromLink(anchor.href);
    if (!nextPath) return;
    event.preventDefault();
    setRouteDraft(nextPath.split(/[?#]/, 1)[0] || "/");
    setRouteError("");
    updatePath(nextPath);
    setPreviewRoute(routeFromLocation());
    addEvent("navigation", nextPath);
  };

  const applyRoute = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextPath = pathFromLink(routeDraft);
    if (!nextPath) {
      setRouteError("Введите локальный адрес, например /catalog.");
      return;
    }
    setRouteError("");
    updatePath(nextPath);
    setPreviewRoute(routeFromLocation());
    setRouteDraft(nextPath.split(/[?#]/, 1)[0] || "/");
    addEvent("navigation", nextPath);
  };

  const initializePreviewFrame = () => {
    const nextDocument = previewFrameRef.current?.contentDocument ?? null;
    setPreviewDocument(nextDocument);
    setPreviewRoot(nextDocument?.getElementById("feature-preview-root") ?? null);
  };

  return (
    <>
      <a className="skip-link" href="#feature-lab-content">Перейти к редактору</a>
      <main className="lab-app" id="feature-lab-content" tabIndex={-1}>
        <header className="lab-header">
        <div>
          <p className="eyebrow">LOCAL FEATURE WORKBENCH</p>
          <h1>Feature Lab</h1>
          <p className="subtitle">Пишите и проверяйте фичи без запуска Engine.</p>
        </div>
        <label className="feature-picker">
          <span>Фича</span>
          <select value={selectedKey} onChange={(event) => chooseFeature(event.currentTarget.value)} disabled={features.length === 0}>
            {features.map((feature) => <option key={feature.key} value={feature.key}>{feature.manifest?.name ?? feature.key} · {feature.key}</option>)}
            {features.length === 0 && <option value="">Фичи не найдены</option>}
          </select>
        </label>
        </header>

        {!selected ? (
          <section className="empty-state"><h2>Фичи не обнаружены</h2><p>Проверьте путь VITASIL_FRONTEND_DIR и наличие src/features/*/manifest.json.</p></section>
        ) : selected.error ? (
          <section className="error-card" role="alert"><h2>{selected.key}</h2><p>{selected.error}</p><code>{selected.entryPath || selected.key}</code></section>
        ) : (
          <div className="lab-layout">
            <section className="preview-column" aria-label="Предпросмотр фичи">
              <div className="feature-meta">
                <div><h2>{selected.manifest?.name ?? selected.key}</h2><p><code>{selected.key}</code><span aria-hidden="true"> / </span><span>v{selected.manifest?.version ?? "?"}</span><span aria-hidden="true"> / </span><span>SDK ABI {selected.manifest?.engineApi ?? "?"}</span><span aria-hidden="true"> / </span><span>{selected.runtimeAbi}</span></p></div>
                <button type="button" className="secondary-button" onClick={() => setRenderVersion((value) => value + 1)}>Сбросить состояние</button>
              </div>
              <div className="preview-controls">
                <div className="route-control">
                  <label htmlFor="preview-path">Путь страницы</label>
                  <form onSubmit={applyRoute}>
                    <input id="preview-path" name="previewPath" autoComplete="off" value={routeDraft} onChange={(event) => { setRouteDraft(event.currentTarget.value); setRouteError(""); }} aria-describedby={routeError ? "preview-path-error" : undefined} aria-invalid={Boolean(routeError)} />
                    <button type="submit">Перейти</button>
                  </form>
                  {routeError && <p className="route-error" id="preview-path-error" role="alert">{routeError}</p>}
                </div>
                <div className="width-control">
                  <span>Ширина предпросмотра</span>
                  <div role="group" aria-label="Готовая ширина предпросмотра">
                    {[360, 390, 768, 1280].map((width) => <button type="button" key={width} className={viewport === width ? "active" : ""} aria-pressed={viewport === width} onClick={() => setViewport(width)}>{width}</button>)}
                    <label className="custom-width" htmlFor="custom-preview-width"><span>Своя</span><input id="custom-preview-width" name="previewWidth" aria-label="Ширина предпросмотра в пикселях" type="number" min={320} max={1920} value={viewport} onChange={(event) => setViewport(Math.min(1920, Math.max(320, Number(event.currentTarget.value) || 320)))} /></label>
                  </div>
                </div>
              </div>
              <div className="preview-stage" aria-label={`Область предпросмотра шириной ${viewport} пикселей`}>
                {!validInputs && <div className="preview-warning" role="status">В JSON есть ошибка. Предпросмотр использует последние корректные данные.</div>}
                <div className="preview-window" aria-busy={previewBusy || Boolean(loadError || moduleError)}>
                  <iframe
                    ref={previewFrameRef}
                    className="preview-frame"
                    title={`Живой предпросмотр: ${selected.manifest?.name ?? selected.key}`}
                    srcDoc={PREVIEW_DOCUMENT}
                    style={{ width: `${viewport}px` }}
                    onLoad={initializePreviewFrame}
                  />
                  {loadError || moduleError ? <div className="preview-overlay preview-import-error" role="alert"><strong>Ошибка фичи</strong><pre>{loadError || moduleError}</pre></div> : previewBusy ? <div className="preview-overlay preview-loading" role="status">Загружаем исходный модуль…</div> : null}
                  {loadedPreview && previewRoot && !loadError && !moduleError && createPortal(
                    <PreviewErrorBoundary key={`${selected.key}:${renderVersion}`}>
                      {loadedPreview.kind === "react" ? (
                        <div key="legacy-react" className="preview-site" style={featureTokenStyles as CSSProperties} onClickCapture={handlePreviewClick}>
                          <loadedPreview.component
                            key={`${selected.key}:${renderVersion}`}
                            {...runtimeContext}
                            content={contentJSON.value}
                            arguments={argumentsJSON.value}
                            featureKey={selected.key}
                            sdk={sdk}
                          />
                        </div>
                      ) : (
                        <div
                          key={loadedPreview.kind}
                          ref={loadedPreview.kind === "module" ? setModuleMountRoot : undefined}
                          className="preview-site"
                          style={featureTokenStyles as CSSProperties}
                          onClickCapture={handlePreviewClick}
                          dangerouslySetInnerHTML={loadedPreview.kind === "html" ? { __html: loadedPreview.html } : undefined}
                        />
                      )}
                    </PreviewErrorBoundary>,
                    previewRoot,
                  )}
                </div>
              </div>
              <div className="event-panel">
                <div className="event-heading"><div><h3>Локальные вызовы SDK</h3><p>Запросы остаются внутри браузера.</p></div><button className="text-button" type="button" onClick={() => setEvents([])} disabled={events.length === 0}>Очистить</button></div>
                {events.length === 0 ? <p className="event-empty">Вызовов пока нет.</p> : <ul aria-live="polite" aria-relevant="additions text">{events.map((item) => <li key={item.id}><time>{item.time}</time><code>{item.name}</code><span>{item.detail}</span></li>)}</ul>}
              </div>
            </section>

            <aside className="editor-column" aria-label="Входные данные фичи">
              <div className="editor-heading"><div><h2>Входные данные</h2><p>arguments читаются из manifest; content строится по dataInputs и имитирует проекцию модели только для предпросмотра.</p></div>{selected.runtimeAbi !== "static-html-v1" && <button type="button" className="text-button" onClick={() => resetInputs(defaults)}>Сбросить fixtures</button>}</div>
              {selected.runtimeAbi === "static-html-v1" ? (
                <p className="static-html-note">static-html-v1 не получает контент, аргументы или вызовы SDK. Редактируйте HTML и стили в исходниках фичи.</p>
              ) : (
                <>
                  <JSONEditor label="arguments" description="Аргументы размещения" state={argumentsJSON} onChange={(text) => updateArgumentsJSON(text, "object")} />
                  <JSONEditor label="content" description="Runtime-данные, форма и поля которых задаются snapshot ER-моделью в Engine" state={contentJSON} onChange={(text) => updateContentJSON(text, "object")} />
                  <JSONEditor label="records" description="Записи для sdk.listCollection" state={recordsJSON} onChange={(text) => updateRecordsJSON(text, "array")} />
                  <JSONEditor label="runtime" description="Системные ресурсы и ссылки" state={runtimeJSON} onChange={(text) => updateRuntimeJSON(text, "object")} />
                </>
              )}
              <div className="source-note"><span>Источник</span><code>{selected.entryPath}</code><span>Манифест</span><code>src/features/{selected.key}/manifest.json</code></div>
            </aside>
          </div>
        )}
      </main>
    </>
  );
}

function JSONEditor<T>({ label, description, state, onChange }: {
  label: string;
  description: string;
  state: JSONState<T>;
  onChange: (value: string) => void;
}) {
  const id = `json-${label}`;
  const descriptionID = `${id}-description`;
  const errorID = `${id}-error`;
  return (
    <section className="json-editor">
      <div className="json-heading"><div><label htmlFor={id}>{label}</label><p id={descriptionID}>{description}</p></div>{state.error && <span className="invalid-tag">Ошибка JSON</span>}</div>
      <textarea id={id} name={label} autoComplete="off" spellCheck={false} value={state.text} onChange={(event) => onChange(event.currentTarget.value)} aria-invalid={Boolean(state.error)} aria-describedby={state.error ? `${descriptionID} ${errorID}` : descriptionID} />
      {state.error && <p className="json-error" id={errorID} role="alert">{state.error}</p>}
    </section>
  );
}

const root = document.getElementById("root");
if (!root) throw new Error("Feature Lab root element was not found.");
const reactRoot = createRoot(root);
reactRoot.render(<App />);
// Release the root before Vite re-evaluates this entry.
if (import.meta.hot) import.meta.hot.dispose(() => reactRoot.unmount());
