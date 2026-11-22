import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { SiteSnapshot } from "@/types";
import { SnapshotStateNotice } from "../../../../../../frontend/admin/src/pages/site/SiteWorkspace";

const snapshot: SiteSnapshot = {
  id: "snapshot-42",
  pageId: "page-7",
  sequence: 42,
  name: "Главная",
  state: "provisioning",
  digest: "sha256:test",
  createdAt: "2026-11-20T10:00:00Z",
};

function render(state: SiteSnapshot["state"]) {
  return renderToStaticMarkup(
    <MemoryRouter>
      <SnapshotStateNotice snapshot={{ ...snapshot, state }} onRefresh={() => {}} />
    </MemoryRouter>,
  );
}

describe("snapshot state notice", () => {
  it("offers a way out to the snapshot tree while a snapshot is provisioning", () => {
    const markup = render("provisioning");

    expect(markup).toContain("Снапшот готовится");
    expect(markup).toContain('role="status"');
    expect(markup).toContain("Обновить состояние");
    expect(markup).toContain("К дереву снапшотов");
    expect(markup).toContain('href="/snapshots/tree?page_id=page-7"');
  });

  it("keeps the same escape hatch when snapshot preparation failed", () => {
    const markup = render("failed");

    expect(markup).toContain("Не удалось подготовить снапшот");
    expect(markup).toContain("К дереву снапшотов");
    expect(markup).toContain('href="/snapshots/tree?page_id=page-7"');
  });
});
