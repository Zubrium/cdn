import type { ArrayFieldRendererProps } from "./array-field-types";
import { arrayUsesCards } from "./helpers";
import { ArrayCards } from "./array-cards";
import { ArrayTable } from "./array-table";
import { ScalarArrayList } from "./scalar-array-list";

export function ArrayEditorValue({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
  renderFieldValue,
}: ArrayFieldRendererProps) {
  const shared = {
    field,
    value,
    onChange,
    onPickMedia,
    disabled,
    errorsByPath,
    path,
    schemaPath,
  };
  if (field.itemType === "object") {
    const Renderer = arrayUsesCards(field) ? ArrayCards : ArrayTable;
    return <Renderer {...shared} renderFieldValue={renderFieldValue} />;
  }
  return <ScalarArrayList {...shared} />;
}
