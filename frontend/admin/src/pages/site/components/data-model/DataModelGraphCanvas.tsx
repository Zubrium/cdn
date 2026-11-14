import { useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { DataModel, DataModelEntity, DataModelRelation, DataModelField } from "@/types";
import { cardinalities, positionFor } from "./model-operations";

interface Position {
  x: number;
  y: number;
}

interface DragState {
  id: string;
  pointerId: number;
  clientX: number;
  clientY: number;
  originX: number;
  originY: number;
}

interface PreviewPosition extends Position {
  id: string;
}

export function DataModelGraphCanvas({
  model,
  selectedEntityId,
  selectedRelationId,
  onSelectEntity,
  onSelectRelation,
  onMoveEntity,
}: {
  model: DataModel;
  selectedEntityId?: string;
  selectedRelationId?: string;
  onSelectEntity: (entityId: string) => void;
  onSelectRelation: (relationId: string) => void;
  onMoveEntity: (entityId: string, x: number, y: number) => void;
}) {
  const [dragging, setDragging] = useState<DragState | null>(null);
  const [preview, setPreview] = useState<PreviewPosition | null>(null);
  const draggingPointer = useRef<number | null>(null);
  const positioned = model.entities.map((entity, index) => ({ entity, position: positionFor(model, entity, index) }));
  const canvasWidth = Math.max(940, ...positioned.map(({ position }) => position.x + 320));
  const canvasHeight = Math.max(620, ...positioned.map(({ position }) => position.y + 250));

  const beginDrag = (event: ReactPointerEvent<HTMLDivElement>, entity: DataModelEntity, position: Position) => {
    if (event.button !== 0) return;
    onSelectEntity(entity.id);
    draggingPointer.current = event.pointerId;
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging({
      id: entity.id,
      pointerId: event.pointerId,
      clientX: event.clientX,
      clientY: event.clientY,
      originX: position.x,
      originY: position.y,
    });
  };
  const move = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragging || dragging.pointerId !== event.pointerId) return;
    setPreview({
      id: dragging.id,
      x: Math.max(0, dragging.originX + event.clientX - dragging.clientX),
      y: Math.max(0, dragging.originY + event.clientY - dragging.clientY),
    });
  };
  const finishDrag = (event: ReactPointerEvent<HTMLDivElement>, cancelled = false) => {
    if (draggingPointer.current !== event.pointerId) return;
    if (dragging && preview && !cancelled) onMoveEntity(preview.id, preview.x, preview.y);
    draggingPointer.current = null;
    setDragging(null);
    setPreview(null);
  };

  const relationGeometry = (relation: DataModelRelation) => {
    const fromIndex = model.entities.findIndex((entity) => entity.id === relation.fromEntityId);
    const toIndex = model.entities.findIndex((entity) => entity.id === relation.toEntityId);
    if (fromIndex < 0 || toIndex < 0) return null;
    const fromEntity = model.entities[fromIndex]!;
    const toEntity = model.entities[toIndex]!;
    const from = preview?.id === relation.fromEntityId ? preview : positionFor(model, fromEntity, fromIndex);
    const to = preview?.id === relation.toEntityId ? preview : positionFor(model, toEntity, toIndex);
    const startX = from.x + 280;
    const startY = from.y + 72;
    const endX = to.x;
    const endY = to.y + 72;
    const midX = Math.round((startX + endX) / 2);
    return {
      path: `M ${startX} ${startY} C ${midX} ${startY}, ${midX} ${endY}, ${endX} ${endY}`,
      labelX: midX,
      labelY: Math.min(startY, endY) - 18,
    };
  };

  return <section aria-label="ER-канвас модели данных" className="h-full min-h-[32rem] overflow-auto rounded-xl border border-border bg-muted/10">
    <div
      role="group"
      aria-label="Схема сущностей и связей"
      className="relative"
      style={{ width: canvasWidth, height: canvasHeight }}
      onPointerMove={move}
      onPointerUp={(event) => finishDrag(event)}
      onPointerCancel={(event) => finishDrag(event, true)}
      onLostPointerCapture={(event) => finishDrag(event, true)}
    >
      <svg aria-hidden="true" className="pointer-events-none absolute inset-0 size-full overflow-visible">
        <defs>
          <marker id="data-model-arrow" markerWidth="9" markerHeight="9" refX="8" refY="4.5" orient="auto">
            <path d="M0 0 L9 4.5 L0 9 z" className="fill-primary" />
          </marker>
        </defs>
        {(model.relations ?? []).map((relation) => {
          const geometry = relationGeometry(relation);
          if (!geometry) return null;
          return <path key={relation.id} d={geometry.path} fill="none" stroke="hsl(var(--primary) / .62)" strokeWidth={selectedRelationId === relation.id ? 3 : 2} markerEnd="url(#data-model-arrow)" />;
        })}
      </svg>

      {(model.relations ?? []).map((relation) => {
        const geometry = relationGeometry(relation);
        if (!geometry) return null;
        return <button
          key={relation.id}
          type="button"
          className={`absolute z-10 max-w-52 -translate-x-1/2 rounded-md border bg-card px-2 py-1 text-xs shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selectedRelationId === relation.id ? "border-primary text-foreground" : "border-border text-muted-foreground hover:border-primary/60"}`}
          style={{ left: geometry.labelX, top: geometry.labelY }}
          onClick={() => onSelectRelation(relation.id)}
        >
          <span className="block truncate font-medium">{relation.name}</span>
          <span className="block truncate">{cardinalities.find((item) => item.value === relation.cardinality)?.label}</span>
        </button>;
      })}

      {positioned.map(({ entity, position }) => {
        const shown = preview?.id === entity.id ? preview : position;
        const selected = selectedEntityId === entity.id;
        return <div
          key={entity.id}
          role="group"
          aria-label={`Сущность ${entity.name || "без названия"}`}
          className={`absolute w-[17.5rem] touch-none select-none overflow-hidden rounded-xl border bg-card shadow-sm ${selected ? "border-primary ring-2 ring-primary/20" : "border-border"}`}
          style={{ left: shown.x, top: shown.y }}
          onPointerDown={(event) => beginDrag(event, entity, position)}
        >
          <button type="button" className="flex w-full cursor-grab items-center justify-between gap-3 border-b border-border bg-muted/30 px-3 py-3 text-left active:cursor-grabbing focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => onSelectEntity(entity.id)}>
            <span className="min-w-0">
              <span className="block truncate text-sm font-semibold">{entity.name || "Без названия"}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">{entity.cardinality === "singleton" ? "Одна запись" : "Коллекция записей"}</span>
            </span>
            <span className="rounded bg-background px-2 py-1 text-xs tabular-nums text-muted-foreground">{entity.fields.length} пол.</span>
          </button>
          <div className="space-y-1 px-3 py-3">
            {entity.fields.slice(0, 5).map((field) => <div key={field.id} className="flex items-center justify-between gap-2 text-xs">
              <span className="truncate">{field.name || "Поле без названия"}</span>
              <span className="shrink-0 text-muted-foreground">{fieldTypeLabel(field)}</span>
            </div>)}
            {entity.fields.length === 0 && <p className="text-xs text-muted-foreground">Полей пока нет</p>}
            {entity.fields.length > 5 && <p className="text-xs text-muted-foreground">Ещё {entity.fields.length - 5}</p>}
          </div>
        </div>;
      })}
    </div>
  </section>;
}

function fieldTypeLabel(field: DataModelField): string {
  const labels: Record<DataModelField["type"], string> = {
    text: "Текст",
    longText: "Длинный текст",
    integer: "Целое",
    number: "Число",
    boolean: "Да/нет",
    date: "Дата",
    dateTime: "Дата и время",
    url: "URL",
    email: "Email",
    enum: "Варианты",
    media: "Файл",
  };
  return labels[field.type];
}
