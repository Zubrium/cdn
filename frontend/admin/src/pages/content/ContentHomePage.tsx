import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, FileImage, Plus, Settings2 } from "lucide-react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { useConfirm } from "@/components/common/confirm";
import { EmptyState, ErrorState, LoadingPage, PageHeader, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import { errorMessage } from "@/lib/errors";
import { hasGroupAccess } from "@/lib/permissions";
import type { ContentModel, ContentModelField, ContentRecord, Media } from "@/types";

const pageSize = 100;
const snapshotKey = ["website", "snapshots"] as const;
type FieldDraft = { id?: string; key?: string; name: string; type: string; required: boolean };
type ModelDraft = { model?: ContentModel; name: string; cardinality: "many" | "singleton"; fields: FieldDraft[] };

function emptyModelDraft(model?: ContentModel): ModelDraft {
  return model
    ? { model, name: model.name, cardinality: model.cardinality, fields: model.fields.map((field) => ({ ...field })) }
    : { name: "", cardinality: "many", fields: [{ name: "Название", type: "text", required: false }] };
}

function generatedKey(value: string): string {
  const normalized = value.normalize("NFKD").toLowerCase().replace(/[\u0300-\u036f]/g, "");
  return normalized.replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 96) || "item";
}

function modelFields(fields: FieldDraft[]): Array<Omit<ContentModelField, "id"> & { id?: string }> {
  const used = new Set<string>();
  return fields.filter((field) => field.name.trim()).map((field, index) => {
    let key = field.key || generatedKey(field.name);
    if (!field.key && used.has(key)) key = `${key}_${index + 1}`;
    used.add(key);
    return { ...(field.id ? { id: field.id } : {}), key, name: field.name.trim(), type: field.type, required: field.required };
  });
}

function recordValues(values: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(values).filter(([, value]) => !(typeof value === "string" && value.trim() === "")));
}

function stableValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableValue);
  if (typeof value !== "object" || value === null) return value;
  return Object.fromEntries(Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, item]) => [key, stableValue(item)]));
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(stableValue(left)) === JSON.stringify(stableValue(right));
}

function primaryRecordValue(record: ContentRecord): string {
  const value = Object.values(record.values).find((candidate) => candidate !== null && candidate !== "");
  if (value === undefined) return "Без названия";
  return typeof value === "object" ? "Запись" : String(value);
}

function labelSnapshot(id: string, siteID: string): string {
  return `Сайт ${siteID.slice(0, 8)} · снапшот ${id.slice(0, 8)}`;
}

function SnapshotSelect({
  value,
  snapshots,
  onChange,
}: {
  value: string;
  snapshots: Array<{ id: string; site_id: string }>;
  onChange: (value: string) => void;
}) {
  return <label className="min-w-0 space-y-1.5 text-sm sm:min-w-64">
    <span className="text-muted-foreground">Снапшот</span>
    <select aria-label="Снапшот" className="h-11 w-full rounded-md border border-input bg-background px-3" value={value} onChange={(event) => onChange(event.currentTarget.value)}>
      {snapshots.map((snapshot) => <option key={snapshot.id} value={snapshot.id}>{labelSnapshot(snapshot.id, snapshot.site_id)}</option>)}
    </select>
  </label>;
}

export default function ContentHomePage() {
  useDocumentTitle("Контент");
  const { principal } = useAuth();
  const confirm = useConfirm();
  const canWrite = hasGroupAccess(principal, "content", 2);
  const canPublish = canWrite;
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { entityKey = "", recordId = "" } = useParams();
  const queryClient = useQueryClient();
  const [selectedModelID, setSelectedModelID] = useState("");
  const [modelDialog, setModelDialog] = useState(false);
  const [modelDraft, setModelDraft] = useState<ModelDraft>(() => emptyModelDraft());
  const [recordDialog, setRecordDialog] = useState(false);
  const [recordDraft, setRecordDraft] = useState<Record<string, unknown>>({});
  const [activeRecord, setActiveRecord] = useState<ContentRecord | null>(null);
  const [mediaField, setMediaField] = useState<string | null>(null);

  const snapshots = useInfiniteQuery({
    queryKey: snapshotKey,
    queryFn: ({ pageParam, signal }) => api.website.snapshots(undefined, pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });
  const snapshotItems = useMemo(() => snapshots.data?.pages.flatMap((page) => page.items) ?? [], [snapshots.data]);
  const requestedSnapshotID = params.get("snapshot") ?? "";
  const selectedSnapshot = snapshotItems.find((snapshot) => snapshot.id === requestedSnapshotID) ?? snapshotItems[0];
  const snapshotID = selectedSnapshot?.id ?? "";

  useEffect(() => {
    if (!snapshotID || requestedSnapshotID === snapshotID) return;
    const next = new URLSearchParams(params);
    next.set("snapshot", snapshotID);
    next.delete("feature");
    next.delete("record");
    setParams(next, { replace: true });
  }, [params, requestedSnapshotID, setParams, snapshotID]);

  const models = useInfiniteQuery({
    queryKey: ["contentEngine", "models", snapshotID],
    queryFn: ({ pageParam, signal }) => api.contentEngine.models(snapshotID, pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: Boolean(snapshotID),
  });
  const modelItems = useMemo(() => models.data?.pages.flatMap((page) => page.items) ?? [], [models.data]);
  const selectedModel = modelItems.find((model) => model.id === selectedModelID)
    ?? modelItems.find((model) => model.key === entityKey)
    ?? modelItems[0];
  const modelID = selectedModel?.id ?? "";

  useEffect(() => {
    if (selectedModel && selectedModelID !== selectedModel.id) setSelectedModelID(selectedModel.id);
    if (!selectedModel && selectedModelID) setSelectedModelID("");
  }, [selectedModel, selectedModelID]);

  const records = useInfiniteQuery({
    queryKey: ["contentEngine", "records", snapshotID, modelID],
    queryFn: ({ pageParam, signal }) => api.contentEngine.records(snapshotID, modelID, pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: Boolean(snapshotID && modelID),
  });
  const recordItems = useMemo(() => records.data?.pages.flatMap((page) => page.items) ?? [], [records.data]);
  const routeRecord = recordItems.find((record) => record.id === recordId) ?? null;
  const recordIsDirty = Boolean(activeRecord && !sameValue(recordValues(recordDraft), recordValues(activeRecord.values)));
  const newRecordHasContent = recordDialog && !activeRecord && Object.keys(recordValues(recordDraft)).length > 0;
  const modelIsDirty = modelDraft.model
    ? !sameValue({ name: modelDraft.name, cardinality: modelDraft.cardinality, fields: modelDraft.fields }, { name: modelDraft.model.name, cardinality: modelDraft.model.cardinality, fields: modelDraft.model.fields })
    : Boolean(modelDraft.name.trim() || modelDraft.fields.length !== 1 || modelDraft.fields[0]?.name !== "Название" || modelDraft.fields[0]?.type !== "text" || modelDraft.fields[0]?.required);
  const hasNextRecordPage = records.hasNextPage;
  const isFetchingNextRecordPage = records.isFetchingNextPage;
  const fetchNextRecordPage = records.fetchNextPage;

  useEffect(() => {
    if (recordId && routeRecord && !recordDialog) {
      setActiveRecord(routeRecord);
      setRecordDraft(routeRecord.values);
      setRecordDialog(true);
    }
  }, [recordDialog, recordId, routeRecord]);

  useEffect(() => {
    if (recordId && !routeRecord && hasNextRecordPage && !isFetchingNextRecordPage) void fetchNextRecordPage();
  }, [recordId, routeRecord, hasNextRecordPage, isFetchingNextRecordPage, fetchNextRecordPage]);

  const modelSave = useMutation({
    mutationFn: () => {
      if (!snapshotID) throw new Error("Сначала выберите снапшот");
      const name = modelDraft.name.trim();
      if (!name) throw new Error("Укажите название модели");
      const fields = modelFields(modelDraft.fields);
      if (fields.length === 0) throw new Error("Добавьте хотя бы одно поле");
      const modelKey = modelDraft.model?.key ?? `${generatedKey(name)}_${crypto.randomUUID().slice(0, 8)}`;
      return api.contentEngine.saveModel({
        snapshot_id: snapshotID,
        ...(modelDraft.model ? { model_id: modelDraft.model.id } : {}),
        key: modelKey,
        name,
        cardinality: modelDraft.cardinality,
        fields,
        expected_revision: modelDraft.model?.revision ?? 0,
      });
    },
    onSuccess: async (saved) => {
      setSelectedModelID(saved.id);
      setModelDialog(false);
      await queryClient.invalidateQueries({ queryKey: ["contentEngine", "models", snapshotID] });
      toast.success(modelDraft.model ? "Модель обновлена" : "Модель создана");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const recordSave = useMutation({
    mutationFn: () => {
      if (!snapshotID || !modelID) throw new Error("Сначала выберите модель");
      return api.contentEngine.saveRecord({
        snapshot_id: snapshotID,
        model_id: modelID,
        ...(activeRecord ? { record_id: activeRecord.id } : {}),
        values: recordValues(recordDraft),
        expected_revision: activeRecord?.revision ?? 0,
      });
    },
    onSuccess: async (saved) => {
      setActiveRecord(saved);
      setRecordDraft(saved.values);
      await queryClient.invalidateQueries({ queryKey: ["contentEngine", "records", snapshotID, modelID] });
      toast.success("Черновик записи сохранён");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const publish = useMutation({
    mutationFn: () => {
      if (!snapshotID || !activeRecord) throw new Error("Сначала сохраните запись");
      return api.contentEngine.publishRecord(snapshotID, activeRecord.id, activeRecord.revision);
    },
    onSuccess: async (saved) => {
      setActiveRecord(saved);
      setRecordDraft(saved.values);
      await queryClient.invalidateQueries({ queryKey: ["contentEngine", "records", snapshotID, modelID] });
      toast.success("Запись опубликована");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const closeRecord = async (navigateToList = true): Promise<boolean> => {
    if ((recordIsDirty || newRecordHasContent) && !(await confirm({ title: "Отбросить изменения записи?", description: "Несохранённый черновик будет потерян.", confirmLabel: "Отбросить", destructive: true }))) return false;
    setRecordDialog(false);
    setActiveRecord(null);
    setRecordDraft({});
    if (navigateToList && recordId && selectedModel) navigate(`/content/entities/${encodeURIComponent(selectedModel.key)}?snapshot=${encodeURIComponent(snapshotID)}`);
    return true;
  };

  const closeModel = async (): Promise<boolean> => {
    if (modelIsDirty && !(await confirm({ title: "Отбросить изменения модели?", description: "Несохранённые поля модели будут потеряны.", confirmLabel: "Отбросить", destructive: true }))) return false;
    setModelDialog(false);
    return true;
  };

  const openNewRecord = () => {
    setActiveRecord(null);
    setRecordDraft({});
    setRecordDialog(true);
    if (selectedModel) navigate(`/content/entities/${encodeURIComponent(selectedModel.key)}?snapshot=${encodeURIComponent(snapshotID)}`);
  };

  const openRecord = (record: ContentRecord) => {
    if (!selectedModel) return;
    setActiveRecord(record);
    setRecordDraft(record.values);
    setRecordDialog(true);
    navigate(`/content/entities/${encodeURIComponent(selectedModel.key)}/records/${encodeURIComponent(record.id)}?snapshot=${encodeURIComponent(snapshotID)}`);
  };

  const setFieldValue = (key: string, value: unknown) => setRecordDraft((current) => ({ ...current, [key]: value }));

  if (snapshots.isLoading) return <LoadingPage label="Загружаем снапшоты…" />;
  if (snapshots.error) return <div className="page-grid"><ErrorState error={snapshots.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshots.refetch()} /></div>;
  if (!selectedSnapshot) return <div className="page-grid"><PageHeader title="Контент" description="Черновики и публикации записей по снапшотам." /><EmptyState title="Нет доступных снапшотов" description="Сначала создайте сайт и снапшот в Website." /></div>;
  if (models.isLoading) return <LoadingPage label="Загружаем модели…" />;
  if (models.error) return <div className="page-grid"><ErrorState error={models.error} title="Не удалось загрузить модели" onRetry={() => void models.refetch()} /></div>;

  return <div className="page-grid">
    <PageHeader title="Контент" description="Редактируйте черновики записей и публикуйте их в выбранном снапшоте." actions={<SnapshotSelect value={snapshotID} snapshots={snapshotItems} onChange={(value) => { void (async () => { if (!(await closeRecord(false))) return; setSelectedModelID(""); setActiveRecord(null); navigate(`/content?snapshot=${encodeURIComponent(value)}`); })(); }} />} />
    {snapshots.hasNextPage && <div className="-mt-5"><Button type="button" variant="ghost" size="sm" onClick={() => void snapshots.fetchNextPage()} loading={snapshots.isFetchingNextPage}>Загрузить ещё снапшоты</Button></div>}
    <div className="grid gap-5 xl:grid-cols-[17rem_minmax(0,1fr)]">
      <section className="min-w-0 rounded-lg border border-border bg-card" aria-labelledby="content-models-title">
        <div className="flex items-center justify-between border-b border-border p-3"><h2 id="content-models-title" className="text-sm font-semibold">Модели</h2>{canWrite && <Button type="button" size="icon" variant="ghost" aria-label="Добавить модель" onClick={() => { setModelDraft(emptyModelDraft()); setModelDialog(true); }}><Plus className="size-4" /></Button>}</div>
        {modelItems.length === 0 ? <p className="px-4 py-8 text-center text-sm text-muted-foreground">В этом снапшоте пока нет моделей.</p> : <ul className="divide-y divide-border">{modelItems.map((model) => <li key={model.id}><button type="button" onClick={() => setSelectedModelID(model.id)} aria-current={model.id === modelID ? "true" : undefined} className={`flex min-h-14 w-full items-center gap-2 px-3 py-2 text-left text-sm ${model.id === modelID ? "bg-accent text-accent-foreground" : "hover:bg-muted/60"}`}><span className="min-w-0 flex-1 truncate">{model.name}</span><span className="shrink-0 text-xs text-muted-foreground">{model.cardinality === "singleton" ? "одна" : "список"}</span></button></li>)}</ul>}
        {models.hasNextPage && <div className="border-t border-border p-2"><Button type="button" variant="ghost" size="sm" className="w-full" onClick={() => void models.fetchNextPage()} loading={models.isFetchingNextPage}>Ещё модели</Button></div>}
      </section>
      <section className="min-w-0 space-y-4" aria-labelledby="content-records-title">
        {!selectedModel ? <EmptyState title="Выберите модель" description={canWrite ? "Создайте модель, чтобы добавлять записи." : "Для этого снапшота модели ещё не созданы."} /> : <>
          <div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><h2 id="content-records-title" className="text-lg font-semibold">{selectedModel.name}</h2><p className="mt-1 text-xs text-muted-foreground">{selectedModel.fields.length} полей · ревизия {selectedModel.revision}</p></div><div className="flex gap-2">{canWrite && <Button type="button" variant="outline" onClick={() => { setModelDraft(emptyModelDraft(selectedModel)); setModelDialog(true); }}><Settings2 />Модель</Button>}{canWrite && <Button type="button" disabled={selectedModel.cardinality === "singleton" && recordItems.length > 0} onClick={openNewRecord}><Plus />Новая запись</Button>}</div></div>
          {records.isLoading ? <p role="status" className="py-8 text-center text-sm text-muted-foreground">Загружаем записи…</p> : records.error ? <ErrorState error={records.error} title="Не удалось загрузить записи" onRetry={() => void records.refetch()} /> : recordItems.length === 0 ? <EmptyState title="Записей пока нет" description={canWrite ? "Добавьте первую запись в этот снапшот." : "В этой модели ещё нет записей."} /> : <div className="overflow-hidden rounded-lg border border-border bg-card"><ul className="divide-y divide-border">{recordItems.map((record, index) => <li key={record.id}><button type="button" onClick={() => openRecord(record)} className="flex min-h-14 w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><span className="grid size-8 shrink-0 place-items-center rounded-full bg-muted text-xs text-muted-foreground">{index + 1}</span><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{primaryRecordValue(record)}</span><span className="mt-0.5 block truncate text-xs text-muted-foreground">Обновлено {new Date(record.updated_at).toLocaleString("ru-RU")}</span></span><span className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground">{record.published_revision > 0 && record.published_revision >= record.revision ? <><Check className="size-3.5" />Опубликовано</> : "Черновик"}</span></button></li>)}</ul>{records.hasNextPage && <div className="border-t border-border p-3 text-center"><Button type="button" variant="outline" onClick={() => void records.fetchNextPage()} loading={records.isFetchingNextPage}>Загрузить ещё</Button></div>}</div>}
        </>}
      </section>
    </div>

    <Dialog open={modelDialog} onOpenChange={(open) => { if (!open && !modelSave.isPending) void closeModel(); }}>
      <DialogContent className="max-w-2xl"><DialogHeader><DialogTitle>{modelDraft.model ? "Настроить модель" : "Новая модель"}</DialogTitle><DialogDescription>Ключ модели и поля создаются автоматически из названий. ID формируются сервером.</DialogDescription></DialogHeader>
        <div className="space-y-4"><label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">Название</span><Input autoFocus maxLength={256} value={modelDraft.name} onChange={(event) => setModelDraft((draft) => ({ ...draft, name: event.currentTarget.value }))} /></label><label className="block space-y-1.5 text-sm"><span className="text-muted-foreground">Количество записей</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={modelDraft.cardinality} onChange={(event) => setModelDraft((draft) => ({ ...draft, cardinality: event.currentTarget.value as ModelDraft["cardinality"] }))}><option value="many">Список</option><option value="singleton">Одна запись</option></select></label>
          <div className="space-y-2"><div className="flex items-center justify-between"><h3 className="text-sm font-medium">Поля</h3><Button type="button" variant="outline" size="sm" onClick={() => setModelDraft((draft) => ({ ...draft, fields: [...draft.fields, { name: "", type: "text", required: false }] }))}><Plus />Добавить поле</Button></div>{modelDraft.fields.map((field, index) => <div key={field.id ?? index} className="grid gap-2 rounded-md border border-border p-3 sm:grid-cols-[minmax(0,1fr)_11rem_auto]"><label className="space-y-1 text-xs"><span className="text-muted-foreground">Название поля</span><Input value={field.name} maxLength={256} onChange={(event) => setModelDraft((draft) => ({ ...draft, fields: draft.fields.map((item, itemIndex) => itemIndex === index ? { ...item, name: event.currentTarget.value } : item) }))} /></label><label className="space-y-1 text-xs"><span className="text-muted-foreground">Тип</span><select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={field.type} onChange={(event) => setModelDraft((draft) => ({ ...draft, fields: draft.fields.map((item, itemIndex) => itemIndex === index ? { ...item, type: event.currentTarget.value } : item) }))}><option value="text">Текст</option><option value="long_text">Длинный текст</option><option value="integer">Целое число</option><option value="number">Число</option><option value="boolean">Да / нет</option><option value="date">Дата</option><option value="datetime">Дата и время</option><option value="url">Ссылка</option><option value="email">Email</option><option value="media">Файл Core</option></select></label><div className="flex items-end"><Button type="button" variant="ghost" disabled={modelDraft.fields.length <= 1} onClick={() => setModelDraft((draft) => ({ ...draft, fields: draft.fields.filter((_, itemIndex) => itemIndex !== index) }))}>Убрать</Button></div></div>)}</div>
        </div><DialogFooter><Button type="button" variant="outline" onClick={() => void closeModel()} disabled={modelSave.isPending}>Отмена</Button><Button type="button" loading={modelSave.isPending} onClick={() => modelSave.mutate()}>{modelDraft.model ? "Сохранить модель" : "Создать модель"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog open={recordDialog} onOpenChange={(open) => !open && void closeRecord()}>
      <DialogContent className="max-w-2xl"><DialogHeader><DialogTitle>{activeRecord ? `Запись ${recordItems.indexOf(activeRecord) + 1}` : "Новая запись"}</DialogTitle><DialogDescription>{selectedModel?.name ?? "Модель"} · черновик снапшота</DialogDescription></DialogHeader>
        <div className="max-h-[60vh] space-y-4 overflow-y-auto pr-1">{selectedModel?.fields.map((field) => {
          const value = recordDraft[field.key];
          if (field.type === "boolean") return <label key={field.id} className="flex min-h-11 items-center gap-3 rounded-md border border-border px-3 text-sm"><input type="checkbox" checked={value === true} onChange={(event) => setFieldValue(field.key, event.currentTarget.checked)} /><span>{field.name}</span></label>;
          if (field.type === "media") return <div key={field.id} className="space-y-1.5"><span className="text-sm text-muted-foreground">{field.name}</span><Button type="button" variant="outline" className="min-h-11 w-full justify-start" onClick={() => setMediaField(field.key)}><FileImage />{typeof value === "string" && value ? `Файл ${value.slice(0, 8)}` : "Выбрать файл Core"}</Button></div>;
          const inputType = field.type === "integer" || field.type === "number" ? "number" : field.type === "date" ? "date" : field.type === "datetime" ? "datetime-local" : field.type === "url" ? "url" : field.type === "email" ? "email" : "text";
          return <label key={field.id} className="block space-y-1.5 text-sm"><span className="text-muted-foreground">{field.name}</span>{field.type === "long_text" ? <textarea className="min-h-28 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={String(value ?? "")} onChange={(event) => setFieldValue(field.key, event.currentTarget.value)} /> : <Input type={inputType} value={String(value ?? "")} onChange={(event) => setFieldValue(field.key, inputType === "number" && event.currentTarget.value !== "" ? Number(event.currentTarget.value) : event.currentTarget.value)} />}</label>;
        })}</div>
        {activeRecord && <p className="text-xs text-muted-foreground">Ревизия {activeRecord.revision} · опубликовано {activeRecord.published_revision}</p>}
        {recordIsDirty && <p role="status" className="text-xs text-muted-foreground">Есть несохранённые изменения.</p>}
        <DialogFooter><Button type="button" variant="outline" onClick={() => void closeRecord()}>Закрыть</Button>{canWrite && <Button type="button" variant="outline" loading={recordSave.isPending} onClick={() => recordSave.mutate()}>Сохранить черновик</Button>}{canPublish && activeRecord && <Button type="button" disabled={recordIsDirty || (activeRecord.published_revision > 0 && activeRecord.published_revision >= activeRecord.revision)} loading={publish.isPending} onClick={() => publish.mutate()}>Опубликовать</Button>}</DialogFooter>
      </DialogContent>
    </Dialog>
    <MediaPickerDialog open={mediaField !== null} onClose={() => setMediaField(null)} onSelect={(files: Media[]) => { if (mediaField) setFieldValue(mediaField, files[0]?.id ?? ""); setMediaField(null); }} title="Выбрать файл Core" />
  </div>;
}
