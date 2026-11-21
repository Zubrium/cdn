import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, FilePlus2, LayoutTemplate, Plus, Search } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { ErrorState, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import type { WebsitePageTemplate } from "@/types";
import { errorMessage } from "@/lib/errors";
import { hasGroupAccess } from "@/lib/permissions";
import { useSiteWorkspace } from "./SiteWorkspace";
import { siteContextQuery } from "./site-query";

const PAGE_LIST_LIMIT = 100;
const TEMPLATE_LIST_LIMIT = 100;

type CreateAttempt = { fingerprint: string; key: string };

function pageDetailURL(pageID: string, snapshotID: string): string {
  return `/site/pages/${encodeURIComponent(pageID)}${siteContextQuery(snapshotID, pageID)}`;
}

export default function PagesPage() {
  useDocumentTitle("Страницы");
  const { snapshot } = useSiteWorkspace();
  const auth = useAuth();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const canEdit = hasGroupAccess(auth.principal, "features", 2);
  const [search, setSearch] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [templateID, setTemplateID] = useState("");
  const [creating, setCreating] = useState(false);
  const createAttempt = useRef<CreateAttempt | null>(null);

  const pages = useInfiniteQuery({
    queryKey: ["site", "website-pages"],
    queryFn: ({ pageParam, signal }) => api.website.pages(pageParam ?? undefined, PAGE_LIST_LIMIT, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
  });
  const templates = useInfiniteQuery({
    queryKey: ["site", "website-page-templates"],
    queryFn: ({ pageParam, signal }) => api.website.pageTemplates(pageParam ?? undefined, TEMPLATE_LIST_LIMIT, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: createOpen,
  });

  const allPages = useMemo(() => pages.data?.pages.flatMap((page) => page.items) ?? [], [pages.data]);
  const allTemplates = useMemo(() => templates.data?.pages.flatMap((page) => page.items) ?? [], [templates.data]);
  const selectedTemplateQuery = useQuery({
    queryKey: ["site", "website-page-template", templateID],
    queryFn: () => api.website.pageTemplate(templateID),
    enabled: Boolean(createOpen && templateID && !allTemplates.some((item) => item.id === templateID)),
  });
  const visiblePages = useMemo(() => {
    const query = search.trim().toLocaleLowerCase("ru-RU");
    if (!query) return allPages;
    return allPages.filter((page) => page.name.toLocaleLowerCase("ru-RU").includes(query));
  }, [allPages, search]);

  useEffect(() => {
    if (searchParams.get("create") !== "1") return;
    setName("");
    setTemplateID(searchParams.get("template_id") ?? "");
    createAttempt.current = null;
    setCreateOpen(true);
    const next = new URLSearchParams(searchParams);
    next.delete("create");
    next.delete("template_id");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const openCreate = (selectedTemplate = "") => {
    setName("");
    setTemplateID(selectedTemplate);
    createAttempt.current = null;
    setCreateOpen(true);
  };

  const closeCreate = (open: boolean) => {
    if (creating) return;
    setCreateOpen(open);
    if (!open) {
      setName("");
      setTemplateID("");
      createAttempt.current = null;
    }
  };

  const createPage = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const cleanName = name.trim();
    if (!cleanName || creating) return;
    const selectedTemplate = allTemplates.find((item) => item.id === templateID) ?? selectedTemplateQuery.data;
    if (templateID && !selectedTemplate) {
      toast.error(selectedTemplateQuery.error ? errorMessage(selectedTemplateQuery.error) : "Шаблон ещё загружается. Повторите создание через мгновение.");
      return;
    }
    const fingerprint = `${cleanName}\u0000${selectedTemplate?.id ?? ""}\u0000${selectedTemplate?.revision ?? 0}`;
    if (createAttempt.current?.fingerprint !== fingerprint) {
      createAttempt.current = { fingerprint, key: `page-create-${crypto.randomUUID()}` };
    }
    setCreating(true);
    try {
      const page = await api.website.createPage({
        name: cleanName,
        ...(selectedTemplate ? { template_id: selectedTemplate.id, template_revision: selectedTemplate.revision } : {}),
      }, createAttempt.current.key);
      if (page.latest_snapshot_state !== "ready") {
        throw new Error("Сервер не подтвердил готовность корневого снапшота страницы.");
      }
      createAttempt.current = null;
      setCreateOpen(false);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["site", "website-pages"] }),
        queryClient.invalidateQueries({ queryKey: ["site", "website-page-templates"] }),
        queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] }),
      ]);
      toast.success("Страница создана");
      navigate(pageDetailURL(page.id, page.latest_snapshot_id));
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setCreating(false);
    }
  };

  if (pages.error) return <ErrorState error={pages.error} title="Не удалось загрузить страницы сайта" onRetry={() => void pages.refetch()} />;

  const selectedTemplate = allTemplates.find((item) => item.id === templateID) ?? selectedTemplateQuery.data;
  const selectedTemplateLoading = Boolean(templateID && !selectedTemplate && selectedTemplateQuery.isLoading);
  const filteredTemplates = allTemplates;

  return <div className="h-full min-h-0 overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-5xl px-4 py-5 sm:px-7 sm:py-7">
      <header className="flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
        <div className="min-w-0"><h1 className="text-2xl font-semibold tracking-tight">Страницы</h1><p className="mt-1 max-w-2xl text-sm leading-6 text-muted-foreground">Страницы создаются отдельно от публичных маршрутов. URL и привязки настраиваются в Router.</p></div>
        <div className="flex flex-wrap gap-2">
          <Button asChild type="button" variant="outline" className="min-h-11"><Link to={`/site/templates${siteContextQuery(snapshot?.id ?? "", snapshot?.pageId)}`}><LayoutTemplate />Шаблоны</Link></Button>
          {canEdit && <Button type="button" className="min-h-11" onClick={() => openCreate()}><Plus />Новая страница</Button>}
        </div>
      </header>

      <div className="border-b border-border py-4">
        <label className="relative block min-w-0">
          <span className="sr-only">Поиск страниц по названию</span>
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" className="min-h-11 pl-9" value={search} onChange={(event) => setSearch(event.currentTarget.value)} placeholder="Название страницы" />
        </label>
      </div>

      {pages.isLoading ? <p role="status" className="py-12 text-center text-sm text-muted-foreground">Загружаем страницы…</p> : visiblePages.length === 0 ? <section className="py-16 text-center">
        <h2 className="font-medium">{search ? "Страницы не найдены" : "На этом сайте ещё нет страниц"}</h2>
        <p className="mx-auto mt-1 max-w-lg text-sm leading-6 text-muted-foreground">{search ? "Измените поисковый запрос." : "Создайте пустую страницу или начните с точной текущей ревизии шаблона. Создание не назначает URL и не меняет Router."}</p>
        {canEdit && !search && <Button type="button" className="mt-5 min-h-11" onClick={() => openCreate()}><Plus />Создать страницу</Button>}
      </section> : <div className="divide-y divide-border">
        {visiblePages.map((page) => {
          const row = <div className="flex min-h-[4.5rem] min-w-0 items-center gap-3 px-1 py-3">
            <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground"><FilePlus2 className="size-4" /></span>
            <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{page.name}</span><span className="mt-1 block text-xs text-muted-foreground">{page.latest_snapshot_state === "ready" ? "Готовый снапшот" : page.latest_snapshot_state === "provisioning" ? "Снапшот готовится" : "Сбой подготовки снапшота"}</span></span>
            <span className="hidden shrink-0 text-xs text-muted-foreground sm:block">Создана {new Date(page.created_at).toLocaleDateString("ru-RU")}</span>
            {canEdit && <ArrowRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
          </div>;
          return <article key={page.id} className="border-b border-border/70 last:border-0">
            {canEdit ? <Link to={pageDetailURL(page.id, page.latest_snapshot_id)} className="block rounded-sm hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{row}</Link> : row}
          </article>;
        })}
        {pages.hasNextPage && <div className="py-4 text-center"><Button type="button" variant="outline" className="min-h-11" loading={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>Загрузить ещё</Button></div>}
      </div>}
    </div>

    <Dialog open={createOpen} onOpenChange={closeCreate}>
      <DialogContent>
        <form className="space-y-5" onSubmit={(event) => void createPage(event)}>
          <DialogHeader>
            <DialogTitle>Новая страница</DialogTitle>
            <DialogDescription>Website создаст страницу с новым ID и готовым корневым снапшотом. Маршрут можно настроить позже в Router.</DialogDescription>
          </DialogHeader>
          <div className="space-y-1.5"><Label htmlFor="website-page-name">Название страницы</Label><Input id="website-page-name" className="min-h-11" required maxLength={120} autoFocus value={name} onChange={(event) => { setName(event.currentTarget.value); createAttempt.current = null; }} placeholder="Например, Каталог" /></div>
          <div className="space-y-1.5"><Label htmlFor="website-page-template">Начать с шаблона</Label><select id="website-page-template" className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={templateID} onChange={(event) => { setTemplateID(event.currentTarget.value); createAttempt.current = null; }} disabled={templates.isLoading || selectedTemplateLoading}>
            <option value="">Пустая страница</option>
            {selectedTemplate && !allTemplates.some((item) => item.id === selectedTemplate.id) && <option value={selectedTemplate.id}>{selectedTemplate.name} · ревизия {selectedTemplate.revision}</option>}
            {filteredTemplates.map((template: WebsitePageTemplate) => <option key={template.id} value={template.id}>{template.name} · ревизия {template.revision}</option>)}
          </select>
          {selectedTemplateQuery.error && templateID && <p role="alert" className="text-xs text-destructive">Не удалось загрузить выбранный шаблон: {errorMessage(selectedTemplateQuery.error)} <button type="button" className="underline underline-offset-2" onClick={() => void selectedTemplateQuery.refetch()}>Повторить</button></p>}
          {selectedTemplateLoading && <p role="status" className="text-xs text-muted-foreground">Загружаем выбранный шаблон…</p>}
          {templates.error && <p role="alert" className="text-xs text-destructive">Не удалось загрузить список шаблонов. <button type="button" className="underline underline-offset-2" onClick={() => void templates.refetch()}>Повторить</button></p>}
          {templates.hasNextPage && <Button type="button" size="sm" variant="ghost" onClick={() => void templates.fetchNextPage()}>Загрузить ещё шаблоны</Button>}
          {selectedTemplate && <p className="text-xs leading-5 text-muted-foreground">Будет использована ревизия {selectedTemplate.revision}. Название страницы выше заменит имя из шаблона.</p>}
          {!templates.isLoading && allTemplates.length === 0 && !templateID && <p className="text-xs text-muted-foreground">Шаблонов пока нет. Вы сможете создать страницу без шаблона.</p>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" className="min-h-11" disabled={creating} onClick={() => closeCreate(false)}>Отмена</Button>
            <Button type="submit" className="min-h-11" disabled={creating || !name.trim() || selectedTemplateLoading || Boolean(templateID && (selectedTemplateQuery.error || templates.error && !selectedTemplate))} loading={creating}>Создать страницу</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </div>;
}
