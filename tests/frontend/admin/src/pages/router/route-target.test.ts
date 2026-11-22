import { describe, expect, it } from "vitest";
import { buildRouterTarget } from "../../../../../../frontend/admin/src/domains/router/route-target";
import type { RouterPublicOperation } from "../../../../../../frontend/admin/src/types";

const uploadOperation: RouterPublicOperation = {
  id: "public.form-upload",
  contract_version: 1,
  methods: ["POST"],
  max_request_bytes: 1_048_576,
  allowed_content_types: ["image/png", "application/pdf"],
};

const draft = {
  match: "exact" as const,
  targetKind: "public.form-upload" as const,
  file: null,
  accessGroup: "uploads",
  fileSetID: "01938f7a-68af-7c10-a51d-78b6ca125399",
  maxBytes: "524288",
  allowedContentTypes: ["image/png"],
};

const invalidCases: Array<{ name: string; operation?: RouterPublicOperation; value: typeof draft }> = [
  { name: "missing compiled capability", operation: undefined, value: draft },
  { name: "method not supported", operation: { ...uploadOperation, methods: ["PUT"] }, value: draft },
  { name: "limit exceeds operation", operation: uploadOperation, value: { ...draft, maxBytes: "1048577" } },
  { name: "MIME not allowed", operation: uploadOperation, value: { ...draft, allowedContentTypes: ["image/jpeg"] } },
  { name: "empty MIME selection", operation: uploadOperation, value: { ...draft, allowedContentTypes: [] } },
];

describe("Router target builder", () => {
  it("builds upload config from MIME types and bounds exposed by the compiled operation", () => {
    expect(buildRouterTarget(draft, [uploadOperation])).toEqual({
      kind: "registered-operation",
      operation_id: "public.form-upload",
      operation_config: {
        enabled: true,
        access_group: "uploads",
        file_set_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
        max_bytes: 524288,
        allowed_content_types: ["image/png"],
      },
    });
  });

  it.each(invalidCases)("rejects invalid upload target: $name", ({ operation, value }) => {
    expect(buildRouterTarget(value, operation ? [operation] : [])).toBeNull();
  });

  it("pins a form-bound upload to its schema and requires a searchable FileSet", () => {
    const bound = {
      ...draft,
      bindForm: true,
      formID: "01938f7a-68af-7c10-a51d-78b6ca125302",
      schemaVersion: 3,
    };
    expect(buildRouterTarget(bound, [uploadOperation])).toMatchObject({
      operation_id: "public.form-upload",
      operation_config: {
        form: { enabled: true, form_id: bound.formID, schema_version: 3 },
        file_set_id: bound.fileSetID,
      },
    });
    expect(JSON.stringify(buildRouterTarget(bound, [uploadOperation]))).not.toContain("snapshot_id");
    expect(buildRouterTarget({ ...bound, fileSetID: "" }, [uploadOperation])).toBeNull();
  });

  it("requires an exact route for a public Core file", () => {
    expect(buildRouterTarget({ ...draft, targetKind: "core-file", file: { id: "file-id" }, match: "prefix" }, [uploadOperation])).toBeNull();
    expect(buildRouterTarget({ ...draft, targetKind: "core-file", file: { id: "file-id" } }, [uploadOperation])).toEqual({
      kind: "core-file", file_id: "file-id",
    });
  });

  it("requires exact routes for registered public operations", () => {
    const formDraft = {
      ...draft,
      targetKind: "public.form-session" as const,
      match: "exact" as const,
      formID: "01938f7a-68af-7c10-a51d-78b6ca125302",
      schemaVersion: 1,
    };
    const operation: RouterPublicOperation = { ...uploadOperation, id: "public.form-session", methods: ["GET", "POST", "DELETE"] };
    expect(buildRouterTarget({ ...formDraft, match: "prefix" }, [operation])).toBeNull();
    expect(buildRouterTarget(formDraft, [operation])).toMatchObject({ operation_id: "public.form-session" });
    expect(buildRouterTarget({ ...formDraft, formID: formDraft.formID.toUpperCase() }, [operation])).toBeNull();
  });

  it("binds a static artifact without a fallback policy", () => {
    expect(buildRouterTarget({ ...draft, targetKind: "static-site-artifact", file: { id: "01938f7a-68af-7c10-a51d-78b6ca125399" } }, [uploadOperation])).toEqual({
      kind: "static-site-artifact",
      file_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
    });
  });
});
