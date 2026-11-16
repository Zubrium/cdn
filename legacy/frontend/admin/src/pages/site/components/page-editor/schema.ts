import type { FeatureBuild, JsonValue, SitePlacement } from "@/types";

export type SchemaField = { type?: string; title?: string; description?: string; format?: string; default?: JsonValue; enum?: JsonValue[]; items?: SchemaField; properties?: Record<string, SchemaField>; required?: string[]; "x-media"?: { accept?: string }; "x-editor-managed-by-content"?: boolean; "x-editor-managed-by-site"?: boolean; "x-engine-record-id"?: boolean };
export function object(value: unknown): Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}; }

export function properties(schema: unknown): Record<string, SchemaField> { return object(object(schema).properties) as Record<string, SchemaField>; }
export function requiredProperties(schema: unknown): Set<string> {
  const values = object(schema).required;
  return new Set(Array.isArray(values) ? values.filter((value): value is string => typeof value === "string") : []);
}
function cloneJsonValue<T extends JsonValue>(value: T): T {
  if (Array.isArray(value)) return value.map((item) => cloneJsonValue(item)) as T;
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, cloneJsonValue(item)])) as T;
  }
  return value;
}
export function initialValue(schema: SchemaField): JsonValue {
  if (schema.default !== undefined) return cloneJsonValue(schema.default);
  if (schema.enum?.length) return cloneJsonValue(schema.enum[0]!);
  if (schema.type === "boolean") return false;
  if (schema.type === "integer" || schema.type === "number") return 0;
  if (schema.type === "array") return [];
  if (schema.type === "object") {
    const required = new Set(schema.required ?? []);
    const values = Object.entries(schema.properties ?? {})
      .filter(([key, property]) => required.has(key) || property.default !== undefined)
      .map(([key, property]) => [key, initialValue(property)] as const);
    return Object.fromEntries(values);
  }
  return "";
}
export function buildFor(builds: FeatureBuild[], placement: SitePlacement) { return builds.find((item) => item.key === placement.build.featureKey && item.version === placement.build.version); }
export function pointerSegment(value: string): string {
  return value.replaceAll("~", "~0").replaceAll("/", "~1");
}

export function appendSchemaPath(path: string, segment: string): string {
  return `${path}/${pointerSegment(segment)}`;
}
