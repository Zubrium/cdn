import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { DataModel, ModelContentRecord, ModelContentSelection, ModelContentWorkspace } from "@/types";
import { PlacementSelections } from "../../../../../../frontend/admin/src/pages/content/model-content-editor";
import { ContentPlacementsPage, placementSectionDefaults } from "../../../../../../frontend/admin/src/pages/content/components/model-content/ContentPlacementsPage";
import { ContentRecordPage } from "../../../../../../frontend/admin/src/pages/content/components/model-content/ContentRecordPage";

const model: DataModel = {
  formatVersion: 1,
  entities: [{
    id: "products",
    key: "product",
    name: "Товары",
    cardinality: "many",
    fields: [
      { id: "title-field", key: "title", name: "Название", type: "text" },
      { id: "slug-field", key: "slug", name: "Адрес", type: "text", unique: true },
    ],
  }],
  positions: {},
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
} as ModelContentWorkspace;

function placementMarkup(
  records: ModelContentRecord[],
  selections: ModelContentSelection[] = [],
  selectedModel: DataModel = model,
  confirmCleanup?: (title: string, description: string) => Promise<boolean>,
) {
  return renderToStaticMarkup(createElement(PlacementSelections, {
    model: selectedModel,
    workspace,
    draft: { records, selections },
    savedDraft: { records: [savedRecord], selections: [] },
    disabled: false,
    updateDraft: () => {},
    confirmCleanup,
  }));
}

function recordMarkup(record: ModelContentRecord, records: ModelContentRecord[] = [record]) {
  return renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() },
    createElement(MemoryRouter, { initialEntries: ["/content"] }, createElement(ContentRecordPage, {
      model,
      entity: model.entities[0]!,
      record,
      recordsForEntity: records,
      snapshotId: "snapshot-1",
      validation: [],
      mediaPreparationsByFieldId: {},
      mediaAcceptByFieldId: {},
      disabled: false,
      deletePending: false,
      onDeleteRecord: () => {},
      onClearSingletonRecord: () => {},
      onCleanOrphanedRecordData: () => {},
      onChangeField: () => {},
    }))));
}

describe("model content placement selections", () => {
  it("renders scalar and record inputs as single-record selectors and explains route lookup", () => {
    const html = placementMarkup([savedRecord, unsavedRecord]);
    expect((html.match(/<select\b/g) ?? []).length).toBe(2);
    expect(html).toContain("Товары · значение из записи");
    expect(html).toContain("Запись определяется по маршруту через аргумент «slug»");
    expect(html).toContain("уникальное поле «Адрес»");
    expect(html).not.toContain("relations");
  });

  it("keeps unsaved records unavailable in single and ordered collection selection", () => {
    const html = placementMarkup([savedRecord, unsavedRecord], [{
      placementId: "collection-placement",
      inputKey: "products",
      mode: "ids",
      recordIds: [savedRecord.id, unsavedRecord.id],
    }]);
    expect(html).toContain('<option value="saved-product">Сохранённый товар</option>');
    expect(html).toContain('<option value="local:unsaved-product" disabled="">Новый товар · сначала сохраните</option>');
    expect(html).toMatch(/type="checkbox" disabled=""/);
  });

  it("shows stale curated IDs and provides a confirmed cleanup action", () => {
    const html = placementMarkup([savedRecord, unsavedRecord], [{
      placementId: "collection-placement",
      inputKey: "products",
      mode: "ids",
      recordIds: [savedRecord.id, "removed-record"],
    }], model, async () => true);
    expect(html).toContain('role="alert"');
    expect(html).toContain("Недоступные выбранные записи: 1.");
    expect(html).toContain("Очистить некорректный выбор");
    expect(html).not.toContain("removed-record");
  });

  it("explains cleanup for a manual selection on a route-looked-up input", () => {
    const html = placementMarkup([savedRecord], [{
      placementId: "lookup-placement",
      inputKey: "product",
      mode: "ids",
      recordIds: [savedRecord.id],
    }], model, async () => true);
    expect(html).toContain("Для входа по маршруту сохранён лишний ручной выбор записей.");
    expect(html).toContain("Очистить некорректный выбор");
  });

  it("keeps the record list keyboard reachable and highlights focused rows", () => {
    const html = placementMarkup([savedRecord, unsavedRecord], [{
      placementId: "collection-placement",
      inputKey: "products",
      mode: "ids",
      recordIds: [],
    }]);
    expect(html).toContain('tabindex="0"');
    expect(html).toContain("focus-within:bg-muted/50");
  });

  it("auto-binds singleton scalar and record inputs while keeping collection input as zero-or-one", () => {
    const singletonModel = { ...model, entities: model.entities.map((entity) => ({ ...entity, cardinality: "singleton" as const })) };
    const html = placementMarkup([savedRecord], [{ placementId: "collection-placement", inputKey: "products", mode: "ids", recordIds: [] }], singletonModel);
    expect(html).not.toContain("<select");
    expect(html).toContain("Значение автоматически берётся из единственной опубликованной записи.");
    expect(html).toContain("Единственная опубликованная запись связывается автоматически.");
    expect(html).toContain("Выбрать 0–1 запись");
    expect((html.match(/type="checkbox"/g) ?? []).length).toBe(1);
  });
});

describe("placement sections", () => {
  it("opens only initial sections and does not derive disclosure state from search", () => {
    expect(placementSectionDefaults(workspace.placements)).toEqual({
      "page:home": true,
      "placement:scalar-placement": false,
      "placement:record-placement": false,
      "page:product-detail": false,
      "placement:lookup-placement": true,
      "page:catalog": false,
      "placement:collection-placement": true,
    });

    const html = renderToStaticMarkup(createElement(MemoryRouter, null, createElement(ContentPlacementsPage, {
      model,
      workspace: { ...workspace, snapshot: { id: "snapshot-1" } } as ModelContentWorkspace,
      draft: { records: [savedRecord], selections: [] },
      savedDraft: { records: [savedRecord], selections: [] },
      disabled: false,
      updateDraft: () => {},
    })));
    expect(html.match(/<details[^>]*>/u)?.[0]).toContain('open=""');
    expect(html).toContain('class="group/page"');
  });
});

describe("flat model record form", () => {
  it("renders only own fields and no relation editor, tabs or counters", () => {
    const html = recordMarkup(savedRecord, [savedRecord, unsavedRecord]);
    expect(html).toContain("Название");
    expect(html).toContain("Адрес");
    expect(html).toContain("Удалить</button>");
    expect(html).not.toMatch(/Связ(и|ей|ь)/u);
    expect(html).not.toContain("relations");
  });

  it("shows one clear action for a singleton record", () => {
    const singletonModel = { ...model, entities: model.entities.map((entity) => ({ ...entity, cardinality: "singleton" as const })) };
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() },
      createElement(MemoryRouter, { initialEntries: ["/content"] }, createElement(ContentRecordPage, {
        model: singletonModel,
        entity: singletonModel.entities[0]!,
        record: savedRecord,
        recordsForEntity: [savedRecord],
        snapshotId: "snapshot-1",
        validation: [],
        mediaPreparationsByFieldId: {},
        mediaAcceptByFieldId: {},
        disabled: false,
        deletePending: false,
        onDeleteRecord: () => {},
        onClearSingletonRecord: () => {},
        onCleanOrphanedRecordData: () => {},
        onChangeField: () => {},
      }))));
    expect(html).toContain("Очистить поля");
    expect(html).not.toContain("Очистить поля и связи");
    expect(html).not.toContain("Добавить");
  });
});

describe("content editor unsaved guard context", () => {
  it("protects snapshot and locale changes but not unrelated query changes", async () => {
    const { changesSnapshotOrLocale } = await import("../../../../../../frontend/admin/src/pages/content/components/model-content-editor/useModelContentEditorController");
    const current = "snapshot=one&locale=ru&page_id=home";

    expect(changesSnapshotOrLocale(current, "snapshot=two&locale=ru&page_id=home")).toBe(true);
    expect(changesSnapshotOrLocale(current, "snapshot=one&locale=en&page_id=home")).toBe(true);
    expect(changesSnapshotOrLocale(current, "snapshot=one&locale=ru&page_id=other")).toBe(false);
  });
});
