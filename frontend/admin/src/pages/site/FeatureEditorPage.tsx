import { ArrowLeft } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api";
import { useConfirm } from "@/components/common/confirm";
import { ErrorState, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { useSiteWorkspace } from "./SiteWorkspace";
import { removePlacement } from "./feature-selection";
import { PlacementEditor } from "./components/page-editor/PlacementEditor";
import { buildFor } from "./components/page-editor/schema";

export default function FeatureEditorPage() {
  const { pageId = "", placementId = "" } = useParams();
  const { config, update } = useSiteWorkspace();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const page = config.pages.find((item) => item.id === pageId);
  const placement = page?.placements.find((item) => item.id === placementId);
  const buildsQuery = useQuery({ queryKey: ["site", "feature-builds"], queryFn: ({ signal }) => api.website.featureBuilds(signal) });
  const build = buildsQuery.data && placement ? buildFor(buildsQuery.data.items, placement) : undefined;
  useDocumentTitle(build?.name ?? "Настройки фичи");
  const snapshotId = searchParams.get("snapshot") ?? "";
  const query = snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : "";
  const pageURL = `/site/pages/${pageId}${query}`;

  if (buildsQuery.isError) return <ErrorState error={buildsQuery.error} title="Не удалось загрузить сборки фич" onRetry={() => void buildsQuery.refetch()} />;
  if (!page || !placement) {
    return <div className="flex h-full items-center justify-center p-6 text-center">
      <div><h1 className="text-xl font-semibold">Фича не найдена</h1><p className="mt-2 text-sm text-muted-foreground">Размещение могло быть удалено из страницы.</p><Button asChild variant="outline" className="mt-5 min-h-11"><Link to={pageURL}><ArrowLeft />К странице</Link></Button></div>
    </div>;
  }

  return <div className="h-full overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-6xl px-4 py-5 sm:px-7 sm:py-7">
      <Link to={pageURL} className="inline-flex min-h-11 items-center gap-2 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" />К странице «{page.name}»</Link>
      <header className="mt-3 border-b border-border pb-5">
        <p className="text-xs text-muted-foreground">Настройки размещения</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">{build?.name ?? "Неизвестная фича"}</h1>
        <p className="mt-1 text-sm text-muted-foreground">Версия {placement.build.version} · слот {placement.slot}</p>
      </header>
      <div className="py-6">
        <PlacementEditor
          page={page}
          placement={placement}
          builds={buildsQuery.data?.items ?? []}
          config={config}
          snapshotId={snapshotId}
          buildsLoading={buildsQuery.isLoading}
          onChange={(next) => update((current) => ({
            ...current,
            pages: current.pages.map((item) => item.id === page.id
              ? { ...item, placements: item.placements.map((candidate) => candidate.id === placement.id ? next : candidate) }
              : item),
          }))}
          onRemove={async () => {
            if (!(await confirm({ title: "Удалить фичу?", description: `«${build?.name ?? "Фича"}» будет удалена со страницы в рабочей копии.`, confirmLabel: "Удалить фичу", destructive: true }))) return;
            update(removePlacement(config, page.id, placement.id));
            navigate(pageURL);
          }}
        />
      </div>
    </div>
  </div>;
}
