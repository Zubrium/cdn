import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "./types";

type IDFactory = () => string;

function defaultIDFactory(): string {
  return crypto.randomUUID();
}

function isRecord(value: JsonValue | undefined): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Generate or replace the manifest-declared engine identity on an array row. */
export function assignArrayItemRecordID(
  value: JsonValue,
  fields: SchemaFieldNode[],
  regenerate = false,
  createID: IDFactory = defaultIDFactory,
): JsonValue {
  const identity = fields.find((field) => field.engineRecordID);
  if (!identity || !isRecord(value)) return value;
  const current = value[identity.key];
  if (!regenerate && typeof current === "string" && current.trim() !== "") return value;
  return { ...value, [identity.key]: createID() };
}

/** Repair missing or duplicate engine-owned row IDs while retaining stable existing IDs. */
export function normalizeEngineRecordIDs(
  fields: SchemaFieldNode[],
  data: JsonObject,
  createID: IDFactory = defaultIDFactory,
): JsonObject {
  let result = data;

  for (const field of fields) {
    const currentValue = result[field.key];
    if (field.type === "object" && isRecord(currentValue)) {
      const normalized = normalizeEngineRecordIDs(field.children, currentValue, createID);
      if (normalized !== currentValue) result = { ...result, [field.key]: normalized };
      continue;
    }

    if (field.type !== "array" || field.itemType !== "object" || !Array.isArray(currentValue)) continue;

    const identity = field.children.find((child) => child.engineRecordID);
    const seen = new Set<string>();
    let rowsChanged = false;
    const rows = currentValue.map((row) => {
      if (!isRecord(row)) return row;
      let normalized = row;
      if (identity) {
        const currentID = row[identity.key];
        if (typeof currentID !== "string" || currentID.trim() === "" || seen.has(currentID)) {
          let nextID = createID();
          while (seen.has(nextID)) nextID = createID();
          normalized = { ...normalized, [identity.key]: nextID };
        }
        seen.add(String(normalized[identity.key]));
      }
      const nested = normalizeEngineRecordIDs(field.children, normalized, createID);
      if (nested !== row) rowsChanged = true;
      return nested;
    });
    if (rowsChanged) result = { ...result, [field.key]: rows };
  }

  return result;
}
