import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DraggableAttributes,
  type DraggableSyntheticListeners,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface SortableDragHandleProps {
  attributes: DraggableAttributes;
  listeners: DraggableSyntheticListeners;
  setActivatorNodeRef: (element: HTMLElement | null) => void;
}

export function SortableList({
  items,
  onReorder,
  getItemLabel,
  children,
}: {
  items: string[];
  onReorder: (items: string[]) => void;
  getItemLabel?: (id: string) => string;
  children: ReactNode;
}) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 180, tolerance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const label = (id: string) => getItemLabel?.(id) || "элемент";

  return <DndContext
    sensors={sensors}
    collisionDetection={closestCenter}
    accessibility={{
      screenReaderInstructions: {
        draggable: "Нажмите пробел, чтобы взять элемент. Стрелки перемещают элемент, пробел завершает, Escape отменяет.",
      },
      announcements: {
        onDragStart: ({ active }) => `Захвачено: ${label(String(active.id))}.`,
        onDragOver: ({ active, over }) => over
          ? `${label(String(active.id))}: текущая позиция рядом с «${label(String(over.id))}».`
          : `${label(String(active.id))}: вне допустимой области.`,
        onDragEnd: ({ active, over }) => over
          ? `${label(String(active.id))} перемещён на позицию «${label(String(over.id))}».`
          : `${label(String(active.id))}: перемещение отменено.`,
        onDragCancel: ({ active }) => `${label(String(active.id))}: перемещение отменено.`,
      },
    }}
    onDragEnd={({ active, over }) => {
      if (!over || active.id === over.id) return;
      const from = items.indexOf(String(active.id));
      const to = items.indexOf(String(over.id));
      if (from < 0 || to < 0) return;
      onReorder(arrayMove(items, from, to));
    }}
  >
    <SortableContext items={items} strategy={verticalListSortingStrategy}>
      {children}
    </SortableContext>
  </DndContext>;
}

export function SortableItem({
  id,
  disabled = false,
  className,
  children,
}: {
  id: string;
  disabled?: boolean;
  className?: string;
  children: (handle: SortableDragHandleProps) => ReactNode;
}) {
  const {
    attributes,
    isDragging,
    listeners,
    setActivatorNodeRef,
    setNodeRef,
    transform,
    transition,
  } = useSortable({ id, disabled });

  return <li
    ref={setNodeRef}
    style={{
      transform: CSS.Transform.toString(transform),
      transition,
      position: "relative",
      zIndex: isDragging ? 1 : undefined,
    }}
    className={cn("min-w-0 bg-background", isDragging && "opacity-70 shadow-lg", className)}
  >
    {children({ attributes, listeners, setActivatorNodeRef })}
  </li>;
}
