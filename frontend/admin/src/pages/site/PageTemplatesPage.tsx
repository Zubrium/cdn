import { useMemo, useRef, useState, type FormEvent } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Copy, FilePlus2, LayoutTemplate, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { useConfirm } from "@/components/common/confirm";
import { EmptyState, ErrorState, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, Label, Textarea } from "@/components/ui/input";
import type { SiteConfig, SiteMetadata, SiteSnapshot, WebsitePage, WebsitePageTemplate, WebsitePageTemplateConfiguration } from "@/types";
import { errorMessage } from "@/lib/errors";
import { hasGroupAccess } from "@/lib/permissions";
import { useSiteWorkspace } from "./SiteWorkspace";
import { siteContextQuery } from "./site-query";

const LIST_LIMIT = 100;
const MAX_NAME_LENGTH = 120;
const MAX_DESCRIPTION_LENGTH = 1000;

type TemplateDraft = {
  id?: string;
  revision?: number;
  name: string;
  description: string;
  pageName: string;
  sourcePageID: string;
  configuration: WebsitePageTemplateConfiguration | null;
};

type ApplicationPreview = {
  template: WebsitePageTemplate;
  page: WebsitePage;
  snapshot: SiteSnapshot;
  contentRevision: number;
  before: string;
  after: string;
  idempotencyKey: string;
};

function blankDraft(): TemplateDraft {
  return { name: "", description: "", pageName: "Новая страница", sourcePageID: "", configuration: null };
}

function cloneValue<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function templatePage(configuration: WebsitePageTemplateConfiguration): SiteConfig["pages"][number] {
  const page = configuration.payload.config.pages?.[0];
  if (!page || typeof page.name !== "string" || !Array.isArray(page.placements)) {
    throw new Error("Шаблон не содержит корректную конфигурацию страницы.");
  }
  return page;
}

function featurePinsForConfig(config: SiteConfig): Record<string, string> {
  return Object.fromEntries(config.pages[0].placements.map((placement) => [placement.build.featureKey, placement.build.version]));
}

function copySnapshotConfiguration(
  snapshot: { payload: { config: SiteConfig; metadata?: SiteMetadata }; snapshot: SiteSnapshot },
  pageName: string,
): WebsitePageTemplateConfiguration {
  const config = cloneValue(snapshot.payload.config);
  const page = config.pages?.[0];
  if (!page) throw new Error("У выбранного снапшота нет конфигурации страницы.");
  page.id = "template-page";
  page.name = pageName.trim();

  const metadata = cloneValue(snapshot.payload.metadata ?? { formatVersion: 1 as const, siteName: snapshot.snapshot.name });
  const pageMetadata = metadata.pages?.[snapshot.snapshot.pageId];
  if (pageMetadata) {
    const nullableFields = ["siteName", "faviconMediaId", "description", "ogTitle", "ogDescription", "ogImageMediaId"] as const;
    for (const field of nullableFields) {
      const value = pageMetadata[field];
      if (value !== undefined && value !== null) Object.assign(metadata, { [field]: value });
    }
    if (pageMetadata.noIndex !== undefined) metadata.noIndex = pageMetadata.noIndex;
  }
  metadata.pages = {};

  return {
    schema_version: 1,
    payload: { config, metadata },
    feature_pins: featurePinsForConfig(config),
  };
}

function newBlankConfiguration(pageName: string, sourceMetadata: SiteMetadata): WebsitePageTemplateConfiguration {
  const metadata = cloneValue(sourceMetadata);
  metadata.pages = {};
  return {
    schema_version: 1,
    payload: {
      config: { format_version: 3, pages: [{ id: "template-page", name: pageName.trim(), kind: "page", placements: [] }] },
      metadata,
    },
    feature_pins: {},
  };
}

function configurationWithPageName(configuration: WebsitePageTemplateConfiguration, pageName: string): WebsitePageTemplateConfiguration {
  const next = cloneValue(configuration);
  const page = templatePage(next);
  page.id = "template-page";
  page.name = pageName.trim();
  return next;
}

function readableConfiguration(configuration: WebsitePageTemplateConfiguration, pageID: string): string {
  const value = cloneValue(configuration);
  const page = templatePage(value);
  page.id = pageID;
  return JSON.stringify({
    page: {
      name: page.name,
      kind: page.kind,
      placements: page.placements,
    },
    metadata: value.payload.metadata,
    feature_pins: value.feature_pins,
    placement_slots: value.placement_slots ?? [],
  }, null, 2);
}

function contextURL(snapshotID: string, pageID: string): string {
  return siteContextQuery(snapshotID, pageID);
}

function createPageURL(templateID: string, snapshotID: string, pageID: string): string {
  const query = new URLSearchParams(contextURL(snapshotID, pageID).replace(/^\\?/u, ""));
  query.set("create", "1");
  query.set("template_id", templateID);
  return `/site/pages?${query.toString()}`;
}

export default function PageTemplatesPage() {
  useDocumentTitle("Шаблоны страниц");
  const { snapshot, metadata } = useSiteWorkspace();
  const auth = useAuth();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const canEdit = hasGroupAccess(auth.principal, "features", 2);
  const [draft, setDraft] = useState<TemplateDraft>(blankDraft);
  const [editorOpen, setEditorOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [applyingTemplateID, setApplyingTemplateID] = useState("");
  const [applicationPageID, setApplicationPageID] = useState("");
  const [applicationOpen, setApplicationOpen] = useState(false);
  const [applicationLoading, setApplicationLoading] = useState(false);
  const [applicationError, setApplicationError] = useState("");
  const [applicationPreview, setApplicationPreview] = useState<ApplicationPreview | null>(null);
  const [applying, setApplying] = useState(false);
  const createAttempt = useRef<{ fingerprint: string; key: string } | null>(null);

  const templates = useInfiniteQuery({
    queryKey: ["site", "website-page-templates"],
    queryFn: ({ pageParam, signal }) => api.website.pageTemplates(pageParam ?? undefined, LIST_LIMIT, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
  });
  const pages = useInfiniteQuery({
    queryKey: ["site", "website-pages"],
    queryFn: ({ pageParam, signal }) => api.website.pages(pageParam ?? undefined, LIST_LIMIT, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
  });
  const templateItems = useMemo(() => templates.data?.pages.flatMap((page) => page.items) ?? [], [templates.data]);
  const pageItems = useMemo(() => pages.data?.pages.flatMap((page) => page.items) ?? [], [pages.data]);

  const openCreate = () => {
    setDraft(blankDraft());
    createAttempt.current = null;
    setEditorOpen(true);
  };

  const openEdit = (template: WebsitePageTemplate) => {
    let pageName = "Новая страница";
    try { pageName = templatePage(template.configuration).name; } catch { /* invalid saved configuration will be reported on save */ }
    setDraft({
      id: template.id,
      revision: template.revision,
      name: template.name,
      description: template.description,
      pageName,
      sourcePageID: "",
      configuration: cloneValue(template.configuration),
    });
    createAttempt.current = null;
    setEditorOpen(true);
  };

  const closeEditor = (open: boolean) => {
    if (saving) return;
    setEditorOpen(open);
    if (!open) {
      setDraft(blankDraft());
      createAttempt.current = null;
    }
  };

  const saveTemplate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const cleanName = draft.name.trim();
    const pageName = draft.pageName.trim();
    if (!cleanName || !pageName || saving) return;
    setSaving(true);
    try {
      let configuration: WebsitePageTemplateConfiguration;
      if (draft.sourcePageID) {
        const source = pageItems.find((page) => page.id === draft.sourcePageID);
        if (!source) throw new Error("Исходная страница не найдена. Обновите список и выберите её снова.");
        const currentPage = await api.website.page(source.id);
        if (currentPage.latest_snapshot_state !== "ready") throw new Error("Для шаблона можно использовать только страницу с готовым последним снапшотом.");
        const sourceSnapshot = await api.siteBuilder.snapshot(currentPage.latest_snapshot_id);
        if (sourceSnapshot.snapshot.pageId !== currentPage.id || sourceSnapshot.snapshot.state !== "ready") {
          throw new Error("Последний снапшот исходной страницы изменился. Обновите список и повторите действие.");
        }
        configuration = copySnapshotConfiguration(sourceSnapshot, pageName);
      } else if (draft.configuration) {
        configuration = configurationWithPageName(draft.configuration, pageName);
      } else {
        configuration = newBlankConfiguration(pageName, metadata);
      }
      const input = { name: cleanName, description: draft.description.trim(), configuration };
      if (draft.id) {
        if (!draft.revision) throw new Error("Не удалось определить ревизию шаблона. Обновите список.");
        await api.website.updatePageTemplate(draft.id, draft.revision, input);
      } else {
        const fingerprint = JSON.stringify(input);
        if (!createAttempt.current || createAttempt.current.fingerprint !== fingerprint) {
          createAttempt.current = { fingerprint, key: `template-create-${crypto.randomUUID()}` };
        }
        await api.website.createPageTemplate(input, createAttempt.current.key);
        createAttempt.current = null;
      }
      setEditorOpen(false);
      setDraft(blankDraft());
      await queryClient.invalidateQueries({ queryKey: ["site", "website-page-templates"] });
      toast.success(draft.id ? "Шаблон обновлён" : "Шаблон создан");
    } catch (error) {
      toast.error(errorMessage(error));
      void queryClient.invalidateQueries({ queryKey: ["site", "website-page-templates"] });
      void queryClient.invalidateQueries({ queryKey: ["site", "website-pages"] });
    } finally {
      setSaving(false);
    }
  };

  const deleteTemplate = async (template: WebsitePageTemplate) => {
    const accepted = await confirm({
      title: "Удалить шаблон?",
      description: `«${template.name}» будет удалён. Уже созданные страницы и их снапшоты не изменятся. Будет удалена ревизия ${template.revision}.`,
      confirmLabel: "Удалить шаблон",
      cancelLabel: "Оставить",
      destructive: true,
    });
    if (!accepted) return;
    try {
      await api.website.deletePageTemplate(template.id, template.revision);
      await queryClient.invalidateQueries({ queryKey: ["site", "website-page-templates"] });
      toast.success("Шаблон удалён");
    } catch (error) {
      toast.error(errorMessage(error));
      void queryClient.invalidateQueries({ queryKey: ["site", "website-page-templates"] });
    }
  };

  const startApplication = async () => {
    if (!applyingTemplateID || !applicationPageID || applicationLoading) return;
    setApplicationLoading(true);
    setApplicationError("");
    setApplicationPreview(null);
    try {
      const [template, page] = await Promise.all([
        api.website.pageTemplate(applyingTemplateID),
        api.website.page(applicationPageID),
      ]);
      if (page.latest_snapshot_state !== "ready") throw new Error("Последний снапшот страницы ещё не готов. Обновите список и повторите действие.");
      const detail = await api.siteBuilder.snapshot(page.latest_snapshot_id);
      if (detail.snapshot.pageId !== page.id || detail.snapshot.state !== "ready" || detail.snapshot.revision === undefined) {
        throw new Error("Последний снапшот изменился или ещё не готов. Обновите список страниц.");
      }
      const localeConfig = await api.contentEngine.localeConfig();
      const content = await api.contentEngine.workspace(detail.snapshot.id, localeConfig.default_locale);
      if (content.pageSnapshotId !== detail.snapshot.id || !Number.isSafeInteger(content.revision) || content.revision < 0) {
        throw new Error("Не удалось проверить текущую ревизию контента. Повторите подготовку применения.");
      }
      const beforeConfiguration: WebsitePageTemplateConfiguration = {
        schema_version: 1,
        payload: { config: detail.payload.config, metadata: detail.payload.metadata ?? { formatVersion: 1, siteName: page.name } },
        feature_pins: featurePinsForConfig(detail.payload.config),
      };
      const afterConfiguration = cloneValue(template.configuration);
      const afterPage = templatePage(afterConfiguration);
      afterPage.id = page.id;
      const preview: ApplicationPreview = {
        template,
        page,
        snapshot: detail.snapshot,
        contentRevision: content.revision,
        before: readableConfiguration(beforeConfiguration, page.id),
        after: readableConfiguration(afterConfiguration, page.id),
        idempotencyKey: `template-apply-${crypto.randomUUID()}`,
      };
      setApplicationPreview(preview);
    } catch (error) {
      setApplicationError(errorMessage(error));
    } finally {
      setApplicationLoading(false);
    }
  };

  const applyTemplate = async () => {
    if (!applicationPreview || applying) return;
    setApplying(true);
    try {
      const created = await api.website.applyPageTemplate(applicationPreview.page.id, {
        template_id: applicationPreview.template.id,
        template_revision: applicationPreview.template.revision,
        expected_snapshot_id: applicationPreview.snapshot.id,
        expected_snapshot_revision: applicationPreview.snapshot.revision!,
        expected_content_revision: applicationPreview.contentRevision,
        confirmed: true,
      }, applicationPreview.idempotencyKey);
      if (created.page_id !== applicationPreview.page.id || created.parent_id !== applicationPreview.snapshot.id) {
        throw new Error("Сервер вернул снапшот, не соответствующий подтверждённому применению.");
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["site", "website-pages"] }),
        queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] }),
        queryClient.invalidateQueries({ queryKey: ["website", "snapshots"] }),
        queryClient.invalidateQueries({ queryKey: ["content"] }),
      ]);
      setApplicationOpen(false);
      setApplicationPreview(null);
      toast.success(created.state === "ready" ? "Шаблон применён: создан дочерний снапшот" : "Дочерний снапшот создан и готовится");
      navigate(`/site/pages/${encodeURIComponent(applicationPreview.page.id)}${contextURL(created.id, applicationPreview.page.id)}`);
    } catch (error) {
      setApplicationError(errorMessage(error));
      void queryClient.invalidateQueries({ queryKey: ["site", "website-pages"] });
      void queryClient.invalidateQueries({ queryKey: ["site", "website-page-templates"] });
    } finally {
      setApplying(false);
    }
  };

  const openApplication = (template: WebsitePageTemplate) => {
    setApplyingTemplateID(template.id);
    setApplicationPageID("");
    setApplicationPreview(null);
    setApplicationError("");
    setApplicationOpen(true);
  };

  if (templates.error) return <ErrorState error={templates.error} title="Не удалось загрузить шаблоны страниц" onRetry={() => void templates.refetch()} />;

  const applicationTemplate = templateItems.find((item) => item.id === applyingTemplateID);
  const selectedApplicationPage = pageItems.find((item) => item.id === applicationPageID);
  const editingConfigurationError = (() => {
    if (!draft.configuration || draft.sourcePageID) return "";
    try { templatePage(draft.configuration); return ""; } catch (error) { return errorMessage(error); }
  })();

  return <div className="min-h-full bg-background">
    <div className="mx-auto w-full max-w-5xl px-4 py-5 sm:px-7 sm:py-7">
      <header className="flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
        <div className="min-w-0"><h1 className="text-2xl font-semibold tracking-tight">Шаблоны страниц</h1><p className="mt-1 max-w-2xl text-sm leading-6 text-muted-foreground">Шаблон хранит точную конфигурацию и ревизию. Страницы, созданные по нему, остаются независимыми от последующих изменений шаблона.</p></div>
        {canEdit && <Button type="button" className="min-h-11" onClick={openCreate}><Plus />Создать шаблон</Button>}
      </header>

      {templates.isLoading ? <p role="status" className="py-12 text-center text-sm text-muted-foreground">Загружаем шаблоны…</p> : templateItems.length === 0 ? <EmptyState title="Шаблонов пока нет" description={canEdit ? "Создайте шаблон из готового снапшота страницы или начните с пустой конфигурации." : "Администратор ещё не создал шаблоны для этого сайта."} action={canEdit ? <Button type="button" className="min-h-11" onClick={openCreate}><Plus />Создать шаблон</Button> : undefined} /> : <div className="divide-y divide-border">
        {templateItems.map((template) => {
          let featureCount = 0;
          let placementCount = 0;
          let pageName = "Страница";
          try {
            const page = templatePage(template.configuration);
            pageName = page.name;
            placementCount = page.placements.length;
            featureCount = new Set(page.placements.map((placement) => placement.build.featureKey)).size;
          } catch { /* invalid state is made visible to the editor when opened */ }
          return <article key={template.id} className="flex flex-col gap-4 py-5 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 gap-3">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground"><LayoutTemplate className="size-4" /></span>
              <div className="min-w-0"><h2 className="truncate text-sm font-semibold">{template.name}</h2><p className="mt-1 line-clamp-2 text-sm leading-5 text-muted-foreground">{template.description || "Описание не задано."}</p><p className="mt-2 text-xs text-muted-foreground">Страница «{pageName}» · {featureCount} фич · {placementCount} размещений · ревизия {template.revision}</p></div>
            </div>
            {canEdit && <div className="flex flex-wrap gap-2 sm:shrink-0">
              <Button asChild type="button" variant="outline" className="min-h-10"><Link to={createPageURL(template.id, snapshot?.id ?? "", snapshot?.pageId ?? "")}><FilePlus2 />Создать страницу</Link></Button>
              <Button type="button" variant="outline" className="min-h-10" onClick={() => openApplication(template)}><Copy />Применить к странице</Button>
              <Button type="button" variant="outline" className="min-h-10" onClick={() => openEdit(template)}><Pencil />Изменить</Button>
              <Button type="button" variant="destructive" className="min-h-10" onClick={() => void deleteTemplate(template)} aria-label={`Удалить шаблон ${template.name}`}><Trash2 /><span className="hidden sm:inline">Удалить</span></Button>
            </div>}
          </article>;
        })}
        {templates.hasNextPage && <div className="py-4 text-center"><Button type="button" variant="outline" className="min-h-11" loading={templates.isFetchingNextPage} onClick={() => void templates.fetchNextPage()}>Загрузить ещё шаблоны</Button></div>}
      </div>}
    </div>

    <Dialog open={editorOpen} onOpenChange={closeEditor}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl">
        <form className="space-y-5" onSubmit={(event) => void saveTemplate(event)}>
          <DialogHeader><DialogTitle>{draft.id ? "Изменить шаблон" : "Новый шаблон"}</DialogTitle><DialogDescription>Укажите имя шаблона и имя страницы, которое будет предложено при его применении. Ревизии шаблона обновляются отдельно от уже созданных страниц.</DialogDescription></DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5"><Label htmlFor="website-template-name">Название шаблона</Label><Input id="website-template-name" className="min-h-11" required maxLength={MAX_NAME_LENGTH} autoFocus value={draft.name} onChange={(event) => { const name = event.currentTarget.value; setDraft((current) => ({ ...current, name })); createAttempt.current = null; }} placeholder="Например, Информационная страница" /></div>
            <div className="space-y-1.5"><Label htmlFor="website-template-page-name">Название страницы</Label><Input id="website-template-page-name" className="min-h-11" required maxLength={MAX_NAME_LENGTH} value={draft.pageName} onChange={(event) => { const pageName = event.currentTarget.value; setDraft((current) => ({ ...current, pageName })); }} /></div>
          </div>
          <div className="space-y-1.5"><Label htmlFor="website-template-description">Описание</Label><Textarea id="website-template-description" maxLength={MAX_DESCRIPTION_LENGTH} rows={3} value={draft.description} onChange={(event) => { const description = event.currentTarget.value; setDraft((current) => ({ ...current, description })); }} placeholder="Для каких страниц подходит этот шаблон" /></div>
          <div className="space-y-1.5"><Label htmlFor="website-template-source">Конфигурация страницы</Label><select id="website-template-source" className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={draft.sourcePageID} onChange={(event) => {
            const sourcePageID = event.currentTarget.value;
            const sourcePage = pageItems.find((page) => page.id === sourcePageID);
            setDraft((current) => ({ ...current, sourcePageID, ...(sourcePage ? { pageName: sourcePage.name } : {}) }));
          }}>
            <option value="">{draft.id ? "Сохранённая конфигурация шаблона" : "Пустая страница без размещений"}</option>
            {pageItems.map((page) => <option key={page.id} value={page.id}>{page.name}{page.latest_snapshot_state === "ready" ? "" : " · снапшот не готов"}</option>)}
          </select>
          <p className="text-xs leading-5 text-muted-foreground">При выборе страницы конфигурация берётся из её последнего готового снапшота. Роуты Router и контентные записи в шаблон не входят.</p>
          {pages.error && <p role="alert" className="text-xs text-destructive">Не удалось загрузить список страниц. <button type="button" className="underline underline-offset-2" onClick={() => void pages.refetch()}>Повторить</button></p>}
          {pages.hasNextPage && <Button type="button" size="sm" variant="ghost" onClick={() => void pages.fetchNextPage()}>Загрузить ещё страницы</Button>}
          {editingConfigurationError && !draft.sourcePageID && <p role="alert" className="text-xs text-destructive">{editingConfigurationError}. Выберите страницу, чтобы заменить конфигурацию.</p>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" className="min-h-11" disabled={saving} onClick={() => closeEditor(false)}>Отмена</Button>
            <Button type="submit" className="min-h-11" disabled={saving || !draft.name.trim() || !draft.pageName.trim() || Boolean(editingConfigurationError) || Boolean(pages.error)} loading={saving}>{draft.id ? "Сохранить шаблон" : "Создать шаблон"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog open={applicationOpen} onOpenChange={(open) => { if (applying) return; setApplicationOpen(open); if (!open) { setApplicationPreview(null); setApplicationError(""); } }}>
      <DialogContent className="max-h-[92dvh] overflow-y-auto sm:max-w-5xl">
        <DialogHeader><DialogTitle>Применить шаблон</DialogTitle><DialogDescription>{applicationTemplate ? `«${applicationTemplate.name}» будет применён к существующей странице с сохранением её ID.` : "Шаблон будет применён к выбранной странице."}</DialogDescription></DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5"><Label htmlFor="website-apply-page">Страница</Label><select id="website-apply-page" className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={applicationPageID} disabled={applicationLoading || applying} onChange={(event) => { setApplicationPageID(event.currentTarget.value); setApplicationPreview(null); setApplicationError(""); }}>
            <option value="">Выберите страницу</option>
            {pageItems.map((page) => <option key={page.id} value={page.id}>{page.name}{page.latest_snapshot_state === "ready" ? "" : " · последний снапшот не готов"}</option>)}
          </select>
          {pages.hasNextPage && <Button type="button" size="sm" variant="ghost" onClick={() => void pages.fetchNextPage()} loading={pages.isFetchingNextPage}>Загрузить ещё страницы</Button>}
          {pages.error && <p role="alert" className="text-xs text-destructive">Не удалось загрузить страницы. <button type="button" className="underline underline-offset-2" onClick={() => void pages.refetch()}>Повторить</button></p>}
          </div>
          {!applicationPreview && <div className="flex flex-wrap items-center gap-2">
            <Button type="button" variant="outline" className="min-h-11" disabled={!applicationPageID || applicationLoading || applying} loading={applicationLoading} onClick={() => void startApplication()}><RefreshCw />Проверить изменения</Button>
            {selectedApplicationPage && <span className="text-xs text-muted-foreground">Текущий снапшот: {selectedApplicationPage.latest_snapshot_state}</span>}
          </div>}
          {applicationError && <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{applicationError}</p>}
          {applicationPreview && <div className="space-y-4">
            <div className="rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm leading-6">
              <p className="font-medium">Будет создан дочерний снапшот; текущий останется нетронутым.</p>
              <p className="mt-1 text-muted-foreground">Конфигурация страницы заменится целиком. Выборы записей для размещений будут сброшены; сами модель, записи и черновики контента остаются вне шаблона. Проверьте привязки после применения.</p>
              <p className="mt-1 text-xs text-muted-foreground">CAS: шаблон ревизии {applicationPreview.template.revision}; снапшот {applicationPreview.snapshot.id}, ревизия {applicationPreview.snapshot.revision}; контентная ревизия {applicationPreview.contentRevision}.</p>
            </div>
            <div className="grid gap-4 lg:grid-cols-2">
              <section className="min-w-0 space-y-2" aria-labelledby="website-apply-before"><h3 id="website-apply-before" className="text-sm font-semibold">Сейчас · {applicationPreview.page.name}</h3><pre className="max-h-80 overflow-auto rounded-md border border-border bg-muted/40 p-3 text-xs leading-5"><code>{applicationPreview.before}</code></pre></section>
              <section className="min-w-0 space-y-2" aria-labelledby="website-apply-after"><h3 id="website-apply-after" className="text-sm font-semibold">Будет применено · ревизия {applicationPreview.template.revision}</h3><pre className="max-h-80 overflow-auto rounded-md border border-border bg-muted/40 p-3 text-xs leading-5"><code>{applicationPreview.after}</code></pre></section>
            </div>
          </div>}
          {applicationPreview && <p className="text-xs leading-5 text-muted-foreground">Применение сохраняет идентификатор страницы. Изменения шаблона после этой операции не затронут созданный снапшот. Снапшот и контент должны остаться на показанных ревизиях; при конкурентном изменении сервер отклонит операцию.</p>}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" className="min-h-11" disabled={applying} onClick={() => setApplicationOpen(false)}>Отмена</Button>
          {applicationPreview && <Button type="button" className="min-h-11" disabled={applying || applicationLoading} loading={applying} onClick={() => void applyTemplate()}><ArrowRight />Подтвердить замену</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>;
}
