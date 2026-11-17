import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ListTodo, RefreshCw, RotateCcw } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/api";
import type { CoordinatorJobState } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { errorMessage } from "@/lib/errors";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { EmptyState, ErrorState, PageHeader, TableSkeleton, useDocumentTitle } from "@/components/common/page";

const stateLabels: Record<CoordinatorJobState, string> = {
  queued: "В очереди",
  running: "Выполняется",
  succeeded: "Готово",
  failed: "Ошибка",
  cancelled: "Отменено",
};

const stateVariants: Record<CoordinatorJobState, "default" | "info" | "success" | "destructive" | "muted"> = {
  queued: "muted",
  running: "info",
  succeeded: "success",
  failed: "destructive",
  cancelled: "muted",
};

const stateOptions: Array<{ value: CoordinatorJobState | "all"; label: string }> = [
  { value: "all", label: "Все состояния" },
  { value: "queued", label: "В очереди" },
  { value: "running", label: "Выполняются" },
  { value: "succeeded", label: "Завершённые" },
  { value: "failed", label: "С ошибкой" },
  { value: "cancelled", label: "Отменённые" },
];

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function ForbiddenState() {
  return (
    <div className="rounded-md border border-border bg-card p-5">
      <h2 className="text-sm font-semibold">Нет доступа к заданиям</h2>
      <p className="mt-1 text-sm leading-6 text-muted-foreground">
        Для просмотра нужен уровень 1 в настроенной группе заданий. Повторный запуск доступен при уровне 2.
      </p>
    </div>
  );
}

export default function CoordinatorJobsPage() {
  useDocumentTitle("Задания Coordinator");
  const queryClient = useQueryClient();
  const [state, setState] = useState<CoordinatorJobState | "all">("all");
  const [typeInput, setTypeInput] = useState("");
  const [type, setType] = useState("");
  const listParams = useMemo(() => ({ state: state === "all" ? "" : state, type }), [state, type]);

  const jobs = useInfiniteQuery({
    queryKey: queryKeys.coordinator.jobList(listParams),
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) => api.coordinator.jobs.list({
      state: listParams.state || undefined,
      type: listParams.type || undefined,
      limit: 50,
      before: pageParam || undefined,
    }, signal),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    retry: false,
  });

  const permissions = useQuery({
    queryKey: queryKeys.coordinator.jobPermissions,
    queryFn: ({ signal }) => api.coordinator.jobs.permissions(signal),
    retry: false,
  });

  const retry = useMutation({
    mutationFn: (id: string) => api.coordinator.jobs.retry(id),
    onSuccess: async () => {
      toast.success("Задание поставлено в очередь повторно");
      await queryClient.invalidateQueries({ queryKey: queryKeys.coordinator.jobs });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const rows = jobs.data?.pages.flatMap((page) => page.items) ?? [];
  const listForbidden = jobs.error instanceof ApiError && jobs.error.isForbidden;
  const permissionsUnavailable = Boolean(permissions.error && !(permissions.error instanceof ApiError && permissions.error.isForbidden));

  return (
    <div className="page-grid">
      <PageHeader
        title="Задания Coordinator"
        description="Состояние фоновой обработки и повторный запуск завершившихся с ошибкой заданий."
        actions={(
          <Button variant="outline" onClick={() => void jobs.refetch()} loading={jobs.isFetching}>
            <RefreshCw />
            Обновить
          </Button>
        )}
      />

      {listForbidden ? <ForbiddenState /> : jobs.isError ? (
        <ErrorState error={jobs.error} title="Не удалось загрузить задания" onRetry={() => void jobs.refetch()} />
      ) : (
        <>
          <section aria-label="Фильтры заданий" className="flex flex-col gap-3 rounded-md border border-border bg-card p-3 sm:flex-row sm:items-end">
            <label className="flex min-w-0 flex-1 flex-col gap-1.5 text-xs font-medium text-muted-foreground">
              Тип задания
              <div className="flex gap-2">
                <Input
                  value={typeInput}
                  onChange={(event) => setTypeInput(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") setType(typeInput.trim());
                  }}
                  placeholder="Например, archive.inspect/v1"
                  aria-label="Фильтр по типу задания"
                />
                <Button type="button" variant="secondary" onClick={() => setType(typeInput.trim())}>Применить</Button>
              </div>
            </label>
            <label className="flex w-full flex-col gap-1.5 text-xs font-medium text-muted-foreground sm:w-56">
              Состояние
              <select
                value={state}
                onChange={(event) => setState(event.target.value as CoordinatorJobState | "all")}
                className="h-11 rounded-md border border-input bg-background px-3 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {stateOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
              </select>
            </label>
          </section>

          {permissionsUnavailable && (
            <p role="status" className="text-sm text-warning">
              Не удалось определить право повторного запуска. Повторите загрузку страницы, прежде чем менять задания.
            </p>
          )}

          {jobs.isLoading ? (
            <div className="overflow-hidden rounded-md border border-border bg-card p-4"><TableSkeleton rows={6} /></div>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={ListTodo}
              title={type || state !== "all" ? "Задания не найдены" : "Очередь пока пуста"}
              description={type || state !== "all" ? "Измените фильтры и повторите поиск." : "Новые фоновые задания появятся здесь."}
            />
          ) : (
            <div className="overflow-x-auto rounded-md border border-border bg-card">
              <table className="w-full min-w-[760px] border-collapse text-left text-sm">
                <thead className="border-b border-border bg-muted/40 text-xs text-muted-foreground">
                  <tr>
                    <th scope="col" className="px-4 py-3 font-medium">Задание</th>
                    <th scope="col" className="px-4 py-3 font-medium">Состояние</th>
                    <th scope="col" className="px-4 py-3 font-medium">Попытки</th>
                    <th scope="col" className="px-4 py-3 font-medium">Создано</th>
                    <th scope="col" className="px-4 py-3 font-medium">Последняя ошибка</th>
                    <th scope="col" aria-label="Действия" className="px-4 py-3 font-medium" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {rows.map((job) => (
                    <tr key={job.id} className="align-top">
                      <td className="max-w-[22rem] px-4 py-3">
                        <div className="truncate font-medium" title={job.type}>{job.type}</div>
                        <code className="mt-1 block truncate text-xs text-muted-foreground" title={job.id}>{job.id}</code>
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant={stateVariants[job.state]}>{stateLabels[job.state]}</Badge>
                      </td>
                      <td className="whitespace-nowrap px-4 py-3 tabular-nums">{job.attempts} / {job.max_attempts}</td>
                      <td className="whitespace-nowrap px-4 py-3 text-muted-foreground">{formatDate(job.created_at)}</td>
                      <td className="max-w-48 truncate px-4 py-3 font-mono text-xs text-muted-foreground" title={job.error_code ?? undefined}>{job.error_code ?? "—"}</td>
                      <td className="px-4 py-3 text-right">
                        {job.state === "failed" && permissions.data?.can_retry && (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            loading={retry.isPending && retry.variables === job.id}
                            disabled={retry.isPending || permissionsUnavailable}
                            onClick={() => retry.mutate(job.id)}
                            aria-label={`Повторить задание ${job.type}`}
                          >
                            <RotateCcw />
                            Повторить
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {jobs.hasNextPage && (
            <div className="flex justify-center">
              <Button variant="outline" loading={jobs.isFetchingNextPage} onClick={() => void jobs.fetchNextPage()}>
                Загрузить ещё
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
