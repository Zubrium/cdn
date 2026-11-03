import type { SnapshotContentDraftInput, SnapshotContentDraftItem } from "@/types";

/** Refuse partial forms for schema constructs the graphical editor cannot interpret. */
export function unsupportedSchemaKeywords(schema: unknown): string[] {
  const found = new Set<string>();
  const visit = (value: unknown) => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value)) { value.forEach(visit); return; }
    const node = value as Record<string, unknown>;
    for (const key of ["$ref", "oneOf", "anyOf", "allOf", "if", "then", "else"]) {
      if (key in node) found.add(key);
    }
    if (node.properties && typeof node.properties === "object" && !Array.isArray(node.properties)) {
      Object.values(node.properties).forEach(visit);
    }
    visit(node.items);
    visit(node.additionalProperties);
  };
  visit(schema);
  return [...found];
}

/** Serialize collection records in their visible manual order before settings. */
export function serializeSnapshotContentItems(items: SnapshotContentDraftItem[]): SnapshotContentDraftInput[] {
  const ordered = [
    ...items.filter((item) => !item.singleSlot).sort((a, b) => a.sortOrder - b.sortOrder),
    ...items.filter((item) => item.singleSlot),
  ];
  return ordered.map((item, sortOrder) => ({
    entryId: item.entryId,
    sortOrder,
    ...(item.singleSlot ? { singleSlot: true } : {}),
    data: item.data,
  }));
}
