import {
  AlignLeft,
  Binary,
  Braces,
  Calendar,
  CalendarClock,
  File as FileIcon,
  Hash,
  Image,
  List,
  ListTree,
  Pilcrow,
  ToggleLeft,
  Type,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ArrayItemType, BuilderFieldType } from "./types";

export interface FieldTypeMeta {
  value: BuilderFieldType;
  label: string;
  group: "Текст" | "Числа и даты" | "Структуры" | "Медиа";
  hint: string;
  icon: LucideIcon;
}

export const FIELD_TYPES: FieldTypeMeta[] = [
  { value: "string", label: "Строка", group: "Текст", hint: "Однострочный текст", icon: Type },
  { value: "text", label: "Многострочный текст", group: "Текст", hint: "Textarea / длинный текст", icon: AlignLeft },
  { value: "richText", label: "Rich text", group: "Текст", hint: "Форматированный текст (widget richText)", icon: Pilcrow },
  { value: "number", label: "Число", group: "Числа и даты", hint: "Дробное число", icon: Hash },
  { value: "integer", label: "Целое число", group: "Числа и даты", hint: "Только целые значения", icon: Binary },
  { value: "boolean", label: "Boolean", group: "Числа и даты", hint: "Да / нет", icon: ToggleLeft },
  { value: "date", label: "Дата", group: "Числа и даты", hint: "Строка с format: date", icon: Calendar },
  { value: "dateTime", label: "Дата и время", group: "Числа и даты", hint: "Строка с format: date-time", icon: CalendarClock },
  { value: "enum", label: "Enum", group: "Структуры", hint: "Фиксированный список значений", icon: List },
  { value: "object", label: "Объект", group: "Структуры", hint: "Вложенные поля", icon: Braces },
  { value: "array", label: "Массив", group: "Структуры", hint: "Повторяющиеся значения", icon: ListTree },
  { value: "media", label: "Медиа", group: "Медиа", hint: "ID файла медиатеки, format: media", icon: Image },
  { value: "file", label: "Файл", group: "Медиа", hint: "ID изображения, видео, SVG или PDF, format: file", icon: FileIcon },
];

export const ARRAY_ITEM_TYPES: Array<{ value: ArrayItemType; label: string }> = [
  { value: "string", label: "Строка" },
  { value: "number", label: "Число" },
  { value: "integer", label: "Целое" },
  { value: "boolean", label: "Boolean" },
  { value: "enum", label: "Enum" },
  { value: "object", label: "Объект" },
  { value: "media", label: "Медиа" },
  { value: "file", label: "Файл" },
];

export function fieldTypeMeta(value: BuilderFieldType): FieldTypeMeta {
  return FIELD_TYPES.find((type) => type.value === value) ?? FIELD_TYPES[0]!;
}
