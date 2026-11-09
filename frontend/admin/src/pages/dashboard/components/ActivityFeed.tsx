import { FileText, Image, Inbox, Layers } from "lucide-react";
import { Link } from "react-router-dom";
import type { OverviewActivity } from "@/types";
import { formatDateTime } from "@/lib/format";

const activityMeta: Record<
  OverviewActivity["kind"],
  { icon: typeof Layers; label: string; to: string }
> = {
  feature: { icon: Layers, label: "Сборка", to: "/site/registry" },
  content: { icon: FileText, label: "Данные модели", to: "/content" },
  submission: { icon: Inbox, label: "Заявка", to: "/submissions" },
  media: { icon: Image, label: "Файлы", to: "/media" },
};

function activityTitle(item: OverviewActivity): string {
  if (item.kind === "feature" && item.title.startsWith("Feature:")) {
    return `Сборка:${item.title.slice("Feature:".length)}`;
  }
  return item.title;
}

export function ActivityFeed({ items }: { items: OverviewActivity[] }) {
  return (
    <section className="min-w-0 rounded-lg border border-border bg-card" aria-labelledby="activity-title">
      <div className="border-b border-border px-4 py-3 sm:px-5">
        <h2 id="activity-title" className="text-sm font-semibold">Последняя активность</h2>
        <p className="mt-0.5 text-xs text-muted-foreground">Реальные события из API сводки</p>
      </div>
      {items.length === 0 ? (
        <p className="px-4 py-6 text-sm text-muted-foreground sm:px-5">Активности пока нет.</p>
      ) : (
        <ul className="divide-y divide-border">
          {items.slice(0, 12).map((item, index) => {
            const meta = activityMeta[item.kind];
            const Icon = meta.icon;
            return (
              <li key={`${item.kind}-${item.occurred_at}-${index}`}>
                <Link
                  to={meta.to}
                  className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/40 sm:px-5"
                >
                  <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-border bg-muted/40">
                    <Icon className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm">{activityTitle(item)}</span>
                    <span className="mt-0.5 block text-xs text-muted-foreground">
                      {meta.label}
                      {item.feature_key ? <> · <code>{item.feature_key}</code></> : null}
                      {" · "}{formatDateTime(item.occurred_at)}
                    </span>
                  </span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
