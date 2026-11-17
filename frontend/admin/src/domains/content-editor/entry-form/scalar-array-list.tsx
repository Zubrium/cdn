import { ArrowDown, ArrowUp, Copy, GripVertical, Plus, Trash2 } from "lucide-react";
import type { JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { Button } from "@/components/ui/button";
import { SortableItem, SortableList } from "@/components/ui/sortable-list";
import type { MediaPickTarget } from "./types";
import { clearMediaArraySelection, cloneOriginal, entryFieldAnchorId, mediaAccept } from "./helpers";
import { MediaValue, ScalarControl } from "./field-controls";
import { starterValue } from "../blocks";

export function ScalarArrayList({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
}: {
  field: SchemaFieldNode;
  value: JsonValue[];
  onChange: (next: JsonValue[]) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
}) {
  const canAdd = field.maxItems === undefined || value.length < field.maxItems;
  const minItemsAllowed = field.minItems ?? 0;
  const canRemove = value.length > minItemsAllowed;
  const duplicateItem = (index: number) => {
    if (!canAdd) return;
    const copy = [...value];
    copy.splice(index + 1, 0, cloneOriginal(value[index]));
    onChange(copy);
  };
  const itemIDs = value.map((_, index) => `index:${index}`);
  const reorderItems = (orderedIDs: string[]) => {
    const byID = new Map(itemIDs.map((id, index) => [id, value[index]!]));
    const next = orderedIDs.flatMap((id) => {
      const item = byID.get(id);
      return item === undefined ? [] : [item];
    });
    if (next.length === value.length) onChange(next);
  };
  const itemField: SchemaFieldNode = {
    ...field,
    id: `${field.id}-item`,
    key: "item",
    label: field.label,
    type: field.itemType as SchemaFieldNode["type"],
    children: [],
    required: false,
    description: "",
  };
  return (
    <div className="space-y-2">
      <SortableList
        items={itemIDs}
        onReorder={reorderItems}
        getItemLabel={(id) => {
          const index = itemIDs.indexOf(id);
          return index >= 0 ? `${field.label || field.key}, элемент ${index + 1}` : "элемент";
        }}
      >
        <ol className="space-y-2" aria-label={`Порядок элементов ${field.label || field.key}`}>
          {value.map((item, index) => {
            const itemPath = `${path}.${index}`;
            const error = errorsByPath.get(itemPath);
            const errorId = error ? `${entryFieldAnchorId(itemPath)}-error` : undefined;
            const itemKey = itemIDs[index]!;
            if (field.itemType === "media" || field.itemType === "file") {
              return <SortableItem key={itemKey} id={itemKey} disabled={disabled}>{(handle) => <div className="flex items-start gap-1">
                <Button
                  ref={handle.setActivatorNodeRef}
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="mt-1 touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
                  aria-label={`Перетащить ${field.label || field.key}, элемент ${index + 1}`}
                  disabled={disabled}
                  {...handle.attributes}
                  {...handle.listeners}
                ><GripVertical className="h-4 w-4" /></Button>
                <div className="min-w-0 flex-1">
                <MediaValue
                  mediaId={typeof item === "string" ? item : ""}
                  variant={field.itemType === "file" ? "file" : "image"}
                  disabled={disabled}
                  compact
                  onPick={() =>
                    onPickMedia({
                      label: `${field.label || field.key} (${index + 1})`,
                      single: true,
                      accept: field.itemType === "file" ? "all" : mediaAccept(field.itemMediaProfile),
                      fieldPath: `${schemaPath}/items`,
                      apply: (media) => {
                        const first = media[0];
                        if (first) {
                          const copy = [...value];
                          copy[index] = first.id;
                          onChange(copy);
                        }
                      },
                    })
                  }
                  onClear={() => onChange(clearMediaArraySelection(value, index))}
                />
                {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="mt-1 text-destructive hover:text-destructive"
                  aria-label="Удалить"
                  disabled={disabled || !canRemove}
                  onClick={() => onChange(value.filter((_, i) => i !== index))}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>}</SortableItem>;
            }
            return <SortableItem key={itemKey} id={itemKey} disabled={disabled}>{(handle) => <div className="flex items-start gap-1">
              <Button
                ref={handle.setActivatorNodeRef}
                type="button"
                variant="ghost"
                size="icon-sm"
                className="mt-1 touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
                aria-label={`Перетащить ${field.label || field.key}, элемент ${index + 1}`}
                disabled={disabled}
                {...handle.attributes}
                {...handle.listeners}
              ><GripVertical className="h-4 w-4" /></Button>
              <div className="min-w-0 flex-1">
              <ScalarControl
                field={itemField}
                value={item}
                disabled={disabled}
                compact={field.itemType === "boolean"}
                fieldPath={`${schemaPath}/items`}
                onChange={(next) => {
                  const copy = [...value];
                  copy[index] = next;
                  onChange(copy);
                }}
                onPickMedia={onPickMedia}
                controlId={entryFieldAnchorId(itemPath)}
                errorId={errorId}
                accessibleLabel={`${field.label || field.key}, элемент ${index + 1}`}
              />
              {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
              </div>
              <div className="flex items-center gap-0.5">
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Выше"
                disabled={disabled || index === 0}
                onClick={() => {
                  const copy = [...value];
                  [copy[index - 1], copy[index]] = [copy[index], copy[index - 1]];
                  onChange(copy);
                }}
              >
                <ArrowUp className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Ниже"
                disabled={disabled || index === value.length - 1}
                onClick={() => {
                  const copy = [...value];
                  [copy[index + 1], copy[index]] = [copy[index], copy[index + 1]];
                  onChange(copy);
                }}
              >
                <ArrowDown className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Дублировать"
                disabled={disabled || !canAdd}
                onClick={() => duplicateItem(index)}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="text-destructive hover:text-destructive"
                aria-label="Удалить"
                disabled={disabled || !canRemove}
                onClick={() => onChange(value.filter((_, i) => i !== index))}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
              </div>
            </div>}</SortableItem>;
          })}
        </ol>
      </SortableList>
      <div className="flex items-center justify-between gap-2">
        {!canAdd && (
          <p className="text-2xs text-muted-foreground">
            Достигнут максимум: {field.maxItems} элемент(ов)
          </p>
        )}
        {minItemsAllowed > 0 && (
          <p className="text-2xs text-muted-foreground">
            Минимум элементов по схеме: {minItemsAllowed}
          </p>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled || !canAdd}
          onClick={() => {
            const copy = [...value, starterValue(itemField)];
            onChange(copy);
          }}
        >
          <Plus className="h-4 w-4" />
          Добавить элемент
        </Button>
      </div>
    </div>
  );
}
