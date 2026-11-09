import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { JsonObject } from "@/types";
import { parsedSchema, schemaFromFields } from "@/domains/content-schema/schema";
import { assignArrayItemRecordID, normalizeEngineRecordIDs } from "@/domains/content-schema/record-identity";
import { clearMediaArraySelection, EntryFieldsForm, validateEntryFields } from "./EntryForm";

describe("validation of manifest fields", () => {
  const fields = parsedSchema({
    type: "object",
    properties: {
      title: { type: "string", minLength: 1, maxLength: 1 },
      links: { type: "array", items: { type: "string" }, maxItems: 0 },
      count: { type: "integer" },
    },
    required: ["title", "links"],
  }).fields;

  it("allows an empty required array while applying string length to Unicode code points", () => {
    expect(validateEntryFields(fields, { title: "🐄", links: [], count: 1 })).toEqual([]);
  });

  it("checks presence, minLength, maxItems zero, and integer values", () => {
    expect(validateEntryFields(fields, { title: "", links: ["x"], count: 1.5 }).map((issue) => issue.path)).toEqual(["title", "links", "count"]);
    expect(validateEntryFields(fields, {}).map((issue) => issue.path)).toEqual(["title", "links"]);
  });
});

it("renders nested objects and arrays inside object-array rows", () => {
  const fields = parsedSchema({ type: "object", properties: { rows: { type: "array", title: "Rows", items: { type: "object", properties: {
    details: { type: "object", title: "Details", properties: { caption: { type: "string", title: "Caption" } } },
    tags: { type: "array", title: "Tags", items: { type: "string" } },
  } } } } }).fields;
  const form = createElement(EntryFieldsForm, { fields, metadata: { rows: [{ details: { caption: "Nested value" }, tags: ["tag"] }] }, onChange: () => {}, issues: [] });
  const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() }, form));
  expect(html).toContain("Nested value");
  expect(html).toContain('value="tag"');
});

it("clears a media array selection without bypassing minItems", () => {
  const items = ["media-1", "media-2"];
  const next = clearMediaArraySelection(items, 0);

  expect(next).toEqual(["", "media-2"]);
  expect(next).toHaveLength(items.length);
  expect(items).toEqual(["media-1", "media-2"]);
});

describe("manifest engine-owned array identity", () => {
  const fields = parsedSchema({
    type: "object",
    properties: {
      rows: {
        type: "array",
        items: {
          type: "object",
          properties: {
            id: { type: "string", title: "Внутренний ID", "x-engine-record-id": true },
            title: { type: "string", title: "Заголовок" },
          },
          required: ["id", "title"],
        },
      },
    },
  }).fields;
  const rowFields = fields[0]!.children;

  it("keeps existing IDs, generates one for new rows, and replaces an ID on duplication", () => {
    const existing = { id: "stable-id", title: "Source" };
    expect(rowFields.find((field) => field.key === "id")?.engineRecordID).toBe(true);
    expect(assignArrayItemRecordID(existing, rowFields, false, () => "generated-id")).toBe(existing);
    expect(assignArrayItemRecordID({ title: "New" }, rowFields, false, () => "generated-id"))
      .toEqual({ id: "generated-id", title: "New" });
    expect(assignArrayItemRecordID(existing, rowFields, true, () => "copy-id"))
      .toEqual({ id: "copy-id", title: "Source" });
    expect(schemaFromFields(fields, "Content")).toMatchObject({
      properties: { rows: { items: { properties: { id: { "x-engine-record-id": true } } } } },
    });
  });

  it("repairs missing and duplicate IDs without changing the first stable row", () => {
    const data: JsonObject = { rows: [
      { id: "stable-id", title: "First" },
      { id: "stable-id", title: "Duplicate" },
      { title: "Legacy" },
    ] };
    let next = 0;
    const normalized = normalizeEngineRecordIDs(fields, data, () => `generated-${++next}`);
    expect(normalized.rows).toEqual([
      { id: "stable-id", title: "First" },
      { id: "generated-1", title: "Duplicate" },
      { title: "Legacy", id: "generated-2" },
    ]);
    expect(data).toEqual({ rows: [
      { id: "stable-id", title: "First" },
      { id: "stable-id", title: "Duplicate" },
      { title: "Legacy" },
    ] });
  });

  it("does not expose engine-owned IDs as editable form fields", () => {
    const form = createElement(EntryFieldsForm, {
      fields,
      metadata: { rows: [{ id: "must-stay-hidden", title: "Visible title" }] },
      onChange: () => {},
      issues: [],
    });
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() }, form));
    expect(html).toContain("Visible title");
    expect(html).not.toContain("must-stay-hidden");
    expect(html).not.toContain("Внутренний ID");
  });

  it("hides engine-owned IDs inside card-layout rows too", () => {
    const cardFields = parsedSchema({
      type: "object",
      properties: {
        rows: {
          type: "array",
          items: {
            type: "object",
            properties: {
              id: { type: "string", title: "Внутренний ID", "x-engine-record-id": true },
              title: { type: "string", title: "Заголовок" },
              details: { type: "object", title: "Детали", properties: { note: { type: "string", title: "Примечание" } } },
            },
          },
        },
      },
    }).fields;
    const form = createElement(EntryFieldsForm, {
      fields: cardFields,
      metadata: { rows: [{ id: "hidden-card-id", title: "Card title", details: { note: "Card details" } }] },
      onChange: () => {},
      issues: [],
    });
    const html = renderToStaticMarkup(createElement(QueryClientProvider, { client: new QueryClient() }, form));
    expect(html).toContain("Card title");
    expect(html).toContain("Card details");
    expect(html).not.toContain("hidden-card-id");
    expect(html).not.toContain("Внутренний ID");
  });
});
