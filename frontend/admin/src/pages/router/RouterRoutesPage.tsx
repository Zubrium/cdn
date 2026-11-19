import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDownToLine, FileUp, Plus, RefreshCw, Route, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { ErrorState, EmptyState, PageHeader, useDocumentTitle } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { useAuth } from "@/app/auth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { buildRouterTarget } from "@/domains/router/route-target";
import type { AccessGroup, FormSchema, RouterPublicOperation, RouterRoute, RouterRouteMatch, RouterRouteTarget } from "@/types";
import { errorMessage } from "@/lib/errors";
import { hasGroupAccess } from "@/lib/permissions";

const routesKey = ["router", "routes"] as const;
type RouteDraft = {
  host: string;
  path: string;
  match: RouterRouteMatch;
  targetKind: "core-file" | "static-site-artifact" | "public.form-upload" | "public.form-session" | "public.form-submit";
  file: { id: string; original_filename?: string } | null;
  accessGroup: string;
  fileSetID: string;
  maxBytes: string;
  allowedContentTypes: string[];
  bindForm: boolean;
  snapshotID: string;
  formID: string;
  schemaVersion: number;
};

const emptyDraft = (): RouteDraft => ({
  host: "",
  path: "/",
  match: "exact",
  targetKind: "core-file",
  file: null,
  accessGroup: "",
  fileSetID: "",
  maxBytes: "",
  allowedContentTypes: [],
  bindForm: false,
  snapshotID: "",
  formID: "",
  schemaVersion: 0,
});

const publicFormOperations = new Set(["public.form-session", "public.form-upload", "public.form-submit"]);

function canEditTarget(route: RouterRoute): boolean {
  return route.target.kind === "core-file" || route.target.kind === "static-site-artifact" || (route.target.kind === "registered-operation" && route.match === "exact" && publicFormOperations.has(route.target.operation_id));
}

function targetLabel(target: RouterRouteTarget): string {
  if (target.kind === "registered-operation") {
    if (target.operation_id === "public.form-upload") return "Загрузка файла формы через Loader · POST";
    if (target.operation_id === "public.form-session") return "Сессия формы · GET/POST/DELETE";
    if (target.operation_id === "public.form-submit") return "Отправка формы · POST";
    return `Операция · ${target.operation_id}`;
  }
  if (target.kind === "core-file") return `Файл Core · GET/HEAD · ${target.file_id}`;
  return `Артефакт статической страницы · ${target.file_id}`;
}

function targetDetails(target: RouterRouteTarget): string | null {
  if (target.kind !== "registered-operation") return null;
  const config = target.operation_config;
  const form = typeof config.form === "object" && config.form !== null ? config.form as Record<string, unknown> : config;
  const binding = typeof form.snapshot_id === "string" && typeof form.form_id === "string"
    ? ` · форма ${form.form_id} · схема v${typeof form.schema_version === "number" ? form.schema_version : "?"}`
    : "";
  if (target.operation_id === "public.form-session" || target.operation_id === "public.form-submit") return binding || null;
  if (target.operation_id !== "public.form-upload") return null;
  const group = typeof config.access_group === "string" ? config.access_group : "—";
  const maxBytes = typeof config.max_bytes === "number" && Number.isSafeInteger(config.max_bytes)
    ? `${config.max_bytes.toLocaleString("ru-RU")} байт`
    : "—";
  const fileSet = typeof config.file_set_id === "string" ? ` · набор ${config.file_set_id}` : "";
  return `Группа ${group} · максимум ${maxBytes}${fileSet}${binding}`;
}

function targetIcon(target: RouterRouteTarget) {
  return target.kind === "registered-operation" ? <FileUp className="size-4" /> : <ArrowDownToLine className="size-4" />;
}

function RouteDialog({
  open,
  route,
  groups,
  operations,
  operationsLoading,
  operationsError,
  onRetryOperations,
  onClose,
  onSave,
  saving,
}: {
  open: boolean;
  route: RouterRoute | null;
  groups: AccessGroup[];
  operations: RouterPublicOperation[];
  operationsLoading: boolean;
  operationsError: boolean;
  onRetryOperations: () => void;
  onClose: () => void;
  onSave: (draft: RouteDraft) => Promise<void>;
  saving: boolean;
}) {
  const [draft, setDraft] = useState<RouteDraft>(() => emptyDraft());
  const [pickerOpen, setPickerOpen] = useState(false);
  const openFilePicker = () => {
    if (draft.targetKind !== "core-file" && draft.targetKind !== "static-site-artifact") return;
    setPickerOpen(true);
  };
  const writableGroups = useMemo(() => groups.filter((group) => group.active && group.level >= 2), [groups]);
  const fileSets = useInfiniteQuery({
    queryKey: ["media", "file-sets", "router-upload"],
    queryFn: ({ pageParam, signal }) => api.media.fileSets({ limit: 50, cursor: pageParam ?? undefined }, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: open && draft.targetKind === "public.form-upload",
  });
  const availableFileSets = useMemo(
    () => fileSets.data?.pages.flatMap((page) => page.items) ?? [],
    [fileSets.data],
  );
  const uploadOperation = operations.find((operation) => operation.id === "public.form-upload");
  const sessionOperation = operations.find((operation) => operation.id === "public.form-session");
  const submitOperation = operations.find((operation) => operation.id === "public.form-submit");
  const requiresFormBinding = draft.targetKind === "public.form-session" || draft.targetKind === "public.form-submit" ||
    (draft.targetKind === "public.form-upload" && draft.bindForm);
  const validSnapshotID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(draft.snapshotID);
  const schemas = useInfiniteQuery({
    queryKey: ["router", "forms", draft.snapshotID],
    queryFn: ({ pageParam, signal }) => api.formsEngine.schemas(draft.snapshotID, pageParam ?? undefined, 100, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: open && requiresFormBinding && validSnapshotID,
  });
  const formSchemas: FormSchema[] = useMemo(() => schemas.data?.pages.flatMap((page) => page.items) ?? [], [schemas.data]);

  const formBindingEditor = <div className="space-y-3 rounded-md border border-border p-3">
    <p className="text-xs leading-5 text-muted-foreground">Операция фиксирует форму и версию схемы на сервере. Посетитель не может выбрать другую форму.</p>
    <label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">ID снапшота</span><Input required maxLength={36} value={draft.snapshotID} onChange={(event) => { const snapshotID = event.currentTarget.value.trim(); setDraft((current) => ({ ...current, snapshotID, ...(snapshotID !== current.snapshotID ? { formID: "", schemaVersion: 0 } : {}) })); }} placeholder="UUID снапшота" /></label>
    {validSnapshotID && schemas.isLoading && <p role="status" className="text-xs text-muted-foreground">Загружаем формы…</p>}
    {validSnapshotID && schemas.error && <p role="status" className="text-xs text-muted-foreground">Список схем недоступен; ID формы и версию можно ввести вручную. <button type="button" className="underline underline-offset-2" onClick={() => void schemas.refetch()}>Повторить</button></p>}
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem]">
      <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">UUID формы</span><Input required maxLength={36} value={draft.formID} onChange={(event) => setDraft((current) => ({ ...current, formID: event.currentTarget.value.trim() }))} placeholder="UUID формы" /></label>
      <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Версия схемы</span><Input type="number" min={1} step={1} required value={draft.schemaVersion || ""} onChange={(event) => setDraft((current) => ({ ...current, schemaVersion: Number(event.currentTarget.value) }))} /></label>
    </div>
    <label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">Выбрать из схем</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={draft.formID ? `${draft.formID}:${draft.schemaVersion}` : ""} disabled={!validSnapshotID || schemas.isLoading} onChange={(event) => { const [formID, version] = event.currentTarget.value.split(":"); setDraft((current) => ({ ...current, formID, schemaVersion: Number(version) })); }}><option value="">Выберите доступную схему</option>{draft.formID && !formSchemas.some((schema) => schema.id === draft.formID && schema.version === draft.schemaVersion) && <option value={`${draft.formID}:${draft.schemaVersion}`}>{draft.formID} · v{draft.schemaVersion} · сохранённый маршрут</option>}{formSchemas.map((schema) => <option key={schema.id} value={`${schema.id}:${schema.version}`}>{schema.key} · v{schema.version}</option>)}</select></label>
    {schemas.hasNextPage && <Button type="button" size="sm" variant="outline" loading={schemas.isFetchingNextPage} onClick={() => void schemas.fetchNextPage()}>Загрузить ещё формы</Button>}
  </div>;

  useEffect(() => {
    if (!open) return;
    const base = emptyDraft();
    if (route) {
      base.host = route.host;
      base.path = route.path;
      base.match = route.match === "prefix" ? "prefix" : "exact";
      if (route.target.kind === "core-file" || route.target.kind === "static-site-artifact") {
        base.targetKind = route.target.kind;
        base.file = { id: route.target.file_id };
      } else if (route.target.kind === "registered-operation" && publicFormOperations.has(route.target.operation_id)) {
        const config = route.target.operation_config;
        base.targetKind = route.target.operation_id as RouteDraft["targetKind"];
        const form = typeof config.form === "object" && config.form !== null ? config.form as Record<string, unknown> : config;
        base.snapshotID = typeof form.snapshot_id === "string" ? form.snapshot_id : "";
        base.formID = typeof form.form_id === "string" ? form.form_id : "";
        base.schemaVersion = typeof form.schema_version === "number" ? form.schema_version : 0;
        if (route.target.operation_id === "public.form-upload") {
          base.accessGroup = typeof config.access_group === "string" ? config.access_group : "";
          base.fileSetID = typeof config.file_set_id === "string" ? config.file_set_id : "";
          base.maxBytes = typeof config.max_bytes === "number" ? String(config.max_bytes) : base.maxBytes;
          base.allowedContentTypes = Array.isArray(config.allowed_content_types) ? config.allowed_content_types.filter((item: unknown): item is string => typeof item === "string") : [];
          base.bindForm = config.form !== undefined;
        }
      }
    }
    setDraft(base);
  }, [open, route]);

  const target = buildRouterTarget(draft, operations);
  const canSave = Boolean(draft.host.trim() && draft.path.startsWith("/") && target && !saving);

  return <>
    <Dialog open={open} onOpenChange={(next) => !next && !saving && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{route ? "Изменить цель маршрута" : "Новый публичный маршрут"}</DialogTitle>
          <DialogDescription>
            {route ? "Путь и область маршрута останутся прежними. Изменение цели применяется с проверкой текущей ревизии." : "Маршрут становится доступен на настроенной ветке после сохранения."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Host</span><Input required maxLength={253} value={draft.host} disabled={Boolean(route)} onChange={(event) => setDraft((current) => ({ ...current, host: event.currentTarget.value }))} placeholder="www.example.com" /></label>
            <label className="space-y-1.5 text-sm"><span className="text-muted-foreground">Путь</span><Input required maxLength={2048} value={draft.path} disabled={Boolean(route)} onChange={(event) => setDraft((current) => ({ ...current, path: event.currentTarget.value }))} placeholder="/uploads/document.pdf" /></label>
          </div>
          {!route && <label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">Сопоставление пути</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={draft.match} onChange={(event) => setDraft((current) => ({ ...current, match: event.currentTarget.value as RouteDraft["match"] }))}><option value="exact">Точное совпадение</option><option value="prefix" disabled={draft.targetKind !== "static-site-artifact"}>Префикс пути</option></select></label>}
          <fieldset className="space-y-2"><legend className="text-sm font-medium">Цель</legend>
            <div className="grid gap-2 sm:grid-cols-2">
              <button type="button" disabled={Boolean(route && route.match === "prefix")} title={route?.match === "prefix" ? "Для публичного файла нужен exact-маршрут" : undefined} className={`min-h-11 rounded-md border px-3 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50 ${draft.targetKind === "core-file" ? "border-primary bg-accent" : "border-border"}`} onClick={() => setDraft((current) => ({ ...current, targetKind: "core-file", match: "exact", file: current.targetKind === "core-file" ? current.file : null }))}>Файл Core</button>
              <button type="button" className={`min-h-11 rounded-md border px-3 text-left text-sm ${draft.targetKind === "static-site-artifact" ? "border-primary bg-accent" : "border-border"}`} onClick={() => setDraft((current) => ({ ...current, targetKind: "static-site-artifact", file: current.targetKind === "static-site-artifact" ? current.file : null }))}>Артефакт статической страницы</button>
              {!route && <>
                <button type="button" disabled={!uploadOperation?.methods.includes("POST")} title={!uploadOperation?.methods.includes("POST") ? "Операция загрузки недоступна в этой сборке" : undefined} className={`min-h-11 rounded-md border px-3 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50 ${draft.targetKind === "public.form-upload" ? "border-primary bg-accent" : "border-border"}`} onClick={() => { if (!uploadOperation?.methods.includes("POST")) return; setDraft((current) => ({ ...current, targetKind: "public.form-upload", match: "exact", maxBytes: current.maxBytes || String(Math.min(uploadOperation.max_request_bytes, 10 * 1024 * 1024)) })); }}>Загрузка файла</button>
                <button type="button" disabled={!sessionOperation} title={!sessionOperation ? "Операция сессии недоступна в этой сборке" : undefined} className={`min-h-11 rounded-md border px-3 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50 ${draft.targetKind === "public.form-session" ? "border-primary bg-accent" : "border-border"}`} onClick={() => sessionOperation && setDraft((current) => ({ ...current, targetKind: "public.form-session", match: "exact" }))}>Сессия формы</button>
                <button type="button" disabled={!submitOperation?.methods.includes("POST")} title={!submitOperation ? "Операция отправки недоступна в этой сборке" : undefined} className={`min-h-11 rounded-md border px-3 text-left text-sm disabled:cursor-not-allowed disabled:opacity-50 ${draft.targetKind === "public.form-submit" ? "border-primary bg-accent" : "border-border"}`} onClick={() => submitOperation && setDraft((current) => ({ ...current, targetKind: "public.form-submit", match: "exact" }))}>Отправка формы</button>
              </>}
            </div>
          </fieldset>
          {operationsLoading && <p role="status" className="text-xs text-muted-foreground">Загружаем возможности Router…</p>}
          {operationsError && <p role="alert" className="text-xs text-destructive">Не удалось загрузить возможности Router. <button type="button" className="underline underline-offset-2" onClick={onRetryOperations}>Повторить</button></p>}
          {!operationsLoading && !operationsError && !uploadOperation?.methods.includes("POST") && !sessionOperation && !submitOperation && <p role="status" className="text-xs text-muted-foreground">Публичные операции недоступны в этой сборке Router.</p>}
          {draft.targetKind === "public.form-upload" ? <div className="space-y-3 rounded-md border border-border p-3">
            <p className="text-xs leading-5 text-muted-foreground">Файл создаётся приватным. Размер и тип содержимого ограничиваются настройками этого маршрута.</p>
            {draft.bindForm && formBindingEditor}
            <label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">Группа доступа для нового файла</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={draft.accessGroup} onChange={(event) => setDraft((current) => ({ ...current, accessGroup: event.currentTarget.value }))}><option value="">Выберите группу</option>{writableGroups.map((group) => <option key={group.key} value={group.key}>{group.key}</option>)}</select></label>
            <div className="space-y-1.5"><label htmlFor="router-upload-file-set" className="text-sm"><span className="text-muted-foreground">Набор для поиска · необязательно</span></label><select id="router-upload-file-set" className="h-11 w-full rounded-md border border-input bg-background px-3" value={draft.fileSetID} onChange={(event) => setDraft((current) => ({ ...current, fileSetID: event.currentTarget.value }))}><option value="">Не добавлять в набор</option>{availableFileSets.map((set) => <option key={set.id} value={set.id}>{set.name} · {set.namespace}</option>)}</select><p className="text-xs leading-5 text-muted-foreground">Набор служит только для поиска и не открывает файлы посетителям.</p>{fileSets.isLoading && <p role="status" className="text-xs text-muted-foreground">Загружаем наборы файлов…</p>}{!fileSets.isLoading && !fileSets.error && availableFileSets.length === 0 && <p className="text-xs text-muted-foreground">Нет доступных наборов файлов.</p>}{fileSets.error && <p role="alert" className="text-xs text-destructive">Не удалось загрузить наборы файлов. <button type="button" className="underline underline-offset-2" onClick={() => void fileSets.refetch()}>Повторить</button></p>}{fileSets.hasNextPage && <Button type="button" size="sm" variant="outline" disabled={fileSets.isFetchingNextPage} loading={fileSets.isFetchingNextPage} onClick={() => void fileSets.fetchNextPage()}>Загрузить ещё наборы</Button>}</div>
            <label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">Максимальный размер, байт</span><Input type="number" min={1} max={uploadOperation?.max_request_bytes ?? 0} required disabled={!uploadOperation} value={draft.maxBytes} onChange={(event) => setDraft((current) => ({ ...current, maxBytes: event.currentTarget.value }))} /><span className="block text-xs text-muted-foreground">Предел операции: {uploadOperation?.max_request_bytes.toLocaleString("ru-RU") ?? "недоступен"} байт</span></label>
            <fieldset className="space-y-2"><legend className="text-sm text-muted-foreground">Разрешённые типы</legend>{(uploadOperation?.allowed_content_types ?? []).map((contentType) => <label className="flex min-h-9 items-center gap-2 text-sm" key={contentType}><input type="checkbox" checked={draft.allowedContentTypes.includes(contentType)} onChange={(event) => setDraft((current) => ({ ...current, allowedContentTypes: event.currentTarget.checked ? [...current.allowedContentTypes, contentType] : current.allowedContentTypes.filter((value) => value !== contentType) }))} /><span>{contentType}</span></label>)}{uploadOperation && uploadOperation.allowed_content_types.length === 0 && <p className="text-xs text-muted-foreground">Нет разрешённых типов файлов.</p>}</fieldset>
            <label className="flex min-h-10 items-center gap-2 text-sm"><input type="checkbox" checked={draft.bindForm} onChange={(event) => setDraft((current) => ({ ...current, bindForm: event.currentTarget.checked }))} /><span>Использовать как вложение формы</span></label>
            {draft.bindForm && !draft.fileSetID && <p role="status" className="text-xs text-muted-foreground">Для вложения формы выберите набор файлов.</p>}
            {(!uploadOperation || !uploadOperation.methods.includes("POST") || !draft.accessGroup || !Number.isSafeInteger(Number(draft.maxBytes)) || Number(draft.maxBytes) < 1 || Number(draft.maxBytes) > uploadOperation.max_request_bytes || draft.allowedContentTypes.length === 0 || draft.allowedContentTypes.some((type) => !uploadOperation.allowed_content_types.includes(type))) && <p role="status" className="text-xs text-muted-foreground">Выберите группу, допустимый лимит и хотя бы один тип из списка операции.</p>}
          </div> : requiresFormBinding ? formBindingEditor : <div className="space-y-2">
            {draft.targetKind === "static-site-artifact" ? <p role="status" className="text-xs leading-5 text-muted-foreground">Выберите файл manifest артефакта статической страницы. Router проверяет manifest и обслуживает только объявленный документ и его assets; это не делает остальные файлы или FileSet публичными.</p> : <p className="text-xs leading-5 text-muted-foreground">Exact GET-маршрут открывает один выбранный файл посетителям. ID файла задаётся здесь и не выбирается посетителем; проверка права чтения Router выполняется сервером.</p>}
            {draft.targetKind === "core-file" || draft.targetKind === "static-site-artifact" ? (draft.file ? <div className="flex items-center gap-3 rounded-md border border-border p-3"><span className="min-w-0 flex-1 truncate text-sm">{draft.file.original_filename || draft.file.id}</span><Button type="button" variant="outline" onClick={openFilePicker}>Выбрать файл</Button></div> : <Button type="button" variant="outline" className="min-h-11" onClick={openFilePicker}>{draft.targetKind === "static-site-artifact" ? "Выбрать manifest артефакта" : "Выбрать файл из Core"}</Button>) : null}
          </div>}
        </div>
        <DialogFooter><Button type="button" variant="outline" onClick={onClose} disabled={saving}>Отмена</Button><Button type="button" disabled={!canSave} loading={saving} onClick={() => void onSave(draft)}>{route ? "Сохранить цель" : "Создать маршрут"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
    <MediaPickerDialog open={pickerOpen} onClose={() => setPickerOpen(false)} onSelect={(items) => setDraft((current) => ({ ...current, file: items[0] ?? null }))} title={draft.targetKind === "static-site-artifact" ? "Выбрать manifest артефакта" : "Выбрать файл для маршрута"} />
  </>;
}

export default function RouterRoutesPage() {
  useDocumentTitle("Публичные маршруты");
  const { principal } = useAuth();
  const canWrite = hasGroupAccess(principal, "router_routes", 2);
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [host, setHost] = useState("");
  const [dialogRoute, setDialogRoute] = useState<RouterRoute | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const routes = useInfiniteQuery({
    queryKey: [...routesKey, host],
    queryFn: ({ pageParam, signal }) => api.router.routes.list({ host: host.trim() || undefined, limit: 100, cursor: pageParam ?? undefined }, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });
  const routeItems = useMemo(() => routes.data?.pages.flatMap((page) => page.items) ?? [], [routes.data]);
  const groups = useQuery({ queryKey: ["access", "groups"], queryFn: ({ signal }) => api.principals.groups(signal) });
  const operations = useQuery({ queryKey: ["router", "operations"], queryFn: ({ signal }) => api.router.operations.list(signal) });
  const publicOperations = operations.data?.items ?? [];
  const save = useMutation({
    mutationFn: async (draft: RouteDraft) => {
      const target = buildRouterTarget(draft, publicOperations);
      if (!target) throw new Error("Проверьте цель маршрута");
      if (target.kind === "registered-operation") {
        const message = target.operation_id === "public.form-upload"
          ? "Посетители смогут загружать файлы через Loader в выбранную приватную группу; для формы также фиксируются сессия, схема и FileSet."
          : "Посетители смогут создавать или отправлять ответы только для закреплённой формы и версии схемы.";
        if (!(await confirm({ title: "Включить публичную операцию?", description: message, confirmLabel: "Включить маршрут" }))) return null;
      } else {
        const artifact = target.kind === "static-site-artifact";
        if (!(await confirm({ title: artifact ? "Опубликовать артефакт статической страницы?" : "Опубликовать файл по маршруту?", description: artifact ? "Посетители смогут получить документ и только assets, объявленные в выбранном static-page-artifact/v1 manifest. Других файлов маршрут не открывает." : "Любой посетитель, который знает адрес, сможет получить выбранный файл через этот маршрут.", confirmLabel: "Сделать доступным" }))) return null;
      }
      if (dialogRoute) return api.router.routes.replaceTarget(dialogRoute.id, target, dialogRoute.revision);
      return api.router.routes.create({ host: draft.host.trim(), path: draft.path.trim(), match: draft.match, target }, `router:${crypto.randomUUID()}`);
    },
    onSuccess: async (route) => {
      if (!route) return;
      await queryClient.invalidateQueries({ queryKey: routesKey });
      setDialogOpen(false);
      setDialogRoute(null);
      toast.success(dialogRoute ? "Цель маршрута обновлена" : "Маршрут создан");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const remove = useMutation({
    mutationFn: async (route: RouterRoute) => {
      if (!(await confirm({ title: "Удалить публичный маршрут?", description: `${route.host}${route.path} перестанет обслуживаться Router.`, confirmLabel: "Удалить маршрут", destructive: true }))) return false;
      await api.router.routes.delete(route.id, route.revision);
      return true;
    },
    onSuccess: async (removed) => { if (removed) { await queryClient.invalidateQueries({ queryKey: routesKey }); toast.success("Маршрут удалён"); } },
    onError: (error) => toast.error(errorMessage(error)),
  });
  const openCreate = () => { setDialogRoute(null); setDialogOpen(true); };

  return <div className="page-grid">
    <PageHeader title="Публичные маршруты" description="Настроенные маршруты текущей ветки. Файловые цели становятся доступны посетителям только через явно сохранённый маршрут." actions={<Button type="button" variant="outline" onClick={() => void routes.refetch()} loading={routes.isFetching}><RefreshCw />Обновить</Button>} />
    <div className="flex flex-col gap-2 sm:flex-row"><label className="min-w-0 flex-1 space-y-1 text-sm"><span className="text-muted-foreground">Фильтр по host</span><Input value={host} maxLength={253} onChange={(event) => setHost(event.currentTarget.value)} placeholder="www.example.com" /></label>{canWrite && <Button type="button" className="min-h-11 self-end" onClick={openCreate}><Plus />Новый маршрут</Button>}</div>
    {routes.isLoading ? <p role="status" className="py-8 text-center text-sm text-muted-foreground">Загружаем маршруты…</p> : routes.error ? <ErrorState error={routes.error} title="Не удалось загрузить маршруты" onRetry={() => void routes.refetch()} /> : routeItems.length === 0 ? <EmptyState icon={Route} title="Маршрутов пока нет" description={canWrite ? "Создайте точный маршрут для файла или явный маршрут загрузки через Loader." : "Маршруты появятся здесь после настройки администратором."} /> : <div className="overflow-hidden rounded-lg border border-border bg-card"><div className="divide-y divide-border">{routeItems.map((route) => <article key={route.id} className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center"><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-2"><h2 className="break-all font-mono text-sm font-medium">{route.host}{route.path}</h2><Badge variant={route.state === "active" ? "secondary" : "outline"}>{route.state}</Badge><Badge variant="outline">{route.match}</Badge></div><p className="mt-1 flex items-center gap-2 break-all text-xs text-muted-foreground">{targetIcon(route.target)}<span>{targetLabel(route.target)}</span></p>{targetDetails(route.target) && <p className="mt-1 break-all text-2xs text-muted-foreground">{targetDetails(route.target)}</p>}<p className="mt-1 text-2xs text-muted-foreground">Ветка {route.branch_id} · ревизия {route.revision}</p></div>{canWrite && <div className="flex shrink-0 gap-2">{canEditTarget(route) && <Button type="button" variant="outline" size="sm" onClick={() => { setDialogRoute(route); setDialogOpen(true); }}>Изменить цель</Button>}<Button type="button" variant="ghost" size="icon" aria-label={`Удалить маршрут ${route.path}`} disabled={remove.isPending} onClick={() => void remove.mutateAsync(route)}><Trash2 className="size-4" /></Button></div>}</article>)}</div></div>}
    {routes.hasNextPage && <div className="flex justify-center"><Button type="button" variant="outline" loading={routes.isFetchingNextPage} disabled={routes.isFetchingNextPage} onClick={() => void routes.fetchNextPage()}>Загрузить ещё маршруты</Button></div>}
    <RouteDialog open={dialogOpen} route={dialogRoute} groups={groups.data ?? []} operations={publicOperations} operationsLoading={operations.isLoading} operationsError={Boolean(operations.error)} onRetryOperations={() => void operations.refetch()} saving={save.isPending} onClose={() => { setDialogOpen(false); setDialogRoute(null); }} onSave={(draft) => save.mutateAsync(draft).then(() => undefined)} />
  </div>;
}
