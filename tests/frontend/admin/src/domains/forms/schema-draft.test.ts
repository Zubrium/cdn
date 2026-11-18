import { describe, expect, it } from "vitest";
import { buildFormFieldDefinitions, formFieldToDraft, newFormFieldDraft } from "../../../../../../frontend/admin/src/domains/forms/schema-draft";

describe("Forms schema draft", () => {
  it("builds every supported explicit field type and its constraints", () => {
    const fields = [
      { ...newFormFieldDraft("name"), type: "text" as const, required: true, minLength: "2", maxLength: "80", pattern: "^[A-Za-z]+$" },
      { ...newFormFieldDraft("email"), type: "email" as const, required: true },
      { ...newFormFieldDraft("age"), type: "number" as const, minimum: "18", maximum: "120" },
      { ...newFormFieldDraft("consent"), type: "boolean" as const },
      { ...newFormFieldDraft("topic"), type: "select" as const, options: "Support\nSales" },
      { ...newFormFieldDraft("attachments"), type: "file" as const, maxFiles: "3", maxFileBytes: "5242880", allowedContentTypes: "application/pdf\nimage/png" },
    ];

    expect(buildFormFieldDefinitions(fields)).toEqual({
      name: { type: "text", required: true, min_length: 2, max_length: 80, pattern: "^[A-Za-z]+$" },
      email: { type: "email", required: true },
      age: { type: "number", required: false, minimum: 18, maximum: 120 },
      consent: { type: "boolean", required: false },
      topic: { type: "select", required: false, options: ["Support", "Sales"] },
      attachments: { type: "file", required: false, max_files: 3, max_file_bytes: 5242880, allowed_content_types: ["application/pdf", "image/png"] },
    });
  });

  it.each([
    ["invalid field identifier", [newFormFieldDraft("Bad-ID")]],
    ["duplicate field identifier", [newFormFieldDraft("name"), newFormFieldDraft("name")]],
    ["empty select options", [{ ...newFormFieldDraft("choice"), type: "select" as const, options: "\n" }]],
    ["duplicate select options", [{ ...newFormFieldDraft("choice"), type: "select" as const, options: "A\nA" }]],
    ["reversed numeric limits", [{ ...newFormFieldDraft("age"), type: "number" as const, minimum: "10", maximum: "2" }]],
    ["active content type", [{ ...newFormFieldDraft("upload"), type: "file" as const, allowedContentTypes: "Image/SVG+XML" }]],
    ["out of range file count", [{ ...newFormFieldDraft("upload"), type: "file" as const, maxFiles: "21" }]],
  ])("rejects %s", (_label, fields) => {
    expect(buildFormFieldDefinitions(fields as ReturnType<typeof newFormFieldDraft>[])).toBeNull();
  });

  it("round-trips a stored field definition to editable controls", () => {
    const draft = formFieldToDraft("attachments", {
      type: "file", required: true, max_files: 2, max_file_bytes: 8192,
      allowed_content_types: ["application/pdf", "image/png"],
    });

    expect(draft).toMatchObject({
      id: "attachments", type: "file", required: true, maxFiles: "2", maxFileBytes: "8192",
      allowedContentTypes: "application/pdf\nimage/png",
    });
  });

  it("caps each schema at 128 uniquely named fields", () => {
    const fields = Array.from({ length: 129 }, (_, index) => newFormFieldDraft(`field_${index + 1}`));

    expect(buildFormFieldDefinitions(fields)).toBeNull();
  });
});
