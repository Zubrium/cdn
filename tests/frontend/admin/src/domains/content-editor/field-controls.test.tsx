import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { createField } from "../../../../../../frontend/admin/src/domains/content-schema/schema";
import { FieldCard, numericDraftValue, ScalarControl } from "../../../../../../frontend/admin/src/domains/content-editor/entry-form/field-controls";

function renderControl(field: ReturnType<typeof createField>, value: unknown = "") {
  return renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() },
    createElement(ScalarControl, {
      field,
      value: value as never,
      onChange: () => {},
      onPickMedia: () => {},
      fieldPath: `/${field.key}`,
      controlId: `entry-field-${field.key}-control`,
      labelId: `entry-field-${field.key}-label`,
      errorId: field.required && field.type === "media" ? `entry-field-${field.key}-error` : undefined,
      accessibleLabel: field.label || field.key,
    }),
  ));
}

describe("numeric entry", () => {
  it("keeps intermediate input while committing complete numbers", () => {
    expect(numericDraftValue("")).toBeNull();
    expect(numericDraftValue("-")).toBeUndefined();
    expect(numericDraftValue("1.")).toBe(1);
    expect(numericDraftValue("-5")).toBe(-5);
    expect(numericDraftValue("nope")).toBeUndefined();
  });
});

describe("accessible field controls", () => {
  it("names the media buttons after the field and links validation", () => {
    const field = createField("media", { key: "cover", label: "Обложка", required: true });

    const selected = renderControl(field, "media-1");
    expect(selected).toContain('aria-label="Заменить Обложка"');
    expect(selected).toContain('aria-label="Удалить выбранный файл Обложка"');
    expect(selected).toContain('aria-describedby="entry-field-cover-error"');

    const empty = renderControl(field);
    expect(empty).toContain('aria-label="Выбрать изображение Обложка"');

    const fileField = createField("file", { key: "attachment", label: "Вложение" });
    expect(renderControl(fileField)).toContain('aria-label="Выбрать файл Вложение"');
  });

  it("marks required inputs for assistive technology", () => {
    const field = createField("string", { key: "title", label: "Название", required: true });
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() },
      createElement(FieldCard, { field, anchorId: "entry-field-title" },
        createElement(ScalarControl, {
          field,
          value: "",
          onChange: () => {},
          onPickMedia: () => {},
          fieldPath: "/title",
          controlId: "entry-field-title-control",
          labelId: "entry-field-title-label",
          accessibleLabel: "Название",
        }),
      ),
    ));

    expect(html).toContain('aria-required="true"');
    expect(html).toContain("обязательное");
  });
});
