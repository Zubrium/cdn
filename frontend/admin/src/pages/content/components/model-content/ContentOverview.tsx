import { ArrowRight, CircleAlert, Database, Layers3, ListChecks } from "lucide-react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { pluralRu } from "@/lib/format";
import type { DataModel, DataModelEntity, ModelContentRecord } from "@/types";
import { modelRecordTitle } from "../../model-content-workspace";
import { contentHref, entityPath, placementsPath } from "../../model-content-paths";
import type { ModelContentRecordValidation } from "./types";

export function ContentOverview({
  model,
  entities,
  records,
  placementsCount,
  validation,
  orphanedEntityRecords,
  orphanedSelections,
  snapshotId,
}: {
  model: DataModel;
  entities: DataModelEntity[];
  records: ModelContentRecord[];
  placementsCount: number;
  validation: ModelContentRecordValidation[];
  orphanedEntityRecords: ModelContentRecord[];
  orphanedSelections: number;
  snapshotId: string;
}) {
  const invalidByRecord = new Map(validation.map((item) => [item.recordId, item.issues.length]));
  const issueCount = validation.reduce((sum, item) => sum + item.issues.length, 0);
  const recordCounts = new Map<string, number>();
  for (const record of records) recordCounts.set(record.entityId, (recordCounts.get(record.entityId) ?? 0) + 1);

  return <div className="space-y-8">
    <section aria-label="Сводка контента" className="grid border-y border-border sm:grid-cols-4">
      <OverviewMetric label="Сущности" value={model.entities.length} icon={Database} />
      <OverviewMetric label="Записи" value={records.length} icon={Layers3} />
      <OverviewMetric label="Размещения" value={placementsCount} icon={ListChecks} />
      <OverviewMetric label="Ошибки" value={issueCount} icon={CircleAlert} warning={issueCount > 0} />
    </section>

    {(orphanedEntityRecords.length > 0 || orphanedSelections > 0) && <section className="border-l-2 border-warning bg-warning/5 px-4 py-3" aria-label="Требует внимания">
      <h2 className="text-sm font-medium">Есть устаревшие данные</h2>
      <p className="mt-1 text-sm text-muted-foreground">
        {orphanedEntityRecords.length > 0 ? `Записей удалённых сущностей: ${orphanedEntityRecords.length}. ` : ""}
        {orphanedSelections > 0 ? `Устаревших выборов размещений: ${orphanedSelections}.` : ""}
      </p>
    </section>}

    <section aria-labelledby="content-structure-title">
      <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border pb-3">
        <div>
          <h2 id="content-structure-title" className="text-lg font-semibold">Структура контента</h2>
          <p className="mt-1 text-sm text-muted-foreground">Откройте сущность, чтобы выбрать запись и перейти к редактированию.</p>
        </div>
      </div>
      {entities.length === 0 ? <p className="py-8 text-center text-sm text-muted-foreground">В модели пока нет сущностей.</p> : <>
        <EntityGroup
          title="Коллекции"
          entities={entities.filter((entity) => entity.cardinality !== "singleton")}
          records={records}
          invalidByRecord={invalidByRecord}
          snapshotId={snapshotId}
        />
        <EntityGroup
          title="Единичные настройки"
          entities={entities.filter((entity) => entity.cardinality === "singleton")}
          records={records}
          invalidByRecord={invalidByRecord}
          snapshotId={snapshotId}
        />
      </>}
    </section>

    <section aria-labelledby="content-placements-title" className="border-t border-border pt-5">
      <Link
        to={contentHref(placementsPath, snapshotId)}
        className="group flex min-w-0 flex-col items-start justify-between gap-2 py-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:flex-row sm:items-center sm:gap-4"
      >
        <span>
          <span id="content-placements-title" className="block text-sm font-medium">Записи на страницах</span>
          <span className="mt-1 block text-sm text-muted-foreground">Что из опубликованного контента показывают фичи сайта.</span>
        </span>
        <span className="flex shrink-0 items-center gap-2 text-sm text-muted-foreground">{placementsCount} размещений<ArrowRight className="size-4 group-hover:translate-x-0.5" /></span>
      </Link>
    </section>
  </div>;
}

function EntityGroup({
  title,
  entities,
  records,
  invalidByRecord,
  snapshotId,
}: {
  title: string;
  entities: DataModelEntity[];
  records: ModelContentRecord[];
  invalidByRecord: Map<string, number>;
  snapshotId: string;
}) {
  if (entities.length === 0) return null;
  return <div className="mt-5">
    <h2 className="mb-1 text-xs font-medium text-muted-foreground">{title}</h2>
    <div className="divide-y divide-border border-t border-border">
      {entities.map((entity) => {
        const entityRecords = records.filter((record) => record.entityId === entity.id).sort((left, right) => left.sortOrder - right.sortOrder);
        const issues = entityRecords.reduce((sum, record) => sum + (invalidByRecord.get(record.id) ?? 0), 0);
        const preview = entityRecords.slice(0, 2).map((record) => modelRecordTitle(record, entity)).join(" · ");
        return <Link
          key={entity.id}
          to={contentHref(entityPath(entity.key), snapshotId)}
          className="group grid min-w-0 gap-3 px-1 py-4 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:grid-cols-[minmax(0,1fr)_10rem_auto] sm:items-center"
        >
          <span className="min-w-0">
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate text-sm font-medium" title={entity.name}>{entity.name}</span>
              {issues > 0 && <Badge variant="destructive">Ошибок: {issues}</Badge>}
            </span>
            <span className="mt-1 block truncate text-xs text-muted-foreground" title={preview || "Записей пока нет"}>{preview || "Записей пока нет"}</span>
          </span>
          <span className="font-mono text-xs text-muted-foreground">{entityRecords.length} {pluralRu(entityRecords.length, ["запись", "записи", "записей"])}</span>
          <span className="flex items-center justify-end gap-2 text-sm text-muted-foreground group-hover:text-foreground">Открыть<ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" /></span>
        </Link>;
      })}
    </div>
  </div>;
}

function OverviewMetric({
  label,
  value,
  icon: Icon,
  warning = false,
}: {
  label: string;
  value: number;
  icon: typeof Database;
  warning?: boolean;
}) {
  return <div className="flex items-center gap-3 border-b border-border py-4 sm:border-b-0 sm:border-r sm:px-4 sm:last:border-r-0">
    <Icon className={`size-4 ${warning ? "text-destructive" : "text-muted-foreground"}`} aria-hidden="true" />
    <div><p className={`text-xl font-semibold ${warning ? "text-destructive" : ""}`}>{value}</p><p className="text-xs text-muted-foreground">{label}</p></div>
  </div>;
}
