import type { FormFieldDefinition } from "@/types";

export type FormFieldType = FormFieldDefinition["type"];

const activeFormContentTypes = new Set([
  "application/ecmascript", "application/javascript", "application/x-httpd-php", "application/xhtml+xml",
  "application/xml", "image/svg+xml", "text/css", "text/html", "text/javascript", "text/xml",
]);

export function isActiveFormContentType(value: string): boolean {
  return activeFormContentTypes.has(value.toLowerCase());
}

export function hasControlCharacters(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0);
    if (codePoint !== undefined && (codePoint <= 0x1f || (codePoint >= 0x7f && codePoint <= 0x9f))) return true;
  }
  return false;
}

export interface FormFieldDraft {
  id: string;
  type: FormFieldType;
  required: boolean;
  minLength: string;
  maxLength: string;
  pattern: string;
  minimum: string;
  maximum: string;
  options: string;
  maxFiles: string;
  maxFileBytes: string;
  allowedContentTypes: string;
}

export function newFormFieldDraft(id = ""): FormFieldDraft {
  return {
    id, type: "text", required: false, minLength: "", maxLength: "", pattern: "",
    minimum: "", maximum: "", options: "", maxFiles: "1", maxFileBytes: "1048576",
    allowedContentTypes: "application/pdf",
  };
}

export function formFieldToDraft(id: string, definition: FormFieldDefinition): FormFieldDraft {
  const draft = newFormFieldDraft(id);
  draft.type = definition.type;
  draft.required = definition.required;
  if (definition.type === "text" || definition.type === "email") {
    draft.minLength = definition.min_length === undefined ? "" : String(definition.min_length);
    draft.maxLength = definition.max_length === undefined ? "" : String(definition.max_length);
    draft.pattern = definition.pattern ?? "";
  } else if (definition.type === "number") {
    draft.minimum = definition.minimum === undefined ? "" : String(definition.minimum);
    draft.maximum = definition.maximum === undefined ? "" : String(definition.maximum);
  } else if (definition.type === "select") {
    draft.options = definition.options.join("\n");
  } else if (definition.type === "file") {
    draft.maxFiles = String(definition.max_files);
    draft.maxFileBytes = String(definition.max_file_bytes);
    draft.allowedContentTypes = definition.allowed_content_types.join("\n");
  }
  return draft;
}

function optionalInteger(raw: string, maximum: number): number | undefined | null {
  if (raw === "") return undefined;
  if (raw.trim() !== raw) return null;
  const value = Number(raw);
  return Number.isSafeInteger(value) && value >= 0 && value <= maximum ? value : null;
}

function optionalNumber(raw: string): number | undefined | null {
  if (raw === "") return undefined;
  if (raw.trim() !== raw) return null;
  const value = Number(raw);
  return Number.isFinite(value) ? value : null;
}

export function buildFormFieldDefinitions(fields: FormFieldDraft[]): Record<string, FormFieldDefinition> | null {
  if (fields.length < 1 || fields.length > 128) return null;
  const definitions: Record<string, FormFieldDefinition> = {};
  for (const field of fields) {
    if (!/^[a-z][a-z0-9_]{0,63}$/.test(field.id) || Object.hasOwn(definitions, field.id)) return null;
    if (field.type === "text" || field.type === "email") {
      const min = optionalInteger(field.minLength, 1 << 20);
      const max = optionalInteger(field.maxLength, 1 << 20);
      if (min === null || max === null || (min !== undefined && min < 0) || (max !== undefined && max < 1) ||
        (min !== undefined && max !== undefined && min > max) || field.pattern.length > 256) return null;
      definitions[field.id] = { type: field.type, required: field.required,
        ...(min === undefined ? {} : { min_length: min }), ...(max === undefined ? {} : { max_length: max }),
        ...(field.pattern ? { pattern: field.pattern } : {}) };
    } else if (field.type === "number") {
      const minimum = optionalNumber(field.minimum);
      const maximum = optionalNumber(field.maximum);
      if (minimum === null || maximum === null || (minimum !== undefined && maximum !== undefined && minimum > maximum)) return null;
      definitions[field.id] = { type: "number", required: field.required,
        ...(minimum === undefined ? {} : { minimum }), ...(maximum === undefined ? {} : { maximum }) };
    } else if (field.type === "boolean") {
      definitions[field.id] = { type: "boolean", required: field.required };
    } else if (field.type === "select") {
      const options = field.options.split(/\r?\n/u).map((value) => value.trim()).filter(Boolean);
      if (options.length < 1 || options.length > 128 || new Set(options).size !== options.length || options.some((value) => value.length > 256)) return null;
      definitions[field.id] = { type: "select", required: field.required, options };
    } else if (field.type === "file") {
      const maxFiles = Number(field.maxFiles);
      const maxFileBytes = Number(field.maxFileBytes);
      const allowedContentTypes = field.allowedContentTypes.split(/\r?\n/u).map((value) => value.trim()).filter(Boolean);
      const unique = new Set(allowedContentTypes.map((value) => value.toLowerCase()));
      if (!Number.isSafeInteger(maxFiles) || maxFiles < 1 || maxFiles > 20 || !Number.isSafeInteger(maxFileBytes) || maxFileBytes < 1 ||
        allowedContentTypes.length < 1 || allowedContentTypes.length > 32 || unique.size !== allowedContentTypes.length ||
        allowedContentTypes.some((value) => !/^[A-Za-z0-9!#$&^_.+-]+\/[A-Za-z0-9!#$&^_.+-]+$/u.test(value) || isActiveFormContentType(value))) return null;
      definitions[field.id] = { type: "file", required: field.required, max_files: maxFiles,
        max_file_bytes: maxFileBytes, allowed_content_types: allowedContentTypes };
    } else {
      return null;
    }
  }
  return definitions;
}
