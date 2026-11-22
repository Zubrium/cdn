import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { parsedSchema } from "../../../../../../frontend/admin/src/domains/content-schema/schema";
import {
  ModelContentActions,
  validationFocusTarget,
} from "../../../../../../frontend/admin/src/pages/content/components/model-content/ModelContentActions";

const fields = parsedSchema({
  type: "object",
  properties: { title: { type: "string", title: "Название" } },
  required: ["title"],
}).fields;

function renderActions(overrides: Partial<Parameters<typeof ModelContentActions>[0]> = {}) {
  return renderToStaticMarkup(createElement(ModelContentActions, {
    snapshots: [],
    selectedSnapshotId: "snapshot-1",
    revision: 3,
    publishedRevision: 2,
    saveState: "idle",
    saveError: "",
    revisionConflict: null,
    dirty: true,
    invalidCount: 1,
    publishPending: false,
    canPublish: true,
    validation: [{ recordId: "record-1", issues: [{ path: "title", message: "Заполните название" }] }],
    fields,
    selectedRecordId: "record-1",
    onSnapshotChange: () => {},
    onOpenHistory: () => {},
    onSave: () => {},
    onPublish: () => {},
    onDownloadLocalDraft: () => {},
    onReloadServerDraft: () => {},
    onRetrySave: () => {},
    ...overrides,
  }));
}

describe("model content actions", () => {
  it("normalizes validation paths for focusing a named field", () => {
    expect(validationFocusTarget(fields, { path: "values.title", message: "Поле отсутствует" })).toEqual({
      path: "title",
      label: "Название",
    });
  });

  it("offers a labelled button that points at the invalid field", () => {
    const html = renderActions();

    expect(html).toContain("Заполните название");
    expect(html).toContain('data-field-path="title"');
    expect(html).toContain("Название");
  });

  it("shows in-flight state on buttons and a single saved badge", () => {
    const saving = renderActions({ saveState: "saving" });
    expect(saving).toContain("aria-busy=\"true\"");
    expect(saving).toContain("Сохранение…");

    const publishing = renderActions({ saveState: "saved", publishPending: true, dirty: false });
    expect(publishing).toContain("Публикация…");
    expect(publishing).toContain("bg-success/12");
    expect(publishing).toContain("Черновик сохранён");
    expect(publishing).not.toContain("Сохранено</span>");
  });
});
