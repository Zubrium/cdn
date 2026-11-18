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
    expect(buildRouterTarget(draft, uploadOperation)).toEqual({
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
    expect(buildRouterTarget(value, operation)).toBeNull();
  });

  it("requires an exact route for a public Core file", () => {
    expect(buildRouterTarget({ ...draft, targetKind: "core-file", file: { id: "file-id" }, match: "prefix" }, uploadOperation)).toBeNull();
    expect(buildRouterTarget({ ...draft, targetKind: "core-file", file: { id: "file-id" } }, uploadOperation)).toEqual({
      kind: "core-file", file_id: "file-id",
    });
  });

  it("enables a manifest fallback only for an explicitly configured static artifact", () => {
    expect(buildRouterTarget({ ...draft, targetKind: "static-site-artifact", file: { id: "01938f7a-68af-7c10-a51d-78b6ca125399" }, fallbackEnabled: true }, uploadOperation)).toEqual({
      kind: "static-site-artifact",
      file_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
      fallback_enabled: true,
    });
    expect(buildRouterTarget({ ...draft, targetKind: "static-site-artifact", file: { id: "01938f7a-68af-7c10-a51d-78b6ca125399" }, fallbackEnabled: false }, uploadOperation)).toEqual({
      kind: "static-site-artifact",
      file_id: "01938f7a-68af-7c10-a51d-78b6ca125399",
    });
  });
});
