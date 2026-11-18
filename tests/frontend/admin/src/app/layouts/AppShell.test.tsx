import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../../../../../../frontend/admin/src/components/ui/tooltip";

const mocks = vi.hoisted(() => ({ useAuth: vi.fn(), syntheticDataMode: false }));

vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));
vi.mock("@/api", () => ({
  getSyntheticDataMode: () => mocks.syntheticDataMode,
  subscribeSyntheticDataMode: () => () => undefined,
}));
vi.mock("@/components/common/theme-toggle", () => ({ ThemeToggle: () => null }));

import { AppShell } from "../../../../../../frontend/admin/src/app/layouts/AppShell";

function renderShell(principal: unknown) {
  mocks.useAuth.mockReturnValue({ principal, logout: vi.fn() });
  return renderToStaticMarkup(createElement(TooltipProvider, { delayDuration: 250 },
    createElement(MemoryRouter, null, createElement(AppShell))));
}

describe("admin navigation permissions", () => {
  beforeEach(() => {
    mocks.useAuth.mockReset();
    mocks.syntheticDataMode = false;
  });

  it("shows read-only runtime settings to a user with feature group read access", () => {
    const rendered = renderShell({ group_access: { features: 1 }, can_manage_administrators: false });

    expect(rendered).toContain("Настройки");
    expect(rendered).not.toContain("Доступы");
    expect(rendered).not.toContain("Дерево снапшотов");
  });

  it("shows Forms schema and submission navigation to a reader", () => {
    const rendered = renderShell({ group_access: { forms: 1 }, can_manage_administrators: false });

    expect(rendered).toContain("Схемы");
    expect(rendered).toContain("Заявки");
    expect(rendered).not.toContain("Доступы");
  });

  it("clearly marks synthetic engine fixtures and their non-persistent writes", () => {
    mocks.syntheticDataMode = true;
    const rendered = renderShell({ group_access: { features: 1 }, can_manage_administrators: false });

    expect(rendered).toContain('role="status"');
    expect(rendered).toContain("Данные сайта, контента и форм демонстрационные; изменения не сохраняются.");
  });
});
