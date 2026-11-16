import { useEffect, useRef, type FocusEvent } from "react";
import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";

export function pointerPart(value: string): string {
  return value.replace(/~/g, "~0").replace(/\//g, "~1");
}

export function mediaAccept(profile: JsonObject | undefined): "image" | "raster-image" | "video" | "all" {
  if (profile?.accept === "video") return "video";
  if (profile?.accept === "file") return "all";
  if (profile?.accept === "raster-image") return "raster-image";
  return "image";
}

export function isRecordValue(value: JsonValue | undefined): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function arrayItemKey(value: JsonValue, index: number, fields: SchemaFieldNode[]): string {
  if (isRecordValue(value)) {
    const identityField = fields.find((field) => field.engineRecordID);
    const identity = identityField ? value[identityField.key] : undefined;
    if (typeof identity === "string" && identity.trim()) return `engine:${identity}`;
  }
  return `index:${index}`;
}

export type ArrayMoveAction = "Выше" | "Ниже";

export function useArrayRowFocusRestoration(disabled: boolean | undefined, rows: JsonValue[]) {
  const rootRef = useRef<HTMLDivElement>(null);
  const pendingFocusRef = useRef<{ row: string; action: ArrayMoveAction; sawDisabled: boolean } | null>(null);

  const rememberRowFocus = (row: string, action: ArrayMoveAction) => {
    pendingFocusRef.current = { row, action, sawDisabled: false };
  };

  const onFocusCapture = (event: FocusEvent<HTMLDivElement>) => {
    const pending = pendingFocusRef.current;
    if (!pending) return;
    const target = event.target as HTMLElement;
    const row = target.closest<HTMLElement>("[data-array-item-index]");
    if (row?.dataset.arrayItemIndex !== pending.row || target.getAttribute("aria-label") !== pending.action) {
      pendingFocusRef.current = null;
    }
  };

  useEffect(() => {
    const pending = pendingFocusRef.current;
    if (!pending) return;
    if (disabled) {
      pending.sawDisabled = true;
      return;
    }

    const root = rootRef.current;
    const row = [...(root?.querySelectorAll<HTMLElement>("[data-array-item-index]") ?? [])]
      .find((element) => element.dataset.arrayItemIndex === pending.row);
    const target = [...(row?.querySelectorAll<HTMLButtonElement>("button[aria-label]") ?? [])]
      .find((button) => button.getAttribute("aria-label") === pending.action && !button.disabled);
    if (!target) return;

    const active = document.activeElement;
    const focusWasLost = active === document.body || active === document.documentElement;
    if (focusWasLost) target.focus();
    if (pending.sawDisabled) pendingFocusRef.current = null;
  }, [disabled, rows]);

  return { rootRef, rememberRowFocus, onFocusCapture };
}

export function cloneOriginal<T extends JsonValue>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

/** Clear a media selection without changing the array's item count. */
export function clearMediaArraySelection(values: JsonValue[], index: number): JsonValue[] {
  if (index < 0 || index >= values.length || typeof values[index] !== "string") return values;
  const next = [...values];
  next[index] = "";
  return next;
}

/**
 * Choose a card layout when object rows would not fit a compact table:
 * long text controls, nested structures, media pickers, or many columns.
 */
export function arrayUsesCards(field: SchemaFieldNode): boolean {
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

export function stringControlType(field: SchemaFieldNode): string {
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

export function entryFieldAnchorId(path: string): string {
  return `entry-field-${encodeURIComponent(path)}`;
}

export function isoToLocalInput(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (input: number) => String(input).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

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

export function codePointLength(value: string): number {
  return [...value].length;
}
