import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Check, CircleAlert, Copy, FileUp, Plus, Search, Trash2 } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api, ApiError } from "@/api";
import { useAuth } from "@/app/auth";
import { ErrorState, LoadingPage, useDocumentTitle, useUnsavedChangesGuard } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { FeatureTypeBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { EntryFieldsForm, fieldLabelForPath, scrollToEntryField, validateEntryFields } from "@/domains/content-editor/EntryForm";
import { starterMetadata } from "@/domains/content-editor/blocks";
import type { EntryFormIssue } from "@/domains/content-editor/EntryForm";
import { parsedSchema } from "@/domains/content-schema/schema";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { errorMessage } from "@/lib/errors";
import { deriveEntryTitle } from "@/lib/json";
import { queryKeys } from "@/lib/query-keys";
import { isRecord } from "@/lib/utils";
import type { ContentFeature, JsonObject, JsonValue, SiteSnapshot, SnapshotContentDraftInput, SnapshotContentDraftItem } from "@/types";
import { serializeSnapshotContentItems, unsupportedSchemaKeywords } from "./content-workspace";

type SaveStatus = "idle" | "saving" | "saved" | "error" | "conflict" | "invalid";

/** Validation issue attributed to the draft record it belongs to. */
type DraftIssue = EntryFormIssue & { recordId: string; itemTitle: string; itemFields: SchemaFieldNode[] };

const UNGROUPED = "Без категории";

function categoryFor(feature: ContentFeature): string {
  return feature.category?.trim() || UNGROUPED;
}

function modeLabel(mode: ContentFeature["mode"]): string {
  if (mode === "single") return "Один набор содержимого";
  if (mode === "form") return "Тексты формы";
  return "Записи каталога";
}

function newItem(fields: SchemaFieldNode[], sortOrder: number, singleSlot: boolean): SnapshotContentDraftItem {
  return {
    entryId: crypto.randomUUID(),
    sortOrder,
    singleSlot,
    data: starterMetadata(fields, null),
  };
}

function cloneData(data: JsonObject): JsonObject {
  return JSON.parse(JSON.stringify(data)) as JsonObject;
}

function displaySnapshot(snapshot: SiteSnapshot): string {
  return `#${snapshot.sequence} · ${snapshot.name}`;
}

export default function ContentHomePage() {
  useDocumentTitle("Редактор контента");
  const principal = useAuth().principal;
  const canCreateSnapshot = principal?.role === "admin" || principal?.role === "moderator";
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [searchParams, setSearchParams] = useSearchParams();
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("Все категории");
  const [localItems, setLocalItems] = useState<SnapshotContentDraftItem[]>([]);
  const localItemsRef = useRef<SnapshotContentDraftItem[]>([]);
  const [localIdentity, setLocalIdentity] = useState("");
  const [revision, setRevision] = useState(0);
  const revisionRef = useRef(0);
  const syncRevision = useCallback((value: number) => {
    revisionRef.current = value;
    setRevision(value);
  }, []);
  const [dirty, setDirty] = useState(false);
  const [saveStatus, setSaveStatus] = useState<SaveStatus>("idle");
  const [saveError, setSaveError] = useState("");
  const [importing, setImporting] = useState(false);
  const importRef = useRef<HTMLInputElement>(null);
  const saveLock = useRef(false);
  const missingSeedIdentity = useRef("");
  const savedStatusTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const snapshotQuery = useQuery({
    queryKey: queryKeys.content.snapshots,
    queryFn: ({ signal }) => api.siteBuilder.contentSnapshots(signal),
  });
  const snapshots = useMemo(() => snapshotQuery.data?.items ?? [], [snapshotQuery.data?.items]);
  const snapshotId = searchParams.get("snapshot") ?? "";
  const selectedSnapshot = snapshots.find((item) => item.id === snapshotId) ?? null;
  const featureKey = searchParams.get("feature") ?? "";
  const recordId = searchParams.get("record") ?? "";
  const editingSettings = searchParams.get("settings") === "1";

  useEffect(() => {
    if (snapshotQuery.isLoading || snapshots.length === 0 || snapshots.some((item) => item.id === snapshotId)) return;
    const next = new URLSearchParams(searchParams);
    next.set("snapshot", snapshots[0]!.id);
    setSearchParams(next, { replace: true });
  }, [snapshotId, snapshotQuery.isLoading, snapshots, searchParams, setSearchParams]);

  const featuresQuery = useQuery({
    queryKey: queryKeys.content.snapshotFeatures(snapshotId),
    queryFn: ({ signal }) => api.siteBuilder.snapshotContentFeatures(snapshotId, signal),
    enabled: Boolean(snapshotId),
  });
  const features = useMemo(() => featuresQuery.data?.items ?? [], [featuresQuery.data?.items]);
  const selectedFeature = features.find((item) => item.key === featureKey) ?? null;
  const unsupported = useMemo(() => [...new Set([
    ...unsupportedSchemaKeywords(selectedFeature?.schema),
    ...unsupportedSchemaKeywords(selectedFeature?.settingsSchema),
  ])], [selectedFeature?.schema, selectedFeature?.settingsSchema]);
  const workspaceIdentity = snapshotId && featureKey ? `${snapshotId}:${featureKey}` : "";
  const workspaceQuery = useQuery({
    queryKey: queryKeys.content.snapshotWorkspace(snapshotId, featureKey),
    queryFn: ({ signal }) => api.siteBuilder.snapshotContentWorkspace(snapshotId, featureKey, signal),
    enabled: Boolean(workspaceIdentity && selectedFeature),
  });

  const collectionFields = useMemo(
    () => parsedSchema(selectedFeature?.schema).fields,
    [selectedFeature?.schema],
  );
  const settingsFields = useMemo(
    () => parsedSchema(selectedFeature?.settingsSchema).fields,
    [selectedFeature?.settingsSchema],
  );
  const activeFields = editingSettings && selectedFeature?.settingsSchema ? settingsFields : collectionFields;
  const activeItem = useMemo(() => {
    if (!selectedFeature) return null;
    if (selectedFeature.mode !== "collection") return localItems.find((item) => item.singleSlot) ?? localItems[0] ?? null;
    if (editingSettings) return localItems.find((item) => item.singleSlot) ?? null;
    return recordId ? localItems.find((item) => item.entryId === recordId && !item.singleSlot) ?? null : null;
  }, [editingSettings, localItems, recordId, selectedFeature]);
  const activeIssues = useMemo<EntryFormIssue[]>(
    () => activeItem ? validateEntryFields(activeFields, activeItem.data) : [],
    [activeFields, activeItem],
  );
  const allIssues = useMemo(() => localItems.flatMap((item) => {
    const fields = item.singleSlot && selectedFeature?.settingsSchema ? settingsFields : collectionFields;
    const itemTitle = item.singleSlot ? "Настройки списка" : (deriveEntryTitle(item.data, fields) || "Без названия");
    return validateEntryFields(fields, item.data).map((issue) => ({ ...issue, recordId: item.entryId, itemTitle, itemFields: fields }));
  }), [collectionFields, localItems, selectedFeature?.settingsSchema, settingsFields]);
  const hasDraft = features.some((item) => (item.draftRevision ?? 0) > 0) || revision > 0;

  const setParams = useCallback((patch: Record<string, string | null>) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      Object.entries(patch).forEach(([key, value]) => {
        if (value === null || value === "") next.delete(key);
        else next.set(key, value);
      });
      return next;
    });
  }, [setSearchParams]);

  const saveMutation = useMutation({
    networkMode: "always",
    mutationFn: (variables: { snapshotId: string; key: string; revision: number; items: SnapshotContentDraftInput[] }) =>
      api.siteBuilder.saveSnapshotContentWorkspace(variables.snapshotId, variables.key, variables.revision, variables.items),
  });

  const saveWorkspace = useCallback(async (sourceItems = localItemsRef.current): Promise<boolean> => {
    if (!snapshotId || !selectedFeature || !workspaceIdentity || saveLock.current || unsupported.length > 0) return false;
    if (allIssues.length > 0) {
      setSaveStatus("invalid");
      setSaveError("Заполните обязательные поля и исправьте ошибки перед сохранением.");
      return false;
    }
    saveLock.current = true;
    setSaveStatus("saving");
    setSaveError("");
    try {
      const saved = await saveMutation.mutateAsync({
        snapshotId,
        key: selectedFeature.key,
        revision: revisionRef.current,
        items: serializeSnapshotContentItems(sourceItems),
      });
      // A response only describes the submitted revision. Never replace edits
      // made while that request was in flight with its older payload.
      const changedDuringSave = localItemsRef.current !== sourceItems;
      if (!changedDuringSave) {
        localItemsRef.current = saved.items;
        setLocalItems(saved.items);
      }
      queryClient.setQueryData(queryKeys.content.snapshotWorkspace(snapshotId, selectedFeature.key), saved);
      syncRevision(saved.revision);
      setDirty(changedDuringSave);
      setSaveStatus(changedDuringSave ? "idle" : "saved");
      setSaveError("");
      void queryClient.invalidateQueries({ queryKey: queryKeys.content.snapshotFeatures(snapshotId) });
      if (savedStatusTimer.current) clearTimeout(savedStatusTimer.current);
      if (!changedDuringSave) savedStatusTimer.current = setTimeout(() => setSaveStatus("idle"), 2200);
      return !changedDuringSave;
    } catch (error) {
      setSaveStatus(error instanceof ApiError && error.status === 409 ? "conflict" : "error");
      setSaveError(errorMessage(error));
      return false;
    } finally {
      saveLock.current = false;
    }
  }, [allIssues.length, queryClient, saveMutation, selectedFeature, snapshotId, syncRevision, unsupported.length, workspaceIdentity]);

  useEffect(() => {
    const workspace = workspaceQuery.data;
    if (!workspace || workspaceIdentity === "") return;
    if (localIdentity !== workspaceIdentity) {
      missingSeedIdentity.current = "";
      localItemsRef.current = workspace.items;
      setLocalItems(workspace.items);
      syncRevision(workspace.revision);
      setDirty(false);
      setSaveStatus("idle");
      setSaveError("");
      setLocalIdentity(workspaceIdentity);
    }
  }, [localIdentity, syncRevision, workspaceIdentity, workspaceQuery.data]);

  useEffect(() => {
    if (!selectedFeature || unsupported.length > 0 || selectedFeature.mode === "collection" || !workspaceQuery.data || localIdentity !== workspaceIdentity || localItems.length > 0 || missingSeedIdentity.current === workspaceIdentity) return;
    const item = newItem(collectionFields, 0, true);
    missingSeedIdentity.current = workspaceIdentity;
    localItemsRef.current = [item];
    setLocalItems([item]);
    setDirty(true);
  }, [collectionFields, localIdentity, localItems.length, selectedFeature, unsupported.length, workspaceIdentity, workspaceQuery.data]);

  useEffect(() => {
    if (!dirty || saveStatus === "saving" || saveStatus === "error" || saveStatus === "conflict" || saveStatus === "invalid" || allIssues.length > 0) return;
    const timer = setTimeout(() => { void saveWorkspace(); }, 800);
    return () => clearTimeout(timer);
  }, [allIssues.length, dirty, localItems, revision, saveStatus, saveWorkspace]);

  const guardBypass = useRef(false);
  const { dialog: unsavedDialog } = useUnsavedChangesGuard(dirty, guardBypass);

  useEffect(() => () => {
    if (savedStatusTimer.current) clearTimeout(savedStatusTimer.current);
  }, []);

  const updateItems = (updater: (current: SnapshotContentDraftItem[]) => SnapshotContentDraftItem[]) => {
    // Update the authoritative value synchronously: React may defer state
    // updaters until after an in-flight save has already resolved.
    const next = updater(localItemsRef.current);
    localItemsRef.current = next;
    setLocalItems(next);
    setDirty(true);
    if (saveStatus === "error" || saveStatus === "invalid" || saveStatus === "saved") {
      setSaveStatus("idle");
      setSaveError("");
    }
  };

  const switchSnapshot = async (nextId: string) => {
    if (nextId === snapshotId) return;
    if (dirty) {
      if (allIssues.length === 0) {
        const saved = await saveWorkspace();
        if (!saved) return;
      } else {
        const discard = await confirm({
          title: "Отбросить несохранённые изменения?",
          description: "В текущем черновике есть ошибки проверки. При смене снапшота эти локальные изменения будут потеряны.",
          confirmLabel: "Отбросить изменения",
          destructive: true,
        });
        if (!discard) return;
      }
    }
    setParams({ snapshot: nextId, feature: null, record: null, settings: null });
    setLocalIdentity("");
    missingSeedIdentity.current = "";
    localItemsRef.current = [];
    setLocalItems([]);
    syncRevision(0);
    setDirty(false);
    setSaveStatus("idle");
    setSaveError("");
  };

  const leaveFeature = async () => {
    if (dirty) {
      if (allIssues.length === 0) {
        if (!(await saveWorkspace())) return;
      } else {
        const discard = await confirm({
          title: "Выйти без сохранения?",
          description: "Изменения не прошли проверку схемы и не будут сохранены.",
          confirmLabel: "Выйти и отбросить",
          destructive: true,
        });
        if (!discard) return;
      }
    }
    setParams({ feature: null, record: null, settings: null });
    setLocalIdentity("");
    missingSeedIdentity.current = "";
    setDirty(false);
    setSaveStatus("idle");
    setSaveError("");
  };

  const reloadWorkspace = async () => {
    const discard = await confirm({
      title: "Загрузить сохранённую версию?",
      description: "Локальные изменения будут заменены последней сохранённой редакцией черновика.",
      confirmLabel: "Перезагрузить",
      destructive: true,
    });
    if (!discard) return;
    const result = await workspaceQuery.refetch();
    if (!result.data) return;
    localItemsRef.current = result.data.items;
    setLocalItems(result.data.items);
    syncRevision(result.data.revision);
    setDirty(false);
    setSaveStatus("idle");
    setSaveError("");
  };

  /** Keep local edits and save them over the newest server revision. */
  const overrideConflict = async () => {
    const accepted = await confirm({
      title: "Сохранить поверх последний вариант?",
      description: "Ваши текущие правки заменят изменения, которые кто-то сохранил в черновик после вас.",
      confirmLabel: "Перезаписать черновик",
      destructive: true,
    });
    if (!accepted) return;
    const result = await workspaceQuery.refetch();
    if (!result.data) return;
    syncRevision(result.data.revision);
    setSaveStatus("idle");
    const saved = await saveWorkspace(localItemsRef.current);
    if (saved) toast.success("Правки перезаписали сохранённую версию");
  };

  const addCollectionRecord = (singleSlot = false) => {
    const fields = singleSlot ? settingsFields : collectionFields;
    const nextOrder = localItems.filter((item) => !item.singleSlot).length;
    const item = newItem(fields, nextOrder, singleSlot);
    updateItems((current) => [...current, item]);
    setParams({ record: singleSlot ? null : item.entryId, settings: singleSlot ? "1" : null });
  };

  const changeActiveData = (key: string, value: JsonValue) => {
    if (!activeItem) return;
    updateItems((current) => current.map((item) => item.entryId === activeItem.entryId
      ? { ...item, data: { ...item.data, [key]: value } }
      : item));
  };

  /** Navigate to a draft record and scroll its field into view. */
  const jumpToIssue = (issue: DraftIssue) => {
    if (!selectedFeature) return;
    if (selectedFeature.mode === "collection") {
      const targetItem = localItems.find((item) => item.entryId === issue.recordId) ?? null;
      const settings = targetItem?.singleSlot ?? false;
      setParams({ record: targetItem && !settings ? targetItem.entryId : null, settings: settings ? "1" : null });
    }
    // Wait for the record form to mount before revealing the field.
    window.setTimeout(() => scrollToEntryField(issue.path), 80);
  };

  /** Insert a copy of a record right after it and open the copy. */
  const duplicateRecord = async (item: SnapshotContentDraftItem) => {
    const copy: SnapshotContentDraftItem = { ...item, entryId: crypto.randomUUID(), data: cloneData(item.data) };
    updateItems((current) => {
      const regular = current.filter((candidate) => !candidate.singleSlot).sort((a, b) => a.sortOrder - b.sortOrder);
      const index = regular.findIndex((candidate) => candidate.entryId === item.entryId);
      if (index < 0) return [...current, copy];
      regular.splice(index + 1, 0, copy);
      const orders = new Map(regular.map((candidate, i) => [candidate.entryId, i]));
      return current.map((candidate) => candidate.singleSlot
        ? candidate
        : { ...candidate, sortOrder: orders.get(candidate.entryId) ?? candidate.sortOrder });
    });
    setParams({ record: copy.entryId, settings: null });
    toast.success("Запись продублирована");
  };

  const createSnapshot = async () => {
    if (dirty && !(await saveWorkspace())) return;
    try {
      const created = await api.siteBuilder.forkSnapshotFromContent(snapshotId);
      void queryClient.invalidateQueries({ queryKey: queryKeys.content.snapshots });
      void queryClient.invalidateQueries({ queryKey: queryKeys.content.snapshotFeatures(snapshotId) });
      setParams({ snapshot: created.id, feature: null, record: null, settings: null });
      setLocalIdentity("");
      missingSeedIdentity.current = "";
      setLocalItems([]);
      localItemsRef.current = [];
      syncRevision(0);
      setDirty(false);
      setSaveStatus("idle");
      setSaveError("");
      toast.success(`Создан снапшот #${created.sequence}`);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };

  const moveRecord = (entryId: string, direction: -1 | 1) => {
    updateItems((current) => {
      const regular = current.filter((item) => !item.singleSlot).sort((a, b) => a.sortOrder - b.sortOrder);
      const index = regular.findIndex((item) => item.entryId === entryId);
      const nextIndex = index + direction;
      if (index < 0 || nextIndex < 0 || nextIndex >= regular.length) return current;
      [regular[index], regular[nextIndex]] = [regular[nextIndex]!, regular[index]!];
      const orders = new Map(regular.map((item, i) => [item.entryId, i]));
      return current.map((item) => item.singleSlot ? item : { ...item, sortOrder: orders.get(item.entryId) ?? item.sortOrder });
    });
  };

  const deleteRecord = async (item: SnapshotContentDraftItem) => {
    const accepted = await confirm({
      title: "Удалить запись из редакции?",
      description: "Запись останется в исходном снапшоте. Она исчезнет только из нового снапшота, созданного из этого черновика.",
      confirmLabel: "Удалить",
      destructive: true,
    });
    if (!accepted) return;
    updateItems((current) => {
      const regular = current.filter((candidate) => !candidate.singleSlot && candidate.entryId !== item.entryId)
        .sort((a, b) => a.sortOrder - b.sortOrder)
        .map((candidate, index) => ({ ...candidate, sortOrder: index }));
      const settings = current.filter((candidate) => candidate.singleSlot && candidate.entryId !== item.entryId);
      return [...regular, ...settings];
    });
  };

  const importJSON = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file || !selectedFeature) return;
    setImporting(true);
    try {
      const parsed: unknown = JSON.parse(await file.text());
      const values = Array.isArray(parsed) ? parsed : [parsed];
      if (values.length === 0 || values.some((value) => !isRecord(value))) throw new Error("Ожидается объект или массив объектов.");
      if (Array.isArray(parsed) && selectedFeature.mode !== "collection") throw new Error("Массив можно импортировать только в каталог.");
      const importingSettings = editingSettings;
      const fields = importingSettings && selectedFeature.settingsSchema ? settingsFields : collectionFields;
      const invalidIndex = values.findIndex((value) => validateEntryFields(fields, value as JsonObject).length > 0);
      if (invalidIndex >= 0) throw new Error(`Запись ${invalidIndex + 1} не соответствует схеме фичи.`);
      // Replacing the active record discards its current field values.
      const replacesActive = Boolean(activeItem) && !(Array.isArray(parsed) && selectedFeature.mode === "collection");
      if (replacesActive) {
        const accepted = await confirm({
          title: "Заменить данные записи?",
          description: "Текущие значения полей записи будут заменены данными из JSON-файла. Отменить замену после сохранения будет нельзя.",
          confirmLabel: "Заменить данные",
          destructive: true,
        });
        if (!accepted) return;
      }
      if (selectedFeature.mode === "collection" && Array.isArray(parsed)) {
        updateItems((current) => [
          ...current,
          ...values.map((value, index) => ({
            entryId: crypto.randomUUID(), sortOrder: current.filter((item) => !item.singleSlot).length + index,
            data: value as JsonObject,
          })),
        ]);
        setParams({ record: null, settings: null });
      } else if (activeItem) {
        const importedData = values[0] as JsonObject;
        updateItems((current) => current.map((item) => item.entryId === activeItem.entryId ? { ...item, data: importedData } : item));
      } else if (selectedFeature.mode === "collection") {
        const importedData = values[0] as JsonObject;
        const item: SnapshotContentDraftItem = { entryId: crypto.randomUUID(), sortOrder: localItems.filter((candidate) => !candidate.singleSlot).length, data: importedData };
        updateItems((current) => [...current, item]);
        setParams({ record: item.entryId, settings: null });
      } else {
        throw new Error("Не удалось определить запись для импорта.");
      }
      toast.success("Данные JSON добавлены в черновик");
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setImporting(false);
    }
  };

  const groupedFeatures = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("ru");
    const filtered = features.filter((item) => {
      const matchesText = !needle || `${item.name} ${item.key} ${item.category ?? ""}`.toLocaleLowerCase("ru").includes(needle);
      return matchesText && (category === "Все категории" || categoryFor(item) === category);
    });
    const groups = new Map<string, ContentFeature[]>();
    for (const item of filtered) groups.set(categoryFor(item), [...(groups.get(categoryFor(item)) ?? []), item]);
    return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b, "ru"));
  }, [category, features, search]);
  const categories = useMemo(() => ["Все категории", ...Array.from(new Set(features.map(categoryFor))).sort((a, b) => a.localeCompare(b, "ru"))], [features]);
  const regularItems = useMemo(() => localItems.filter((item) => !item.singleSlot).sort((a, b) => a.sortOrder - b.sortOrder), [localItems]);
  const [recordSearch, setRecordSearch] = useState("");
  const visibleRecords = useMemo(() => {
    const needle = recordSearch.trim().toLocaleLowerCase("ru");
    if (!needle) return regularItems;
    return regularItems.filter((item) => `${deriveEntryTitle(item.data, collectionFields)} ${item.entryId}`.toLocaleLowerCase("ru").includes(needle));
  }, [collectionFields, recordSearch, regularItems]);
  const issuesByRecord = useMemo(() => {
    const counts = new Map<string, number>();
    allIssues.forEach((issue) => counts.set(issue.recordId, (counts.get(issue.recordId) ?? 0) + 1));
    return counts;
  }, [allIssues]);
  const singleSettings = localItems.find((item) => item.singleSlot) ?? null;
  const selectedSnapshotLabel = selectedSnapshot ? displaySnapshot(selectedSnapshot) : "";
  const moreIssues = Math.max(0, allIssues.length - 12);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== "s") return;
      event.preventDefault();
      if (dirty && allIssues.length === 0 && selectedFeature && unsupported.length === 0) void saveWorkspace();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [allIssues.length, dirty, saveWorkspace, selectedFeature, unsupported.length]);

  if (snapshotQuery.isLoading) return <LoadingPage label="Загружаем снапшоты…" />;
  if (snapshotQuery.error) return <div className="page-grid"><ErrorState error={snapshotQuery.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshotQuery.refetch()} /></div>;

  return (
    <div className="page-grid min-w-0">
      {unsavedDialog}
      <header className="flex min-w-0 flex-col gap-4 border-b border-border pb-4 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">Редактор</h1>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            Содержимое сохраняется в отдельный черновик выбранного снапшота. Исходный сайт не меняется.
          </p>
        </div>
        {canCreateSnapshot && selectedSnapshot && (
          <Button variant="outline" size="sm" disabled={!hasDraft || saveStatus === "saving"} onClick={() => void createSnapshot()}>
            Создать снапшот из черновика
          </Button>
        )}
      </header>

      {snapshots.length === 0 ? (
        <section className="rounded-xl border border-dashed border-border p-8 text-center">
          <h2 className="font-medium">Пока нет снапшотов для редактирования</h2>
          <p className="mx-auto mt-2 max-w-lg text-sm text-muted-foreground">Администратор может создать первый снапшот в настройках сайта. Активные сайты пока не изменятся.</p>
        </section>
      ) : (
        <section className="grid min-w-0 gap-2 sm:max-w-xl">
          <Label htmlFor="content-snapshot">Снапшот</Label>
          <Select value={selectedSnapshot?.id ?? ""} onValueChange={(value) => void switchSnapshot(value)} disabled={saveStatus === "saving"}>
            <SelectTrigger id="content-snapshot" aria-label="Выбрать снапшот для редактирования">
              <SelectValue placeholder="Выберите снапшот" />
            </SelectTrigger>
            <SelectContent>
              {snapshots.map((item) => <SelectItem key={item.id} value={item.id}>{displaySnapshot(item)}</SelectItem>)}
            </SelectContent>
          </Select>
        </section>
      )}

      {selectedSnapshot && featuresQuery.isLoading && <LoadingPage label="Загружаем состав снапшота…" />}
      {selectedSnapshot && featuresQuery.error && <ErrorState error={featuresQuery.error} title="Не удалось загрузить состав снапшота" onRetry={() => void featuresQuery.refetch()} />}

      {selectedSnapshot && !featuresQuery.isLoading && !featuresQuery.error && !selectedFeature && (
        <section className="min-w-0 space-y-4">
          <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="relative min-w-0 flex-1 sm:max-w-sm">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
              <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Найти фичу" className="pl-9" aria-label="Найти фичу" />
            </div>
            <Select value={category} onValueChange={setCategory}>
              <SelectTrigger className="w-full sm:w-56" aria-label="Фильтр по категории"><SelectValue /></SelectTrigger>
              <SelectContent>{categories.map((item) => <SelectItem key={item} value={item}>{item}</SelectItem>)}</SelectContent>
            </Select>
          </div>
          {groupedFeatures.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border p-8 text-center">
              <h2 className="font-medium">В этом снапшоте нет доступного контента</h2>
              <p className="mt-1 text-sm text-muted-foreground">Выберите другой снапшот или очистите фильтры.</p>
            </div>
          ) : groupedFeatures.map(([group, items]) => (
            <section key={group} aria-labelledby={`category-${group}`} className="min-w-0 space-y-2">
              <h2 id={`category-${group}`} className="text-sm font-medium text-muted-foreground">{group}</h2>
              <div className="grid min-w-0 gap-2 sm:grid-cols-2 xl:grid-cols-3">
                {items.map((item) => (
                  <button key={item.id} type="button" onClick={() => setParams({ feature: item.key, record: null, settings: null })}
                    className="group flex min-w-0 items-center justify-between gap-3 rounded-xl border border-border bg-card px-4 py-3 text-left transition-colors hover:border-primary/40 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <span className="min-w-0">
                      <span className="block truncate text-sm font-medium">{item.name}</span>
                      <span className="mt-1 block truncate text-xs text-muted-foreground">{modeLabel(item.mode)}</span>
                    </span>
                    <span className="flex shrink-0 items-center gap-2">
                      <FeatureTypeBadge type={item.mode} />
                      <ArrowRight className="h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
                    </span>
                  </button>
                ))}
              </div>
            </section>
          ))}
        </section>
      )}

      {selectedFeature && selectedSnapshot && (
        <section className="min-w-0 space-y-4">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <Button variant="ghost" size="sm" onClick={() => void leaveFeature()}><ArrowLeft />Назад</Button>
            <div className="min-w-0 flex-1 basis-40">
              <h2 className="truncate text-lg font-semibold">{selectedFeature.name}</h2>
              <p className="truncate text-xs text-muted-foreground">{selectedSnapshotLabel}</p>
            </div>
            <div className="flex w-full min-w-0 flex-wrap items-center gap-2 sm:w-auto">
              <FeatureTypeBadge type={selectedFeature.mode} />
              {selectedFeature.mode === "form" && <Button variant="outline" size="sm" asChild><Link to={`/submissions?form=${encodeURIComponent(selectedFeature.key)}`}>Заявки</Link></Button>}
              {unsupported.length === 0 && (activeItem || (selectedFeature.mode === "collection" && localItems.length > 0)) && (
                <>
                  <input ref={importRef} type="file" accept="application/json,.json" hidden onChange={(event) => void importJSON(event)} />
                  <Button type="button" variant="outline" size="sm" disabled={importing || saveStatus === "saving"} onClick={() => importRef.current?.click()}><FileUp />Импорт JSON</Button>
                </>
              )}
              {dirty && <Badge variant={saveStatus === "error" || saveStatus === "conflict" || saveStatus === "invalid" ? "destructive" : "warning"} role="status" aria-live="polite">{saveStatus === "saving" ? "Сохранение…" : saveStatus === "conflict" ? "Конфликт версии" : saveStatus === "error" ? "Ошибка сохранения" : saveStatus === "invalid" ? "Проверьте поля" : "Есть изменения"}</Badge>}
              {saveStatus === "saved" && <Badge variant="success" role="status" aria-live="polite"><Check />Сохранено</Badge>}
              {dirty && <Button size="sm" disabled={saveStatus === "saving" || allIssues.length > 0} onClick={() => void saveWorkspace()}>{saveStatus === "saving" ? "Сохранение…" : "Сохранить"}</Button>}
            </div>
          </div>

          {saveError && (
            <div className="flex min-w-0 flex-col gap-2 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm sm:flex-row sm:items-center sm:justify-between" role="alert">
              <p className="min-w-0 break-words">{saveError}{saveStatus === "conflict" ? " Локальные правки сохранены в редакторе." : ""}</p>
              {saveStatus === "conflict" && (
                <div className="flex shrink-0 flex-wrap gap-2">
                  <Button variant="outline" size="sm" onClick={() => void overrideConflict()}>Сохранить поверх</Button>
                  <Button variant="ghost" size="sm" onClick={() => void reloadWorkspace()}>Загрузить сохранённую</Button>
                </div>
              )}
            </div>
          )}

          {unsupported.length === 0 && allIssues.length > 0 && workspaceQuery.data && (
            <div role="alert" className="min-w-0 space-y-2 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
              <p className="flex items-center gap-1.5 font-medium"><CircleAlert className="h-4 w-4 shrink-0" aria-hidden="true" />Сохранение приостановлено: не все поля заполнены верно ({allIssues.length}).</p>
              <ul className="max-h-44 space-y-0.5 overflow-y-auto pr-1 text-xs">
                {allIssues.slice(0, 12).map((issue, index) => (
                  <li key={`${issue.recordId}-${issue.path}-${index}`} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                    <button
                      type="button"
                      className="min-w-0 text-left underline decoration-dotted underline-offset-2 hover:text-foreground hover:decoration-solid"
                      onClick={() => jumpToIssue(issue)}
                    >
                      {issue.itemTitle} · {fieldLabelForPath(issue.itemFields, issue.path)}
                    </button>
                    <span className="text-destructive">{issue.message}</span>
                  </li>
                ))}
                {moreIssues > 0 && <li className="text-muted-foreground">…и ещё {moreIssues}</li>}
              </ul>
            </div>
          )}

          {workspaceQuery.isLoading && <LoadingPage label="Загружаем содержимое снапшота…" />}
          {workspaceQuery.error && <ErrorState error={workspaceQuery.error} title="Не удалось загрузить содержимое" onRetry={() => void workspaceQuery.refetch()} />}

          {unsupported.length > 0 && <p role="alert" className="rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">Эта схема содержит неподдерживаемые конструкции ({unsupported.join(", ")}). Графический редактор не может безопасно изменить её содержимое. Данные не изменены; обратитесь к разработчику фичи.</p>}

          {unsupported.length === 0 && workspaceQuery.data && selectedFeature.mode === "collection" && !activeItem && (
            <div className="min-w-0 space-y-3">
              {selectedFeature.settingsSchema && (
                <div className="flex flex-col gap-2 rounded-xl border border-border bg-card p-4 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0"><h3 className="text-sm font-medium">Настройки списка</h3><p className="text-xs text-muted-foreground">Общие параметры этой коллекции.</p></div>
                  <Button size="sm" variant="outline" onClick={() => {
                    if (!singleSettings) addCollectionRecord(true);
                    else setParams({ record: null, settings: "1" });
                  }}>{singleSettings ? "Редактировать" : "Добавить настройки"}</Button>
                </div>
              )}
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
                <h3 className="text-sm font-medium">Записи: {recordSearch.trim() ? `${visibleRecords.length} из ${regularItems.length}` : regularItems.length}</h3>
                <div className="flex items-center gap-2">
                  {regularItems.length > 6 && (
                    <div className="relative">
                      <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                      <Input value={recordSearch} onChange={(event) => setRecordSearch(event.target.value)} placeholder="Найти запись" className="h-8 w-48 pl-9 text-xs" aria-label="Найти запись" />
                    </div>
                  )}
                  <Button size="sm" onClick={() => addCollectionRecord(false)}><Plus />Добавить запись</Button>
                </div>
              </div>
              {regularItems.length === 0 ? (
                <div className="rounded-xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">В черновике пока нет записей каталога.</div>
              ) : visibleRecords.length === 0 ? (
                <div className="rounded-xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">Записи по запросу «{recordSearch}» не найдены.</div>
              ) : (
                <ol className="min-w-0 divide-y divide-border overflow-hidden rounded-xl border border-border bg-card">
                  {visibleRecords.map((item) => {
                    const issueCount = issuesByRecord.get(item.entryId) ?? 0;
                    return (
                      <li key={item.entryId} className="flex min-w-0 items-center gap-2 p-3 sm:gap-3">
                        <span className="w-7 shrink-0 text-center text-xs text-muted-foreground">{regularItems.indexOf(item) + 1}</span>
                        <button type="button" className="min-w-0 flex-1 truncate text-left text-sm font-medium hover:text-primary" onClick={() => setParams({ record: item.entryId, settings: null })}>{deriveEntryTitle(item.data, collectionFields)}</button>
                        {issueCount > 0 && allIssues.some((issue) => issue.recordId === item.entryId) && (
                          <button type="button" className="shrink-0" onClick={() => {
                            const firstIssue = allIssues.find((issue) => issue.recordId === item.entryId);
                            if (firstIssue) jumpToIssue(firstIssue);
                          }}>
                            <Badge variant="destructive" className="gap-1"><CircleAlert className="h-3 w-3" />{issueCount}</Badge>
                            <span className="sr-only"> — есть ошибки, перейдите к полям</span>
                          </button>
                        )}
                        <div className="flex shrink-0 items-center gap-1">
                          <Button variant="ghost" size="icon-sm" aria-label="Переместить вверх" disabled={item.sortOrder === 0 || saveStatus === "saving"} onClick={() => moveRecord(item.entryId, -1)}><ArrowUp /></Button>
                          <Button variant="ghost" size="icon-sm" aria-label="Переместить вниз" disabled={item.sortOrder === regularItems.length - 1 || saveStatus === "saving"} onClick={() => moveRecord(item.entryId, 1)}><ArrowDown /></Button>
                          <Button variant="ghost" size="icon-sm" aria-label="Дублировать запись" disabled={saveStatus === "saving"} onClick={() => void duplicateRecord(item)}><Copy /></Button>
                          <Button variant="ghost" size="icon-sm" aria-label="Удалить запись" onClick={() => void deleteRecord(item)}><Trash2 /></Button>
                        </div>
                      </li>
                    );
                  })}
                </ol>
              )}
            </div>
          )}

          {unsupported.length === 0 && workspaceQuery.data && activeItem && (
            <div className="min-w-0 rounded-xl border border-border bg-card p-3 sm:p-5">
              {selectedFeature.mode === "collection" && (
                <div className="mb-4 flex min-w-0 items-center gap-2 border-b border-border pb-3">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">{editingSettings ? "Настройки списка" : deriveEntryTitle(activeItem.data, activeFields)}</span>
                  <Button variant="ghost" size="sm" onClick={() => setParams({ record: null, settings: null })}>К списку</Button>
                </div>
              )}
              <EntryFieldsForm
                fields={activeFields}
                metadata={activeItem.data}
                onChange={changeActiveData}
                disabled={saveStatus === "saving" || importing}
                issues={activeIssues}
                featureKey={selectedFeature.key}
                buildDigest={selectedFeature.buildDigest}
                schemaKind={editingSettings ? "settings" : "content"}
                compact
              />
              {selectedFeature.mode === "collection" && activeIssues.length > 0 && dirty && (
                <p className="mt-3 text-xs text-destructive" role="status">Заполните обязательные поля: {activeIssues.slice(0, 2).map((issue) => issue.path).join(", ")}</p>
              )}
            </div>
          )}
        </section>
      )}

      {selectedFeature && !workspaceQuery.isLoading && !workspaceQuery.error && !workspaceQuery.data && (
        <section className="rounded-xl border border-dashed border-border p-8 text-center">
          <h2 className="font-medium">Черновик пока недоступен</h2>
          <p className="mx-auto mt-2 max-w-lg text-sm text-muted-foreground">Не удалось получить содержимое этой фичи. Попробуйте вернуться к списку фич или перезагрузить снапшот.</p>
        </section>
      )}
    </div>
  );
}
