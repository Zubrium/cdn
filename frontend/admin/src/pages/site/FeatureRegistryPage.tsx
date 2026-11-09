import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Boxes, FileCode2, Fingerprint } from "lucide-react";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState, PageHeader } from "@/components/common/page";
import type { FeatureBuild } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import { selectFeature, selectedFeatures } from "./feature-selection";
import { groupFeatureBuilds } from "./feature-builds";

export default function FeatureRegistryPage() {
  const { config, dirty, saving, update, save } = useSiteWorkspace();
  const selected = useMemo(() => selectedFeatures(config), [config]);
  const [items, setItems] = useState<FeatureBuild[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [pendingVersions, setPendingVersions] = useState<Record<string, string>>({});

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

  if (loading) return <p className="p-6 text-sm text-muted-foreground">Загружаем закреплённые сборки…</p>;
  if (error) return <ErrorState error={error} title="Не удалось загрузить сборки" onRetry={() => void retry()} />;

  const clearPendingVersion = (key: string) => setPendingVersions((current) => {
    const next = { ...current };
    delete next[key];
    return next;
  });
  const chooseBuild = (build: FeatureBuild) => {
    update((current) => selectFeature(current, build.key, build.version));
    clearPendingVersion(build.key);
  };

  const allGroups = groupFeatureBuilds(items);
  const selectedGroups = allGroups.filter((group) => selected.has(group.key));
  const missingBuilds = [...selected].filter(([key, version]) =>
    !items.some((item) => item.key === key && item.version === version),
  );

  return (
    <div className="page-grid">
      <PageHeader title="Состав фичей снапшота" description="Список строится по размещениям страниц. Меняйте версии фич, используемых в этом снапшоте." />
      <div className="flex flex-wrap items-center gap-3">
        <p className="flex-1 text-sm text-muted-foreground">Используется фич: {selected.size}{dirty ? " · есть несохранённые изменения" : ""}</p>
        <Button type="button" disabled={!dirty || saving} onClick={() => void save()}>{saving ? "Создаём…" : "Создать снапшот"}</Button>
      </div>
      {missingBuilds.length > 0 && <div role="alert" className="rounded-lg border border-warning/40 bg-warning/10 p-4 text-sm">
        <p className="font-medium">Для выбранного снапшота не найдены некоторые закреплённые ZIP-сборки.</p>
        <p className="mt-1 text-muted-foreground">Зарегистрируйте нужную сборку в <Link className="underline underline-offset-4" to="/snapshots/registry">глобальном реестре</Link>, затем повторите проверку версии.</p>
        <ul className="mt-2 list-disc pl-5">{missingBuilds.map(([key, version]) => <li key={key}>{key}@{version}</li>)}</ul>
      </div>}
      <Card><CardHeader><CardTitle className="text-base">Фичи размещений</CardTitle></CardHeader>
        <CardContent>
          {selectedGroups.length === 0 ? <div className="px-4 py-12 text-center">
            <Boxes className="mx-auto size-8 text-muted-foreground" />
            <p className="mt-3 text-sm font-medium">Пока нет размещённых фич</p>
            <p className="mt-1 text-sm text-muted-foreground">Добавьте фичу на страницу в редакторе сайта — её сборка появится здесь автоматически.</p>
          </div> : (
            <ul className="divide-y divide-border">
              {selectedGroups.map((group) => {
                const currentVersion = selected.get(group.key)!;
                const requestedVersion = pendingVersions[group.key] ?? currentVersion;
                const build = group.versions.find((item) => item.version === requestedVersion) ?? group.versions.find((item) => item.version === currentVersion);
                if (!build) return null;
                const isCurrent = currentVersion === build.version;
                return <li className="py-3 first:pt-0 last:pb-0" key={group.key}><details className="group">
                  <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <span className="flex min-w-0 items-center gap-3">
                      <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted"><Boxes className="size-4" /></span>
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-medium">{group.name}</span>
                        <span className="mt-0.5 block truncate text-xs text-muted-foreground">{group.key} · в снапшоте v{currentVersion} · версий: {group.versions.length}</span>
                      </span>
                    </span>
                    <span className="flex items-center gap-2 text-xs text-muted-foreground"><Fingerprint className="size-3.5" />{build.digest.slice(0, 16)}</span>
                  </summary>
                  <div className="mt-3 space-y-4 border-t border-border pt-3">
                    <div className="flex flex-wrap items-end gap-3">
                      <label className="min-w-56 flex-1 space-y-1.5 text-sm">
                        <span className="text-muted-foreground">Версия сборки</span>
                        <select aria-label={`Версия сборки ${group.name}`} className="h-11 w-full rounded-md border border-input bg-background px-3" value={requestedVersion} disabled={saving} onChange={(event) => setPendingVersions((current) => ({ ...current, [group.key]: event.currentTarget.value }))}>
                          {group.versions.map((item) => <option key={item.id} value={item.version}>v{item.version}{item.id === group.latest.id ? " · последняя по SemVer" : ""}</option>)}
                        </select>
                      </label>
                      {isCurrent ? <span className="text-xs text-muted-foreground">Используется всеми размещениями и связями этой фичи</span> : <Button type="button" size="sm" variant="outline" className="min-h-11" disabled={saving} onClick={() => chooseBuild(build)}>
                        Выбрать v{build.version}
                      </Button>}
                    </div>
                    <div className="grid gap-4 border-t border-border pt-3 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,.8fr)]">
                      <div className="space-y-3 text-sm">
                        <div className="flex items-center gap-2 font-medium"><FileCode2 className="size-4" />Манифест v{build.version}</div>
                        <dl className="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs">
                          <dt className="text-muted-foreground">Входы модели</dt><dd>{build.manifest.dataInputs?.map((input) => `${input.key} (${input.kind})`).join(", ") || "Нет"}</dd>
                          <dt className="text-muted-foreground">Совместимость</dt><dd>{build.manifest.engineApi}</dd>
                          <dt className="text-muted-foreground">Точки размещения</dt><dd>{build.manifest.slots.join(", ")}</dd>
                          <dt className="text-muted-foreground">Аргументы</dt><dd>{Object.keys((build.manifest.arguments.properties as Record<string, unknown> | undefined) ?? {}).join(", ") || "Нет"}</dd>
                          <dt className="text-muted-foreground">SHA-256</dt><dd className="break-all font-mono">{build.digest}</dd>
                        </dl>
                      </div>
                      <div className="min-w-0"><p className="mb-2 text-xs font-medium text-muted-foreground">Контракт сборки из manifest.json (только для просмотра)</p><pre className="max-h-64 overflow-auto rounded-md border border-border bg-background p-3 text-[11px] leading-5">{JSON.stringify(build.manifest, null, 2)}</pre></div>
                    </div>
                  </div>
                </details></li>;
              })}
            </ul>
          )}
        </CardContent>
      </Card>
      <p className="text-xs text-muted-foreground">ZIP-файлы регистрируются отдельно в <Link className="underline underline-offset-4" to="/snapshots/registry">глобальном реестре фичей</Link>. Здесь доступны только уже используемые сборки.</p>
    </div>
  );
}
