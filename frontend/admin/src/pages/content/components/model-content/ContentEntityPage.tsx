import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, CircleAlert, GripVertical, Plus, Search } from "lucide-react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SortableItem, SortableList } from "@/components/ui/sortable-list";
import { pluralRu } from "@/lib/format";
import type { DataModelEntity, ModelContentRecord } from "@/types";
import { modelRecordTitle, reorderModelRecords, reorderModelRecordsById } from "../../model-content-workspace";
import { contentHref, recordPath } from "../../model-content-paths";
import type { ModelContentDraftUpdater, ModelContentRecordValidation } from "./types";

export function ContentEntityPage({
  entity,
  recordsForEntity,
  visibleRecords,
  search,
  snapshotId,
  validation,
  disabled,
  onSearchChange,
  onAddRecord,
  updateDraft,
}: {
  entity: DataModelEntity;
  recordsForEntity: ModelContentRecord[];
  visibleRecords: ModelContentRecord[];
  search: string;
  snapshotId: string;
  validation: ModelContentRecordValidation[];
  disabled: boolean;
  onSearchChange: (value: string) => void;
  onAddRecord: () => void;
  updateDraft: ModelContentDraftUpdater;
}) {
  const issueCounts = new Map(validation.map((item) => [item.recordId, item.issues.length]));
  const singleton = entity.cardinality === "singleton";

  return <div className="space-y-6">
    <Link to={contentHref("/content", snapshotId)} className="inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft className="size-4" />Контент
    </Link>

    <header className="flex flex-wrap items-end justify-between gap-4 border-b border-border pb-5">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="break-words text-2xl font-semibold tracking-tight">{entity.name}</h1>
          <Badge variant="outline">{singleton ? "Одна запись" : "Коллекция"}</Badge>
        </div>
        <p className="mt-1 text-sm text-muted-foreground">
          {recordsForEntity.length} {pluralRu(recordsForEntity.length, ["запись", "записи", "записей"])} · полей в схеме: {entity.fields.length}
        </p>
      </div>
      {!singleton && <Button type="button" className="min-h-11" disabled={disabled} onClick={onAddRecord}><Plus />Добавить запись</Button>}
    </header>

    {recordsForEntity.length > 0 && !singleton && <label className="relative block max-w-xl">
      <span className="sr-only">Поиск записей</span>
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
      <Input type="search" value={search} onChange={(event) => onSearchChange(event.currentTarget.value)} placeholder="Название или значение поля" className="min-h-11 pl-9" />
    </label>}

    {recordsForEntity.length === 0 ? <section className="border-y border-dashed border-border py-14 text-center">
      <h2 className="font-medium">{singleton ? "Единственная запись не создана" : "Записей пока нет"}</h2>
      <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-muted-foreground">
        {singleton ? "Сервер должен создать одну пустую запись при добавлении singleton-сущности в модель." : "Добавьте первую запись. Она сохранится в черновике выбранного снапшота."}
      </p>
      {!singleton && <Button type="button" className="mt-5 min-h-11" disabled={disabled} onClick={onAddRecord}><Plus />Добавить запись</Button>}
    </section> : visibleRecords.length === 0 ? <p className="border-y border-dashed border-border py-12 text-center text-sm text-muted-foreground">Совпадений нет. Измените поисковый запрос.</p> : <SortableList
      items={visibleRecords.map((record) => record.id)}
      onReorder={(orderedIDs) => {
        if (search || singleton) return;
        updateDraft((current) => ({ ...current, records: reorderModelRecordsById(current.records, entity.id, orderedIDs) }));
      }}
      getItemLabel={(id) => {
        const item = visibleRecords.find((record) => record.id === id);
        return item ? modelRecordTitle(item, entity) : "запись";
      }}
    >
      <ol className="divide-y divide-border border-y border-border" aria-label={`Записи сущности ${entity.name}`}>
        {visibleRecords.map((record) => {
          const recordIndex = recordsForEntity.findIndex((item) => item.id === record.id);
          const title = modelRecordTitle(record, entity);
          const issues = issueCounts.get(record.id) ?? 0;
          return <SortableItem key={record.id} id={record.id} disabled={disabled || Boolean(search) || singleton}>{(handle) => <div className="flex min-w-0 items-center gap-1 py-2">
            {!singleton && <Button
              ref={handle.setActivatorNodeRef}
              type="button"
              size="icon"
              variant="ghost"
              className="size-11 shrink-0 touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
              aria-label={`Перетащить ${title}`}
              disabled={disabled || Boolean(search)}
              {...handle.attributes}
              {...handle.listeners}
            ><GripVertical /></Button>}
            <Link
              to={contentHref(recordPath(entity.key, record.id), snapshotId)}
              className="grid min-h-14 min-w-0 flex-1 gap-1 rounded-md px-2 py-2 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center"
            >
              <span className="min-w-0">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm font-medium" title={title}>{title}</span>
                  {issues > 0 && <Badge variant="destructive"><CircleAlert size={12} />{issues}</Badge>}
                </span>
                <span className="mt-0.5 block truncate text-xs text-muted-foreground" title={recordPreview(record)}>{recordPreview(record)}</span>
              </span>
              <span className="flex items-center justify-end gap-2 text-xs text-muted-foreground">Открыть запись<ArrowRight className="size-4" /></span>
            </Link>
            {!singleton && <Button type="button" size="icon" variant="ghost" className="size-11 shrink-0" aria-label={`Поднять ${title}`} disabled={disabled || Boolean(search) || recordIndex <= 0} onClick={() => updateDraft((current) => ({ ...current, records: reorderModelRecords(current.records, entity.id, record.id, -1) }))}><ArrowUp /></Button>}
            {!singleton && <Button type="button" size="icon" variant="ghost" className="size-11 shrink-0" aria-label={`Опустить ${title}`} disabled={disabled || Boolean(search) || recordIndex >= recordsForEntity.length - 1} onClick={() => updateDraft((current) => ({ ...current, records: reorderModelRecords(current.records, entity.id, record.id, 1) }))}><ArrowDown /></Button>}
          </div>}</SortableItem>;
        })}
      </ol>
    </SortableList>}
  </div>;
}

function recordPreview(record: ModelContentRecord): string {
  const scalarValues = Object.values(record.values)
    .filter((value) => typeof value === "string" || typeof value === "number" || typeof value === "boolean")
    .map(String)
    .filter((value) => value && !/^[0-9a-f]{8}-[0-9a-f-]{27,}$/iu.test(value));
  return scalarValues.length ? scalarValues.slice(0, 2).join(" · ") : "Поля пока не заполнены";
}
