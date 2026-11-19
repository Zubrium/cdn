import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  getSyntheticDataMode: vi.fn(() => true),
  subscribeSyntheticDataMode: vi.fn(() => () => undefined),
  useAuth: vi.fn(),
}));

vi.mock("@/api", () => ({
  getSyntheticDataMode: mocks.getSyntheticDataMode,
  subscribeSyntheticDataMode: mocks.subscribeSyntheticDataMode,
}));
vi.mock("@/app/auth", () => ({ useAuth: mocks.useAuth }));

import SettingsPage from "../../../../../../frontend/admin/src/pages/settings/SettingsPage";

function renderPage(principal: unknown = { group_access: { features: 1 } }) {
  mocks.useAuth.mockReturnValue({ principal });
  return renderToStaticMarkup(createElement(
    MemoryRouter,
    null,
    createElement(SettingsPage),
  ));
}

describe("static runtime settings", () => {
  it("does not imply runtime configuration was loaded or saved", () => {
    const rendered = renderPage();

    expect(rendered).toContain("Обзор недоступен через Admin API");
    expect(rendered).toContain("не предоставляет endpoint для чтения runtime-конфигурации");
    expect(rendered).not.toContain("Пример runtime-конфигурации");
    expect(rendered).not.toContain("Сохранить");
  });

  it("offers branch assignment only to a user with feature group write access", () => {
    const rendered = renderPage({ group_access: { features: 2 } });

    expect(rendered).toContain("Назначения снапшотов");
  });
});
