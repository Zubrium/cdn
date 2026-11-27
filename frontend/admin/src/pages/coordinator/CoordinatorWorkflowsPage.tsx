import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, RefreshCw, RotateCcw, Workflow as WorkflowIcon } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/api";
import type { CoordinatorTask, CoordinatorWorkflowState } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { errorMessage } from "@/lib/errors";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState, ErrorState, PageHeader, TableSkeleton, useDocumentTitle } from "@/components/common/page";

const stateLabels: Record<CoordinatorWorkflowState, string> = {
  queued: "В очереди",
  running: "Выполняется",
  succeeded: "Завершён",
  failed: "Ошибка",
  cancelled: "Отменён",
};

const stateVariants: Record<CoordinatorWorkflowState | "skipped", "default" | "info" | "success" | "destructive" | "muted"> = {
  queued: "muted",
  running: "info",
  succeeded: "success",
  failed: "destructive",
  cancelled: "muted",
  skipped: "muted",
};

const stateOptions: Array<{ value: CoordinatorWorkflowState | "all"; label: string }> = [
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

function shortID(value: string): string {
  return value.length > 13 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function ForbiddenState() {
  return (
    <div className="rounded-md border border-border bg-card p-5">
      <h2 className="text-sm font-semibold">Нет доступа к workflow</h2>
      <p className="mt-1 text-sm leading-6 text-muted-foreground">
        Для просмотра нужен уровень 1 в настроенной группе Coordinator. Повторный запуск worker-задачи доступен при уровне 2.
      </p>
    </div>
  );
}

export default function CoordinatorWorkflowsPage() {
  const { workflowId } = useParams();
  return workflowId ? <WorkflowDetails workflowId={workflowId} /> : <WorkflowList />;
}

function WorkflowList() {
  useDocumentTitle("Workflows Coordinator");
  const [state, setState] = useState<CoordinatorWorkflowState | "all">("all");
  const listParams = useMemo(() => ({ state: state === "all" ? "" : state }), [state]);
  const workflows = useInfiniteQuery({
    queryKey: queryKeys.coordinator.workflowList(listParams),
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) => api.coordinator.workflows.list({
      state: listParams.state || undefined,
      limit: 50,
      before: pageParam || undefined,
    }, signal),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    retry: false,
  });

  const rows = workflows.data?.pages.flatMap((page) => page.items) ?? [];
  const listForbidden = workflows.error instanceof ApiError && workflows.error.isForbidden;

  return (
    <div className="page-grid">
      <PageHeader
        title="Workflows Coordinator"
        description="Состояние workflow и количество задач в каждом графе."
        actions={(
          <Button variant="outline" onClick={() => void workflows.refetch()} loading={workflows.isFetching}>
            <RefreshCw />
            Обновить
          </Button>
        )}
      />

      {listForbidden ? <ForbiddenState /> : workflows.isError ? (
        <ErrorState error={workflows.error} title="Не удалось загрузить workflow" onRetry={() => void workflows.refetch()} />
      ) : (
        <>
          <section aria-label="Фильтр workflow" className="flex flex-col gap-3 rounded-md border border-border bg-card p-3 sm:flex-row sm:items-end">
            <label className="flex w-full flex-col gap-1.5 text-xs font-medium text-muted-foreground sm:w-64">
              Состояние
              <select
                value={state}
                onChange={(event) => setState(event.target.value as CoordinatorWorkflowState | "all")}
                className="h-11 rounded-md border border-input bg-background px-3 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {stateOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
              </select>
            </label>
          </section>

          {workflows.isLoading ? (
            <div className="overflow-hidden rounded-md border border-border bg-card p-4"><TableSkeleton rows={6} /></div>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={WorkflowIcon}
              title={state !== "all" ? "Workflow не найдены" : "Workflow пока нет"}
              description={state !== "all" ? "Выберите другое состояние и повторите поиск." : "Запуски зарегистрированных операций появятся здесь."}
            />
          ) : (
            <div className="overflow-x-auto rounded-md border border-border bg-card">
              <table className="w-full min-w-[720px] border-collapse text-left text-sm">
                <thead className="border-b border-border bg-muted/40 text-xs text-muted-foreground">
                  <tr>
                    <th scope="col" className="px-4 py-3 font-medium">Workflow</th>
                    <th scope="col" className="px-4 py-3 font-medium">Владелец</th>
                    <th scope="col" className="px-4 py-3 font-medium">Состояние</th>
                    <th scope="col" className="px-4 py-3 font-medium">Задач</th>
                    <th scope="col" className="px-4 py-3 font-medium">Создан</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {rows.map((workflow) => (
                    <tr key={workflow.id} className="align-top">
                      <td className="px-4 py-3">
                        <Link to={`/coordinator/workflows/${encodeURIComponent(workflow.id)}`} className="font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                          {shortID(workflow.id)}
                        </Link>
                        <code className="mt-1 block max-w-sm truncate text-xs text-muted-foreground" title={workflow.id}>{workflow.id}</code>
                      </td>
                      <td className="px-4 py-3 font-mono text-xs">{workflow.owner_engine_id}</td>
                      <td className="px-4 py-3"><Badge variant={stateVariants[workflow.state]}>{stateLabels[workflow.state]}</Badge></td>
                      <td className="px-4 py-3 tabular-nums">{workflow.task_count}</td>
                      <td className="whitespace-nowrap px-4 py-3 text-muted-foreground">{formatDate(workflow.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {workflows.hasNextPage && (
            <div className="flex justify-center">
              <Button variant="outline" loading={workflows.isFetchingNextPage} onClick={() => void workflows.fetchNextPage()}>
                Загрузить ещё
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function WorkflowDetails({ workflowId }: { workflowId: string }) {
  useDocumentTitle("Workflow Coordinator");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workflow = useInfiniteQuery({
    queryKey: queryKeys.coordinator.workflow(workflowId),
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) => api.coordinator.workflows.detail(workflowId, {
      limit: 100,
      before: pageParam || undefined,
    }, signal),
    getNextPageParam: (lastPage) => lastPage.tasks.next_cursor ?? undefined,
    retry: false,
  });
  const permissions = useQuery({
    queryKey: queryKeys.coordinator.workflowPermissions,
    queryFn: ({ signal }) => api.coordinator.workflows.permissions(signal),
    retry: false,
  });
  const retry = useMutation({
    mutationFn: (task: CoordinatorTask) => api.coordinator.workflows.retryTask(workflowId, task.id),
    onSuccess: async () => {
      toast.success("Worker-задача поставлена в очередь повторно");
      await queryClient.invalidateQueries({ queryKey: queryKeys.coordinator.workflow(workflowId) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.coordinator.workflows });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const firstPage = workflow.data?.pages[0];
  const tasks = useMemo(() => workflow.data?.pages.flatMap((page) => page.tasks.items) ?? [], [workflow.data?.pages]);
  const orderedTasks = useMemo(() => [...tasks].sort((left, right) => left.created_at.localeCompare(right.created_at)), [tasks]);
  const detailForbidden = workflow.error instanceof ApiError && workflow.error.isForbidden;
  const permissionsUnavailable = Boolean(permissions.error && !(permissions.error instanceof ApiError && permissions.error.isForbidden));

  return (
    <div className="page-grid">
      <PageHeader
        title="Workflow"
        description={firstPage ? `${firstPage.workflow.owner_engine_id} · ${shortID(firstPage.workflow.id)}` : "Граф задач и состояние выполнения."}
        actions={(
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => navigate("/coordinator/workflows")}>
              <ArrowLeft />
              К списку
            </Button>
            <Button variant="outline" onClick={() => void workflow.refetch()} loading={workflow.isFetching}>
              <RefreshCw />
              Обновить
            </Button>
          </div>
        )}
      />

      {detailForbidden ? <ForbiddenState /> : workflow.isError ? (
        <ErrorState error={workflow.error} title="Не удалось загрузить workflow" onRetry={() => void workflow.refetch()} />
      ) : workflow.isLoading || !firstPage ? (
        <div className="overflow-hidden rounded-md border border-border bg-card p-4"><TableSkeleton rows={5} /></div>
      ) : (
        <>
          <section className="grid gap-3 rounded-md border border-border bg-card p-4 sm:grid-cols-3" aria-label="Сводка workflow">
            <div>
              <div className="text-xs text-muted-foreground">Состояние</div>
              <div className="mt-2"><Badge variant={stateVariants[firstPage.workflow.state]}>{stateLabels[firstPage.workflow.state]}</Badge></div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">Корневая задача</div>
              <code className="mt-2 block truncate text-xs" title={firstPage.workflow.root_task_id}>{firstPage.workflow.root_task_id}</code>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">Создан</div>
              <div className="mt-2 text-sm">{formatDate(firstPage.workflow.created_at)}</div>
            </div>
          </section>

          {permissionsUnavailable && (
            <p role="status" className="text-sm text-warning">
              Не удалось определить право повторного запуска. Обновите страницу перед retry.
            </p>
          )}

          <section aria-label="Граф задач workflow" className="rounded-md border border-border bg-card p-4">
            <div className="mb-4 flex items-center gap-2">
              <WorkflowIcon className="size-4 text-muted-foreground" aria-hidden="true" />
              <h2 className="text-sm font-semibold">Граф задач</h2>
              <span className="text-xs text-muted-foreground">{tasks.length}{workflow.hasNextPage ? "+" : ""}</span>
            </div>
            {orderedTasks.length === 0 ? (
              <p className="text-sm text-muted-foreground">В этом workflow пока нет задач.</p>
            ) : (
              <ol className="space-y-3">
                {orderedTasks.map((task, index) => (
                  <TaskNode
                    key={task.id}
                    task={task}
                    index={index}
                    retrying={retry.isPending && retry.variables?.id === task.id}
                    retryDisabled={retry.isPending || permissionsUnavailable || !permissions.data?.can_retry_tasks}
                    onRetry={() => retry.mutate(task)}
                  />
                ))}
              </ol>
            )}
            {workflow.hasNextPage && (
              <div className="mt-4 flex justify-center">
                <Button variant="outline" loading={workflow.isFetchingNextPage} onClick={() => void workflow.fetchNextPage()}>
                  Загрузить следующие задачи
                </Button>
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}

function TaskNode({
  task,
  index,
  retrying,
  retryDisabled,
  onRetry,
}: {
  task: CoordinatorTask;
  index: number;
  retrying: boolean;
  retryDisabled: boolean;
  onRetry: () => void;
}) {
  const workerLabel = task.worker ? `${task.worker.component_id} · ${task.worker.operation_id}/v${task.worker.contract_version}` : "Барьер";
  return (
    <li className="relative pl-7">
      {index > 0 && <span aria-hidden="true" className="absolute bottom-full left-[0.68rem] top-0 border-l border-border" />}
      <span aria-hidden="true" className="absolute left-1 top-5 size-3 rounded-full border-2 border-primary bg-background" />
      <article className="rounded-md border border-border bg-background p-3 sm:p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{task.kind === "worker" ? "Worker task" : "Join task"}</span>
              <Badge variant={stateVariants[task.state]}>{stateLabels[task.state as CoordinatorWorkflowState] ?? "Пропущена"}</Badge>
            </div>
            <div className="mt-2 truncate text-sm font-medium" title={workerLabel}>{workerLabel}</div>
            <code className="mt-1 block break-all text-xs text-muted-foreground">{task.id}</code>
          </div>
          {task.kind === "worker" && task.state === "failed" && (
            <Button type="button" variant="outline" size="sm" loading={retrying} disabled={retryDisabled} onClick={onRetry}>
              <RotateCcw />
              Retry задачи
            </Button>
          )}
        </div>
        <div className="mt-3 flex flex-wrap gap-x-5 gap-y-2 text-xs text-muted-foreground">
          <span>Попытки: {task.attempts} / {task.max_attempts}</span>
          <span>Создана: {formatDate(task.created_at)}</span>
          {task.error_code && <span className="font-mono text-destructive">{task.error_code}</span>}
        </div>
        {task.depends_on.length > 0 && (
          <div className="mt-3 border-t border-border pt-3">
            <div className="text-xs text-muted-foreground">Зависит от</div>
            <ul className="mt-1 flex flex-wrap gap-2">
              {task.depends_on.map((dependencyID) => (
                <li key={dependencyID}><code className="rounded bg-muted px-1.5 py-1 text-xs" title={dependencyID}>{shortID(dependencyID)}</code></li>
              ))}
            </ul>
          </div>
        )}
      </article>
    </li>
  );
}
