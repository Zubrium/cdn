import { publicMediaPath } from "@/api";
import type { JsonObject, JsonValue } from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { deriveDocumentTitle } from "./blocks";
import { entryTitleField } from "@/lib/json";
import type { ContentBlock } from "./types";
import { MarkdownText } from "./MarkdownText";

function BlockPreview({ block }: { block: ContentBlock }) {
  switch (block.type) {
    case "paragraph":
      return <p className="text-sm leading-6">{block.data.text}</p>;
    case "heading":
      return <p className="text-lg font-semibold"> {block.data.text} </p>;
    case "bulletedList":
      return (
        <ul className="list-disc pl-5 text-sm">
          {block.data.items.map((item, i) => (
            <li key={i}>{item}</li>
          ))}
        </ul>
      );
    case "numberedList":
      return (
        <ol className="list-decimal pl-5 text-sm">
          {block.data.items.map((item, i) => (
            <li key={i}>{item}</li>
          ))}
        </ol>
      );
    case "quote":
      return (
        <figure className="border-l-2 border-primary/40 pl-4">
          <blockquote className="text-sm leading-6">
            {block.data.text}
          </blockquote>
          {block.data.attribution ?? null}
        </figure>
      );
    case "image":
      return (
        <figure>
          <img src={publicMediaPath(block.data.mediaId)} alt="" />
          {block.data.caption ? <figcaption>{block.data.caption}</figcaption> : null}
        </figure>
      );
    case "video":
      return (
        <figure>
          <img src={publicMediaPath(block.data.mediaId)} alt="" />
        </figure>
      );
    case "gallery":
      return (
        <div className="grid grid-cols-2 gap-2">
          {block.data.mediaIds.map((id) => (
            <img key={id} src={publicMediaPath(id)} alt="" />
          ))}
        </div>
      );
    default:
      return null;
  }
}

/**
 * Schema-driven article preview: the title is derived from the schema, the
 * values are rendered with human-friendly controls (markdown renderer for
 * richText, images for media, tables for arrays). Legacy block data renders
 * below when present.
 */
export function ContentPreview({
  metadata,
  blocks,
  fields,
  width,
}: {
  metadata: JsonObject;
  blocks: ContentBlock[];
  fields: SchemaFieldNode[];
  width: "desktop" | "tablet" | "mobile";
}) {
  const title = deriveDocumentTitle(metadata, blocks, fields);
  // Hide the field that already renders as the H1 title (schema-aware, same
  // pick as the editor's title card).
  const titleFieldKey = entryTitleField(fields)?.key;
  const metaFields = fields.filter((field) => {
    if (field.type === "blocks" || field.key === titleFieldKey) return false;
    if (!field.key.trim()) return false;
    const value = metadata[field.key];
    if (value === undefined || value === null || value === "") return false;
    if (Array.isArray(value) && value.length === 0) return false;
    return true;
  });

  const widthClass =
    width === "mobile"
      ? "max-w-sm"
      : width === "tablet"
        ? "max-w-2xl"
        : "max-w-3xl";

  return (
    <div className={`${widthClass} mx-auto w-full`}>
      <article className="cms-prose min-h-[30rem] rounded-md border border-border bg-card px-5 py-8 sm:px-10">
        <h1>{title}</h1>
        {metaFields.map((field) => (
          <FieldPreview key={field.id} field={field} value={metadata[field.key]} />
        ))}
        {blocks.length > 0 && (
          <div className="mt-6 border-t border-border pt-4">
            <p className="mb-3 text-2xs uppercase tracking-wide text-muted-foreground">
              Блочные данные (legacy)
            </p>
            <div className="space-y-3">
              {blocks.map((block) => (
                <BlockPreview key={block.id} block={block} />
              ))}
            </div>
          </div>
        )}
      </article>
    </div>
  );
}

function FieldPreview({
  field,
  value,
}: {
  field: SchemaFieldNode;
  value: JsonValue | undefined;
}) {
  const label = (
    <p className="mb-1 text-2xs uppercase tracking-wide text-muted-foreground">
      {field.label || field.key}
    </p>
  );

  if (typeof value === "string" && field.type === "media") {
    return (
      <figure className="mb-4">
        <img src={publicMediaPath(value)} alt="" className="max-h-64 rounded-md object-cover" />
        {label}
      </figure>
    );
  }
  if (typeof value === "string" && field.type === "file" && value) {
    return <p className="mb-4">{label}<a className="text-primary underline" href={publicMediaPath(value)} target="_blank" rel="noreferrer">Открыть файл</a></p>;
  }
  if (field.type === "richText" && typeof value === "string" && value.trim()) {
    return (
      <div className="mb-4">
        {label}
        <MarkdownText source={value} />
      </div>
    );
  }
  if (Array.isArray(value)) {
    return (
      <div className="mb-4">
        {label}
        {field.itemType === "object" ? (
          <table className="w-full border-collapse text-xs">
            <thead>
              <tr>
                {field.children
                  .filter((child) => child.key.trim())
                  .map((child) => (
                    <th
                      key={child.id}
                      className="border-b border-border px-2 py-1.5 text-left font-medium text-muted-foreground"
                    >
                      {child.label || child.key}
                    </th>
                  ))}
              </tr>
            </thead>
            <tbody>
              {value.map((row, index) => (
                <tr key={index} className="border-b border-border/60 last:border-none">
                  {field.children
                    .filter((child) => child.key.trim())
                    .map((child) => (
                      <td key={child.id} className="px-2 py-1.5">
                        <CellPreview field={child} value={isRow(row) ? row[child.key] : undefined} />
                      </td>
                    ))}
                </tr>
              ))}
            </tbody>
          </table>
        ) : field.itemType === "media" ? (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            {value.map((item, index) => (
              <img
                key={index}
                src={publicMediaPath(String(item))}
                alt=""
                className="max-h-40 w-full rounded-md border border-border object-cover"
              />
            ))}
          </div>
        ) : field.itemType === "file" ? (
          <ul className="list-disc pl-5 text-sm">
            {value.map((item, index) => <li key={index}><a className="text-primary underline" href={publicMediaPath(String(item))} target="_blank" rel="noreferrer">Открыть файл</a></li>)}
          </ul>
        ) : (
          <ul className="list-disc pl-5 text-sm">
            {value.map((item, index) => (
              <li key={index}>
                <PlainValue value={item} />
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  }
  if (typeof value === "object" && value !== null) {
    return (
      <div className="mb-4 rounded-md border border-border bg-muted/20 p-3">
        {label}
        <dl className="grid gap-2 text-xs sm:grid-cols-2">
          {field.children
            .filter((child) => child.key.trim())
            .map((child) => {
              const childValue = (value as JsonObject)[child.key];
              return (
                <div key={child.id}>
                  <dt className="text-muted-foreground">{child.label || child.key}</dt>
                  <dd><CellPreview field={child} value={childValue} /></dd>
                </div>
              );
            })}
        </dl>
      </div>
    );
  }
  if (field.type === "boolean") {
    return (
      <p className="mb-4 text-sm">
        {label}
        {value === true ? "да" : "нет"}
      </p>
    );
  }
  return (
    <p className={`mb-4 text-sm ${field.type === "text" ? "whitespace-pre-line" : ""}`}>
      {label}
      <PlainValue value={value} />
    </p>
  );
}

function CellPreview({
  field,
  value,
}: {
  field: SchemaFieldNode;
  value: JsonValue | undefined;
}) {
  if (typeof value === "string" && field.type === "media" && value) {
    return <img src={publicMediaPath(value)} alt="" className="max-h-12 rounded" />;
  }
  if (typeof value === "string" && field.type === "file" && value) {
    return <a className="text-primary underline" href={publicMediaPath(value)} target="_blank" rel="noreferrer">Файл</a>;
  }
  if (field.type === "richText" && typeof value === "string") {
    return <MarkdownText source={value} />;
  }
  return <PlainValue value={value} />;
}

function PlainValue({ value }: { value: JsonValue | undefined }) {
  if (value === undefined || value === null || value === "") {
    return <span className="text-muted-foreground">—</span>;
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return <span>{String(value)}</span>;
  }
  return <code className="text-2xs">{JSON.stringify(value)}</code>;
}

function isRow(value: JsonValue): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
