import { useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { BranchRecord, SiteSnapshot } from "@/types";

function BranchHistory({ branchId }: { branchId: string }) {
  const [open, setOpen] = useState(false);
  const history = useInfiniteQuery({
    queryKey: ["branch-history", branchId],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) => api.siteBuilder.snapshotEvents(branchId, pageParam, signal),
    getNextPageParam: (page) => page.nextCursor,
    enabled: open,
  });
  const events = history.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <details open={open} onToggle={(event) => setOpen(event.currentTarget.open)} className="border-t border-border pt-3">
      <summary className="cursor-pointer text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">История назначений</summary>
      {history.isLoading && <p className="mt-2 text-sm text-muted-foreground">Загружаем историю…</p>}
      {history.error && <ErrorState error={history.error} title="Не удалось загрузить историю" onRetry={() => void history.refetch()} />}
      {open && !history.isLoading && !history.error && events.length === 0 && <p className="mt-2 text-sm text-muted-foreground">Назначений пока не было.</p>}
      {events.length > 0 && <ol className="mt-2 divide-y divide-border">{events.map((event) => (
        <li key={event.id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
          <span>#{event.sequence} · {event.name}</span>
          <time className="text-xs text-muted-foreground" dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleString("ru-RU")}</time>
        </li>
      ))}</ol>}
      {history.hasNextPage && <Button variant="outline" size="sm" disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>Показать ещё</Button>}
    </details>
  );
}

export default function BranchesPage() {
  useDocumentTitle("Ветки");
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [params] = useSearchParams();
  const [choices, setChoices] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<{ branchId: string; phase: "checking" | "assigning" } | null>(null);
  const branches = useQuery({ queryKey: ["branches"], queryFn: ({ signal }) => api.siteBuilder.branches(signal) });
  const snapshots = useQuery({ queryKey: ["site", "snapshot-choices"], queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal) });

  const assign = async (branch: BranchRecord, id: string) => {
    const target = snapshots.data?.items.find((snapshot) => snapshot.id === id);
    setBusy({ branchId: branch.id, phase: "checking" });
    try {
      const detail = await api.siteBuilder.snapshot(id);
      const accepted = await confirm({
        title: `Назначить снапшот ветке «${branch.name}»?`,
        description: (
          <div className="space-y-3">
            <p>Ветка начнёт отдавать #{target?.sequence ?? detail.snapshot.sequence} · {target?.name ?? detail.snapshot.name} с опубликованным контентом. Черновики останутся скрыты.</p>
            <p>Черновики контента останутся скрыты. Новое назначение затронет только эту ветку.</p>
          </div>
        ),
        confirmLabel: "Назначить",
      });
      if (!accepted) return;
      setBusy({ branchId: branch.id, phase: "assigning" });
      await api.siteBuilder.assignBranch(branch.id, id, branch.revision, detail.snapshot.contentRevision ?? 0);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["branches"] }),
        queryClient.invalidateQueries({ queryKey: ["branch-history", branch.id] }),
      ]);
      toast.success("Снапшот назначен");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Не удалось назначить снапшот");
      await queryClient.invalidateQueries({ queryKey: ["branches"] });
    } finally {
      setBusy(null);
    }
  };

  if (branches.isLoading || snapshots.isLoading) return <LoadingPage />;
  if (branches.error || snapshots.error) return <ErrorState error={branches.error ?? snapshots.error} onRetry={() => { void branches.refetch(); void snapshots.refetch(); }} />;

  return (
    <div className="page-grid">
      <PageHeader title="Ветки" description="Состав, порты, CORS и индексацию задаёт конфигурация запуска. Здесь назначаются снапшоты." />
      {branches.data?.items.map((branch) => {
        const selected = choices[branch.id] ?? params.get("snapshot") ?? branch.snapshotId ?? "";
        const current: SiteSnapshot | undefined = snapshots.data?.items.find((item) => item.id === branch.snapshotId);
        return (
          <section key={branch.id} className="space-y-4 rounded-xl border border-border bg-card p-4">
            <div>
              <h2 className="font-semibold">{branch.name}</h2>
              <p className="text-xs text-muted-foreground">{branch.httpAddr} · {branch.indexable ? "Индексация разрешена" : "Индексация закрыта"}</p>
              <p className="mt-2 text-sm">{current ? `#${current.sequence} · ${current.name}` : "Сайт ещё не настроен"}</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Select value={selected} onValueChange={(value) => setChoices((currentChoices) => ({ ...currentChoices, [branch.id]: value }))} disabled={busy !== null}>
                <SelectTrigger className="w-full sm:w-80" aria-label={`Снапшот ветки ${branch.name}`}><SelectValue placeholder="Выберите снапшот" /></SelectTrigger>
                <SelectContent>{snapshots.data?.items.map((item) => <SelectItem key={item.id} value={item.id}>#{item.sequence} · {item.name}</SelectItem>)}</SelectContent>
              </Select>
              <Button disabled={!selected || busy !== null || selected === branch.snapshotId} onClick={() => void assign(branch, selected)}>{busy?.branchId === branch.id ? busy.phase === "checking" ? "Проверяем…" : "Назначаем…" : "Назначить"}</Button>
              {current?.parentSnapshotId && <Button variant="outline" disabled={busy !== null} onClick={() => void assign(branch, current.parentSnapshotId!)}>Вернуться к родителю</Button>}
              {current && <Button variant="ghost" asChild><Link to={`/content?snapshot=${current.id}`}>Контент</Link></Button>}
            </div>
            <BranchHistory branchId={branch.id} />
          </section>
        );
      })}
    </div>
  );
}
