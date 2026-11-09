import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowLeft, Save } from "lucide-react";
import { api, isApiError } from "@/api";
import { ErrorState, useDocumentTitle, useUnsavedChangesGuard } from "@/components/common/page";
import { InlineAlert } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/misc";
import type { Environment, SiteEnvironment } from "@/types";

const envLabels: Record<Environment, string> = {
  dev: "Разработка (dev)",
  prod: "Продакшен (prod)",
};

export default function EnvironmentSettingsPage() {
  useDocumentTitle("Настройки среды");
  const params = useParams<{ envKey: string }>();
  const envKey = (params.envKey === "prod" ? "prod" : "dev") as Environment;
  const queryClient = useQueryClient();

  const environments = useQuery({
    queryKey: ["site-environments"],
    queryFn: ({ signal }) => api.environments.list(signal),
  });
  const env = environments.data?.items.find((item) => item.key === envKey);

  if (environments.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }
  if (environments.error) {
    return (
      <ErrorState
        error={environments.error}
        title="Не удалось загрузить настройки среды"
        onRetry={() => void environments.refetch()}
      />
    );
  }
  if (!env) {
    return <div className="text-sm text-muted-foreground">Среда не найдена.</div>;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" asChild>
          <Link to="/settings">
            <ArrowLeft />
            К настройкам
          </Link>
        </Button>
      </div>
      <div>
        <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
          {envLabels[envKey]}
          <Badge variant={envKey === "prod" ? "success" : "secondary"}>{envKey}</Badge>
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Имя и базовый адрес этой среды. Списки CORS для обеих сред собраны в{" "}
          <Link to="/site-metadata" className="text-foreground underline underline-offset-4">
            Настройках сайта
          </Link>.
        </p>
      </div>
      <EnvironmentForm
        key={env.revision}
        env={env}
        reloading={environments.isFetching}
        onReload={() => environments.refetch()}
        onSaved={() => {
          void queryClient.invalidateQueries({ queryKey: ["site-environments"] });
          toast.success("Настройки среды сохранены");
        }}
      />
    </div>
  );
}

function EnvironmentForm({ env, reloading, onReload, onSaved }: { env: SiteEnvironment; reloading: boolean; onReload: () => Promise<unknown>; onSaved: () => void }) {
  const [name, setName] = useState(env.name);
  const [baseUrl, setBaseUrl] = useState(env.public_base_url);
  const [conflict, setConflict] = useState(false);
  const dirty = name !== env.name || baseUrl !== env.public_base_url;
  const guard = useUnsavedChangesGuard(dirty);

  const save = useMutation({
    mutationFn: () =>
      api.environments.update(env.key, {
        revision: env.revision,
        name: name.trim() || undefined,
        public_base_url: baseUrl.trim(),
      }),
    onSuccess: () => {
      onSaved();
    },
    onError: (error) => {
      if (isApiError(error) && error.status === 409) {
        setConflict(true);
        return;
      }
      toast.error(isApiError(error) ? error.message : "Не удалось сохранить настройки");
    },
  });

  return (
    <>
      {guard.dialog}
      <Card>
      <CardHeader>
        <CardTitle>Параметры среды</CardTitle>
        <CardDescription>
          Ревизия {env.revision}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {conflict && (
          <InlineAlert tone="warning" title="Настройки изменились">
            Ваши поля останутся здесь. Загрузите актуальную ревизию, чтобы заменить их последними сохранёнными значениями.
            <div className="mt-3">
              <Button type="button" variant="outline" size="sm" onClick={() => void onReload()} disabled={reloading || save.isPending}>
                {reloading ? "Загружаем…" : "Загрузить актуальную ревизию"}
              </Button>
            </div>
          </InlineAlert>
        )}
        <label className="block max-w-md text-sm">
          <span className="mb-1 block text-xs text-muted-foreground">Название</span>
          <Input value={name} onChange={(event) => setName(event.target.value)} maxLength={200} />
        </label>
        <label className="block max-w-md text-sm">
          <span className="mb-1 block text-xs text-muted-foreground">Публичный base URL</span>
          <Input
            value={baseUrl}
            onChange={(event) => setBaseUrl(event.target.value)}
            placeholder="https://example.com"
          />
          <span className="mt-1 block text-xs text-muted-foreground">
            Используется в absolute-ссылках (медиа, письма). Оставьте пустым для относительных
            ссылок.
          </span>
        </label>
        <div className="flex items-center gap-2">
          <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!dirty || reloading || conflict}>
            <Save />
            Сохранить
          </Button>
          <Button
            variant="outline"
            type="button"
            disabled={!dirty || conflict}
            onClick={() => {
              setName(env.name);
              setBaseUrl(env.public_base_url);
            }}
          >
            Сбросить
          </Button>
        </div>
      </CardContent>
      </Card>
    </>
  );
}
