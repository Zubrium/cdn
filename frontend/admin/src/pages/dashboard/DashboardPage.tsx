import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  ArrowRight,
  FileText,
  Layers,
  Plus,
  Sparkles,
} from "lucide-react";
import { api, ApiError } from "@/api";
import { useAuth } from "@/app/auth";
import { queryKeys } from "@/lib/query-keys";
import { pluralRu } from "@/lib/format";
import {
  EmptyState,
  ErrorState,
  PageHeader,
  useDocumentTitle,
} from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/misc";
import { SiteStatusSection } from "@/pages/dashboard/SiteStatusCard";
import { OverviewStats } from "@/pages/dashboard/components/OverviewStats";
import { AttentionQueue } from "@/pages/dashboard/components/AttentionQueue";
import { ActivityFeed } from "@/pages/dashboard/components/ActivityFeed";

function OverviewSkeleton() {
  return (
    <div className="space-y-6" aria-busy="true">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <Skeleton key={index} className="h-28 w-full" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton className="h-72 w-full" />
        <Skeleton className="h-72 w-full" />
      </div>
    </div>
  );
}

export default function DashboardPage() {
  useDocumentTitle("Главная");
  const auth = useAuth();
  const canManageSite = auth.principal?.role === "admin" || auth.principal?.role === "moderator";
  const overview = useQuery({
    queryKey: queryKeys.overview,
    queryFn: ({ signal }) => api.overview.get(signal),
    retry: false,
  });

  const unavailable =
    overview.error instanceof ApiError && overview.error.status === 404;

  const counts = overview.data?.counts;
  const attention = overview.data?.attention;
  const activity = overview.data?.recent_activity ?? [];
  const hasAnyFeature =
    counts !== undefined &&
    counts.features_active + counts.features_archived > 0;
  const availableSections = [
    ["Контент", "Записи модели", "/content"],
    ["Заявки", "Обработка форм", "/submissions"],
    ["Файлы", "Медиатека", "/media"],
    ...(canManageSite
      ? [
          ["Сайт", "Роутер, страницы и снапшоты", "/site/pages"],
          ["Доступы", "Пользователи и роли", "/access/tokens"],
        ]
      : []),
  ];

  return (
    <div className="page-grid">
      <PageHeader
        title="Главная"
        description="Сборки фич, записи модели, заявки и ветки сайтов."
        actions={
          <>
            <Button variant="outline" asChild>
              <Link to="/content">
                <Layers />
                Контент
              </Link>
            </Button>
            {canManageSite && (
              <Button asChild>
                <Link to="/site/pages">
                  <Plus />
                  Сборки и страницы
                </Link>
              </Button>
            )}
          </>
        }
      />

      {overview.isLoading ? (
        <OverviewSkeleton />
      ) : overview.error ? (
        <>
          {unavailable ? (
            <section aria-label="Основные разделы" className="space-y-6">
              <section aria-labelledby="dashboard-unavailable-title" className="rounded-lg border border-warning/30 bg-warning/5 p-5">
                <div className="flex gap-3">
                  <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-warning" />
                  <div>
                    <h2 id="dashboard-unavailable-title" className="text-sm font-semibold">Сводка недоступна</h2>
                    <p className="mt-1 max-w-2xl text-sm leading-6 text-muted-foreground">
                      Endpoint <code className="text-xs">GET /api/v1/admin/overview</code>{" "}
                      ещё не подключён на сервере. Мы не показываем вымышленные
                      цифры и активность. Разделы CMS при этом полностью доступны.
                    </p>
                    <div className="mt-4 flex flex-wrap gap-2">
                      <Button asChild size="sm">
                        <Link to="/content">Открыть контент</Link>
                      </Button>
                      <Button asChild size="sm" variant="outline">
                        <Link to="/submissions">Открыть заявки</Link>
                      </Button>
                      <Button asChild size="sm" variant="outline">
                        <Link to="/media">Открыть медиатеку</Link>
                      </Button>
                    </div>
                  </div>
                </div>
              </section>

              <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
                <EmptyState
                  icon={Sparkles}
                  title="Работайте с контентом"
                  description="Откройте фичу из выбранного снапшота, заполните её поля и опубликуйте контент."
                  action={
                    <Button asChild>
                      <Link to="/content">
                        <FileText />
                        Открыть контент
                      </Link>
                    </Button>
                  }
                  className="bg-card"
                />
                <Card>
                  <CardContent className="p-5">
                    <h2 className="text-sm font-semibold">Доступные разделы</h2>
                    <ul className="mt-3 space-y-2">
                      {availableSections.map(([title, text, to]) => (
                        <li key={to}>
                          <Link
                            to={to}
                            className="flex items-center justify-between rounded-md border border-border px-3 py-2.5 text-sm transition-colors hover:bg-muted/50"
                          >
                            <span>
                              <span className="font-medium">{title}</span>
                              <span className="ml-2 text-xs text-muted-foreground">{text}</span>
                            </span>
                            <ArrowRight className="h-4 w-4 text-muted-foreground" />
                          </Link>
                        </li>
                      ))}
                    </ul>
                  </CardContent>
                </Card>
              </div>
            </section>
          ) : (
            <ErrorState
              error={overview.error}
              title="Не удалось загрузить сводку"
              onRetry={() => void overview.refetch()}
            />
          )}
        </>
      ) : overview.data ? (
        <div className="space-y-6">
          <OverviewStats counts={overview.data.counts} />

          {canManageSite && <SiteStatusSection />}

          <section aria-label="Рабочая очередь и активность" className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-4 xl:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]">
            <AttentionQueue attention={attention} />
            <ActivityFeed items={activity} />
          </section>

          {!hasAnyFeature && canManageSite && (
            <section className="border-t border-border pt-4" aria-labelledby="quick-start-title">
              <h2 id="quick-start-title" className="text-sm font-semibold">Быстрый старт</h2>
              <ol className="mt-3 grid gap-x-5 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
                {[
                  ["1", "Загрузить фичи", "Импортировать ZIP-сборки с манифестами", "/site/features"],
                  ["2", "Собрать страницы", "Настроить маршруты и размещения", "/site/pages"],
                  ["3", "Добавить контент", "Заполнить поля из манифестов", "/content"],
                  ["4", "Назначить снапшот", "Выбрать ветку и подтвердить назначение", "/branches"],
                ].map(([step, title, text, to]) => (
                  <li key={step}>
                    <Link
                      to={to}
                      className="group block h-full rounded-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      <span className="font-mono text-xs text-muted-foreground group-hover:text-primary">
                        {step}
                      </span>
                      <span className="mt-1 block text-sm font-medium group-hover:text-primary">{title}</span>
                      <span className="mt-0.5 block text-xs text-muted-foreground">{text}</span>
                    </Link>
                  </li>
                ))}
              </ol>
            </section>
          )}
        </div>
      ) : null}

      {counts && (
        <p className="mt-6 text-2xs text-muted-foreground">
          Всего разделов: {counts.features_active + counts.features_archived} ·{" "}
          {counts.draft_model_records}{" "}
          {pluralRu(counts.draft_model_records, ["черновая запись", "черновые записи", "черновых записей"])}{" "}
          · ошибок медиа: {counts.failed_media}
        </p>
      )}
    </div>
  );
}
