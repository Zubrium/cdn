import { useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import type { DataModel, DataModelEntity, DataModelRelation } from "@/types";
import { DataModelEntityCard, type DataModelEntityCardActions, type DataModelPosition } from "./DataModelEntityCard";
import { cardinalities, finalDataModelDragPosition, positionFor } from "./model-operations";

interface DragState extends DataModelPosition {
  id: string;
  pointerId: number;
  x: number;
  y: number;
  originX: number;
  originY: number;
}

interface PreviewPosition extends DataModelPosition {
  id: string;
}

interface Props extends DataModelEntityCardActions {
  model: DataModel;
}

export function DataModelGraphCanvas({ model, ...cardActions }: Props) {
  const [dragging, setDragging] = useState<DragState | null>(null);
  const [dragPreview, setDragPreview] = useState<PreviewPosition | null>(null);
  const draggingPointerID = useRef<number | null>(null);
  const positioned = model.entities.map((entity, index) => ({ entity, position: positionFor(model, entity, index) }));
  const canvasWidth = Math.max(920, ...positioned.map(({ position }) => position.x + 340));
  const canvasHeight = Math.max(620, ...positioned.map(({ position }) => position.y + 300));

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>, entity: DataModelEntity, position: DataModelPosition) => {
    if (event.button !== 0) return;
    draggingPointerID.current = event.pointerId;
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging({ id: entity.id, pointerId: event.pointerId, x: event.clientX, y: event.clientY, originX: position.x, originY: position.y });
  };

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragging || dragging.pointerId !== event.pointerId) return;
    setDragPreview({
      id: dragging.id,
      x: Math.max(0, dragging.originX + event.clientX - dragging.x),
      y: Math.max(0, dragging.originY + event.clientY - dragging.y),
    });
  };

  const finishDrag = (event: ReactPointerEvent<HTMLDivElement>, cancelled = false) => {
    if (draggingPointerID.current !== event.pointerId) return;
    const position = finalDataModelDragPosition(dragging, dragPreview, event.pointerId, cancelled);
    if (position) cardActions.onMoveEntity(position.id, position.x, position.y);
    draggingPointerID.current = null;
    setDragging(null);
    setDragPreview(null);
  };

  const renderRelation = (relation: DataModelRelation) => {
    const fromIndex = model.entities.findIndex((entity) => entity.id === relation.fromEntityId);
    const toIndex = model.entities.findIndex((entity) => entity.id === relation.toEntityId);
    if (fromIndex < 0 || toIndex < 0) return null;
    const fromEntity = model.entities[fromIndex]!;
    const toEntity = model.entities[toIndex]!;
    const from = dragPreview?.id === relation.fromEntityId ? dragPreview : positionFor(model, fromEntity, fromIndex);
    const to = dragPreview?.id === relation.toEntityId ? dragPreview : positionFor(model, toEntity, toIndex);
    const startX = from.x + 310;
    const startY = from.y + 72;
    const endX = to.x + 10;
    const endY = to.y + 72;
    const midX = Math.round((startX + endX) / 2);
    return <g key={relation.id}>
      <path d={`M ${startX} ${startY} C ${midX} ${startY}, ${midX} ${endY}, ${endX} ${endY}`} fill="none" stroke="hsl(var(--primary))" strokeWidth="2" markerEnd="url(#data-model-arrow)" />
      <text x={midX} y={Math.min(startY, endY) - 8} textAnchor="middle" className="fill-foreground text-xs">{relation.name} · {cardinalities.find((item) => item.value === relation.cardinality)?.label}</text>
    </g>;
  };

  return <section aria-label="Граф связей модели данных" className="overflow-auto rounded-xl border border-border bg-muted/10">
    <div role="group" aria-label="Схема связей. Для перемещения используйте кнопки на карточках" className="relative" style={{ width: canvasWidth, height: canvasHeight }} onPointerMove={onPointerMove} onPointerUp={(event) => finishDrag(event)} onPointerCancel={(event) => finishDrag(event, true)} onLostPointerCapture={(event) => finishDrag(event, true)}>
      <svg aria-hidden="true" className="pointer-events-none absolute inset-0 size-full overflow-visible">
        <defs><marker id="data-model-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" className="fill-primary" /></marker></defs>
        {(model.relations ?? []).map(renderRelation)}
      </svg>
      {positioned.map(({ entity, position }) => {
        const shown = dragPreview?.id === entity.id ? dragPreview : position;
        return <DataModelEntityCard key={entity.id} {...cardActions} entity={entity} position={shown} graphView onStartDrag={onPointerDown} />;
      })}
    </div>
  </section>;
}
