import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api, isApiError } from "@/api";
import { ErrorState, InlineAlert, useUnsavedChangesGuard } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { Environment, SiteEnvironment } from "@/types";

const envLabels: Record<Environment, string> = {
  dev: "Разработка",
  prod: "Продакшен",
};

type DirtyByEnvironment = Record<Environment, boolean>;

export function SiteCorsSettings() {
  const queryClient = useQueryClient();
  const [dirtyByEnvironment, setDirtyByEnvironment] = useState<DirtyByEnvironment>({ dev: false, prod: false });
  const [activeEnvironment, setActiveEnvironment] = useState<Environment>("dev");
  const environments = useQuery({
    queryKey: ["site-environments"],
    queryFn: ({ signal }) => api.environments.list(signal),
  });

  const updateDirty = useCallback((key: Environment, dirty: boolean) => {
    setDirtyByEnvironment((current) => current[key] === dirty ? current : { ...current, [key]: dirty });
  }, []);
  const hasUnsavedChanges = dirtyByEnvironment.dev || dirtyByEnvironment.prod;
  const guard = useUnsavedChangesGuard(hasUnsavedChanges);

  const changeEnvironment = (value: string) => setActiveEnvironment(value as Environment);

  const onSaved = useCallback((updated: SiteEnvironment) => {
    queryClient.setQueryData<{ items: SiteEnvironment[] }>(["site-environments"], (current) => ({
      items: current?.items.map((item) => item.key === updated.key ? updated : item) ?? [updated],
    }));
    toast.success(`CORS для ${updated.key} сохранён`);
  }, [queryClient]);

  if (environments.isLoading) {
    return <div className="rounded-xl border border-border p-5 text-sm text-muted-foreground">Загружаем списки CORS…</div>;
  }
  if (environments.error && !environments.data) {
    return <ErrorState error={environments.error} title="Не удалось загрузить списки CORS" onRetry={() => void environments.refetch()} />;
  }

  const items = environments.data?.items ?? [];
  const byKey = new Map(items.map((item) => [item.key, item]));
  const dev = byKey.get("dev");
  const prod = byKey.get("prod");

  return (
    <>
      {guard.dialog}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base"><ShieldCheck className="size-4" />CORS для публичного API</CardTitle>
          <CardDescription>Разрешённые источники настраиваются отдельно для dev и prod.</CardDescription>
        </CardHeader>
        <CardContent>
          <Tabs value={activeEnvironment} onValueChange={changeEnvironment}>
            <TabsList aria-label="Среда для настройки CORS">
              <TabsTrigger value="dev">Разработка <Badge variant="muted">dev</Badge></TabsTrigger>
              <TabsTrigger value="prod">Продакшен <Badge variant="muted">prod</Badge></TabsTrigger>
            </TabsList>
            <TabsContent value="dev" forceMount>
              {dev ? <EnvironmentCorsForm env={dev} reloading={environments.isFetching} onReload={() => environments.refetch()} onSaved={onSaved} onDirtyChange={updateDirty} /> : <InlineAlert tone="warning" title="Среда dev не найдена">Обновите страницу или проверьте конфигурацию сред.</InlineAlert>}
            </TabsContent>
            <TabsContent value="prod" forceMount>
              {prod ? <EnvironmentCorsForm env={prod} reloading={environments.isFetching} onReload={() => environments.refetch()} onSaved={onSaved} onDirtyChange={updateDirty} /> : <InlineAlert tone="warning" title="Среда prod не найдена">Обновите страницу или проверьте конфигурацию сред.</InlineAlert>}
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>
    </>
  );
}

function EnvironmentCorsForm({
  env,
  reloading,
  onReload,
  onSaved,
  onDirtyChange,
}: {
  env: SiteEnvironment;
  reloading: boolean;
  onReload: () => Promise<{ data?: { items: SiteEnvironment[] }; isError?: boolean }>;
  onSaved: (updated: SiteEnvironment) => void;
  onDirtyChange: (key: Environment, dirty: boolean) => void;
}) {
  const [origins, setOrigins] = useState(env.cors_origins.join("\n"));
  const [conflict, setConflict] = useState(false);
  const baseRevision = useRef(env.revision);
  const dirty = origins !== env.cors_origins.join("\n");

  useEffect(() => {
    if (env.revision === baseRevision.current) return;
    if (dirty) {
      setConflict(true);
      return;
    }
    baseRevision.current = env.revision;
    setOrigins(env.cors_origins.join("\n"));
    setConflict(false);
    onDirtyChange(env.key, false);
  }, [dirty, env.cors_origins, env.key, env.revision, onDirtyChange]);

  useEffect(() => {
    onDirtyChange(env.key, dirty);
  }, [dirty, env.key, onDirtyChange]);

  const save = useMutation({
    mutationFn: () => api.environments.update(env.key, {
        revision: env.revision,
        cors_origins: origins.split(/[\n,]+/).map((origin) => origin.trim()).filter(Boolean),
      }),
    onSuccess: (updated) => {
      baseRevision.current = updated.revision;
      setConflict(false);
      setOrigins(updated.cors_origins.join("\n"));
      onSaved(updated);
    },
    onError: (reason) => {
      if (isApiError(reason) && reason.status === 409) {
        setConflict(true);
        return;
      }
      toast.error(isApiError(reason) ? reason.message : "Не удалось сохранить список CORS");
    },
  });

  const reloadLatest = async () => {
    const result = await onReload();
    if (result.isError) {
      toast.error("Не удалось загрузить актуальные настройки. Повторите попытку.");
      return;
    }
    const latest = result.data?.items.find((item) => item.key === env.key);
    if (!latest) return;
    baseRevision.current = latest.revision;
    setOrigins(latest.cors_origins.join("\n"));
    setConflict(false);
    onDirtyChange(env.key, false);
  };

  return (
    <div className="mt-4 space-y-4">
      {conflict && (
        <InlineAlert tone="warning" title="Настройки среды изменились">
          Загрузите актуальную ревизию, чтобы заменить этот список последними сохранёнными значениями.
          <div className="mt-3">
            <Button type="button" variant="outline" size="sm" onClick={() => void reloadLatest()} disabled={reloading}>
              {reloading ? "Загружаем…" : "Загрузить актуальную ревизию"}
            </Button>
          </div>
        </InlineAlert>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span className="font-medium">{envLabels[env.key]} · ревизия {env.revision}</span>
        <span className="text-xs text-muted-foreground">{env.cors_origins.length} источников сохранено</span>
      </div>
      <label className="block space-y-1.5">
        <span className="text-sm font-medium">Разрешённые источники</span>
        <Textarea
          value={origins}
          onChange={(event) => {
            const next = event.currentTarget.value;
            setOrigins(next);
            onDirtyChange(env.key, next !== env.cors_origins.join("\n"));
          }}
          placeholder={"https://app.example\nhttps://editor.example"}
          rows={5}
          disabled={conflict}
          aria-describedby={`cors-help-${env.key}`}
        />
        <span id={`cors-help-${env.key}`} className="block text-xs text-muted-foreground">
          По одному URL http(s) в строке; * разрешает любой источник. Пустой список использует bootstrap-настройку сервера. Изменения применяются без перезапуска.
        </span>
      </label>
      <div className="flex flex-wrap gap-2">
        <Button type="button" onClick={() => save.mutate()} loading={save.isPending} disabled={!dirty || conflict || reloading}>
          Сохранить {env.key}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={!dirty || conflict}
          onClick={() => {
            setOrigins(env.cors_origins.join("\n"));
            onDirtyChange(env.key, false);
          }}
        >
          Сбросить
        </Button>
      </div>
    </div>
  );
}
