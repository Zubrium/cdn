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
import type { SiteConfig, SiteMetadata, SiteSnapshot } from "@/types";
import { hasCompetingSiteDraftChange, parseSiteDraft, siteDraftKey } from "./workspace-draft";
import type { StructuredDesignSystem } from "@/domains/design-system/types";
import { featureVersionChanges, normalizeFeatureComposition } from "./feature-selection";
import { siteContextQuery } from "./site-query";
import { readySnapshots } from "@/domains/snapshots/lifecycle";

type Workspace = {
  config: SiteConfig;
  metadata: SiteMetadata;
  designSystem: StructuredDesignSystem;
  revision: number;
  snapshot: SiteSnapshot | null;
  dirty: boolean;
  saving: boolean;
  conflict: boolean;
  localSaveError: boolean;
  update: (next: SiteConfig | ((current: SiteConfig) => SiteConfig)) => void;
  updateMetadata: (next: SiteMetadata | ((current: SiteMetadata) => SiteMetadata)) => void;
  updateDesignSystem: (next: StructuredDesignSystem | ((current: StructuredDesignSystem) => StructuredDesignSystem)) => void;
  save: (name?: string) => Promise<boolean>;
  reload: () => Promise<void>;
};

const WorkspaceContext = createContext<Workspace | null>(null);
const emptyDesignSystem: StructuredDesignSystem = {formatVersion: 2, groups: [], baseTokens: [], featureValues: {}, breakpoints: [], fonts: []};
const SNAPSHOT_NAME_LIMIT = 120;
const siteEditorModes = [
  ["/site/pages", "Страницы"],
  ["/site/features", "Версии"],
  ["/site/backend", "Бэкенд"],
  ["/site/settings", "Настройки сайта"],
  ["/site/design-system", "Дизайн-система"],
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

function emptyConfig(): SiteConfig {
  const pageId = crypto.randomUUID();
  return {
    formatVersion: 3,
    pages: [{ id: pageId, name: "Главная", kind: "page", placements: [] }],
    routeTree: { id: crypto.randomUUID(), type: "site", children: [{ id: crypto.randomUUID(), type: "path", path: "/", pageId, children: [] }] },
  };
}

export function SiteWorkspace({ children }: { children?: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const principal = useAuth().principal;
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const selected = params.get("snapshot") ?? "";
  const requestedSiteID = params.get("site_id") ?? "";
  const [rootSiteSelection, setRootSiteSelection] = useState(requestedSiteID);
  useDocumentTitle("Снапшоты");

  const snapshots = useQuery({
    queryKey: ["site", "snapshot-choices"],
    queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal),
    refetchInterval: (query) => query.state.data?.items.some((item) => item.state === "provisioning") ? 2000 : false,
  });
  const branches = useQuery({ queryKey: ["website", "branches"], queryFn: ({ signal }) => api.website.branches(signal) });
  const readySnapshotItems = readySnapshots(snapshots.data?.items ?? []);
  const selectedSnapshotState = snapshots.data?.items.find((item) => item.id === selected)?.state;
  const configuredSiteIDs = [...new Set((branches.data?.items ?? []).map((branch) => branch.site_id))].sort();
  const waitingForSelection = !selected && (snapshots.isLoading || branches.isLoading || snapshots.isError || branches.isError || readySnapshotItems.length > 0);
  const [config, setConfig] = useState<SiteConfig | null>(null);
  const [savedConfig, setSavedConfig] = useState<SiteConfig | null>(null);
  const [metadata, setMetadata] = useState<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const [savedMetadata, setSavedMetadata] = useState<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const [designSystem, setDesignSystem] = useState<StructuredDesignSystem>(emptyDesignSystem);
  const [savedDesignSystem, setSavedDesignSystem] = useState<StructuredDesignSystem>(emptyDesignSystem);
  const [snapshot, setSnapshot] = useState<SiteSnapshot | null>(null);
  const rootSiteID = requestedSiteID || rootSiteSelection || snapshot?.siteId || (configuredSiteIDs.length === 1 ? configuredSiteIDs[0]! : "");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [localSaveError, setLocalSaveError] = useState(false);
  const currentConfig = useRef<SiteConfig | null>(null);
  const currentMetadata = useRef<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const currentDesignSystem = useRef<StructuredDesignSystem>(emptyDesignSystem);
  const savedConfigRef = useRef<SiteConfig | null>(null);
  const savedMetadataRef = useRef<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const savedDesignSystemRef = useRef<StructuredDesignSystem>(emptyDesignSystem);
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
      const [detail, designDetail] = selected ? await Promise.all([api.siteBuilder.snapshot(selected), api.siteBuilder.designSnapshot(selected)]) : [null, null];
      if (request !== requestNumber.current) return;
      let initial = detail?.payload.config ?? emptyConfig();
      const initialMetadata = detail?.payload.metadata ?? {formatVersion: 1 as const, siteName: detail?.snapshot.name ?? "Website", pages: {}};
      let restored = initial;
      let restoredMetadata = initialMetadata;
      const initialDesign = designDetail?.designSystem ?? emptyDesignSystem;
      let restoredDesign = initialDesign;
      try {
        const raw = principal ? localStorage.getItem(siteDraftKey(principal.id, selected)) : null;
        lastLocalDraft.current = raw;
        const local = parseSiteDraft(raw);
        localDraftConflict.current = Boolean(raw && (!local || (selected && JSON.stringify(local.baseline) !== JSON.stringify(initial))));
        if (local && (!selected || JSON.stringify(local.baseline) === JSON.stringify(initial))) {
          if (!selected) initial = local.baseline;
          restored = local.config;
          if (local.metadata && local.baselineMetadata && JSON.stringify(local.baselineMetadata) === JSON.stringify(initialMetadata)) restoredMetadata = local.metadata;
          if (local.designSystem && local.baselineDesignSystem && JSON.stringify(local.baselineDesignSystem) === JSON.stringify(initialDesign)) restoredDesign = local.designSystem;
          if (JSON.stringify(restored) !== JSON.stringify(initial)) toast.info("Восстановлены локальные изменения редактора");
        } else if (local) toast.warning("Локальные изменения относятся к другой версии снапшота и сохранены в браузере.");
        setLocalSaveError(localDraftConflict.current);
      } catch { setLocalSaveError(true); }
      currentConfig.current = restored;
      savedConfigRef.current = initial;
      setConfig(restored);
      setSavedConfig(initial);
      setMetadata(restoredMetadata);
      savedMetadataRef.current = initialMetadata;
      setSavedMetadata(initialMetadata);
      setDesignSystem(restoredDesign);
      savedDesignSystemRef.current = initialDesign;
      setSavedDesignSystem(initialDesign);
      currentMetadata.current = restoredMetadata;
      currentDesignSystem.current = restoredDesign;
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
      setParams((current) => { const next = new URLSearchParams(current); next.set("snapshot", readySnapshotItems[0]!.id); return next; }, { replace: true });
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
    if (!selected || !snapshot || snapshot.id !== selected || !selectedSnapshotState || selectedSnapshotState === snapshot.state) return;
    if (selectedSnapshotState === "ready") {
      void reload();
      return;
    }
    setSnapshot({ ...snapshot, state: selectedSnapshotState });
  }, [reload, selected, selectedSnapshotState, snapshot]);

  const dirty = useMemo(() => config !== null && savedConfig !== null && JSON.stringify(config) !== JSON.stringify(savedConfig), [config, savedConfig]);
  const allDirty = dirty || JSON.stringify(metadata) !== JSON.stringify(savedMetadata) || JSON.stringify(designSystem) !== JSON.stringify(savedDesignSystem);
  const guard = useUnsavedChangesGuard(allDirty, guardBypass, {
    allowPathChange: (current, next) => {
      return isSiteEditorPath(current) && isSiteEditorPath(next);
    },
    protectSearch: (current, next) => new URLSearchParams(current).get("snapshot") !== new URLSearchParams(next).get("snapshot"),
  });

  const persistLocal = useCallback((updated: SiteConfig, updatedMetadata: SiteMetadata, updatedDesignSystem: StructuredDesignSystem) => {
    if (!principal || !savedConfigRef.current) return;
    try {
      const key = siteDraftKey(principal.id, selected);
      if (localDraftConflict.current || localStorage.getItem(key) !== lastLocalDraft.current) throw new Error("Local draft changed in another tab");
      const raw = JSON.stringify({ version: 1, baseline: savedConfigRef.current, config: updated, baselineMetadata: savedMetadataRef.current, metadata: updatedMetadata, baselineDesignSystem: savedDesignSystemRef.current, designSystem: updatedDesignSystem });
      localStorage.setItem(key, raw);
      lastLocalDraft.current = raw;
      setLocalSaveError(false);
    } catch { setLocalSaveError(true); }
  }, [principal, selected]);
  const update: Workspace["update"] = (next) => {
    const previous = currentConfig.current;
    if (!previous) return;
    const updated = normalizeFeatureComposition(typeof next === "function" ? next(previous) : next);
    persistLocal(updated, currentMetadata.current, currentDesignSystem.current);
    currentConfig.current = updated;
    setConfig(updated);
  };
  const updateMetadata: Workspace["updateMetadata"] = (next) => {
    const updated = typeof next === "function" ? next(currentMetadata.current) : next;
    currentMetadata.current = updated;
    persistLocal(currentConfig.current ?? emptyConfig(), updated, currentDesignSystem.current);
    setMetadata(updated);
  };
  const updateDesignSystem: Workspace["updateDesignSystem"] = (next) => {
    const updated = typeof next === "function" ? next(currentDesignSystem.current) : next;
    currentDesignSystem.current = updated;
    persistLocal(currentConfig.current ?? emptyConfig(), currentMetadata.current, updated);
    setDesignSystem(updated);
  };

  const save = useCallback(async (name?: string) => {
    const current = currentConfig.current;
    const submitted = current ? normalizeFeatureComposition(current) : null;
    const submittedMetadata = currentMetadata.current;
    const submittedDesignSystem = currentDesignSystem.current;
    if (!submitted || saving) return false;
    if (submitted !== current) {
      currentConfig.current = submitted;
      setConfig(submitted);
      persistLocal(submitted, submittedMetadata, submittedDesignSystem);
    }
    const isDirtyNow = () => currentConfig.current !== savedConfigRef.current || JSON.stringify(currentMetadata.current) !== JSON.stringify(savedMetadataRef.current) || JSON.stringify(currentDesignSystem.current) !== JSON.stringify(savedDesignSystemRef.current);
    if (!isDirtyNow() && snapshot && name === undefined) return true;
    const versionChanges = snapshot ? featureVersionChanges(savedConfigRef.current ?? submitted, submitted) : [];
    const changedDuringSave = () => currentConfig.current !== submitted || JSON.stringify(currentMetadata.current) !== JSON.stringify(submittedMetadata) || JSON.stringify(currentDesignSystem.current) !== JSON.stringify(submittedDesignSystem);
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
        siteID: snapshot?.siteId ?? rootSiteID,
        name: name?.trim() || (snapshot ? childSnapshotName(snapshot) : "Новый сайт"),
        ...(parentSnapshotRequest ?? {}),
        config: submitted,
        metadata: submittedMetadata,
        designSystem: submittedDesignSystem,
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
            const raw = JSON.stringify({ version: 1, baseline: submitted, config: currentConfig.current, baselineMetadata: submittedMetadata, metadata: currentMetadata.current, baselineDesignSystem: submittedDesignSystem, designSystem: currentDesignSystem.current });
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
      savedDesignSystemRef.current = submittedDesignSystem;
      setSavedConfig(submitted);
      setSavedMetadata(submittedMetadata);
      setSavedDesignSystem(submittedDesignSystem);
      setSnapshot(created);
      createdSelection.current = created.id;
      guardBypass.current = true;
      setParams((current) => { const next = new URLSearchParams(current); next.set("snapshot", created.id); next.delete("site_id"); return next; }, { replace: true });
      void queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] });
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
  }, [saving, snapshot, rootSiteID, setParams, queryClient, confirm, principal, selected, persistLocal]);

  if (snapshots.error || branches.error) return <ErrorState error={snapshots.error ?? branches.error} title="Не удалось загрузить конфигурацию сайта" onRetry={() => { void snapshots.refetch(); void branches.refetch(); }} />;
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
    || location.pathname === "/site/router"
    || location.pathname === "/site/data-model"
    || location.pathname.startsWith("/site/data-model/");
  return (
    <WorkspaceContext.Provider value={{ config, metadata, designSystem, revision: snapshot?.revision ?? 0, snapshot, dirty: allDirty, saving, conflict, localSaveError, update, updateMetadata, updateDesignSystem, save, reload }}>
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
                if (snapshot?.siteId) {
                  next.set("site_id", snapshot.siteId);
                  setRootSiteSelection(snapshot.siteId);
                }
              } else {
                next.set("snapshot", value);
                next.delete("site_id");
              }
              navigate({ pathname: "/site/data-model", search: next.size ? `?${next}` : "" });
            } else {
              setParams((current) => {
                const next = new URLSearchParams(current);
                if (value === "new") {
                  next.delete("snapshot");
                  if (snapshot?.siteId) {
                    next.set("site_id", snapshot.siteId);
                    setRootSiteSelection(snapshot.siteId);
                  }
                } else {
                  next.set("snapshot", value);
                  next.delete("site_id");
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
          {!selected && <Select value={rootSiteID || "none"} disabled={saving || configuredSiteIDs.length === 0} onValueChange={(siteID) => {
            setRootSiteSelection(siteID === "none" ? "" : siteID);
            setParams((current) => { const next = new URLSearchParams(current); if (siteID && siteID !== "none") next.set("site_id", siteID); else next.delete("site_id"); return next; });
          }}>
            <SelectTrigger className="w-full sm:w-72" aria-label="Сайт из конфигурации веток"><SelectValue placeholder="Выберите сайт" /></SelectTrigger>
            <SelectContent>
              {configuredSiteIDs.length === 0 ? <SelectItem value="none" disabled>Нет настроенных сайтов</SelectItem> : configuredSiteIDs.map((siteID) => <SelectItem key={siteID} value={siteID}>Сайт · {siteID}</SelectItem>)}
            </SelectContent>
          </Select>}
          <nav aria-label="Режим редактора сайта" className="flex flex-wrap gap-1 rounded-md border border-border p-1">
            {siteEditorModes.map(([path, label]) => {
              const active = location.pathname === path
                || path === "/site/pages" && location.pathname.startsWith("/site/pages/")
                || path === "/site/data-model" && location.pathname.startsWith("/site/data-model/");
              return <Link key={path} to={`${path}${siteContextQuery(selected, rootSiteID)}`} aria-current={active ? "page" : undefined} className={`rounded px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${active ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent"}`}>{label}</Link>;
            })}
          </nav>
          <span role="status" aria-live="polite" className={`ml-auto text-xs ${allDirty ? "text-warning" : "text-success"}`}>{allDirty ? "Есть изменения" : "Сохранено"}</span>
          {conflict && <Button type="button" size="sm" variant="outline" disabled={saving} onClick={async () => {
            if (allDirty && !(await confirm({ title: "Загрузить актуальную версию?", description: "Несохранённые изменения локальной рабочей копии будут заменены.", confirmLabel: "Загрузить", cancelLabel: "Остаться", destructive: true }))) return;
            await reload();
          }}>Загрузить актуальную версию</Button>}
          <Button type="button" size="sm" className="ml-auto" disabled={!allDirty || saving || (!snapshot && !rootSiteID)} onClick={() => void save()}>{saving ? "Сохраняем…" : "Сохранить"}</Button>
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
