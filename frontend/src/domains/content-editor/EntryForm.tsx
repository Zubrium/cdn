import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDown,
  ArrowUp,
  Copy,
  File as FileIcon,
  ImagePlus,
  Lock,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import { parseCssColor } from "@/lib/css-color";
import { cn } from "@/lib/utils";
import type { JsonObject, JsonValue, MediaRenditionContext } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { api, publicMediaPath } from "@/api";
import { Button } from "@/components/ui/button";
import { Input, Label, Textarea } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/misc";
import { Badge } from "@/components/ui/badge";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { queryKeys } from "@/lib/query-keys";
import type { Media } from "@/types";
import { starterValue } from "./blocks";

/**
 * Strict, schema-driven entry editor. Every visible field comes from the
 * published/draft schema; unknown keys stay untouched in the data. Structured
 * fields (object/array) are edited with nested widgets and a table instead of
 * dedicated graphical controls.
 */

export interface EntryFormIssue {
  /** Dotted path, e.g. "gallery.0.caption" or "title". */
  path: string;
  message: string;
}

export interface MediaPickTarget {
  label: string;
  single: boolean;
  accept?: "image" | "video" | "all";
  fieldPath: string;
  preparation?: MediaRenditionContext;
  apply: (media: Media[]) => void;
}

const MediaRenditionScope = createContext<Omit<MediaRenditionContext, "fieldPath"> | null>(null);

function pointerPart(value: string): string {
  return value.replace(/~/g, "~0").replace(/\//g, "~1");
}

function mediaAccept(profile: JsonObject | undefined): "image" | "video" | "all" {
  if (profile?.accept === "video") return "video";
  if (profile?.accept === "file") return "all";
  return "image";
}

function isRecordValue(value: JsonValue | undefined): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function cloneOriginal<T extends JsonValue>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function scrollToEntryField(path: string): void {
  const candidates: string[] = [];
  const parts = path.split(".");
  for (let index = parts.length; index > 0; index -= 1) {
    candidates.push(entryFieldAnchorId(parts.slice(0, index).join(".")));
  }
  for (const id of candidates) {
    const section = document.getElementById(id);
    if (!section) continue;
    section.scrollIntoView({ block: "center" });
    const control = document.getElementById(`${id}-control`);
    (control ?? section).focus({ preventScroll: true });
    return;
  }
}

export function fieldLabelForPath(fields: SchemaFieldNode[], path: string): string {
  const parts = path.split(".");
  const labels: string[] = [];
  let nodes = fields;
  for (const part of parts) {
    if (/^\d+$/.test(part)) {
      labels.push(`элемент ${Number(part) + 1}`);
      continue;
    }
    const field = nodes.find((candidate) => candidate.key === part);
    if (!field) {
      labels.push(part);
      break;
    }
    labels.push(field.label || field.key);
    nodes = field.children;
  }
  return labels.join(" · ");
}

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

function codePointLength(value: string): number {
  return [...value].length;
}

/**
 * Choose a card layout when object rows would not fit a compact table:
 * long text controls, nested structures, media pickers, or many columns.
 */
function arrayUsesCards(field: SchemaFieldNode): boolean {
  if (field.itemType !== "object") return false;
  const children = field.children.filter((child) => child.key.trim());
  if (children.length > 4) return true;
  return children.some((child) =>
    child.type === "object"
    || child.type === "array"
    || child.type === "media"
    || child.type === "file"
    || child.type === "text"
    || child.type === "richText",
  );
}

function stringControlType(field: SchemaFieldNode): string {
  switch (field.stringFormat) {
    case "email":
      return "email";
    case "uri":
    case "url":
      return "url";
    case "tel":
      return "tel";
    case "password":
      return "password";
    default:
      return "text";
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

// ---------------------------------------------------------------------------
// Controls
// ---------------------------------------------------------------------------

export function entryFieldAnchorId(path: string): string {
  return `entry-field-${encodeURIComponent(path)}`;
}

function FieldCard({
  field,
  anchorId,
  error,
  action,
  compact,
  children,
}: {
  field: SchemaFieldNode;
  anchorId: string;
  error?: string;
  action?: ReactNode;
  compact?: boolean;
  children: ReactNode;
}) {
  const labelId = `${anchorId}-label`;
  const descriptionId = field.description ? `${anchorId}-description` : undefined;
  const errorId = error ? `${anchorId}-error` : undefined;
  return (
    <section
      id={anchorId}
      tabIndex={-1}
      role="group"
      aria-labelledby={labelId}
      aria-describedby={[descriptionId, errorId].filter(Boolean).join(" ") || undefined}
      className={cn(
        "scroll-mt-24 focus-visible:rounded-md",
        compact
          ? "space-y-1.5 border-b border-border py-3 first:pt-0 last:border-0 last:pb-0"
          : "space-y-2 border-b border-border pb-7 pt-7 first:pt-0 last:border-0 last:pb-0",
        error && "rounded-md bg-destructive/5 px-3",
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <Label
          id={labelId}
          htmlFor={field.type !== "object" && field.type !== "array" && field.type !== "media" && field.type !== "file" ? `${anchorId}-control` : undefined}
          className="text-sm leading-5"
        >
          {field.label || field.key}
          {field.required && <span className="ml-0.5 text-destructive">*</span>}
        </Label>
        <div className="flex shrink-0 items-center gap-1">
          {action}
          {field.isPrivate && (
            <Badge variant="muted" title="x-private: исключается из публичных данных">
              <Lock />
              private
            </Badge>
          )}
        </div>
      </div>
      {field.description && (
        <p id={descriptionId} className="text-xs leading-5 text-muted-foreground">
          {field.description}
        </p>
      )}
      <div>{children}</div>
      {error && (
        <p id={errorId} className="flex items-center gap-1.5 text-xs text-destructive">
          <span aria-hidden="true">!</span>{error}
        </p>
      )}
    </section>
  );
}

function MediaValue({
  mediaId,
  fieldPath,
  variant = "image",
  onPick,
  onClear,
  disabled,
  compact,
}: {
  mediaId: string;
  fieldPath: string;
  variant?: "image" | "file";
  onPick: () => void;
  onClear: () => void;
  disabled?: boolean;
  compact?: boolean;
}) {
  const scope = useContext(MediaRenditionScope);
  const queryClient = useQueryClient();
  const preparation = useMemo(
    () => (scope && fieldPath ? { ...scope, fieldPath } : null),
    [fieldPath, scope],
  );
  const stateKey = useMemo(
    () => ["media", "rendition", mediaId, preparation?.featureKey, preparation?.schemaKind, fieldPath, preparation?.buildDigest] as const,
    [fieldPath, mediaId, preparation],
  );
  const requestedKey = useRef("");
  const renditionQuery = useQuery({
    queryKey: stateKey,
    queryFn: ({ signal }) => api.media.renditionState(mediaId, preparation!, signal),
    enabled: Boolean(mediaId && preparation),
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "pending" || status === "processing" ? 1500 : false;
    },
  });
  const mediaQuery = useQuery({
    queryKey: queryKeys.media.detail(mediaId),
    queryFn: ({ signal }) => api.media.get(mediaId, signal),
    enabled: Boolean(mediaId),
    staleTime: 5 * 60 * 1000,
  });
  useEffect(() => {
    if (!preparation || !mediaId || renditionQuery.data?.status !== "pending") return;
    const requestKey = `${mediaId}:${preparation.featureKey}:${preparation.schemaKind}:${fieldPath}:${preparation.buildDigest}`;
    if (requestedKey.current === requestKey) return;
    requestedKey.current = requestKey;
    void api.media.prepareRenditions(mediaId, preparation)
      .then((state) => queryClient.setQueryData(stateKey, state))
      .catch((error: unknown) => {
        queryClient.setQueryData(stateKey, {
          mediaId,
          status: "failed",
          outputs: {},
          error: error instanceof Error ? error.message : "Не удалось поставить обработку в очередь",
        });
      });
  }, [fieldPath, mediaId, preparation, queryClient, renditionQuery.data?.status, stateKey]);
  const retryProcessing = async () => {
    if (!preparation) return;
    requestedKey.current = `${mediaId}:${preparation.featureKey}:${preparation.schemaKind}:${fieldPath}:${preparation.buildDigest}`;
    try {
      const state = await api.media.prepareRenditions(mediaId, preparation);
      queryClient.setQueryData(stateKey, state);
    } catch (error) {
      queryClient.setQueryData(stateKey, {
        mediaId,
        status: "failed",
        outputs: {},
        error: error instanceof Error ? error.message : "Не удалось поставить обработку в очередь",
      });
    }
  };

  return (
    <div className="space-y-2">
      {mediaId && preparation && renditionQuery.data && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          {renditionQuery.data.status === "ready" ? (
            <Badge variant="success">Медиа подготовлено</Badge>
          ) : renditionQuery.data.status === "failed" ? (
            <>
              <Badge variant="destructive">Ошибка подготовки</Badge>
              <span className="min-w-0 break-words [overflow-wrap:anywhere] text-destructive">{renditionQuery.data.error || "Производные файлы не созданы"}</span>
              <Button type="button" variant="outline" size="sm" onClick={() => void retryProcessing()}>
                Повторить
              </Button>
            </>
          ) : (
            <Badge variant="warning">Подготовка медиа…</Badge>
          )}
        </div>
      )}
      {mediaId ? (
        <div className="overflow-hidden rounded-md border border-border">
          {variant === "image" ? (
            <div className={compact ? "flex h-16 items-center bg-muted/20 px-2" : "flex max-h-56 items-center justify-center bg-muted/20"}>
              <img
                src={publicMediaPath(mediaId)}
                alt={mediaQuery.data?.original_filename ? `Выбрано: ${mediaQuery.data.original_filename}` : "Выбранный файл"}
                width={compact ? 112 : 448}
                height={compact ? 56 : 224}
                loading="lazy"
                className={compact ? "max-h-14 w-auto max-w-full object-contain" : "max-h-56 w-auto max-w-full object-contain"}
              />
            </div>
          ) : (
            <a href={publicMediaPath(mediaId)} target="_blank" rel="noreferrer" className="flex items-center gap-2 px-3 py-2 text-sm hover:bg-muted/30">
              <FileIcon className="h-5 w-5 text-muted-foreground" />
              <span className="min-w-0 truncate">{mediaQuery.data?.original_filename ?? "Открыть файл"}</span>
            </a>
          )}
          <div className="flex items-center justify-between gap-2 border-t border-border bg-muted/30 px-2 py-1.5">
            <span className="min-w-0 truncate text-2xs text-muted-foreground" title={mediaQuery.data?.original_filename ?? mediaId}>
              {mediaQuery.data?.original_filename ?? mediaId}
            </span>
            <div className="flex gap-1">
              <Button type="button" variant="ghost" size="sm" onClick={onPick} disabled={disabled}>
                Заменить
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="text-destructive hover:text-destructive"
                aria-label="Удалить выбранный файл"
                onClick={onClear}
                disabled={disabled}
              >
                <X className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        </div>
      ) : (
        <button
          type="button"
          onClick={onPick}
          disabled={disabled}
          className="flex w-full items-center justify-center gap-2 rounded-md border border-dashed border-border px-3 py-5 text-xs text-muted-foreground hover:border-primary/40 hover:bg-muted/30 disabled:opacity-50"
        >
          <ImagePlus className="h-4 w-4" />
          {variant === "image" ? "Выбрать изображение" : "Выбрать файл"}
        </button>
      )}
    </div>
  );
}

function enumSelect(field: SchemaFieldNode): {
  value: string;
  label: string;
  id: string;
}[] {
  return field.enumValues
    .filter((option) => option.value.trim())
    .map((option) => ({
      value: option.value,
      label: option.label || option.value,
      id: option.id,
    }));
}

function ScalarControl({
  field,
  fieldPath,
  value,
  onChange,
  onPickMedia,
  disabled,
  compact,
  controlId,
  labelId,
  errorId,
  accessibleLabel,
}: {
  field: SchemaFieldNode;
  fieldPath: string;
  value: JsonValue;
  onChange: (value: JsonValue) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  compact?: boolean;
  controlId?: string;
  labelId?: string;
  errorId?: string;
  accessibleLabel?: string;
}) {
  const accessibilityProps = {
    id: controlId,
    "aria-labelledby": labelId,
    "aria-label": accessibleLabel,
    "aria-invalid": errorId ? true : undefined,
    "aria-describedby": errorId,
  };
  switch (field.type) {
    case "text":
    case "richText": {
      const text = typeof value === "string" ? value : "";
      return (
        <div>
          <Textarea
            rows={field.type === "richText" ? (compact ? 3 : 6) : 4}
            autoComplete="off"
            spellCheck={field.type !== "richText"}
            {...accessibilityProps}
            value={text}
            disabled={disabled}
            onChange={(event) => onChange(event.target.value)}
            placeholder={field.placeholder || (field.type === "richText" ? "Текст с разметкой…" : undefined)}
            className={field.type === "richText" ? "font-mono text-xs leading-6" : undefined}
          />
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 text-2xs text-muted-foreground">
            {field.type === "richText" && !compact ? (
              <p>Markdown: **жирный**, *курсив*, списки «- », ссылки [текст](url), заголовки «##».</p>
            ) : <span />}
            {field.maxLength !== undefined && (
              <p className={codePointLength(text) > field.maxLength ? "text-destructive" : undefined}>
                {codePointLength(text)}&nbsp;/&nbsp;{field.maxLength}
              </p>
            )}
          </div>
        </div>
      );
    }
    case "number":
    case "integer":
      return (
        <Input
          type="number"
          inputMode={field.type === "integer" ? "numeric" : "decimal"}
          autoComplete="off"
          {...accessibilityProps}
          className={compact ? "h-8" : "h-10"}
          step={field.type === "integer" ? 1 : field.step ?? "any"}
          min={field.min}
          max={field.max}
          value={typeof value === "number" ? value : ""}
          disabled={disabled}
          onChange={(event) => {
            const raw = event.target.value;
            if (raw === "") {
              onChange(null);
              return;
            }
            const parsed = Number(raw);
            if (!Number.isFinite(parsed)) return;
            onChange(parsed);
          }}
        />
      );
    case "boolean":
      return (
        <label className="flex min-h-9 w-fit cursor-pointer items-center gap-2">
          <Switch
            {...accessibilityProps}
            checked={value === true}
            disabled={disabled}
            onCheckedChange={(checked) => onChange(checked)}
          />
          <span className="text-xs text-muted-foreground">
            {value === true ? "Да" : "Нет"}
          </span>
        </label>
      );
    case "date":
      return (
        <Input
          type="date"
          {...accessibilityProps}
          value={typeof value === "string" ? value.slice(0, 10) : ""}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
      );
    case "dateTime":
      return (
        <Input
          type="datetime-local"
          {...accessibilityProps}
          value={isoToLocalInput(typeof value === "string" ? value : "")}
          disabled={disabled}
          onChange={(event) => {
            if (!event.target.value) {
              onChange("");
              return;
            }
            const date = new Date(event.target.value);
            onChange(Number.isNaN(date.getTime()) ? event.target.value : date.toISOString());
          }}
        />
      );
    case "enum": {
      const options = enumSelect(field);
      return (
        <Select
          value={typeof value === "string" && options.some((o) => o.value === value) ? value : ""}
          onValueChange={(next) => onChange(next)}
          disabled={disabled}
        >
          <SelectTrigger {...accessibilityProps} className={compact ? "h-8" : undefined}>
            <SelectValue placeholder="Выберите значение" />
          </SelectTrigger>
          <SelectContent>
            {options.map((option) => (
              <SelectItem key={option.id} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      );
    }
    case "media":
    case "file":
      return (
        <MediaValue
          mediaId={typeof value === "string" ? value : ""}
          fieldPath={fieldPath}
          variant={field.type === "file" ? "file" : "image"}
          disabled={disabled}
          compact={compact}
          onPick={() =>
            onPickMedia({
              label: field.label || field.key,
              single: true,
              accept: field.type === "file" ? "all" : mediaAccept(field.mediaProfile),
              fieldPath,
              apply: (media) => {
                const first = media[0];
                if (first) onChange(first.id);
              },
            })
          }
          onClear={() => onChange("")}
        />
      );
    case "string":
    default:
      return (
        <Input
          type={stringControlType(field)}
          autoComplete="off"
          {...accessibilityProps}
          className={compact ? "h-8" : undefined}
          value={typeof value === "string" ? value : ""}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
          placeholder={field.placeholder}
        />
      );
  }
}

function isoToLocalInput(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (input: number) => String(input).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// ---------------------------------------------------------------------------
// Structured editors
// ---------------------------------------------------------------------------

function ArrayTable({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
}: {
  field: SchemaFieldNode;
  value: JsonValue[];
  onChange: (next: JsonValue[]) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
}) {
  const rows = value;
  const children = field.children.filter((child) => child.key.trim());
  const canAdd = field.maxItems === undefined || rows.length < field.maxItems;
  // The published schema enforces minItems server-side; going below it would
  // make autosave fail with schema_validation_failed.
  const minRows = field.minItems ?? 0;
  const canRemove = rows.length > minRows;

  const setRow = (index: number, next: JsonObject) => {
    const copy = [...rows];
    copy[index] = next;
    onChange(copy);
  };
  const moveRow = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= rows.length) return;
    const copy = [...rows];
    [copy[index], copy[target]] = [copy[target], copy[index]];
    onChange(copy);
  };
  const removeRow = (index: number) => {
    onChange(rows.filter((_, i) => i !== index));
  };
  const duplicateRow = (index: number) => {
    if (!canAdd) return;
    const copy = [...rows];
    copy.splice(index + 1, 0, cloneOriginal(rows[index] as JsonValue));
    onChange(copy);
  };
  const addRow = () => {
    if (!canAdd || children.length === 0) return;
    const row: JsonObject = {};
    children.forEach((child) => {
      row[child.key] = starterValue(child);
    });
    onChange([...rows, row]);
  };

  const compactControlFor = (
    child: SchemaFieldNode,
    cellValue: JsonValue,
    setCell: (value: JsonValue) => void,
    cellPath: string,
    cellSchemaPath: string,
  ): ReactNode => {
    const error = errorsByPath.get(cellPath);
    const errorId = error ? `${entryFieldAnchorId(cellPath)}-error` : undefined;
    if (child.type === "media" || child.type === "file") {
      return (
        <div key={cellPath}>
          <MediaValue
            mediaId={typeof cellValue === "string" ? cellValue : ""}
            fieldPath={cellSchemaPath}
            variant={child.type === "file" ? "file" : "image"}
            disabled={disabled}
            onPick={() =>
              onPickMedia({
                label: `${field.label || field.key} · ${child.label || child.key}`,
                single: true,
                accept: child.type === "file" ? "all" : mediaAccept(child.mediaProfile),
                fieldPath: cellSchemaPath,
                apply: (media) => {
                  const first = media[0];
                  if (first) setCell(first.id);
                },
              })
            }
            onClear={() => setCell("")}
          />
          {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
        </div>
      );
    }
    if (child.type === "object") {
      return <ObjectFields field={child} value={isRecordValue(cellValue) ? cellValue : {}} onChange={setCell} onPickMedia={onPickMedia} disabled={disabled} errorsByPath={errorsByPath} path={cellPath} schemaPath={cellSchemaPath} />;
    }
    if (child.type === "array") {
      return <ArrayEditorValue field={child} value={Array.isArray(cellValue) ? cellValue : []} onChange={setCell} onPickMedia={onPickMedia} disabled={disabled} errorsByPath={errorsByPath} path={cellPath} schemaPath={cellSchemaPath} />;
    }
    return (
      <div key={cellPath}>
        <ScalarControl
          field={child}
          value={cellValue}
          onChange={setCell}
          onPickMedia={onPickMedia}
          disabled={disabled}
          compact
          fieldPath={cellSchemaPath}
          controlId={entryFieldAnchorId(cellPath)}
          errorId={errorId}
          accessibleLabel={`${field.label || field.key}, ${child.label || child.key}, строка ${Number(cellPath.slice(path.length + 1).split(".")[0]) + 1}`}
        />
        {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
      </div>
    );
  };

  if (children.length === 0) {
    return (
      <p className="rounded-md border border-dashed border-border px-3 py-3 text-xs text-muted-foreground">
        В схеме массива нет полей для строк таблицы.
      </p>
    );
  }

  return (
    <div className="space-y-2">
      <div className="overflow-x-auto rounded-md border border-border">
        <table className="w-full min-w-[36rem] border-collapse text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40 text-left">
              {children.map((child) => (
                <th
                  key={child.id}
                  id={`${entryFieldAnchorId(path)}-column-${child.id}`}
                  className="px-2 py-2 text-xs font-medium text-muted-foreground"
                >
                  {child.label || child.key}
                  {child.required && <span className="ml-0.5 text-destructive">*</span>}
                </th>
              ))}
              <th className="w-24 px-2 py-2" aria-label="Действия" />
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) => {
              const record = isRecordValue(row) ? row : {};
              return (
                <tr
                  key={index}
                  className={errorsByPath.has(`${path}.${index}`) ? "bg-destructive/5" : undefined}
                >
                  {children.map((child) => {
                    const cellPath = `${path}.${index}.${child.key}`;
                    const cell = compactControlFor(
                      child,
                      record[child.key],
                      (next) => setRow(index, { ...record, [child.key]: next }),
                      cellPath,
                      `${schemaPath}/items/${pointerPart(child.key)}`,
                    );
                    return (
                      <td
                        key={child.id}
                        headers={`${entryFieldAnchorId(path)}-column-${child.id}`}
                        className="align-top px-2 py-2"
                      >
                        {cell}
                      </td>
                    );
                  })}
                  <td className="align-top px-2 py-2">
                    <div className="flex items-center justify-end gap-0.5">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Выше"
                        disabled={disabled || index === 0}
                        onClick={() => moveRow(index, -1)}
                      >
                        <ArrowUp className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Ниже"
                        disabled={disabled || index === rows.length - 1}
                        onClick={() => moveRow(index, 1)}
                      >
                        <ArrowDown className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label="Дублировать"
                        disabled={disabled || !canAdd}
                        onClick={() => duplicateRow(index)}
                      >
                        <Copy className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={canRemove ? "Удалить" : `Минимум ${minRows} строк`}
                        className="text-destructive hover:text-destructive"
                        disabled={disabled || !canRemove}
                        onClick={() => removeRow(index)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </td>
                </tr>
              );
            })}
            {rows.length === 0 && (
              <tr>
                <td colSpan={children.length + 1} className="px-3 py-4 text-center text-xs text-muted-foreground">
                  Пока нет строк
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <div className="flex items-center justify-between gap-2">
        {!canAdd && (
          <p className="text-2xs text-muted-foreground">
            Достигнут максимум: {field.maxItems} элемент(ов)
          </p>
        )}
        {minRows > 0 && (
          <p className="text-2xs text-muted-foreground">
            Минимум строк по схеме: {minRows}
          </p>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addRow}
          disabled={disabled || !canAdd}
        >
          <Plus className="h-4 w-4" />
          Добавить строку
        </Button>
      </div>
    </div>
  );
}


function ScalarArrayList({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
}: {
  field: SchemaFieldNode;
  value: JsonValue[];
  onChange: (next: JsonValue[]) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
}) {
  const canAdd = field.maxItems === undefined || value.length < field.maxItems;
  const minItemsAllowed = field.minItems ?? 0;
  const canRemove = value.length > minItemsAllowed;
  const duplicateItem = (index: number) => {
    if (!canAdd) return;
    const copy = [...value];
    copy.splice(index + 1, 0, cloneOriginal(value[index]));
    onChange(copy);
  };
  const itemField: SchemaFieldNode = {
    ...field,
    id: `${field.id}-item`,
    key: "item",
    label: field.label,
    type: field.itemType as SchemaFieldNode["type"],
    children: [],
    required: false,
    description: "",
  };
  return (
    <div className="space-y-2">
      {value.map((item, index) => {
        const itemPath = `${path}.${index}`;
        const error = errorsByPath.get(itemPath);
        const errorId = error ? `${entryFieldAnchorId(itemPath)}-error` : undefined;
        if (field.itemType === "media" || field.itemType === "file") {
          return (
            <div key={index} className="flex items-start gap-2">
              <div className="min-w-0 flex-1">
                <MediaValue
                  mediaId={typeof item === "string" ? item : ""}
                  fieldPath={`${schemaPath}/items`}
                  variant={field.itemType === "file" ? "file" : "image"}
                  disabled={disabled}
                  compact
                  onPick={() =>
                    onPickMedia({
                      label: `${field.label || field.key} (${index + 1})`,
                      single: true,
                      accept: field.itemType === "file" ? "all" : mediaAccept(field.itemMediaProfile),
                      fieldPath: `${schemaPath}/items`,
                      apply: (media) => {
                        const first = media[0];
                        if (first) {
                          const copy = [...value];
                          copy[index] = first.id;
                          onChange(copy);
                        }
                      },
                    })
                  }
                  onClear={() => onChange(value.filter((_, i) => i !== index))}
                />
                {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="mt-1 text-destructive hover:text-destructive"
                aria-label="Удалить"
                disabled={disabled || !canRemove}
                onClick={() => onChange(value.filter((_, i) => i !== index))}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          );
        }
        return (
          <div key={index} className="flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <ScalarControl
                field={itemField}
                value={item}
                disabled={disabled}
                compact={field.itemType === "boolean"}
                fieldPath={`${schemaPath}/items`}
                onChange={(next) => {
                  const copy = [...value];
                  copy[index] = next;
                  onChange(copy);
                }}
                onPickMedia={onPickMedia}
                controlId={entryFieldAnchorId(itemPath)}
                errorId={errorId}
                accessibleLabel={`${field.label || field.key}, элемент ${index + 1}`}
              />
              {error && <p id={errorId} className="mt-1 text-2xs text-destructive">{error}</p>}
            </div>
            <div className="flex items-center gap-0.5">
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Выше"
                disabled={disabled || index === 0}
                onClick={() => {
                  const copy = [...value];
                  [copy[index - 1], copy[index]] = [copy[index], copy[index - 1]];
                  onChange(copy);
                }}
              >
                <ArrowUp className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Ниже"
                disabled={disabled || index === value.length - 1}
                onClick={() => {
                  const copy = [...value];
                  [copy[index + 1], copy[index]] = [copy[index], copy[index + 1]];
                  onChange(copy);
                }}
              >
                <ArrowDown className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Дублировать"
                disabled={disabled || !canAdd}
                onClick={() => duplicateItem(index)}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="text-destructive hover:text-destructive"
                aria-label="Удалить"
                disabled={disabled || !canRemove}
                onClick={() => onChange(value.filter((_, i) => i !== index))}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          </div>
        );
      })}
      <div className="flex items-center justify-between gap-2">
        {!canAdd && (
          <p className="text-2xs text-muted-foreground">
            Достигнут максимум: {field.maxItems} элемент(ов)
          </p>
        )}
        {minItemsAllowed > 0 && (
          <p className="text-2xs text-muted-foreground">
            Минимум элементов по схеме: {minItemsAllowed}
          </p>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled || !canAdd}
          onClick={() => {
            const copy = [...value, starterValue(itemField)];
            onChange(copy);
          }}
        >
          <Plus className="h-4 w-4" />
          Добавить элемент
        </Button>
      </div>
    </div>
  );
}

function ObjectFields({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
}: {
  field: SchemaFieldNode;
  value: JsonObject;
  onChange: (next: JsonObject) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
}) {
  const children = field.children.filter((child) => child.key.trim());
  if (children.length === 0) {
    return (
      <p className="rounded-md border border-dashed border-border px-3 py-3 text-xs text-muted-foreground">
        В схеме объекта нет вложенных полей.
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {children.map((child) => {
        const childPath = `${path}.${child.key}`;
        const childSchemaPath = `${schemaPath}/${pointerPart(child.key)}`;
        const error = errorsByPath.get(childPath);
        const labelId = `${entryFieldAnchorId(childPath)}-label`;
        const errorId = error ? `${entryFieldAnchorId(childPath)}-error` : undefined;
        return (
          <div key={child.id}>
            <div className="flex items-center justify-between gap-2">
              <Label id={labelId} className="text-xs text-muted-foreground">
                {child.label || child.key}
                {child.required && <span className="ml-0.5 text-destructive">*</span>}
              </Label>
              {child.isPrivate && (
                <Badge variant="muted" title="x-private: исключается из публичных данных">
                  <Lock />
                  private
                </Badge>
              )}
            </div>
            {child.type === "object" ? (
              <div className="mt-1.5 rounded-md border border-border border-dashed p-3">
                <ObjectFields
                  field={child}
                  value={isRecordValue(value[child.key]) ? (value[child.key] as JsonObject) : {}}
                  onChange={(next) => onChange({ ...value, [child.key]: next })}
                  onPickMedia={onPickMedia}
                  disabled={disabled}
                  errorsByPath={errorsByPath}
                  path={childPath}
                  schemaPath={childSchemaPath}
                />
              </div>
            ) : child.type === "array" ? (
              <div className="mt-1.5">
                <ArrayEditorValue
                  field={child}
                  value={Array.isArray(value[child.key]) ? (value[child.key] as JsonValue[]) : []}
                  onChange={(next) => onChange({ ...value, [child.key]: next })}
                  onPickMedia={onPickMedia}
                  disabled={disabled}
                  errorsByPath={errorsByPath}
                  path={childPath}
                  schemaPath={childSchemaPath}
                />
              </div>
            ) : (
              <div className="mt-1.5">
                <ScalarControl
                  field={child}
                  value={value[child.key]}
                  disabled={disabled}
                  onChange={(next) => onChange({ ...value, [child.key]: next })}
                  onPickMedia={onPickMedia}
                  compact={child.type === "enum" || child.type === "boolean"}
                  fieldPath={childSchemaPath}
                  labelId={labelId}
                  errorId={errorId}
                />
              </div>
            )}
            {error && <p id={errorId} className="mt-1 text-xs text-destructive">{error}</p>}
          </div>
        );
      })}
    </div>
  );
}

/** Card layout for object arrays that do not fit the compact table. */
function ArrayCards({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
}: {
  field: SchemaFieldNode;
  value: JsonValue[];
  onChange: (next: JsonValue[]) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
}) {
  const rows = value;
  const canAdd = field.maxItems === undefined || rows.length < field.maxItems;
  const minRows = field.minItems ?? 0;
  const canRemove = rows.length > minRows;

  const rowField: SchemaFieldNode = {
    ...field,
    id: `${field.id}-row`,
    key: "row",
    label: field.label,
    type: "object",
    children: field.children.filter((child) => child.key.trim()),
    description: "",
    required: false,
  };

  const setRow = (index: number, next: JsonObject) => {
    const copy = [...rows];
    copy[index] = next;
    onChange(copy);
  };
  const moveRow = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= rows.length) return;
    const copy = [...rows];
    [copy[index], copy[target]] = [copy[target], copy[index]];
    onChange(copy);
  };
  const removeRow = (index: number) => {
    onChange(rows.filter((_, i) => i !== index));
  };
  const duplicateRow = (index: number) => {
    if (!canAdd) return;
    const copy = [...rows];
    copy.splice(index + 1, 0, cloneOriginal(rows[index] as JsonValue));
    onChange(copy);
  };
  const addRow = () => {
    if (!canAdd) return;
    const row: JsonObject = {};
    rowField.children.forEach((child) => {
      row[child.key] = starterValue(child);
    });
    onChange([...rows, row]);
  };

  const rowTitle = (row: JsonValue, index: number): string => {
    const record = isRecordValue(row) ? row as JsonObject : {};
    for (const child of rowField.children) {
      if (child.type !== "string" && child.type !== "text") continue;
      const textValue = record[child.key];
      if (typeof textValue === "string" && textValue.trim()) return textValue.trim();
    }
    return `Элемент ${index + 1}`;
  };

  return (
    <div className="space-y-2">
      {rows.map((row, index) => {
        const rowPath = `${path}.${index}`;
        const rowError = errorsByPath.has(rowPath);
        const headingId = `${entryFieldAnchorId(rowPath)}-heading`;
        return (
          <section
            key={index}
            aria-labelledby={headingId}
            className={cn(
              "space-y-3 rounded-lg border p-3",
              rowError ? "border-destructive/40 bg-destructive/5" : "border-border bg-muted/20",
            )}
          >
            <header className="flex items-center gap-2">
              <h4 id={headingId} className="min-w-0 flex-1 truncate text-sm font-medium">
                {rowTitle(row, index)}
              </h4>
              {rowError && <Badge variant="destructive">Ошибки</Badge>}
              <div className="flex shrink-0 items-center gap-0.5">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Выше"
                  disabled={disabled || index === 0}
                  onClick={() => moveRow(index, -1)}
                >
                  <ArrowUp className="h-3.5 w-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Ниже"
                  disabled={disabled || index === rows.length - 1}
                  onClick={() => moveRow(index, 1)}
                >
                  <ArrowDown className="h-3.5 w-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Дублировать"
                  disabled={disabled || !canAdd}
                  onClick={() => duplicateRow(index)}
                >
                  <Copy className="h-3.5 w-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={canRemove ? "Удалить" : `Минимум ${minRows} элементов`}
                  className="text-destructive hover:text-destructive"
                  disabled={disabled || !canRemove}
                  onClick={() => removeRow(index)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </header>
            <ObjectFields
              field={rowField}
              value={isRecordValue(row) ? row as JsonObject : {}}
              onChange={(next) => setRow(index, next)}
              onPickMedia={onPickMedia}
              disabled={disabled}
              errorsByPath={errorsByPath}
              path={rowPath}
              schemaPath={`${schemaPath}/items`}
            />
          </section>
        );
      })}
      {rows.length === 0 && (
        <p className="rounded-md border border-dashed border-border px-3 py-3 text-center text-xs text-muted-foreground">
          Пока нет элементов
        </p>
      )}
      <div className="flex items-center justify-between gap-2">
        {!canAdd && (
          <p className="text-2xs text-muted-foreground">
            Достигнут максимум: {field.maxItems} элемент(ов)
          </p>
        )}
        {minRows > 0 && (
          <p className="text-2xs text-muted-foreground">
            Минимум элементов по схеме: {minRows}
          </p>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addRow}
          disabled={disabled || !canAdd}
        >
          <Plus className="h-4 w-4" />
          Добавить элемент
        </Button>
      </div>
    </div>
  );
}

function ArrayEditorValue({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
}: {
  field: SchemaFieldNode;
  value: JsonValue[];
  onChange: (next: JsonValue[]) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
}) {
  if (field.itemType === "object") {
    if (arrayUsesCards(field)) {
      return (
        <ArrayCards
          field={field}
          value={value}
          onChange={onChange}
          onPickMedia={onPickMedia}
          disabled={disabled}
          errorsByPath={errorsByPath}
          path={path}
          schemaPath={schemaPath}
        />
      );
    }
    return (
      <ArrayTable
        field={field}
        value={value}
        onChange={onChange}
        onPickMedia={onPickMedia}
        disabled={disabled}
        errorsByPath={errorsByPath}
        path={path}
        schemaPath={schemaPath}
      />
    );
  }
  return (
    <ScalarArrayList
      field={field}
      value={value}
      onChange={onChange}
      onPickMedia={onPickMedia}
      disabled={disabled}
      errorsByPath={errorsByPath}
      path={path}
      schemaPath={schemaPath}
    />
  );
}

// ---------------------------------------------------------------------------
// Main form
// ---------------------------------------------------------------------------

export function EntryFieldsForm({
  fields,
  metadata,
  onChange,
  disabled,
  issues,
  featureKey,
  buildDigest,
  schemaKind,
  compact = false,
}: {
  /** Visible schema fields (without the legacy blocks key). */
  fields: SchemaFieldNode[];
  metadata: JsonObject;
  onChange: (key: string, value: JsonValue) => void;
  disabled?: boolean;
  issues: EntryFormIssue[];
  featureKey?: string;
  buildDigest?: string;
  schemaKind?: "content" | "settings";
  compact?: boolean;
}) {
  const scope = useMemo(
    () => (featureKey && buildDigest && schemaKind ? { featureKey, buildDigest, schemaKind } : null),
    [buildDigest, featureKey, schemaKind],
  );
  const [mediaTarget, setMediaTarget] = useState<MediaPickTarget | null>(null);
  const openMediaPick = useCallback(
    (target: MediaPickTarget) => setMediaTarget({
      ...target,
      preparation: scope ? { ...scope, fieldPath: target.fieldPath } : undefined,
    }),
    [scope],
  );

  const errorsByPath = useMemo(() => {
    const map = new Map<string, string>();
    issues.forEach((issue) => {
      if (!map.has(issue.path)) map.set(issue.path, issue.message);
    });
    return map;
  }, [issues]);

  if (fields.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-border p-4 text-sm text-muted-foreground">
        В схеме нет полей. Данные для этой записи задаются иначе.
      </p>
    );
  }

  return (
    <MediaRenditionScope.Provider value={scope}>
    <div className="space-y-0">
      {fields.map((field) => {
        const anchorId = entryFieldAnchorId(field.key);
        const error = errorsByPath.get(field.key);
        const errorId = error ? `${anchorId}-error` : undefined;
        const value = metadata[field.key];
        return (
          <FieldCard
            key={field.id}
            field={field}
            anchorId={anchorId}
            error={error}
            compact={compact}

          >
            {field.type === "object" ? (
              <ObjectFields
                field={field}
                value={isRecordValue(value) ? value : {}}
                onChange={(next) => onChange(field.key, next)}
                onPickMedia={openMediaPick}
                disabled={disabled}
                errorsByPath={errorsByPath}
                path={field.key}
                schemaPath={`/${pointerPart(field.key)}`}
              />
            ) : field.type === "array" ? (
              <ArrayEditorValue
                field={field}
                value={Array.isArray(value) ? value : []}
                onChange={(next) => onChange(field.key, next)}
                onPickMedia={openMediaPick}
                disabled={disabled}
                errorsByPath={errorsByPath}
                path={field.key}
                schemaPath={`/${pointerPart(field.key)}`}
              />
            ) : (
              <ScalarControl
                field={field}
                value={value as JsonValue}
                disabled={disabled}
                compact={compact}
                onChange={(next) => onChange(field.key, next)}
                onPickMedia={openMediaPick}
                fieldPath={`/${pointerPart(field.key)}`}
                controlId={`${anchorId}-control`}
                labelId={`${anchorId}-label`}
                errorId={errorId}
              />
            )}
          </FieldCard>
        );
      })}

      <MediaPickerDialog
        open={mediaTarget !== null}
        onClose={() => setMediaTarget(null)}
        onSelect={(media: Media[]) => {
          const target = mediaTarget;
          setMediaTarget(null);
          if (!target) return;
          const apply = target.single ? media.slice(0, 1) : media;
          target.apply(apply);
        }}
        mode={mediaTarget?.single === false ? "multiple" : "single"}
        accept={mediaTarget?.accept ?? "image"}
        title={mediaTarget ? `Выбрать: ${mediaTarget.label}` : "Выбрать файл"}
        preparation={mediaTarget?.preparation}
      />


    </div>
    </MediaRenditionScope.Provider>
  );
}


