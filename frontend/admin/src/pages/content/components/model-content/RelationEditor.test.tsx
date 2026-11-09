import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { DataModelEntity, DataModelRelation, ModelContentRecord } from "@/types";
import { RelationEditor } from "./RelationEditor";

const sourceEntity: DataModelEntity = { id: "products", key: "product", name: "Товары", fields: [] };
const targetEntity: DataModelEntity = { id: "categories", key: "category", name: "Категории", fields: [{ id: "category-title", key: "title", name: "Название", type: "text" }] };
const relation: DataModelRelation = {
  id: "product-category",
  name: "Категория товара",
  fromEntityId: sourceEntity.id,
  toEntityId: targetEntity.id,
  cardinality: "one-to-one",
};
const sourceRecord: ModelContentRecord = {
  id: "product-1",
  entityId: sourceEntity.id,
  sortOrder: 0,
  values: {},
  relations: {},
};
const targetRecord: ModelContentRecord = {
  id: "category-1",
  entityId: targetEntity.id,
  sortOrder: 0,
  values: { title: "Категория" },
};

describe("model content relation editor", () => {
  it("allows clearing an existing one-to-one link without entering an identifier", () => {
    const html = renderToStaticMarkup(createElement(RelationEditor, {
      relation,
      targetEntity,
      targetRecords: [targetRecord],
      currentRecord: sourceRecord,
      persistedRecordIDs: new Set([targetRecord.id]),
      onChange: () => {},
    }));

    expect(html).toContain("Не связано");
    expect(html).toContain('type="radio"');
    expect(html).toContain('checked=""');
    expect(html).toContain("Категория");
    expect(html).not.toContain(">product-1<");
  });

  it("surfaces stale relation targets and asks for explicit cleanup", () => {
    const html = renderToStaticMarkup(createElement(RelationEditor, {
      relation,
      targetEntity,
      targetRecords: [targetRecord],
      currentRecord: { ...sourceRecord, relations: { [relation.id]: ["deleted-category"] } },
      persistedRecordIDs: new Set([targetRecord.id]),
      onChange: () => {},
      onConfirmCleanup: async () => true,
    }));

    expect(html).toContain('role="alert"');
    expect(html).toContain("больше нет в сущности «Категории»");
    expect(html).toContain("Удалить недоступные ссылки");
    expect(html).not.toContain("deleted-category");
  });

  it("prevents a target from acquiring two owners in one-to-one/one-to-many relations", () => {
    const alreadyOwned = { ...targetRecord, id: "category-2", sortOrder: 1, values: { title: "Уже связанная категория" } };
    const html = renderToStaticMarkup(createElement(RelationEditor, {
      relation,
      targetEntity,
      targetRecords: [targetRecord, alreadyOwned],
      currentRecord: sourceRecord,
      persistedRecordIDs: new Set([targetRecord.id, alreadyOwned.id]),
      recordIDsOwnedByOther: new Set([alreadyOwned.id]),
      onChange: () => {},
    }));

    expect(html).toContain("Уже связанная категория");
    expect(html).toContain("· уже связана");
  });
});
