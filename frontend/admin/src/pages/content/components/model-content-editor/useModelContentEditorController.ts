import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@/api";
import { useAuth } from "@/app/auth";
import { useUnsavedChangesGuard } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { errorMessage } from "@/lib/errors";
import { canPublishContent } from "@/lib/permissions";
import type { DataModelEntity, JsonValue, ModelContentRecord, ModelContentSelection, ModelContentWorkspace } from "@/types";
import type { ModelContentDraft, ModelContentRevisionConflict, ModelContentSaveState } from "../model-content/types";
import { useModelContentEditorModel } from "./useModelContentEditorModel";
import {
  apiModelRecordInput,
  canonicalRecordID,
  clearModelRecordData,
  cleanOrphanedModelContentSelections,
  createModelRecord,
  deleteModelRecord,
  modelDraftHasRevisionConflict,
  orphanedModelRecordData,
  removeOrphanedModelRecordData,
  remapLocalRecordIDs,
  modelRecordTitle,
  modelRecordsEqual,
  modelRecordDeletionImpact,
  modelSelectionsEqual,
  updateModelRecordField,
} from "../../model-content-workspace";

type LocalModelDraft = ModelContentDraft;
type SaveState = ModelContentSaveState;
type RecoveryDraft = LocalModelDraft & { baseRevision: number; savedAt: string };
type RevisionConflict = ModelContentRevisionConflict;

export const modelWorkspaceQueryKey = (snapshotId: string, locale: string) => ["content", "model-workspace", snapshotId, locale] as const;

function sameDraft(left: LocalModelDraft, right: LocalModelDraft): boolean {
  return modelRecordsEqual(left.records, right.records) && modelSelectionsEqual(left.selections, right.selections);
}

function draftStorageKey(snapshotId: string, locale: string): string {
  return `website-engine:model-content-draft:${snapshotId}:${locale}`;
}

function readRecoveryDraft(snapshotId: string, locale: string): RecoveryDraft | null {
  try {
    const raw = sessionStorage.getItem(draftStorageKey(snapshotId, locale));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<RecoveryDraft>;
    if (typeof parsed.baseRevision !== "number" || !Array.isArray(parsed.records) || !Array.isArray(parsed.selections)) return null;
    return { baseRevision: parsed.baseRevision, records: parsed.records as ModelContentRecord[], selections: parsed.selections as ModelContentSelection[], savedAt: typeof parsed.savedAt === "string" ? parsed.savedAt : "" };
  } catch {
    return null;
  }
}

function downloadDraft(snapshotId: string, draft: LocalModelDraft): void {
  const blob = new Blob([JSON.stringify({ snapshotId, ...draft }, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `content-draft-${snapshotId}.json`;
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function useModelContentEditorController({
  workspace,
  requestedEntityKey = "",
  requestedRecordId = "",
}: {
  workspace: ModelContentWorkspace;
  requestedEntityKey?: string;
  requestedRecordId?: string;
}) {
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const principal = useAuth().principal;
  const canPublish = canPublishContent(principal);
  const initialServerDraft = useMemo<LocalModelDraft>(() => ({ records: workspace.records, selections: workspace.selections }), [workspace.records, workspace.selections]);
  const [recoveryDraft] = useState(() => readRecoveryDraft(workspace.snapshot.id, workspace.locale));
  const initialDraft = recoveryDraft ?? initialServerDraft;
  const [draft, setDraft] = useState<LocalModelDraft>(initialDraft);
  const draftRef = useRef<LocalModelDraft>(initialDraft);
  const [savedDraft, setSavedDraft] = useState<LocalModelDraft>(initialServerDraft);
  const [revision, setRevision] = useState(workspace.revision);
  const revisionRef = useRef(workspace.revision);
  const selectionRevisionRef = useRef(workspace.selectionRevision);
  const draftBaseRevisionRef = useRef(recoveryDraft?.baseRevision ?? workspace.revision);
  const recoveredConflict = recoveryDraft ? modelDraftHasRevisionConflict(
    recoveryDraft.baseRevision,
    workspace.revision,
    recoveryDraft,
    initialServerDraft,
  ) : false;
  const [saveState, setSaveState] = useState<SaveState>(recoveredConflict ? "conflict" : "idle");
  const [saveError, setSaveError] = useState(recoveredConflict ? "Найден локальный черновик, сохранённый поверх другой ревизии. Он восстановлен и не отправлен автоматически." : "");
  const [revisionConflict, setRevisionConflict] = useState<RevisionConflict | null>(() => recoveredConflict ? {
    local: recoveryDraft?.baseRevision ?? workspace.revision,
    server: workspace.revision,
  } : null);
  const initialEntityId = workspace.dataModel?.entities.find((entity) => entity.key === requestedEntityKey)?.id
    ?? (requestedEntityKey ? "" : workspace.dataModel?.entities[0]?.id ?? "");
  const [entityId, setEntityId] = useState(initialEntityId);
  const [recordId, setRecordId] = useState(requestedRecordId);
  const [search, setSearch] = useState("");
  const {
    model, entities, mediaEditorProps, selectedEntity, recordsForEntity,
    selectedRecord, validation, invalidCount, orphanedEntityRecords,
    orphanedSelections, visibleRecords,
  } = useModelContentEditorModel(workspace, draft, entityId, recordId, search);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [publishPending, setPublishPending] = useState(false);
  const [deletePending, setDeletePending] = useState(false);
  const saveLock = useRef(false);
  const saveRetry = useRef<{ fingerprint: string; key: string } | null>(null);
  const publishRetry = useRef<string | null>(null);
  const restoreRetry = useRef<{ historyID: string; revision: number; key: string } | null>(null);
  const changeSequence = useRef(0);
  const bypassGuard = useRef(false);
  const dirty = !sameDraft(draft, savedDraft);
  const historyQuery = useInfiniteQuery({
    queryKey: ["content", "model-history", workspace.snapshot.id, workspace.locale],
    queryFn: ({ pageParam, signal }) => api.contentEngine.history(workspace.snapshot.id, workspace.locale, pageParam, undefined, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor,
    enabled: historyOpen,
  });

  useEffect(() => {
    if (requestedEntityKey) return;
    if (!entities.some((entity) => entity.id === entityId)) setEntityId(entities[0]?.id ?? "");
  }, [entities, entityId, requestedEntityKey]);

  useEffect(() => {
    if (!requestedEntityKey) return;
    const requested = entities.find((entity) => entity.key === requestedEntityKey);
    if (!requested) {
      if (entityId) setEntityId("");
      if (recordId) setRecordId("");
      return;
    }
    if (requested.id !== entityId) {
      setEntityId(requested.id);
      setRecordId(requestedRecordId);
      setSearch("");
    }
  }, [entities, entityId, recordId, requestedEntityKey, requestedRecordId]);

  useEffect(() => {
    if (!requestedRecordId || requestedEntityKey === "") return;
    if (recordsForEntity.some((record) => record.id === requestedRecordId) && recordId !== requestedRecordId) {
      setRecordId(requestedRecordId);
    }
  }, [recordId, recordsForEntity, requestedEntityKey, requestedRecordId]);

  useEffect(() => {
    if (selectedEntity?.cardinality !== "singleton") return;
    const soleRecordID = recordsForEntity.length === 1 ? recordsForEntity[0]?.id ?? "" : "";
    if (recordId !== soleRecordID) setRecordId(soleRecordID);
  }, [recordId, recordsForEntity, selectedEntity]);

  useEffect(() => {
    if (dirty) {
      try {
        sessionStorage.setItem(draftStorageKey(workspace.snapshot.id, workspace.locale), JSON.stringify({ ...draft, baseRevision: draftBaseRevisionRef.current, savedAt: new Date().toISOString() } satisfies RecoveryDraft));
      } catch {
        // The editor remains usable when browser storage is unavailable; the UI still warns before navigation.
      }
      return;
    }
    try { sessionStorage.removeItem(draftStorageKey(workspace.snapshot.id, workspace.locale)); } catch { /* storage can be disabled */ }
  }, [dirty, draft, workspace.snapshot.id]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (saveState === "saved") setSaveState("idle");
    }, 2200);
    return () => window.clearTimeout(timer);
  }, [saveState]);

  const updateDraft = useCallback((updater: (current: LocalModelDraft) => LocalModelDraft) => {
    const next = updater(draftRef.current);
    draftRef.current = next;
    changeSequence.current += 1;
    setDraft(next);
    if (saveState !== "conflict") {
      setSaveState("idle");
      setSaveError("");
    }
  }, [saveState]);

  const setRevisionValue = (next: number) => {
    revisionRef.current = next;
    setRevision(next);
  };

  const markRevisionConflict = useCallback((localRevision: number) => {
    const serverRevision = workspace.revision > localRevision ? workspace.revision : null;
    setRevisionConflict({ local: localRevision, server: serverRevision });
    setSaveState("conflict");
    setSaveError("Локальные изменения сохранены в этой вкладке. Скачайте копию перед загрузкой серверной ревизии.");
    void queryClient.invalidateQueries({ queryKey: modelWorkspaceQueryKey(workspace.snapshot.id, workspace.locale) });
  }, [queryClient, workspace.revision, workspace.snapshot.id]);

  const saveDraft = useCallback(async (): Promise<ModelContentWorkspace | null> => {
    if (!dirty || saveLock.current || invalidCount > 0 || saveState === "conflict") return null;
    saveLock.current = true;
    const submitted = draftRef.current;
    const submittedSequence = changeSequence.current;
    const inputs = submitted.records.map(apiModelRecordInput);
    const fingerprint = JSON.stringify({ records: inputs, selections: submitted.selections });
    if (!saveRetry.current || saveRetry.current.fingerprint !== fingerprint) {
      saveRetry.current = { fingerprint, key: `content-save-${crypto.randomUUID()}` };
    }
    const idempotencyKey = saveRetry.current.key;
    setSaveState("saving");
    setSaveError("");
    try {
      const saved = await api.contentEngine.saveWorkspaceDraft(workspace.snapshot.id, workspace.locale, revisionRef.current, selectionRevisionRef.current, inputs, submitted.selections, idempotencyKey);
      saveRetry.current = null;
      revisionRef.current = saved.revision;
      selectionRevisionRef.current = saved.selectionRevision;
      draftBaseRevisionRef.current = saved.revision;
      setRevision(saved.revision);
      const canonical: LocalModelDraft = { records: saved.records, selections: saved.selections };
      setSavedDraft(canonical);
      const currentDraft = remapLocalRecordIDs(draftRef.current, submitted.records, saved.records, saved.clientRecordIDs);
      draftRef.current = currentDraft;
      setDraft(currentDraft);
      const canonicalID = canonicalRecordID(recordId, submitted.records, saved.records, saved.clientRecordIDs);
      if (canonicalID) setRecordId(canonicalID);
      if (changeSequence.current === submittedSequence) {
        setSaveState("saved");
      } else {
        setSaveState("idle");
      }
      setRevisionConflict(null);
      queryClient.setQueryData(modelWorkspaceQueryKey(workspace.snapshot.id, workspace.locale), saved);
      void queryClient.invalidateQueries({ queryKey: ["content", "model-history", workspace.snapshot.id, workspace.locale] });
      return saved;
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        markRevisionConflict(revisionRef.current);
      } else {
        setSaveState("error");
        setSaveError(errorMessage(error));
      }
      return null;
    } finally {
      saveLock.current = false;
    }
  }, [dirty, invalidCount, markRevisionConflict, queryClient, recordId, saveState, workspace.snapshot.id]);

  useEffect(() => {
    if (!dirty || saveState !== "idle" || invalidCount > 0) return;
    const timer = window.setTimeout(() => { void saveDraft(); }, 800);
    return () => window.clearTimeout(timer);
  }, [dirty, draft, invalidCount, saveDraft, saveState]);

  const { dialog: unsavedDialog } = useUnsavedChangesGuard(dirty, bypassGuard);

  useEffect(() => {
    if (workspace.revision <= revisionRef.current) return;
    if (!dirty || !modelDraftHasRevisionConflict(revisionRef.current, workspace.revision, draftRef.current, initialServerDraft)) {
      const current = initialServerDraft;
      draftRef.current = current;
      setDraft(current);
      setSavedDraft(current);
      setRevisionValue(workspace.revision);
      selectionRevisionRef.current = workspace.selectionRevision;
      draftBaseRevisionRef.current = workspace.revision;
      setSaveState("idle");
      setSaveError("");
      setRevisionConflict(null);
      return;
    }
    if (modelDraftHasRevisionConflict(revisionRef.current, workspace.revision, draftRef.current, initialServerDraft)) {
      setSaveState("conflict");
      setRevisionConflict({ local: revisionRef.current, server: workspace.revision });
      setSaveError("На сервере появилась новая ревизия. Локальные изменения оставлены без перезаписи.");
    }
  }, [dirty, initialServerDraft, workspace.revision]);

  const addRecord = (): ModelContentRecord | null => {
    if (!selectedEntity || selectedEntity.cardinality === "singleton") return null;
    const nextOrder = recordsForEntity.reduce((max, item) => Math.max(max, item.sortOrder), -1) + 1;
    const item = createModelRecord(selectedEntity.id, nextOrder, workspace.locale);
    updateDraft((current) => ({ ...current, records: [...current.records, item] }));
    setRecordId(item.id);
    return item;
  };

  const removeRecord = async (record: ModelContentRecord): Promise<boolean> => {
    if (entities.find((entity) => entity.id === record.entityId)?.cardinality === "singleton") return false;
    const title = selectedEntity ? modelRecordTitle(record, selectedEntity) : "эту запись";
    const impact = model ? modelRecordDeletionImpact(draftRef.current.records, draftRef.current.selections, model, record.id, workspace.placements) : null;
    const consequences = impact ? [
      impact.placementSelections > 0 ? `Выборы размещений, из которых будет убрана запись: ${impact.placementSelections}.` : "",
      impact.affectedFeatureInputs > 0 ? `Входы фич, которые используют эту запись и потеряют её: ${impact.affectedFeatureInputs}.` : "",
      impact.mediaReferences > 0 ? `Ссылки записи на файлы в медиатеке: ${impact.mediaReferences}; сами файлы останутся.` : "",
    ].filter(Boolean).join(" ") : "";
    const accepted = await confirm({
      title: "Удалить запись?",
      description: `Будет удалена запись «${title}». ${consequences} Изменения попадут сначала в черновик и станут публичными только после отдельной публикации.`,
      confirmLabel: "Удалить запись",
      destructive: true,
    });
    if (!accepted) return false;
    setDeletePending(true);
    const result = deleteModelRecord(draftRef.current.records, draftRef.current.selections, record.id);
    updateDraft((current) => ({ ...current, ...result }));
    if (recordId === record.id) setRecordId("");
    setDeletePending(false);
    return true;
  };

  const clearSingletonRecord = async (record: ModelContentRecord) => {
    const entity = entities.find((item) => item.id === record.entityId);
    if (entity?.cardinality !== "singleton" || selectedRecord?.id !== record.id) return;
    const accepted = await confirm({
      title: "Очистить данные единственной записи?",
      description: `Все поля записи «${modelRecordTitle(record, entity)}» будут очищены в черновике. ID записи сохранится, файлы в медиатеке не удалятся. Публичные страницы изменятся только после отдельной публикации.`,
      confirmLabel: "Очистить поля",
      destructive: true,
    });
    if (!accepted) return;
    updateDraft((current) => ({ ...current, records: clearModelRecordData(current.records, record.id) }));
  };

  const removeOrphanedEntityRecord = async (record: ModelContentRecord) => {
    const accepted = await confirm({
      title: "Удалить запись удалённой сущности?",
      description: `Запись ${record.id} ссылается на сущность, которой больше нет в модели. Удаление очистит ссылки и выборы размещений в черновике этого снапшота; родительские снапшоты не изменятся.`,
      confirmLabel: "Удалить запись",
      destructive: true,
    });
    if (!accepted) return;
    setDeletePending(true);
    const next = deleteModelRecord(draftRef.current.records, draftRef.current.selections, record.id);
    updateDraft((current) => ({ ...current, ...next }));
    setDeletePending(false);
  };

  const cleanOrphanedRecordData = async (record: ModelContentRecord) => {
    if (!model) return;
    const orphaned = orphanedModelRecordData(record, model);
    const count = orphaned.unknownFieldKeys.length;
    if (count === 0) return;
    const details = orphaned.unknownFieldKeys.map((key) => `поле «${key}»`);
    const accepted = await confirm({
      title: "Удалить данные без определения в модели?",
      description: `Из черновика записи будут удалены: ${details.join(", ")}. Публичные данные изменятся только после отдельной публикации. Родительские снапшоты не меняются.`,
      confirmLabel: "Удалить устаревшие данные",
      destructive: true,
    });
    if (!accepted) return;
    updateDraft((current) => ({ ...current, records: removeOrphanedModelRecordData(current.records, model, record.id) }));
  };

  const changeField = (key: string, value: JsonValue | undefined) => {
    if (!selectedRecord) return;
    updateDraft((current) => ({ ...current, records: updateModelRecordField(current.records, selectedRecord.id, key, value) }));
  };

  const selectEntity = (nextEntityId: string): DataModelEntity | null => {
    setEntityId(nextEntityId);
    const nextEntity = entities.find((entity) => entity.id === nextEntityId);
    const nextRecords = draftRef.current.records.filter((record) => record.entityId === nextEntityId);
    setRecordId(nextEntity?.cardinality === "singleton" && nextRecords.length === 1 ? nextRecords[0]!.id : "");
    setSearch("");
    return nextEntity ?? null;
  };

  const publish = async () => {
    if (!canPublish || invalidCount > 0 || saveState === "conflict") return;
    const accepted = await confirm({
      title: "Опубликовать контент?",
      description: "Публичные страницы начнут использовать записи и выборы размещений этой ревизии. Изменение не создаёт новый снапшот.",
      confirmLabel: "Опубликовать",
    });
    if (!accepted) return;
    setPublishPending(true);
    try {
      const saved = dirty ? await saveDraft() : null;
      if (dirty && !saved) return;
      publishRetry.current ??= `content-publish-${crypto.randomUUID()}`;
      const published = await api.contentEngine.publishWorkspace(workspace.snapshot.id, workspace.locale, saved?.revision ?? revisionRef.current, saved?.selectionRevision ?? selectionRevisionRef.current, publishRetry.current);
      publishRetry.current = null;
      const canonical: LocalModelDraft = { records: published.records, selections: published.selections };
      draftRef.current = canonical;
      setDraft(canonical);
      setSavedDraft(canonical);
      setRevisionValue(published.revision);
      selectionRevisionRef.current = published.selectionRevision;
      draftBaseRevisionRef.current = published.revision;
      setSaveState("saved");
      setSaveError("");
      setRevisionConflict(null);
      queryClient.setQueryData(modelWorkspaceQueryKey(workspace.snapshot.id, workspace.locale), published);
      void queryClient.invalidateQueries({ queryKey: ["content", "model-history", workspace.snapshot.id, workspace.locale] });
      toast.success("Контент опубликован");
    } catch (error) {
      setSaveState(error instanceof ApiError && error.status === 409 ? "conflict" : "error");
      if (error instanceof ApiError && error.status === 409) markRevisionConflict(revisionRef.current);
      else setSaveError(errorMessage(error));
    } finally {
      setPublishPending(false);
    }
  };

  const restorePublication = async (publicationId: string) => {
    const accepted = await confirm({
      title: "Восстановить публикацию?",
      description: `${dirty ? "Текущие локальные изменения будут заменены. " : ""}Выбранная публикация станет новым черновиком. Публичные страницы изменятся только после отдельной публикации.`,
      confirmLabel: "Создать черновик",
    });
    if (!accepted) return;
    try {
      if (!restoreRetry.current || restoreRetry.current.historyID !== publicationId || restoreRetry.current.revision !== revisionRef.current) {
        restoreRetry.current = { historyID: publicationId, revision: revisionRef.current, key: `content-restore-${crypto.randomUUID()}` };
      }
      const restored = await api.contentEngine.restoreHistory(workspace.snapshot.id, workspace.locale, publicationId, revisionRef.current, selectionRevisionRef.current, restoreRetry.current.key);
      restoreRetry.current = null;
      const canonical: LocalModelDraft = { records: restored.records, selections: restored.selections };
      draftRef.current = canonical;
      setDraft(canonical);
      setSavedDraft(canonical);
      setRevisionValue(restored.revision);
      selectionRevisionRef.current = restored.selectionRevision;
      draftBaseRevisionRef.current = restored.revision;
      setSaveState("saved");
      setSaveError("");
      setRevisionConflict(null);
      queryClient.setQueryData(modelWorkspaceQueryKey(workspace.snapshot.id, workspace.locale), restored);
      void queryClient.invalidateQueries({ queryKey: ["content", "model-history", workspace.snapshot.id, workspace.locale] });
      setHistoryOpen(false);
      toast.success("Создан черновик из публикации");
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) markRevisionConflict(revisionRef.current);
      else {
        setSaveState("error");
        setSaveError(errorMessage(error));
      }
    }
  };

  const reloadServerDraft = async () => {
    const accepted = await confirm({
      title: "Загрузить сохранённую ревизию?",
      description: "Локальные изменения будут заменены серверным черновиком. Перед продолжением скачайте локальную копию, если хотите её сохранить.",
      confirmLabel: "Загрузить серверную версию",
      destructive: true,
    });
    if (!accepted) return;
    try {
      const latest = await api.contentEngine.workspace(workspace.snapshot.id, workspace.locale);
      const canonical: LocalModelDraft = { records: latest.records, selections: latest.selections };
      draftRef.current = canonical;
      setDraft(canonical);
      setSavedDraft(canonical);
      setRevisionValue(latest.revision);
      draftBaseRevisionRef.current = latest.revision;
      setSaveState("idle");
      setSaveError("");
      setRevisionConflict(null);
      queryClient.setQueryData(modelWorkspaceQueryKey(workspace.snapshot.id, workspace.locale), latest);
    } catch (error) {
      setSaveError(errorMessage(error));
    }
  };

  const cleanOrphanedSelections = async () => {
    const accepted = await confirm({
      title: "Очистить выборы удалённых размещений?",
      description: `Устаревших выборов для удаления: ${orphanedSelections.length}. Для них больше нет размещения или входа данных; при дублях сохранится первый выбор. Публичные страницы изменятся только после отдельной публикации.`,
      confirmLabel: "Очистить выборы",
      destructive: true,
    });
    if (!accepted) return;
    updateDraft((current) => ({
      ...current,
      selections: cleanOrphanedModelContentSelections(current.selections, workspace.placements),
    }));
  };

  return {
    workspace, canPublish, model, entities, mediaEditorProps,
    draft, savedDraft, revision, revisionConflict, saveState, saveError, dirty,
    invalidCount, publishPending, deletePending, historyOpen, setHistoryOpen,
    entityId, recordId, setRecordId, search, setSearch,
    selectEntity,
    selectedEntity, selectedRecord, recordsForEntity, visibleRecords,
    orphanedEntityRecords, orphanedSelections, validation,
    unsavedDialog, historyQuery, updateDraft, confirm,
    saveDraft, publish, restorePublication, reloadServerDraft,
    addRecord, removeRecord, clearSingletonRecord, removeOrphanedEntityRecord,
    cleanOrphanedRecordData, changeField, cleanOrphanedSelections,
    downloadLocalDraft: () => downloadDraft(workspace.snapshot.id, draftRef.current),
  };
}
