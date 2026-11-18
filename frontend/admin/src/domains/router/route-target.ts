import type { RouterPublicOperation, RouterRouteMatch, RouterRouteTarget } from "@/types";

export interface RouterTargetDraft {
  match: RouterRouteMatch;
  targetKind: "core-file" | "static-site-artifact" | "public.form-upload";
  file: { id: string } | null;
  fallbackEnabled?: boolean;
  accessGroup: string;
  fileSetID: string;
  maxBytes: string;
  allowedContentTypes: string[];
}

export function buildRouterTarget(draft: RouterTargetDraft, uploadOperation?: RouterPublicOperation): RouterRouteTarget | null {
  if (draft.targetKind === "core-file" || draft.targetKind === "static-site-artifact") {
    if (draft.targetKind === "core-file" && draft.match !== "exact") return null;
    if (!draft.file) return null;
    return {
      kind: draft.targetKind,
      file_id: draft.file.id,
      ...(draft.targetKind === "static-site-artifact" && draft.fallbackEnabled ? { fallback_enabled: true } : {}),
    };
  }

  const maxBytes = Number(draft.maxBytes);
  if (!uploadOperation?.methods.includes("POST") || !Number.isSafeInteger(maxBytes) || maxBytes < 1 ||
    maxBytes > uploadOperation.max_request_bytes || !draft.accessGroup || draft.allowedContentTypes.length === 0 ||
    draft.allowedContentTypes.some((type) => !uploadOperation.allowed_content_types.includes(type))) return null;

  return {
    kind: "registered-operation",
    operation_id: "public.form-upload",
    operation_config: {
      enabled: true,
      access_group: draft.accessGroup,
      ...(draft.fileSetID ? { file_set_id: draft.fileSetID } : {}),
      max_bytes: maxBytes,
      allowed_content_types: draft.allowedContentTypes,
    },
  };
}
