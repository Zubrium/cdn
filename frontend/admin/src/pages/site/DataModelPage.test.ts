import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
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
  addDataModelRelation,
  changeDataModelFieldType,
  DATA_MODEL_ADD_ENTITY_CONTROL_ID,
  DATA_MODEL_NEW_RELATION_FROM_CONTROL_ID,
  dataModelEntityBindingImpacts,
  dataModelEntityNameControlID,
  dataModelEntityRemovalFocusTarget,
  dataModelFieldBindingImpacts,
  dataModelFieldNameControlID,
  dataModelFieldRemovalFocusTarget,
  dataModelAddFieldControlID,
  dataModelRelationNameControlID,
  dataModelRelationRemovalFocusTarget,
  dataModelRelationBindingImpacts,
  finalDataModelDragPosition,
  focusDataModelControl,
  findDataModelRelationConflict,
  moveDataModelField,
  parseDataModelEnumOptions,
  renameDataModelEntity,
  renameDataModelField,
  renameDataModelRelation,
  removeDataModelEntity,
  removeDataModelField,
  removeDataModelRelation,
  setDataModelEntityPosition,
  updateDataModelEntityCardinality,
  updateDataModelRelation,
} from "./DataModelPage";
import { DataModelGraphCanvas } from "./components/data-model/DataModelGraphCanvas";
import type { DataModelEntityCardActions } from "./components/data-model/DataModelEntityCard";

const productID = "11111111-1111-4111-8111-111111111111";
const categoryID = "22222222-2222-4222-8222-222222222222";
const titleFieldID = "33333333-3333-4333-8333-333333333333";
const statusFieldID = "44444444-4444-4444-8444-444444444444";
const relationID = "55555555-5555-4555-8555-555555555555";
const categoryNameFieldID = "abababab-abab-4bab-8bab-abababababab";

const model: DataModel = {
  formatVersion: 1,
  entities: [
    {
      id: productID,
      key: "product",
      name: "Товар",
      fields: [
        { id: titleFieldID, key: "title", name: "Название", type: "text", unique: true },
        { id: statusFieldID, key: "status", name: "Статус", type: "enum", options: ["Новый", "В продаже"] },
      ],
    },
    { id: categoryID, key: "category", name: "Категория", fields: [{ id: categoryNameFieldID, key: "name", name: "Название", type: "text" }] },
  ],
  relations: [{
    id: relationID,
    name: "принадлежит",
    fromEntityId: productID,
    toEntityId: categoryID,
    cardinality: "one-to-many",
  }],
  positions: { [productID]: { x: 50, y: 60 }, [categoryID]: { x: 420, y: 60 } },
};

const config: SiteConfig = {
  formatVersion: 3,
  pages: [],
  routeTree: { id: "site", type: "site", children: [] },
  dataModel: model,
};

describe("data model operations", () => {
  it("creates entities and fields with generated IDs, stable keys, and independent positions", () => {
    const withEntity = addDataModelEntity(model, () => "66666666-6666-4666-8666-666666666666");
    expect(withEntity.entities[2]).toEqual({
      id: "66666666-6666-4666-8666-666666666666",
      key: "entity_66666666666646668666666666666666",
      name: "Новая сущность",
      cardinality: "many",
      fields: [],
    });
    expect(withEntity.positions?.["66666666-6666-4666-8666-666666666666"]).toEqual({ x: 756, y: 36 });

    const withField = addDataModelField(withEntity, productID, () => "77777777-7777-4777-8777-777777777777");
    expect(withField.entities[0]?.fields.at(-1)).toMatchObject({
      id: "77777777-7777-4777-8777-777777777777",
      key: "field_77777777777747778777777777777777",
      type: "text",
    });
    expect(model.entities[0]?.fields).toHaveLength(2);
    expect(model.positions?.["66666666-6666-4666-8666-666666666666"]).toBeUndefined();
  });

  it("updates labels and enum metadata immutably while keeping enum options valid", () => {
    const renamed = renameDataModelEntity(model, productID, "Каталог");
    expect(renamed.entities[0]).toMatchObject({ id: productID, key: "product", name: "Каталог" });
    expect(model.entities[0]?.name).toBe("Товар");

    const renamedField = renameDataModelField(model, productID, titleFieldID, "Заголовок");
    expect(renamedField.entities[0]?.fields[0]).toMatchObject({ id: titleFieldID, key: "title", name: "Заголовок" });
    expect(model.entities[0]?.fields[0]).toMatchObject({ id: titleFieldID, key: "title", name: "Название" });
    const renamedRelation = renameDataModelRelation(model, relationID, "входит в");
    expect(renamedRelation.relations?.[0]).toMatchObject({ id: relationID, name: "входит в" });
    expect(model.relations?.[0]?.name).toBe("принадлежит");

    const toEnum = changeDataModelFieldType(model, productID, titleFieldID, "enum");
    expect(toEnum.entities[0]?.fields[0]).toMatchObject({ type: "enum", options: ["Вариант 1"], unique: true });
    const toMedia = changeDataModelFieldType(toEnum, productID, titleFieldID, "media");
    expect(toMedia.entities[0]?.fields[0]).toMatchObject({ type: "media", unique: false });
    expect(toMedia.entities[0]?.fields[0]).not.toHaveProperty("options");
    expect(parseDataModelEnumOptions("Новый\n В продаже \n\nНовый")).toEqual(["Новый", "В продаже"]);
  });

  it("defaults omitted entity cardinality to many and updates it immutably", () => {
    const singleton = updateDataModelEntityCardinality(model, productID, "singleton");
    expect(singleton.entities[0]?.cardinality).toBe("singleton");
    expect(model.entities[0]).not.toHaveProperty("cardinality");
    expect(updateDataModelEntityCardinality(singleton, "missing", "many")).toBe(singleton);
  });

  it("reorders fields without changing their generated identities", () => {
    const moved = moveDataModelField(model, productID, statusFieldID, -1);
    expect(moved.entities[0]?.fields.map((field) => field.id)).toEqual([statusFieldID, titleFieldID]);
    expect(moved.entities[0]?.fields.map((field) => field.key)).toEqual(["status", "title"]);
    expect(moveDataModelField(moved, productID, statusFieldID, -1)).toBe(moved);
    expect(moveDataModelField(moved, productID, "missing", 1)).toBe(moved);
    expect(model.entities[0]?.fields.map((field) => field.id)).toEqual([titleFieldID, statusFieldID]);
  });

  it("generates a fresh value key for each new field so a deleted key cannot be reused", () => {
    const first = addDataModelField(model, productID, () => "66666666-6666-4666-8666-666666666666");
    const firstField = first.entities[0]!.fields.at(-1)!;
    const afterRemoval = removeDataModelField(first, productID, firstField.id);
    const second = addDataModelField(afterRemoval, productID, () => "77777777-7777-4777-8777-777777777777");

    expect(second.entities[0]!.fields.at(-1)?.key).not.toBe(firstField.key);
    expect(second.entities[0]!.fields.at(-1)?.id).not.toBe(firstField.id);
  });

  it("adds only relations between existing entities and removes entities, fields, and relations without mutating the parent", () => {
    const linked = addDataModelRelation(model, {
      name: "  включает  ",
      fromEntityId: productID,
      toEntityId: categoryID,
      cardinality: "one-to-many",
    }, () => "88888888-8888-4888-8888-888888888888");
    expect(linked.relations?.at(-1)).toEqual({
      id: "88888888-8888-4888-8888-888888888888",
      name: "включает",
      fromEntityId: productID,
      toEntityId: categoryID,
      cardinality: "one-to-many",
    });
    expect(addDataModelRelation(model, {
      name: "invalid",
      fromEntityId: productID,
      toEntityId: "missing",
      cardinality: "one-to-one",
    })).toBe(model);
    expect(addDataModelRelation(model, {
      name: "ПРИНАДЛЕЖИТ",
      fromEntityId: productID,
      toEntityId: categoryID,
      cardinality: "one-to-one",
    })).toBe(model);

    const withoutField = removeDataModelField(model, productID, statusFieldID);
    expect(withoutField.entities[0]?.fields.map((field) => field.id)).toEqual([titleFieldID]);
    const withoutRelation = removeDataModelRelation(model, relationID);
    expect(withoutRelation.relations).toEqual([]);
    const withoutCategory = removeDataModelEntity(model, categoryID);
    expect(withoutCategory.entities.map((entity) => entity.id)).toEqual([productID]);
    expect(withoutCategory.relations).toEqual([]);
    expect(withoutCategory.positions).not.toHaveProperty(categoryID);
    expect(model.entities).toHaveLength(2);
    expect(model.relations).toHaveLength(1);
  });

  it("finds duplicate relations and updates the existing relation immutably", () => {
    const duplicate = {
      name: " ПРИНАДЛЕЖИТ ",
      fromEntityId: productID,
      toEntityId: categoryID,
      cardinality: "many-to-many" as const,
    };
    expect(findDataModelRelationConflict(model, duplicate)?.id).toBe(relationID);
    expect(findDataModelRelationConflict(model, duplicate, relationID)).toBeUndefined();
    expect(findDataModelRelationConflict(model, { ...duplicate, toEntityId: productID })).toBeUndefined();

    const changed = updateDataModelRelation(model, relationID, (relation) => ({ ...relation, cardinality: "many-to-many", name: "теги" }));
    expect(changed.relations?.[0]).toMatchObject({ id: relationID, cardinality: "many-to-many", name: "теги" });
    expect(model.relations?.[0]).toMatchObject({ cardinality: "one-to-many", name: "принадлежит" });
  });

  it("reports impacted typed bindings without reading record values", () => {
    const boundConfig: SiteConfig = {
      ...config,
      pages: [{
        id: "catalog-page",
        name: "Каталог",
        kind: "collection-list",
        placements: [{
          id: "catalog-placement",
          build: { featureKey: "catalog", version: "2.0.0" },
          slot: "main",
          dataBindings: [{
            inputKey: "products",
            entityId: productID,
            fields: [
              { inputPath: "/title", fieldId: categoryNameFieldID, relationIds: [relationID] },
              { inputPath: "/status", fieldId: statusFieldID },
            ],
            lookup: { argument: "slug", fieldId: titleFieldID },
          }],
        }],
      }],
    };

    expect(dataModelEntityBindingImpacts(boundConfig, productID)).toEqual([{
      pageName: "Каталог",
      featureKey: "catalog",
      version: "2.0.0",
      inputKey: "products",
      paths: ["/title", "/status", "маршрут: slug"],
    }]);
    expect(dataModelFieldBindingImpacts(boundConfig, productID, titleFieldID)).toEqual([{
      pageName: "Каталог",
      featureKey: "catalog",
      version: "2.0.0",
      inputKey: "products",
      paths: ["маршрут: slug"],
    }]);
    expect(dataModelFieldBindingImpacts(boundConfig, categoryID, categoryNameFieldID)).toEqual([{
      pageName: "Каталог",
      featureKey: "catalog",
      version: "2.0.0",
      inputKey: "products",
      paths: ["/title"],
    }]);
    expect(dataModelRelationBindingImpacts(boundConfig, relationID)).toEqual([{
      pageName: "Каталог",
      featureKey: "catalog",
      version: "2.0.0",
      inputKey: "products",
      paths: ["/title"],
    }]);
    expect(JSON.stringify(dataModelEntityBindingImpacts(boundConfig, productID))).not.toContain("record");
  });

  it("finds bindings to fields and relations reached through another entity", () => {
    const categoryTitleID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
    const nestedConfig: SiteConfig = {
      ...config,
      dataModel: {
        ...model,
        entities: model.entities.map((entity) => entity.id === categoryID
          ? { ...entity, fields: [{ id: categoryTitleID, key: "name", name: "Название", type: "text" }] }
          : entity),
      },
      pages: [{
        id: "catalog-page",
        name: "Каталог",
        kind: "collection-list",
        placements: [{
          id: "catalog-placement",
          build: { featureKey: "catalog", version: "2.0.0" },
          slot: "main",
          dataBindings: [{
            inputKey: "products",
            entityId: productID,
            fields: [{ inputPath: "/category/name", fieldId: categoryTitleID, relationIds: [relationID] }],
          }],
        }],
      }],
    };

    expect(dataModelFieldBindingImpacts(nestedConfig, categoryID, categoryTitleID)[0]?.paths).toEqual(["/category/name"]);
    expect(dataModelEntityBindingImpacts(nestedConfig, categoryID)[0]?.paths).toEqual(["/category/name"]);
    expect(dataModelRelationBindingImpacts(nestedConfig, relationID)[0]?.paths).toEqual(["/category/name"]);
  });

  it("reports detail SEO field references before deleting their model fields or entity", () => {
    const detailConfig: SiteConfig = {
      ...config,
      pages: [{
        id: "product-detail",
        name: "Карточка товара",
        kind: "detail",
        placements: [{
          id: "product-detail-placement",
          build: { featureKey: "catalog-detail", version: "2.0.0" },
          slot: "main",
          arguments: { slug: { source: "path", pathArg: "slug" } },
          dataBindings: [{
            inputKey: "product",
            entityId: productID,
            fields: [{ inputPath: "/title", fieldId: titleFieldID }],
            lookup: { argument: "slug", fieldId: titleFieldID },
            detailSEO: { titleFieldId: titleFieldID, descriptionFieldId: titleFieldID },
          }],
        }],
      }],
    };

    expect(dataModelFieldBindingImpacts(detailConfig, productID, titleFieldID)).toEqual([{
      pageName: "Карточка товара",
      featureKey: "catalog-detail",
      version: "2.0.0",
      inputKey: "product",
      paths: ["/title", "SEO: заголовок страницы", "SEO: описание страницы", "маршрут: slug"],
    }]);
    expect(dataModelEntityBindingImpacts(detailConfig, productID)[0]?.paths).toEqual([
      "/title",
      "SEO: заголовок страницы",
      "SEO: описание страницы",
      "маршрут: slug",
    ]);
  });

  it("commits only the active pointer's completed drag and discards cancelled drags", () => {
    const drag = { id: productID, pointerId: 7, x: 10, y: 20, originX: 50, originY: 60 };
    const preview = { id: productID, x: 90, y: 120 };

    expect(finalDataModelDragPosition(drag, preview, 7)).toEqual(preview);
    expect(finalDataModelDragPosition(drag, preview, 8)).toBeUndefined();
    expect(finalDataModelDragPosition(drag, preview, 7, true)).toBeUndefined();
    expect(finalDataModelDragPosition(drag, null, 7)).toBeUndefined();
  });

  it("clamps keyboard layout movement to valid coordinates", () => {
    const moved = setDataModelEntityPosition(model, productID, -15, 120000);
    expect(moved.positions?.[productID]).toEqual({ x: 0, y: 100000 });
    expect(setDataModelEntityPosition(moved, "missing", 30, 30)).toBe(moved);
    expect(model.positions?.[productID]).toEqual({ x: 50, y: 60 });
  });

  it("selects a surviving named control after removals and focuses generated names after additions", () => {
    const addedEntity = addDataModelEntity(model, () => "66666666-6666-4666-8666-666666666666");
    const addedField = addDataModelField(model, productID, () => "77777777-7777-4777-8777-777777777777");
    const addedRelation = addDataModelRelation(model, {
      name: "включает",
      fromEntityId: productID,
      toEntityId: categoryID,
      cardinality: "one-to-many",
    }, () => "88888888-8888-4888-8888-888888888888");

    expect(dataModelEntityNameControlID(addedEntity.entities[2]!.id)).toBe("data-model-entity-name-66666666-6666-4666-8666-666666666666");
    expect(dataModelFieldNameControlID(addedField.entities[0]!.fields[2]!.id)).toBe("data-model-field-name-77777777-7777-4777-8777-777777777777");
    expect(dataModelRelationNameControlID(addedRelation.relations!.at(-1)!.id)).toBe("data-model-relation-name-88888888-8888-4888-8888-888888888888");

    expect(dataModelEntityRemovalFocusTarget(model, productID)).toBe(dataModelEntityNameControlID(categoryID));
    expect(dataModelEntityRemovalFocusTarget({ ...model, entities: [model.entities[0]!] }, productID)).toBe(DATA_MODEL_ADD_ENTITY_CONTROL_ID);
    expect(dataModelFieldRemovalFocusTarget(model.entities[0]!, titleFieldID)).toBe(dataModelFieldNameControlID(statusFieldID));
    expect(dataModelFieldRemovalFocusTarget({ ...model.entities[0]!, fields: [model.entities[0]!.fields[0]!] }, titleFieldID)).toBe(dataModelAddFieldControlID(productID));
    expect(dataModelRelationRemovalFocusTarget(addedRelation, relationID)).toBe(dataModelRelationNameControlID("88888888-8888-4888-8888-888888888888"));
    expect(dataModelRelationRemovalFocusTarget(model, relationID)).toBe(DATA_MODEL_NEW_RELATION_FROM_CONTROL_ID);

    const targetID = dataModelFieldRemovalFocusTarget(model.entities[0]!, titleFieldID);
    let activeElement: { focus: (options?: FocusOptions) => void } | null = null;
    const focusable = { focus: (options?: FocusOptions) => { expect(options).toEqual({ preventScroll: true }); activeElement = focusable; } };
    expect(focusDataModelControl(targetID, (id) => id === targetID ? focusable : null)).toBe(true);
    expect(activeElement).toBe(focusable);
    expect(focusDataModelControl("missing", () => null)).toBe(false);
  });
});

describe("data model page accessibility", () => {
  beforeEach(() => {
    mocks.useSiteWorkspace.mockReturnValue({ config, update: vi.fn() });
    mocks.useConfirm.mockReturnValue(vi.fn(async () => false));
  });

  it("defaults to an editable keyboard list with named controls and optional CMS fields", () => {
    const markup = renderToStaticMarkup(createElement(DataModelPage));

    expect(markup).toContain('aria-label="Сущности модели, список"');
    expect(markup).toContain('<ol class="grid list-none');
    expect(markup).toContain('aria-pressed="true"');
    expect(markup).toContain("Карточки модели");
    expect(markup).toContain("ER-граф");
    expect(markup).toContain("Переименование сохраняет системные ID и привязки.");
    expect(markup).toContain("При удалении покажем затронутые размещения");
    expect(markup).toContain("Один к одному");
    expect(markup).toContain("Один ко многим");
    expect(markup).toContain("Многие ко многим");
    expect(markup).toContain(`aria-labelledby="data-model-entity-title-${productID}"`);
    expect(markup).toContain(`id="data-model-entity-title-${productID}"`);
    expect(markup).toContain("Название сущности");
    expect(markup).toContain('aria-label="Количество записей сущности Товар"');
    expect(markup).toContain('<option value="many" selected="">Много записей</option>');
    expect(markup).toContain('<option value="singleton">Одна запись</option>');
    expect(markup).toContain("Название поля");
    expect(markup).toContain("Тип поля");
    expect(markup).toContain(`aria-label="Варианты поля Статус сущности Товар"`);
    expect(markup).toContain(`aria-label="Удалить поле Статус из сущности Товар"`);
    expect(markup).toContain(`aria-label="Удалить связь Товар — принадлежит — Категория"`);
    expect(markup).toContain(`aria-label="Название связи Товар — Категория"`);
    expect(markup).toContain(`aria-label="Тип связи Товар — принадлежит — Категория"`);
    expect(markup).toContain(`aria-label="Переместить поле Название выше"`);
    expect(markup).toContain(`aria-label="Переместить поле Статус ниже"`);
    expect(markup).toContain(`id="${dataModelEntityNameControlID(productID)}"`);
    expect(markup).toContain(`id="${dataModelFieldNameControlID(titleFieldID)}"`);
    expect(markup).toContain(`id="${dataModelRelationNameControlID(relationID)}"`);
    expect(markup).toContain('id="data-model-new-relation-from"');
    expect(markup).toContain('id="data-model-new-relation-name"');
    expect(markup).toContain("Все поля записей необязательны");
    expect(markup).not.toContain("обязательность редактируются");
  });

  it("associates duplicate relationship errors with fields and keeps the conflicting value visible", () => {
    const duplicatedName = "использует";
    const duplicate: DataModel = {
      ...model,
      relations: [
        { ...model.relations![0]!, name: duplicatedName },
        {
          id: "99999999-9999-4999-8999-999999999999",
          name: duplicatedName,
          fromEntityId: productID,
          toEntityId: categoryID,
          cardinality: "many-to-many",
        },
      ],
    };
    mocks.useSiteWorkspace.mockReturnValue({ config: { ...config, dataModel: duplicate }, update: vi.fn() });

    const markup = renderToStaticMarkup(createElement(DataModelPage));
    const firstErrorID = `data-model-relation-name-error-${relationID}`;
    const secondRelationID = "99999999-9999-4999-8999-999999999999";
    const secondErrorID = `data-model-relation-name-error-${secondRelationID}`;

    expect(markup).toContain(`id="${dataModelRelationNameControlID(relationID)}" required="" aria-invalid="true" aria-describedby="${firstErrorID}"`);
    expect(markup).toContain(`aria-describedby="${firstErrorID}"`);
    expect(markup).toContain(`id="${firstErrorID}" class="text-destructive" role="alert">Такая связь уже есть. Измените название или направление.</span>`);
    expect(markup).toContain(`id="${dataModelRelationNameControlID(secondRelationID)}" required="" aria-invalid="true" aria-describedby="${secondErrorID}"`);
    expect(markup).toContain(`aria-describedby="${secondErrorID}"`);
    expect(markup).toContain(`id="${secondErrorID}" class="text-destructive" role="alert">Такая связь уже есть. Измените название или направление.</span>`);
    expect(markup).toContain(`value="${duplicatedName}"`);
  });

  it("marks empty or overlong structural names as invalid", () => {
    const invalidConfig: SiteConfig = {
      ...config,
      dataModel: {
        ...model,
        entities: model.entities.map((entity, index) => index === 0
          ? { ...entity, name: " ", fields: entity.fields.map((field, fieldIndex) => fieldIndex === 0 ? { ...field, name: "x".repeat(121) } : field) }
          : entity),
      },
    };
    mocks.useSiteWorkspace.mockReturnValue({ config: invalidConfig, update: vi.fn() });
    const markup = renderToStaticMarkup(createElement(DataModelPage));

    expect(markup).toContain('aria-invalid="true"');
    expect(markup).toContain("Укажите название длиной до 120 символов.");
  });

  it("keeps a single stable fallback focus target when the model is empty", () => {
    mocks.useSiteWorkspace.mockReturnValue({
      config: { ...config, dataModel: { ...model, entities: [], relations: [], positions: {} } },
      update: vi.fn(),
    });
    const markup = renderToStaticMarkup(createElement(DataModelPage));

    expect(markup.match(new RegExp(`id="${DATA_MODEL_ADD_ENTITY_CONTROL_ID}"`, "g"))).toHaveLength(1);
    expect(markup).toContain("Создать первую сущность");
  });
});

describe("data model graph component", () => {
  it("renders labelled relations and keyboard movement controls", () => {
    const noOp = () => {};
    const actions: DataModelEntityCardActions = {
      onAddField: noOp,
      onChangeFieldType: noOp,
      onChangeEntityCardinality: noOp,
      onMoveEntity: noOp,
      onMoveField: noOp,
      onRemoveEntity: noOp,
      onRemoveField: noOp,
      onRenameEntity: noOp,
      onRenameField: noOp,
      onUpdateField: noOp,
    };
    const markup = renderToStaticMarkup(createElement(DataModelGraphCanvas, { model, ...actions }));

    expect(markup).toContain('aria-label="Граф связей модели данных"');
    expect(markup).toContain('aria-label="Схема связей. Для перемещения используйте кнопки на карточках"');
    expect(markup).toContain("принадлежит · Один ко многим");
    expect(markup).toContain('aria-label="Количество записей сущности Товар"');
    expect(markup).toContain('aria-label="Сдвинуть сущность Товар влево"');
    expect(markup).toContain('aria-label="Сдвинуть сущность Категория вправо"');
  });
});
