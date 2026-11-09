import { Link } from "react-router-dom";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { FeatureArgumentBinding, FeatureBuild, SiteConfig, SitePage, SitePlacement } from "@/types";
import { routeCapturesForPage, detailRoutes as getDetailRoutes } from "../../route-tree";
import { BackendRequestsEditor } from "./BackendRequestEditor";
import { FeatureArgumentsEditor } from "./FeatureArgumentsEditor";
import { DetailLinkEditor, StructuralBindingsEditor } from "./StructuralBindingsEditor";
import { properties, requiredProperties, type SchemaField } from "./schema";
import { buildFor } from "./schema";

export function PlacementEditor({ page, placement, builds, config, snapshotId, buildsLoading = false, onChange, onRemove }: { page: SitePage; placement: SitePlacement; builds: FeatureBuild[]; config: SiteConfig; snapshotId: string; buildsLoading?: boolean; onChange: (value: SitePlacement) => void; onRemove: () => void }) {
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
  const patchBinding = (key: string, binding?: FeatureArgumentBinding) => { const next = { ...(placement.arguments ?? {}) }; if (binding) next[key] = binding; else delete next[key]; onChange({ ...placement, arguments: next }); };

  return <section className="space-y-4 rounded-md border border-border bg-muted/15 p-4">
    <div className="flex items-start justify-between gap-3"><div><p className="text-sm font-medium">{manifest?.name ?? "Фича страницы"}</p><p className="mt-1 text-xs text-muted-foreground">{placement.build.featureKey}@{placement.build.version}</p></div><Button type="button" variant="ghost" size="icon" className="size-11 shrink-0" aria-label="Удалить фичу со страницы" onClick={onRemove}><Trash2 /></Button></div>
    {!build && <p role={buildsLoading ? "status" : "alert"} className={`rounded-md border p-3 text-sm ${buildsLoading ? "border-border text-muted-foreground" : "border-warning/40 bg-warning/10 text-warning"}`}>
      {buildsLoading ? "Загружаем manifest закреплённой сборки…" : <>
        Manifest этой сборки отсутствует в реестре, поэтому аргументы и входы модели пока нельзя настроить. Зарегистрируйте исходный ZIP в <Link className="font-medium underline underline-offset-4" to={`/snapshots/registry${snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : ""}`}>реестре сборок</Link>, затем вернитесь к этой странице.
      </>}
    </p>}
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem]"><div className="space-y-1.5 text-sm"><span className="text-muted-foreground">Закреплённая сборка</span><p className="min-h-11 rounded-md border border-border bg-muted/30 px-3 py-2">{placement.build.featureKey}@{placement.build.version} · версия меняется для всех размещений через состав фичей</p></div>
      <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Место на странице</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={placement.slot} onChange={(event) => onChange({ ...placement, slot: event.currentTarget.value })}>{(manifest?.slots ?? ["main"]).map((slot) => <option key={slot} value={slot}>{slot}</option>)}</select></label></div>
    {Object.entries(fields).length > 0 && <FeatureArgumentsEditor fields={fields} requiredKeys={requiredArgumentKeys} arguments={placement.arguments} captures={uniqueCaptures} preparation={build && snapshotId ? { snapshotId, featureKey: build.key, buildVersion: build.version, schemaKind: "arguments", buildDigest: build.digest } : undefined} onChange={(key, binding) => patchBinding(key, binding)} />}
    {manifest && <StructuralBindingsEditor page={page} placement={placement} manifest={manifest} config={config} captures={uniqueCaptures} snapshotId={snapshotId} onChange={onChange} />}
    {manifest && <DetailLinkEditor placement={placement} formatVersion={manifest.formatVersion} fields={uniqueDataCollectionFields} detailRoutes={detailRoutes} onChange={onChange} />}
    {manifest?.requests && <BackendRequestsEditor slots={manifest.requests} placement={placement} fields={fields} captures={uniqueCaptures} config={config} onChange={onChange} />}
  </section>;
}
