import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Lock } from "lucide-react";
import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { entryFieldAnchorId, isRecordValue, pointerPart } from "./helpers";
import type { MediaPickTarget } from "./types";
import type { FieldValueRenderer } from "./field-value-types";

type RenderFieldValue = FieldValueRenderer;
type ObjectFieldsProps = {
  field: SchemaFieldNode;
  value: JsonObject;
  onChange: (next: JsonObject) => void;
  onPickMedia: (target: MediaPickTarget) => void;
  disabled?: boolean;
  errorsByPath: Map<string, string>;
  path: string;
  schemaPath: string;
  renderFieldValue: RenderFieldValue;
};

export function ObjectFields({
  field,
  value,
  onChange,
  onPickMedia,
  disabled,
  errorsByPath,
  path,
  schemaPath,
  renderFieldValue: RenderFieldValue,
}: ObjectFieldsProps) {
  const children = field.children.filter((child) => child.key.trim() && !child.engineRecordID);
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
              {!child.required && value[child.key] !== undefined && (
                <Button type="button" variant="ghost" size="sm" disabled={disabled}
                  aria-label={`Очистить ${child.label || child.key}`}
                  onClick={() => { const next = { ...value }; delete next[child.key]; onChange(next); }}>
                  Очистить
                </Button>
              )}
              {child.isPrivate && (
                <Badge variant="muted" title="x-private: исключается из публичных данных">
                  <Lock />
                  private
                </Badge>
              )}
            </div>
            {child.type === "object" ? (
              <div className="mt-1.5 rounded-md border border-border border-dashed p-3">
                <RenderFieldValue
                  field={child}
                  value={isRecordValue(value[child.key]) ? value[child.key] as JsonObject : {}}
                  onChange={(next) => onChange({ ...value, [child.key]: next })}
                  onPickMedia={onPickMedia}
                  disabled={disabled}
                  compact={false}
                  errorsByPath={errorsByPath}
                  path={childPath}
                  schemaPath={childSchemaPath}
                  labelId={labelId}
                  errorId={errorId}
                />
              </div>
            ) : child.type === "array" ? (
              <div className="mt-1.5">
                <RenderFieldValue
                  field={child}
                  value={Array.isArray(value[child.key]) ? value[child.key] as JsonValue[] : []}
                  onChange={(next) => onChange({ ...value, [child.key]: next })}
                  onPickMedia={onPickMedia}
                  disabled={disabled}
                  compact={false}
                  errorsByPath={errorsByPath}
                  path={childPath}
                  schemaPath={childSchemaPath}
                  labelId={labelId}
                  errorId={errorId}
                />
              </div>
            ) : (
              <div className="mt-1.5">
                <RenderFieldValue
                  field={child}
                  value={value[child.key]}
                  disabled={disabled}
                  onChange={(next) => onChange({ ...value, [child.key]: next })}
                  onPickMedia={onPickMedia}
                  compact={child.type === "enum" || child.type === "boolean"}
                  errorsByPath={errorsByPath}
                  path={childPath}
                  schemaPath={childSchemaPath}
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
