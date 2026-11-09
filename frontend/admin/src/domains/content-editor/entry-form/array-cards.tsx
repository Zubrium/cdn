import { ArrowDown, ArrowUp, Copy, Plus, Trash2 } from "lucide-react";
import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { assignArrayItemRecordID } from "@/domains/content-schema/record-identity";
import { starterValue } from "../blocks";
import type { ArrayFieldRendererProps } from "./array-field-types";
import { type ArrayMoveAction, arrayItemKey, cloneOriginal, entryFieldAnchorId, isRecordValue, useArrayRowFocusRestoration } from "./helpers";

/** Card layout for object arrays that do not fit the compact table. */
export function ArrayCards({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
  renderFieldValue: RenderFieldValue,
}: ArrayFieldRendererProps) {
  const rows = value;
  const canAdd = field.maxItems === undefined || rows.length < field.maxItems;
  const minRows = field.minItems ?? 0;
  const canRemove = rows.length > minRows;
  const rowFocus = useArrayRowFocusRestoration(disabled, rows);

  const rowField: SchemaFieldNode = {
    ...field,
    id: `${field.id}-row`,
    key: "row",
    label: field.label,
    type: "object",
    children: field.children.filter((child) => child.key.trim()),
    description: "",
    required: false,
  };

  const setRow = (index: number, next: JsonObject) => {
    const copy = [...rows];
    copy[index] = next;
    onChange(copy);
  };
  const moveRow = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= rows.length) return;
    const preferredAction: ArrayMoveAction = delta > 0 ? "Ниже" : "Выше";
    const canContinueInSameDirection = target + delta >= 0 && target + delta < rows.length;
    rowFocus.rememberRowFocus(
      `${path}:${target}`,
      canContinueInSameDirection ? preferredAction : preferredAction === "Ниже" ? "Выше" : "Ниже",
    );
    const copy = [...rows];
    [copy[index], copy[target]] = [copy[target], copy[index]];
    onChange(copy);
  };
  const removeRow = (index: number) => {
    onChange(rows.filter((_, i) => i !== index));
  };
  const duplicateRow = (index: number) => {
    if (!canAdd) return;
    const copy = [...rows];
    copy.splice(index + 1, 0, assignArrayItemRecordID(cloneOriginal(rows[index] as JsonValue), rowField.children, true));
    onChange(copy);
  };
  const addRow = () => {
    if (!canAdd) return;
    const row: JsonObject = {};
    rowField.children.forEach((child) => {
      if (child.required || child.defaultValue) row[child.key] = starterValue(child);
    });
    onChange([...rows, assignArrayItemRecordID(row, rowField.children, true)]);
  };

  const rowTitle = (row: JsonValue, index: number): string => {
    const record = isRecordValue(row) ? row as JsonObject : {};
    for (const child of rowField.children) {
      if (child.engineRecordID || (child.type !== "string" && child.type !== "text")) continue;
      const textValue = record[child.key];
      if (typeof textValue === "string" && textValue.trim()) return textValue.trim();
    }
    return `Элемент ${index + 1}`;
  };

  return (
    <div ref={rowFocus.rootRef} onFocusCapture={rowFocus.onFocusCapture} className="space-y-2">
      {rows.map((row, index) => {
        const rowPath = `${path}.${index}`;
        const rowError = errorsByPath.has(rowPath);
        const headingId = `${entryFieldAnchorId(rowPath)}-heading`;
        const itemKey = arrayItemKey(row, index, rowField.children);
        return (
          <section
            key={itemKey}
            data-array-item-index={`${path}:${index}`}
            aria-labelledby={headingId}
            className={cn(
              "space-y-3 rounded-lg border p-3",
              rowError ? "border-destructive/40 bg-destructive/5" : "border-border bg-muted/20",
            )}
          >
            <header className="flex items-center gap-2">
              <h4 id={headingId} className="min-w-0 flex-1 truncate text-sm font-medium">
                {rowTitle(row, index)}
              </h4>
              {rowError && <Badge variant="destructive">Ошибки</Badge>}
              <div className="flex shrink-0 items-center gap-0.5">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Выше"
                  disabled={disabled || index === 0}
                  onClick={() => moveRow(index, -1)}
                >
                  <ArrowUp className="h-3.5 w-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Ниже"
                  disabled={disabled || index === rows.length - 1}
                  onClick={() => moveRow(index, 1)}
                >
                  <ArrowDown className="h-3.5 w-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Дублировать"
                  disabled={disabled || !canAdd}
                  onClick={() => duplicateRow(index)}
                >
                  <Copy className="h-3.5 w-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={canRemove ? "Удалить" : `Минимум ${minRows} элементов`}
                  className="text-destructive hover:text-destructive"
                  disabled={disabled || !canRemove}
                  onClick={() => removeRow(index)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </header>
            <RenderFieldValue
              field={rowField}
              value={isRecordValue(row) ? row as JsonObject : {}}
              onChange={(next) => setRow(index, next as JsonObject)}
              onPickMedia={onPickMedia}
              disabled={disabled}
              compact={false}
              errorsByPath={errorsByPath}
              path={rowPath}
              schemaPath={`${schemaPath}/items`}
            />
          </section>
        );
      })}
      {rows.length === 0 && (
        <p className="rounded-md border border-dashed border-border px-3 py-3 text-center text-xs text-muted-foreground">
          Пока нет элементов
        </p>
      )}
      <div className="flex items-center justify-between gap-2">
        {!canAdd && (
          <p className="text-2xs text-muted-foreground">
            Достигнут максимум: {field.maxItems} элемент(ов)
          </p>
        )}
        {minRows > 0 && (
          <p className="text-2xs text-muted-foreground">
            Минимум элементов по схеме: {minRows}
          </p>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addRow}
          disabled={disabled || !canAdd}
        >
          <Plus className="h-4 w-4" />
          Добавить элемент
        </Button>
      </div>
    </div>
  );
}
