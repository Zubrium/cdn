import { useCallback, useEffect, useMemo, useState } from "react";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  Download,
  Inbox,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import type {
  SubmissionFilters,
  SubmissionStatus,
} from "@/types";
import { queryKeys } from "@/lib/query-keys";
import {
  DEFAULT_SUBMISSION_SORT,
  hasActiveSubmissionFilters,
  RESET_SUBMISSION_FILTERS,
  submissionListHref,
} from "./filters";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { errorMessage } from "@/lib/errors";
import { downloadBlob, dateInputToRfc3339 } from "@/lib/format";
import { Button } from "@/components/ui/button";
import {
  EmptyState,
  ErrorState,
  PageHeader,
  TableSkeleton,
  useDocumentTitle,
} from "@/components/common/page";
import { SubmissionDetail } from "./components/SubmissionDetail";
import { SubmissionResults } from "./components/SubmissionResults";
import { SubmissionFiltersPanel } from "./components/submissions-page/SubmissionFiltersPanel";

function useSubmissionFilterParams() {
  const [searchParams, setSearchParams] = useSearchParams();
  const form = searchParams.get("form") ?? "";
  const status = (searchParams.get("status") ?? "all") as SubmissionStatus | "all";
  const branchId = searchParams.get("branchId") ?? "all";
  const field = searchParams.get("field") ?? "";
  const from = searchParams.get("from") ?? "";
  const to = searchParams.get("to") ?? "";
  const sort = searchParams.get("sort") ?? DEFAULT_SUBMISSION_SORT;
  const archived = searchParams.get("archived") === "1";
  const q = searchParams.get("q") ?? "";

  const update = useCallback(
    (values: Record<string, string | null>, replace = false) => {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          Object.entries(values).forEach(([key, value]) => {
            if (value === null || value === "" || value === "all") {
              next.delete(key);
            } else {
              next.set(key, value);
            }
          });
          return next;
        },
        { replace },
      );
    },
    [setSearchParams],
  );

  return { form, branchId, status, field, from, to, sort, archived, q, update };
}

export default function SubmissionsPage() {
  const { submissionId } = useParams<{ submissionId: string }>();
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { form, branchId, status, field, from, to, sort, archived, q, update } =
    useSubmissionFilterParams();
  const [searchInput, setSearchInput] = useState(q);
  const debouncedSearch = useDebouncedValue(searchInput, 300);

  useDocumentTitle(submissionId ? "Заявка" : "Заявки");

  useEffect(() => {
    setSearchInput(q);
  }, [q]);

  useEffect(() => {
    if (debouncedSearch !== q) {
      update({ q: debouncedSearch || null }, true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedSearch]);

  const branchesQuery = useQuery({ queryKey: ["submission-branches"], queryFn: ({ signal }) => api.forms.branches(signal) });
  const branchName = (id: string) => branchesQuery.data?.items.find((branch) => branch.id === id)?.name ?? "Недоступная ветка";
  const formsQuery = useQuery({
    queryKey: queryKeys.features.list({ type: "form" }),
    queryFn: async ({ signal }) => {
      const items = [];
      let cursor: string | null = null;
      do {
        const page = await api.features.list({ type: "form", status: "", cursor }, signal);
        items.push(...page.items);
        if (page.next_cursor && page.next_cursor === cursor) throw new Error("Не удалось загрузить список форм: повторился указатель страницы.");
        cursor = page.next_cursor;
      } while (cursor);
      return { items };
    },
  });
  const forms = useMemo(
    () => formsQuery.data?.items.filter((item) => item.type === "form") ?? [],
    [formsQuery.data],
  );

  const filters = useMemo<SubmissionFilters>(
    () => ({
      branchId: branchId === "all" ? undefined : branchId,
      status: status === "all" ? "" : status,
      created_from: dateInputToRfc3339(from, "start"),
      created_to: dateInputToRfc3339(to, "end"),
      q: q || undefined,
      field: field || undefined,
      include_archived: archived,
      sort: sort as "asc" | "desc",
    }),
    [branchId, status, from, to, q, field, archived, sort],
  );

  const submissionsQuery = useInfiniteQuery({
    queryKey: queryKeys.submissions.list(form, {
      status,
      branchId,
      field,
      from,
      to,
      sort,
      archived,
      q,
    }),
    queryFn: ({ pageParam, signal }) =>
      api.forms.submissions(
        form,
        filters,
        pageParam ?? undefined,
        undefined,
        signal,
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
  });

  const formFields = useMemo(
    () => submissionsQuery.data?.pages[0]?.fields ?? [],
    [submissionsQuery.data],
  );

  const loaded = useMemo(
    () => submissionsQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [submissionsQuery.data],
  );

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: queryKeys.submissions.all });
    void queryClient.invalidateQueries({ queryKey: queryKeys.overview });
  };

  const exportMutation = useMutation({
    mutationFn: () => api.forms.exportCsv(form, filters),
    onSuccess: ({ blob, filename }) => {
      downloadBlob(blob, filename);
      toast.success("Экспорт заявок готов");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  if (submissionId) {
    return (
      <SubmissionDetail
        submissionId={submissionId}
        onBack={() => navigate(submissionListHref(location.search))}
        onChanged={invalidate}
      />
    );
  }

  const filtersActive = hasActiveSubmissionFilters({ form, branchId, status, field, from, to, sort, archived, q });

  const resetFilters = () => {
    setSearchInput("");
    update(RESET_SUBMISSION_FILTERS);
  };

  return (
    <div className="page-grid">
      <PageHeader
        title="Заявки"
        description="Отправки публичных форм. Данные показываются по той версии схемы, которая была опубликована на момент отправки."
        actions={
          <>
              <Button
                variant="outline"
                loading={exportMutation.isPending}
                onClick={() => exportMutation.mutate()}
              >
                <Download />
                Экспорт CSV
              </Button>
          </>
        }
      />

      {branchesQuery.error && <ErrorState error={branchesQuery.error} title="Не удалось загрузить ветки для фильтра" onRetry={() => void branchesQuery.refetch()} />}
      {formsQuery.error && <ErrorState error={formsQuery.error} title="Не удалось загрузить список форм" onRetry={() => void formsQuery.refetch()} />}
      <SubmissionFiltersPanel
        form={form}
        forms={forms}
        branchId={branchId}
        branches={branchesQuery.data?.items ?? []}
        status={status}
        field={field}
        fields={formFields}
        sort={sort}
        searchInput={searchInput}
        onSearchInputChange={setSearchInput}
        from={from}
        to={to}
        loadedCount={loaded.length}
        hasNextPage={Boolean(submissionsQuery.hasNextPage)}
        filtersActive={filtersActive}
        onUpdate={update}
        onReset={resetFilters}
      />

      {submissionsQuery.isLoading ? (
        <div className="overflow-hidden rounded-lg border border-border bg-card p-4">
          <TableSkeleton rows={6} />
        </div>
      ) : submissionsQuery.isError ? (
        <ErrorState
          error={submissionsQuery.error}
          title="Не удалось загрузить заявки"
          onRetry={() => void submissionsQuery.refetch()}
        />
      ) : loaded.length === 0 ? (
        <EmptyState
          icon={Inbox}
          title={filtersActive ? "Заявки не найдены" : "Заявок пока нет"}
          description={
            filtersActive
              ? "По заданным условиям ничего не найдено. Сбросьте фильтры или измените запрос."
              : "Как только форму заполнят, отправки появятся здесь."
          }
        />
      ) : (
        <SubmissionResults
          items={loaded}
          branchName={branchName}
          locationSearch={location.search}
          hasNextPage={Boolean(submissionsQuery.hasNextPage)}
          isFetchingNextPage={submissionsQuery.isFetchingNextPage}
          onLoadMore={() => void submissionsQuery.fetchNextPage()}
        />
      )}
    </div>
  );
}
