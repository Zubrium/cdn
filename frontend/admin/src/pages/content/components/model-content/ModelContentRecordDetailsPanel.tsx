import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EntryFieldsForm } from "@/domains/content-editor/EntryForm";
import type { DataModel, DataModelEntity, JsonValue, MediaRenditionContext, ModelContentRecord } from "@/types";
import { modelEntityFields, modelRecordTitle, orphanedModelRecordData } from "../../model-content-workspace";
import type { ModelContentRecordValidation } from "./types";

export interface ModelContentRecordDetailsPanelProps {
  model: DataModel;
  selectedEntity: DataModelEntity;
  selectedRecord: ModelContentRecord;
  validation: ModelContentRecordValidation[];
  mediaPreparationsByFieldId: Record<string, MediaRenditionContext[]>;
  mediaAcceptByFieldId: Record<string, "image" | "raster-image" | "video" | "all" | "none">;
  disabled: boolean;
  deletePending: boolean;
  onDeleteRecord: (record: ModelContentRecord) => void;
  onClearSingletonRecord: (record: ModelContentRecord) => void;
  onCleanOrphanedRecordData: (record: ModelContentRecord) => void;
  onChangeField: (key: string, value: JsonValue | undefined) => void;
}

export function ModelContentRecordDetailsPanel({
  model,
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
}: ModelContentRecordDetailsPanelProps) {
  const recordValidation = validation.find((item) => item.recordId === selectedRecord.id);
  const orphaned = orphanedModelRecordData(selectedRecord, model);
  const recordTitle = modelRecordTitle(selectedRecord, selectedEntity);

  return <section className="min-w-0 space-y-5 rounded-xl border border-border bg-card p-4 sm:p-5">
    <header className="flex min-w-0 flex-wrap items-center justify-between gap-3">
      <div className="min-w-0">
        <h1 className="truncate text-lg font-semibold" title={recordTitle}>{recordTitle}</h1>
        <p className="text-xs text-muted-foreground">{selectedEntity.name} · {selectedEntity.cardinality === "singleton" ? "единственная запись" : `порядковый номер ${selectedRecord.sortOrder + 1}`}</p>
      </div>
      {selectedEntity.cardinality === "singleton"
        ? <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={disabled} onClick={() => onClearSingletonRecord(selectedRecord)}>Очистить поля</Button>
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

    {orphaned.unknownFieldKeys.length > 0 ? <section role="alert" aria-labelledby="orphaned-record-data-title" className="space-y-3 rounded-lg border border-warning/50 bg-warning/10 p-3">
      <div>
        <h2 id="orphaned-record-data-title" className="text-sm font-semibold">Унаследованные данные вне модели</h2>
        <p className="text-sm text-muted-foreground">Данные сохранены в черновике, но фичи и публичный runtime их не читают. Удалите их здесь явно или восстановите поля в модели.</p>
      </div>
      {orphaned.unknownFieldKeys.length > 0 && <ul className="space-y-1 text-sm">{orphaned.unknownFieldKeys.map((key) => <li key={key} className="break-all"><span className="font-medium">Поле «{key}»:</span> <span className="text-muted-foreground">{JSON.stringify(selectedRecord.values[key])}</span></li>)}</ul>}
      <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={disabled} onClick={() => onCleanOrphanedRecordData(selectedRecord)}><Trash2 />Удалить устаревшие данные из записи</Button>
    </section> : null}
  </section>;
}
