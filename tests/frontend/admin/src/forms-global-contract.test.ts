import { describe, expect, it } from "vitest";
import { ApiError, resolveSiteConfigRouteArguments } from "../../../../frontend/admin/src/api";
import {
  formPublicationSteps,
  formPublicationWarnings,
  formSaveErrorMessage,
  formUploadSettingsError,
  hasFileFields,
  normalizedFormBasePath,
  routeSuggestions,
  staleFormBindingWarning,
  validFormKey,
  validRouterHost,
} from "../../../../frontend/admin/src/domains/forms/publication";
import type { FormSchema, RouterPublicOperation, RouterRoute } from "../../../../frontend/admin/src/types";

const schema: FormSchema = {
  id: "44444444-4444-4444-8444-444444444444",
  key: "contact_request",
  version: 2,
  revision: 5,
  fields: {
    email: { type: "email", required: true },
  },
};

const operations: RouterPublicOperation[] = [
  { id: "public.form-session", contract_version: 1, methods: ["GET", "POST", "DELETE"], max_request_bytes: 1024, allowed_content_types: ["application/json"] },
  { id: "public.form-submit", contract_version: 1, methods: ["POST"], max_request_bytes: 1024, allowed_content_types: ["application/json"] },
  { id: "public.form-upload", contract_version: 1, methods: ["POST"], max_request_bytes: 1_048_576, allowed_content_types: ["application/pdf", "image/png"] },
];

function existingRoute(operationId: string, path: string, version = 1): RouterRoute {
  return {
    id: `route-${operationId}`,
    branch_id: "main",
    host: "forms.example.test",
    path,
    match: "exact",
    target: {
      kind: "registered-operation",
      operation_id: operationId,
      operation_config: { enabled: true, form_id: schema.id, schema_version: version },
    },
    state: "active",
    revision: 3,
    created_at: "2026-11-22T10:00:00Z",
    updated_at: "2026-11-22T10:00:00Z",
  };
}

function publicRoute(id: string, path: string, target: RouterRoute["target"]): RouterRoute {
  return { ...existingRoute(id, path), id, path, target };
}

describe("global forms publication helpers", () => {
  it("rejects a route host with a port before publishing", () => {
    expect(validRouterHost("forms.example.test")).toBe(true);
    expect(validRouterHost("127.0.0.1")).toBe(true);
    expect(validRouterHost("127.0.0.1:49268")).toBe(false);
    expect(validRouterHost("forms example.test")).toBe(false);
    expect(validRouterHost("")).toBe(false);
  });

  it("normalizes the default base path from a globally unique key", () => {
    expect(normalizedFormBasePath("contact_request")).toBe("/forms/contact_request");
    expect(normalizedFormBasePath("contact_request", "contact/") ).toBe("/forms/contact_request");
    expect(normalizedFormBasePath("contact_request", "/feedback/contact/")).toBe("/feedback/contact");
  });

  it("matches validText(key, 128) without an extra key alphabet", () => {
    expect(validFormKey("Contact.Request-2026")).toBe(true);
    expect(validFormKey("contact request")).toBe(true);
    expect(validFormKey("ключ + документы")).toBe(true);
    expect(validFormKey("A".repeat(128))).toBe(true);
    expect(validFormKey("A".repeat(129))).toBe(false);
    expect(validFormKey("€".repeat(42))).toBe(true);
    expect(validFormKey("€".repeat(43))).toBe(false);
    expect(validFormKey("")).toBe(false);
    expect(validFormKey(" leading")).toBe(false);
    expect(validFormKey("trailing ")).toBe(false);
    expect(validFormKey("bad\x00key")).toBe(false);
  });

  it("detects file fields to decide whether an upload route is required", () => {
    expect(hasFileFields(schema)).toBe(false);
    expect(hasFileFields({ ...schema, fields: { attachment: { type: "file", required: false, max_files: 1, max_file_bytes: 1024, allowed_content_types: ["application/pdf"] } } })).toBe(true);
  });

  it("plans only session and submit routes for a form without file fields", () => {
    const steps = formPublicationSteps({ schema, host: "forms.example.test", basePath: "/forms/contact_request", operations });

    expect(steps.map((step) => [step.operationId, step.path, step.action])).toEqual([
      ["public.form-session", "/forms/contact_request", "create"],
      ["public.form-submit", "/forms/contact_request/submit", "create"],
    ]);
    expect(steps[0]?.target).toEqual({
      kind: "registered-operation",
      operation_id: "public.form-session",
      operation_config: { enabled: true, form_id: schema.id, schema_version: 2 },
    });
  });

  it("adds a pinned upload route when the schema contains file fields", () => {
    const withFile: FormSchema = { ...schema, version: 4, fields: {
      attachment: { type: "file", required: false, max_files: 2, max_file_bytes: 2048, allowed_content_types: ["application/pdf"] },
    } };
    const steps = formPublicationSteps({
      schema: withFile,
      host: "forms.example.test",
      basePath: "/forms/contact_request",
      operations,
      upload: { accessGroup: "uploads", fileSetID: "01938f7a-68af-7c10-a51d-78b6ca125399", maxBytes: 1024, allowedContentTypes: ["application/pdf"] },
    });

    expect(steps.map((step) => step.operationId)).toEqual([
      "public.form-session", "public.form-submit", "public.form-upload",
    ]);
    expect(steps[2]?.target).toEqual({
      kind: "registered-operation",
      operation_id: "public.form-upload",
      operation_config: {
        enabled: true,
        access_group: "uploads",
        file_set_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
        max_bytes: 1024,
        allowed_content_types: ["application/pdf"],
        form: { enabled: true, form_id: schema.id, schema_version: 4 },
      },
    });
  });

  it("does not create an upload route until all backend-required settings are provided", () => {
    const withFile: FormSchema = { ...schema, fields: { attachment: { type: "file", required: false, max_files: 1, max_file_bytes: 1024, allowed_content_types: ["application/pdf"] } } };
    const steps = formPublicationSteps({ schema: withFile, host: "forms.example.test", basePath: "/forms/contact_request", operations });

    const uploadStep = steps.find((step) => step.operationId === "public.form-upload");
    expect(uploadStep).toMatchObject({ action: "skip" });
    expect(uploadStep?.reason).toContain("Укажите настройки загрузки");
    expect(uploadStep?.target.operation_config).toEqual({});
    expect(formUploadSettingsError(undefined, operations)).toContain("Укажите настройки загрузки");
    expect(formUploadSettingsError({ accessGroup: "uploads", fileSetID: "bad", maxBytes: 1024, allowedContentTypes: ["application/pdf"] }, operations)).toContain("UUID набора файлов");
  });

  it("shows existing routes and lets the operator skip or update them", () => {
    const basePath = "/forms/contact_request";
    const input = {
      schema,
      host: "forms.example.test",
      basePath,
      operations,
      existingRoutes: [existingRoute("public.form-session", basePath, 1)],
      decisions: { "public.form-session": "skip" as const },
    };

    const steps = formPublicationSteps(input);
    expect(steps[0]).toMatchObject({ operationId: "public.form-session", action: "skip", existing: { id: "route-public.form-session" } });

    const updated = formPublicationSteps({ ...input, decisions: { "public.form-session": "update" } });
    expect(updated[0]).toMatchObject({ action: "update", existing: { id: "route-public.form-session" } });
    expect(updated[0]?.target).toMatchObject({ operation_config: { schema_version: 2 } });
  });

  it("warns about unpublished and stale pinned versions without blocking the plan", () => {
    expect(staleFormBindingWarning(1, 2, true)).toContain("версии 1");
    expect(staleFormBindingWarning(2, 2, true)).toBeNull();
    expect(staleFormBindingWarning(undefined, 2, false)).toContain("не опубликован");
  });

  it("reports a stale pinned version only for routes that already exist", () => {
    const basePath = "/forms/contact_request";
    const steps = formPublicationSteps({
      schema,
      host: "forms.example.test",
      basePath,
      operations,
      existingRoutes: [existingRoute("public.form-session", basePath, 1)],
    });

    const warnings = formPublicationWarnings(steps, schema.version);
    expect(warnings).toHaveLength(1);
    expect(warnings[0]).toContain("public.form-session");
    expect(warnings[0]).toContain("версии 1");
    expect(formPublicationWarnings(steps.map((step) => ({ ...step, existing: undefined })), schema.version)).toEqual([]);
  });

  it("warns about a stale nested upload binding without blocking the plan", () => {
    const basePath = "/forms/contact_request";
    const withFile: FormSchema = { ...schema, fields: {
      attachment: { type: "file", required: false, max_files: 1, max_file_bytes: 1024, allowed_content_types: ["application/pdf"] },
    } };
    const uploadRoute = publicRoute("route-public.form-upload", `${basePath}/upload`, {
      kind: "registered-operation",
      operation_id: "public.form-upload",
      operation_config: {
        enabled: true,
        access_group: "uploads",
        file_set_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
        max_bytes: 1024,
        allowed_content_types: ["application/pdf"],
        form: { enabled: true, form_id: schema.id, schema_version: 1 },
      },
    });
    const steps = formPublicationSteps({
      schema: withFile,
      host: "forms.example.test",
      basePath,
      operations,
      upload: { accessGroup: "uploads", fileSetID: "01938f7a-68af-7c10-a51d-78b6ca125399", maxBytes: 1024, allowedContentTypes: ["application/pdf"] },
      existingRoutes: [uploadRoute],
    });

    const warnings = formPublicationWarnings(steps, withFile.version);
    expect(warnings).toHaveLength(1);
    expect(warnings[0]).toContain("public.form-upload");
    expect(warnings[0]).toContain("версии 1");
  });

  it("explains a globally duplicated form key as a validation error", () => {
    expect(formSaveErrorMessage(new ApiError(409, "conflict", "conflict"))).toContain("уже используется");
    expect(formSaveErrorMessage(new Error("network"))).toBe("network");
  });

  it("suggests every public route, not only compiled form operations", () => {
    const routes = [
      publicRoute("form-route", "/forms/contact_request", { kind: "registered-operation", operation_id: "public.form-session", operation_config: { enabled: true, form_id: schema.id, schema_version: 3 } }),
      publicRoute("content-route", "/home", { kind: "registered-operation", operation_id: "public.site-page", operation_config: {} }),
      publicRoute("artifact-route", "/static/logo.svg", { kind: "static-site-artifact", file_id: "11111111-1111-4111-8111-111111111111" }),
      publicRoute("file-route", "/uploads/logo.png", { kind: "core-file", file_id: "22222222-2222-4222-8222-222222222222" }),
    ];

    const suggestions = routeSuggestions(routes, operations);

    expect(suggestions.map((suggestion) => suggestion.path)).toEqual([
      "/forms/contact_request", "/home", "/static/logo.svg", "/uploads/logo.png",
    ]);
    expect(suggestions[0]).toMatchObject({ operationId: "public.form-session", operationCompiled: true, formId: schema.id, schemaVersion: 3 });
    expect(suggestions[1]).toMatchObject({ operationId: "public.site-page", operationCompiled: false });
    expect(suggestions[2]?.id).toBe("artifact-route");
    expect(suggestions[2]?.operationId).toBeUndefined();
    expect(suggestions[3]?.id).toBe("file-route");
    expect(suggestions[3]?.operationId).toBeUndefined();
  });
});

describe("route argument resolution for snapshot payloads", () => {
  function configWith(route: unknown) {
    return {
      formatVersion: 3,
      pages: [{ id: "home", placements: [{ id: "p1", build: { featureKey: "f", version: "1" }, slot: "main", arguments: {
        relative: { source: "route", route: { mode: "path", path: "/forms/contact_request" } },
        absolute: { source: "route", route },
        query: { source: "query", queryKey: "page" },
      } }] }],
    };
  }

  it("replaces structured route bindings with strings before the Website payload is sent", () => {
    const original = configWith({ mode: "absolute", host: "forms.example.test", path: "/forms/contact_request" });
    const resolved = resolveSiteConfigRouteArguments(original) as typeof original;

    expect(resolved).not.toBe(original);
    expect(resolved.pages[0]?.placements[0]?.arguments).toEqual({
      relative: { source: "literal", value: "/forms/contact_request" },
      absolute: { source: "literal", value: "https://forms.example.test/forms/contact_request" },
      query: { source: "query", queryKey: "page" },
    });
    expect(JSON.stringify(resolved)).not.toContain("schemaVersion");
  });

  it("rejects malformed route bindings instead of sending an object to a feature", () => {
    expect(() => resolveSiteConfigRouteArguments(configWith({ mode: "absolute", path: "/forms/contact_request" })))
      .toThrowError(expect.objectContaining({ status: 400, code: "invalid_snapshot_payload" }));
  });
});
