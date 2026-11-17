import type { FieldValueProps } from "./field-value-types";
import { ArrayEditorValue } from "./array-fields";
import { ObjectFields } from "./object-fields";
import { ScalarControl } from "./field-controls";
import { isRecordValue } from "./helpers";

export function EntryFieldValue({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  compact,
  errorsByPath,
  path,
  schemaPath,
  controlId,
  labelId,
  errorId,
  accessibleLabel,
  mediaLabel,
}: FieldValueProps) {
  if (field.type === "object") {
    return (
      <ObjectFields
        field={field}
        value={isRecordValue(value) ? value : {}}
        onChange={onChange}
        onPickMedia={onPickMedia}
        disabled={disabled}
        errorsByPath={errorsByPath}
        path={path}
        schemaPath={schemaPath}
        renderFieldValue={EntryFieldValue}
      />
    );
  }
  if (field.type === "array") {
    return (
      <ArrayEditorValue
        field={field}
        value={Array.isArray(value) ? value : []}
        onChange={onChange}
        onPickMedia={onPickMedia}
        disabled={disabled}
        errorsByPath={errorsByPath}
        path={path}
        schemaPath={schemaPath}
        renderFieldValue={EntryFieldValue}
      />
    );
  }
  return (
    <ScalarControl
      field={field}
      fieldPath={schemaPath}
      value={value}
      onChange={onChange}
      onPickMedia={onPickMedia}
      disabled={disabled}
      compact={compact}
      controlId={controlId}
      labelId={labelId}
      errorId={errorId}
      accessibleLabel={accessibleLabel}
      mediaLabel={mediaLabel}
    />
  );
}
