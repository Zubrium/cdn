import type { ReactNode } from "react";
import { ArrowDown, ArrowUp, Copy, Plus, Trash2 } from "lucide-react";
import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { Button } from "@/components/ui/button";
import { assignArrayItemRecordID } from "@/domains/content-schema/record-identity";
import { starterValue } from "../blocks";
import type { ArrayFieldRendererProps } from "./array-field-types";
import { arrayItemKey, type ArrayMoveAction, cloneOriginal, entryFieldAnchorId, isRecordValue, pointerPart, useArrayRowFocusRestoration } from "./helpers";

export function ArrayTable({
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
  const rowFields = field.children.filter((child) => child.key.trim());
  const children = rowFields.filter((child) => !child.engineRecordID);
  const canAdd = field.maxItems === undefined || rows.length < field.maxItems;
  // The published schema enforces minItems server-side; going below it would
  // make autosave fail with schema_validation_failed.
  const minRows = field.minItems ?? 0;
  const canRemove = rows.length > minRows;
  const rowFocus = useArrayRowFocusRestoration(disabled, rows);

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
    copy.splice(index + 1, 0, assignArrayItemRecordID(cloneOriginal(rows[index] as JsonValue), rowFields, true));
    onChange(copy);
  };
  const addRow = () => {
    if (!canAdd || rowFields.length === 0) return;
    const row: JsonObject = {};
    rowFields.forEach((child) => {
      if (child.required || child.defaultValue) row[child.key] = starterValue(child);
    });
    onChange([...rows, assignArrayItemRecordID(row, rowFields, true)]);
  };

  const compactControlFor = (
    child: SchemaFieldNode,
    cellValue: JsonValue,
    setCell: (value: JsonValue) => void,
    cellPath: string,
    cellSchemaPath: string,
  ): ReactNode => {
    const error = errorsByPath.get(cellPath);
    const errorId = error ? `${entryFieldAnchorId(cellPath)}-error` : undefined;
    const rowIndex = Number(cellPath.slice(path.length + 1).split(".")[0]) + 1;
    const isMedia = child.type === "media" || child.type === "file";
    const control = (
      <RenderFieldValue
        field={child}
        value={cellValue}
        onChange={setCell}
        onPickMedia={onPickMedia}
        disabled={disabled}
        compact={!isMedia}
        errorsByPath={errorsByPath}
        path={cellPath}
        schemaPath={cellSchemaPath}
        controlId={entryFieldAnchorId(cellPath)}
        errorId={errorId}
        accessibleLabel={`${field.label || field.key}, ${child.label || child.key}, строка ${rowIndex}`}
        mediaLabel={`${field.label || field.key} · ${child.label || child.key}`}
      />
    );
    if (child.type === "object" || child.type === "array") return control;
    return (
      <div key={cellPath}>
        {control}
        {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
      </div>
    );
  };

  if (children.length === 0) {
    return (
      <p className="rounded-md border border-dashed border-border px-3 py-3 text-xs text-muted-foreground">
        В схеме массива нет полей для строк таблицы.
      </p>
    );
  }

  return (
    <div ref={rowFocus.rootRef} onFocusCapture={rowFocus.onFocusCapture} className="space-y-2">
      <div className="overflow-x-auto rounded-md border border-border">
        <table className="w-full min-w-[36rem] border-collapse text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40 text-left">
              {children.map((child) => (
                <th
                  key={child.id}
                  id={`${entryFieldAnchorId(path)}-column-${child.id}`}
                  className="px-2 py-2 text-xs font-medium text-muted-foreground"
                >
                  {child.label || child.key}
                  {child.required && <span className="ml-0.5 text-destructive">*</span>}
                </th>
              ))}
              <th className="w-24 px-2 py-2" aria-label="Действия" />
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) => {
              const record = isRecordValue(row) ? row : {};
              return (
                <tr
                  key={arrayItemKey(row, index, rowFields)}
                  data-array-item-index={`${path}:${index}`}
                  className={errorsByPath.has(`${path}.${index}`) ? "bg-destructive/5" : undefined}
                >
                  {children.map((child) => {
                    const cellPath = `${path}.${index}.${child.key}`;
                    const cell = compactControlFor(
                      child,
                      record[child.key],
                      (next) => setRow(index, { ...record, [child.key]: next }),
                      cellPath,
                      `${schemaPath}/items/${pointerPart(child.key)}`,
                    );
                    return (
                      <td
                        key={child.id}
                        headers={`${entryFieldAnchorId(path)}-column-${child.id}`}
                        className="align-top px-2 py-2"
                      >
                        {cell}
                        {!child.required && record[child.key] !== undefined && (
                          <Button type="button" variant="ghost" size="sm" disabled={disabled}
                            aria-label={`Очистить ${child.label || child.key}, строка ${index + 1}`}
                            onClick={() => { const next = { ...record }; delete next[child.key]; setRow(index, next); }}>
                            Очистить
                          </Button>
                        )}
                      </td>
                    );
                  })}
                  <td className="align-top px-2 py-2">
                    <div className="flex items-center justify-end gap-0.5">
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
                        aria-label={canRemove ? "Удалить" : `Минимум ${minRows} строк`}
                        className="text-destructive hover:text-destructive"
                        disabled={disabled || !canRemove}
                        onClick={() => removeRow(index)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </td>
                </tr>
              );
            })}
            {rows.length === 0 && (
              <tr>
                <td colSpan={children.length + 1} className="px-3 py-4 text-center text-xs text-muted-foreground">
                  Пока нет строк
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <div className="flex items-center justify-between gap-2">
        {!canAdd && (
          <p className="text-2xs text-muted-foreground">
            Достигнут максимум: {field.maxItems} элемент(ов)
          </p>
        )}
        {minRows > 0 && (
          <p className="text-2xs text-muted-foreground">
            Минимум строк по схеме: {minRows}
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
          Добавить строку
        </Button>
      </div>
    </div>
  );
}
