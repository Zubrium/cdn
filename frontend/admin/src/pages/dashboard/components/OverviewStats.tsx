import { ArrowRight, CircleDashed, Inbox, Layers, Image } from "lucide-react";
import { Link } from "react-router-dom";
import type { OverviewCounts } from "@/types";

const statDefinitions: Array<{
  key: keyof OverviewCounts;
  label: string;
  description: string;
  icon: typeof Layers;
  to: string;
}> = [
  {
    key: "features_active",
    label: "Активные фичи",
    description: "Сборки, зарегистрированные в движке",
    icon: Layers,
    to: "/content",
  },
  {
    key: "draft_model_snapshots",
    label: "Снапшоты с черновиком",
    description: "Черновики записей модели, ожидающие публикации",
    icon: CircleDashed,
    to: "/content",
  },
  {
    key: "new_submissions",
    label: "Новые заявки",
    description: "Ожидают обработки",
    icon: Inbox,
    to: "/submissions?status=new",
  },
  {
    key: "media_ready",
    label: "Готовые медиа",
    description: "Доступны в медиатеке",
    icon: Image,
    to: "/media?status=ready",
  },
];

export function OverviewStats({ counts }: { counts: OverviewCounts }) {
  return (
    <section
      aria-label="Операционные показатели"
      className="grid gap-x-5 gap-y-4 border-y border-border py-4 sm:grid-cols-2 xl:grid-cols-4"
    >
      {statDefinitions.map((stat) => {
        const Icon = stat.icon;
        return (
          <Link
            key={stat.key}
            to={stat.to}
            className="group rounded-sm transition-colors hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                <Icon className="h-3.5 w-3.5" aria-hidden="true" />
                {stat.label}
              </div>
              <ArrowRight className="h-4 w-4 text-muted-foreground/50 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
            </div>
            <p className="mt-2 text-2xl font-semibold tabular-nums">
              {counts[stat.key]}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">{stat.description}</p>
          </Link>
        );
      })}
    </section>
  );
}
