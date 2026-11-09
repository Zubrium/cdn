import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { DataInputBinding, DataModelEntity, JsonValue } from "@/types";
import {
  addArgumentArrayItem,
  ArgumentField,
  DetailSEOFieldsEditor,
  moveArgumentArrayItem,
  removeArgumentArrayItem,
  updateDetailSEOField,
  updateArgumentArrayItem,
} from "./PageEditorPage";

vi.mock("@/domains/media/MediaPickerDialog", async () => {
  const React = await import("react");
  return {
    MediaPickerDialog: (props: { preparation?: { fieldPath?: string } }) => React.createElement("output", {
      "data-media-field-path": props.preparation?.fieldPath ?? "",
    }),
  };
});

function renderArgument({
  argumentKey = "settings",
  schema,
  value,
  required = false,
  binding = { source: "literal" as const, value },
  preparation,
}: {
  argumentKey?: string;
  schema: Parameters<typeof ArgumentField>[0]["schema"];
  value: JsonValue;
  required?: boolean;
  binding?: Parameters<typeof ArgumentField>[0]["binding"];
  preparation?: Parameters<typeof ArgumentField>[0]["preparation"];
}) {
  return renderToStaticMarkup(createElement(ArgumentField, {
    argumentKey,
    schema,
    required,
    binding,
    captures: ["slug"],
    onChange: () => {},
    preparation,
  }));
}

describe("PageEditorPage argument value editor", () => {
  it("renders nested object values with typed scalar, enum, and boolean controls", () => {
    const markup = renderArgument({
      schema: {
        type: "object",
        title: "Настройки",
        required: ["heading", "appearance"],
        properties: {
          heading: { type: "string", title: "Заголовок" },
          appearance: {
            type: "object",
            title: "Оформление",
            required: ["enabled"],
            properties: {
              enabled: { type: "boolean", title: "Включено" },
              width: { type: "integer", title: "Ширина" },
              mode: { type: "string", title: "Режим", enum: ["compact", "wide"] },
            },
          },
        },
      },
      value: {
        heading: "Новая витрина",
        appearance: { enabled: true, width: 640, mode: "wide" },
      },
      required: true,
    });

    expect(markup).toContain("Настройки · Заголовок");
    expect(markup).toContain('value="Новая витрина"');
    expect(markup).toContain("Настройки · Оформление · Включено");
    expect(markup).toContain('value="true" selected=""');
    expect(markup).toContain('type="number"');
    expect(markup).toContain('value="640"');
    expect(markup).toContain('value="&quot;wide&quot;" selected=""');
    expect(markup).not.toContain("textarea");
    expect(markup).not.toContain("<pre");
  });

  it("renders arrays of objects and scalars with named add, remove, and reorder controls", () => {
    const objectMarkup = renderArgument({
      schema: {
        type: "array",
        title: "Карточки",
        items: {
          type: "object",
          required: ["title"],
          properties: {
            title: { type: "string", title: "Заголовок" },
            active: { type: "boolean", title: "Активна" },
          },
        },
      },
      value: [
        { title: "Первая", active: true },
        { title: "Вторая", active: false },
      ],
    });
    expect(objectMarkup).toContain('aria-label="Элементы Карточки"');
    expect(objectMarkup).toContain('aria-label="Добавить элемент в Карточки"');
    expect(objectMarkup).toContain('aria-label="Удалить Карточки · элемент 1"');
    expect(objectMarkup).toContain('aria-label="Переместить Карточки · элемент 1 выше"');
    expect(objectMarkup).toContain('aria-label="Переместить Карточки · элемент 2 ниже"');
    expect(objectMarkup).toContain('value="Первая"');
    expect(objectMarkup).toContain('value="Вторая"');

    const scalarMarkup = renderArgument({
      argumentKey: "tags",
      schema: { type: "array", title: "Метки", items: { type: "string" } },
      value: ["новинка", "хит"],
    });
    expect(scalarMarkup).toContain('value="новинка"');
    expect(scalarMarkup).toContain('value="хит"');
    expect(scalarMarkup).not.toContain("textarea");
  });

  it("creates, updates, removes, and reorders values without mutating existing arrays", () => {
    const current: JsonValue[] = [
      { title: "Первая", enabled: true },
      { title: "Вторая", enabled: false },
    ];
    const itemSchema: Parameters<typeof ArgumentField>[0]["schema"] = {
      type: "object",
      required: ["title"],
      properties: {
        title: { type: "string" },
        enabled: { type: "boolean" },
        tags: { type: "array", items: { type: "integer" } },
      },
    };

    expect(addArgumentArrayItem(current, itemSchema)).toEqual([
      ...current,
      { title: "" },
    ]);
    expect(updateArgumentArrayItem(current, 1, { title: "Изменена", enabled: true })).toEqual([
      current[0], { title: "Изменена", enabled: true },
    ]);
    expect(removeArgumentArrayItem(current, 0)).toEqual([current[1]]);
    expect(moveArgumentArrayItem(current, 0, 1)).toEqual([current[1], current[0]]);
    expect(moveArgumentArrayItem(current, 0, -1)).toBe(current);
    expect(current).toEqual([
      { title: "Первая", enabled: true },
      { title: "Вторая", enabled: false },
    ]);
    expect(addArgumentArrayItem([], { type: "integer" })).toEqual([0]);
    expect(addArgumentArrayItem([], { type: "string", enum: ["small", "large"] })).toEqual(["small"]);
  });

  it("keeps nested media selection bound to the manifest JSON pointer and preserves dynamic bindings", () => {
    const preparation = {
      snapshotId: "snapshot-1",
      featureKey: "gallery",
      buildVersion: "2.0.0",
      schemaKind: "arguments" as const,
      fieldPath: "/gallery",
      buildDigest: "sha256:fixture",
    };
    const markup = renderArgument({
      argumentKey: "gallery",
      schema: {
        type: "array",
        title: "Галерея",
        items: {
          type: "object",
          properties: {
            image: { type: "string", format: "media", "x-media": { accept: "raster-image" } },
          },
        },
      },
      value: [{ image: "media-1" }],
      preparation,
    });
    expect(markup).toContain('data-media-field-path="/gallery/items/image"');
    expect(markup).toContain('aria-label="Выбрать медиа для Галерея · элемент 1 · image"');
    expect(markup).not.toContain("type=\"file\"");

    const queryMarkup = renderArgument({
      argumentKey: "filter",
      schema: { type: "string", title: "Фильтр" },
      value: "category",
      binding: { source: "query", queryKey: "category" },
    });
    expect(queryMarkup).toContain('value="query"');
    expect(queryMarkup).toContain('value="category"');
  });
});

describe("PageEditorPage model-backed detail SEO editor", () => {
  const entity: DataModelEntity = {
    id: "product-entity",
    key: "product",
    name: "Товар",
    fields: [
      { id: "title-id", key: "title", name: "Название", type: "text" },
      { id: "description-id", key: "description", name: "Описание", type: "longText" },
      { id: "image-id", key: "image", name: "Фото", type: "media" },
      { id: "price-id", key: "price", name: "Цена", type: "number" },
    ],
  };
  const binding: DataInputBinding = {
    inputKey: "product",
    entityId: entity.id,
    lookup: { argument: "slug", fieldId: "slug-id" },
    detailSEO: { titleFieldId: "title-id", ogImageFieldId: "image-id" },
  };

  it("offers only compatible text and media model fields and shows the published-value fallback", () => {
    const markup = renderToStaticMarkup(createElement(DetailSEOFieldsEditor, {
      binding,
      entity,
      onChange: () => {},
    }));
    expect(markup).toContain("SEO из записи");
    expect(markup).toContain("значения опубликованной записи");
    expect(markup).toContain('value="title-id" selected=""');
    expect(markup).toContain('value="image-id" selected=""');
    expect(markup).toContain("Описание");
    expect(markup).not.toContain('value="price-id"');
  });

  it("prevents a second record source from configuring the page head", () => {
    const markup = renderToStaticMarkup(createElement(DetailSEOFieldsEditor, {
      binding,
      entity,
      disabled: true,
      onChange: () => {},
    }));
    expect(markup).toContain("SEO-поля уже сопоставлены с другой записью");
    expect(markup).toContain("disabled=\"\"");
  });

  it("updates one field mapping immutably and removes an empty detailSEO contract", () => {
    const original: DataInputBinding[] = [binding, { inputKey: "badge", entityId: entity.id }];
    const next = updateDetailSEOField(original, "product", "descriptionFieldId", "description-id");
    expect(next[0]?.detailSEO).toEqual({ titleFieldId: "title-id", descriptionFieldId: "description-id", ogImageFieldId: "image-id" });
    expect(next[1]).toBe(original[1]);
    expect(original[0]?.detailSEO).toEqual({ titleFieldId: "title-id", ogImageFieldId: "image-id" });

    const cleared = updateDetailSEOField([{ inputKey: "product", entityId: entity.id, detailSEO: { titleFieldId: "title-id" } }], "product", "titleFieldId", "");
    expect(cleared[0]?.detailSEO).toBeUndefined();
  });
});
