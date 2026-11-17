import type { JsonValue } from "@/types";
import type { FieldValueProps, FieldValueRenderer } from "./field-value-types";

export type ArrayFieldRendererProps = Omit<FieldValueProps, "value" | "onChange"> & {
  value: JsonValue[];
  onChange: (next: JsonValue[]) => void;
  renderFieldValue: FieldValueRenderer;
};
