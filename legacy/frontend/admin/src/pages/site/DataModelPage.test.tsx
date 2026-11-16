import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DataModel, SiteConfig } from "@/types";

const mocks = vi.hoisted(() => ({
  useConfirm: vi.fn(),
  useSiteWorkspace: vi.fn(),
}));

vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));
vi.mock("./SiteWorkspace", () => ({ useSiteWorkspace: mocks.useSiteWorkspace }));

import DataModelPage, {
  addDataModelEntity,
  addDataModelField,
  changeDataModelFieldType,
  dataModelEntityBindingImpacts,
  dataModelFieldBindingImpacts,
  moveDataModelField,
  removeDataModelEntity,
  removeDataModelField,
  renameDataModelEntity,
  renameDataModelField,
  reorderDataModelFields,
  updateDataModelEntityCardinality,
} from "./DataModelPage";
import DataModelEntityPage from "./DataModelEntityPage";

const productID = "11111111-1111-4111-8111-111111111111";
const categoryID = "22222222-2222-4222-8222-222222222222";
const titleFieldID = "33333333-3333-4333-8333-333333333333";
const statusFieldID = "44444444-4444-4444-8444-444444444444";

const model: DataModel = {
  formatVersion: 1,
  entities: [
    {
      id: productID,
      key: "product",
      name: "Товар",
      cardinality: "many",
      fields: [
        { id: titleFieldID, key: "title", name: "Название", type: "text", unique: true },
        { id: statusFieldID, key: "status", name: "Статус", type: "enum", options: ["Новый", "В продаже"] },
      ],
    },
    { id: categoryID, key: "category", name: "Категория", cardinality: "singleton", fields: [] },
  ],
  positions: { [productID]: { x: 50, y: 60 } },
};

const config: SiteConfig = {
  formatVersion: 3,
  pages: [],
  routeTree: { id: "site", type: "site", children: [] },
  dataModel: model,
};

function renderWithRoute(element: React.ReactNode, path: string, initialEntry: string) {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes><Route path={path} element={element} /></Routes>
    </MemoryRouter>,
  );
}

describe("flat data model", () => {
  beforeEach(() => {
    mocks.useConfirm.mockReset();
    mocks.useConfirm.mockResolvedValue(true);
    mocks.useSiteWorkspace.mockReturnValue({ config, update: vi.fn(), dirty: true, saving: false, snapshot: null });
  });

  it("creates models with a readable key, selected cardinality and retained layout position", () => {
    const withEntity = addDataModelEntity(model, () => "66666666-6666-4666-8666-666666666666", "Группы", "singleton");
    expect(withEntity.entities.at(-1)).toEqual({
      id: "66666666-6666-4666-8666-666666666666",
      key: "model_66666666666646668666666666666666",
      name: "Группы",
      cardinality: "singleton",
      fields: [],
    });
    expect(withEntity.positions?.["66666666-6666-4666-8666-666666666666"]).toBeDefined();
    expect(withEntity).not.toHaveProperty("relations");
  });

  it("renames models and fields and changes cardinality without mutating the source", () => {
    const renamed = renameDataModelEntity(model, productID, "Каталог");
    expect(renamed.entities[0]?.name).toBe("Каталог");
    expect(model.entities[0]?.name).toBe("Товар");
    expect(renameDataModelField(model, productID, titleFieldID, "Заголовок").entities[0]?.fields[0]?.name).toBe("Заголовок");
    expect(updateDataModelEntityCardinality(model, productID, "singleton").entities[0]?.cardinality).toBe("singleton");
  });

  it("adds, reorders, changes and removes fields while preserving identities", () => {
    const withField = addDataModelField(model, productID, () => "77777777-7777-4777-8777-777777777777");
    expect(withField.entities[0]?.fields).toHaveLength(3);
    const reordered = reorderDataModelFields(withField, productID, [statusFieldID, titleFieldID, "77777777-7777-4777-8777-777777777777"]);
    expect(reordered.entities[0]?.fields.map((field) => field.id)).toEqual([statusFieldID, titleFieldID, "77777777-7777-4777-8777-777777777777"]);
    expect(moveDataModelField(reordered, productID, statusFieldID, 1).entities[0]?.fields.map((field) => field.id)).toEqual([titleFieldID, statusFieldID, "77777777-7777-4777-8777-777777777777"]);
    expect(removeDataModelField(model, productID, statusFieldID).entities[0]?.fields.map((field) => field.id)).toEqual([titleFieldID]);
    expect(changeDataModelFieldType(model, productID, titleFieldID, "enum").entities[0]?.fields[0]).toMatchObject({ type: "enum", options: ["Вариант 1"] });
  });

  it("removes a model without retaining relation data", () => {
    const removed = removeDataModelEntity(model, categoryID);
    expect(removed.entities.map((entity) => entity.key)).toEqual(["product"]);
    expect(removed.positions).not.toHaveProperty(categoryID);
    expect(removed).not.toHaveProperty("relations");
  });

  it("reports only direct model binding impacts", () => {
    const boundConfig: SiteConfig = {
      ...config,
      pages: [{
        id: "page",
        name: "Каталог",
        kind: "collection-list",
        placements: [{
          id: "placement",
          build: { featureKey: "catalog", version: "2.0.0" },
          slot: "main",
          dataBindings: [{
            inputKey: "products",
            entityId: productID,
            fields: [{ inputPath: "/title", fieldId: titleFieldID }, { inputPath: "/status", fieldId: statusFieldID }],
            lookup: { argument: "slug", fieldId: titleFieldID },
          }],
        }],
      }],
    };
    expect(dataModelEntityBindingImpacts(boundConfig, productID)[0]?.paths).toEqual(["/title", "/status", "маршрут: slug"]);
    expect(dataModelFieldBindingImpacts(boundConfig, productID, titleFieldID)[0]?.paths).toEqual(["/title", "маршрут: slug"]);
  });

  it("renders a compact model list with snapshot deep links and no relation navigation", () => {
    const html = renderWithRoute(createElement(DataModelPage), "/site/data-model", "/site/data-model?snapshot=snapshot-1");
    expect(html).toContain("Модель данных");
    expect(html).toContain("Коллекции");
    expect(html).toContain("Одиночные настройки");
    expect(html).toContain("href=\"/site/data-model/product?snapshot=snapshot-1\"");
    expect(html).not.toMatch(/Связ(и|ей|ь)/u);
    expect(html).not.toContain(productID);
  });

  it("renders a selected model with fields, ordering controls and no relation tab", () => {
    const html = renderWithRoute(createElement(DataModelEntityPage), "/site/data-model/:model", "/site/data-model/product?snapshot=snapshot-1");
    expect(html).toContain("Название модели");
    expect(html).toContain("Добавить поле");
    expect(html).toContain("Перетащить поле Название");
    expect(html).toContain("href=\"/site/data-model?snapshot=snapshot-1\"");
    expect(html).not.toContain("Связи");
  });
});
