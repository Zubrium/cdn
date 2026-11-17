import type { ReactNode } from "react";
import type { JsonValue } from "@/types";
import { cn } from "@/lib/utils";

function Primitive({ value }: { value: string | number | boolean | null }) {
  if (value === null) return <span className="text-muted-foreground">null</span>;
  if (typeof value === "boolean") {
    return <span className="font-mono text-info">{String(value)}</span>;
  }
  if (typeof value === "number") {
    return <span className="font-mono text-primary">{value}</span>;
  }
  if (!value) return <span className="text-muted-foreground">""</span>;
  return <span className="whitespace-pre-wrap break-words">{value}</span>;
}

function renderValue(value: JsonValue, depth = 0): ReactNode {
  if (depth > 8) {
    return <span className="text-muted-foreground">…</span>;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) return <span className="text-muted-foreground">[]</span>;
    return (
      <ol className="space-y-1.5">
        {value.map((item, index) => (
          <li key={index} className="flex gap-2">
            <span className="mt-0.5 select-none text-2xs text-muted-foreground">
              {index + 1}.
            </span>
            <div className="min-w-0 flex-1">{renderValue(item, depth + 1)}</div>
          </li>
        ))}
      </ol>
    );
  }
  if (typeof value === "object" && value !== null) {
    const entries = Object.entries(value);
    if (entries.length === 0) return <span className="text-muted-foreground">{"{}"}</span>;
    return (
      <dl className="space-y-2">
        {entries.map(([key, child]) => (
          <div key={key} className="grid grid-cols-[minmax(7rem,30%)_1fr] gap-2">
            <dt className="break-words text-xs font-medium text-muted-foreground">{key}</dt>
            <dd className="min-w-0 text-sm">{renderValue(child, depth + 1)}</dd>
          </div>
        ))}
      </dl>
    );
  }
  return <Primitive value={value} />;
}

export function JsonView({
  value,
  className,
  empty = "Нет данных",
}: {
  value: JsonValue | undefined;
  className?: string;
  empty?: string;
}) {
  if (value === undefined) {
    return <span className="text-sm text-muted-foreground">{empty}</span>;
  }
  return <div className={cn("text-sm leading-6", className)}>{renderValue(value)}</div>;
}
