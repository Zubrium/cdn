import type { JsonObject } from "@/types";

export type BuilderFieldType =
  | "string"
  | "text"
  | "richText"
  | "number"
  | "integer"
  | "boolean"
  | "date"
  | "dateTime"
  | "enum"
  | "object"
  | "array"
  | "media"
  | "file"
  | "blocks";

export type ArrayItemType =
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "enum"
  | "object"
  | "media"
  | "file";

export type BlockTypeName =
  | "paragraph"
  | "heading"
  | "bulletedList"
  | "numberedList"
  | "quote"
  | "code"
  | "divider"
  | "image"
  | "gallery"
  | "video";

export interface EnumOption {
  id: string;
  value: string;
  label: string;
}

export interface SchemaFieldNode {
  id: string;
  key: string;
  label: string;
  type: BuilderFieldType;
  description: string;
  required: boolean;
  nullable: boolean;
  isPrivate: boolean;
  /** Field-level x-media processor profile from the immutable feature manifest. */
  mediaProfile?: JsonObject;
  /** x-media profile of array items, when the array directly contains media IDs. */
  itemMediaProfile?: JsonObject;
  placeholder: string;
  defaultValue: string;
  /** JSON Schema `format` for string fields (`email`, `uri`, `uuid`, `color`, …). */
  stringFormat?: string;
  pattern?: string;
  minLength?: number;
  maxLength?: number;
  min?: number;
  max?: number;
  step?: number;
  enumValues: EnumOption[];
  children: SchemaFieldNode[];
  itemType: ArrayItemType;
  minItems?: number;
  maxItems?: number;
  allowedBlocks: BlockTypeName[];
}

export type IssueSeverity = "error" | "warning";

export interface BuilderIssue {
  fieldId?: string;
  path: string;
  message: string;
  severity: IssueSeverity;
}

export interface ParsedSchema {
  fields: SchemaFieldNode[];
  warnings: string[];
}

export interface FieldTypeMeta {
  value: BuilderFieldType;
  label: string;
  group: "Базовые" | "Текст" | "Структуры" | "Медиа";
  hint: string;
}
