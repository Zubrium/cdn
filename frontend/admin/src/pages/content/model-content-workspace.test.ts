import { describe, expect, it } from "vitest";
import type { DataModel, ModelContentRecord, ModelContentSelection, ModelContentWorkspace } from "@/types";
import {
  apiModelRecordInput,
  cleanOrphanedModelContentSelections,
  clearModelRecordData,
  createModelRecord,
  deleteModelRecord,
  duplicateUniqueModelValueIssues,
  modelDraftHasRevisionConflict,
  modelEntityFields,
  modelRecordDeletionImpact,
  modelRecordsEqual,
  orphanedModelContentSelections,
  orphanedModelRecordData,
  remapLocalRecordIDs,
  removeOrphanedModelRecordData,
  reorderModelRecords,
  reorderModelRecordsById,
  reorderSelectedRecords,
  reorderSelectedRecordsById,
  setModelContentSelection,
  updateModelRecordField,
} from "./model-content-workspace";

const model: DataModel = {
  formatVersion: 1,
  entities: [
    {
      id: "products",
      key: "product",
      name: "Товар",
      cardinality: "many",
      fields: [
        { id: "title-field", key: "title", name: "Название", type: "text", unique: true },
        { id: "price-field", key: "price", name: "Цена", type: "number" },
        { id: "image-field", key: "image", name: "Изображение", type: "media" },
      ],
    },
    { id: "settings", key: "settings", name: "Настройки", cardinality: "singleton", fields: [] },
  ],
  positions: {},
};

const record = (id: string, entityId: string, sortOrder: number, title: string): ModelContentRecord => ({
  id,
  entityId,
  sortOrder,
  values: { title },
});

describe("model-backed content helpers", () => {
  it("generates optional field metadata without retired relations", () => {
    const fields = modelEntityFields(model.entities[0]!);
    expect(fields.map((field) => field.type)).toEqual(["string", "number", "media"]);
    expect(fields.every((field) => field.required === false)).toBe(true);
    expect(model).not.toHaveProperty("relations");
    expect(record("p1", "products", 0, "Товар")).not.toHaveProperty("relations");
  });

  it("updates and clears only own record values", () => {
    const initial = [record("p1", "products", 0, "Товар")];
    const selected = updateModelRecordField(initial, "p1", "image", "media-uuid");
    expect(selected[0]?.values.image).toBe("media-uuid");
    expect(clearModelRecordData(selected, "p1")[0]?.values).toEqual({});
    expect(initial[0]?.values.image).toBeUndefined();
  });

  it("orders records and curated placement selections immutably", () => {
    const initial = [record("a", "products", 0, "А"), record("b", "products", 1, "Б"), record("other", "settings", 0, "X")];
    expect(reorderModelRecords(initial, "products", "b", -1).filter((item) => item.entityId === "products").sort((a, b) => a.sortOrder - b.sortOrder).map((item) => item.id)).toEqual(["b", "a"]);
    expect(reorderModelRecordsById(initial, "products", ["b", "a"]).filter((item) => item.entityId === "products").sort((a, b) => a.sortOrder - b.sortOrder).map((item) => item.id)).toEqual(["b", "a"]);
    expect(reorderModelRecordsById(initial, "products", ["a", "a"])).toBe(initial);
    expect(reorderSelectedRecords(["a", "b"], "b", -1)).toEqual(["b", "a"]);
    expect(reorderSelectedRecordsById(["a", "b"], ["b", "a"])).toEqual(["b", "a"]);
    expect(reorderSelectedRecordsById(["a", "b"], ["a", "a"])).toEqual(["a", "b"]);
    expect(initial.map((item) => item.id)).toEqual(["a", "b", "other"]);
  });

  it("previews record deletion impact without relation references", () => {
    const target = { ...record("p1", "products", 0, "Товар"), values: { title: "Товар", image: "media-file" } };
    const selections: ModelContentSelection[] = [{ placementId: "placement", inputKey: "products", mode: "ids", recordIds: ["p1"] }];
    const placements: ModelContentWorkspace["placements"] = [{
      id: "placement",
      pageId: "catalog",
      pageName: "Каталог",
      featureKey: "catalog",
      featureName: "Каталог",
      version: "2.0.0",
      inputs: [{ inputKey: "products", kind: "collection", entityId: "products", fields: [] }],
    }];
    expect(modelRecordDeletionImpact([target], selections, model, "p1", placements)).toEqual({
      placementSelections: 1,
      affectedFeatureInputs: 1,
      mediaReferences: 1,
    });
  });

  it("flags duplicate unique values and ignores optional empty values", () => {
    const records = [record("p1", "products", 0, "Одинаково"), record("p2", "products", 1, "Одинаково"), record("p3", "products", 2, "")];
    expect(duplicateUniqueModelValueIssues(records, model)).toEqual([
      { recordId: "p1", fieldKey: "title", message: "Значение должно быть уникальным в этой сущности." },
      { recordId: "p2", fieldKey: "title", message: "Значение должно быть уникальным в этой сущности." },
    ]);
  });

  it("cleans inherited fields that are no longer in the model", () => {
    const inherited = { ...record("p1", "products", 0, "Товар"), values: { title: "Товар", retired: "old" } };
    expect(orphanedModelRecordData(inherited, model)).toEqual({ unknownFieldKeys: ["retired"] });
    expect(removeOrphanedModelRecordData([inherited], model, "p1")[0]?.values).toEqual({ title: "Товар" });
  });

  it("finds and cleans orphaned placement selections", () => {
    const placements: ModelContentWorkspace["placements"] = [{
      id: "active",
      pageId: "catalog",
      pageName: "Каталог",
      featureKey: "catalog",
      featureName: "Каталог",
      version: "2.0.0",
      inputs: [{ inputKey: "products", kind: "collection", entityId: "products", fields: [] }],
    }];
    const selections: ModelContentSelection[] = [
      { placementId: "active", inputKey: "products", mode: "all", recordIds: [] },
      { placementId: "active", inputKey: "products", mode: "all", recordIds: [] },
      { placementId: "removed", inputKey: "items", mode: "ids", recordIds: ["p1"] },
    ];
    expect(orphanedModelContentSelections(selections, placements)).toEqual([selections[1], selections[2]]);
    expect(cleanOrphanedModelContentSelections(selections, placements)).toEqual([selections[0]]);
  });

  it("removes a record, updates selections and closes sort gaps", () => {
    const result = deleteModelRecord(
      [record("p1", "products", 0, "Первый"), record("p2", "products", 1, "Второй")],
      [{ placementId: "placement", inputKey: "products", mode: "ids", recordIds: ["p1", "p2"] }],
      "p1",
    );
    expect(result.records).toEqual([record("p2", "products", 0, "Второй")]);
    expect(result.selections[0]?.recordIds).toEqual(["p2"]);
  });

  it("round-trips local record IDs and omits relations from API input", () => {
    const local = createModelRecord("products", 0);
    expect(local).not.toHaveProperty("relations");
    expect(apiModelRecordInput(local)).not.toHaveProperty("relations");
    const remapped = remapLocalRecordIDs(
      { records: [local], selections: [{ placementId: "placement", inputKey: "products", mode: "ids", recordIds: [local.id] }] },
      [local],
      [{ ...local, id: "saved-record" }],
    );
    expect(remapped.records[0]?.id).toBe("saved-record");
    expect(remapped.selections[0]?.recordIds).toEqual(["saved-record"]);
  });

  it("detects draft changes against a newer revision", () => {
    const base = [record("a", "products", 0, "A")];
    const changed = [record("a", "products", 0, "B")];
    expect(modelDraftHasRevisionConflict(1, 2, { records: changed, selections: [] }, { records: base, selections: [] })).toBe(true);
    expect(modelRecordsEqual(base, base)).toBe(true);
    expect(setModelContentSelection([], "placement", "products", "ids", ["a", "a"])[0]?.recordIds).toEqual(["a"]);
  });
});
