import type { JsonObject, JsonValue } from "@/types";
import { isRecord, uid } from "@/lib/utils";
import type {
  ArrayItemType,
  BlockTypeName,
  BuilderFieldType,
  BuilderIssue,
  EnumOption,
  ParsedSchema,
  SchemaFieldNode,
} from "./types";

export const BLOCK_TYPES: Array<{ value: BlockTypeName; label: string }> = [
  { value: "paragraph", label: "Параграф" },
  { value: "heading", label: "Заголовок" },
  { value: "bulletedList", label: "Маркированный список" },
  { value: "numberedList", label: "Нумерованный список" },
  { value: "quote", label: "Цитата" },
  { value: "code", label: "Код" },
  { value: "divider", label: "Разделитель" },
  { value: "image", label: "Изображение" },
  { value: "gallery", label: "Галерея" },
  { value: "video", label: "Видео" },
];

export const FIELD_KEY_PATTERN = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

const BLOCK_TYPE_NAMES = BLOCK_TYPES.map((block) => block.value);

export function createEnumOption(value = "", label = ""): EnumOption {
  return { id: uid("enum"), value, label };
}

export function createField(
  type: BuilderFieldType,
  overrides: Partial<SchemaFieldNode> = {},
): SchemaFieldNode {
  return {
    id: uid("field"),
    key: "",
    label: "",
    type,
    description: "",
    required: false,
    nullable: false,
    isPrivate: false,
    placeholder: "",
    defaultValue: "",
    minLength: undefined,
    maxLength: undefined,
    min: undefined,
    max: undefined,
    step: undefined,
    enumValues: type === "enum" ? [createEnumOption()] : [],
    children: [],
    itemType: "string",
    minItems: undefined,
    maxItems: undefined,
    allowedBlocks: BLOCK_TYPES.map((block) => block.value),
    ...overrides,
  };
}

export function createChildField(): SchemaFieldNode {
  return createField("string");
}

export function cloneField(field: SchemaFieldNode): SchemaFieldNode {
  return {
    ...field,
    id: uid("field"),
    enumValues: field.enumValues.map((option) => ({
      ...option,
      id: uid("enum"),
    })),
    children: field.children.map(cloneField),
  };
}

function nullableType(type: string, nullable: boolean): JsonValue {
  return nullable ? [type, "null"] : type;
}

function buildUi(field: SchemaFieldNode): JsonObject | null {
  const ui: JsonObject = {};
  if (field.label.trim()) ui.label = field.label.trim();
  if (field.placeholder.trim()) ui.placeholder = field.placeholder.trim();
  if (field.type === "text") ui.widget = "textarea";
  if (field.type === "richText") ui.widget = "richText";
  if (field.type === "media") ui.widget = "media";
  if (field.type === "file") ui.widget = "file";
  if (field.type === "blocks") {
    ui.widget = "blocks";
    ui.allowedBlocks = field.allowedBlocks.filter((block) =>
      BLOCK_TYPE_NAMES.includes(block),
    );
  }
  if ((field.type === "number" || field.type === "integer") && field.step !== undefined) {
    ui.step = field.step;
  }
  return Object.keys(ui).length > 0 ? ui : null;
}

function applyCommon(schema: JsonObject, field: SchemaFieldNode): JsonObject {
  const label = field.label.trim() || field.key.trim();
  if (label) schema.title = label;
  if (field.description.trim()) schema.description = field.description.trim();
  if (field.isPrivate) schema["x-private"] = true;
  const ui = buildUi(field);
  if (ui) schema["x-ui"] = ui;
  if (field.defaultValue !== "" && field.type !== "object" && field.type !== "array" && field.type !== "blocks") {
    if (field.type === "number" || field.type === "integer") {
      const parsed = Number(field.defaultValue);
      if (Number.isFinite(parsed)) {
        schema.default = field.type === "integer" ? Math.trunc(parsed) : parsed;
      }
    } else if (field.type === "boolean") {
      if (field.defaultValue === "true" || field.defaultValue === "false") {
        schema.default = field.defaultValue === "true";
      }
    } else if (field.type === "enum") {
      if (field.enumValues.some((option) => option.value === field.defaultValue)) {
        schema.default = field.defaultValue;
      }
    } else {
      schema.default = field.defaultValue;
    }
  }
  return schema;
}

function enumValuesToSchema(values: EnumOption[], nullable: boolean): JsonValue[] {
  const result: JsonValue[] = values
    .map((option) => option.value.trim())
    .filter(Boolean);
  if (nullable) result.push(null);
  return result;
}

function enumLabels(field: SchemaFieldNode): JsonObject | undefined {
  const labels: JsonObject = {};
  field.enumValues.forEach((option) => {
    if (option.value.trim() && option.label.trim()) {
      labels[option.value.trim()] = option.label.trim();
    }
  });
  return Object.keys(labels).length > 0 ? labels : undefined;
}

function orderedKeys(fields: SchemaFieldNode[], limit = 500): JsonValue[] {
  const keys: JsonValue[] = [];
  for (const child of fields) {
    const key = child.key.trim();
    if (key) keys.push(key);
  }
  return keys.slice(0, limit);
}

function sortEntriesByOrder<T>(
  entries: [string, T][],
  order: JsonValue | undefined,
): [string, T][] {
  if (!Array.isArray(order)) return entries;
  const rank = new Map<string, number>();
  order.forEach((value, index) => {
    if (typeof value === "string") rank.set(value, index);
  });
  if (rank.size === 0) return entries;
  // Judged keys come first in the persisted order; keys not listed keep their
  // schema order after them.
  return [...entries]
    .map(([key, value], index) => ({
      key,
      value,
      rank: rank.get(key) ?? order.length + index,
    }))
    .sort((a, b) => a.rank - b.rank)
    .map(({ key, value }) => [key, value] as [string, T]);
}

function buildObjectSchema(field: SchemaFieldNode): JsonObject {
  const properties: JsonObject = {};
  const required: JsonValue[] = [];
  for (const child of field.children) {
    const key = child.key.trim();
    if (!key) continue;
    properties[key] = fieldToSchema(child);
    if (child.required) required.push(key);
  }
  const schema: JsonObject = {
    type: nullableType("object", field.nullable),
    properties,
    additionalProperties: false,
  };
  // jsonb canonicalizes object key order server-side, so the visual field
  // order is persisted explicitly and restored when parsing.
  const order = orderedKeys(field.children);
  if (order.length > 0) schema["x-order"] = order.slice(0, 20);
  if (required.length > 0) schema.required = required;
  return schema;
}

function buildArrayItemSchema(field: SchemaFieldNode): JsonObject {
  switch (field.itemType) {
    case "number":
    case "integer":
      return {
        type: field.itemType,
        ...(field.min !== undefined ? { minimum: field.min } : {}),
        ...(field.max !== undefined ? { maximum: field.max } : {}),
      };
    case "boolean":
      return { type: "boolean" };
    case "enum": {
      const labels = enumLabels(field);
      return {
        type: "string",
        enum: field.enumValues.map((option) => option.value.trim()).filter(Boolean),
        ...(labels ? { "x-ui": { labels } } : {}),
      };
    }
    case "object": {
      const properties: JsonObject = {};
      const required: JsonValue[] = [];
      for (const child of field.children) {
        const key = child.key.trim();
        if (!key) continue;
        properties[key] = fieldToSchema(child);
        if (child.required) required.push(key);
      }
      const order = orderedKeys(field.children);
      return {
        type: "object",
        properties,
        additionalProperties: false,
        ...(order.length > 0 ? { "x-order": order } : {}),
        ...(required.length > 0 ? { required } : {}),
      };
    }
    case "media":
      return { type: "string", format: "media" };
    case "file":
      return { type: "string", format: "file" };
    case "string":
    default:
      return { type: "string" };
  }
}

function fieldToSchema(field: SchemaFieldNode): JsonObject {
  switch (field.type) {
    case "string":
    case "text":
    case "richText":
    case "date":
    case "dateTime":
    case "media": {
      const schema: JsonObject = {
        type: nullableType("string", field.nullable),
      };
      if (field.type === "date") schema.format = "date";
      if (field.type === "dateTime") schema.format = "date-time";
      if (field.type === "media") schema.format = "media";
      if (field.minLength !== undefined) schema.minLength = field.minLength;
      if (field.maxLength !== undefined) schema.maxLength = field.maxLength;
      return applyCommon(schema, field);
    }
    case "file": {
      const schema: JsonObject = { type: nullableType("string", field.nullable), format: "file" };
      if (field.minLength !== undefined) schema.minLength = field.minLength;
      if (field.maxLength !== undefined) schema.maxLength = field.maxLength;
      return applyCommon(schema, field);
    }
    case "number":
    case "integer": {
      const schema: JsonObject = {
        type: nullableType(field.type, field.nullable),
      };
      if (field.min !== undefined) schema.minimum = field.min;
      if (field.max !== undefined) schema.maximum = field.max;
      return applyCommon(schema, field);
    }
    case "boolean":
      return applyCommon({ type: nullableType("boolean", field.nullable) }, field);
    case "enum": {
      const values = enumValuesToSchema(field.enumValues, field.nullable);
      const labels = enumLabels(field);
      const schema: JsonObject = {
        type: nullableType("string", field.nullable),
        enum: values,
      };
      // applyCommon re-emits x-ui from buildUi, so the enum labels have to be
      // merged afterwards — otherwise any field with a label/placeholder would
      // silently drop the option labels on the next save.
      const withCommon = applyCommon(schema, field);
      if (labels) {
        withCommon["x-ui"] = {
          ...(isRecord(withCommon["x-ui"]) ? withCommon["x-ui"] : {}),
          labels,
        };
      }
      return withCommon;
    }
    case "object":
      return applyCommon(buildObjectSchema(field), field);
    case "array": {
      const schema: JsonObject = {
        type: nullableType("array", field.nullable),
        items: buildArrayItemSchema(field),
      };
      if (field.minItems !== undefined) schema.minItems = field.minItems;
      if (field.maxItems !== undefined) schema.maxItems = field.maxItems;
      return applyCommon(schema, field);
    }
    case "blocks": {
      const blockNames = field.allowedBlocks.length
        ? field.allowedBlocks
        : BLOCK_TYPE_NAMES;
      const schema: JsonObject = {
        type: nullableType("array", field.nullable),
        items: {
          type: "object",
          properties: {
            id: { type: "string" },
            type: { type: "string", enum: blockNames },
            data: { type: "object" },
          },
          required: ["id", "type", "data"],
        },
      };
      if (field.minItems !== undefined) schema.minItems = field.minItems;
      if (field.maxItems !== undefined) schema.maxItems = field.maxItems;
      return applyCommon(schema, field);
    }
    default:
      return applyCommon({ type: nullableType("string", field.nullable) }, field);
  }
}

export function schemaFromFields(fields: SchemaFieldNode[], title: string): JsonObject {
  const properties: JsonObject = {};
  const required: JsonValue[] = [];
  for (const field of fields) {
    const key = field.key.trim();
    if (!key) continue;
    properties[key] = fieldToSchema(field);
    if (field.required) required.push(key);
  }
  const schema: JsonObject = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    title,
    type: "object",
    properties,
    additionalProperties: false,
  };
  const order = orderedKeys(fields);
  if (order.length > 0) schema["x-order"] = order;
  if (required.length > 0) schema.required = required;
  return schema;
}

function nullableInfo(schema: JsonObject): boolean {
  const type = schema.type;
  if (Array.isArray(type)) return type.includes("null");
  return schema.type === "null";
}

function baseType(schema: JsonObject): string | undefined {
  if (typeof schema.type === "string") return schema.type;
  if (Array.isArray(schema.type)) {
    const first = schema.type.find((value) => typeof value === "string" && value !== "null");
    return typeof first === "string" ? first : undefined;
  }
  return undefined;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function parseEnumValues(raw: JsonObject, nullable: boolean): EnumOption[] {
  const values = Array.isArray(raw.enum) ? raw.enum : [];
  const ui = isRecord(raw["x-ui"]) ? raw["x-ui"] : {};
  const labels = isRecord(ui.labels) ? ui.labels : {};
  return values
    .filter((value): value is string => typeof value === "string")
    .map((value) => ({
      id: uid("enum"),
      value,
      label: typeof labels[value] === "string" ? (labels[value] as string) : "",
    }))
    .concat(
      nullable && values.includes(null)
        ? []
        : values.length === 0
          ? [createEnumOption()]
          : [],
    );
}

function parseField(
  key: string,
  raw: JsonObject,
  required: boolean,
  warnings: string[],
): SchemaFieldNode {
  const nullable = nullableInfo(raw);
  const type = baseType(raw);
  const format = typeof raw.format === "string" ? raw.format : "";
  const ui = isRecord(raw["x-ui"]) ? raw["x-ui"] : {};
  const widget = typeof ui.widget === "string" ? ui.widget : "";
  const label =
    typeof raw.title === "string"
      ? raw.title
      : typeof ui.label === "string"
        ? ui.label
        : key;
  const base: Partial<SchemaFieldNode> = {
    id: uid("field"),
    key,
    label,
    description: stringValue(raw.description),
    required,
    nullable,
    isPrivate: raw["x-private"] === true,
    mediaProfile: isRecord(raw["x-media"]) ? raw["x-media"] : undefined,
    placeholder: stringValue(ui.placeholder),
    stringFormat: format || undefined,
    pattern: typeof raw.pattern === "string" ? raw.pattern : undefined,
    defaultValue:
      typeof raw.default === "string"
        ? raw.default
        : typeof raw.default === "number" || typeof raw.default === "boolean"
          ? String(raw.default)
          : "",
    minLength: numberValue(raw.minLength),
    maxLength: numberValue(raw.maxLength),
    min: numberValue(raw.minimum),
    max: numberValue(raw.maximum),
    step: numberValue(ui.step),
    enumValues: [],
    children: [],
    itemType: "string",
    minItems: numberValue(raw.minItems),
    maxItems: numberValue(raw.maxItems),
    allowedBlocks:
      Array.isArray(ui.allowedBlocks) && ui.allowedBlocks.length > 0
        ? ui.allowedBlocks.filter((value): value is BlockTypeName =>
            typeof value === "string" && BLOCK_TYPE_NAMES.includes(value as BlockTypeName),
          )
        : [...BLOCK_TYPE_NAMES],
  };

  if (raw.anyOf || raw.oneOf) {
    warnings.push(
      `Поле «${key}» использует anyOf/oneOf. Конструктор отобразит только простую часть, проверьте результат перед публикацией.`,
    );
  }

  if (Array.isArray(raw.enum)) {
    return createField("enum", { ...base, enumValues: parseEnumValues(raw, nullable) });
  }
  if (type === "string") {
    if (format === "date") return createField("date", { ...base, stringFormat: undefined });
    if (format === "date-time") return createField("dateTime", { ...base, stringFormat: undefined });
    if (format === "media" || widget === "media") return createField("media", { ...base, stringFormat: undefined });
    if (format === "file" || widget === "file") return createField("file", { ...base, stringFormat: undefined });
    if (widget === "richText") return createField("richText", base);
    if (widget === "textarea" || widget === "text") return createField("text", base);
    return createField("string", base);
  }
  if (type === "number") return createField("number", base);
  if (type === "integer") return createField("integer", base);
  if (type === "boolean") return createField("boolean", base);
  if (type === "object") {
    const properties = isRecord(raw.properties) ? raw.properties : {};
    const requiredKeys = new Set(
      Array.isArray(raw.required)
        ? raw.required.filter((value): value is string => typeof value === "string")
        : [],
    );
    const entries = Object.entries(properties).filter(
      (entry): entry is [string, JsonObject] => isRecord(entry[1]),
    );
    const children = sortEntriesByOrder(entries, raw["x-order"]).map(
      ([childKey, childRaw]) =>
        parseField(childKey, childRaw, requiredKeys.has(childKey), warnings),
    );
    return createField("object", { ...base, children });
  }
  if (type === "array") {
    const items = isRecord(raw.items) ? raw.items : {};
    if (widget === "blocks") {
      return createField("blocks", base);
    }
    const itemTypeRaw = baseType(items);
    if (itemTypeRaw === "object") {
      const properties = isRecord(items.properties) ? items.properties : {};
      const requiredKeys = new Set(
        Array.isArray(items.required)
          ? items.required.filter((value): value is string => typeof value === "string")
          : [],
      );
      const itemEntries = Object.entries(properties).filter(
        (entry): entry is [string, JsonObject] => isRecord(entry[1]),
      );
      const children = sortEntriesByOrder(itemEntries, items["x-order"]).map(
        ([childKey, childRaw]) =>
          parseField(childKey, childRaw, requiredKeys.has(childKey), warnings),
      );
      return createField("array", {
        ...base,
        itemType: "object",
        children,
      });
    }
    if (Array.isArray(items.enum)) {
      const enumValues = parseEnumValues(items, false);
      return createField("array", {
        ...base,
        itemType: "enum",
        enumValues,
      });
    }
    if (items.format === "media") {
      return createField("array", {
        ...base,
        itemType: "media",
        itemMediaProfile: isRecord(items["x-media"]) ? items["x-media"] : undefined,
      });
    }
    if (items.format === "file") {
      return createField("array", {
        ...base,
        itemType: "file",
        itemMediaProfile: isRecord(items["x-media"]) ? items["x-media"] : undefined,
      });
    }
    const itemType: ArrayItemType =
      itemTypeRaw === "number" ||
      itemTypeRaw === "integer" ||
      itemTypeRaw === "boolean"
        ? itemTypeRaw
        : "string";
    return createField("array", {
      ...base,
      itemType,
      min: numberValue(items.minimum),
      max: numberValue(items.maximum),
    });
  }

  warnings.push(
    `Поле «${key}» имеет неподдерживаемый тип «${String(raw.type ?? "unknown")}» и показано как строка.`,
  );
  return createField("string", base);
}

export function parsedSchema(raw: JsonObject | null | undefined): ParsedSchema {
  if (!raw) return { fields: [], warnings: [] };
  const warnings: string[] = [];
  if (raw.type && raw.type !== "object") {
    warnings.push("Корень схемы не является object. Конструктор работает с объектными схемами.");
  }
  if (raw.additionalProperties === true) {
    warnings.push(
      "В схеме разрешены дополнительные свойства. При сохранении из конструктора будет задано additionalProperties: false.",
    );
  }
  if (raw.$ref || raw.anyOf || raw.oneOf || raw.allOf) {
    warnings.push(
      "Корень схемы использует $ref/anyOf/oneOf/allOf. Эти конструкции не поддерживаются визуальным конструктором.",
    );
  }
  const properties = isRecord(raw.properties) ? raw.properties : {};
  const requiredKeys = new Set(
    Array.isArray(raw.required)
      ? raw.required.filter((value): value is string => typeof value === "string")
      : [],
  );
  const entries = Object.entries(properties).filter(
    (entry): entry is [string, JsonObject] => isRecord(entry[1]),
  );
  const fields = sortEntriesByOrder(entries, raw["x-order"]).map(
    ([key, property]) =>
      parseField(key, property, requiredKeys.has(key), warnings),
  );
  return { fields, warnings };
}

export function validateFields(fields: SchemaFieldNode[]): BuilderIssue[] {
  const issues: BuilderIssue[] = [];
  const walk = (
    nodes: SchemaFieldNode[],
    parentPath: string,
  ) => {
    const keys = new Map<string, SchemaFieldNode>();
    nodes.forEach((field) => {
      const key = field.key.trim();
      const path = parentPath ? `${parentPath}.${key || "?"}` : key || "?";
      if (!key) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Укажите ключ поля",
          severity: "error",
        });
      } else if (!FIELD_KEY_PATTERN.test(key)) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Ключ: латинская буква или _, далее только буквы, цифры и _",
          severity: "error",
        });
      } else if (keys.has(key)) {
        issues.push({
          fieldId: field.id,
          path,
          message: `Дублирующийся ключ «${key}» на одном уровне`,
          severity: "error",
        });
      } else {
        keys.set(key, field);
      }
      if (!field.label.trim()) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Укажите название поля",
          severity: "warning",
        });
      }

      if (
        field.minLength !== undefined &&
        field.maxLength !== undefined &&
        field.minLength > field.maxLength
      ) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Минимальная длина больше максимальной",
          severity: "error",
        });
      }
      if (
        field.min !== undefined &&
        field.max !== undefined &&
        field.min > field.max
      ) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Минимум больше максимума",
          severity: "error",
        });
      }
      if (
        field.minItems !== undefined &&
        field.maxItems !== undefined &&
        field.minItems > field.maxItems
      ) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Минимальное количество больше максимального",
          severity: "error",
        });
      }

      if (field.type === "enum") {
        const values = field.enumValues.map((option) => option.value.trim());
        if (values.length === 0 || values.every((value) => !value)) {
          issues.push({
            fieldId: field.id,
            path,
            message: "Enum должен содержать хотя бы одно значение",
            severity: "error",
          });
        }
        if (values.some((value) => !value)) {
          issues.push({
            fieldId: field.id,
            path,
            message: "У enum-поля есть пустые значения",
            severity: "error",
          });
        }
        const seen = new Set<string>();
        values.filter(Boolean).forEach((value) => {
          if (seen.has(value)) {
            issues.push({
              fieldId: field.id,
              path,
              message: `Значение enum «${value}» повторяется`,
              severity: "error",
            });
          }
          seen.add(value);
        });
        if (
          field.defaultValue &&
          !values.includes(field.defaultValue)
        ) {
          issues.push({
            fieldId: field.id,
            path,
            message: "Значение по умолчанию отсутствует в enum",
            severity: "error",
          });
        }
      }

      if (field.type === "object") {
        if (field.children.length === 0) {
          issues.push({
            fieldId: field.id,
            path,
            message: "Объект не содержит дочерних полей",
            severity: "warning",
          });
        }
        walk(field.children, path);
      }
      if (field.type === "array") {
        if (field.itemType === "object") {
          if (field.children.length === 0) {
            issues.push({
              fieldId: field.id,
              path,
              message: "Массив объектов не содержит полей элемента",
              severity: "warning",
            });
          }
          walk(field.children, `${path}[]`);
        }
        if (field.itemType === "enum") {
          const values = field.enumValues.map((option) => option.value.trim()).filter(Boolean);
          if (values.length === 0) {
            issues.push({
              fieldId: field.id,
              path,
              message: "Enum элемента массива не содержит значений",
              severity: "error",
            });
          }
          if (new Set(values).size !== values.length) {
            issues.push({
              fieldId: field.id,
              path,
              message: "Значения enum элемента массива повторяются",
              severity: "error",
            });
          }
        }
      }
      if (field.type === "blocks" && field.allowedBlocks.length === 0) {
        issues.push({
          fieldId: field.id,
          path,
          message: "Выберите хотя бы один разрешённый тип блока",
          severity: "error",
        });
      }
    });
  };
  walk(fields, "");
  return issues;
}

export function fieldLabel(path: string, fields: SchemaFieldNode[]): string {
  const parts = path.split(".");
  let nodes = fields;
  let label = path;
  for (const part of parts) {
    const node = nodes.find((field) => field.key === part);
    if (!node) break;
    label = node.label || node.key;
    nodes = node.children;
  }
  return label;
}

export function findBlocksField(fields: SchemaFieldNode[]): SchemaFieldNode | undefined {
  return fields.find((field) => field.type === "blocks");
}

export interface FlatField {
  field: SchemaFieldNode;
  depth: number;
  path: string;
}

export function flattenFields(
  fields: SchemaFieldNode[],
  depth = 0,
  prefix = "",
): FlatField[] {
  return fields.flatMap((field) => {
    const path = prefix ? `${prefix}.${field.key}` : field.key;
    return [
      { field, depth, path },
      ...flattenFields(field.children, depth + 1, path),
    ];
  });
}
