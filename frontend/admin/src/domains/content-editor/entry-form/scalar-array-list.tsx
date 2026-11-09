import { ArrowDown, ArrowUp, Copy, Plus, Trash2 } from "lucide-react";
import type { JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { Button } from "@/components/ui/button";
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
      {value.map((item, index) => {
        const itemPath = `${path}.${index}`;
        const error = errorsByPath.get(itemPath);
        const errorId = error ? `${entryFieldAnchorId(itemPath)}-error` : undefined;
        if (field.itemType === "media" || field.itemType === "file") {
          return (
            <div key={index} className="flex items-start gap-2">
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
            </div>
          );
        }
        return (
          <div key={index} className="flex items-start gap-2">
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
          </div>
        );
      })}
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
