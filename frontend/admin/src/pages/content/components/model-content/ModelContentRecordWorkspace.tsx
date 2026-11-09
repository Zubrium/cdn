import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import type { DataModel, DataModelEntity, JsonValue, MediaRenditionContext, ModelContentRecord } from "@/types";
import { ModelContentRecordDetailsPanel } from "./ModelContentRecordDetailsPanel";
import { ModelContentRecordEmptyPanel } from "./ModelContentRecordEmptyPanel";
import { ModelContentRecordSidebar } from "./ModelContentRecordSidebar";
import type { ModelContentDraft, ModelContentDraftUpdater, ModelContentRecordValidation } from "./types";

export interface ModelContentRecordWorkspaceProps {
  model: DataModel;
  draft: ModelContentDraft;
  savedDraft: ModelContentDraft;
  entities: DataModelEntity[];
  selectedEntity: DataModelEntity | null;
  selectedRecord: ModelContentRecord | null;
  entityId: string;
  recordId: string;
  search: string;
  recordsForEntity: ModelContentRecord[];
  visibleRecords: ModelContentRecord[];
  orphanedEntityRecords: ModelContentRecord[];
  validation: ModelContentRecordValidation[];
  mediaPreparationsByFieldId: Record<string, MediaRenditionContext[]>;
  mediaAcceptByFieldId: Record<string, "image" | "raster-image" | "video" | "all">;
  disabled: boolean;
  deletePending: boolean;
  onSelectEntity: (entityId: string) => void;
  onSelectRecord: (recordId: string) => void;
  onSearchChange: (search: string) => void;
  onAddRecord: () => void;
  onDeleteRecord: (record: ModelContentRecord) => void;
  onClearSingletonRecord: (record: ModelContentRecord) => void;
  onDeleteOrphanedEntityRecord: (record: ModelContentRecord) => void;
  onCleanOrphanedRecordData: (record: ModelContentRecord) => void;
  onChangeField: (key: string, value: JsonValue | undefined) => void;
  onOpenTargetRecord: (record: ModelContentRecord) => void;
  onConfirmCleanup: (title: string, description: string) => Promise<boolean>;
  updateDraft: ModelContentDraftUpdater;
  beforeContent?: ReactNode;
  placementSelections?: ReactNode;
}

export function ModelContentRecordWorkspace({
  model,
  draft,
  savedDraft,
  entities,
  selectedEntity,
  selectedRecord,
  entityId,
  recordId,
  search,
  recordsForEntity,
  visibleRecords,
  orphanedEntityRecords,
  validation,
  mediaPreparationsByFieldId,
  mediaAcceptByFieldId,
  disabled,
  deletePending,
  onSelectEntity,
  onSelectRecord,
  onSearchChange,
  onAddRecord,
  onDeleteRecord,
  onClearSingletonRecord,
  onDeleteOrphanedEntityRecord,
  onCleanOrphanedRecordData,
  onChangeField,
  onOpenTargetRecord,
  onConfirmCleanup,
  updateDraft,
  beforeContent,
  placementSelections,
}: ModelContentRecordWorkspaceProps) {
  return <>
    {beforeContent}
    {orphanedEntityRecords.length > 0 && <section role="alert" aria-labelledby="orphaned-model-records-title" className="space-y-3 rounded-xl border border-warning/50 bg-warning/10 p-4">
      <div>
        <h2 id="orphaned-model-records-title" className="font-semibold">Записи удалённых сущностей</h2>
        <p className="text-sm text-muted-foreground">Эти унаследованные записи нельзя публиковать, пока сущность не восстановлена или запись явно не удалена из черновика.</p>
      </div>
      <ul className="space-y-2">
        {orphanedEntityRecords.map((record) => <li key={record.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-card p-3">
          <div className="min-w-0 text-sm">
            <p className="font-medium">Запись удалённой сущности</p>
            <p className="break-all text-xs text-muted-foreground">{record.id} · значений: {Object.keys(record.values).length}</p>
          </div>
          <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={deletePending || disabled} onClick={() => onDeleteOrphanedEntityRecord(record)}>Удалить запись</Button>
        </li>)}
      </ul>
    </section>}

    {entities.length === 0 ? (
      <section className="rounded-xl border border-dashed border-border p-8 text-center">
        <h2 className="font-medium">В этом снапшоте нет модели данных</h2>
        <p className="mx-auto mt-2 max-w-lg text-sm text-muted-foreground">Создайте сущности, поля и связи в редакторе модели сайта. Значения записей редактируются здесь.</p>
      </section>
    ) : (
      <section className="grid min-w-0 gap-4 xl:grid-cols-[minmax(14rem,0.8fr)_minmax(0,2fr)]">
        <ModelContentRecordSidebar
          entities={entities}
          records={draft.records}
          selectedEntity={selectedEntity}
          entityId={entityId}
          recordId={recordId}
          search={search}
          recordsForEntity={recordsForEntity}
          visibleRecords={visibleRecords}
          validation={validation}
          disabled={disabled}
          onSelectEntity={onSelectEntity}
          onSelectRecord={onSelectRecord}
          onSearchChange={onSearchChange}
          onAddRecord={onAddRecord}
          updateDraft={updateDraft}
        />
        <div className="min-w-0 space-y-4">
          {selectedEntity && selectedRecord ? <ModelContentRecordDetailsPanel
            model={model}
            draft={draft}
            savedDraft={savedDraft}
            entities={entities}
            selectedEntity={selectedEntity}
            selectedRecord={selectedRecord}
            validation={validation}
            mediaPreparationsByFieldId={mediaPreparationsByFieldId}
            mediaAcceptByFieldId={mediaAcceptByFieldId}
            disabled={disabled}
            deletePending={deletePending}
            onDeleteRecord={onDeleteRecord}
            onClearSingletonRecord={onClearSingletonRecord}
            onCleanOrphanedRecordData={onCleanOrphanedRecordData}
            onChangeField={onChangeField}
            onOpenTargetRecord={onOpenTargetRecord}
            onConfirmCleanup={onConfirmCleanup}
            updateDraft={updateDraft}
          /> : <ModelContentRecordEmptyPanel
            selectedEntity={selectedEntity}
            recordsForEntity={recordsForEntity}
            onAddRecord={onAddRecord}
          />}
          {placementSelections}
        </div>
      </section>
    )}
  </>;
}
