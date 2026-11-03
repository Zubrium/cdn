import { describe, expect, it } from "vitest";
import { serializeSnapshotContentItems, unsupportedSchemaKeywords } from "./content-workspace";

it("detects unsupported schema constructs in nested fields without inspecting default values", () => {
  expect(unsupportedSchemaKeywords({ type: "object", properties: { title: { type: "string", default: { oneOf: "text" } }, nested: { type: "array", items: { $ref: "#/$defs/item" } } } })).toEqual(["$ref"]);
});

describe("serializeSnapshotContentItems", () => {
  it("sends collection records in manual order and keeps list settings separate", () => {
    const result = serializeSnapshotContentItems([
      { entryId: "alpha", sortOrder: 1, data: { title: "Альфа" } },
      { entryId: "settings", sortOrder: 0, singleSlot: true, data: { heading: "Каталог" } },
      { entryId: "beta", sortOrder: 0, data: { title: "Бета" } },
    ]);

    expect(result.map((item) => item.entryId)).toEqual(["beta", "alpha", "settings"]);
    expect(result.map((item) => item.sortOrder)).toEqual([0, 1, 2]);
    expect(result[0]?.singleSlot).toBeUndefined();
    expect(result[2]?.singleSlot).toBe(true);
  });

  it("keeps single and form content in the engine's dedicated content slot", () => {
    expect(serializeSnapshotContentItems([
      { entryId: "single", sortOrder: 0, singleSlot: true, data: { title: "Текст" } },
    ])).toEqual([
      { entryId: "single", sortOrder: 0, singleSlot: true, data: { title: "Текст" } },
    ]);
  });
});
