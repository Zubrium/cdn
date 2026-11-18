import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ErrorState, EmptyState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { hasGroupAccess } from "@/lib/permissions";

export default function SettingsPage() {
  useDocumentTitle("Настройки");
  const { principal } = useAuth();
  const branches = useQuery({
    queryKey: ["website", "branches"],
    queryFn: ({ signal }) => api.website.branches(signal),
  });

  if (branches.isLoading) return <LoadingPage label="Загружаем конфигурацию…" />;
  if (branches.error) {
    return (
      <div className="page-grid">
        <PageHeader title="Настройки" />
        <ErrorState
          error={branches.error}
          title="Не удалось загрузить конфигурацию веток"
          onRetry={() => void branches.refetch()}
        />
      </div>
    );
  }

  const items = branches.data?.items ?? [];
  return (
    <div className="page-grid">
      <PageHeader
        title="Конфигурация запуска"
        description="Состав веток, адреса, CORS и индексация задаются в YAML при запуске."
        actions={hasGroupAccess(principal, "features", 2) ? (
          <Button variant="outline" asChild>
            <Link to="/snapshots/branches">Назначения снапшотов</Link>
          </Button>
        ) : undefined}
      />
      {items.length === 0 ? (
        <EmptyState
          title="Нет настроенных веток"
          description="Добавьте ветки в конфигурацию запуска и перезапустите систему."
        />
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {items.map((branch) => (
            <section key={branch.id} className="rounded-xl border border-border bg-card p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="font-semibold">{branch.name}</h2>
                  <p className="mt-1 break-all text-xs text-muted-foreground">Сайт · {branch.site_id}</p>
                </div>
                <Badge variant={branch.enabled ? "success" : "muted"}>
                  {branch.enabled ? "Включена" : "Отключена"}
                </Badge>
              </div>

              <dl className="mt-4 divide-y divide-border text-sm">
                <div className="flex flex-wrap justify-between gap-2 py-2">
                  <dt className="text-muted-foreground">Адрес прослушивания</dt>
                  <dd><code>{branch.listen_address}</code></dd>
                </div>
                <div className="flex flex-wrap justify-between gap-2 py-2">
                  <dt className="text-muted-foreground">Индексация</dt>
                  <dd>{branch.indexable ? "Разрешена" : "Закрыта"}</dd>
                </div>
                <div className="py-2">
                  <dt className="text-muted-foreground">CORS origins</dt>
                  <dd className="mt-1 flex flex-wrap gap-1.5">
                    {branch.cors_origins.length > 0 ? branch.cors_origins.map((origin) => (
                      <Badge key={origin} variant="outline">{origin}</Badge>
                    )) : <span className="text-xs">Не заданы</span>}
                  </dd>
                </div>
                <div className="flex flex-wrap justify-between gap-2 py-2">
                  <dt className="text-muted-foreground">Назначенный снапшот</dt>
                  <dd className="max-w-full break-all text-right">
                    {branch.snapshot_id ? <code>{branch.snapshot_id}</code> : "Снапшот не назначен"}
                  </dd>
                </div>
              </dl>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
