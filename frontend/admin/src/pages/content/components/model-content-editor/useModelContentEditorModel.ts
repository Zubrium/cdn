import { useMemo } from "react";
import { validateEntryFields } from "@/domains/content-editor/EntryForm";
import type { MediaRenditionContext, ModelContentWorkspace } from "@/types";
import type { ModelContentDraft } from "../model-content/types";
import {
  duplicateUniqueModelValueIssues,
  modelContentRelationIssues,
  modelEntityFields,
  modelRecordTitle,
  orphanedModelContentSelections,
  orphanedModelRecordData,
  selectedModelRecord,
} from "../../model-content-workspace";

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
  const mediaEditorProps = useMemo(() => {
    const preparations: Record<string, MediaRenditionContext[]> = {};
    const accepts: Record<string, "image" | "raster-image" | "video" | "all"> = {};
    for (const placement of workspace.placements) {
      for (const input of placement.inputs) {
        for (const field of input.fields) {
          if (field.type !== "media") continue;
          const contexts = (field.preparations ?? []).map((item) => ({ ...item, snapshotId: workspace.snapshot.id }));
          const current = preparations[field.fieldId] ?? [];
          const byKey = new Map(current.map((item) => [JSON.stringify(item), item]));
          contexts.forEach((item) => byKey.set(JSON.stringify(item), item));
          preparations[field.fieldId] = [...byKey.values()];
          const declared = contexts.map((item) => field.preparations?.find((candidate) => candidate.featureKey === item.featureKey && candidate.buildVersion === item.buildVersion && candidate.fieldPath === item.fieldPath)?.accept ?? "image");
          const combined = !declared.length ? "image"
            : declared.every((item) => item === "video") ? "video"
              : declared.every((item) => item === "raster-image") ? "raster-image"
                : declared.every((item) => item === "image" || item === "raster-image") ? (declared.includes("raster-image") ? "raster-image" : "image")
                  : "all";
          const previous = accepts[field.fieldId];
          accepts[field.fieldId] = previous && previous !== combined ? "all" : combined;
        }
      }
    }
    return { preparations, accepts };
  }, [workspace.placements, workspace.snapshot.id]);
  const recordsForEntity = useMemo(
    () => draft.records.filter((record) => record.entityId === entityId).sort((left, right) => left.sortOrder - right.sortOrder),
    [draft.records, entityId],
  );
  const selectedEntity = entities.find((entity) => entity.id === entityId) ?? null;
  const selectedRecord = selectedModelRecord(selectedEntity, recordsForEntity, recordId);
  const validation = useMemo(() => {
    const uniqueValueIssues = model ? duplicateUniqueModelValueIssues(draft.records, model) : [];
    const relationIssues = model ? modelContentRelationIssues(draft.records, model) : [];
    const recordIssues = draft.records.flatMap((record) => {
      const entity = entities.find((item) => item.id === record.entityId);
      if (!entity) return [{ recordId: record.id, issues: [{ path: "entityId", message: "Сущность записи больше не существует." }] }];
      const orphaned = model ? orphanedModelRecordData(record, model) : { unknownFieldKeys: [], unknownRelationIds: [] };
      const structuralIssues = [
        ...orphaned.unknownFieldKeys.map((key) => ({ path: `values.${key}`, message: "Поле отсутствует в модели." })),
        ...orphaned.unknownRelationIds.map((id) => ({ path: `relations.${id}`, message: "Связь отсутствует в модели." })),
      ];
      const uniqueIssues = uniqueValueIssues.filter((issue) => issue.recordId === record.id)
        .map((issue) => ({ path: issue.fieldKey, message: issue.message }));
      const linkedRecordIssues = relationIssues.filter((issue) => issue.recordId === record.id)
        .map((issue) => ({ path: `relations.${issue.relationId}`, message: issue.message }));
      return [{ recordId: record.id, issues: [...validateEntryFields(modelEntityFields(entity), record.values), ...structuralIssues, ...uniqueIssues, ...linkedRecordIssues] }];
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
