import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, Outlet, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, isApiError } from "@/api";
import { useAuth } from "@/app/auth";
import { ErrorState, useDocumentTitle, useUnsavedChangesGuard } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import type { DataModel, SiteConfig, SiteContentSchema, SiteEditorDraft, SiteMetadata, SiteSnapshot } from "@/types";
import { hasCompetingSiteDraftChange, parseSiteDraft, siteDraftKey } from "./workspace-draft";
import { featureVersionChanges, normalizeFeatureComposition } from "./feature-selection";
import { siteContextQuery } from "./site-query";
import { readySnapshots } from "@/domains/snapshots/lifecycle";
import { hasGroupAccess } from "@/lib/permissions";

type Workspace = {
  config: SiteEditorDraft;
  dataModel: DataModel | null;
  modelRevision: number;
  modelDirty: boolean;
  modelConflict: boolean;
  metadata: SiteMetadata;
  revision: number;
  snapshot: SiteSnapshot | null;
  dirty: boolean;
  saving: boolean;
  conflict: boolean;
  localSaveError: boolean;
  update: (next: SiteEditorDraft | ((current: SiteEditorDraft) => SiteEditorDraft)) => void;
  updateDataModel: (next: DataModel | ((current: DataModel) => DataModel)) => void;
  updateMetadata: (next: SiteMetadata | ((current: SiteMetadata) => SiteMetadata)) => void;
  save: (name?: string) => Promise<boolean>;
  saveDataModel: () => Promise<boolean>;
  reload: () => Promise<void>;
};

const WorkspaceContext = createContext<Workspace | null>(null);
const SNAPSHOT_NAME_LIMIT = 120;
const siteEditorModes = [
  ["/site/pages", "Страницы"],
  ["/site/templates", "Шаблоны"],
  ["/site/features", "Версии"],
  ["/site/settings", "Настройки сайта"],
  ["/site/data-model", "Модель данных"],
] as const;
const siteEditorModePaths = new Set<string>(siteEditorModes.map(([path]) => path));
function isSiteEditorPath(pathname: string): boolean {
  return siteEditorModePaths.has(pathname)
    || pathname.startsWith("/site/pages/")
    || pathname.startsWith("/site/data-model/");
}

function childSnapshotName(snapshot: SiteSnapshot): string {
  const base = snapshot.name.replace(/(?: · изменение(?: после #\d+)?)$/u, "").trim() || snapshot.name.trim();
  const suffix = ` · изменение после #${snapshot.sequence}`;
  const maxBaseLength = SNAPSHOT_NAME_LIMIT - Array.from(suffix).length;
  const shortBase = Array.from(base).slice(0, maxBaseLength).join("").trimEnd();
  return `${shortBase}${suffix}`;
}

export function useSiteWorkspace() {
  const value = useContext(WorkspaceContext);
  if (!value) throw new Error("useSiteWorkspace must be used inside SiteWorkspace");
  return value;
}

function editorDraftFromConfig(config: SiteConfig): SiteEditorDraft {
  return {
    formatVersion: config.format_version,
    pages: [...config.pages],
    routeTree: { id: `site-${config.pages[0].id}`, type: "site", children: [] },
  };
}

function emptyConfig(): SiteEditorDraft {
  const pageId = crypto.randomUUID();
  return {
    formatVersion: 3,
    pages: [{ id: pageId, name: "Главная", kind: "page", placements: [] }],
    routeTree: { id: `site-${pageId}`, type: "site", children: [] },
  };
}

export function SiteWorkspace({ children }: { children?: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const principal = useAuth().principal;
  const canManageSite = hasGroupAccess(principal, "features", 2);
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const selected = params.get("snapshot") ?? "";
  const requestedSiteID = params.get("site_id") ?? "";
  const requestedPageID = params.get("page_id") ?? "";
  useDocumentTitle("Снапшоты");

  const snapshots = useQuery({
    queryKey: ["site", "snapshot-choices", requestedSiteID, requestedPageID],
    queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal, requestedPageID || undefined, requestedSiteID || undefined),
  });
  const readySnapshotItems = readySnapshots(snapshots.data?.items ?? []);
  const selectedSnapshotState = snapshots.data?.items.find((item) => item.id === selected)?.state;
  const waitingForSelection = !selected && (snapshots.isLoading || snapshots.isError || readySnapshotItems.length > 0);
  const [config, setConfig] = useState<SiteEditorDraft | null>(null);
  const [savedConfig, setSavedConfig] = useState<SiteEditorDraft | null>(null);
  const [dataModel, setDataModel] = useState<DataModel | null>(null);
  const [savedDataModel, setSavedDataModel] = useState<DataModel | null>(null);
  const [modelRevision, setModelRevision] = useState(0);
  const [modelSaving, setModelSaving] = useState(false);
  const [modelConflict, setModelConflict] = useState(false);
  const [metadata, setMetadata] = useState<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const [savedMetadata, setSavedMetadata] = useState<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const [snapshot, setSnapshot] = useState<SiteSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [localSaveError, setLocalSaveError] = useState(false);
  const currentConfig = useRef<SiteEditorDraft | null>(null);
  const currentDataModel = useRef<DataModel | null>(null);
  const savedDataModelRef = useRef<DataModel | null>(null);
  const currentMetadata = useRef<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const savedConfigRef = useRef<SiteEditorDraft | null>(null);
  const savedMetadataRef = useRef<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const lastLocalDraft = useRef<string | null>(null);
  const localDraftConflict = useRef(false);
  const requestNumber = useRef(0);
  const createdSelection = useRef("");
  const createAttempt = useRef<{ fingerprint: string; key: string } | null>(null);
  const guardBypass = useRef(false);
  const userId = principal?.id ?? "";

  useEffect(() => {
    if (!userId || typeof window === "undefined") return;
    const key = siteDraftKey(userId, selected);
    const onStorage = (event: StorageEvent) => {
      if (!hasCompetingSiteDraftChange(event.key, key, lastLocalDraft.current, event.newValue)) return;
      localDraftConflict.current = true;
      setLocalSaveError(true);
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [userId, selected]);

  const reload = useCallback(async () => {
    const request = ++requestNumber.current;
    setLoading(true);
    setError(null);
    try {
      const detail = selected ? await api.siteBuilder.snapshot(selected) : null;
      let modelSchema: SiteContentSchema | null = null;
      if (detail?.snapshot.siteId) {
        try {
          modelSchema = await api.website.siteModel(detail.snapshot.siteId);
        } catch (reason) {
          if (!isApiError(reason) || reason.status !== 404) throw reason;
          modelSchema = {
            version: 1,
            siteId: detail.snapshot.siteId,
            modelRevision: 0,
            digest: "",
            dataModel: { formatVersion: 1, entities: [], positions: {} },
          };
        }
      }
      if (request !== requestNumber.current) return;
      let initial = detail ? editorDraftFromConfig(detail.payload.config) : emptyConfig();
      const initialMetadata = detail?.payload.metadata ?? {formatVersion: 1 as const, siteName: detail?.snapshot.name ?? "Website", pages: {}};
      let restored = initial;
      let restoredMetadata = initialMetadata;
      try {
        const raw = principal ? localStorage.getItem(siteDraftKey(principal.id, selected)) : null;
        lastLocalDraft.current = raw;
        const local = parseSiteDraft(raw);
        localDraftConflict.current = Boolean(raw && (!local || (selected && JSON.stringify(local.baseline) !== JSON.stringify(initial))));
        if (local && (!selected || JSON.stringify(local.baseline) === JSON.stringify(initial))) {
          if (!selected) initial = local.baseline;
          restored = local.config;
          if (local.metadata && local.baselineMetadata && JSON.stringify(local.baselineMetadata) === JSON.stringify(initialMetadata)) restoredMetadata = local.metadata;
          if (JSON.stringify(restored) !== JSON.stringify(initial)) toast.info("Восстановлены локальные изменения редактора");
        } else if (local) toast.warning("Локальные изменения относятся к другой версии снапшота и сохранены в браузере.");
        setLocalSaveError(localDraftConflict.current);
      } catch { setLocalSaveError(true); }
      currentConfig.current = restored;
      savedConfigRef.current = initial;
      setConfig(restored);
      setSavedConfig(initial);
      currentDataModel.current = modelSchema?.dataModel ?? null;
      savedDataModelRef.current = modelSchema?.dataModel ?? null;
      setDataModel(modelSchema?.dataModel ?? null);
      setSavedDataModel(modelSchema?.dataModel ?? null);
      setModelRevision(modelSchema?.modelRevision ?? 0);
      setModelConflict(false);
      setMetadata(restoredMetadata);
      savedMetadataRef.current = initialMetadata;
      setSavedMetadata(initialMetadata);
      currentMetadata.current = restoredMetadata;
      setSnapshot(detail?.snapshot ?? null);
      setConflict(false);
    } catch (reason) {
      if (request === requestNumber.current) setError(reason);
    } finally {
      if (request === requestNumber.current) setLoading(false);
    }
  }, [selected, principal]);

  useEffect(() => {
    if (!selected && readySnapshotItems.length > 0) {
      const first = readySnapshotItems[0]!;
      setParams((current) => { const next = new URLSearchParams(current); next.set("snapshot", first.id); next.set("site_id", first.siteId); next.set("page_id", first.pageId); return next; }, { replace: true });
    }
  }, [selected, setParams, readySnapshotItems]);

  useEffect(() => {
    if (waitingForSelection) return;
    if (createdSelection.current === selected && selected) {
      // The save already installed the new snapshot and preserved any edits
      // made while its request was in flight. A refetch here would erase them.
      createdSelection.current = "";
      return;
    }
    void reload();
    return () => { requestNumber.current += 1; };
  }, [reload, selected, waitingForSelection]);

  useEffect(() => {
    if (!selected || snapshot?.id !== selected || !snapshot.siteId || !snapshot.pageId || (requestedSiteID === snapshot.siteId && requestedPageID === snapshot.pageId)) return;
    setParams((current) => { const next = new URLSearchParams(current); next.set("site_id", snapshot.siteId); next.set("page_id", snapshot.pageId); return next; }, { replace: true });
  }, [requestedPageID, requestedSiteID, selected, setParams, snapshot]);

  useEffect(() => {
    if (!selected || !snapshot || snapshot.id !== selected || !selectedSnapshotState || selectedSnapshotState === snapshot.state) return;
    if (selectedSnapshotState === "ready") {
      void reload();
      return;
    }
    setSnapshot({ ...snapshot, state: selectedSnapshotState });
  }, [reload, selected, selectedSnapshotState, snapshot]);

  const dirty = useMemo(() => config !== null && savedConfig !== null && JSON.stringify(config) !== JSON.stringify(savedConfig), [config, savedConfig]);
  const modelDirty = dataModel !== null && savedDataModel !== null && JSON.stringify(dataModel) !== JSON.stringify(savedDataModel);
  const allDirty = dirty || modelDirty || JSON.stringify(metadata) !== JSON.stringify(savedMetadata);
  const guard = useUnsavedChangesGuard(allDirty, guardBypass, {
    allowPathChange: (current, next) => {
      return isSiteEditorPath(current) && isSiteEditorPath(next);
    },
    protectSearch: (current, next) => {
      const currentParams = new URLSearchParams(current);
      const nextParams = new URLSearchParams(next);
      return currentParams.get("snapshot") !== nextParams.get("snapshot") || currentParams.get("site_id") !== nextParams.get("site_id") || currentParams.get("page_id") !== nextParams.get("page_id");
    },
  });

  const persistLocal = useCallback((updated: SiteEditorDraft, updatedMetadata: SiteMetadata) => {
    if (!principal || !savedConfigRef.current) return;
    try {
      const key = siteDraftKey(principal.id, selected);
      if (localDraftConflict.current || localStorage.getItem(key) !== lastLocalDraft.current) throw new Error("Local draft changed in another tab");
      const raw = JSON.stringify({ version: 1, baseline: savedConfigRef.current, config: updated, baselineMetadata: savedMetadataRef.current, metadata: updatedMetadata });
      localStorage.setItem(key, raw);
      lastLocalDraft.current = raw;
      setLocalSaveError(false);
    } catch { setLocalSaveError(true); }
  }, [principal, selected]);
  const update: Workspace["update"] = (next) => {
    const previous = currentConfig.current;
    if (!previous) return;
    const updated = normalizeFeatureComposition(typeof next === "function" ? next(previous) : next);
    persistLocal(updated, currentMetadata.current);
    currentConfig.current = updated;
    setConfig(updated);
  };
  const updateDataModel: Workspace["updateDataModel"] = (next) => {
    const previous = currentDataModel.current;
    if (!previous) return;
    const updated = typeof next === "function" ? next(previous) : next;
    currentDataModel.current = updated;
    setDataModel(updated);
    setModelConflict(false);
  };
  const updateMetadata: Workspace["updateMetadata"] = (next) => {
    const updated = typeof next === "function" ? next(currentMetadata.current) : next;
    currentMetadata.current = updated;
    persistLocal(currentConfig.current ?? emptyConfig(), updated);
    setMetadata(updated);
  };

  const saveDataModel = useCallback(async () => {
    const submitted = currentDataModel.current;
    if (!submitted || !snapshot || modelSaving) return false;
    if (savedDataModelRef.current && JSON.stringify(submitted) === JSON.stringify(savedDataModelRef.current)) return true;
    setModelSaving(true);
    setModelConflict(false);
    try {
      const saved = await api.website.saveSiteModel(snapshot.siteId, modelRevision, submitted);
      savedDataModelRef.current = saved.dataModel;
      setSavedDataModel(saved.dataModel);
      setModelRevision(saved.modelRevision);
      if (currentDataModel.current === submitted) {
        currentDataModel.current = saved.dataModel;
        setDataModel(saved.dataModel);
      }
      toast.success("Модель сайта сохранена");
      return true;
    } catch (reason) {
      if (isApiError(reason) && reason.status === 409) setModelConflict(true);
      toast.error(reason instanceof Error ? `Модель не сохранена: ${reason.message}` : "Модель не сохранена");
      return false;
    } finally {
      setModelSaving(false);
    }
  }, [modelRevision, modelSaving, snapshot]);

  const save = useCallback(async (name?: string) => {
    if (modelDirty && !(await saveDataModel())) return false;
    const current = currentConfig.current;
    const submitted = current ? normalizeFeatureComposition(current) : null;
    const submittedMetadata = currentMetadata.current;
    if (!submitted || saving) return false;
    if (submitted !== current) {
      currentConfig.current = submitted;
      setConfig(submitted);
      persistLocal(submitted, submittedMetadata);
    }
    const isDirtyNow = () => currentConfig.current !== savedConfigRef.current || JSON.stringify(currentMetadata.current) !== JSON.stringify(savedMetadataRef.current);
    if (!isDirtyNow() && snapshot && name === undefined) return true;
    if (!snapshot) {
      toast.error("Новый корневой снапшот нельзя создать из Admin: текущий API не предоставляет источник настроенных site_id и page_id.");
      return false;
    }
    const versionChanges = featureVersionChanges(savedConfigRef.current ?? submitted, submitted);
    const changedDuringSave = () => currentConfig.current !== submitted || JSON.stringify(currentMetadata.current) !== JSON.stringify(submittedMetadata);
    setSaving(true);
    setConflict(false);
    try {
	  let parentSnapshotRequest: { parentSnapshotId: string; expectedParentRevision: number; expectedParentContentRevision: number } | undefined;
	  if (snapshot) {
	    const contentWorkspace = await api.contentEngine.workspace(snapshot.id);
	    if (snapshot.revision === undefined || !Number.isSafeInteger(contentWorkspace.revision) || contentWorkspace.revision < 0) {
	      throw new Error("Не удалось проверить ревизии родительского снапшота. Обновите страницу и повторите действие.");
	    }
	    parentSnapshotRequest = {
	      parentSnapshotId: snapshot.id,
	      expectedParentRevision: snapshot.revision,
	      expectedParentContentRevision: contentWorkspace.revision,
	    };
	    if (changedDuringSave()) {
	      toast.info("Настройки изменились во время проверки. Сохраните их ещё раз.");
	      return false;
	    }
	    if (versionChanges.length > 0) {
	      const accepted = await confirm({
	        title: "Изменить версии фич",
	        description: (
	          <div className="space-y-3 text-sm">
	            <p>Будет создан дочерний снапшот. Модель, опубликованные записи, черновики и выборы размещений скопируются из родителя; их редактирование в дочернем снапшоте станет независимым.</p>
	            <ul className="list-disc space-y-1 pl-5">{versionChanges.map((change) => <li key={change.featureKey}>{change.featureKey}: {change.fromVersion} → {change.toVersion}</li>)}</ul>
	            <p>Engine не преобразует данные между версиями фич. Новые bindings проверяются по манифесту выбранной сборки.</p>
	          </div>
	        ),
	        confirmLabel: "Создать снапшот",
	        cancelLabel: "Вернуться к настройкам",
	      });
	      if (!accepted) return false;
	      if (changedDuringSave()) {
	        toast.info("Настройки изменились во время проверки. Сохраните их ещё раз.");
	        return false;
	      }
	    }
	  }
      const input = {
        siteID: snapshot.siteId,
        pageID: snapshot.pageId,
        name: name?.trim() || childSnapshotName(snapshot),
        ...(parentSnapshotRequest ?? {}),
        config: submitted,
        metadata: submittedMetadata,
      };
      const fingerprint = JSON.stringify(input);
      if (!createAttempt.current || createAttempt.current.fingerprint !== fingerprint) {
        createAttempt.current = { fingerprint, key: `snapshot-${crypto.randomUUID()}` };
      }
      const created = await api.siteBuilder.createSnapshotFrom({ ...input, idempotencyKey: createAttempt.current.key });
      createAttempt.current = null;
      if (principal) {
        try {
          const oldKey = siteDraftKey(principal.id, selected);
          if (localDraftConflict.current || localStorage.getItem(oldKey) !== lastLocalDraft.current) throw new Error("Local draft changed in another tab");
          if (changedDuringSave() && currentConfig.current) {
            const raw = JSON.stringify({ version: 1, baseline: submitted, config: currentConfig.current, baselineMetadata: submittedMetadata, metadata: currentMetadata.current });
            localStorage.setItem(siteDraftKey(principal.id, created.id), raw);
            lastLocalDraft.current = raw;
          } else {
            lastLocalDraft.current = null;
          }
          localStorage.removeItem(oldKey);
          setLocalSaveError(false);
        } catch { setLocalSaveError(true); }
      }
      // Mark the actual submitted state as saved; a later local edit remains
      // dirty and must not be replaced by the older response.
      savedConfigRef.current = submitted;
      savedMetadataRef.current = submittedMetadata;
      setSavedConfig(submitted);
      setSavedMetadata(submittedMetadata);
      setSnapshot(created);
      createdSelection.current = created.id;
      guardBypass.current = true;
      setParams((current) => { const next = new URLSearchParams(current); next.set("snapshot", created.id); next.set("site_id", created.siteId); next.set("page_id", created.pageId); return next; }, { replace: true });
      void queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] });
      void queryClient.invalidateQueries({ queryKey: ["website", "snapshots"] });
      void queryClient.invalidateQueries({ queryKey: ["content"] });
      toast.success(created.state === "ready" ? `Создан снапшот #${created.sequence}` : `Снапшот #${created.sequence} создан и готовится`);
      if (created.warnings?.length) toast.warning(`Проверьте перенос данных: найдено проблем ${created.warnings.length}`);
      if (changedDuringSave()) {
        toast.info("Новые изменения ещё не сохранены");
        return true;
      }
      return true;
    } catch (reason) {
      if (isApiError(reason) && reason.status === 409) setConflict(true);
	      const message = reason instanceof Error ? reason.message : "Не удалось проверить или сохранить изменения";
	      toast.error(snapshot ? `Снапшот не создан: ${message}` : message);
      return false;
    } finally {
      setSaving(false);
    }
  }, [saving, snapshot, setParams, queryClient, confirm, principal, selected, persistLocal, modelDirty, saveDataModel]);

  if (snapshots.error) return <ErrorState error={snapshots.error} title="Не удалось загрузить конфигурацию сайта" onRetry={() => void snapshots.refetch()} />;
  if (loading || snapshots.isLoading || (!selected && readySnapshotItems.length > 0)) return <p className="p-8 text-sm text-muted-foreground">Загружаем снапшот…</p>;
  if (error || !config) return <ErrorState error={error} title="Не удалось загрузить снапшот" onRetry={() => void reload()} />;
  if (snapshot && snapshot.state !== "ready") {
    return <div className="page-grid p-6">
      <h1 className="text-lg font-semibold">{snapshot.state === "provisioning" ? "Снапшот готовится" : "Не удалось подготовить снапшот"}</h1>
      <p role="status" className="text-sm text-muted-foreground">{snapshot.state === "provisioning" ? "Копируются записи, публикации и выборы контента. Редактирование станет доступно после завершения." : "Этот снапшот нельзя редактировать или назначать ветке. Проверьте его состояние в дереве снапшотов."}</p>
      <Button type="button" variant="outline" className="w-fit" onClick={() => void snapshots.refetch()}>Обновить состояние</Button>
    </div>;
  }

  const canvas = location.pathname.startsWith("/site/pages")
    || location.pathname.startsWith("/site/templates")
    || location.pathname === "/site/data-model"
    || location.pathname.startsWith("/site/data-model/");
  const visibleModes = canManageSite
    ? siteEditorModes
    : siteEditorModes.filter(([path]) => path === "/site/pages" || path === "/site/templates");
  return (
    <WorkspaceContext.Provider value={{ config, dataModel, modelRevision, modelDirty, modelConflict, metadata, revision: snapshot?.revision ?? 0, snapshot, dirty: allDirty, saving: saving || modelSaving, conflict, localSaveError, update, updateDataModel, updateMetadata, save, saveDataModel, reload }}>
      {guard.dialog}
      <div className={canvas ? "flex h-full min-h-0 flex-col" : "min-w-0"}>
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2">
          <span className="text-xs text-muted-foreground">Снапшот</span>
          <Select value={selected || "new"} disabled={saving} onValueChange={async (value) => {
            if (allDirty && !(await confirm({ title: "Отбросить изменения?", description: "Несохранённые изменения редактора будут потеряны.", confirmLabel: "Отбросить", destructive: true }))) return;
            if (allDirty && principal) { try { const key = siteDraftKey(principal.id, selected); if (localDraftConflict.current || localStorage.getItem(key) !== lastLocalDraft.current) throw new Error("Local draft changed in another tab"); localStorage.removeItem(key); lastLocalDraft.current = null; } catch { toast.error("Локальная копия изменилась или недоступна. Сохраните правки перед переключением."); return; } }
            guardBypass.current = true;
            if (location.pathname.startsWith("/site/data-model/")) {
              const next = new URLSearchParams(location.search);
              if (value === "new") {
                next.delete("snapshot");
              } else {
                const item = snapshots.data?.items.find((candidate) => candidate.id === value);
                next.set("snapshot", value);
                if (item?.siteId) next.set("site_id", item.siteId);
                if (item?.pageId) next.set("page_id", item.pageId);
              }
              navigate({ pathname: "/site/data-model", search: next.size ? `?${next}` : "" });
            } else {
              setParams((current) => {
                const next = new URLSearchParams(current);
                if (value === "new") next.delete("snapshot");
                else {
                  const item = snapshots.data?.items.find((candidate) => candidate.id === value);
                  next.set("snapshot", value);
                  if (item?.siteId) next.set("site_id", item.siteId);
                  if (item?.pageId) next.set("page_id", item.pageId);
                }
                return next;
              });
            }
          }}>
            <SelectTrigger className="w-full sm:w-80" aria-label="Рабочий снапшот"><SelectValue /></SelectTrigger>
            <SelectContent>
              {readySnapshotItems.length === 0 && <SelectItem value="new">Первый снапшот</SelectItem>}
              {readySnapshotItems.map((item) => <SelectItem key={item.id} value={item.id}>#{item.sequence} · {item.name}</SelectItem>)}
            </SelectContent>
          </Select>
          <Button type="button" size="sm" variant="outline" onClick={() => void snapshots.refetch()} disabled={snapshots.isFetching}>Обновить список</Button>
          {!selected && readySnapshotItems.length === 0 && <span role="status" className="text-xs text-muted-foreground">Нет снапшотов. Для корневого снапшота нужны site_id и page_id из конфигурации запуска; текущий Admin API их не предоставляет.</span>}
          <nav aria-label="Режим редактора сайта" className="flex flex-wrap gap-1 rounded-md border border-border p-1">
            {visibleModes.map(([path, label]) => {
              const active = location.pathname === path
                || path === "/site/pages" && location.pathname.startsWith("/site/pages/")
                || path === "/site/data-model" && location.pathname.startsWith("/site/data-model/");
              return <Link key={path} to={`${path}${siteContextQuery(selected, snapshot?.pageId ?? requestedPageID, snapshot?.siteId ?? requestedSiteID)}`} aria-current={active ? "page" : undefined} className={`rounded px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${active ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent"}`}>{label}</Link>;
            })}
          </nav>
          <span role="status" aria-live="polite" className={`ml-auto text-xs ${allDirty ? "text-warning" : "text-success"}`}>{allDirty ? "Есть изменения" : "Сохранено"}</span>
          {(conflict || modelConflict) && <Button type="button" size="sm" variant="outline" disabled={saving} onClick={async () => {
            if (allDirty && !(await confirm({ title: "Загрузить актуальную версию?", description: "Несохранённые изменения локальной рабочей копии будут заменены.", confirmLabel: "Загрузить", cancelLabel: "Остаться", destructive: true }))) return;
            await reload();
          }}>Загрузить актуальную версию</Button>}
          {canManageSite && <Button type="button" size="sm" className="ml-auto" disabled={!allDirty || saving || !snapshot} onClick={() => void save()}>{saving ? "Сохраняем…" : "Сохранить"}</Button>}
          {localSaveError && <span role="alert" className="text-xs text-destructive">Локальное автосохранение недоступно или копия изменена в другой вкладке. Сохраните изменения в снапшот.</span>}
        </div>
        {snapshot?.warnings && snapshot.warnings.length > 0 && (
          <section role="alert" className="mx-4 mt-3 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
            <p className="font-medium">Проверьте совместимость содержимого</p>
            <ul className="mt-1 list-disc pl-5">{snapshot.warnings.map((warning, index) => <li key={`${index}-${warning}`}>{warning}</li>)}</ul>
          </section>
        )}
        <div className={canvas ? "min-h-0 flex-1 overflow-hidden" : ""}>{children ?? <Outlet />}</div>
      </div>
    </WorkspaceContext.Provider>
  );
}
