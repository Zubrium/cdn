import { useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Search } from "lucide-react";
import { api } from "@/api";
import { ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { SnapshotTreeItem } from "./components/snapshots-page/SnapshotTreeItem";

export default function SnapshotsPage() {
  useDocumentTitle("Дерево снапшотов");
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [params] = useSearchParams();
  const pageID = params.get("page_id") ?? "";
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const createAttempt = useRef<{ fingerprint: string; key: string } | null>(null);
  const snapshots = useQuery({
    queryKey: ["site", "snapshot-choices", pageID],
    queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal, pageID || undefined),
    refetchInterval: (query) => query.state.data?.items.some((item) => item.state === "provisioning") ? 5000 : false,
  });

  const create = async (parentSnapshotId: string) => {
    const trimmedName = name.trim();
    if (!trimmedName || busy) return;
    setBusy(true);
    try {
      const parent = await api.siteBuilder.snapshot(parentSnapshotId);
      const localeConfig = await api.contentEngine.localeConfig();
      const parentContent = await api.contentEngine.workspace(parentSnapshotId, localeConfig.default_locale);
      if (parent.snapshot.state !== "ready" || parent.snapshot.revision === undefined ||
        !Number.isSafeInteger(parentContent.revision) || parentContent.revision < 0) {
        throw new Error("Не удалось проверить ревизии родительского снапшота. Обновите страницу и повторите действие.");
      }
      const input = {
        pageID: parent.snapshot.pageId,
        name: trimmedName,
        parentSnapshotId,
        expectedParentRevision: parent.snapshot.revision,
        expectedParentContentRevision: parentContent.revision,
      };
      const fingerprint = JSON.stringify(input);
      if (!createAttempt.current || createAttempt.current.fingerprint !== fingerprint) {
        createAttempt.current = { fingerprint, key: `snapshot-${crypto.randomUUID()}` };
      }
      const created = await api.siteBuilder.createSnapshotFrom({ ...input, idempotencyKey: createAttempt.current.key });
      createAttempt.current = null;
      setName("");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] }),
        queryClient.invalidateQueries({ queryKey: ["website", "snapshots"] }),
      ]);
      toast.success(created.state === "ready"
        ? `Создан снапшот #${created.sequence}`
        : `Снапшот #${created.sequence} создан и готовится.`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Не удалось создать снапшот");
    } finally {
      setBusy(false);
    }
  };

  const remove = async (id: string) => {
    if (!(await confirm({ title: "Удалить снапшот?", description: "Дочерние снапшоты сохранят содержимое и будут перепривязаны к родителю. Действие необратимо.", confirmLabel: "Удалить", destructive: true }))) return;
    setBusy(true);
    try {
      const snapshot = snapshots.data?.items.find((item) => item.id === id);
      if (!snapshot?.revision) throw new Error("Обновите список снапшотов перед удалением.");
      await api.siteBuilder.deleteSnapshot(id, snapshot.revision);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] }),
        queryClient.invalidateQueries({ queryKey: ["website", "snapshots"] }),
      ]);
      toast.success("Снапшот удалён");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Ошибка удаления");
    } finally {
      setBusy(false);
    }
  };

  if (snapshots.isLoading) return <LoadingPage />;
  if (snapshots.error) return <ErrorState error={snapshots.error} onRetry={() => void snapshots.refetch()} />;

  const needle = query.trim().toLocaleLowerCase("ru");
  const visibleSnapshots = (snapshots.data?.items ?? []).filter((snapshot) =>
    `#${snapshot.sequence} ${snapshot.name} ${snapshot.parentName ?? ""} ${snapshot.pageId}`.toLocaleLowerCase("ru").includes(needle));

  return (
    <div className="page-grid">
      <PageHeader title="Дерево снапшотов" description="Снапшоты изолированы по странице. Назначения веток здесь не управляются." />
      <div className="flex justify-end"><Button type="button" variant="outline" onClick={() => void snapshots.refetch()} loading={snapshots.isFetching}>Обновить состояние</Button></div>
      {pageID && <p className="text-sm text-muted-foreground">Фильтр page_id: <code>{pageID}</code></p>}
      <div className="flex flex-wrap items-end gap-2">
        <label className="min-w-0 flex-1 space-y-1 sm:max-w-sm">
          <span className="text-xs font-medium text-muted-foreground">Название дочернего снапшота</span>
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Например, Изменение главной" aria-label="Название дочернего снапшота" maxLength={120} />
        </label>
      </div>
      <p className="text-sm text-muted-foreground">Корневые снапшоты создаются вместе со страницами. Здесь можно добавить дочерний снапшот к выбранному.</p>
      <label className="relative block max-w-xl">
        <span className="sr-only">Поиск снапшотов</span>
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
        <Input type="search" value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Номер, название или page_id" className="min-h-11 pl-9" />
      </label>
      <ul aria-label="Дерево снапшотов" className="divide-y divide-border rounded-xl border border-border bg-card">
        {visibleSnapshots.map((snapshot) => (
          <SnapshotTreeItem
            key={snapshot.id}
            snapshot={snapshot}
            canCreate={Boolean(name.trim())}
            busy={busy}
            onCreateChild={(snapshotId) => void create(snapshotId)}
            onDelete={(snapshotId) => void remove(snapshotId)}
          />
        ))}
      </ul>
      {snapshots.data?.items.length !== 0 && visibleSnapshots.length === 0 && <p className="py-8 text-center text-sm text-muted-foreground">Снапшоты не найдены.</p>}
      {snapshots.data?.items.length === 0 && <p className="text-sm text-muted-foreground">Снапшотов нет. Создайте страницу, чтобы получить корневой снапшот.</p>}
    </div>
  );
}
