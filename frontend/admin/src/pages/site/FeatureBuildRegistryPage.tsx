import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { Boxes, CloudUpload, FileCode2, Fingerprint } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState, PageHeader } from "@/components/common/page";
import type { FeatureBuild } from "@/types";
import { groupFeatureBuilds } from "./feature-builds";

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Неизвестная ошибка";
}

export default function FeatureBuildRegistryPage() {
  const [items, setItems] = useState<FeatureBuild[]>([]);
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const fileInput = useRef<HTMLInputElement>(null);

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
    api.siteBuilder.featureBuilds()
      .then((result) => { if (active) setItems(result.items); })
      .catch((reason: unknown) => { if (active) setError(reason); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  const upload = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (!file) return;
    if (!file.name.toLowerCase().endsWith(".zip")) {
      toast.error("Выберите ZIP-архив сборки фичи.");
      return;
    }
    setUploading(true);
    try {
      const build = await api.siteBuilder.importFeatureBuild(file);
      await refresh();
      toast.success(`${build.key}@${build.version} добавлена в реестр`);
    } catch (reason) {
      toast.error(errorMessage(reason));
    } finally {
      setUploading(false);
    }
  };

  if (loading) return <p className="p-6 text-sm text-muted-foreground">Загружаем реестр фичей…</p>;
  if (error) return <ErrorState error={error} title="Не удалось загрузить реестр фичей" onRetry={() => void retry()} />;

  const groups = groupFeatureBuilds(items);
  return (
    <div className="page-grid">
      <PageHeader
        title="Реестр фичей"
        description="Здесь хранятся версии сборок. Состав фич настраивается отдельно для каждого снапшота."
        actions={(
          <>
            <input
              ref={fileInput}
              className="sr-only"
              type="file"
              accept=".zip,application/zip"
              disabled={uploading}
              onChange={upload}
              aria-label="Выбрать ZIP-сборку фичи"
            />
            <Button type="button" disabled={uploading} onClick={() => fileInput.current?.click()}>
              <CloudUpload className="size-4" />{uploading ? "Загружаем сборку…" : "Загрузить ZIP"}
            </Button>
          </>
        )}
      />
      <Card>
        <CardHeader><CardTitle className="text-base">Фичи</CardTitle></CardHeader>
        <CardContent>
          {groups.length === 0 ? (
            <div className="px-4 py-12 text-center">
              <Boxes className="mx-auto size-8 text-muted-foreground" />
              <p className="mt-3 text-sm font-medium">Реестр пока пуст</p>
              <p className="mt-1 text-sm text-muted-foreground">Загрузите ZIP-сборку, чтобы добавить фичу в систему.</p>
            </div>
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
                    <span className="flex items-center gap-2 text-xs text-muted-foreground"><Fingerprint className="size-3.5" />{group.latest.digest.slice(0, 16)}</span>
                  </summary>
                  <div className="mt-3 space-y-2 border-t border-border pt-3">
                    {group.versions.map((build) => (
                      <details className="rounded-md border border-border p-3" key={build.id}>
                        <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-2 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                          <span className="text-sm font-medium">v{build.version}{build.version === group.latest.version ? " · последняя по SemVer" : ""}</span>
                          <span className="flex items-center gap-2 text-xs text-muted-foreground"><span>{build.manifest.dataInputs?.length ? `Входов данных: ${build.manifest.dataInputs.length}` : "Без модельных данных"}</span><Fingerprint className="size-3.5" />{build.digest.slice(0, 16)}</span>
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
                              <dt className="text-muted-foreground">Загружена</dt><dd>{new Date(build.createdAt).toLocaleString("ru-RU")}</dd>
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
      <p className="text-xs text-muted-foreground">Сборки, которые используются снапшотами, сохраняются в реестре.</p>
    </div>
  );
}
