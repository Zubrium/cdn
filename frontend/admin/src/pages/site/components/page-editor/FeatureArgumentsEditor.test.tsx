import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { FeatureArgumentBinding } from "@/types";
import { ArgumentField } from "./FeatureArgumentsEditor";

vi.mock("@/domains/media/MediaPickerDialog", () => ({
  MediaPickerDialog: () => null,
}));

function renderArgument(schema: Parameters<typeof ArgumentField>[0]["schema"], binding?: FeatureArgumentBinding, captures: string[] = [], required = false) {
  return renderToStaticMarkup(createElement(ArgumentField, {
    schema,
    argumentKey: "value",
    binding,
    captures,
    required,
    onChange: () => {},
  }));
}

describe("feature argument dynamic sources", () => {
  it("offers query values only for route-coercible scalars and arrays of scalars", () => {
    const object = renderArgument({ type: "object", properties: { title: { type: "string" } } });
    const objectArray = renderArgument({ type: "array", items: { type: "object", properties: { title: { type: "string" } } } });
    const scalarArray = renderArgument({ type: "array", items: { type: "integer" } });

    expect(object).not.toContain("Из query</option>");
    expect(objectArray).not.toContain("Из query</option>");
    expect(scalarArray).toContain("Из query</option>");
  });

  it("keeps an invalid saved query source visible and explains how to repair it", () => {
    const markup = renderArgument(
      { type: "object", title: "Настройки", properties: { title: { type: "string" } } },
      { source: "query", queryKey: "settings" },
    );

    expect(markup).toContain("Из query · тип больше не поддерживается");
    expect(markup).toContain("Тип аргумента изменился и больше не может быть заполнен query-параметром.");
    expect(markup).toContain('role="alert"');
  });

  it("shows a deleted route parameter and the required-query absence behavior", () => {
    const stalePath = renderArgument(
      { type: "string", title: "Идентификатор" },
      { source: "path", pathArg: "slug" },
    );
    const requiredQuery = renderArgument(
      { type: "string", title: "Фильтр" },
      { source: "query", queryKey: "category" },
      [],
      true,
    );

    expect(stalePath).toContain("slug · маршрут недоступен");
    expect(stalePath).toContain("Сохранённый параметр пути больше не доступен для страницы.");
    expect(requiredQuery).toContain("Если параметр отсутствует в URL, обязательный аргумент не будет передан");
  });

  it("explains when a manifest media argument still has an obsolete dynamic source", () => {
    const markup = renderArgument(
      { type: "string", format: "media", title: "Изображение" },
      { source: "path", pathArg: "image" },
      ["image"],
    );

    expect(markup).toContain("Аргумент медиа больше нельзя брать из маршрута или query.");
    expect(markup).toContain("Выбрать медиа для Изображение");
    expect(markup).not.toContain('type="file"');
  });
});
