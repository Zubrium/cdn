import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useAuth: vi.fn(),
  useConfirm: vi.fn(),
  api: { formsEngine: { schemas: vi.fn(), submissions: vi.fn(), submission: vi.fn(), exportSubmissions: vi.fn() } },
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));

import SubmissionsPage, { updateSubmissionFilter } from "../../../../../../frontend/admin/src/pages/submissions/SubmissionsPage";

const schema = { id: "form-1", key: "contact", version: 1, fields: {}, revision: 1 };
const submission = {
  id: "submission-1", form_id: "form-1", schema_version: 1, state: "new", values: { email: "person@example.test" },
  created_at: "2026-11-18T10:00:00Z", revision: 2,
};

function renderPage(path: string, level: 1 | 2) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["formsEngine", "schemas"], { pages: [{ items: [schema], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["formsEngine", "submissions", "form-1", "", ""], { pages: [{ items: [submission], next_cursor: null }], pageParams: [null] });
  client.setQueryData(["formsEngine", "submission", "submission-1"], submission);
  mocks.useAuth.mockReturnValue({ principal: { group_access: { forms_submissions: level, forms_schemas: 1 } } });
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client },
    createElement(MemoryRouter, { initialEntries: [path] }, createElement(Routes, null,
      createElement(Route, { path: "/submissions", element: createElement(SubmissionsPage) }),
      createElement(Route, { path: "/submissions/:submissionId", element: createElement(SubmissionsPage) }),
    )),
  ));
}

describe("global Forms submissions", () => {
  beforeEach(() => {
    mocks.useConfirm.mockReturnValue(vi.fn(async () => true));
    mocks.api.formsEngine.schemas.mockResolvedValue({ items: [schema], next_cursor: null });
    mocks.api.formsEngine.submissions.mockResolvedValue({ items: [submission], next_cursor: null });
    mocks.api.formsEngine.submission.mockResolvedValue(submission);
    mocks.api.formsEngine.exportSubmissions.mockResolvedValue({ blob: new Blob(), filename: "forms-form-1-submissions.csv" });
  });

  it("lists immutable answers by form without snapshot scope", () => {
    const rendered = renderPage("/submissions?form=form-1", 1);
    expect(rendered).toContain("Заявки");
    expect(rendered).toContain("Ключ формы");
    expect(rendered).toContain("person@example.test");
    expect(rendered).toContain("contact");
    expect(rendered).toContain("Экспорт CSV");
    expect(rendered).not.toContain("Удалить заявку");
    expect(rendered).not.toContain("Снапшот");
  });

  it("shows revision-aware status and deletion controls to a group writer", () => {
    const rendered = renderPage("/submissions/submission-1?form=form-1", 2);
    expect(rendered).toContain("Ответы");
    expect(rendered).toContain("person@example.test");
    expect(rendered).toContain("Удалить");
    expect(rendered).toContain("Статус");
    expect(rendered).not.toContain("snapshot=");
  });

  it("keeps the focused list row visible inside its bounds", () => {
    const rendered = renderPage("/submissions?form=form-1", 1);
    expect(rendered).toContain("focus-visible:ring-inset");
  });

  it("replaces the URL history entry when a filter changes", () => {
    const setParams = vi.fn();
    const params = new URLSearchParams("form=form-1&key=old&state=new");

    updateSubmissionFilter(params, "key", "person", setParams);

    expect(setParams).toHaveBeenCalledTimes(1);
    const [next, options] = setParams.mock.calls[0]!;
    expect(next.get("key")).toBe("person");
    expect(next.get("form")).toBe("form-1");
    expect(options).toEqual({ replace: true });
  });

  it("removes an empty optional filter without adding a history entry", () => {
    const setParams = vi.fn();

    updateSubmissionFilter(new URLSearchParams("form=form-1&state=processed"), "state", "", setParams);

    const [next, options] = setParams.mock.calls[0]!;
    expect(next.has("state")).toBe(false);
    expect(options).toEqual({ replace: true });
  });
});
