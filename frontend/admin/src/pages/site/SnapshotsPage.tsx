import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Search } from "lucide-react";
import { api } from "@/api";
import { ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
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
  useDocumentTitle("Дерево снапшотов");
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [selectedSiteID, setSelectedSiteID] = useState("");
  const [busy, setBusy] = useState(false);
  const snapshots = useQuery({ queryKey: ["site", "snapshot-choices"], queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal) });
  const branches = useQuery({ queryKey: ["website", "branches"], queryFn: ({ signal }) => api.website.branches(signal) });

  const create = async (parentSnapshotId?: string) => {
    const trimmedName = name.trim();
    if (!trimmedName || busy) return;
    setBusy(true);
    try {
      const parent = parentSnapshotId ? await api.siteBuilder.snapshot(parentSnapshotId) : null;
      if (parentSnapshotId && (parent?.snapshot.contentRevision === undefined || parent.snapshot.revision === undefined)) {
        throw new Error("Не удалось проверить ревизии родительского снапшота. Обновите страницу и повторите действие.");
      }
      const configuredSites = [...new Map((branches.data?.items ?? []).map((branch) => [branch.site_id, branch.name])).entries()];
      const siteID = parent?.snapshot.siteId || selectedSiteID || (configuredSites.length === 1 ? configuredSites[0]?.[0] : undefined);
      if (!siteID) throw new Error("Выберите сайт из статической конфигурации веток.");
      const created = await api.siteBuilder.createSnapshotFrom({
        siteID,
        name: trimmedName,
        ...(parentSnapshotId
          ? { parentSnapshotId, expectedParentRevision: parent!.snapshot.revision, expectedParentContentRevision: parent!.snapshot.contentRevision }
          : {
              config: emptySiteConfig(),
              metadata: { formatVersion: 1, siteName: trimmedName, pages: {} },
              designSystem: { formatVersion: 2, groups: [], baseTokens: [], featureValues: {}, breakpoints: [], fonts: [] },
            }),
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
      const snapshot = snapshots.data?.items.find((item) => item.id === id);
      if (!snapshot?.revision) throw new Error("Обновите список снапшотов перед удалением.");
      await api.siteBuilder.deleteSnapshot(id, snapshot.revision);
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
  const configuredSites = [...new Map((branches.data?.items ?? []).map((branch) => [branch.site_id, branch.name])).entries()];
  const targetSiteID = selectedSiteID || (configuredSites.length === 1 ? configuredSites[0]![0] : "");
  for (const branch of branches.data?.items ?? []) {
    if (!branch.snapshot_id) continue;
    protectedIds.add(branch.snapshot_id);
    const parent = snapshots.data?.items.find((item) => item.id === branch.snapshot_id)?.parentSnapshotId;
    if (parent) protectedIds.add(parent);
  }
  const needle = query.trim().toLocaleLowerCase("ru");
  const visibleSnapshots = (snapshots.data?.items ?? []).filter((snapshot) => {
    if (!needle) return true;
    const branchNames = (branches.data?.items ?? []).filter((branch) => branch.snapshot_id === snapshot.id).map((branch) => branch.name).join(" ");
    return `#${snapshot.sequence} ${snapshot.name} ${snapshot.parentName ?? ""} ${branchNames}`.toLocaleLowerCase("ru").includes(needle);
  });

  return (
    <div className="page-grid">
      <PageHeader title="Дерево снапшотов" description="Изменения текущего сайта сохраняются потомком. Создайте пустой корневой снапшот для независимого сайта; назначение ветке выполняется отдельно в разделе «Ветки»." />
      <div className="flex flex-wrap items-end gap-2">
        <label className="min-w-0 flex-1 space-y-1 sm:max-w-sm">
          <span className="text-xs font-medium text-muted-foreground">Название нового сайта или снапшота</span>
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Например, Каталог или Новый стенд" aria-label="Название нового сайта или снапшота" maxLength={160} />
        </label>
        <label className="min-w-0 space-y-1 sm:w-64">
          <span className="text-xs font-medium text-muted-foreground">Сайт из конфигурации веток</span>
          <select aria-label="Сайт из конфигурации веток" className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm" value={targetSiteID} onChange={(event) => setSelectedSiteID(event.target.value)}>
            <option value="">Выберите сайт</option>
            {configuredSites.map(([siteID]) => <option key={siteID} value={siteID}>Сайт · {siteID}</option>)}
          </select>
        </label>
        <Button disabled={!name.trim() || !targetSiteID || busy} onClick={() => void create()}>
          {busy ? "Создаём…" : "Создать корневой снапшот"}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">Создаст независимый корневой снапшот с пустой главной страницей без фич и контента. Сайт выбирается из статической конфигурации; назначения веток не изменятся.</p>
      <label className="relative block max-w-xl">
        <span className="sr-only">Поиск снапшотов</span>
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
        <Input type="search" value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Номер, название или ветка" className="min-h-11 pl-9" />
      </label>
      <ul aria-label="Дерево снапшотов" className="divide-y divide-border rounded-xl border border-border bg-card">
        {visibleSnapshots.map((snapshot) => {
          const assigned = branches.data?.items.filter((branch) => branch.snapshot_id === snapshot.id).map((branch) => branch.name) ?? [];
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
      {snapshots.data?.items.length !== 0 && visibleSnapshots.length === 0 && <p className="py-8 text-center text-sm text-muted-foreground">Снапшоты не найдены.</p>}
      {snapshots.data?.items.length === 0 && <p className="text-sm text-muted-foreground">Создайте пустой сайт или настройте текущий сайт в редакторе.</p>}
      <SitepackImportAction />
    </div>
  );
}
