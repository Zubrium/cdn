import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { ChevronRight, FilePlus2, Plus } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { EmptyState, ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { hasGroupAccess } from "@/lib/permissions";
import { pageSize, schemaQueryKey } from "./forms-query";

export default function FormsPage() {
  useDocumentTitle("Формы");
  const { principal } = useAuth();
  const canManage = hasGroupAccess(principal, "forms_schemas", 2);
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const schemas = useInfiniteQuery({
    queryKey: schemaQueryKey,
    queryFn: ({ pageParam, signal }) => api.formsEngine.schemas(pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });
  const schemaItems = useMemo(() => schemas.data?.pages.flatMap((page) => page.items) ?? [], [schemas.data]);
  const normalizedQuery = query.trim().toLocaleLowerCase("ru-RU");
  const visibleItems = normalizedQuery
    ? schemaItems.filter((schema) => schema.key.toLocaleLowerCase("ru-RU").includes(normalizedQuery))
    : schemaItems;

  if (schemas.isLoading) return <LoadingPage label="Загружаем схемы форм…" />;
  if (schemas.error) return <div className="page-grid"><ErrorState error={schemas.error} title="Не удалось загрузить схемы форм" onRetry={() => void schemas.refetch()} /></div>;

  return <div className="page-grid">
    <PageHeader title="Формы" description="Схемы задают явные типы, обязательность и ограничения полей. Каждый ответ проверяется по закреплённой версии." actions={canManage && <Button type="button" onClick={() => navigate("/forms/new")}><Plus />Новая форма</Button>} />

    <section aria-labelledby="forms-list-title" className="min-w-0">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 id="forms-list-title" className="text-lg font-semibold">Схемы форм</h2>
          <p className="mt-1 text-xs text-muted-foreground">Выберите форму, чтобы открыть её редактор. Количество форм не ограничено.</p>
        </div>
        {schemas.hasNextPage && <Button type="button" variant="outline" onClick={() => void schemas.fetchNextPage()} loading={schemas.isFetchingNextPage}>Показать ещё</Button>}
      </div>

      {schemaItems.length > 1 && <label className="mb-3 block max-w-md space-y-1.5 text-sm">
        <span className="sr-only">Поиск по ключу формы</span>
        <Input value={query} onChange={(event) => setQuery(event.currentTarget.value)} placeholder="Поиск по ключу формы" aria-label="Поиск по ключу формы" />
      </label>}

      {schemaItems.length === 0 ? <EmptyState
        icon={FilePlus2}
        title="Схем пока нет"
        description={canManage ? "Создайте первую форму. Дальше можно добавлять любое количество схем." : "Схемы появятся здесь после создания администратором."}
        action={canManage && <Button type="button" onClick={() => navigate("/forms/new")}><Plus />Создать форму</Button>}
      /> : visibleItems.length === 0 ? <p className="rounded-md border border-dashed border-border px-3 py-8 text-center text-sm text-muted-foreground">Формы с таким ключом не найдены.</p> : <ul className="divide-y divide-border rounded-md border border-border bg-card">
        {visibleItems.map((schema) => <li key={schema.id}>
          <button
            type="button"
            className="flex min-h-16 w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            onClick={() => navigate(`/forms/${schema.id}`)}
          >
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">{schema.key}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">Версия {schema.version} · ревизия {schema.revision} · ID {schema.id}</span>
            </span>
            <Badge variant="outline">{Object.keys(schema.fields).length} поле(й)</Badge>
            <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          </button>
        </li>)}
      </ul>}
    </section>
  </div>;
}
