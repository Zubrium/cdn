import type { ReactNode } from "react";

/**
 * Minimal markdown renderer for the admin preview of richText fields.
 * Supports: headings, paragraphs, bold/italic/inline-code, links,
 * unordered/ordered lists. Never touches HTML — output is React nodes.
 */

const INLINE_PATTERN =
  /(\*\*[^*]+\*\*|\*[^*]+\*|`[^`]+`)|\[([^\]]+)\]\(([^)]+)\)/g;

function renderInline(text: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  let key = 0;
  INLINE_PATTERN.lastIndex = 0;
  while ((match = INLINE_PATTERN.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }
    const token = match[0];
    if (token.startsWith("**")) {
      nodes.push(<strong key={key++}>{token.slice(2, -2)}</strong>);
    } else if (token.startsWith("`")) {
      nodes.push(<code key={key++}>{token.slice(1, -1)}</code>);
    } else if (token.startsWith("*")) {
      nodes.push(<em key={key++}>{token.slice(1, -1)}</em>);
    } else if (match[2] && match[3]) {
      nodes.push(
        <a
          key={key++}
          href={match[3]}
          target="_blank"
          rel="noreferrer noopener"
          className="underline hover:text-primary"
        >
          {match[2]}
        </a>,
      );
    }
    lastIndex = INLINE_PATTERN.lastIndex;
  }
  if (lastIndex < text.length) nodes.push(text.slice(lastIndex));
  return nodes;
}

type Line =
  | { kind: "heading"; level: number; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "unordered"; items: string[] }
  | { kind: "ordered"; items: string[] };

function parseLines(source: string): Line[] {
  const out: Line[] = [];
  let list: { kind: "unordered" | "ordered"; items: string[] } | null = null;
  const flush = () => {
    if (list) {
      out.push(list);
      list = null;
    }
  };
  source.split("\n").forEach((raw) => {
    const line = raw.trimEnd();
    if (!line.trim()) {
      flush();
      return;
    }
    const heading = /^(#{1,4})\s+(.*)$/.exec(line);
    if (heading) {
      flush();
      out.push({
        kind: "heading",
        level: heading[1].length,
        text: heading[2],
      });
      return;
    }
    const unordered = /^[-*]\s+(.*)$/.exec(line);
    if (unordered) {
      if (list?.kind !== "unordered") {
        flush();
        list = { kind: "unordered", items: [] };
      }
      list.items.push(unordered[1]);
      return;
    }
    const ordered = /^\d+[.)]\s+(.*)$/.exec(line);
    if (ordered) {
      if (list?.kind !== "ordered") {
        flush();
        list = { kind: "ordered", items: [] };
      }
      list.items.push(ordered[1]);
      return;
    }
    flush();
    out.push({ kind: "paragraph", text: line });
  });
  flush();
  return out;
}

export function MarkdownText({ source }: { source: string }) {
  const lines = parseLines(source ?? "");
  if (lines.length === 0) return null;
  const headingClass = "font-semibold leading-tight";
  return (
    <div className="space-y-2">
      {lines.map((line, index) => {
        switch (line.kind) {
          case "heading": {
            const sizes: Record<number, string> = {
              1: "text-xl",
              2: "text-lg",
              3: "text-base",
              4: "text-sm",
            };
            return (
              <p
                key={index}
                className={`${headingClass} ${sizes[line.level] ?? "text-sm"}`}
              >
                {renderInline(line.text)}
              </p>
            );
          }
          case "unordered":
            return (
              <ul key={index} className="list-disc pl-5">
                {line.items.map((item, i) => (
                  <li key={i}>{renderInline(item)}</li>
                ))}
              </ul>
            );
          case "ordered":
            return (
              <ol key={index} className="list-decimal pl-5">
                {line.items.map((item, i) => (
                  <li key={i}>{renderInline(item)}</li>
                ))}
              </ol>
            );
          case "paragraph":
          default:
            return <p key={index}>{renderInline(line.text)}</p>;
        }
      })}
    </div>
  );
}


