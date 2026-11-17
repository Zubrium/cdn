import { parseCssColor } from "@/lib/css-color";
import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import type { EntryFormIssue } from "./types";
import { codePointLength, isRecordValue } from "./helpers";

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function compiledPattern(pattern: string | undefined): RegExp | null {
  if (!pattern) return null;
  try {
    return new RegExp(pattern);
  } catch {
    return null;
  }
}

function stringFormatIssue(format: string | undefined, value: string): string | null {
  if (!format || value === "") return null;
  switch (format) {
    case "email":
      return EMAIL_PATTERN.test(value) ? null : "Введите адрес электронной почты";
    case "uri":
    case "url":
      try {
        const parsed = new URL(value);
        return parsed.protocol === "http:" || parsed.protocol === "https:" ? null : "Введите ссылку с http или https";
      } catch {
        return "Введите полный адрес ссылки";
      }
    case "uuid":
      return UUID_PATTERN.test(value) ? null : "Введите UUID";
    case "color":
      return parseCssColor(value) ? null : "Введите CSS-цвет";
    default:
      return null;
  }
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

export function validateEntryFields(
  fields: SchemaFieldNode[],
  metadata: JsonObject,
): EntryFormIssue[] {
  const issues: EntryFormIssue[] = [];
  for (const field of fields) {
    validateField(field, metadata[field.key], field.key, issues, false);
  }
  return issues;
}

function validateField(
  field: SchemaFieldNode,
  value: JsonValue | undefined,
  path: string,
  issues: EntryFormIssue[],
  perItem: boolean,
): void {
  if (value === undefined) {
    if (field.required) issues.push({ path, message: "Обязательно для заполнения" });
    return;
  }
  if (value === null) {
    if (!field.nullable) issues.push({ path, message: "Значение не может быть пустым" });
    return;
  }

  switch (field.type) {
    case "number":
    case "integer":
      if (typeof value !== "number") {
        issues.push({ path, message: "Введите число" });
        return;
      }
      if (field.type === "integer" && !Number.isInteger(value)) {
        issues.push({ path, message: "Введите целое число" });
      }
      if (field.min !== undefined && value < field.min) {
        issues.push({ path, message: `Не меньше ${field.min}` });
      }
      if (field.max !== undefined && value > field.max) {
        issues.push({ path, message: `Не больше ${field.max}` });
      }
      break;
    case "string":
    case "text":
    case "richText": {
      if (typeof value !== "string") {
        issues.push({ path, message: "Введите текст" });
        return;
      }
      const length = codePointLength(value);
      if (field.minLength !== undefined && length < field.minLength) {
        issues.push({
          path,
          message: `Минимум ${field.minLength} символов`,
        });
      }
      if (field.maxLength !== undefined && length > field.maxLength) {
        issues.push({
          path,
          message: `Не больше ${field.maxLength} символов`,
        });
      }
      const formatIssue = stringFormatIssue(field.stringFormat, value);
      if (formatIssue) issues.push({ path, message: formatIssue });
      const pattern = compiledPattern(field.pattern);
      if (pattern && value !== "" && !pattern.test(value)) {
        issues.push({ path, message: "Значение не соответствует заданному шаблону" });
      }
      break;
    }
    case "enum": {
      const allowed = field.enumValues.map((option) => option.value);
      if (!allowed.includes(String(value))) {
        issues.push({ path, message: "Выберите значение из списка" });
      }
      break;
    }
    case "date":
    case "dateTime":
      if (typeof value !== "string" || Number.isNaN(new Date(value).getTime())) {
        issues.push({ path, message: "Некорректная дата" });
      }
      break;
    case "media":
    case "file": {
      if (typeof value !== "string") issues.push({ path, message: "Выберите файл из медиатеки" });
      break;
    }
    case "array": {
      if (!Array.isArray(value)) {
        issues.push({ path, message: "Значение должно быть списком" });
        return;
      }
      const items = value;
      if (field.minItems !== undefined && items.length < field.minItems) {
        issues.push({ path, message: `Минимум ${field.minItems} элемент(ов)` });
      }
      if (field.maxItems !== undefined && items.length > field.maxItems) {
        issues.push({ path, message: `Не больше ${field.maxItems} элемент(ов)` });
      }
      items.forEach((item, index) => {
        const itemPath = `${path}.${index}`;
        if (field.itemType === "object") {
          if (!isRecordValue(item)) {
            issues.push({ path: itemPath, message: "Элемент должен быть объектом" });
            return;
          }
          field.children.forEach((child) => {
            validateField(
              child,
              item[child.key],
              `${itemPath}.${child.key}`,
              issues,
              true,
            );
          });
          return;
        }
        const pseudo: SchemaFieldNode = {
          ...field,
          id: `${field.id}-item`,
          key: String(index),
          type: field.itemType as SchemaFieldNode["type"],
          children: [],
          required: false,
        };
        validateField(pseudo, item as JsonValue, itemPath, issues, true);
      });
      break;
    }
    case "object": {
      if (!isRecordValue(value)) {
        issues.push({ path, message: "Значение должно быть объектом" });
        return;
      }
      // Missing object keys are reported only when the nested field is
      // required and this is not a per-item schema.
      field.children.forEach((child) => {
        validateField(
          child,
          value[child.key],
          `${path}.${child.key}`,
          issues,
          perItem,
        );
      });
      break;
    }
    case "blocks":
      // Block fields are legacy: their data passes through untouched and the
      // editor no longer validates it.
      break;
  }
}
