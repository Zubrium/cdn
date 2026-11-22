import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Database, Route, Trash2 } from "lucide-react";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import type { DataModel, FeatureArgumentBinding, FeatureBuild, SiteEditorDraft, SitePage, SitePlacement } from "@/types";
import { routeCapturesForPage, detailRoutes as getDetailRoutes } from "../../route-tree";
import { FeatureArgumentsEditor } from "./FeatureArgumentsEditor";
import { DetailLinkEditor, StructuralBindingsEditor } from "./StructuralBindingsEditor";
import { properties, requiredProperties, type SchemaField } from "./schema";
import { buildFor } from "./schema";
import { siteContextQuery } from "../../site-query";
import { routeSuggestions } from "@/domains/forms/publication";

export function PlacementEditor({ page, placement, builds, config, dataModel, snapshotId, buildsLoading = false, onChange, onRemove }: { page: SitePage; placement: SitePlacement; builds: FeatureBuild[]; config: SiteEditorDraft; dataModel: DataModel | null; snapshotId: string; buildsLoading?: boolean; onChange: (value: SitePlacement) => void; onRemove: () => void }) {
  const build = buildFor(builds, placement);
  const manifest = build?.manifest;
  const fields = Object.fromEntries(Object.entries(properties(manifest?.arguments)).filter(([, schema]) => schema["x-editor-managed-by-site"] !== true)) as Record<string, SchemaField>;
  const requiredArgumentKeys = requiredProperties(manifest?.arguments);
  const captures = routeCapturesForPage(config.routeTree, page.id).flatMap((route) => route.captures);
  const uniqueCaptures = [...new Set(captures)];
  const dataCollectionFields = (manifest?.dataInputs ?? []).filter((input) => input.kind === "collection").flatMap((input) => (input.fields ?? []).flatMap((field) => {
    const match = /^\/([A-Za-z][A-Za-z0-9_]*)$/.exec(field.path);
    return match ? [{ field: match[1], label: `${input.key} · ${field.path}` }] : [];
  }));
  const uniqueDataCollectionFields = dataCollectionFields.filter((candidate) => dataCollectionFields.filter((item) => item.field === candidate.field).length === 1);
  const detailRoutes = getDetailRoutes(config.routeTree, config.pages);
  const routerRoutesAvailable = typeof api.router?.routes?.list === "function";
  const routerOperationsAvailable = typeof api.router?.operations?.list === "function";
  const formsAvailable = typeof api.formsEngine?.schemas === "function";
  const routerRoutesQuery = useQuery({
    queryKey: ["router", "routes", "argument-hints"],
    queryFn: ({ signal }) => api.router.routes.list({ limit: 100 }, signal),
    enabled: routerRoutesAvailable && Object.values(fields).some((field) => field.type === "string" && field.format === "route"),
  });
  const routerOperationsQuery = useQuery({
    queryKey: ["router", "operations"],
    queryFn: ({ signal }) => api.router.operations.list(signal),
    enabled: routerOperationsAvailable && routerRoutesQuery.isSuccess,
  });
  const formSchemasQuery = useQuery({
    queryKey: ["formsEngine", "schemas"],
    queryFn: ({ signal }) => api.formsEngine.schemas(undefined, 100, signal),
    enabled: formsAvailable && routerRoutesQuery.isSuccess,
  });
  const routeHints = routerRoutesQuery.isSuccess && routerOperationsQuery.isSuccess
    ? routeSuggestions(routerRoutesQuery.data.items, routerOperationsQuery.data.items)
    : [];
  const patchBinding = (key: string, binding?: FeatureArgumentBinding) => { const next = { ...(placement.arguments ?? {}) }; if (binding) next[key] = binding; else delete next[key]; onChange({ ...placement, arguments: next }); };

  const contentInputs = manifest?.dataInputs?.length ?? 0;
  const pageInputs = Object.keys(fields).length;
  return <section className="space-y-6">
    <div className="flex items-start justify-between gap-3"><div><p className="text-sm font-semibold">{manifest?.name ?? "Фича страницы"}</p><p className="mt-1 text-xs text-muted-foreground">Версия {placement.build.version}</p></div><Button type="button" variant="ghost" size="icon" className="size-11 shrink-0" aria-label="Удалить фичу со страницы" onClick={onRemove}><Trash2 /></Button></div>
    {!build && <p role={buildsLoading ? "status" : "alert"} className={`rounded-md border p-3 text-sm ${buildsLoading ? "border-border text-muted-foreground" : "border-warning/40 bg-warning/10 text-warning"}`}>
      {buildsLoading ? "Загружаем manifest закреплённой сборки…" : <>
        Manifest этой сборки отсутствует в реестре, поэтому аргументы и входы модели пока нельзя настроить. Зарегистрируйте исходный ZIP в <Link className="font-medium underline underline-offset-4" to={`/snapshots/registry${siteContextQuery(snapshotId)}`}>реестре сборок</Link>, затем вернитесь к этой странице.
      </>}
    </p>}
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem]"><div className="space-y-1.5 text-sm"><span className="text-muted-foreground">Версия фичи</span><p className="min-h-11 rounded-md border border-border bg-muted/30 px-3 py-2">Версия {placement.build.version} назначается в разделе «Версии»</p></div>
      <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Место на странице</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={placement.slot} onChange={(event) => onChange({ ...placement, slot: event.currentTarget.value })}>{(manifest?.slots ?? ["main"]).map((slot) => <option key={slot} value={slot}>{slot}</option>)}</select></label></div>
    <fieldset className="space-y-4 border-t border-border pt-5">
      <legend className="px-1 text-sm font-semibold">Источники данных</legend>
      <p className="text-xs text-muted-foreground">Проверьте, откуда фича получает значения страницы и опубликованные записи Content.</p>
      <div className="flex flex-wrap gap-2" aria-label="Сводка источников данных">
        {pageInputs > 0 || uniqueCaptures.length > 0 ? <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2.5 py-1 text-xs"><Route className="size-3.5" />Страница: {pageInputs + (uniqueCaptures.length ? 1 : 0)}</span> : null}
        {contentInputs > 0 ? <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2.5 py-1 text-xs"><Database className="size-3.5" />Контент: {contentInputs}</span> : null}
      </div>
      {!pageInputs && !contentInputs && <p className="rounded-md border border-dashed border-border p-3 text-sm text-muted-foreground">Эта фича не объявляет входящих данных. В её настройках ниже остаются только параметры отображения.</p>}

      {(pageInputs > 0 || uniqueCaptures.length > 0) && <details open className="border-t border-border/70 pt-4">
        <summary className="cursor-pointer text-sm font-medium">Параметры страницы и постоянные значения</summary>
        <p className="mt-1 text-xs text-muted-foreground">Значения можно взять из параметров маршрута или query, либо задать вручную. Для каждого источника показан пример использования.</p>
        <div className="mt-3">{Object.entries(fields).length > 0 && <FeatureArgumentsEditor fields={fields} requiredKeys={requiredArgumentKeys} arguments={placement.arguments} captures={uniqueCaptures} preparation={build && snapshotId ? { snapshotId, featureKey: build.key, buildVersion: build.version, schemaKind: "arguments", buildDigest: build.digest } : undefined} routeSuggestions={routeHints} formSchemas={formSchemasQuery.data?.items ?? []} onChange={(key, binding) => patchBinding(key, binding)} />}</div>
      </details>}

      {contentInputs > 0 && <details open className="border-t border-border/70 pt-4">
        <summary className="cursor-pointer text-sm font-medium">Контент модели</summary>
        <p className="mt-1 text-xs text-muted-foreground">Здесь выбирается форма данных. Конкретные записи и их порядок настраиваются в «Контенте».</p>
        <div className="mt-3">{manifest && <StructuralBindingsEditor page={page} placement={placement} manifest={manifest} dataModel={dataModel} captures={uniqueCaptures} snapshotId={snapshotId} argumentFields={fields} onChange={onChange} />}</div>
      </details>}

    </fieldset>
    {manifest && <DetailLinkEditor placement={placement} formatVersion={manifest.formatVersion} fields={uniqueDataCollectionFields} detailRoutes={detailRoutes} onChange={onChange} />}
  </section>;
}
