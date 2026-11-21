import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ useAuth: vi.fn() }));

vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));

import DashboardPage from "../../../../../../frontend/admin/src/pages/dashboard/DashboardPage";

function renderPage(principal: unknown) {
  mocks.useAuth.mockReturnValue({ principal });
  return renderToStaticMarkup(createElement(MemoryRouter, null, createElement(DashboardPage)));
}

describe("modular admin dashboard", () => {
  beforeEach(() => mocks.useAuth.mockReset());

  it("shows modules from direct group access without requesting an overview", () => {
    const rendered = renderPage({
      group_access: { content: 1, files: 1, features: 2, forms_schemas: 0, forms_submissions: 0 },
      can_manage_administrators: false,
    });

    expect(rendered).toContain("Контент");
    expect(rendered).toContain("Файлы");
    expect(rendered).toContain("Редактор страниц");
    expect(rendered).not.toContain("Заявки");
    expect(rendered).not.toContain("Активность");
  });

  it("offers read-only runtime configuration to users with Website read access", () => {
    const rendered = renderPage({
      group_access: { features: 1 },
      can_manage_administrators: false,
    });

    expect(rendered).toContain("Конфигурация запуска");
    expect(rendered).not.toContain("Снапшоты и фичи");
  });
});
