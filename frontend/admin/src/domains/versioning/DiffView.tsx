import { Minus, Plus, RefreshCw } from "lucide-react";
import type { DiffEntry } from "./diff";
import { diffSummary } from "./diff";
import { Badge } from "@/components/ui/badge";

const kindConfig = {
  added: { label: "Добавлено", icon: Plus, className: "text-success" },
  removed: { label: "Удалено", icon: Minus, className: "text-destructive" },
  changed: { label: "Изменено", icon: RefreshCw, className: "text-warning" },
} as const;

export function DiffSummaryBadges({ entries }: { entries: DiffEntry[] }) {
  const summary = diffSummary(entries);
  return (
    <div className="flex flex-wrap gap-1.5">
      <Badge variant="success">+{summary.added}</Badge>
      <Badge variant="destructive">−{summary.removed}</Badge>
      <Badge variant="warning">~{summary.changed}</Badge>
    </div>
  );
}

export function DiffList({
  entries,
  emptyText = "Различий не найдено",
}: {
  entries: DiffEntry[];
  emptyText?: string;
}) {
  if (entries.length === 0) {
    return (
      <p className="rounded-md border border-border bg-muted/30 px-3 py-4 text-sm text-muted-foreground">
        {emptyText}
      </p>
    );
  }
  return (
    <ul className="divide-y divide-border rounded-md border border-border">
      {entries.map((entry, index) => {
        const config = kindConfig[entry.kind];
        const Icon = config.icon;
        return (
          <li key={`${entry.path}-${entry.kind}-${index}`} className="px-3 py-2.5">
            <div className="flex items-start gap-2">
              <span className={`mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded border border-border ${config.className}`}>
                <Icon className="h-3 w-3" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="flex flex-wrap items-center gap-2">
                  <code className="break-all text-xs font-medium">{entry.path || "root"}</code>
                  <span className={`text-2xs ${config.className}`}>{config.label}</span>
                </p>
                <div className="mt-1 grid gap-1 text-2xs leading-5 sm:grid-cols-2">
                  {entry.kind !== "added" && (
                    <div className="rounded bg-destructive/5 px-2 py-1 text-muted-foreground">
                      <span className="font-medium text-destructive">Было: </span>
                      <span className="break-words">{entry.before ?? "—"}</span>
                    </div>
                  )}
                  {entry.kind !== "removed" && (
                    <div className="rounded bg-success/5 px-2 py-1 text-muted-foreground">
                      <span className="font-medium text-success">Стало: </span>
                      <span className="break-words">{entry.after ?? "—"}</span>
                    </div>
                  )}
                </div>
              </div>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
