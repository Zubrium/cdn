import { isRecord } from "@/lib/utils";

export type DiffKind = "added" | "removed" | "changed";

export interface DiffEntry {
  path: string;
  kind: DiffKind;
  before?: string;
  after?: string;
}

function shortValue(value: unknown): string {
  if (typeof value === "string") {
    return value.length > 140 ? `${value.slice(0, 137)}…` : value;
  }
  if (value === undefined) return "—";
  try {
    const text = JSON.stringify(value);
    if (!text) return "—";
    return text.length > 140 ? `${text.slice(0, 137)}…` : text;
  } catch {
    return String(value);
  }
}

function joinPath(base: string, key: string | number): string {
  if (typeof key === "number") return `${base}[${key}]`;
  return base ? `${base}.${key}` : key;
}

/**
 * Produces a bounded, human-readable structural diff. Arrays of objects with
 * an `id` are matched by id so block order and identity are respected.
 */
export function diffJson(before: unknown, after: unknown, maxEntries = 200): DiffEntry[] {
  const entries: DiffEntry[] = [];

  const walk = (left: unknown, right: unknown, path: string, depth: number) => {
    if (entries.length >= maxEntries) return;
    if (left === right) return;
    if (depth > 12) {
      entries.push({
        path: path || "root",
        kind: "changed",
        before: shortValue(left),
        after: shortValue(right),
      });
      return;
    }

    if (Array.isArray(left) && Array.isArray(right)) {
      const leftIds = left.filter(isRecord).map((item) => item.id).filter((id): id is string => typeof id === "string");
      const rightIds = right.filter(isRecord).map((item) => item.id).filter((id): id is string => typeof id === "string");
      if (leftIds.length > 0 || rightIds.length > 0) {
        const leftMap = new Map<string, unknown>();
        left.forEach((item) => {
          if (isRecord(item) && typeof item.id === "string") leftMap.set(item.id, item);
        });
        const rightMap = new Map<string, unknown>();
        right.forEach((item) => {
          if (isRecord(item) && typeof item.id === "string") rightMap.set(item.id, item);
        });
        const allIds = Array.from(new Set([...leftMap.keys(), ...rightMap.keys()]));
        allIds.forEach((id) => {
          const leftItem = leftMap.get(id);
          const rightItem = rightMap.get(id);
          const label = isRecord(leftItem) && typeof leftItem.type === "string"
            ? leftItem.type
            : isRecord(rightItem) && typeof rightItem.type === "string"
              ? rightItem.type
              : id;
          if (!leftMap.has(id)) {
            entries.push({
              path: `${path}[${label}]`,
              kind: "added",
              after: shortValue(rightItem),
            });
          } else if (!rightMap.has(id)) {
            entries.push({
              path: `${path}[${label}]`,
              kind: "removed",
              before: shortValue(leftItem),
            });
          } else {
            walk(leftItem, rightItem, `${path}[${label}]`, depth + 1);
          }
        });
        return;
      }
      const length = Math.max(left.length, right.length);
      for (let index = 0; index < length; index += 1) {
        if (entries.length >= maxEntries) return;
        const leftItem = left[index];
        const rightItem = right[index];
        if (leftItem === undefined) {
          entries.push({ path: joinPath(path, index), kind: "added", after: shortValue(rightItem) });
        } else if (rightItem === undefined) {
          entries.push({ path: joinPath(path, index), kind: "removed", before: shortValue(leftItem) });
        } else {
          walk(leftItem, rightItem, joinPath(path, index), depth + 1);
        }
      }
      return;
    }

    if (isRecord(left) && isRecord(right)) {
      const keys = new Set([...Object.keys(left), ...Object.keys(right)]);
      keys.forEach((key) => {
        if (entries.length >= maxEntries) return;
        const leftItem = left[key];
        const rightItem = right[key];
        if (!(key in left)) {
          entries.push({ path: joinPath(path, key), kind: "added", after: shortValue(rightItem) });
        } else if (!(key in right)) {
          entries.push({ path: joinPath(path, key), kind: "removed", before: shortValue(leftItem) });
        } else {
          walk(leftItem, rightItem, joinPath(path, key), depth + 1);
        }
      });
      return;
    }

    entries.push({
      path: path || "root",
      kind: "changed",
      before: shortValue(left),
      after: shortValue(right),
    });
  };

  walk(before, after, "", 0);
  return entries;
}

export function diffSummary(entries: DiffEntry[]): {
  added: number;
  removed: number;
  changed: number;
} {
  return entries.reduce(
    (summary, entry) => {
      summary[entry.kind] += 1;
      return summary;
    },
    { added: 0, removed: 0, changed: 0 },
  );
}
