import type { ModelContentWorkspace } from "@/types";

export type ModelContentDraft = Pick<ModelContentWorkspace, "records" | "selections">;

export type ModelContentDraftUpdater = (
  updater: (current: ModelContentDraft) => ModelContentDraft,
) => void;

export interface ModelContentValidationIssue {
  path: string;
  message: string;
}

export interface ModelContentRecordValidation {
  recordId: string;
  issues: ModelContentValidationIssue[];
}

export interface ModelContentRevisionConflict {
  local: number;
  server: number | null;
}

export type ModelContentSaveState = "idle" | "saving" | "saved" | "error" | "conflict" | "invalid";
