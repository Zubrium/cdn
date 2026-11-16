import { describe, expect, it } from "vitest";
import { snapshotCreationLines, snapshotCreatorLabel } from "./provenance";

describe("snapshot provenance", () => {
  it("summarizes structural, feature-version, and inherited model data", () => {
    expect(snapshotCreationLines({
      origin: "child",
      changes: ["pages_routes", "features", "design_system"],
      featureChanges: [
        { featureKey: "catalog", action: "updated", previousVersion: "1.0.4", version: "1.0.5" },
      ],
      inheritedModelContent: [{ entityKey: "product", publishedRecords: 3, draftRecords: 2 }],
    })).toEqual([
      "Изменения сайта",
      "Обновлены страницы и маршруты",
      "Изменена дизайн-система",
      "Обновлена фича catalog: 1.0.4 → 1.0.5",
      "Данные product: 3 опубликованных, 2 черновых записей",
    ]);
  });

  it("labels independent roots and imports and handles missing creators", () => {
    expect(snapshotCreationLines({ origin: "root" })).toEqual(["Самостоятельный сайт"]);
    expect(snapshotCreationLines({ origin: "sitepack_import" })).toEqual(["Импорт sitepack"]);
    expect(snapshotCreatorLabel({ createdByName: "  QA operator  " })).toBe("QA operator");
    expect(snapshotCreatorLabel({})).toBe("Недоступен");
  });
});
