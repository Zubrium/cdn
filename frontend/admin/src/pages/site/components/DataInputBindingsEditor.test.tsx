import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { DataInputBinding, DataInputPort, DataModel } from "@/types";
import { DataInputBindingsEditor } from "./DataInputBindingsEditor";

const entityID = "11111111-1111-4111-8111-111111111111";
const slugID = "22222222-2222-4222-8222-222222222222";
const titleID = "33333333-3333-4333-8333-333333333333";
const mediaID = "44444444-4444-4444-8444-444444444444";

const model: DataModel = {
  formatVersion: 1,
  entities: [{ id: entityID, key: "product", name: "Товар", fields: [
    { id: slugID, key: "slug", name: "Адрес", type: "text", unique: true },
    { id: titleID, key: "title", name: "Название", type: "text" },
    { id: mediaID, key: "image", name: "Изображение", type: "media" },
  ] }],
};

function render(ports: DataInputPort[], bindings: DataInputBinding[] = [], selectedModel: DataModel | undefined = model) {
  return renderToStaticMarkup(createElement(MemoryRouter, null, createElement(DataInputBindingsEditor, {
    ports,
    dataModel: selectedModel,
    bindings,
    arguments: { slug: { source: "path", pathArg: "slug" } },
    routeCaptures: ["slug"],
    snapshotId: "snapshot-1",
    onChange: () => {},
  })));
}

describe("DataInputBindingsEditor", () => {
  it("shows only type-compatible model fields for a shared collection input", () => {
    const markup = render([{ key: "products", kind: "collection", fields: [
      { path: "/slug", type: "text" },
      { path: "/title", type: "text" },
    ] }], [{ inputKey: "products", entityId: entityID, fields: [
      { inputPath: "/slug", fieldId: slugID },
      { inputPath: "/title", fieldId: titleID },
    ] }]);

    expect(markup).toContain("Подключение данных модели");
    expect(markup).toContain("в редакторе контента");
    expect(markup).toContain("Товар → Адрес");
    expect(markup).toContain("Товар → Название");
    expect(markup).not.toContain("Изображение · image");
    expect(markup).not.toContain("Загрузить");
  });

  it("offers a route-bound unique model field for a detail record", () => {
    const markup = render([{ key: "product", kind: "record", fields: [
      { path: "/slug", type: "text" },
      { path: "/title", type: "text" },
    ] }], [{ inputKey: "product", entityId: entityID, fields: [
      { inputPath: "/slug", fieldId: slugID },
      { inputPath: "/title", fieldId: titleID },
    ], lookup: { argument: "slug", fieldId: slugID } }]);

    expect(markup).toContain("Запись из параметра маршрута");
    expect(markup).toContain("slug ← slug");
    expect(markup).toContain("Уникальное поле модели");
  });

  it("shows inline errors for a missing entity target and an unmapped compatible input field", () => {
    const port: DataInputPort = { key: "products", kind: "collection", fields: [{ path: "/title", type: "text" }] };
    const missingTarget = render([port], [{ inputKey: "products", entityId: "retired-entity", fields: [] }]);
    expect(missingTarget).toContain('id="data-input-products-entity"');
    expect(missingTarget).toContain('aria-invalid="true"');
    expect(missingTarget).toContain("Сохранённая сущность больше не существует в этой модели.");

    const missingField = render([port], [{ inputKey: "products", entityId: entityID, fields: [] }]);
    expect(missingField).toContain("Выберите совместимое поле модели для этого входа.");
    expect(missingField).toContain('id="data-input-products-mapping-0-error"');
  });
});
