import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Database } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "@/api";
import { ErrorState, LoadingPage, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { queryKeys } from "@/lib/query-keys";
import type { SiteSnapshot } from "@/types";
import { ModelContentEditor, modelWorkspaceQueryKey } from "./model-content-editor";

function DataModelSetupPage({ snapshot }: { snapshot?: SiteSnapshot }) {
  const target = snapshot ? `/site/data-model?snapshot=${encodeURIComponent(snapshot.id)}` : "/site";
  const hasSnapshot = Boolean(snapshot);
  return <div className="page-grid">
    <section className="mx-auto flex max-w-2xl flex-col items-center rounded-xl border border-dashed border-border px-6 py-12 text-center">
      <span className="mb-4 grid size-12 place-items-center rounded-full bg-muted text-muted-foreground"><Database aria-hidden="true" /></span>
      <h1 className="text-xl font-semibold">{hasSnapshot ? "Сначала настройте модель данных" : "Нет снапшотов для редактирования"}</h1>
      <p className="mt-2 max-w-lg text-sm text-muted-foreground">
        {hasSnapshot
          ? "Редактор контента строит формы из модели выбранного снапшота. Создайте сущности и поля в редакторе сайта."
          : "Сначала создайте корневой снапшот и модель данных в редакторе сайта. Значения записей будут храниться в контексте выбранного снапшота."}
      </p>
      <Button asChild className="mt-5 min-h-11"><Link to={target}>{hasSnapshot ? "Открыть модель данных" : "Открыть редактор сайтов"}</Link></Button>
    </section>
  </div>;
}

export default function ContentHomePage() {
  useDocumentTitle("Редактор контента");
  const [searchParams, setSearchParams] = useSearchParams();
  const queryClient = useQueryClient();
  const snapshotQuery = useQuery({
    queryKey: queryKeys.content.snapshots,
    queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal),
  });
  const snapshots = snapshotQuery.data?.items ?? [];
  const requestedSnapshotId = searchParams.get("snapshot") ?? "";
  const selectedSnapshot = snapshots.find((snapshot) => snapshot.id === requestedSnapshotId) ?? snapshots[0] ?? null;
  const snapshotId = selectedSnapshot?.id ?? "";

  useEffect(() => {
    if (!selectedSnapshot || requestedSnapshotId === selectedSnapshot.id) return;
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.set("snapshot", selectedSnapshot.id);
      next.delete("feature");
      next.delete("record");
      next.delete("settings");
      return next;
    }, { replace: true });
  }, [requestedSnapshotId, selectedSnapshot, setSearchParams]);

  const modelQuery = useQuery({
    queryKey: modelWorkspaceQueryKey(snapshotId),
    queryFn: ({ signal }) => api.siteBuilder.modelContentWorkspace(snapshotId, signal),
    enabled: Boolean(snapshotId),
  });

  if (snapshotQuery.isLoading) return <LoadingPage label="Загружаем снапшоты…" />;
  if (snapshotQuery.error) return <div className="page-grid"><ErrorState error={snapshotQuery.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshotQuery.refetch()} /></div>;
  if (!snapshotId) return <DataModelSetupPage />;
  if (modelQuery.isLoading) return <LoadingPage label="Загружаем модель контента…" />;
  if (modelQuery.error) return <div className="page-grid"><ErrorState error={modelQuery.error} title="Не удалось загрузить контент снапшота" onRetry={() => void modelQuery.refetch()} /></div>;
  if (!modelQuery.data?.dataModel) return <DataModelSetupPage snapshot={selectedSnapshot ?? undefined} />;

  return <ModelContentEditor
    key={snapshotId}
    snapshots={snapshots}
    workspace={modelQuery.data}
    onSnapshotChange={(nextId) => {
      setSearchParams((current) => {
        const next = new URLSearchParams(current);
        next.set("snapshot", nextId);
        next.delete("feature");
        next.delete("record");
        next.delete("settings");
        return next;
      });
      void queryClient.invalidateQueries({ queryKey: modelWorkspaceQueryKey(nextId) });
    }}
  />;
}
