import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, FileCode2, RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import type { FeatureImportState, Media } from "@/types";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { EmptyState, ErrorState, PageHeader, useDocumentTitle } from "@/components/common/page";
import { errorMessage } from "@/lib/errors";
import { uid } from "@/lib/utils";
import { groupFeatureBuilds } from "./feature-builds";
import { activeFeatureImportIDForPrincipal } from "./active-feature-import";

const catalogQueryKey = ["site", "feature-builds"] as const;
const stateLabels: Record<FeatureImportState, string> = {
  queued: "В очереди",
  running: "Импортируется",
  succeeded: "Готово",
  failed: "Ошибка импорта",
  cancelled: "Отменено",
};
const stateVariants: Record<FeatureImportState, "muted" | "info" | "success" | "destructive"> = {
  queued: "muted",
  running: "info",
  succeeded: "success",
  failed: "destructive",
  cancelled: "muted",
};

function readActiveImportID(principalID: string): string | null {
  try {
    const value = window.localStorage.getItem(`we:feature-import:active:v1:${principalID}`);
    return value && /^[A-Za-z0-9_.-]{1,128}$/.test(value) ? value : null;
  } catch {
    return null;
  }
}

function setStoredActiveImportID(principalID: string, id: string | null): void {
  const key = `we:feature-import:active:v1:${principalID}`;
  try {
    if (id) window.localStorage.setItem(key, id);
    else window.localStorage.removeItem(key);
  } catch {
    // The page remains usable when browser storage is disabled.
  }
}

function importRequestKey(principalID: string, sourceFileID: string, fallback: Map<string, string>): string {
  const fallbackKey = `${principalID}:${sourceFileID}`;
  const storageKey = `we:feature-import:request:v1:${principalID}:${sourceFileID}`;
  try {
    const saved = window.localStorage.getItem(storageKey);
    if (saved && saved.length >= 16 && saved.length <= 128 && /^[A-Za-z0-9._:-]+$/.test(saved)) return saved;
  } catch {
    // Keep the key in memory when browser storage is disabled.
  }
  const current = fallback.get(fallbackKey);
  if (current) return current;

  const key = uid("feature-import");
  fallback.set(fallbackKey, key);
  try {
    window.localStorage.setItem(storageKey, key);
  } catch {
    // A retry in this page still reuses the in-memory idempotency key.
  }
  return key;
}

function clearImportRequestKey(principalID: string, sourceFileID: string, fallback: Map<string, string>): void {
  fallback.delete(`${principalID}:${sourceFileID}`);
  try {
    window.localStorage.removeItem(`we:feature-import:request:v1:${principalID}:${sourceFileID}`);
  } catch {
    // The successful job can still be followed in this page.
  }
}

function isTerminal(state: FeatureImportState | undefined): boolean {
  return state === "succeeded" || state === "failed" || state === "cancelled";
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export default function FeatureBuildRegistryPage() {
  useDocumentTitle("Реестр фичей");
  const { principal } = useAuth();
  const queryClient = useQueryClient();
  const [pickerOpen, setPickerOpen] = useState(false);
  const [activeImport, setActiveImport] = useState<{ principalID: string; id: string } | null>(null);
  const activeImportID = activeFeatureImportIDForPrincipal(activeImport, principal?.id);
  const pendingKeys = useRef(new Map<string, string>());
  const notifiedCompletion = useRef<string | null>(null);

  useEffect(() => {
    const id = principal ? readActiveImportID(principal.id) : null;
    setActiveImport(principal && id ? { principalID: principal.id, id } : null);
  }, [principal]);

  const catalog = useQuery({
    queryKey: catalogQueryKey,
    queryFn: ({ signal }) => api.website.featureBuilds(signal),
    retry: false,
  });
  const importJob = useQuery({
    queryKey: ["site", "feature-import", activeImportID],
    queryFn: ({ signal }) => api.website.featureImport(activeImportID!, signal),
    enabled: Boolean(activeImportID),
    retry: 1,
    refetchInterval: (query) => isTerminal(query.state.data?.state) ? false : 1500,
  });
  const beginImport = useMutation({
    mutationFn: (media: Media) => {
      if (!principal) throw new Error("Требуется войти в систему");
      const idempotencyKey = importRequestKey(principal.id, media.id, pendingKeys.current);
      const expectedSHA256 = media.sha256 && /^[a-f0-9]{64}$/i.test(media.sha256) ? media.sha256 : undefined;
      return api.website.beginFeatureImport(media.id, idempotencyKey, expectedSHA256);
    },
    onSuccess: (job) => {
      if (!principal) return;
      clearImportRequestKey(principal.id, job.source_file_id, pendingKeys.current);
      setStoredActiveImportID(principal.id, job.id);
      setActiveImport({ principalID: principal.id, id: job.id });
      setPickerOpen(false);
      toast.success("ZIP принят на импорт");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  useEffect(() => {
    const job = importJob.data;
    if (!job || job.state !== "succeeded" || notifiedCompletion.current === job.id) return;
    notifiedCompletion.current = job.id;
    toast.success("Фича добавлена в реестр");
    void queryClient.invalidateQueries({ queryKey: catalogQueryKey });
  }, [importJob.data, queryClient]);

  const setActiveJob = (id: string | null) => {
    if (principal) setStoredActiveImportID(principal.id, id);
    setActiveImport(principal && id ? { principalID: principal.id, id } : null);
  };

  if (catalog.isPending) return <p className="p-6 text-sm text-muted-foreground">Загружаем реестр фичей…</p>;
  if (catalog.error) return <ErrorState error={catalog.error} title="Не удалось загрузить реестр фичей" onRetry={() => void catalog.refetch()} />;

  const groups = groupFeatureBuilds(catalog.data.items);
  const job = importJob.data;
  const canStartImport = !beginImport.isPending && (!activeImportID || Boolean(job && isTerminal(job.state)));

  return (
    <div className="page-grid">
      <PageHeader
        title="Реестр фичей"
        description="Неизменяемые версии ZIP. Состав фич настраивается отдельно для каждого снапшота."
        actions={(
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" onClick={() => void catalog.refetch()} loading={catalog.isFetching}>
              <RefreshCw />Обновить
            </Button>
            <Button type="button" disabled={!canStartImport} onClick={() => setPickerOpen(true)}>
              {beginImport.isPending ? "Отправляем…" : "Импортировать ZIP"}
            </Button>
          </div>
        )}
      />

      {activeImportID && (
        <section aria-label="Состояние импорта фичи" className="flex flex-wrap items-start justify-between gap-3 rounded-md border border-border bg-card p-4">
          <div className="min-w-0 space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-sm font-medium">Импорт ZIP</h2>
              {job && <Badge variant={stateVariants[job.state]}>{stateLabels[job.state]}</Badge>}
              {importJob.isPending && <span role="status" className="text-xs text-muted-foreground">Проверяем состояние…</span>}
            </div>
            {job?.feature_id && <p className="text-sm text-muted-foreground">{job.feature_id}{job.version ? ` · v${job.version}` : ""}</p>}
            {job?.error_code && <p role="alert" className="text-sm text-destructive">Код ошибки: {job.error_code}</p>}
            {importJob.error && <p role="alert" className="text-sm text-destructive">Не удалось получить состояние задания: {errorMessage(importJob.error)}</p>}
            {job && <p className="text-xs text-muted-foreground">Создано {formatDate(job.created_at)}</p>}
          </div>
          <div className="flex gap-2">
            {importJob.error && <Button type="button" variant="outline" size="sm" onClick={() => void importJob.refetch()}>Повторить запрос</Button>}
            {(job && isTerminal(job.state) || importJob.error) && <Button type="button" variant="ghost" size="sm" onClick={() => setActiveJob(null)}>Скрыть</Button>}
          </div>
        </section>
      )}

      <Card>
        <CardHeader><CardTitle className="text-base">Фичи</CardTitle></CardHeader>
        <CardContent>
          {groups.length === 0 ? (
            <EmptyState
              icon={Boxes}
              title="Реестр пока пуст"
              description="Загрузите ZIP в медиатеку, затем импортируйте его здесь."
              action={<Button asChild variant="outline"><Link to="/media">Открыть медиатеку</Link></Button>}
            />
          ) : (
            <ul className="divide-y divide-border">
              {groups.map((group) => (
                <li className="py-3 first:pt-0 last:pb-0" key={group.key}><details className="group">
                  <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <span className="flex min-w-0 items-center gap-3">
                      <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted"><Boxes className="size-4" /></span>
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-medium">{group.name}</span>
                        <span className="mt-0.5 block truncate text-xs text-muted-foreground">{group.key} · версий: {group.versions.length} · последняя v{group.latest.version}</span>
                      </span>
                    </span>
                    <span className="text-xs text-muted-foreground">Последняя v{group.latest.version}</span>
                  </summary>
                  <div className="mt-3 space-y-2 border-t border-border pt-3">
                    {group.versions.map((build) => (
                      <details className="rounded-md border border-border p-3" key={build.id}>
                        <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-2 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                          <span className="text-sm font-medium">v{build.version}{build.version === group.latest.version ? " · последняя по SemVer" : ""}</span>
                          <span className="text-xs text-muted-foreground">{build.manifest.dataInputs?.length ? `Входов данных: ${build.manifest.dataInputs.length}` : "Без модельных данных"}</span>
                        </summary>
                        <div className="mt-3 grid gap-4 border-t border-border pt-3 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,.8fr)]">
                          <div className="space-y-3 text-sm">
                            <div className="flex items-center gap-2 font-medium"><FileCode2 className="size-4" />Манифест сборки</div>
                            <dl className="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs">
                              <dt className="text-muted-foreground">Название версии</dt><dd>{build.name}</dd>
                              <dt className="text-muted-foreground">Входы модели</dt><dd>{build.manifest.dataInputs?.map((input) => `${input.key} (${input.kind})`).join(", ") || "Нет"}</dd>
                              <dt className="text-muted-foreground">Совместимость</dt><dd>{build.manifest.engineApi}</dd>
                              <dt className="text-muted-foreground">Точки размещения</dt><dd>{build.manifest.slots.join(", ")}</dd>
                              <dt className="text-muted-foreground">Аргументы</dt><dd>{Object.keys((build.manifest.arguments.properties as Record<string, unknown> | undefined) ?? {}).join(", ") || "Нет"}</dd>
                              <dt className="text-muted-foreground">Загружена</dt><dd>{formatDate(build.createdAt)}</dd>
                              <dt className="text-muted-foreground">Размер</dt><dd>{build.artifactSize.toLocaleString("ru-RU")} байт</dd>
                              <dt className="text-muted-foreground">SHA-256</dt><dd className="break-all font-mono">{build.digest}</dd>
                            </dl>
                          </div>
                          <div className="min-w-0"><p className="mb-2 text-xs font-medium text-muted-foreground">Исходный manifest.json</p><pre className="max-h-64 overflow-auto rounded-md border border-border bg-background p-3 text-[11px] leading-5">{JSON.stringify(build.manifest, null, 2)}</pre></div>
                        </div>
                      </details>
                    ))}
                  </div>
                </details></li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      <p className="text-xs text-muted-foreground">Архивы сначала загружаются через Loader в <Link className="underline underline-offset-4" to="/media">медиатеке</Link>; Website проверяет архив и сам создаёт набор файлов фичи.</p>
      <MediaPickerDialog
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        onSelect={(items) => { if (items[0]) beginImport.mutate(items[0]); }}
        mode="single"
        accept="zip"
        title="Выбрать ZIP-фичу"
      />
    </div>
  );
}
