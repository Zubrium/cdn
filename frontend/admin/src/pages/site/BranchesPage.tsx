import { useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { WebsiteBranch, WebsiteSnapshot } from "@/types";

function BranchHistory({ branchId }: { branchId: string }) {
  const [open, setOpen] = useState(false);
  const history = useInfiniteQuery({
    queryKey: ["branch-history", branchId],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) => api.website.branchEvents(branchId, pageParam, undefined, signal),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
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
          <span>#{event.snapshot_sequence} · {event.snapshot_name}</span>
          <time className="text-xs text-muted-foreground" dateTime={event.created_at}>{new Date(event.created_at).toLocaleString("ru-RU")}</time>
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
  const branches = useQuery({ queryKey: ["website", "branches"], queryFn: ({ signal }) => api.website.branches(signal) });
  const snapshots = useQuery({ queryKey: ["website", "snapshots", "all"], queryFn: ({ signal }) => api.website.allSnapshots(signal) });

  const assign = async (branch: WebsiteBranch, id: string) => {
    const target = snapshots.data?.find((snapshot) => snapshot.id === id && snapshot.site_id === branch.site_id);
    if (!target) {
      toast.error("Выберите снапшот сайта этой ветки");
      return;
    }
    setBusy({ branchId: branch.id, phase: "checking" });
    try {
      const detail = await api.website.snapshot(id);
      if (detail.site_id !== branch.site_id) throw new Error("Снапшот относится к другому сайту");
      const accepted = await confirm({
        title: `Назначить снапшот ветке «${branch.name}»?`,
        description: (
          <div className="space-y-3">
            <p>Ветка начнёт использовать снапшот #{target.sequence} · {target.name}.</p>
            <p>Изменится только эта ветка. Соседние ветки и сам снапшот останутся без изменений.</p>
          </div>
        ),
        confirmLabel: "Назначить",
      });
      if (!accepted) return;
      setBusy({ branchId: branch.id, phase: "assigning" });
      await api.website.assignBranch(branch.id, id, branch.revision, detail.revision);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["website", "branches"] }),
        queryClient.invalidateQueries({ queryKey: ["branch-history", branch.id] }),
      ]);
      toast.success("Снапшот назначен");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Не удалось назначить снапшот");
      await queryClient.invalidateQueries({ queryKey: ["website", "branches"] });
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
        const branchSnapshots = snapshots.data?.filter((snapshot) => snapshot.site_id === branch.site_id) ?? [];
        const requested = choices[branch.id] ?? params.get("snapshot") ?? branch.snapshot_id ?? "";
        const selected = branchSnapshots.some((snapshot) => snapshot.id === requested) ? requested : branch.snapshot_id ?? "";
        const current: WebsiteSnapshot | undefined = branchSnapshots.find((item) => item.id === branch.snapshot_id);
        return (
          <section key={branch.id} className="space-y-4 rounded-xl border border-border bg-card p-4">
            <div>
              <h2 className="font-semibold">{branch.name}</h2>
              <p className="text-xs text-muted-foreground">{branch.listen_address} · {branch.indexable ? "Индексация разрешена" : "Индексация закрыта"}</p>
              <p className="mt-2 text-sm">{current ? `#${current.sequence} · ${current.name}` : "Снапшот не назначен"}</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Select value={selected} onValueChange={(value) => setChoices((currentChoices) => ({ ...currentChoices, [branch.id]: value }))} disabled={busy !== null || !branch.enabled}>
                <SelectTrigger className="w-full sm:w-80" aria-label={`Снапшот ветки ${branch.name}`}><SelectValue placeholder="Выберите снапшот" /></SelectTrigger>
                <SelectContent>{branchSnapshots.map((item) => <SelectItem key={item.id} value={item.id}>#{item.sequence} · {item.name}</SelectItem>)}</SelectContent>
              </Select>
              <Button disabled={!selected || busy !== null || selected === branch.snapshot_id || !branch.enabled} onClick={() => void assign(branch, selected)}>{busy?.branchId === branch.id ? busy.phase === "checking" ? "Проверяем…" : "Назначаем…" : "Назначить"}</Button>
              {current?.parent_id && <Button variant="outline" disabled={busy !== null || !branch.enabled} onClick={() => void assign(branch, current.parent_id!)}>Вернуться к родителю</Button>}
              {current && <Button variant="ghost" asChild><Link to={`/content?snapshot=${current.id}`}>Контент</Link></Button>}
            </div>
            <BranchHistory branchId={branch.id} />
          </section>
        );
      })}
    </div>
  );
}
