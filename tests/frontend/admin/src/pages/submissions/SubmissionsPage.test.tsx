import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useAuth: vi.fn(),
  useConfirm: vi.fn(),
  api: { website: { snapshots: vi.fn() }, formsEngine: { schemas: vi.fn(), submissions: vi.fn(), submission: vi.fn(), exportSubmissions: vi.fn() } },
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));

import SubmissionsPage from "../../../../../../frontend/admin/src/pages/submissions/SubmissionsPage";

const snapshot = { id: "snapshot-1", page_id: "home", name: "Homepage", sequence: 1, revision: 1, feature_pins: {}, state: "published" };
const schema = { id: "form-1", key: "contact", version: 1, fields: {}, revision: 1 };
const submission = {
  id: "submission-1", form_id: "form-1", state: "new", values: { email: "person@example.test" },
  created_at: "2026-11-18T10:00:00Z", revision: 2,
};

function renderPage(path: string, level: 1 | 2) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["website", "snapshots"], { pages: [{ items: [snapshot], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["formsEngine", "schemas", "snapshot-1"], { pages: [{ items: [schema], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["formsEngine", "submissions", "snapshot-1", "form-1", ""], { pages: [{ items: [submission], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["formsEngine", "submission", "snapshot-1", "submission-1"], submission);
  mocks.useAuth.mockReturnValue({ principal: { group_access: { forms_submissions: level } } });
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(MemoryRouter, { initialEntries: [path] }, createElement(Routes, null,
      createElement(Route, { path: "/submissions", element: createElement(SubmissionsPage) }),
      createElement(Route, { path: "/submissions/:submissionId", element: createElement(SubmissionsPage) }),
    )),
  ));
}

describe("Forms Engine submissions", () => {
  beforeEach(() => {
    mocks.useConfirm.mockReturnValue(vi.fn(async () => true));
    mocks.api.website.snapshots.mockResolvedValue({ items: [snapshot], next_cursor: null });
    mocks.api.formsEngine.schemas.mockResolvedValue({ items: [schema], next_cursor: null });
    mocks.api.formsEngine.submissions.mockResolvedValue({ items: [submission], next_cursor: null });
    mocks.api.formsEngine.submission.mockResolvedValue(submission);
  });

  it("lists snapshot-scoped immutable answers for a reader", () => {
    const rendered = renderPage("/submissions?snapshot=snapshot-1&form=form-1", 1);
    expect(rendered).toContain("Заявки");
    expect(rendered).toContain("person@example.test");
    expect(rendered).toContain("contact");
    expect(rendered).toContain("Экспорт CSV");
    expect(rendered).not.toContain("Удалить заявку");
  });

  it("shows revision-aware status and deletion controls to a group writer", () => {
    const rendered = renderPage("/submissions/submission-1?snapshot=snapshot-1&form=form-1", 2);
    expect(rendered).toContain("Ответы");
    expect(rendered).toContain("person@example.test");
    expect(rendered).toContain("Удалить");
    expect(rendered).toContain("Статус");
  });
});
