import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { newFormFieldDraft } from "../../../../../../frontend/admin/src/domains/forms/schema-draft";
import {
  changeFormFieldType,
  formFieldErrors,
  PublishFormDialog,
} from "../../../../../../frontend/admin/src/pages/forms/FormEditor";

vi.mock("@/components/common/confirm", () => ({ useConfirm: () => vi.fn(async () => true) }));
vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: ReactNode }) => createElement("div", null, children),
  DialogContent: ({ children }: { children: ReactNode }) => createElement("div", null, children),
  DialogHeader: ({ children }: { children: ReactNode }) => createElement("div", null, children),
  DialogTitle: ({ children }: { children: ReactNode }) => createElement("h2", null, children),
  DialogDescription: ({ children }: { children: ReactNode }) => createElement("p", null, children),
}));

const schema = {
  id: "form-1",
  key: "contact",
  version: 1,
  revision: 2,
  fields: {},
};

function renderPublishDialog(basePath = "") {
  return renderToStaticMarkup(createElement(
    QueryClientProvider,
    { client: new QueryClient() },
    createElement(MemoryRouter, null, createElement(PublishFormDialog, {
      schema,
      operations: [],
      routes: [],
      onClose: () => {},
      onPublished: () => {},
      ...(basePath ? { initialBasePath: basePath } : {}),
    })),
  ));
}

describe("form schema editor field constraints", () => {
  it("keeps text and email constraints when the field type changes", () => {
    const field = { ...newFormFieldDraft("email"), minLength: "3", maxLength: "120", pattern: "^a" };

    const email = changeFormFieldType(field, "email");
    expect(email).toMatchObject({ id: "email", type: "email", minLength: "3", maxLength: "120", pattern: "^a" });

    const number = changeFormFieldType(email, "number");
    expect(number.type).toBe("number");
    expect(number).toMatchObject({ minLength: "3", maxLength: "120", pattern: "^a" });
  });

  it("maps invalid field constraints to that field", () => {
    const invalid = { ...newFormFieldDraft("email"), maxLength: "0", pattern: "[" };

    expect(formFieldErrors([newFormFieldDraft("name"), invalid])).toEqual({
      "1": expect.stringContaining("Проверьте"),
    });
  });
});

describe("form publication dialog", () => {
  it("starts with an empty host and keeps publish disabled until it is valid", () => {
    const html = renderPublishDialog();

    expect(html).not.toContain('value="forms.example.test"');
    expect(html).toContain("Укажите hostname");
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>Опубликовать<\/button>/);
  });

  it("shows an accessible error for a base path without a leading slash", () => {
    const html = renderPublishDialog("forms/contact");

    expect(html).toContain('id="form-publish-base-path-error"');
    expect(html).toContain('role="alert"');
    expect(html).toContain('aria-describedby="form-publish-base-path-error"');
  });
});
