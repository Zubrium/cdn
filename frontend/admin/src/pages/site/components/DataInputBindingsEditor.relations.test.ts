import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { StaticRouter } from "react-router-dom/server";
import { describe, expect, it } from "vitest";
import type { DataInputBinding, DataInputPort, DataModel } from "@/types";
import { compatibleRelationFieldChoices, compatibleScalarFieldChoices, DataInputBindingsEditor, relationFieldBinding, scalarRelationFieldBinding } from "./DataInputBindingsEditor";

const productID = "11111111-1111-4111-8111-111111111111";
const productNameID = "22222222-2222-4222-8222-222222222222";
const categoryID = "33333333-3333-4333-8333-333333333333";
const categoryNameID = "44444444-4444-4444-8444-444444444444";
const tagID = "55555555-5555-4555-8555-555555555555";
const tagNameID = "66666666-6666-4666-8666-666666666666";
const productCategoryRelationID = "77777777-7777-4777-8777-777777777777";
const productTagsRelationID = "88888888-8888-4888-8888-888888888888";

const model: DataModel = {
  formatVersion: 1,
  entities: [
    { id: productID, key: "product", name: "Товар", fields: [{ id: productNameID, key: "title", name: "Название товара", type: "text" }] },
    { id: categoryID, key: "category", name: "Категория", fields: [{ id: categoryNameID, key: "name", name: "Название категории", type: "text" }] },
    { id: tagID, key: "tag", name: "Метка", fields: [{ id: tagNameID, key: "label", name: "Название метки", type: "text" }] },
  ],
  relations: [
    { id: productCategoryRelationID, name: "Категория товара", fromEntityId: productID, toEntityId: categoryID, cardinality: "one-to-one" },
    { id: productTagsRelationID, name: "Метки товара", fromEntityId: productID, toEntityId: tagID, cardinality: "many-to-many" },
  ],
};

function render(ports: DataInputPort[], bindings: DataInputBinding[] = []) {
  return renderToStaticMarkup(createElement(StaticRouter, { location: "/" }, createElement(DataInputBindingsEditor, {
    ports,
    dataModel: model,
    bindings,
    arguments: {},
    routeCaptures: [],
    snapshotId: "snapshot-1",
    onChange: () => {},
  })));
}

describe("DataInputBindingsEditor relation paths", () => {
  it("offers directed reachable fields with wildcard count matching relation cardinality", () => {
    const local = compatibleRelationFieldChoices(model, productID, { path: "/title", type: "text" });
    expect(local.map((choice) => [choice.field.id, choice.relationIds])).toContainEqual([productNameID, []]);

    const singular = compatibleRelationFieldChoices(model, productID, { path: "/category/name", type: "text" });
    expect(singular.map((choice) => [choice.field.id, choice.relationIds])).toContainEqual([categoryNameID, [productCategoryRelationID]]);
    expect(singular.map((choice) => choice.field.id)).not.toContain(tagNameID);
    expect(relationFieldBinding("/category/name", singular.find((choice) => choice.field.id === categoryNameID)!)).toEqual({
      inputPath: "/category/name", fieldId: categoryNameID, relationIds: [productCategoryRelationID],
    });

    const collection = compatibleRelationFieldChoices(model, productID, { path: "/tags/*/label", type: "text" });
    expect(collection.map((choice) => [choice.field.id, choice.relationIds])).toContainEqual([tagNameID, [productTagsRelationID]]);
    expect(collection.map((choice) => choice.field.id)).not.toContain(categoryNameID);

    expect(compatibleRelationFieldChoices(model, productID, { path: "/tags/label", type: "text" }).map((choice) => choice.field.id)).not.toContain(tagNameID);
    expect(compatibleRelationFieldChoices(model, productID, { path: "/tags/*/extra/*/label", type: "text" }).map((choice) => choice.field.id)).not.toContain(tagNameID);
    expect(compatibleRelationFieldChoices(model, categoryID, { path: "/title", type: "text" }).map((choice) => choice.field.id)).not.toContain(productNameID);
  });

  it("offers scalar fields at the root or through one-to-one paths and serializes relation IDs", () => {
    const scalar: DataInputPort = { key: "subtitle", kind: "scalar", type: "text" };
    const choices = compatibleScalarFieldChoices(model, productID, scalar);

    expect(choices.map((choice) => [choice.field.id, choice.relationIds])).toContainEqual([productNameID, []]);
    expect(choices.map((choice) => [choice.field.id, choice.relationIds])).toContainEqual([categoryNameID, [productCategoryRelationID]]);
    expect(choices.map((choice) => choice.field.id)).not.toContain(tagNameID);
    expect(scalarRelationFieldBinding(choices.find((choice) => choice.field.id === categoryNameID)!)).toEqual({
      fieldId: categoryNameID,
      relationIds: [productCategoryRelationID],
    });
  });

  it("renders relation names and stores stable field and relation IDs without exposing JSON paths", () => {
    const ports: DataInputPort[] = [{ key: "products", kind: "collection", fields: [
      { path: "/category/name", type: "text" },
      { path: "/tags/*/label", type: "text" },
    ] }];
    const bindings: DataInputBinding[] = [{ inputKey: "products", entityId: productID, fields: [
      { inputPath: "/category/name", fieldId: categoryNameID, relationIds: [productCategoryRelationID] },
      { inputPath: "/tags/*/label", fieldId: tagNameID, relationIds: [productTagsRelationID] },
    ] }];
    const markup = render(ports, bindings);

    expect(markup).toContain("Товар → связь «Категория товара» → Категория → Название категории");
    expect(markup).toContain("Товар → связь «Метки товара» → Метка → Название метки");
    expect(markup).toContain(`value="${productCategoryRelationID}|${categoryNameID}" selected=""`);
    expect(markup).toContain(`value="${productTagsRelationID}|${tagNameID}" selected=""`);
    expect(markup).toContain("products → category → name");
    expect(markup).toContain("products → tags → элемент списка → label");
    expect(markup).not.toContain("/category/name");
    expect(markup).not.toContain("/tags/*/label");
    expect(markup).not.toContain("Загрузить");
  });

  it("renders a selected scalar relation path by readable name and excludes multi-target paths", () => {
    const ports: DataInputPort[] = [{ key: "subtitle", kind: "scalar", type: "text" }];
    const bindings: DataInputBinding[] = [{
      inputKey: "subtitle",
      entityId: productID,
      fieldId: categoryNameID,
      relationIds: [productCategoryRelationID],
    }];
    const markup = render(ports, bindings);

    expect(markup).toContain('aria-label="Поле модели для scalar subtitle"');
    expect(markup).toContain("Товар → связь «Категория товара» → Категория → Название категории");
    expect(markup).toContain(`value="${productCategoryRelationID}|${categoryNameID}" selected=""`);
    expect(markup).not.toContain("Название метки");
    expect(markup).not.toContain("Загрузить");

    const invalidMarkup = render(ports, [{
      inputKey: "subtitle",
      entityId: productID,
      fieldId: tagNameID,
      relationIds: [productTagsRelationID],
    }]);
    expect(invalidMarkup).toContain("Сохранённый путь поля больше не подходит");
    expect(invalidMarkup).not.toContain(`value="${productTagsRelationID}|${tagNameID}"`);
  });

  it("reports stale directed paths and wildcard/cardinality mismatches inline", () => {
    const wrongDirection = render([{ key: "products", kind: "collection", fields: [
      { path: "/title", type: "text" },
    ] }], [{ inputKey: "products", entityId: categoryID, fields: [
      { inputPath: "/title", fieldId: productNameID },
    ] }]);
    expect(wrongDirection).toContain("Это сопоставление больше не подходит модели.");
    expect(wrongDirection).toContain('aria-invalid="true"');

    const wrongWildcard = render([{ key: "products", kind: "collection", fields: [
      { path: "/category/*/extra/*/name", type: "text" },
    ] }], [{ inputKey: "products", entityId: productID, fields: [] }]);
    expect(wrongWildcard).toContain("Для этого входа нужно 2 связей со множественными записями");
    expect(wrongWildcard).not.toContain(`value="${productCategoryRelationID}|${categoryNameID}" selected=""`);
  });
});
