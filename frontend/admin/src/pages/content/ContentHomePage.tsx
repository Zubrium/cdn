import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useLocation, useParams, useSearchParams } from "react-router-dom";
import { api } from "@/api";
import { EmptyState, ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { ModelContentEditor } from "./model-content-editor";
import { Button } from "@/components/ui/button";

export default function ContentHomePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const location = useLocation();
  const { entityKey = "", recordId = "" } = useParams();
  const requestedSnapshotID = searchParams.get("snapshot") ?? "";
  const requestedSiteID = searchParams.get("site_id") ?? "";
  const requestedPageID = searchParams.get("page_id") ?? "";
  const snapshots = useQuery({
    queryKey: ["website", "snapshots", "content", requestedSiteID, requestedPageID],
    queryFn: ({ signal }) => api.website.allSnapshots(signal, requestedSiteID || undefined, requestedPageID || undefined),
  });
  const allSnapshots = useMemo(() => snapshots.data ?? [], [snapshots.data]);
  const readySnapshots = useMemo(() => allSnapshots.filter((snapshot) => snapshot.state === "ready"), [allSnapshots]);
  const requestedSnapshot = allSnapshots.find((snapshot) => snapshot.id === requestedSnapshotID);
  const selectedSnapshot = requestedSnapshot
    ? requestedSnapshot.state === "ready" ? requestedSnapshot : undefined
    : readySnapshots[0];

  useDocumentTitle("Контент");

  useEffect(() => {
    if (!selectedSnapshot || (requestedSnapshot && requestedSnapshot.id !== selectedSnapshot.id)) return;
    if (requestedSnapshotID === selectedSnapshot.id && requestedSiteID === selectedSnapshot.site_id && requestedPageID === selectedSnapshot.page_id) return;
    const next = new URLSearchParams(searchParams);
    next.set("snapshot", selectedSnapshot.id);
    next.set("site_id", selectedSnapshot.site_id);
    next.set("page_id", selectedSnapshot.page_id);
    setSearchParams(next, { replace: true });
  }, [requestedPageID, requestedSiteID, requestedSnapshot, requestedSnapshotID, searchParams, selectedSnapshot, setSearchParams]);

  const workspace = useQuery({
    queryKey: ["content", "model-workspace", selectedSnapshot?.id],
    queryFn: ({ signal }) => api.contentEngine.workspace(selectedSnapshot!.id, signal),
    enabled: Boolean(selectedSnapshot),
  });

  if (snapshots.isLoading) return <LoadingPage label="Загружаем снапшоты…" />;
  if (snapshots.error) return <div className="page-grid"><ErrorState error={snapshots.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshots.refetch()} /></div>;

  if (requestedSnapshot && requestedSnapshot.state !== "ready") {
    return <div className="page-grid"><PageHeader title="Контент" description="Данные появятся после завершения копирования в дочерний снапшот." /><EmptyState title="Снапшот готовится" description="Пока Website и Content завершают durable workflow, черновики и публикации недоступны." action={<Button type="button" variant="outline" onClick={() => void snapshots.refetch()} loading={snapshots.isFetching}>Обновить состояние</Button>} /></div>;
  }
  if (!selectedSnapshot) {
    return <div className="page-grid"><PageHeader title="Контент" description="Записи и их публикации для выбранной структуры сайта." /><EmptyState title="Нет готовых снапшотов" description={allSnapshots.length ? "Дождитесь завершения подготовки снапшота." : "Создайте снапшот в разделе «Сайт», затем добавьте в нём модель данных."} action={allSnapshots.length ? <Button type="button" variant="outline" onClick={() => void snapshots.refetch()} loading={snapshots.isFetching}>Обновить состояние</Button> : undefined} /></div>;
  }
  if (workspace.isLoading) return <LoadingPage label="Загружаем контент…" />;
  if (workspace.error) return <div className="page-grid"><ErrorState error={workspace.error} title="Не удалось загрузить контент" onRetry={() => void workspace.refetch()} /></div>;
  if (!workspace.data) return <LoadingPage />;

  const view = location.pathname.endsWith("/content/placements")
    ? "placements"
    : recordId ? "record" : entityKey ? "entity" : "overview";

  return <ModelContentEditor
    key={selectedSnapshot.id}
    snapshots={readySnapshots}
    workspace={workspace.data}
    view={view}
    entityKey={entityKey}
    recordId={recordId}
    onSnapshotChange={(snapshotID) => {
      const next = new URLSearchParams(searchParams);
      next.set("snapshot", snapshotID);
      const nextSnapshot = readySnapshots.find((candidate) => candidate.id === snapshotID);
      if (nextSnapshot) {
        next.set("site_id", nextSnapshot.site_id);
        next.set("page_id", nextSnapshot.page_id);
      }
      setSearchParams(next);
    }}
  />;
}
