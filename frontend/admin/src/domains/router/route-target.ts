import type { RouterPublicOperation, RouterRouteMatch, RouterRouteTarget } from "@/types";

export interface RouterTargetDraft {
  match: RouterRouteMatch;
  targetKind: "core-file" | "static-site-artifact" | "public.form-upload" | "public.form-session" | "public.form-submit";
  file: { id: string } | null;
  accessGroup: string;
  fileSetID: string;
  maxBytes: string;
  allowedContentTypes: string[];
  bindForm?: boolean;
  snapshotID?: string;
  formID?: string;
  schemaVersion?: number;
}

function formBinding(draft: RouterTargetDraft): Record<string, unknown> | null {
  const version = draft.schemaVersion;
  const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  if (!canonicalUUID.test(draft.snapshotID ?? "") || !canonicalUUID.test(draft.formID ?? "") || !Number.isSafeInteger(version) || Number(version) < 1) return null;
  return { enabled: true, snapshot_id: draft.snapshotID, form_id: draft.formID, schema_version: version };
}

export function buildRouterTarget(draft: RouterTargetDraft, operations: RouterPublicOperation[] = []): RouterRouteTarget | null {
  if (draft.targetKind === "core-file") {
    if (draft.match !== "exact" || !draft.file) return null;
    return { kind: "core-file", file_id: draft.file.id };
  }
  if (draft.targetKind === "static-site-artifact") {
    if (!draft.file) return null;
    return { kind: "static-site-artifact", file_id: draft.file.id };
  }

  if (draft.match !== "exact") return null;

  const maxBytes = Number(draft.maxBytes);
  if (draft.targetKind === "public.form-session" || draft.targetKind === "public.form-submit") {
    const operation = operations.find((item) => item.id === draft.targetKind);
    const binding = formBinding(draft);
    if (!operation || !operation.methods.length || !binding) return null;
    return { kind: "registered-operation", operation_id: draft.targetKind, operation_config: binding };
  }

  const uploadOperation = operations.find((item) => item.id === "public.form-upload");
  if (!uploadOperation?.methods.includes("POST") || !Number.isSafeInteger(maxBytes) || maxBytes < 1 ||
    maxBytes > uploadOperation.max_request_bytes || !draft.accessGroup || draft.allowedContentTypes.length === 0 ||
    draft.allowedContentTypes.some((type) => !uploadOperation.allowed_content_types.includes(type))) return null;

  const binding = draft.bindForm ? formBinding(draft) : null;
  if (draft.bindForm && (!binding || !draft.fileSetID)) return null;

  return {
    kind: "registered-operation",
    operation_id: "public.form-upload",
    operation_config: {
      enabled: true,
      access_group: draft.accessGroup,
      ...(draft.fileSetID ? { file_set_id: draft.fileSetID } : {}),
      max_bytes: maxBytes,
      allowed_content_types: draft.allowedContentTypes,
      ...(binding ? { form: binding } : {}),
    },
  };
}
