import { useMemo } from "react";
import { validateEntryFields } from "@/domains/content-editor/EntryForm";
import type { MediaRenditionContext, ModelContentWorkspace } from "@/types";
import type { ModelContentDraft } from "../model-content/types";
import {
  duplicateUniqueModelValueIssues,
  modelEntityFields,
  modelRecordTitle,
  orphanedModelContentSelections,
  orphanedModelRecordData,
  selectedModelRecord,
} from "../../model-content-workspace";

/** Derive each job target from the trusted projection for the pinned snapshot. */
export function contentMediaRequirements(workspace: ModelContentWorkspace) {
  const preparations: Record<string, MediaRenditionContext[]> = {};
  const accepts: Record<string, "image" | "raster-image" | "video" | "all" | "none"> = {};
  for (const placement of workspace.placements) {
    for (const input of placement.inputs) {
      for (const field of input.fields) {
        if (field.type !== "media" || !field.mediaAccept) continue;
        const fieldPath = `/${input.inputKey}${input.kind === "scalar" ? "" : `/${field.inputPath.replace(/^\//, "")}`}`;
        const context: MediaRenditionContext = { snapshotId: workspace.snapshot.id, placementId: placement.id, schemaKind: "dataInputs", fieldPath };
        const current = preparations[field.fieldId] ?? [];
        if (!current.some((item) => JSON.stringify(item) === JSON.stringify(context))) current.push(context);
        preparations[field.fieldId] = current;
        const accept = field.mediaAccept === "file" ? "all" : field.mediaAccept;
        const previous = accepts[field.fieldId];
        if (!previous || previous === "all") accepts[field.fieldId] = accept;
        else if (accept === "all" || previous === accept) accepts[field.fieldId] = previous;
        else if ((previous === "image" && accept === "raster-image") || (previous === "raster-image" && accept === "image")) accepts[field.fieldId] = "raster-image";
        else accepts[field.fieldId] = "none";
      }
    }
  }
  return { preparations, accepts };
}

/** Derives the active entity, validation, media rules and visible record list. */
export function useModelContentEditorModel(
  workspace: ModelContentWorkspace,
  draft: ModelContentDraft,
  entityId: string,
  recordId: string,
  search: string,
) {
  const model = workspace.dataModel;
  const entities = useMemo(() => model?.entities ?? [], [model?.entities]);
  const mediaEditorProps = useMemo(() => contentMediaRequirements(workspace), [workspace]);
  const recordsForEntity = useMemo(
    () => draft.records.filter((record) => record.entityId === entityId).sort((left, right) => left.sortOrder - right.sortOrder),
    [draft.records, entityId],
  );
  const selectedEntity = entities.find((entity) => entity.id === entityId) ?? null;
  const selectedRecord = selectedModelRecord(selectedEntity, recordsForEntity, recordId);
  const validation = useMemo(() => {
    const uniqueValueIssues = model ? duplicateUniqueModelValueIssues(draft.records, model) : [];
    const recordIssues = draft.records.flatMap((record) => {
      const entity = entities.find((item) => item.id === record.entityId);
      if (!entity) return [{ recordId: record.id, issues: [{ path: "entityId", message: "Сущность записи больше не существует." }] }];
      const orphaned = model ? orphanedModelRecordData(record, model) : { unknownFieldKeys: [] };
      const structuralIssues = [
        ...orphaned.unknownFieldKeys.map((key) => ({ path: `values.${key}`, message: "Поле отсутствует в модели." })),
      ];
      const uniqueIssues = uniqueValueIssues.filter((issue) => issue.recordId === record.id)
        .map((issue) => ({ path: issue.fieldKey, message: issue.message }));
      return [{ recordId: record.id, issues: [...validateEntryFields(modelEntityFields(entity), record.values), ...structuralIssues, ...uniqueIssues] }];
    }).filter((item) => item.issues.length > 0);
    const singletonIntegrityIssues = entities.flatMap((entity) => {
      if (entity.cardinality !== "singleton") return [];
      const recordCount = draft.records.filter((record) => record.entityId === entity.id).length;
      return recordCount === 1 ? [] : [{
        recordId: `singleton:${entity.id}`,
        issues: [{ path: "cardinality", message: `Сущность «${entity.name}» должна иметь одну запись; сейчас записей: ${recordCount}.` }],
      }];
    });
    return [...recordIssues, ...singletonIntegrityIssues];
  }, [draft.records, entities, model]);
  const invalidCount = validation.reduce((sum, item) => sum + item.issues.length, 0);
  const orphanedEntityRecords = model ? draft.records.filter((record) => !entities.some((entity) => entity.id === record.entityId)) : [];
  const orphanedSelections = useMemo(
    () => orphanedModelContentSelections(draft.selections, workspace.placements),
    [draft.selections, workspace.placements],
  );
  const visibleRecords = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("ru");
    if (!needle || !selectedEntity) return recordsForEntity;
    return recordsForEntity.filter((record) => `${modelRecordTitle(record, selectedEntity)} ${JSON.stringify(record.values)}`.toLocaleLowerCase("ru").includes(needle));
  }, [recordsForEntity, search, selectedEntity]);

  return {
    model,
    entities,
    mediaEditorProps,
    recordsForEntity,
    selectedEntity,
    selectedRecord,
    validation,
    invalidCount,
    orphanedEntityRecords,
    orphanedSelections,
    visibleRecords,
  };
}
