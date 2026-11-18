import { ArrowRight, Database, FileText, Files, Layers, LockKeyhole, Settings, Workflow } from "lucide-react";
import { Link } from "react-router-dom";
import { useAuth } from "@/app/auth";
import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { Card, CardContent } from "@/components/ui/card";
import { canManageAccess, canManageSchema, hasAnyGroupAccess, hasGroupAccess } from "@/lib/permissions";

type AdminArea = { title: string; description: string; href: string; icon: typeof Layers };

export default function DashboardPage() {
  useDocumentTitle("Главная");
  const { principal } = useAuth();
  const areas: AdminArea[] = [
    ...(hasGroupAccess(principal, "content") ? [{ title: "Контент", description: "Модели и записи", href: "/content", icon: Database }] : []),
    ...(hasGroupAccess(principal, "forms") ? [{ title: "Заявки", description: "Ответы на формы", href: "/submissions", icon: FileText }] : []),
    ...(hasAnyGroupAccess(principal) ? [{ title: "Файлы", description: "Файловая библиотека", href: "/media", icon: Files }] : []),
    ...(hasGroupAccess(principal, "features") ? [{ title: "Конфигурация запуска", description: "Статические ветки и их параметры", href: "/settings", icon: Settings }] : []),
    ...(canManageSchema(principal) ? [{ title: "Сайт", description: "Снапшоты и фичи", href: "/site/pages", icon: Layers }] : []),
    ...(hasGroupAccess(principal, "router_routes") ? [{ title: "Маршруты", description: "Публичная выдача и загрузка", href: "/router/routes", icon: Workflow }] : []),
    ...(canManageAccess(principal) ? [{ title: "Доступы", description: "Пользователи и права групп", href: "/access/users", icon: LockKeyhole }] : []),
    ...(hasGroupAccess(principal, "coordinator_jobs") ? [{ title: "Задания", description: "Очередь автоматизации", href: "/coordinator/jobs", icon: Workflow }] : []),
  ];

  return (
    <div className="page-grid">
      <PageHeader title="Главная" description="Разделы доступны согласно вашим прямым правам на группы." />
      {areas.length === 0 ? (
        <p className="rounded-lg border border-border bg-card p-5 text-sm text-muted-foreground">
          Для этой учётной записи пока не назначен доступ к разделам.
        </p>
      ) : (
        <section aria-label="Разделы системы" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {areas.map(({ title, description, href, icon: Icon }) => (
            <Link key={href} to={href} className="rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <Card className="h-full transition-colors hover:bg-muted/40">
                <CardContent className="flex min-h-24 items-center gap-3 p-4">
                  <Icon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">{title}</span>
                    <span className="mt-1 block text-xs text-muted-foreground">{description}</span>
                  </span>
                  <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                </CardContent>
              </Card>
            </Link>
          ))}
        </section>
      )}
    </div>
  );
}
