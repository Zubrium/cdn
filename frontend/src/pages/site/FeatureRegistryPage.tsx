import { useEffect, useMemo, useState, type ChangeEvent } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { Boxes, CloudUpload, FileCode2, Fingerprint } from "lucide-react";
import { toast } from "sonner";
import { api, publicMediaPath } from "@/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState, PageHeader } from "@/components/common/page";
import { isRecord } from "@/lib/utils";
import type { FeatureBuild } from "@/types";

function firstMediaId(schema: unknown, value: unknown): string | null {
  if (!isRecord(schema)) return null;
  if (schema.format === "media" && typeof value === "string" && value) {
    const mediaOptions = schema["x-media"];
    const accept = isRecord(mediaOptions) && typeof mediaOptions.accept === "string"
      ? mediaOptions.accept
      : "image";
    return accept === "image" || accept === "raster-image" ? value : null;
  }

  if (schema.type === "object" && isRecord(schema.properties) && isRecord(value)) {
    for (const [key, childSchema] of Object.entries(schema.properties)) {
      const mediaId = firstMediaId(childSchema, value[key]);
      if (mediaId) return mediaId;
    }
  }

  if (schema.type === "array" && isRecord(schema.items) && Array.isArray(value)) {
    for (const item of value) {
      const mediaId = firstMediaId(schema.items, item);
      if (mediaId) return mediaId;
    }
  }
  return null;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Неизвестная ошибка";
}

export default function FeatureRegistryPage() {
  const [items, setItems] = useState<FeatureBuild[]>([]);
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const snapshotsQuery = useQuery({
    queryKey: ["site", "snapshots"],
    queryFn: ({ signal }) => api.siteBuilder.snapshots(undefined, signal),
  });
  const activeSnapshotId = snapshotsQuery.data?.active.dev ?? "";
  const contentFeaturesQuery = useQuery({
    queryKey: ["site", "snapshot-content-features", activeSnapshotId],
    queryFn: ({ signal }) => api.siteBuilder.snapshotContentFeatures(activeSnapshotId, signal),
    enabled: Boolean(activeSnapshotId),
  });
  const contentFeatures = useMemo(
    () => contentFeaturesQuery.data?.items ?? [],
    [contentFeaturesQuery.data?.items],
  );
  const workspaceQueries = useQueries({
    queries: contentFeatures.map((feature) => ({
      queryKey: ["site", "snapshot-content-workspace", activeSnapshotId, feature.key],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        api.siteBuilder.snapshotContentWorkspace(activeSnapshotId, feature.key, signal),
      enabled: Boolean(activeSnapshotId),
    })),
  });
  const previewByKey = useMemo(() => {
    const previews = new Map<string, string>();
    contentFeatures.forEach((feature, index) => {
      const workspace = workspaceQueries[index]?.data;
      if (!workspace) return;
      const orderedItems = [...workspace.items].sort((a, b) => a.sortOrder - b.sortOrder);
      for (const item of orderedItems) {
        const mediaId = firstMediaId(feature.schema, item.data);
        if (mediaId) {
          previews.set(feature.key, mediaId);
          break;
        }
      }
    });
    return previews;
  }, [contentFeatures, workspaceQueries]);
  const contentBuildByKey = useMemo(
    () => new Map(contentFeatures.map((feature) => [feature.key, feature.buildDigest])),
    [contentFeatures],
  );

  const refresh = async () => {
    const result = await api.siteBuilder.featureBuilds();
    setItems(result.items);
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
    api.siteBuilder.featureBuilds().then((result) => { if (active) setItems(result.items); })
      .catch((reason: unknown) => { if (active) setError(reason); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  const upload = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (!file) return;
    if (!file.name.toLowerCase().endsWith(".zip")) { toast.error("Выберите ZIP-архив сборки фичи."); return; }
    setUploading(true);
    try {
      const build = await api.siteBuilder.importFeatureBuild(file);
      await refresh();
      toast.success(`${build.key}@${build.version} добавлена в реестр`);
    } catch (reason) { toast.error(errorMessage(reason)); }
    finally { setUploading(false); }
  };

  if (loading) return <p className="p-6 text-sm text-muted-foreground">Загружаем реестр фичей…</p>;
  if (error) return <ErrorState error={error} title="Не удалось загрузить реестр фичей" onRetry={() => void retry()} />;

  const sorted = [...items].sort((a, b) => a.key.localeCompare(b.key) || b.version.localeCompare(a.version));
  return (
    <div className="page-grid">
      <PageHeader title="Реестр фичей" description="Неизменяемые версии компонентов, доступные редактору страниц. Загружайте сюда ZIP-сборки." actions={
        <label className="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded-sm bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-within:outline-none focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2">
          <CloudUpload className="size-4" />{uploading ? "Загружаем сборку…" : "Загрузить ZIP"}
          <input className="sr-only" type="file" accept=".zip,application/zip" disabled={uploading} onChange={upload} />
        </label>
      } />
      <Card><CardHeader><CardTitle className="text-base">Сборки фич</CardTitle></CardHeader>
        <CardContent>
          {sorted.length === 0 ? <div className="px-4 py-12 text-center"><Boxes className="mx-auto size-8 text-muted-foreground" /><p className="mt-3 text-sm font-medium">Реестр пока пуст</p><p className="mt-1 text-sm text-muted-foreground">Загрузите первую ZIP-сборку, чтобы добавить её на страницу.</p></div> : (
            <div className="divide-y divide-border">
              {sorted.map((build) => <details className="group py-3 first:pt-0 last:pb-0" key={build.id}>
                <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  <span className="flex min-w-0 items-center gap-3"><span className="flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-md bg-muted">{contentBuildByKey.get(build.key) === build.digest && previewByKey.has(build.key) ? <img src={publicMediaPath(previewByKey.get(build.key)!)} alt="" aria-hidden="true" className="size-full object-cover" loading="lazy" /> : <Boxes className="size-4" />}</span><span className="min-w-0"><span className="block truncate text-sm font-medium">{build.name}</span><span className="mt-0.5 block truncate text-xs text-muted-foreground">{build.key}@{build.version} · {build.manifest.content.mode}</span></span></span>
                  <span className="flex items-center gap-2 text-xs text-muted-foreground"><Fingerprint className="size-3.5" />{build.digest.slice(0, 16)}</span>
                </summary>
                <div className="mt-3 grid gap-4 border-t border-border pt-3 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,.8fr)]">
                  <div className="space-y-3 text-sm"><div className="flex items-center gap-2 font-medium"><FileCode2 className="size-4" />Манифест сборки</div><dl className="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs"><dt className="text-muted-foreground">Режим контента</dt><dd>{build.manifest.content.mode}</dd><dt className="text-muted-foreground">Совместимость</dt><dd>{build.manifest.engineApi}</dd><dt className="text-muted-foreground">Точки размещения</dt><dd>{build.manifest.slots.join(", ")}</dd><dt className="text-muted-foreground">Аргументы</dt><dd>{Object.keys((build.manifest.arguments.properties as Record<string, unknown> | undefined) ?? {}).join(", ") || "Нет"}</dd><dt className="text-muted-foreground">Загружена</dt><dd>{new Date(build.createdAt).toLocaleString("ru-RU")}</dd><dt className="text-muted-foreground">SHA-256</dt><dd className="break-all font-mono">{build.digest}</dd></dl></div>
                  <div className="min-w-0"><p className="mb-2 text-xs font-medium text-muted-foreground">Исходный manifest.json</p><pre className="max-h-64 overflow-auto rounded-md border border-border bg-background p-3 text-[11px] leading-5">{JSON.stringify(build.manifest, null, 2)}</pre></div>
                </div>
              </details>)}
            </div>
          )}
        </CardContent>
      </Card>
      <p className="text-xs text-muted-foreground">Сборки, используемые опубликованными снапшотами, не удаляются из реестра.</p>
    </div>
  );
}
