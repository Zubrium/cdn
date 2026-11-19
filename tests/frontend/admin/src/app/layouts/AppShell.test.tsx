import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
  const queryClient = new QueryClient();
  return renderToStaticMarkup(createElement(QueryClientProvider, { client: queryClient },
    createElement(TooltipProvider, { delayDuration: 250 },
      createElement(MemoryRouter, null, createElement(AppShell)))));
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

  it("shows only the Forms area granted by each independent group", () => {
    const schemaReader = renderShell({ group_access: { forms_schemas: 1 }, can_manage_administrators: false });
    expect(schemaReader).toContain("Схемы");
    expect(schemaReader).not.toContain("Заявки");

    const submissionReader = renderShell({ group_access: { forms_submissions: 1 }, can_manage_administrators: false });
    expect(submissionReader).not.toContain("Схемы");
    expect(submissionReader).toContain("Заявки");
  });

  it("clearly marks synthetic engine fixtures and their non-persistent writes", () => {
    mocks.syntheticDataMode = true;
    const rendered = renderShell({ group_access: { features: 1 }, can_manage_administrators: false });

    expect(rendered).toContain('role="status"');
    expect(rendered).toContain("Данные сайта, контента и форм демонстрационные; изменения не сохраняются.");
  });
});
