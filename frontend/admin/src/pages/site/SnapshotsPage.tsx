import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/api";
import { ErrorState, LoadingPage, PageHeader } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { SiteConfig } from "@/types";
import { SitepackImportAction } from "./SitepackActions";
import { SnapshotTreeItem } from "./components/snapshots-page/SnapshotTreeItem";

function emptySiteConfig(): SiteConfig {
  const pageId = crypto.randomUUID();
  return {
    formatVersion: 3,
    pages: [{ id: pageId, name: "Главная", kind: "page", placements: [] }],
    routeTree: { id: crypto.randomUUID(), type: "site", children: [{ id: crypto.randomUUID(), type: "path", path: "/", pageId, children: [] }] },
  };
}

export default function SnapshotsPage() {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const snapshots = useQuery({ queryKey: ["site", "snapshot-choices"], queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal) });
  const branches = useQuery({ queryKey: ["branches"], queryFn: ({ signal }) => api.siteBuilder.branches(signal) });

  const create = async (parentSnapshotId?: string) => {
    const trimmedName = name.trim();
    if (!trimmedName || busy) return;
    setBusy(true);
    try {
      const parent = parentSnapshotId ? await api.siteBuilder.snapshot(parentSnapshotId) : null;
      if (parentSnapshotId && parent?.snapshot.contentRevision === undefined) {
        throw new Error("Не удалось проверить ревизию родительского снапшота. Обновите страницу и повторите действие.");
      }
      const created = await api.siteBuilder.createSnapshotFrom({
        name: trimmedName,
        ...(parentSnapshotId
          ? { parentSnapshotId, expectedParentContentRevision: parent!.snapshot.contentRevision }
          : { config: emptySiteConfig() }),
      });
      setName("");
      await queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] });
      toast.success(`Снапшот #${created.sequence} создан. Назначьте его в разделе «Ветки», если он должен обслуживать сайт.`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Не удалось создать снапшот");
    } finally {
      setBusy(false);
    }
  };

  const remove = async (id: string) => {
    if (!(await confirm({ title: "Удалить снапшот?", description: "Его дочерние снапшоты сохранят содержимое и будут перепривязаны к родителю. Действие необратимо.", confirmLabel: "Удалить", destructive: true }))) return;
    setBusy(true);
    try {
      await api.siteBuilder.deleteSnapshot(id);
      await queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] });
      toast.success("Снапшот удалён");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Ошибка удаления");
    } finally {
      setBusy(false);
    }
  };

  if (snapshots.isLoading || branches.isLoading) return <LoadingPage />;
  if (snapshots.error || branches.error) return <ErrorState error={snapshots.error ?? branches.error} onRetry={() => { void snapshots.refetch(); void branches.refetch(); }} />;

  const protectedIds = new Set<string>();
  for (const branch of branches.data?.items ?? []) {
    if (!branch.snapshotId) continue;
    protectedIds.add(branch.snapshotId);
    const parent = snapshots.data?.items.find((item) => item.id === branch.snapshotId)?.parentSnapshotId;
    if (parent) protectedIds.add(parent);
  }

  return (
    <div className="page-grid">
      <PageHeader title="Дерево снапшотов" description="Изменения текущего сайта сохраняются потомком. Создайте пустой корневой снапшот для независимого сайта; назначение ветке выполняется отдельно в разделе «Ветки»." />
      <div className="flex flex-wrap items-end gap-2">
        <label className="min-w-0 flex-1 space-y-1 sm:max-w-sm">
          <span className="text-xs font-medium text-muted-foreground">Название нового сайта или снапшота</span>
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Например, Каталог или Новый стенд" aria-label="Название нового сайта или снапшота" maxLength={160} />
        </label>
        <Button disabled={!name.trim() || busy} onClick={() => void create()}>
          {busy ? "Создаём…" : "Создать пустой сайт"}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">Создаст независимый корневой снапшот с пустой главной страницей без фич и контента. Текущий выбор и назначения веток не изменятся.</p>
      <ul aria-label="Дерево снапшотов" className="divide-y divide-border rounded-xl border border-border bg-card">
        {snapshots.data?.items.map((snapshot) => {
          const assigned = branches.data?.items.filter((branch) => branch.snapshotId === snapshot.id).map((branch) => branch.name) ?? [];
          const protectedSnapshot = protectedIds.has(snapshot.id);
          return (
            <SnapshotTreeItem
              key={snapshot.id}
              snapshot={snapshot}
              assignedBranchNames={assigned}
              protectedSnapshot={protectedSnapshot}
              canCreate={Boolean(name.trim())}
              busy={busy}
              onCreateChild={(snapshotId) => void create(snapshotId)}
              onDelete={(snapshotId) => void remove(snapshotId)}
            />
          );
        })}
      </ul>
      {snapshots.data?.items.length === 0 && <p className="text-sm text-muted-foreground">Создайте пустой сайт или настройте текущий сайт в редакторе.</p>}
      <SitepackImportAction />
    </div>
  );
}
