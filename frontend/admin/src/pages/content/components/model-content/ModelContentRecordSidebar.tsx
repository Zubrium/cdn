import { ArrowDown, ArrowUp, GripVertical, Plus, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SortableItem, SortableList, type SortableDragHandleProps } from "@/components/ui/sortable-list";
import type { DataModelEntity, ModelContentRecord } from "@/types";
import { modelRecordTitle, reorderModelRecords, reorderModelRecordsById } from "../../model-content-workspace";
import type { ModelContentDraftUpdater, ModelContentRecordValidation } from "./types";

export interface ModelContentRecordSidebarProps {
  entities: DataModelEntity[];
  records: ModelContentRecord[];
  selectedEntity: DataModelEntity | null;
  entityId: string;
  recordId: string;
  search: string;
  recordsForEntity: ModelContentRecord[];
  visibleRecords: ModelContentRecord[];
  validation: ModelContentRecordValidation[];
  disabled: boolean;
  onSelectEntity: (entityId: string) => void;
  onSelectRecord: (recordId: string) => void;
  onSearchChange: (search: string) => void;
  onAddRecord: () => void;
  updateDraft: ModelContentDraftUpdater;
}

export function ModelContentRecordSidebar({
  entities,
  records,
  selectedEntity,
  entityId,
  recordId,
  search,
  recordsForEntity,
  visibleRecords,
  validation,
  disabled,
  onSelectEntity,
  onSelectRecord,
  onSearchChange,
  onAddRecord,
  updateDraft,
}: ModelContentRecordSidebarProps) {
  return <aside aria-label="Сущности и записи" className="min-w-0 space-y-3">
    <nav aria-label="Сущности" className="flex min-w-0 gap-2 overflow-x-auto pb-1 xl:flex-col xl:overflow-visible">
      {entities.map((entity) => (
        <Button key={entity.id} type="button" variant={entityId === entity.id ? "secondary" : "outline"} disabled={disabled} className="min-h-11 justify-between gap-3 whitespace-nowrap xl:w-full" onClick={() => onSelectEntity(entity.id)}>
          <span className="truncate">{entity.name}</span><span className="text-xs text-muted-foreground">{records.filter((record) => record.entityId === entity.id).length}</span>
        </Button>
      ))}
    </nav>

    {selectedEntity && selectedEntity.cardinality !== "singleton" && <div className="min-w-0 space-y-3 rounded-xl border border-border bg-card p-3">
      <div className="flex min-w-0 items-center justify-between gap-2">
        <h2 className="truncate text-sm font-semibold">{selectedEntity.name}</h2>
        <Button type="button" size="sm" className="min-h-11" disabled={disabled} onClick={onAddRecord}><Plus />Добавить</Button>
      </div>
      {recordsForEntity.length > 0 && <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
        <Input value={search} onChange={(event) => onSearchChange(event.currentTarget.value)} placeholder="Найти запись" aria-label="Найти запись" className="pl-9" />
      </div>}
      {recordsForEntity.length === 0 ? <p className="rounded-lg border border-dashed border-border p-4 text-center text-sm text-muted-foreground">Записей пока нет.</p> : visibleRecords.length === 0 ? <p className="p-3 text-sm text-muted-foreground">Совпадений нет.</p> : (
        <SortableList
          items={visibleRecords.map((record) => record.id)}
          getItemLabel={(id) => {
            const item = visibleRecords.find((record) => record.id === id);
            return item ? modelRecordTitle(item, selectedEntity) : "запись";
          }}
          onReorder={(orderedIDs) => {
            if (search) return;
            updateDraft((current) => ({ ...current, records: reorderModelRecordsById(current.records, selectedEntity.id, orderedIDs) }));
          }}
        >
          <ol className="min-w-0 divide-y divide-border overflow-hidden rounded-lg border border-border" aria-label={`Записи сущности ${selectedEntity.name}`}>
            {visibleRecords.map((record) => <SortableItem key={record.id} id={record.id} disabled={disabled || Boolean(search)}>
              {(handle) => <ModelContentRecordListItem
                record={record}
                entity={selectedEntity}
                recordsForEntity={recordsForEntity}
                selected={record.id === recordId}
                disabled={disabled || Boolean(search)}
                issueCount={validation.find((item) => item.recordId === record.id)?.issues.length ?? 0}
                dragHandle={handle}
                onSelectRecord={onSelectRecord}
                updateDraft={updateDraft}
              />}
            </SortableItem>)}
          </ol>
        </SortableList>
      )}
    </div>}
  </aside>;
}

interface ModelContentRecordListItemProps {
  record: ModelContentRecord;
  entity: DataModelEntity;
  recordsForEntity: ModelContentRecord[];
  selected: boolean;
  disabled: boolean;
  issueCount: number;
  dragHandle: SortableDragHandleProps;
  onSelectRecord: (recordId: string) => void;
  updateDraft: ModelContentDraftUpdater;
}

function ModelContentRecordListItem({
  record,
  entity,
  recordsForEntity,
  selected,
  disabled,
  issueCount,
  dragHandle,
  onSelectRecord,
  updateDraft,
}: ModelContentRecordListItemProps) {
  const recordIndex = recordsForEntity.findIndex((item) => item.id === record.id);
  const title = modelRecordTitle(record, entity);

  return <div className="flex min-w-0 items-center gap-1 p-1.5">
    <Button
      ref={dragHandle.setActivatorNodeRef}
      type="button"
      size="icon-sm"
      variant="ghost"
      className="min-h-11 min-w-11 touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
      aria-label={`Перетащить ${title}`}
      disabled={disabled}
      {...dragHandle.attributes}
      {...dragHandle.listeners}
    ><GripVertical /></Button>
    <Button type="button" variant={selected ? "secondary" : "ghost"} disabled={disabled} className="min-w-0 flex-1 justify-start truncate text-left" onClick={() => onSelectRecord(record.id)}>
      <span className="truncate">{title}</span>
      {issueCount > 0 && <span className="ml-auto text-xs text-destructive">Ошибок: {issueCount}</span>}
    </Button>
    <Button type="button" size="icon-sm" variant="ghost" className="min-h-11 min-w-11" aria-label={`Переместить ${title} выше`} disabled={recordIndex <= 0 || disabled} onClick={() => updateDraft((current) => ({ ...current, records: reorderModelRecords(current.records, entity.id, record.id, -1) }))}><ArrowUp /></Button>
    <Button type="button" size="icon-sm" variant="ghost" className="min-h-11 min-w-11" aria-label={`Переместить ${title} ниже`} disabled={recordIndex >= recordsForEntity.length - 1 || disabled} onClick={() => updateDraft((current) => ({ ...current, records: reorderModelRecords(current.records, entity.id, record.id, 1) }))}><ArrowDown /></Button>
  </div>;
}
