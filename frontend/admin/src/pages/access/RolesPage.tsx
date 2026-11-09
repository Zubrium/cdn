import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { CheckCircle2, Info, RefreshCw, ShieldCheck } from "lucide-react";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import {
  EmptyState,
  ErrorState,
  InlineAlert,
  LoadingPage,
} from "@/components/common/page";
import { RoleBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { cn } from "@/lib/utils";

export default function RolesPage() {
  const { principal } = useAuth();
  const settingsQuery = useQuery({
    queryKey: ["settings"],
    queryFn: ({ signal }) => api.settings.get(signal),
  });

  if (settingsQuery.isLoading) return <LoadingPage label="Загружаем роли…" />;

  if (settingsQuery.error) {
    return (
      <ErrorState
        error={settingsQuery.error}
        title="Не удалось загрузить матрицу ролей"
        onRetry={() => void settingsQuery.refetch()}
      />
    );
  }

  const roles = settingsQuery.data?.roles ?? [];

  if (roles.length === 0) {
    return (
      <EmptyState
        icon={ShieldCheck}
        title="Роли не настроены"
        description="Сервер не вернул описание ролей."
        action={
          <Button variant="outline" onClick={() => void settingsQuery.refetch()}>
            <RefreshCw />
            Повторить
          </Button>
        }
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Роли и права</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Матрица отражает правила, которые реально применяет сервер. Роли
            фиксированы и не редактируются из интерфейса.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {principal && (
            <span className="flex items-center gap-2 text-xs text-muted-foreground">
              Ваша роль:
              <RoleBadge role={principal.role} />
            </span>
          )}
          <Button
            variant="outline"
            onClick={() => void settingsQuery.refetch()}
            loading={settingsQuery.isFetching}
          >
            <RefreshCw />
            Обновить
          </Button>
        </div>
      </div>

      <InlineAlert tone="info" title="Как выдаются права">
        Права задаются ролью токена. Чтобы изменить их, отзовите токен и
        выпустите новый с нужной ролью. <Link to="/access/tokens" className="underline">Перейти к токенам</Link>.
      </InlineAlert>

      <div className="grid gap-3 lg:grid-cols-3">
        {roles.map((role) => {
          const current = principal?.role === role.id;
          return (
            <Card
              key={role.id}
              className={cn(current && "border-primary/60 ring-1 ring-primary/20")}
            >
              <CardHeader>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <CardTitle className="text-base">{role.label}</CardTitle>
                  <div className="flex items-center gap-1.5">
                    {current && <Badge variant="default">Ваша роль</Badge>}
                    <code className="text-2xs text-muted-foreground">{role.id}</code>
                  </div>
                </div>
                <CardDescription>{role.summary}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="space-y-2">
                  {role.permissions.map((permission) => (
                    <div
                      key={permission.resource}
                      className="rounded-md border border-border bg-muted/20 p-2"
                    >
                      <p className="text-xs font-medium">{permission.resource}</p>
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {permission.actions.map((action) => (
                          <Badge key={action} variant="muted">
                            {action}
                          </Badge>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>

                {role.notes.length > 0 && (
                  <div>
                    <p className="flex items-center gap-1.5 text-2xs font-medium text-muted-foreground">
                      <Info className="h-3.5 w-3.5" aria-hidden="true" />
                      Особенности
                    </p>
                    <ul className="mt-1.5 space-y-1">
                      {role.notes.map((note) => (
                        <li
                          key={note}
                          className="flex gap-1.5 text-xs leading-5 text-muted-foreground"
                        >
                          <CheckCircle2
                            className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success"
                            aria-hidden="true"
                          />
                          <span>{note}</span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </CardContent>
            </Card>
          );
        })}
      </div>
    </div>
  );
}
