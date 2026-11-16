import type { JsonObject, JsonValue, Media, MediaRenditionContext } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";

export interface EntryFormIssue {
  /** Dotted path, e.g. "gallery.0.caption" or "title". */
  path: string;
  message: string;
}

export interface MediaPickTarget {
  label: string;
  single: boolean;
  accept?: "image" | "raster-image" | "video" | "all";
  preparations?: MediaRenditionContext[];
  fieldPath: string;
  apply: (media: Media[]) => void;
}

export interface EntryFieldsFormProps {
  /** Visible schema fields (without the legacy blocks key). */
  fields: SchemaFieldNode[];
  metadata: JsonObject;
  onChange: (key: string, value: JsonValue) => void;
  onRemove?: (key: string) => void;
  disabled?: boolean;
  issues: EntryFormIssue[];
  /** JSON Pointer prefix for fields rendered outside the entity root. */
  schemaPathPrefix?: string;
  /** Unique UI path prefix when the same schema is rendered more than once. */
  anchorPathPrefix?: string;
  compact?: boolean;
  mediaPreparationsByFieldId?: Record<string, MediaRenditionContext[]>;
  mediaAcceptByFieldId?: Record<string, MediaPickTarget["accept"]>;
}
