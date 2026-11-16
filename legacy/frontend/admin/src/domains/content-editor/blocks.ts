import type { JsonObject, JsonValue } from "@/types";
import { isRecord, uid } from "@/lib/utils";
import { deriveEntryTitle } from "@/lib/json";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import type {
  BlockType,
  BlockTypeOption,
  ContentBlock,
  HeadingBlock,
} from "./types";

export const BLOCK_OPTIONS: BlockTypeOption[] = [
  { type: "paragraph", label: "Параграф", description: "Обычный текст" },
  { type: "heading", label: "Заголовок", description: "Уровни 1–4" },
  { type: "bulletedList", label: "Маркированный список", description: "Пункты списка" },
  { type: "numberedList", label: "Нумерованный список", description: "Упорядоченные пункты" },
  { type: "quote", label: "Цитата", description: "Цитата и автор" },
  { type: "code", label: "Код", description: "Фрагмент кода" },
  { type: "divider", label: "Разделитель", description: "Горизонтальная линия" },
  { type: "image", label: "Изображение", description: "Файл из медиатеки" },
  { type: "gallery", label: "Галерея", description: "Несколько изображений" },
  { type: "video", label: "Видео", description: "MP4 из медиатеки" },
];

export const TEXT_BLOCK_TYPES: BlockType[] = [
  "paragraph",
  "heading",
  "quote",
  "code",
];

export function createBlock(type: BlockType): ContentBlock {
  const id = uid("block");
  switch (type) {
    case "heading":
      return { id, type, data: { text: "", level: 2 } };
    case "bulletedList":
      return { id, type, data: { items: [""] } };
    case "numberedList":
      return { id, type, data: { items: [""] } };
    case "quote":
      return { id, type, data: { text: "", attribution: "" } };
    case "code":
      return { id, type, data: { code: "", language: "" } };
    case "divider":
      return { id, type, data: {} };
    case "image":
      return { id, type, data: { mediaId: "", alt: "", caption: "" } };
    case "gallery":
      return { id, type, data: { mediaIds: [], caption: "" } };
    case "video":
      return { id, type, data: { mediaId: "", caption: "" } };
    case "paragraph":
    default:
      return { id, type: "paragraph", data: { text: "" } };
  }
}

export function duplicateBlock(block: ContentBlock): ContentBlock {
  const copy = JSON.parse(JSON.stringify(block)) as ContentBlock;
  copy.id = uid("block");
  return copy;
}

export function jsonClone(value: unknown): JsonValue {
  return JSON.parse(JSON.stringify(value)) as JsonValue;
}

function stringData(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function parseOne(raw: unknown, warnings: string[]): ContentBlock | null {
  if (!isRecord(raw)) return null;
  const id = typeof raw.id === "string" && raw.id ? raw.id : uid("block");
  const type = typeof raw.type === "string" ? raw.type : "";
  const data = isRecord(raw.data) ? raw.data : {};
  switch (type) {
    case "paragraph":
      return { id, type, data: { text: stringData(data.text) } };
    case "heading": {
      const rawLevel = typeof data.level === "number" ? data.level : 2;
      const level = Math.min(4, Math.max(1, Math.trunc(rawLevel))) as HeadingBlock["data"]["level"];
      return { id, type, data: { text: stringData(data.text), level } };
    }
    case "bulletedList":
    case "numberedList":
      return {
        id,
        type,
        data: {
          items: Array.isArray(data.items)
            ? data.items.filter((item): item is string => typeof item === "string")
            : [""],
        },
      };
    case "quote":
      return {
        id,
        type,
        data: {
          text: stringData(data.text),
          attribution: stringData(data.attribution),
        },
      };
    case "code":
      return {
        id,
        type,
        data: {
          code: stringData(data.code),
          language: stringData(data.language),
        },
      };
    case "divider":
      return { id, type, data: {} };
    case "image":
      return {
        id,
        type,
        data: {
          mediaId: stringData(data.mediaId),
          alt: stringData(data.alt),
          caption: stringData(data.caption),
        },
      };
    case "gallery":
      return {
        id,
        type,
        data: {
          mediaIds: Array.isArray(data.mediaIds)
            ? data.mediaIds.filter((item): item is string => typeof item === "string")
            : [],
          caption: stringData(data.caption),
        },
      };
    case "video":
      return {
        id,
        type,
        data: {
          mediaId: stringData(data.mediaId),
          caption: stringData(data.caption),
        },
      };
    default:
      warnings.push(
        `Блок неизвестного типа «${type || "без типа"}» пропущен. Проверьте данные через JSON.`,
      );
      return null;
  }
}

export function parseBlocks(value: JsonValue | undefined): {
  blocks: ContentBlock[];
  warnings: string[];
} {
  const warnings: string[] = [];
  if (!Array.isArray(value)) {
    if (value !== undefined && value !== null) {
      warnings.push("Значение поля блоков не является массивом. Блоки не загружены.");
    }
    return { blocks: [], warnings };
  }
  const blocks = value
    .map((item) => parseOne(item, warnings))
    .filter((block): block is ContentBlock => block !== null);
  return { blocks, warnings };
}

export function blocksToJson(blocks: ContentBlock[]): JsonValue[] {
  return blocks.map((block) => ({
    id: block.id,
    type: block.type,
    data: jsonClone(block.data),
  }));
}

export function isBlocksField(field: SchemaFieldNode): boolean {
  return field.type === "blocks";
}

export function blocksKeyFromFields(fields: SchemaFieldNode[]): string | null {
  return fields.find(isBlocksField)?.key ?? null;
}

export function starterValue(field: SchemaFieldNode): JsonValue {
  if (field.defaultValue) {
    switch (field.type) {
      case "number":
      case "integer": {
        const parsed = Number(field.defaultValue);
        return Number.isFinite(parsed) ? parsed : 0;
      }
      case "boolean":
        return field.defaultValue === "true";
      default:
        return field.defaultValue;
    }
  }
  switch (field.type) {
    case "number":
    case "integer": {
      // Respect schema bounds so a fresh entry doesn't start invalid.
      if (typeof field.min === "number") return field.min;
      if (typeof field.max === "number" && field.max < 0) return field.max;
      return 0;
    }
    case "array": {
      // Prefill arrays up to minItems so autosave never sends data the
      // published schema rejects (minItems would 422 an empty array).
      const minCount = Math.max(0, field.minItems ?? 0);
      const rows: JsonValue[] = [];
      for (let i = 0; i < minCount; i += 1) {
        if (field.itemType === "object") {
          const row: JsonObject = {};
          field.children.forEach((child) => {
            if (child.key.trim()) row[child.key.trim()] = starterValue(child);
          });
          rows.push(row);
          continue;
        }
        const pseudo: SchemaFieldNode = {
          ...field,
          id: `${field.id}-item`,
          key: "item",
          type: field.itemType as SchemaFieldNode["type"],
          children: [],
          required: false,
        };
        rows.push(pseudo.type === "object" ? {} : starterValue(pseudo));
      }
      return rows;
    }
    case "boolean":
      return false;
    case "enum":
      return field.enumValues.find((option) => option.value.trim())?.value.trim() ?? "";
    case "object": {
      const object: JsonObject = {};
      field.children.forEach((child) => {
        if (child.key.trim()) object[child.key.trim()] = starterValue(child);
      });
      return object;
    }
    case "blocks":
      return [];
    case "date":
      return new Date().toISOString().slice(0, 10);
    case "dateTime":
      return new Date().toISOString();
    default:
      return "";
  }
}

export function starterMetadata(fields: SchemaFieldNode[], blocksKey: string | null): JsonObject {
  const metadata: JsonObject = {};
  fields.forEach((field) => {
    if (field.type === "blocks" || field.key === blocksKey) return;
    if (field.key.trim()) metadata[field.key.trim()] = starterValue(field);
  });
  return metadata;
}

export function deriveDocumentTitle(
  metadata: JsonObject,
  blocks: ContentBlock[],
  fields?: { key: string; type: string }[] | null,
): string {
  const fromMeta = deriveEntryTitle(metadata, fields);
  if (fromMeta !== "Без названия") return fromMeta;
  const heading = blocks.find(
    (block): block is HeadingBlock => block.type === "heading" && block.data.text.trim().length > 0,
  );
  if (heading) return heading.data.text.trim();
  const paragraph = blocks.find(
    (block) => block.type === "paragraph" && block.data.text.trim().length > 0,
  );
  if (paragraph && paragraph.type === "paragraph") {
    return paragraph.data.text.trim().slice(0, 80);
  }
  return "Без названия";
}

export function documentMetadata(
  data: JsonObject | null | undefined,
  blocksKey: string | null,
): JsonObject {
  if (!data) return {};
  const copy: JsonObject = {};
  Object.entries(data).forEach(([key, value]) => {
    if (blocksKey && key === blocksKey) return;
    copy[key] = value;
  });
  return copy;
}

export function buildDocumentData(
  metadata: JsonObject,
  blocks: ContentBlock[],
  blocksKey: string | null,
): JsonObject {
  if (!blocksKey) return { ...metadata };
  return {
    ...metadata,
    [blocksKey]: blocksToJson(blocks),
  };
}

export function blockPreviewText(block: ContentBlock): string {
  switch (block.type) {
    case "paragraph":
    case "quote":
      return block.data.text.slice(0, 80);
    case "heading":
      return block.data.text.slice(0, 80);
    case "bulletedList":
    case "numberedList":
      return block.data.items.join(" · ").slice(0, 80);
    case "code":
      return block.data.code.slice(0, 80);
    case "image":
      return block.data.alt || "Изображение";
    case "gallery":
      return `${block.data.mediaIds.length} изображ.`;
    case "video":
      return block.data.caption || "Видео";
    case "divider":
      return "Разделитель";
    default:
      return "";
  }
}
