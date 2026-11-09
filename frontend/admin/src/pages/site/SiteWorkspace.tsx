import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, Outlet, useLocation, useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, isApiError } from "@/api";
import { useAuth } from "@/app/auth";
import { ErrorState, useDocumentTitle, useUnsavedChangesGuard } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import type { SiteConfig, SiteMetadata, SiteSnapshot } from "@/types";
import { formatSnapshotDate, snapshotCreationLines, snapshotCreatorLabel } from "@/domains/snapshots/provenance";
import { hasCompetingSiteDraftChange, parseSiteDraft, siteDraftKey } from "./workspace-draft";
import type { StructuredDesignSystem } from "@/domains/design-system/types";
import { featureVersionChanges, normalizeFeatureComposition } from "./feature-selection";
import { ExternalBackendSettings } from "./components/workspace/ExternalBackendSettings";

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
  pageEditorOpen: boolean;
  setPageEditorOpen: (open: boolean) => void;
  update: (next: SiteConfig | ((current: SiteConfig) => SiteConfig)) => void;
  updateMetadata: (next: SiteMetadata | ((current: SiteMetadata) => SiteMetadata)) => void;
  updateDesignSystem: (next: StructuredDesignSystem | ((current: StructuredDesignSystem) => StructuredDesignSystem)) => void;
  save: (name?: string) => Promise<boolean>;
  reload: () => Promise<void>;
};

const WorkspaceContext = createContext<Workspace | null>(null);
const emptyDesignSystem: StructuredDesignSystem = {formatVersion: 2, groups: [], baseTokens: [], featureValues: {}, breakpoints: [], fonts: []};

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
  const confirm = useConfirm();
  const principal = useAuth().principal;
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const selected = params.get("snapshot") ?? "";
  useDocumentTitle("Снапшоты");

  const snapshots = useQuery({ queryKey: ["site", "snapshot-choices"], queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal) });
  const waitingForSelection = !selected && (snapshots.isLoading || snapshots.isError || Boolean(snapshots.data?.items.length));
  const [config, setConfig] = useState<SiteConfig | null>(null);
  const [savedConfig, setSavedConfig] = useState<SiteConfig | null>(null);
  const [metadata, setMetadata] = useState<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const [savedMetadata, setSavedMetadata] = useState<SiteMetadata>({formatVersion: 1, siteName: "Website", pages: {}});
  const [designSystem, setDesignSystem] = useState<StructuredDesignSystem>(emptyDesignSystem);
  const [savedDesignSystem, setSavedDesignSystem] = useState<StructuredDesignSystem>(emptyDesignSystem);
  const [snapshot, setSnapshot] = useState<SiteSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [localSaveError, setLocalSaveError] = useState(false);
  const [pageEditorOpen, setPageEditorOpen] = useState(false);
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
    if (!selected && snapshots.data?.items.length) {
      setParams((current) => { const next = new URLSearchParams(current); next.set("snapshot", snapshots.data.items[0]!.id); return next; }, { replace: true });
    }
  }, [selected, setParams, snapshots.data]);

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

  const dirty = useMemo(() => config !== null && savedConfig !== null && JSON.stringify(config) !== JSON.stringify(savedConfig), [config, savedConfig]);
  const allDirty = dirty || JSON.stringify(metadata) !== JSON.stringify(savedMetadata) || JSON.stringify(designSystem) !== JSON.stringify(savedDesignSystem);
  const guard = useUnsavedChangesGuard(allDirty, guardBypass, {
    allowPathChange: (current, next) => {
      const workspaceModes = ["/site/pages", "/site/settings", "/site/design-system", "/site/data-model"];
      return workspaceModes.includes(current) && workspaceModes.includes(next);
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
	  let parentSnapshotRequest: { parentSnapshotId: string; expectedParentContentRevision: number } | undefined;
	  if (snapshot) {
	    const parentDetail = await api.siteBuilder.snapshot(snapshot.id);
	    parentSnapshotRequest = { parentSnapshotId: snapshot.id, expectedParentContentRevision: parentDetail.snapshot.contentRevision ?? snapshot.contentRevision ?? 0 };
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
      const created = await api.siteBuilder.createSnapshotFrom({
        name: name?.trim() || (snapshot ? `${snapshot.name} · изменение` : "Новый сайт"),
        ...(parentSnapshotRequest ?? {}),
        config: submitted,
        metadata: submittedMetadata,
        designSystem: submittedDesignSystem,
      });
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
      setParams((current) => { const next = new URLSearchParams(current); next.set("snapshot", created.id); return next; }, { replace: true });
      void queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] });
      void queryClient.invalidateQueries({ queryKey: ["content"] });
      toast.success(`Создан снапшот #${created.sequence}`);
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
  }, [saving, snapshot, setParams, queryClient, confirm, principal, selected, persistLocal]);

  if (snapshots.error) return <ErrorState error={snapshots.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshots.refetch()} />;
  if (loading || snapshots.isLoading || (!selected && Boolean(snapshots.data?.items.length))) return <p className="p-8 text-sm text-muted-foreground">Загружаем снапшот…</p>;
  if (error || !config) return <ErrorState error={error} title="Не удалось загрузить снапшот" onRetry={() => void reload()} />;

  const canvas = location.pathname === "/site/pages" || location.pathname === "/site/router" || location.pathname === "/site/data-model";
  return (
    <WorkspaceContext.Provider value={{ config, metadata, designSystem, revision: snapshot?.sequence ?? 0, snapshot, dirty: allDirty, saving, conflict, localSaveError, pageEditorOpen, setPageEditorOpen, update, updateMetadata, updateDesignSystem, save, reload }}>
      {guard.dialog}
      <div className={canvas ? "flex h-full min-h-0 flex-col" : "min-w-0"}>
        {!pageEditorOpen && <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2">
          <span className="text-xs text-muted-foreground">Снапшот</span>
          <Select value={selected || "new"} disabled={saving} onValueChange={async (value) => {
            if (allDirty && !(await confirm({ title: "Отбросить изменения?", description: "Несохранённые изменения редактора будут потеряны.", confirmLabel: "Отбросить", destructive: true }))) return;
            if (allDirty && principal) { try { const key = siteDraftKey(principal.id, selected); if (localDraftConflict.current || localStorage.getItem(key) !== lastLocalDraft.current) throw new Error("Local draft changed in another tab"); localStorage.removeItem(key); lastLocalDraft.current = null; } catch { toast.error("Локальная копия изменилась или недоступна. Сохраните правки перед переключением."); return; } }
            guardBypass.current = true;
            setParams((current) => { const next = new URLSearchParams(current); if (value === "new") next.delete("snapshot"); else next.set("snapshot", value); return next; });
          }}>
            <SelectTrigger className="w-full sm:w-80" aria-label="Рабочий снапшот"><SelectValue /></SelectTrigger>
            <SelectContent>
              {!snapshots.data?.items.length && <SelectItem value="new">Первый снапшот</SelectItem>}
              {snapshots.data?.items.map((item) => <SelectItem key={item.id} value={item.id}>#{item.sequence} · {item.name}</SelectItem>)}
            </SelectContent>
          </Select>
          <nav aria-label="Режим редактора сайта" className="flex flex-wrap gap-1 rounded-md border border-border p-1">
            {[["/site/pages", "Канвас"], ["/site/settings", "Настройки сайта"], ["/site/design-system", "Дизайн-система"], ["/site/data-model", "Модель данных"]].map(([path, label]) => <Link key={path} to={`${path}${selected ? `?snapshot=${encodeURIComponent(selected)}` : ""}`} aria-current={location.pathname === path ? "page" : undefined} className={`rounded px-3 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${location.pathname === path ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-accent"}`}>{label}</Link>)}
          </nav>
          <Button type="button" size="sm" className="ml-auto" disabled={!allDirty || saving} onClick={() => void save()}>{saving ? "Сохраняем…" : allDirty ? "Сохранить" : "Сохранено"}</Button>
          {localSaveError && <span role="alert" className="text-xs text-destructive">Локальное автосохранение недоступно или копия изменена в другой вкладке. Сохраните изменения в снапшот.</span>}
        </div>}
        {snapshot && <section aria-label="Сведения о снапшоте" className="mx-4 mt-3 rounded-lg border border-border bg-card px-3 py-2 text-xs text-muted-foreground">
          <p>Создан {formatSnapshotDate(snapshot.createdAt)} · Инициатор: {snapshotCreatorLabel(snapshot)} · {snapshot.parentSnapshotId
            ? snapshot.parentSequence && snapshot.parentName ? `Родитель: #${snapshot.parentSequence} · ${snapshot.parentName}` : "Есть родительский снапшот"
            : "Корневой снапшот"}</p>
          {snapshot.creationSummary && <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
            {snapshotCreationLines(snapshot.creationSummary).map((line, index) => <li key={`${index}-${line}`}>{line}</li>)}
          </ul>}
        </section>}
        {snapshot?.warnings && snapshot.warnings.length > 0 && (
          <section role="alert" className="mx-4 mt-3 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
            <p className="font-medium">Проверьте совместимость содержимого</p>
            <ul className="mt-1 list-disc pl-5">{snapshot.warnings.map((warning, index) => <li key={`${index}-${warning}`}>{warning}</li>)}</ul>
          </section>
        )}
        <div className={canvas ? "min-h-0 flex-1" : ""}>{children ?? <Outlet />}</div>
        {location.pathname === "/site/settings" && <ExternalBackendSettings
          config={config}
          onBackendsChange={(backends) => update((current) => ({ ...current, backends }))}
          onConstantsChange={(publicConstants) => update((current) => ({ ...current, publicConstants }))}
        />}
      </div>
    </WorkspaceContext.Provider>
  );
}
