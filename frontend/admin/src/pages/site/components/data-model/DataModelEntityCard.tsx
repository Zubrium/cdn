import type { PointerEvent as ReactPointerEvent } from "react";
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { DataModelEntity, DataModelField, DataModelFieldType } from "@/types";
import {
  dataModelAddFieldControlID,
  dataModelEntityNameControlID,
  dataModelFieldNameControlID,
  entityCardinalities,
  fieldTypes,
  parseDataModelEnumOptions,
  validModelName,
} from "./model-operations";

export interface DataModelPosition {
  x: number;
  y: number;
}

export interface DataModelEntityCardActions {
  onAddField: (entity: DataModelEntity) => void;
  onChangeFieldType: (entity: DataModelEntity, field: DataModelField, type: DataModelFieldType) => void;
  onChangeEntityCardinality: (entity: DataModelEntity, cardinality: NonNullable<DataModelEntity["cardinality"]>) => void;
  onMoveEntity: (entityID: string, x: number, y: number) => void;
  onMoveField: (entityID: string, fieldID: string, offset: -1 | 1) => void;
  onRemoveEntity: (entity: DataModelEntity) => void;
  onRemoveField: (entity: DataModelEntity, field: DataModelField) => void;
  onRenameEntity: (entityID: string, name: string) => void;
  onRenameField: (entityID: string, fieldID: string, name: string) => void;
  onUpdateField: (entityID: string, fieldID: string, transform: (field: DataModelField) => DataModelField) => void;
}

interface Props extends DataModelEntityCardActions {
  entity: DataModelEntity;
  graphView: boolean;
  onStartDrag?: (event: ReactPointerEvent<HTMLDivElement>, entity: DataModelEntity, position: DataModelPosition) => void;
  position: DataModelPosition;
}

export function DataModelEntityCard({
  entity,
  graphView,
  onAddField,
  onChangeFieldType,
  onChangeEntityCardinality,
  onMoveEntity,
  onMoveField,
  onRemoveEntity,
  onRemoveField,
  onRenameEntity,
  onRenameField,
  onStartDrag,
  onUpdateField,
  position,
}: Props) {
  const titleID = `data-model-entity-title-${entity.id}`;
  const entityLabel = entity.name.trim() || "без названия";

  return <article
    aria-labelledby={titleID}
    className={`${graphView ? "absolute w-[320px]" : "w-full min-w-0"} rounded-xl border border-border bg-card shadow-sm`}
    style={graphView ? { left: position.x, top: position.y } : undefined}
  >
    <div
      role="group"
      aria-label={`Сущность ${entityLabel}${graphView ? ". Для точного размещения используйте кнопки перемещения" : ""}`}
      className={`flex select-none items-center justify-between gap-2 rounded-t-xl border-b border-border bg-muted/30 px-3 py-2 ${graphView ? "cursor-grab touch-none active:cursor-grabbing" : ""}`}
      onPointerDown={graphView ? (event) => onStartDrag?.(event, entity, position) : undefined}
    >
      <h3 id={titleID} className="truncate text-sm font-semibold">{entity.name || "Без названия"}</h3>
      {graphView && <div className="flex shrink-0 gap-1">
        <Button type="button" variant="ghost" size="icon" className="size-10" aria-label={`Сдвинуть сущность ${entityLabel} влево`} onPointerDown={(event) => event.stopPropagation()} onClick={() => onMoveEntity(entity.id, position.x - 30, position.y)}><ArrowLeft className="size-4" /></Button>
        <Button type="button" variant="ghost" size="icon" className="size-10" aria-label={`Сдвинуть сущность ${entityLabel} вверх`} onPointerDown={(event) => event.stopPropagation()} onClick={() => onMoveEntity(entity.id, position.x, position.y - 30)}><ArrowUp className="size-4" /></Button>
        <Button type="button" variant="ghost" size="icon" className="size-10" aria-label={`Сдвинуть сущность ${entityLabel} вниз`} onPointerDown={(event) => event.stopPropagation()} onClick={() => onMoveEntity(entity.id, position.x, position.y + 30)}><ArrowDown className="size-4" /></Button>
        <Button type="button" variant="ghost" size="icon" className="size-10" aria-label={`Сдвинуть сущность ${entityLabel} вправо`} onPointerDown={(event) => event.stopPropagation()} onClick={() => onMoveEntity(entity.id, position.x + 30, position.y)}><ArrowRight className="size-4" /></Button>
      </div>}
    </div>

    <div className="space-y-3 p-3">
      <label className="block space-y-1 text-xs text-muted-foreground">
        <span>Название сущности</span>
        <Input id={dataModelEntityNameControlID(entity.id)} className="min-h-11 text-sm text-foreground" value={entity.name} required aria-invalid={!validModelName(entity.name)} aria-describedby={`data-model-entity-name-help-${entity.id}`} onChange={(event) => onRenameEntity(entity.id, event.currentTarget.value)} />
        <span id={`data-model-entity-name-help-${entity.id}`} className={!validModelName(entity.name) ? "text-destructive" : "sr-only"}>{validModelName(entity.name) ? "" : "Укажите название длиной до 120 символов."}</span>
      </label>
      <label className="block space-y-1 text-xs text-muted-foreground">
        <span>Количество записей</span>
        <select
          className="min-h-11 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          value={entity.cardinality ?? "many"}
          aria-label={`Количество записей сущности ${entityLabel}`}
          onChange={(event) => onChangeEntityCardinality(entity, event.currentTarget.value as NonNullable<DataModelEntity["cardinality"]>)}
        >
          {entityCardinalities.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
        <span className="sr-only">Одна запись автоматически связывается с типизированными входами; выбор записей нужен только для списков.</span>
      </label>
      <p className="text-xs text-muted-foreground">{entity.fields.length} {entity.fields.length === 1 ? "поле" : "полей"}</p>

      <div className="max-h-80 space-y-2 overflow-auto pr-1">
        {entity.fields.map((field, fieldIndex) => <fieldset key={field.id} className="grid min-w-0 grid-cols-1 items-end gap-2 rounded-md border border-border p-2 sm:grid-cols-[minmax(0,1fr)_9rem_auto]">
          <legend className="sr-only">Поле {field.name} сущности {entity.name || "без названия"}</legend>
          <label className="min-w-0 space-y-1 text-xs text-muted-foreground">
            <span>Название поля</span>
            <Input id={dataModelFieldNameControlID(field.id)} className="min-h-10 text-sm text-foreground" value={field.name} required aria-invalid={!validModelName(field.name)} aria-describedby={`data-model-field-name-help-${field.id}`} onChange={(event) => onRenameField(entity.id, field.id, event.currentTarget.value)} />
            <span id={`data-model-field-name-help-${field.id}`} className={!validModelName(field.name) ? "text-destructive" : "sr-only"}>{validModelName(field.name) ? "" : "Укажите название длиной до 120 символов."}</span>
          </label>
          <label className="min-w-0 space-y-1 text-xs text-muted-foreground">
            <span>Тип поля</span>
            <select className="min-h-10 w-full rounded-md border border-input bg-background px-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" value={field.type} aria-label={`Тип поля ${field.name} сущности ${entityLabel}`} onChange={(event) => onChangeFieldType(entity, field, event.currentTarget.value as DataModelFieldType)}>
              {fieldTypes.map((type) => <option key={type.value} value={type.value}>{type.label}</option>)}
            </select>
          </label>
          <div className="flex items-center justify-end gap-1">
            <Button type="button" variant="ghost" size="icon" className="size-10" aria-label={`Переместить поле ${field.name} выше`} disabled={fieldIndex === 0} onClick={() => onMoveField(entity.id, field.id, -1)}><ArrowUp className="size-4" /></Button>
            <Button type="button" variant="ghost" size="icon" className="size-10" aria-label={`Переместить поле ${field.name} ниже`} disabled={fieldIndex === entity.fields.length - 1} onClick={() => onMoveField(entity.id, field.id, 1)}><ArrowDown className="size-4" /></Button>
            <Button type="button" variant="ghost" size="icon" className="size-10 text-destructive" aria-label={`Удалить поле ${field.name} из сущности ${entityLabel}`} onClick={() => onRemoveField(entity, field)}><Trash2 className="size-4" /></Button>
          </div>
          {field.type === "enum" && <label className="col-span-full space-y-1 text-xs text-muted-foreground">
            <span>Варианты (по одному на строку)</span>
            <textarea className="min-h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" aria-label={`Варианты поля ${field.name} сущности ${entityLabel}`} aria-invalid={!field.options?.length} aria-describedby={`data-model-enum-help-${field.id}`} value={(field.options ?? []).join("\n")} onChange={(event) => {
              const options = parseDataModelEnumOptions(event.currentTarget.value);
              onUpdateField(entity.id, field.id, (current) => ({ ...current, options }));
            }} />
            {!field.options?.length && <span id={`data-model-enum-help-${field.id}`} className="text-destructive">Добавьте хотя бы один вариант.</span>}
          </label>}
          <label className="col-span-full flex min-h-10 items-center gap-2 text-sm">
            <input className="size-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" type="checkbox" checked={Boolean(field.unique)} disabled={field.type === "longText" || field.type === "media" || field.type === "boolean"} onChange={(event) => {
              const unique = event.currentTarget.checked;
              onUpdateField(entity.id, field.id, (current) => ({ ...current, unique }));
            }} />
            <span>Значение уникально</span>
          </label>
        </fieldset>)}
        {entity.fields.length === 0 && <p className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">У сущности пока нет полей.</p>}
      </div>

      <div className="flex flex-wrap justify-between gap-2">
        <Button id={dataModelAddFieldControlID(entity.id)} type="button" variant="outline" className="min-h-10" aria-label={`Добавить поле в сущность ${entityLabel}`} onClick={() => onAddField(entity)}><Plus className="size-4" />Поле</Button>
        <Button type="button" variant="ghost" className="min-h-10 text-destructive" aria-label={`Удалить сущность ${entityLabel}`} onClick={() => onRemoveEntity(entity)}><Trash2 className="size-4" />Удалить</Button>
      </div>
    </div>
  </article>;
}
