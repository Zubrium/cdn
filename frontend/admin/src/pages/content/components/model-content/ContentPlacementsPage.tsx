import { useDeferredValue, useMemo, useState } from "react";
import { ArrowLeft, ChevronDown, Layers3, Search } from "lucide-react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { pluralRu } from "@/lib/format";
import type { DataModel, ModelContentWorkspace } from "@/types";
import { contentHref } from "../../model-content-paths";
import { PlacementSelections } from "./PlacementSelections";
import type { ModelContentDraft, ModelContentDraftUpdater } from "./types";

type Placement = ModelContentWorkspace["placements"][number];

/** Initial disclosure state; later user choices are kept across searches. */
export function placementSectionDefaults(placements: Placement[]): Record<string, boolean> {
  const defaults: Record<string, boolean> = {};
  const firstPageId = placements[0]?.pageId;
  const countByPage = new Map<string, number>();
  for (const placement of placements) {
    countByPage.set(placement.pageId, (countByPage.get(placement.pageId) ?? 0) + 1);
  }
  for (const placement of placements) {
    defaults[`placement:${placement.id}`] = countByPage.get(placement.pageId) === 1;
    defaults[`page:${placement.pageId}`] = placement.pageId === firstPageId;
  }
  return defaults;
}

export function ContentPlacementsPage({
  model,
  workspace,
  draft,
  savedDraft,
  disabled,
  updateDraft,
  confirmCleanup,
}: {
  model: DataModel;
  workspace: ModelContentWorkspace;
  draft: ModelContentDraft;
  savedDraft: ModelContentDraft;
  disabled: boolean;
  updateDraft: ModelContentDraftUpdater;
  confirmCleanup?: (title: string, description: string) => Promise<boolean>;
}) {
  const [search, setSearch] = useState("");
  const [openSections, setOpenSections] = useState<Record<string, boolean>>(() => placementSectionDefaults(workspace.placements));
  const deferredSearch = useDeferredValue(search.trim().toLocaleLowerCase("ru"));
  const grouped = useMemo(() => {
    const entities = new Map(model.entities.map((entity) => [entity.id, entity.name]));
    const placements = workspace.placements.filter((placement) => {
      if (!deferredSearch) return true;
      const text = [
        placement.pageName,
        placement.featureName,
        placement.featureKey,
        placement.version,
        ...placement.inputs.flatMap((input) => [input.inputKey, entities.get(input.entityId) ?? ""]),
      ].join(" ").toLocaleLowerCase("ru");
      return text.includes(deferredSearch);
    });
    const pages = new Map<string, { id: string; name: string; placements: Placement[] }>();
    for (const placement of placements) {
      const group = pages.get(placement.pageId) ?? { id: placement.pageId, name: placement.pageName, placements: [] };
      group.placements.push(placement);
      pages.set(placement.pageId, group);
    }
    return [...pages.values()];
  }, [deferredSearch, model.entities, workspace.placements]);

  const inputCount = workspace.placements.reduce((sum, placement) => sum + placement.inputs.length, 0);
  return <div className="space-y-6">
    <Link to={contentHref("/content", workspace.snapshot.id)} className="inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft className="size-4" />Контент
    </Link>

    <header className="border-b border-border pb-5">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">Записи на страницах</h1>
        <Badge variant="outline">{workspace.placements.length} размещений</Badge>
      </div>
      <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">Выберите, какие опубликованные записи получает каждый вход каждой фичи. Порядок коллекции сохраняется отдельно для каждого размещения.</p>
    </header>

    {workspace.placements.length > 0 && <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
      <label className="relative min-w-0 flex-1 sm:max-w-lg">
        <span className="sr-only">Поиск размещений</span>
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
        <Input type="search" value={search} onChange={(event) => setSearch(event.currentTarget.value)} placeholder="Страница, фича или вход данных" className="min-h-11 pl-9" />
      </label>
      <p className="text-xs text-muted-foreground">Всего входов данных: {inputCount}</p>
    </div>}

    {workspace.placements.length === 0 ? <section className="border-y border-dashed border-border py-14 text-center">
      <Layers3 className="mx-auto size-6 text-muted-foreground" aria-hidden="true" />
      <h2 className="mt-3 font-medium">Нет размещений с данными</h2>
      <p className="mt-1 text-sm text-muted-foreground">Добавьте в фичи входы данных и разместите их на страницах сайта.</p>
    </section> : grouped.length === 0 ? <p className="border-y border-dashed border-border py-12 text-center text-sm text-muted-foreground">Совпадений нет.</p> : <div className="divide-y divide-border border-y border-border">
      {grouped.map((page) => {
        const pageInputCount = page.placements.reduce((sum, placement) => sum + placement.inputs.length, 0);
        const pageKey = `page:${page.id}`;
        return <details key={page.id} open={openSections[pageKey] ?? false} onToggle={(event) => setOpenSections((current) => ({ ...current, [pageKey]: event.currentTarget.open }))} className="group/page">
          <summary className="flex min-h-14 cursor-pointer list-none items-center gap-3 px-1 py-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
            <ChevronDown className="size-4 shrink-0 text-muted-foreground transition-transform group-open/page:rotate-180" />
            <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{page.name}</span><span className="mt-0.5 block text-xs text-muted-foreground">{page.placements.length} {pluralRu(page.placements.length, ["фича", "фичи", "фич"])} · {pageInputCount} {pluralRu(pageInputCount, ["вход", "входа", "входов"])}</span></span>
          </summary>
          <div className="space-y-2 pb-4 pl-7">
            {page.placements.map((placement, placementIndex) => {
              const placementKey = `placement:${placement.id}`;
              return <details key={placement.id} open={openSections[placementKey] ?? false} onToggle={(event) => setOpenSections((current) => ({ ...current, [placementKey]: event.currentTarget.open }))} className="border-t border-border/70">
                <summary className="flex min-h-12 cursor-pointer list-none items-center gap-3 py-2.5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                  <ChevronDown className="size-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
                  <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{placement.featureName}</span><span className="mt-0.5 block text-xs text-muted-foreground">Версия {placement.version} · {placement.inputs.length} {pluralRu(placement.inputs.length, ["вход", "входа", "входов"])}</span></span>
                  {placementIndex === 0 && <Badge variant="muted">первая на странице</Badge>}
                </summary>
                <div className="pb-3 pl-6">
                  <PlacementSelections
                    model={model}
                    workspace={{ ...workspace, placements: [placement] }}
                    draft={draft}
                    savedDraft={savedDraft}
                    disabled={disabled}
                    updateDraft={updateDraft}
                    confirmCleanup={confirmCleanup}
                  />
                </div>
              </details>;
            })}
          </div>
        </details>;
      })}
    </div>}
  </div>;
}
