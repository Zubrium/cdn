import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EntryFieldsForm } from "@/domains/content-editor/EntryForm";
import type { DataModel, DataModelEntity, JsonValue, MediaRenditionContext, ModelContentRecord } from "@/types";
import { modelEntityFields, modelRecordTitle, orphanedModelRecordData, updateModelRecordRelation } from "../../model-content-workspace";
import { RelationEditor } from "./RelationEditor";
import type { ModelContentDraft, ModelContentDraftUpdater, ModelContentRecordValidation } from "./types";

export interface ModelContentRecordDetailsPanelProps {
  model: DataModel;
  draft: ModelContentDraft;
  savedDraft: ModelContentDraft;
  entities: DataModelEntity[];
  selectedEntity: DataModelEntity;
  selectedRecord: ModelContentRecord;
  validation: ModelContentRecordValidation[];
  mediaPreparationsByFieldId: Record<string, MediaRenditionContext[]>;
  mediaAcceptByFieldId: Record<string, "image" | "raster-image" | "video" | "all">;
  disabled: boolean;
  deletePending: boolean;
  onDeleteRecord: (record: ModelContentRecord) => void;
  onClearSingletonRecord: (record: ModelContentRecord) => void;
  onCleanOrphanedRecordData: (record: ModelContentRecord) => void;
  onChangeField: (key: string, value: JsonValue | undefined) => void;
  onOpenTargetRecord: (record: ModelContentRecord) => void;
  onConfirmCleanup: (title: string, description: string) => Promise<boolean>;
  updateDraft: ModelContentDraftUpdater;
}

export function ModelContentRecordDetailsPanel({
  model,
  draft,
  savedDraft,
  entities,
  selectedEntity,
  selectedRecord,
  validation,
  mediaPreparationsByFieldId,
  mediaAcceptByFieldId,
  disabled,
  deletePending,
  onDeleteRecord,
  onClearSingletonRecord,
  onCleanOrphanedRecordData,
  onChangeField,
  onOpenTargetRecord,
  onConfirmCleanup,
  updateDraft,
}: ModelContentRecordDetailsPanelProps) {
  const recordValidation = validation.find((item) => item.recordId === selectedRecord.id);
  const orphaned = orphanedModelRecordData(selectedRecord, model);
  const relations = (model.relations ?? []).filter((relation) => relation.fromEntityId === selectedEntity.id);

  return <section className="min-w-0 space-y-5 rounded-xl border border-border bg-card p-4 sm:p-5">
    <header className="flex min-w-0 flex-wrap items-center justify-between gap-3">
      <div className="min-w-0">
        <h2 className="truncate text-lg font-semibold">{modelRecordTitle(selectedRecord, selectedEntity)}</h2>
        <p className="text-xs text-muted-foreground">{selectedEntity.name} · {selectedEntity.cardinality === "singleton" ? "единственная запись" : `порядковый номер ${selectedRecord.sortOrder + 1}`}</p>
      </div>
      {selectedEntity.cardinality === "singleton"
        ? <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={disabled} onClick={() => onClearSingletonRecord(selectedRecord)}>Очистить поля и связи</Button>
        : <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={deletePending || disabled} onClick={() => onDeleteRecord(selectedRecord)}><Trash2 />Удалить</Button>}
    </header>

    <EntryFieldsForm
      fields={modelEntityFields(selectedEntity)}
      mediaPreparationsByFieldId={mediaPreparationsByFieldId}
      mediaAcceptByFieldId={mediaAcceptByFieldId}
      metadata={selectedRecord.values}
      onChange={onChangeField}
      onRemove={(key) => onChangeField(key, undefined)}
      disabled={disabled}
      issues={recordValidation?.issues ?? []}
      compact={false}
    />

    {orphaned.unknownFieldKeys.length > 0 || orphaned.unknownRelationIds.length > 0 ? <section role="alert" aria-labelledby="orphaned-record-data-title" className="space-y-3 rounded-lg border border-warning/50 bg-warning/10 p-3">
      <div>
        <h3 id="orphaned-record-data-title" className="text-sm font-semibold">Унаследованные данные вне модели</h3>
        <p className="text-sm text-muted-foreground">Данные сохранены в черновике, но новые фичи и публичный runtime их не читают. Удалите их здесь явно или восстановите соответствующие поля и связи в модели.</p>
      </div>
      {orphaned.unknownFieldKeys.length > 0 && <ul className="space-y-1 text-sm">{orphaned.unknownFieldKeys.map((key) => <li key={key} className="break-all"><span className="font-medium">Поле «{key}»:</span> <span className="text-muted-foreground">{JSON.stringify(selectedRecord.values[key])}</span></li>)}</ul>}
      {orphaned.unknownRelationIds.length > 0 && <p className="break-all text-sm">Связи без определения: {orphaned.unknownRelationIds.join(", ")}</p>}
      <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={disabled} onClick={() => onCleanOrphanedRecordData(selectedRecord)}><Trash2 />Удалить устаревшие данные из записи</Button>
    </section> : null}

    {relations.map((relation) => {
      const targetEntity = entities.find((entity) => entity.id === relation.toEntityId);
      if (!targetEntity) return null;
      const targets = draft.records.filter((record) => record.entityId === targetEntity.id).sort((left, right) => left.sortOrder - right.sortOrder);
      const recordIDsOwnedByOther = relation.cardinality === "many-to-many" ? new Set<string>() : new Set(
        draft.records
          .filter((record) => record.entityId === relation.fromEntityId && record.id !== selectedRecord.id)
          .flatMap((record) => record.relations?.[relation.id] ?? []),
      );
      const relationIssues = recordValidation?.issues
        .filter((issue) => issue.path === `relations.${relation.id}`)
        .map((issue) => issue.message) ?? [];

      return <RelationEditor
        key={relation.id}
        relation={relation}
        targetEntity={targetEntity}
        targetRecords={targets}
        currentRecord={selectedRecord}
        persistedRecordIDs={new Set(savedDraft.records.map((record) => record.id))}
        recordIDsOwnedByOther={recordIDsOwnedByOther}
        validationIssues={relationIssues}
        disabled={disabled}
        onChange={(recordIDs) => updateDraft((current) => ({
          ...current,
          records: updateModelRecordRelation(current.records, selectedRecord.id, relation.id, recordIDs),
        }))}
        onOpenTargetRecord={onOpenTargetRecord}
        onConfirmCleanup={onConfirmCleanup}
      />;
    })}
  </section>;
}
