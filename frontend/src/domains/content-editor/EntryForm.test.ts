import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { parsedSchema } from "@/domains/content-schema/schema";
import { EntryFieldsForm, validateEntryFields } from "./EntryForm";

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
