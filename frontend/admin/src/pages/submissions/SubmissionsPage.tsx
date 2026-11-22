import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Download, Trash2 } from "lucide-react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { EmptyState, ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canManageSubmissions, canReadFormSchemas, canViewSubmissions } from "@/lib/permissions";
import { errorMessage } from "@/lib/errors";
import { downloadBlob } from "@/lib/format";
import type { FormSubmission } from "@/types";

const pageSize = 100;
const statusValues = ["new", "in_progress", "processed", "rejected"];

function displayValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value, null, 2);
}

function summary(values: Record<string, unknown>): string {
  return Object.values(values).filter((value) => value !== null && value !== "").slice(0, 3).map((value) => typeof value === "object" ? "…" : String(value)).join(" · ");
}

function dateTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

type SubmissionFilter = "form" | "key" | "state";
type SetSubmissionParams = ReturnType<typeof useSearchParams>[1];

/** Filters update in place so back navigation does not step through every keystroke. */
export function updateSubmissionFilter(
  current: URLSearchParams,
  name: SubmissionFilter,
  value: string,
  setParams: SetSubmissionParams,
): void {
  const next = new URLSearchParams(current);
  if (value) next.set(name, value);
  else next.delete(name);
  setParams(next, { replace: true });
}

export default function SubmissionsPage() {
  useDocumentTitle("Заявки");
  const { submissionId = "" } = useParams();
  const { principal } = useAuth();
  const canView = canViewSubmissions(principal);
  const canManage = canManageSubmissions(principal);
  const canReadSchemas = canReadFormSchemas(principal);
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [pendingStatusId, setPendingStatusId] = useState("");
  const [statusError, setStatusError] = useState("");
  const schemas = useInfiniteQuery({
    queryKey: ["formsEngine", "schemas"],
    queryFn: ({ pageParam, signal }) => api.formsEngine.schemas(pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: canReadSchemas,
  });
  const schemaItems = useMemo(() => schemas.data?.pages.flatMap((page) => page.items) ?? [], [schemas.data]);
  const requestedFormID = params.get("form") ?? "";
  const formID = canReadSchemas
    ? (schemaItems.some((schema) => schema.id === requestedFormID) ? requestedFormID : schemaItems[0]?.id ?? "")
    : requestedFormID;
  const key = params.get("key") ?? "";
  const state = params.get("state") ?? "";
  useEffect(() => {
    if (!formID || requestedFormID === formID) return;
    const next = new URLSearchParams(params);
    next.set("form", formID);
    setParams(next, { replace: true });
  }, [formID, params, requestedFormID, setParams]);

  const submissions = useInfiniteQuery({
    queryKey: ["formsEngine", "submissions", formID, key, state],
    queryFn: ({ pageParam, signal }) => api.formsEngine.submissions(formID, { key: key || undefined, state: state || undefined }, pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: Boolean(formID && canView),
  });
  const submissionItems = useMemo(() => submissions.data?.pages.flatMap((page) => page.items) ?? [], [submissions.data]);
  const selectedForm = schemaItems.find((item) => item.id === formID);
  const exportMutation = useMutation({
    mutationFn: () => api.formsEngine.exportSubmissions(formID, { key: key || undefined, state: state || undefined }),
    onSuccess: ({ blob, filename }) => {
      downloadBlob(blob, filename);
      toast.success("Экспорт CSV готов");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const detail = useQuery({
    queryKey: ["formsEngine", "submission", submissionId],
    queryFn: ({ signal }) => api.formsEngine.submission(submissionId, signal),
    enabled: Boolean(submissionId && canView),
  });

  const statusMutation = useMutation({
    mutationFn: (next: string) => {
      const current = detail.data;
      if (!current) throw new Error("Заявка ещё не загружена");
      return api.formsEngine.setSubmissionStatus(current.id, next, current.revision);
    },
    onMutate: () => {
      setStatusError("");
      setPendingStatusId(detail.data?.id ?? "");
    },
    onSuccess: async (updated) => {
      queryClient.setQueryData(["formsEngine", "submission", updated.id], updated);
      await queryClient.invalidateQueries({ queryKey: ["formsEngine", "submissions", formID] });
      toast.success("Статус заявки изменён");
    },
    onError: (error) => {
      const message = errorMessage(error);
      setStatusError(message);
      toast.error(message);
    },
    onSettled: () => setPendingStatusId(""),
  });

  const deleteMutation = useMutation({
    mutationFn: async (submission: FormSubmission) => {
      const accepted = await confirm({
        title: "Удалить заявку и вложения?",
        description: "Заявка и все её вложения будут удалены окончательно. Действие нельзя отменить.",
        confirmLabel: "Удалить заявку",
        destructive: true,
      });
      if (!accepted) return false;
      await api.formsEngine.deleteSubmission(submission.id, submission.revision);
      return true;
    },
    onSuccess: async (deleted) => {
      if (!deleted) return;
      await queryClient.invalidateQueries({ queryKey: ["formsEngine", "submissions", formID] });
      await queryClient.removeQueries({ queryKey: ["formsEngine", "submission", submissionId] });
      toast.success("Заявка удалена");
      navigate(`/submissions?form=${encodeURIComponent(formID)}`);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  if (!canView) return <div className="page-grid"><PageHeader title="Заявки" description="Ответы на опубликованные формы." /><EmptyState title="Нет доступа к заявкам" description="Для просмотра заявок требуется доступ к группе forms_submissions." /></div>;
  if (canReadSchemas && schemas.isLoading) return <LoadingPage label="Загружаем формы…" />;
  if (canReadSchemas && schemas.error) return <div className="page-grid"><ErrorState error={schemas.error} title="Не удалось загрузить формы" onRetry={() => void schemas.refetch()} /></div>;
  if (!formID) return <div className="page-grid"><PageHeader title="Заявки" description="Ответы на опубликованные формы." /><EmptyState title="Форма не выбрана" description={canReadSchemas ? "Создайте форму в разделе «Формы» или откройте ссылку с идентификатором формы." : "Для просмотра заявок откройте ссылку с идентификатором формы."} /></div>;

  if (submissionId) {
    if (detail.isLoading) return <LoadingPage label="Загружаем заявку…" />;
    if (detail.error) return <div className="page-grid"><ErrorState error={detail.error} title="Не удалось загрузить заявку" onRetry={() => void detail.refetch()} /><Button type="button" variant="outline" onClick={() => navigate(`/submissions?form=${encodeURIComponent(formID)}`)}><ArrowLeft />К заявкам</Button></div>;
    if (!detail.data) return <div className="page-grid"><EmptyState title="Заявка не найдена" /></div>;
    const submission = detail.data;
    return <div className="page-grid">
      <PageHeader title="Заявка" description={`${selectedForm?.key ?? submission.form_id} · ${dateTime(submission.created_at)}`} backTo={`/submissions?form=${encodeURIComponent(formID)}`} backLabel="К списку заявок" actions={<Badge variant="outline">{submission.state}</Badge>} />
      {canManage && <div className="flex flex-wrap items-end gap-2"><label className="space-y-1 text-sm"><span className="text-muted-foreground">Статус</span><select aria-label="Статус заявки" aria-busy={pendingStatusId === submission.id} className="h-11 min-w-48 rounded-md border border-input bg-background px-3" value={submission.state} disabled={statusMutation.isPending} onChange={(event) => statusMutation.mutate(event.currentTarget.value)}>{[...new Set([...statusValues, submission.state])].map((value) => <option key={value} value={value}>{value}</option>)}</select></label><Button type="button" variant="destructive" className="min-h-11" loading={deleteMutation.isPending} onClick={() => void deleteMutation.mutate(submission)}><Trash2 />Удалить</Button></div>}
      {statusError && <p role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">Не удалось изменить статус: {statusError}</p>}
      <section className="overflow-hidden rounded-lg border border-border bg-card"><h2 className="border-b border-border px-4 py-3 text-sm font-semibold">Ответы</h2><dl className="divide-y divide-border">{Object.entries(submission.values).map(([key, value]) => <div key={key} className="grid gap-1 px-4 py-3 sm:grid-cols-[14rem_minmax(0,1fr)] sm:gap-4"><dt className="break-words text-xs font-medium text-muted-foreground">{key}</dt><dd className="min-w-0 whitespace-pre-wrap break-words text-sm">{displayValue(value)}</dd></div>)}</dl></section>
      <p className="text-xs text-muted-foreground">Ответ неизменяем. Ревизия метаданных: {submission.revision}.</p>
    </div>;
  }

  return <div className="page-grid">
    <PageHeader title="Заявки" description="Ответы на опубликованные формы. Содержание заявки неизменяемо." actions={<>
      <Button type="button" variant="outline" className="min-h-11" disabled={!formID || !canView} loading={exportMutation.isPending} onClick={() => exportMutation.mutate()}><Download />Экспорт CSV</Button>
      {canReadSchemas && <label className="space-y-1.5 text-sm"><span className="sr-only">Форма</span><select aria-label="Форма" className="h-11 min-w-48 rounded-md border border-input bg-background px-3" value={formID} onChange={(event) => updateSubmissionFilter(params, "form", event.currentTarget.value, setParams)}>{schemaItems.map((schema) => <option key={schema.id} value={schema.id}>{schema.key}</option>)}</select></label>}
      <label className="space-y-1.5 text-sm"><span className="sr-only">Ключ формы</span><Input aria-label="Ключ формы" className="h-11 w-40" value={key} onChange={(event) => updateSubmissionFilter(params, "key", event.currentTarget.value, setParams)} placeholder="Ключ формы" /></label>
      <label className="space-y-1.5 text-sm"><span className="sr-only">Статус</span><select aria-label="Статус" className="h-11 min-w-36 rounded-md border border-input bg-background px-3" value={state} onChange={(event) => updateSubmissionFilter(params, "state", event.currentTarget.value, setParams)}><option value="">Все статусы</option>{[...new Set([...statusValues, ...submissionItems.map((item) => item.state)])].map((value) => <option key={value} value={value}>{value}</option>)}</select></label>
    </>} />
    {canReadSchemas && schemas.hasNextPage && <div className="-mt-5"><Button type="button" variant="ghost" size="sm" onClick={() => void schemas.fetchNextPage()} loading={schemas.isFetchingNextPage}>Загрузить ещё формы</Button></div>}
    {submissions.isLoading ? <p role="status" className="py-12 text-center text-sm text-muted-foreground">Загружаем заявки…</p> : submissions.error ? <ErrorState error={submissions.error} title="Не удалось загрузить заявки" onRetry={() => void submissions.refetch()} /> : submissionItems.length === 0 ? <EmptyState title="Заявок пока нет" description="Новые ответы появятся после отправки опубликованной формы." /> : <div className="overflow-hidden rounded-lg border border-border bg-card"><ul className="divide-y divide-border">{submissionItems.map((submission) => <li key={submission.id}><button type="button" className="flex min-h-16 w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring" onClick={() => navigate(`/submissions/${encodeURIComponent(submission.id)}?form=${encodeURIComponent(formID)}`)}><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{summary(submission.values) || selectedForm?.key || submission.form_id}</span><span className="mt-1 block text-xs text-muted-foreground">{dateTime(submission.created_at)} · {selectedForm?.key ?? submission.form_id}</span></span><Badge variant="outline">{submission.state}</Badge></button></li>)}</ul>{submissions.hasNextPage && <div className="border-t border-border p-3 text-center"><Button type="button" variant="outline" onClick={() => void submissions.fetchNextPage()} loading={submissions.isFetchingNextPage}>Загрузить ещё</Button></div>}</div>}
  </div>;
}
