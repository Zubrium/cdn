import { useEffect, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useLocation, useParams, useSearchParams } from "react-router-dom";
import { api } from "@/api";
import { EmptyState, ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { ModelContentEditor } from "./model-content-editor";

export default function ContentHomePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const location = useLocation();
  const { entityKey = "", recordId = "" } = useParams();
  const requestedSnapshotID = searchParams.get("snapshot") ?? "";
  const snapshots = useQuery({
    queryKey: ["website", "snapshots", "content"],
    queryFn: ({ signal }) => api.website.allSnapshots(signal),
    refetchInterval: (query) => query.state.data?.some((snapshot) => snapshot.state === "provisioning") ? 2000 : false,
  });
  const allSnapshots = useMemo(() => snapshots.data ?? [], [snapshots.data]);
  const readySnapshots = useMemo(() => allSnapshots.filter((snapshot) => snapshot.state === "ready"), [allSnapshots]);
  const requestedSnapshot = allSnapshots.find((snapshot) => snapshot.id === requestedSnapshotID);
  const selectedSnapshot = requestedSnapshot
    ? requestedSnapshot.state === "ready" ? requestedSnapshot : undefined
    : readySnapshots[0];

  useDocumentTitle("Контент");

  useEffect(() => {
    if (!selectedSnapshot || requestedSnapshotID === selectedSnapshot.id || requestedSnapshot) return;
    const next = new URLSearchParams(searchParams);
    next.set("snapshot", selectedSnapshot.id);
    setSearchParams(next, { replace: true });
  }, [requestedSnapshot, requestedSnapshotID, searchParams, selectedSnapshot, setSearchParams]);

  const workspace = useQuery({
    queryKey: ["content", "model-workspace", selectedSnapshot?.id],
    queryFn: ({ signal }) => api.contentEngine.workspace(selectedSnapshot!.id, signal),
    enabled: Boolean(selectedSnapshot),
  });

  if (snapshots.isLoading) return <LoadingPage label="Загружаем снапшоты…" />;
  if (snapshots.error) return <div className="page-grid"><ErrorState error={snapshots.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshots.refetch()} /></div>;

  if (requestedSnapshot && requestedSnapshot.state !== "ready") {
    return <div className="page-grid"><PageHeader title="Контент" description="Данные появятся после завершения копирования в дочерний снапшот." /><EmptyState title="Снапшот готовится" description="Пока Website и Content завершают durable workflow, черновики и публикации недоступны." /></div>;
  }
  if (!selectedSnapshot) {
    return <div className="page-grid"><PageHeader title="Контент" description="Записи и их публикации для выбранной структуры сайта." /><EmptyState title="Нет готовых снапшотов" description={allSnapshots.length ? "Дождитесь завершения подготовки снапшота." : "Создайте снапшот в разделе «Сайт», затем добавьте в нём модель данных."} /></div>;
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
      setSearchParams(next);
    }}
  />;
}
