import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { PlacementSelections } from "./model-content-editor";
import { ModelContentRecordWorkspace } from "./components/model-content/ModelContentRecordWorkspace";
import type { DataModel, DataModelEntity, ModelContentRecord, ModelContentSelection, ModelContentWorkspace } from "@/types";

const model: DataModel = {
  formatVersion: 1,
  entities: [{
    id: "products",
    key: "product",
    name: "Товары",
    fields: [
      { id: "title-field", key: "title", name: "Название", type: "text" },
      { id: "slug-field", key: "slug", name: "Адрес", type: "text", unique: true },
    ],
  }],
  relations: [],
};

const savedRecord: ModelContentRecord = {
  id: "saved-product",
  entityId: "products",
  sortOrder: 0,
  values: { title: "Сохранённый товар", slug: "saved-product" },
};

const unsavedRecord: ModelContentRecord = {
  id: "local:unsaved-product",
  entityId: "products",
  sortOrder: 1,
  values: { title: "Новый товар", slug: "new-product" },
};

const workspace = {
  placements: [
    {
      id: "scalar-placement",
      pageId: "home",
      pageName: "Главная",
      featureKey: "main-data",
      featureName: "Преимущества",
      version: "2.0.1",
      inputs: [{ inputKey: "rating", kind: "scalar", entityId: "products", fields: [{ path: "", fieldId: "title-field", type: "text" }] }],
    },
    {
      id: "record-placement",
      pageId: "home",
      pageName: "Главная",
      featureKey: "product-card",
      featureName: "Карточка товара",
      version: "2.0.1",
      inputs: [{ inputKey: "product", kind: "record", entityId: "products", fields: [{ path: "/title", fieldId: "title-field", type: "text" }] }],
    },
    {
      id: "lookup-placement",
      pageId: "product-detail",
      pageName: "Товар",
      featureKey: "catalog-detail",
      featureName: "Детальная страница",
      version: "2.0.1",
      inputs: [{
        inputKey: "product",
        kind: "record",
        entityId: "products",
        fields: [{ path: "/title", fieldId: "title-field", type: "text" }],
        lookup: { argument: "slug", fieldId: "slug-field" },
      }],
    },
    {
      id: "collection-placement",
      pageId: "catalog",
      pageName: "Каталог",
      featureKey: "catalog",
      featureName: "Каталог",
      version: "2.0.1",
      inputs: [{ inputKey: "products", kind: "collection", entityId: "products", fields: [{ path: "/title", fieldId: "title-field", type: "text" }] }],
    },
  ],
} as unknown as ModelContentWorkspace;

function recordWorkspaceMarkup(
  records: ModelContentRecord[],
  cardinality: "many" | "singleton" = "singleton",
  selectedRecordIndex = records.length === 1 ? 0 : -1,
): string {
  const entity: DataModelEntity = { ...model.entities[0]!, cardinality };
  const selectedRecord = records[selectedRecordIndex] ?? null;
  return renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() }, createElement(ModelContentRecordWorkspace, {
    model: { ...model, entities: [entity] },
    draft: { records, selections: [] },
    savedDraft: { records, selections: [] },
    entities: [entity],
    selectedEntity: entity,
    selectedRecord,
    entityId: entity.id,
    recordId: selectedRecord?.id ?? "",
    search: "",
    recordsForEntity: records,
    visibleRecords: records,
    orphanedEntityRecords: [],
    validation: [],
    mediaPreparationsByFieldId: {},
    mediaAcceptByFieldId: {},
    disabled: false,
    deletePending: false,
    onSelectEntity: () => {},
    onSelectRecord: () => {},
    onSearchChange: () => {},
    onAddRecord: () => {},
    onDeleteRecord: () => {},
    onClearSingletonRecord: () => {},
    onDeleteOrphanedEntityRecord: () => {},
    onCleanOrphanedRecordData: () => {},
    onChangeField: () => {},
    onOpenTargetRecord: () => {},
    onConfirmCleanup: async () => true,
    updateDraft: () => {},
  })));
}

function singletonRecordWorkspaceMarkup(records: ModelContentRecord[]): string {
  return recordWorkspaceMarkup(records, "singleton");
}

describe("model content placement selections", () => {
  it("renders scalar and record inputs as single-record selectors and explains route lookup", () => {
    const html = renderToStaticMarkup(createElement(PlacementSelections, {
      model,
      workspace,
      draft: { records: [savedRecord, unsavedRecord], selections: [] },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
    }));

    expect((html.match(/<select\b/g) ?? []).length).toBe(2);
    expect(html).toContain("Товары · значение из записи");
    expect(html).not.toContain("/title");
    expect(html).not.toContain("· Название");
    expect(html).toContain("Запись определяется по маршруту через аргумент «slug»");
    expect(html).toContain("уникальное поле «Адрес»");
    expect(html).toContain("Не выбрано — пустой вход");
  });

  it("keeps unsaved records unavailable in both single and ordered collection selection", () => {
    const selections: ModelContentSelection[] = [{
      placementId: "collection-placement",
      inputKey: "products",
      mode: "ids",
      recordIds: [savedRecord.id, unsavedRecord.id],
    }];
    const html = renderToStaticMarkup(createElement(PlacementSelections, {
      model,
      workspace,
      draft: { records: [savedRecord, unsavedRecord], selections },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
    }));

    expect(html).toContain('<option value="saved-product">Сохранённый товар</option>');
    expect(html).toContain('<option value="local:unsaved-product" disabled="">Новый товар · сначала сохраните</option>');
    expect(html).toMatch(/type="checkbox" disabled=""/);
  });

  it("shows stale curated IDs and provides a confirmed cleanup action", () => {
    const selections: ModelContentSelection[] = [{
      placementId: "collection-placement",
      inputKey: "products",
      mode: "ids",
      recordIds: [savedRecord.id, "removed-record"],
    }];
    const html = renderToStaticMarkup(createElement(PlacementSelections, {
      model,
      workspace,
      draft: { records: [savedRecord, unsavedRecord], selections },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
      confirmCleanup: async () => true,
    }));

    expect(html).toContain('role="alert"');
    expect(html).toContain("Недоступные выбранные записи: 1.");
    expect(html).toContain("Очистить некорректный выбор");
    expect(html).not.toContain("removed-record");
  });

  it("explains and exposes cleanup for a manual selection on a route-looked-up input", () => {
    const selections: ModelContentSelection[] = [{
      placementId: "lookup-placement",
      inputKey: "product",
      mode: "ids",
      recordIds: [savedRecord.id],
    }];
    const html = renderToStaticMarkup(createElement(PlacementSelections, {
      model,
      workspace,
      draft: { records: [savedRecord], selections },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
      confirmCleanup: async () => true,
    }));

    expect(html).toContain("Для входа по маршруту сохранён лишний ручной выбор записей.");
    expect(html).toContain("Очистить некорректный выбор");
    expect(html).toContain("Запись определяется по маршруту через аргумент «slug»");
  });

  it("auto-binds singleton scalar and record inputs while keeping collection input as a zero-or-one list", () => {
    const singletonModel = { ...model, entities: model.entities.map((entity) => ({ ...entity, cardinality: "singleton" as const })) };
    const html = renderToStaticMarkup(createElement(PlacementSelections, {
      model: singletonModel,
      workspace,
      draft: { records: [savedRecord], selections: [{ placementId: "collection-placement", inputKey: "products", mode: "ids", recordIds: [] }] },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
    }));

    expect(html).not.toContain("<select");
    expect(html).toContain("Значение автоматически берётся из единственной опубликованной записи.");
    expect(html).toContain("Единственная опубликованная запись связывается автоматически.");
    expect(html).toContain("Запись определяется по маршруту через аргумент «slug»");
    expect(html).toContain("Выбрать 0–1 запись");
    expect((html.match(/type="checkbox"/g) ?? []).length).toBe(1);
    expect(html).not.toContain("Переместить");
  });

  it("asks to remove an obsolete explicit selection when a scalar becomes auto-bound", () => {
    const singletonModel = { ...model, entities: model.entities.map((entity) => ({ ...entity, cardinality: "singleton" as const })) };
    const html = renderToStaticMarkup(createElement(PlacementSelections, {
      model: singletonModel,
      workspace,
      draft: { records: [savedRecord], selections: [{ placementId: "scalar-placement", inputKey: "rating", mode: "ids", recordIds: [savedRecord.id] }] },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
      confirmCleanup: async () => true,
    }));

    expect(html).toContain("Единственная запись связывается автоматически; ручной выбор для этого входа не нужен.");
    expect(html).toContain("Очистить некорректный выбор");
  });
});

describe("singleton record workspace", () => {
  it("shows the sole record form and confirmed clearing action without list, add, reorder, or delete controls", () => {
    const singletonRecord = { ...savedRecord, values: { title: "Сохранённый товар" }, relations: {} };
    const markup = singletonRecordWorkspaceMarkup([singletonRecord]);

    expect(markup).toContain("единственная запись");
    expect(markup).toContain("Очистить поля и связи");
    expect(markup).toContain("Название");
    expect(markup).not.toContain("Добавить");
    expect(markup).not.toContain("Удалить</button>");
    expect(markup).not.toContain("Найти запись");
    expect(markup).not.toContain("Переместить");
    expect(markup).not.toContain("Записи сущности");
  });

  it("blocks the singleton form when the API returns zero or multiple rows and exposes the integrity problem", () => {
    const multiple = [savedRecord, { ...savedRecord, id: "second-row", sortOrder: 1 }];
    const markup = singletonRecordWorkspaceMarkup(multiple);

    expect(markup).toContain('role="alert"');
    expect(markup).toContain("Сейчас записей: 2.");
    expect(markup).toContain("ID записи: saved-product");
    expect(markup).toContain("ID записи: second-row");
    expect(markup).not.toContain("Очистить поля и связи");
    expect(markup).not.toContain("Добавить");
    expect(markup).not.toContain("Удалить</button>");

    const empty = singletonRecordWorkspaceMarkup([]);
    expect(empty).toContain("Сейчас записей: 0.");
    expect(empty).not.toContain("Добавить");
  });
});

describe("many record workspace navigation", () => {
  it("keeps named entity navigation, labelled record ordering controls, and the selected record form", () => {
    const records = [savedRecord, { ...unsavedRecord, sortOrder: 1 }];
    const markup = recordWorkspaceMarkup(records, "many", 0);

    expect(markup).toContain('<aside aria-label="Сущности и записи"');
    expect(markup).toContain('<nav aria-label="Сущности"');
    expect(markup).toContain('aria-label="Записи сущности Товары"');
    expect(markup).toContain('aria-label="Найти запись"');
    expect(markup).toContain('aria-label="Переместить Сохранённый товар выше"');
    expect(markup).toContain('aria-label="Переместить Сохранённый товар ниже"');
    expect(markup).toContain("Удалить</button>");
    expect(markup).toContain("Сохранённый товар");
  });
});
