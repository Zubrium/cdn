import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { DataModel, DataModelEntity, ModelContentRecord, ModelContentWorkspace } from "@/types";
import { ContentEntityPage } from "../../../../../../frontend/admin/src/pages/content/components/model-content/ContentEntityPage";
import { ContentOverview } from "../../../../../../frontend/admin/src/pages/content/components/model-content/ContentOverview";
import { ContentPlacementsPage } from "../../../../../../frontend/admin/src/pages/content/components/model-content/ContentPlacementsPage";
import { PlacementSelections } from "../../../../../../frontend/admin/src/pages/content/components/model-content/PlacementSelections";

const entity: DataModelEntity = {
  id: "entity-products",
  key: "product",
  name: "Товары",
  cardinality: "many",
  fields: [{ id: "title", key: "title", name: "Название", type: "text" }],
};
const model: DataModel = { formatVersion: 1, entities: [entity], positions: {} };
const record: ModelContentRecord = {
  id: "record-1",
  entityId: entity.id,
  sortOrder: 0,
  values: { title: "Первый товар" },
};
const workspace: ModelContentWorkspace = {
  snapshot: {
    id: "snapshot-1",
    sequence: 1,
    name: "QA",
    digest: "sha256:qa",
    createdAt: "2026-11-15T00:00:00Z",
    createdByName: "QA admin",
    contentRevision: 1,
    warnings: [],
  },
  revision: 1,
  publishedRevision: 1,
  dataModel: model,
  records: [record],
  selections: [],
  placements: [{
    id: "placement-1",
    pageId: "page-1",
    pageName: "Главная",
    featureKey: "catalog",
    featureName: "Каталог",
    version: "1.0.0",
    inputs: [{ inputKey: "products", kind: "collection", entityId: entity.id, fields: [] }],
  }],
};

function render(element: React.ReactNode) {
  return renderToStaticMarkup(<MemoryRouter>{element}</MemoryRouter>);
}

describe("content hierarchy", () => {
  it("shows entities as the overview and preserves the snapshot deep link", () => {
    const html = render(createElement(ContentOverview, {
      model,
      entities: [entity],
      records: [record],
      placementsCount: 1,
      validation: [],
      orphanedEntityRecords: [],
      orphanedSelections: 0,
      snapshotId: "snapshot-1",
    }));

    expect(html).toContain("Структура контента");
    expect(html).toContain("href=\"/content/entities/product?snapshot=snapshot-1\"");
    expect(html).toContain("Записи на страницах");
    expect(html).not.toContain("record-1");
  });

  it("shows record links on an entity page without embedding the record form", () => {
    const html = render(createElement(ContentEntityPage, {
      entity,
      recordsForEntity: [record],
      visibleRecords: [record],
      search: "",
      snapshotId: "snapshot-1",
      validation: [],
      disabled: false,
      onSearchChange: () => {},
      onAddRecord: () => {},
      updateDraft: () => {},
    }));

    expect(html).toContain("href=\"/content/entities/product/records/record-1?snapshot=snapshot-1\"");
    expect(html).toContain("Открыть запись");
    expect(html).not.toContain("Очистить поля и связи");
  });

  it("offers pointer and keyboard ordering on an entity record list", () => {
    const secondRecord = {
      ...record,
      id: "record-2",
      sortOrder: 1,
      values: { title: "Второй товар" },
    };
    const html = render(createElement(ContentEntityPage, {
      entity,
      recordsForEntity: [record, secondRecord],
      visibleRecords: [record, secondRecord],
      search: "",
      snapshotId: "snapshot-1",
      validation: [],
      disabled: false,
      onSearchChange: () => {},
      onAddRecord: () => {},
      updateDraft: () => {},
    }));

    expect(html).toContain('aria-label="Перетащить Первый товар"');
    expect(html).toContain('aria-label="Опустить Первый товар"');
    expect(html).toContain('aria-label="Поднять Второй товар"');
  });

  it("groups page placements under progressive-disclosure sections", () => {
    const html = render(createElement(ContentPlacementsPage, {
      model,
      workspace,
      draft: { records: [record], selections: [] },
      savedDraft: { records: [record], selections: [] },
      disabled: false,
      updateDraft: () => {},
    }));

    expect(html).toContain("Записи на страницах");
    expect(html).toContain("Страница, фича или вход данных");
    expect(html).toContain("<details");
    expect(html).toContain("Главная");
    expect(html).toContain("Каталог");
  });

  it("offers pointer and keyboard ordering for curated placement records", () => {
    const secondRecord = {
      ...record,
      id: "record-2",
      sortOrder: 1,
      values: { title: "Второй товар" },
    };
    const html = render(createElement(PlacementSelections, {
      model,
      workspace,
      draft: {
        records: [record, secondRecord],
        selections: [{
          placementId: "placement-1",
          inputKey: "products",
          mode: "ids",
          recordIds: [record.id, secondRecord.id],
        }],
      },
      savedDraft: {
        records: [record, secondRecord],
        selections: [],
      },
      disabled: false,
      updateDraft: () => {},
    }));

    expect(html).toContain('aria-label="Перетащить Первый товар"');
    expect(html).toContain('aria-label="Переместить Второй товар выше в списке"');
  });
});
