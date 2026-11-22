import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { FeatureArgumentBinding } from "@/types";
import { ArgumentField, reorderArgumentArrayItems } from "../../../../../../../../frontend/admin/src/pages/site/components/page-editor/FeatureArgumentsEditor";

vi.mock("@/domains/media/MediaPickerDialog", () => ({
  MediaPickerDialog: () => null,
}));

function renderArgument(
  schema: Parameters<typeof ArgumentField>[0]["schema"],
  binding?: FeatureArgumentBinding,
  captures: string[] = [],
  required = false,
  extras: Partial<Parameters<typeof ArgumentField>[0]> = {},
) {
  return renderToStaticMarkup(createElement(ArgumentField, {
    schema,
    argumentKey: "value",
    binding,
    captures,
    required,
    onChange: () => {},
    ...extras,
  }));
}

describe("feature argument dynamic sources", () => {
  it("reorders array values only for a complete permutation", () => {
    expect(reorderArgumentArrayItems(["a", "b", "c"], [2, 0, 1])).toEqual(["c", "a", "b"]);
    expect(reorderArgumentArrayItems(["a", "b"], [0])).toEqual(["a", "b"]);
    expect(reorderArgumentArrayItems(["a", "b"], [0, 0])).toEqual(["a", "b"]);
  });

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

  it("offers a structured route source only for format route string arguments", () => {
    const routeMarkup = renderArgument({ type: "string", format: "route", title: "Форма" });
    const plainMarkup = renderArgument({ type: "string", title: "Заголовок" });

    expect(routeMarkup).toContain('value="route"');
    expect(routeMarkup).toContain("Из маршрута");
    expect(plainMarkup).not.toContain('value="route"');
  });

  it("shows Router route suggestions and keeps an absolute binding structured", () => {
    const markup = renderArgument(
      { type: "string", format: "route", title: "Форма" },
      { source: "route", route: { mode: "absolute", host: "forms.example.test", path: "/forms/contact", routeId: "route-1", formId: "form-1", schemaVersion: 2 } },
      [],
      false,
      {
        routeSuggestions: [{
          id: "route-1",
          host: "forms.example.test",
          path: "/forms/contact",
          operationId: "public.form-session",
          formId: "form-1",
          schemaVersion: 3,
        }],
        formSchemas: [{ id: "form-1", key: "contact", version: 3, revision: 4, fields: {} }],
      },
    );

    expect(markup).toContain("https://forms.example.test/forms/contact");
    expect(markup).toContain('value="forms.example.test"');
    expect(markup).toContain("Маршрут закреплён на версии 2, текущая версия формы 3");
    expect(markup).toContain("не блокирует сохранение страницы");
  });

  it("warns when a selected form route is not published yet", () => {
    const markup = renderArgument(
      { type: "string", format: "route", title: "Форма" },
      { source: "route", route: { mode: "path", path: "/forms/contact", routeId: "missing-route", formId: "form-1", schemaVersion: 1 } },
      [],
      false,
      {
        routeSuggestions: [],
        formSchemas: [{ id: "form-1", key: "contact", version: 1, revision: 1, fields: {} }],
      },
    );

    expect(markup).toContain("не опубликован");
    expect(markup).toContain('role="alert"');
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
