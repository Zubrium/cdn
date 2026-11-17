import type { ComponentType } from "react";
import type { JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import type { MediaPickTarget } from "./types";

export interface FieldValueProps {
  field: SchemaFieldNode;
  value: JsonValue;
  onChange: (value: JsonValue) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  compact?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
  controlId?: string;
  labelId?: string;
  errorId?: string;
  accessibleLabel?: string;
  mediaLabel?: string;
}

export type FieldValueRenderer = ComponentType<FieldValueProps>;
