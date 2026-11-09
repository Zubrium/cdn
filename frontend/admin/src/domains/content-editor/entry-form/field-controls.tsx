import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { File as FileIcon, ImagePlus, Lock, X } from "lucide-react";
import type { JsonValue, MediaRenditionContext } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { api, publicMediaPath } from "@/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input, Label, Textarea } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/misc";
import { Badge } from "@/components/ui/badge";
import { queryKeys } from "@/lib/query-keys";
import type { MediaPickTarget } from "./types";
import { codePointLength, mediaAccept, stringControlType, isoToLocalInput } from "./helpers";

export function FieldCard({
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

export function MediaValue({
  mediaId,
  variant = "image",
  onPick,
  onClear,
  disabled,
  compact,
}: {
  mediaId: string;
  variant?: "image" | "file";
  onPick: () => void;
  onClear: () => void;
  disabled?: boolean;
  compact?: boolean;
}) {
  const mediaQuery = useQuery({
    queryKey: queryKeys.media.detail(mediaId),
    queryFn: ({ signal }) => api.media.get(mediaId, signal),
    enabled: Boolean(mediaId),
    staleTime: 5 * 60 * 1000,
  });
  return (
    <div className="space-y-2">
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

export function ScalarControl({
  field,
  preparations,
  acceptOverride,
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
  mediaLabel,
}: {
  field: SchemaFieldNode;
  preparations?: MediaRenditionContext[];
  acceptOverride?: MediaPickTarget["accept"];
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
  mediaLabel?: string;
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
          variant={field.type === "file" ? "file" : "image"}
          disabled={disabled}
          compact={compact}
          onPick={() =>
            onPickMedia({
              label: mediaLabel || field.label || field.key,
              single: true,
              accept: field.type === "file" ? "all" : acceptOverride ?? mediaAccept(field.mediaProfile),
              preparations,
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
