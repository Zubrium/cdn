import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { SiteSnapshot } from "@/types";
import { SnapshotTreeItem } from "../../../../../../frontend/admin/src/pages/site/components/snapshots-page/SnapshotTreeItem";

const snapshot: SiteSnapshot = {
  id: "snapshot-42",
  pageId: "page-7",
  sequence: 42,
  name: "Главная",
  state: "ready",
  digest: "sha256:test",
  createdAt: "2026-11-20T10:00:00Z",
};

function render(canCreate: boolean) {
  return renderToStaticMarkup(
    <MemoryRouter>
      <ul>
        <SnapshotTreeItem
          snapshot={snapshot}
          canCreate={canCreate}
          busy={false}
          onCreateChild={vi.fn()}
          onDelete={vi.fn()}
        />
      </ul>
    </MemoryRouter>,
  );
}

describe("snapshot tree item actions", () => {
  it("marks deletion as destructive", () => {
    const markup = render(true);

    expect(markup).toContain("text-destructive hover:text-destructive");
  });

  it("explains why a child snapshot cannot be created yet", () => {
    const markup = render(false);

    expect(markup).toContain('title="Введите название дочернего снапшота"');
    expect(markup).toContain("disabled=\"\"");
  });
});
