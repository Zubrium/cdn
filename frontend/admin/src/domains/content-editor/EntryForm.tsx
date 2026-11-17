import { useCallback, useMemo, useState } from "react";
import type { JsonValue, Media } from "@/types";
import { Button } from "@/components/ui/button";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { entryFieldAnchorId, isRecordValue, pointerPart } from "./entry-form/helpers";
import { ArrayEditorValue } from "./entry-form/array-fields";
import { ObjectFields } from "./entry-form/object-fields";
import { EntryFieldValue } from "./entry-form/field-value";
import { FieldCard, ScalarControl } from "./entry-form/field-controls";
import type { EntryFieldsFormProps, MediaPickTarget } from "./entry-form/types";

export type { EntryFieldsFormProps, EntryFormIssue, MediaPickTarget } from "./entry-form/types";
export {
  clearMediaArraySelection,
  entryFieldAnchorId,
  fieldLabelForPath,
  scrollToEntryField,
} from "./entry-form/helpers";
export { validateEntryFields } from "./entry-form/validation";

export function EntryFieldsForm({
  fields,
  metadata,
  onChange,
  onRemove,
  disabled,
  issues,
  schemaPathPrefix = "",
  anchorPathPrefix = "",
  compact = false,
  mediaPreparationsByFieldId,
  mediaAcceptByFieldId,
}: EntryFieldsFormProps) {
  const [mediaTarget, setMediaTarget] = useState<MediaPickTarget | null>(null);
  const openMediaPick = useCallback(
    (target: MediaPickTarget) => setMediaTarget(target),
    [],
  );

  const errorsByPath = useMemo(() => {
    const map = new Map<string, string>();
    issues.forEach((issue) => {
      if (!map.has(issue.path)) map.set(issue.path, issue.message);
    });
    return map;
  }, [issues]);

  if (fields.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted-foreground">
        В схеме нет полей. Данные для этой записи задаются иначе.
      </p>
    );
  }

  return (
    <div className="space-y-0">
      {fields.map((field) => {
        const fieldPath = [anchorPathPrefix, field.key].filter(Boolean).join(".");
        const schemaPath = `${schemaPathPrefix.replace(/\/$/, "")}/${pointerPart(field.key)}`;
        const anchorId = entryFieldAnchorId(fieldPath);
        const error = errorsByPath.get(fieldPath);
        const errorId = error ? `${anchorId}-error` : undefined;
        const value = metadata[field.key];
        return (
          <FieldCard
            key={field.id}
            field={field}
            anchorId={anchorId}
            error={error}
            compact={compact}
            action={onRemove && !field.required && value !== undefined ? (
              <Button type="button" variant="ghost" size="sm" disabled={disabled}
                aria-label={`Очистить ${field.label || field.key}`} onClick={() => onRemove(field.key)}>
                Очистить
              </Button>
            ) : undefined}

          >
            {field.type === "object" ? (
              <ObjectFields
                field={field}
                value={isRecordValue(value) ? value : {}}
                onChange={(next) => onChange(field.key, next)}
                onPickMedia={openMediaPick}
                disabled={disabled}
                errorsByPath={errorsByPath}
                path={fieldPath}
                schemaPath={schemaPath}
                renderFieldValue={EntryFieldValue}
              />
            ) : field.type === "array" ? (
              <ArrayEditorValue
                field={field}
                value={Array.isArray(value) ? value : []}
                onChange={(next) => onChange(field.key, next)}
                onPickMedia={openMediaPick}
                disabled={disabled}
                errorsByPath={errorsByPath}
                path={fieldPath}
                schemaPath={schemaPath}
                renderFieldValue={EntryFieldValue}
              />
            ) : (
              <ScalarControl
                field={field}
                preparations={mediaPreparationsByFieldId?.[field.id]}
                acceptOverride={mediaAcceptByFieldId?.[field.id]}
                value={value as JsonValue}
                disabled={disabled}
                compact={compact}
                onChange={(next) => onChange(field.key, next)}
                onPickMedia={openMediaPick}
                fieldPath={schemaPath}
                controlId={`${anchorId}-control`}
                labelId={`${anchorId}-label`}
                errorId={errorId}
              />
            )}
          </FieldCard>
        );
      })}

      <MediaPickerDialog
        open={mediaTarget !== null}
        onClose={() => setMediaTarget(null)}
        onSelect={(media: Media[]) => {
          const target = mediaTarget;
          setMediaTarget(null);
          if (!target) return;
          const apply = target.single ? media.slice(0, 1) : media;
          target.apply(apply);
        }}
        mode={mediaTarget?.single === false ? "multiple" : "single"}
        accept={mediaTarget?.accept ?? "image"}
        preparations={mediaTarget?.preparations}
        title={mediaTarget ? `Выбрать: ${mediaTarget.label}` : "Выбрать файл"}
      />


    </div>
  );
}
