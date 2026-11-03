import { useEffect, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { History, Rocket, Save } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { useConfirm } from "@/components/common/confirm";
import { EmptyState, ErrorState, PageHeader } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { Environment, SiteSnapshot, SiteSnapshotList } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";

const emptyList: SiteSnapshotList = {
  items: [],
  active: { dev: "", prod: "" },
  activeSnapshots: { dev: null, prod: null },
};

const errorMessage = (error: unknown) => error instanceof Error ? error.message : "Неизвестная ошибка";
const environmentNames: Record<Environment, string> = { dev: "dev", prod: "production" };

function SnapshotState({ snapshot, environment, deployedAt }: { snapshot: SiteSnapshot | null; environment: Environment; deployedAt?: string }) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-xs text-muted-foreground">Активен в {environmentNames[environment]}</p>
        {snapshot ? (
          <div className="mt-1 min-w-0">
            <p className="truncate text-sm font-medium">#{snapshot.sequence} · {snapshot.name}</p>
            <p className="mt-1 text-xs text-muted-foreground">{deployedAt ? `Назначен ${new Date(deployedAt).toLocaleString("ru-RU")}` : "Дата назначения недоступна"}</p>
          </div>
        ) : <p className="mt-1 text-sm text-muted-foreground">Нет активной версии</p>}
      </CardContent>
    </Card>
  );
}

export default function SnapshotsPage() {
  const { dirty, saving: draftSaving, conflict, save, reload } = useSiteWorkspace();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [snapshots, setSnapshots] = useState<SiteSnapshotList>(emptyList);
  const [name, setName] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [environment, setEnvironment] = useState<Environment>("dev");
  const history = useInfiniteQuery({
    queryKey: ["snapshot-events", environment],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) => api.siteBuilder.snapshotEvents(environment, pageParam, signal),
    getNextPageParam: (page) => page.nextCursor,
  });
  const events = history.data?.pages.flatMap((page) => page.items) ?? [];

  const refresh = async () => {
    const result = await api.siteBuilder.snapshots();
    setSnapshots(result);
  };

  const retry = async () => {
    setLoading(true);
    setError(null);
    try {
      await refresh();
    } catch (reason) {
      setError(reason);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    api.siteBuilder.snapshots()
      .then((result) => { if (active) setSnapshots(result); })
      .catch((reason: unknown) => { if (active) setError(reason); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  const create = async () => {
    if (dirty) return;
    setBusy(true);
    try {
      const snapshot = await api.siteBuilder.createSnapshot(name.trim());
      setName("");
      await refresh();
      toast.success(`Снапшот #${snapshot.sequence} создан. Выберите среду для развёртывания.`);
    } catch (reason) {
      toast.error(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };

  const deploy = async (snapshot: SiteSnapshot, environment: Environment, rollback = false) => {
    const target = environment === "dev" ? "dev" : "production";
    const accepted = await confirm({
      title: `${rollback ? "Откатить" : "Развернуть"} снапшот в ${target}?`,
      description: <>
        Снапшот <strong>#{snapshot.sequence} · {snapshot.name}</strong> станет активной версией среды {target}.
        Это можно сделать для любого снапшота; dev и production переключаются независимо.
      </>,
      confirmLabel: `${rollback ? "Откатить" : "Развернуть"} в ${target}`,
    });
    if (!accepted) return;

    setBusy(true);
    try {
      if (rollback) await api.siteBuilder.rollback(snapshot.id, environment);
      else await api.siteBuilder.deploySnapshot(snapshot.id, environment);
      await Promise.all([refresh(), queryClient.invalidateQueries({ queryKey: ["snapshot-events", environment] })]);
      toast.success(`Снапшот #${snapshot.sequence} ${rollback ? "восстановлен" : "развёрнут"} в ${target}`);
    } catch (reason) {
      toast.error(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };

  const loadMore = async () => {
    if (snapshots.nextCursor === undefined || loadingMore) return;
    setLoadingMore(true);
    try {
      const result = await api.siteBuilder.snapshots(snapshots.nextCursor);
      setSnapshots((current) => {
        const seen = new Set(current.items.map((item) => item.id));
        return {
          ...result,
          items: [...current.items, ...result.items.filter((item) => !seen.has(item.id))],
        };
      });
    } catch (reason) {
      toast.error(errorMessage(reason));
    } finally {
      setLoadingMore(false);
    }
  };

  if (loading) return <p className="p-6 text-sm text-muted-foreground">Загружаем снапшоты…</p>;
  if (error) return <ErrorState error={error} title="Не удалось загрузить снапшоты" onRetry={() => void retry()} />;

  const activeRecord = (environment: Environment) => snapshots.activeSnapshots?.[environment]
    ?? snapshots.items.find((item) => item.id === snapshots.active[environment])
    ?? null;

  return (
    <div className="page-grid">
      <PageHeader
        title="Снапшоты"
        description="Снапшот — неизменяемая копия текущего сохранённого черновика. Создайте сколько нужно версий и разверните любую сразу в dev или production."
      />

      <div className="space-y-4">
        <Tabs value={environment} onValueChange={(value) => setEnvironment(value as Environment)}>
          <TabsList aria-label="Окружение">
            <TabsTrigger value="dev">Development</TabsTrigger>
            <TabsTrigger value="prod">Production</TabsTrigger>
          </TabsList>
          <TabsContent value={environment} className="space-y-4">
        <SnapshotState snapshot={activeRecord(environment)} environment={environment} deployedAt={events.find((event) => event.snapshotId === snapshots.active[environment])?.createdAt} />

        <Card>
          <CardHeader><h2 className="text-base font-semibold">История развёртываний</h2><CardDescription>Последние операции выбранной среды.</CardDescription></CardHeader>
          <CardContent>
            {history.isPending ? <p className="text-sm text-muted-foreground">Загружаем историю…</p> : history.isError && events.length === 0 ? <ErrorState error={history.error} title="Не удалось загрузить историю" onRetry={() => void history.refetch()} /> : events.length === 0 ? <p className="text-sm text-muted-foreground">Операций пока нет.</p> : (
              <ol className="divide-y divide-border">{events.map((event) => (
                <li key={event.id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
                  <span>{event.action === "rollback" ? "Откат" : "Развёртывание"} · #{event.sequence} · {event.name}</span>
                  <span className="text-xs text-muted-foreground">{new Date(event.createdAt).toLocaleString("ru-RU")}</span>
                </li>
              ))}</ol>
            )}
            {history.hasNextPage && <Button type="button" variant="outline" size="sm" className="mt-3" loading={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>Показать более ранние операции</Button>}
            {history.isFetchNextPageError && <p role="alert" className="mt-2 text-sm text-destructive">Не удалось загрузить остальные операции. Повторите попытку.</p>}
          </CardContent>
        </Card>
          </TabsContent>
        </Tabs>

        <Card>
        <CardHeader>
          <h2 className="text-base font-semibold">Создать снапшот</h2>
          <CardDescription>Фиксируется только сохранённый черновик. Создание не меняет активные версии dev или production.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
            <div className="min-w-0 flex-1 space-y-1.5">
              <Label htmlFor="snapshot-name">Название <span className="font-normal text-muted-foreground">(необязательно)</span></Label>
              <Input id="snapshot-name" value={name} onChange={(event) => setName(event.currentTarget.value)} maxLength={160} placeholder="Например, каталог с новыми карточками" />
            </div>
            <Button type="button" onClick={() => void create()} loading={busy} disabled={dirty || draftSaving}>
              <Save />Создать снапшот
            </Button>
          </div>
          {dirty && (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
              <p className="text-warning">Есть несохранённые изменения страниц и маршрутов. Сохраните их, прежде чем создавать снапшот.</p>
              <div className="flex gap-2">
                {conflict && <Button type="button" size="sm" variant="outline" disabled={draftSaving} onClick={() => void reload()}>Загрузить актуальную версию</Button>}
                <Button type="button" size="sm" disabled={draftSaving} onClick={() => void save()}>{draftSaving ? "Сохраняем…" : "Сохранить черновик"}</Button>
              </div>
            </div>
          )}
        </CardContent>
        </Card>

        <Card>
        <CardHeader>
          <h2 className="text-base font-semibold">История снапшотов</h2>
          <CardDescription>Выберите версию и среду назначения. Развёртывание не требует предварительной отправки в dev.</CardDescription>
        </CardHeader>
        <CardContent>
          {snapshots.items.length === 0 ? (
            <EmptyState icon={History} title="Снапшотов пока нет" description="Сохраните черновик и создайте первую версию сайта." />
          ) : (
            <div className="divide-y divide-border">
              {snapshots.items.map((snapshot) => {
                const inDev = snapshot.id === snapshots.active.dev;
                const inProd = snapshot.id === snapshots.active.prod;
                return (
                  <article className="flex flex-col gap-3 py-4 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between" key={snapshot.id}>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-sm font-medium">#{snapshot.sequence} · {snapshot.name}</h3>
                        {inDev && <span className="rounded-md bg-secondary px-2 py-0.5 text-[11px] text-secondary-foreground">Активен в dev</span>}
                        {inProd && <span className="rounded-md bg-success/10 px-2 py-0.5 text-[11px] text-success">Активен в production</span>}
                      </div>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {new Date(snapshot.createdAt).toLocaleString("ru-RU")} · <span className="break-all font-mono">SHA-256 {snapshot.digest}</span>
                      </p>
                    </div>
                    <div className="flex shrink-0 flex-wrap gap-2">
                      {snapshot.id !== snapshots.active[environment] && <Button type="button" size="sm" onClick={() => void deploy(snapshot, environment)} disabled={busy}><Rocket />Развернуть</Button>}
                      {snapshot.id !== snapshots.active[environment] && <Button type="button" size="sm" variant="outline" onClick={() => void deploy(snapshot, environment, true)} disabled={busy}>Откатить</Button>}
                    </div>
                  </article>
                );
              })}
            </div>
          )}
          {snapshots.nextCursor !== undefined && (
            <div className="mt-4 flex justify-center border-t border-border pt-4">
              <Button type="button" variant="outline" onClick={() => void loadMore()} loading={loadingMore} disabled={busy}>
                Показать более ранние снапшоты
              </Button>
            </div>
          )}
        </CardContent>
        </Card>
      </div>
    </div>
  );
}
