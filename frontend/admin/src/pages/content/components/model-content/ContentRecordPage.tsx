import { ArrowLeft, ArrowRight, ChevronLeft } from "lucide-react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import type { DataModel, DataModelEntity, JsonValue, MediaRenditionContext, ModelContentRecord } from "@/types";
import { modelRecordTitle } from "../../model-content-workspace";
import { contentHref, entityPath, recordPath } from "../../model-content-paths";
import { ModelContentRecordDetailsPanel } from "./ModelContentRecordDetailsPanel";
import type { ModelContentRecordValidation } from "./types";

export function ContentRecordPage({
  model,
  entity,
  record,
  recordsForEntity,
  snapshotId,
  validation,
  mediaPreparationsByFieldId,
  mediaAcceptByFieldId,
  disabled,
  deletePending,
  onDeleteRecord,
  onClearSingletonRecord,
  onCleanOrphanedRecordData,
  onChangeField,
}: {
  model: DataModel;
  entity: DataModelEntity;
  record: ModelContentRecord;
  recordsForEntity: ModelContentRecord[];
  snapshotId: string;
  validation: ModelContentRecordValidation[];
  mediaPreparationsByFieldId: Record<string, MediaRenditionContext[]>;
  mediaAcceptByFieldId: Record<string, "image" | "raster-image" | "video" | "all">;
  disabled: boolean;
  deletePending: boolean;
  onDeleteRecord: (record: ModelContentRecord) => void;
  onClearSingletonRecord: (record: ModelContentRecord) => void;
  onCleanOrphanedRecordData: (record: ModelContentRecord) => void;
  onChangeField: (key: string, value: JsonValue | undefined) => void;
}) {
  const index = recordsForEntity.findIndex((item) => item.id === record.id);
  const previous = index > 0 ? recordsForEntity[index - 1] : undefined;
  const next = index >= 0 && index < recordsForEntity.length - 1 ? recordsForEntity[index + 1] : undefined;

  return <div className="space-y-5">
    <nav aria-label="Путь к записи" className="flex min-w-0 flex-wrap items-center gap-1 text-sm text-muted-foreground">
      <Link to={contentHref("/content", snapshotId)} className="inline-flex min-h-11 items-center gap-1.5 px-1 hover:text-foreground"><ChevronLeft className="size-4" />Контент</Link>
      <span aria-hidden="true">/</span>
      <Link to={contentHref(entityPath(entity.key), snapshotId)} className="inline-flex min-h-11 items-center px-1 hover:text-foreground">{entity.name}</Link>
    </nav>

    <ModelContentRecordDetailsPanel
      model={model}
      selectedEntity={entity}
      selectedRecord={record}
      validation={validation}
      mediaPreparationsByFieldId={mediaPreparationsByFieldId}
      mediaAcceptByFieldId={mediaAcceptByFieldId}
      disabled={disabled}
      deletePending={deletePending}
      onDeleteRecord={onDeleteRecord}
      onClearSingletonRecord={onClearSingletonRecord}
      onCleanOrphanedRecordData={onCleanOrphanedRecordData}
      onChangeField={onChangeField}
    />

    {recordsForEntity.length > 1 && <nav aria-label="Переход между записями" className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
      {previous ? <Button asChild variant="outline" className="min-h-11 max-w-[48%] justify-start">
        <Link to={contentHref(recordPath(entity.key, previous.id), snapshotId)}><ArrowLeft /><span className="truncate">Назад: {modelRecordTitle(previous, entity)}</span></Link>
      </Button> : <span />}
      {next && <Button asChild variant="outline" className="min-h-11 max-w-[48%] justify-end">
        <Link to={contentHref(recordPath(entity.key, next.id), snapshotId)}><span className="truncate">Далее: {modelRecordTitle(next, entity)}</span><ArrowRight /></Link>
      </Button>}
    </nav>}
  </div>;
}

export function ContentRecordNotFound({ entity, snapshotId }: { entity?: DataModelEntity; snapshotId: string }) {
  return <div className="border-y border-dashed border-border py-14 text-center">
    <h1 className="text-xl font-semibold">Запись не найдена</h1>
    <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-muted-foreground">Она могла быть удалена из рабочего черновика или ссылка относится к другому снапшоту.</p>
    <Button asChild variant="outline" className="mt-5 min-h-11">
      <Link to={contentHref(entity ? entityPath(entity.key) : "/content", snapshotId)}><ArrowLeft />{entity ? `К сущности «${entity.name}»` : "К контенту"}</Link>
    </Button>
  </div>;
}
