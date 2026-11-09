import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { DataModel, ModelContentRecord, ModelContentSelection, ModelContentWorkspace } from "@/types";
import { EntryFieldsForm, validateEntryFields } from "@/domains/content-editor/EntryForm";
import {
  apiModelRecordInput,
  canonicalRecordID,
  clearModelRecordData,
  cleanOrphanedModelContentSelections,
  createModelRecord,
  deleteModelRecord,
  modelDraftHasRevisionConflict,
  modelEntityFields,
  modelRecordDeletionImpact,
  modelRecordTitle,
  selectedModelRecord,
  duplicateUniqueModelValueIssues,
  modelContentRelationIssues,
  orphanedModelContentSelections,
  orphanedModelRecordData,
  reorderModelRecords,
  reorderSelectedRecords,
  remapLocalRecordIDs,
  removeOrphanedModelRecordData,
  setModelContentSelection,
  updateModelRecordField,
  updateModelRecordRelation,
} from "./model-content-workspace";

const model: DataModel = {
  formatVersion: 1,
  entities: [
    {
      id: "products",
      key: "product",
      name: "Товар",
      fields: [
        { id: "title-field", key: "title", name: "Название", type: "text" },
        { id: "price-field", key: "price", name: "Цена", type: "number" },
        { id: "status-field", key: "status", name: "Статус", type: "enum", options: ["draft", "live"] },
        { id: "image-field", key: "image", name: "Изображение", type: "media" },
      ],
    },
    { id: "categories", key: "category", name: "Категория", fields: [{ id: "category-name", key: "name", name: "Имя", type: "text" }] },
  ],
  relations: [{ id: "product-category", name: "Категория", fromEntityId: "products", toEntityId: "categories", cardinality: "many-to-many" }],
};

const record = (id: string, entityId: string, sortOrder: number, title: string): ModelContentRecord => ({
  id,
  entityId,
  sortOrder,
  values: { title },
  relations: {},
});

describe("model-backed content helpers", () => {
  it("generates accessible field metadata with every model field optional and validates only supplied values", () => {
    const fields = modelEntityFields(model.entities[0]!);
    expect(fields.map((field) => field.type)).toEqual(["string", "number", "enum", "media"]);
    expect(fields.every((field) => field.required === false)).toBe(true);
    expect(fields[2]?.enumValues.map((option) => option.value)).toEqual(["draft", "live"]);
    expect(validateEntryFields(fields, {})).toEqual([]);
    expect(validateEntryFields(fields, { price: "not-a-number", status: "other" }).length).toBeGreaterThan(0);
  });

  it("renders model field labels and an existing-media picker without making fields required", () => {
    const fields = modelEntityFields(model.entities[0]!);
    const form = createElement(EntryFieldsForm, { fields, metadata: {}, onChange: () => {}, issues: [] });
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() }, form));
    expect(html).toContain("Название");
    expect(html).toContain("Цена");
    expect(html).toContain("Выбрать изображение");
    expect(html).not.toMatch(/\brequired(?:="")?/);
  });

  it("keeps media values as ordinary model values and omits cleared optional values", () => {
    const initial = [record("p1", "products", 0, "Товар")];
    const selected = updateModelRecordField(initial, "p1", "image", "media-uuid");
    expect(selected[0]?.values.image).toBe("media-uuid");
    const cleared = updateModelRecordField(selected, "p1", "image", "");
    expect(cleared[0]?.values).not.toHaveProperty("image");
  });

  it("orders records and curated collection selections immutably", () => {
    const initial = [record("a", "products", 0, "А"), record("b", "products", 1, "Б"), record("other", "categories", 0, "X")];
    const reordered = reorderModelRecords(initial, "products", "b", -1);
    expect(reordered.filter((item) => item.entityId === "products").sort((a, b) => a.sortOrder - b.sortOrder).map((item) => item.id)).toEqual(["b", "a"]);
    expect(initial[0]?.id).toBe("a");

    const selections: ModelContentSelection[] = [];
    const selected = setModelContentSelection(selections, "placement", "products", "ids", ["a", "b"]);
    expect(setModelContentSelection(selected, "placement", "products", "ids", ["b", "a"])[0]?.recordIds).toEqual(["b", "a"]);
    expect(reorderSelectedRecords(["a", "b"], "b", -1)).toEqual(["b", "a"]);
    const linked = { ...record("p1", "products", 0, "Связанный товар"), relations: { "product-category": ["c1"] } };
    const sortedLinks = reorderModelRecords([linked, record("p2", "products", 1, "Другой товар")], "products", "p2", -1);
    expect(sortedLinks.find((item) => item.id === "p1")?.relations?.["product-category"]).toEqual(["c1"]);
    expect(selections).toEqual([]);
  });

  it("previews incoming links and placement uses before deleting a record", () => {
    const source = { ...record("p1", "products", 0, "Товар"), relations: { "product-category": ["c1"] } };
    const target = record("c1", "categories", 0, "Категория");
    const selections: ModelContentSelection[] = [{ placementId: "placement", inputKey: "categories", mode: "ids", recordIds: ["c1"] }];
    const placements: ModelContentWorkspace["placements"] = [{
      id: "placement",
      pageId: "catalog",
      pageName: "Каталог",
      featureKey: "catalog",
      featureName: "Каталог",
      version: "2.0.1",
      inputs: [{ inputKey: "categories", kind: "collection", entityId: "categories", fields: [] }],
    }];
    expect(modelRecordDeletionImpact([source, target], selections, model, "c1", placements)).toEqual({
      incomingRelations: 1,
      outgoingRelations: 0,
      placementSelections: 1,
      affectedFeatureInputs: 1,
      mediaReferences: 0,
    });
  });

  it("reports media references without implying that media files will be deleted", () => {
    const relatedModel: DataModel = {
      ...model,
      relations: [{ id: "related-products", name: "Похожие товары", fromEntityId: "products", toEntityId: "products", cardinality: "many-to-many" }],
    };
    const target = { ...record("p1", "products", 0, "Товар"), values: { title: "Товар", image: "media-file" }, relations: { "related-products": ["p3"] } };
    const source = { ...record("p2", "products", 1, "Другой товар"), relations: { "related-products": ["p1"] } };
    const selections: ModelContentSelection[] = [{ placementId: "placement", inputKey: "products", mode: "ids", recordIds: ["p1"] }];

    expect(modelRecordDeletionImpact([target, source, record("p3", "products", 2, "Третий")], selections, relatedModel, "p1")).toEqual({
      incomingRelations: 1,
      outgoingRelations: 1,
      placementSelections: 1,
      affectedFeatureInputs: 0,
      mediaReferences: 1,
    });
  });

  it("validates normalized relation targets, cardinality, and exclusive ownership", () => {
    const ownedRelationModel: DataModel = {
      ...model,
      relations: [{ ...model.relations![0]!, cardinality: "one-to-many" }],
    };
    const records: ModelContentRecord[] = [
      { ...record("p1", "products", 0, "Первый товар"), relations: { "product-category": ["c1", "missing"] } },
      { ...record("p2", "products", 1, "Второй товар"), relations: { "product-category": ["c1"] } },
      record("c1", "categories", 0, "Категория"),
    ];
    const issues = modelContentRelationIssues(records, ownedRelationModel);

    expect(issues).toContainEqual({ recordId: "p1", relationId: "product-category", message: "Связанная запись не найдена." });
    expect(issues).toContainEqual({ recordId: "p1", relationId: "product-category", message: "Запись уже связана с другой исходной записью." });
    expect(issues).toContainEqual({ recordId: "p2", relationId: "product-category", message: "Запись уже связана с другой исходной записью." });
  });

  it("flags duplicate non-empty unique values while leaving optional empty values valid", () => {
    const uniqueModel: DataModel = {
      ...model,
      entities: [{
        ...model.entities[0]!,
        fields: model.entities[0]!.fields.map((field) => field.key === "title" ? { ...field, unique: true } : field),
      }],
    };
    const records: ModelContentRecord[] = [
      record("p1", "products", 0, "Одинаковое значение"),
      record("p2", "products", 1, "Одинаковое значение"),
      record("p3", "products", 2, ""),
    ];

    expect(duplicateUniqueModelValueIssues(records, uniqueModel)).toEqual([
      { recordId: "p1", fieldKey: "title", message: "Значение должно быть уникальным в этой сущности." },
      { recordId: "p2", fieldKey: "title", message: "Значение должно быть уникальным в этой сущности." },
    ]);
  });

  it("finds placement selections whose placement or input was removed", () => {
    const placements: ModelContentWorkspace["placements"] = [{
      id: "active-placement",
      pageId: "catalog",
      pageName: "Каталог",
      featureKey: "catalog",
      featureName: "Каталог",
      version: "2.0.1",
      inputs: [{ inputKey: "products", kind: "collection", entityId: "products", fields: [] }],
    }];
    const selections: ModelContentSelection[] = [
      { placementId: "active-placement", inputKey: "products", mode: "all", recordIds: [] },
      { placementId: "active-placement", inputKey: "products", mode: "all", recordIds: [] },
      { placementId: "removed-placement", inputKey: "items", mode: "ids", recordIds: ["p1"] },
    ];
    expect(orphanedModelContentSelections(selections, placements)).toEqual([selections[1], selections[2]]);
    expect(cleanOrphanedModelContentSelections(selections, placements)).toEqual([selections[0]]);
  });

  it("deletes a row only after confirmation in the screen and removes its inbound references", () => {
    const records: ModelContentRecord[] = [
      { ...record("p1", "products", 0, "Товар"), relations: { "product-category": ["c1"] } },
      { ...record("c1", "categories", 0, "Категория"), relations: {} },
    ];
    const selections: ModelContentSelection[] = [{ placementId: "placement", inputKey: "items", mode: "ids", recordIds: ["c1", "p1"] }];
    const deleted = deleteModelRecord(records, selections, model, "c1");
    expect(deleted.records.map((item) => item.id)).toEqual(["p1"]);
    expect(deleted.records[0]?.relations?.["product-category"]).toEqual([]);
    expect(deleted.selections[0]?.recordIds).toEqual(["p1"]);
    expect(records[0]?.relations?.["product-category"]).toEqual(["c1"]);
  });

  it("compacts an entity's order after deletion without changing the other entity", () => {
    const records = [
      record("p1", "products", 0, "Первый"),
      record("p2", "products", 1, "Удалить"),
      record("p3", "products", 2, "Третий"),
      record("c1", "categories", 0, "Категория"),
    ];
    const result = deleteModelRecord(records, [], model, "p2");

    expect(result.records.filter((item) => item.entityId === "products").map((item) => [item.id, item.sortOrder])).toEqual([["p1", 0], ["p3", 1]]);
    expect(result.records.find((item) => item.id === "c1")?.sortOrder).toBe(0);
  });

  it("uses a field value as the record label without displaying internal IDs", () => {
    expect(modelRecordTitle(record("secret-id", "products", 4, "Тёплая куртка"), model.entities[0]!)).toBe("Тёплая куртка");
  });

  it("prefers a human title over an earlier technical slug field", () => {
    const slugFirstEntity = {
      ...model.entities[0]!,
      fields: [
        { id: "slug-field", key: "slug", name: "Слаг", type: "text" as const },
        { id: "title-field", key: "title", name: "Название", type: "text" as const },
      ],
    };
    const pear = { ...record("pear-id", "products", 0, "Груша"), values: { slug: "pear", title: "Груша" } };
    expect(modelRecordTitle(pear, slugFirstEntity)).toBe("Груша");
  });

  it("leaves new IDs to the server and maps the selected local row to its canonical response ID", () => {
    const local = createModelRecord("products", 2);
    expect(local.id.startsWith("local:")).toBe(true);
    expect(apiModelRecordInput(local)).not.toHaveProperty("id");
    const saved = { ...local, id: "server-uuid" };
    expect(canonicalRecordID(local.id, [local], [saved])).toBe("server-uuid");
    expect(apiModelRecordInput(saved)).toEqual(saved);
    const editedDuringSave = { ...local, values: { title: "Changed while request was in flight" } };
    expect(remapLocalRecordIDs({ records: [editedDuringSave], selections: [] }, [local], [saved]).records[0]).toEqual({ ...editedDuringSave, id: "server-uuid" });
  });

  it("auto-selects exactly one singleton row and clears its values and relations without changing identity", () => {
    const singleton = { ...model.entities[0]!, cardinality: "singleton" as const };
    const row: ModelContentRecord = {
      ...record("singleton-row", singleton.id, 0, "Единственная запись"),
      values: { title: "Текущее значение", image: "media-id" },
      relations: { "product-category": ["category-1"] },
    };

    expect(selectedModelRecord(singleton, [row], "")).toBe(row);
    expect(selectedModelRecord(singleton, [], "")).toBeNull();
    expect(selectedModelRecord(singleton, [row, { ...row, id: "second" }], "singleton-row")).toBeNull();

    const cleared = clearModelRecordData([row], row.id);
    expect(cleared[0]).toEqual({ ...row, values: {}, relations: {} });
    expect(cleared[0]?.id).toBe(row.id);
    expect(cleared[0]?.entityId).toBe(row.entityId);
    expect(cleared[0]?.sortOrder).toBe(row.sortOrder);
    expect(row.values).toEqual({ title: "Текущее значение", image: "media-id" });
    expect(row.relations).toEqual({ "product-category": ["category-1"] });
  });

  it("detects a stale local revision without replacing its record values", () => {
    const server = { records: [record("p1", "products", 0, "Server")], selections: [] };
    const local = { records: [record("p1", "products", 0, "Local draft")], selections: [] };
    expect(modelDraftHasRevisionConflict(4, 5, local, server)).toBe(true);
    expect(local.records[0]?.values.title).toBe("Local draft");
    expect(modelDraftHasRevisionConflict(5, 5, local, server)).toBe(false);
  });

  it("updates a relation using generated target choices", () => {
    const records = [record("p1", "products", 0, "Товар")];
    expect(updateModelRecordRelation(records, "p1", "product-category", ["category-a", "category-b"])[0]?.relations?.["product-category"]).toEqual(["category-a", "category-b"]);
  });

  it("surfaces and explicitly removes values and relations left by a structural model edit", () => {
    const stale: ModelContentRecord = {
      ...record("p1", "products", 0, "Товар"),
      values: { title: "Товар", retiredImage: "file-id" },
      relations: { "product-category": ["category-a"], "retired-relation": ["category-b"] },
    };
    expect(orphanedModelRecordData(stale, model)).toEqual({
      unknownFieldKeys: ["retiredImage"],
      unknownRelationIds: ["retired-relation"],
    });
    expect(removeOrphanedModelRecordData([stale], model, "p1")[0]).toMatchObject({
      values: { title: "Товар" },
      relations: { "product-category": ["category-a"] },
    });
  });

  it("removes dangling record references and editorial selections when a retired-entity record is deleted", () => {
    const source = { ...record("p1", "products", 0, "Товар"), relations: { "retired-relation": ["old-record", "other"] } };
    const retired = { ...record("old-record", "removed-entity", 0, "Устаревшая запись") };
    const selections: ModelContentSelection[] = [{ placementId: "placement", inputKey: "items", mode: "ids", recordIds: ["old-record", "p1"] }];
    const result = deleteModelRecord([source, retired], selections, model, "old-record");

    expect(result.records.map((item) => item.id)).toEqual(["p1"]);
    expect(result.records[0]?.relations?.["retired-relation"]).toEqual(["other"]);
    expect(result.selections[0]?.recordIds).toEqual(["p1"]);
  });
});
