import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FilePlus2, Plus, Save, Trash2 } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import { useAuth } from "@/app/auth";
import { CopyButton, EmptyState, ErrorState, LoadingPage, PageHeader, useDocumentTitle, useUnsavedChangesGuard } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox, Input, Textarea } from "@/components/ui/input";
import { hasGroupAccess } from "@/lib/permissions";
import { errorMessage } from "@/lib/errors";
import type { FormSchema, WebsiteSnapshot } from "@/types";
import { buildFormFieldDefinitions, formFieldToDraft, hasControlCharacters, newFormFieldDraft, type FormFieldDraft, type FormFieldType } from "@/domains/forms/schema-draft";

const pageSize = 100;
const schemaQueryKey = (snapshotID: string) => ["formsEngine", "schemas", snapshotID] as const;
const schemaDetailQueryKey = (snapshotID: string, schemaID: string) => ["formsEngine", "schema", snapshotID, schemaID] as const;
const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const fieldTypes: { value: FormFieldType; label: string }[] = [
  { value: "text", label: "Текст" },
  { value: "email", label: "Электронная почта" },
  { value: "number", label: "Число" },
  { value: "boolean", label: "Да / нет" },
  { value: "select", label: "Список вариантов" },
  { value: "file", label: "Файл" },
];

function snapshotLabel(snapshot: WebsiteSnapshot): string {
  return `Сайт ${snapshot.site_id.slice(0, 8)} · #${snapshot.sequence} · ${snapshot.name}`;
}

function validSchemaKey(value: string): boolean {
  return value.length > 0 && value.length <= 128 && value.trim() === value && !hasControlCharacters(value);
}

function nextFieldKey(fields: FormFieldDraft[]): string {
  const present = new Set(fields.map((field) => field.id));
  for (let index = 1; index <= 128; index += 1) {
    const candidate = `field_${index}`;
    if (!present.has(candidate)) return candidate;
  }
  return "field";
}

function schemaFields(schema: FormSchema): FormFieldDraft[] {
  return Object.entries(schema.fields).map(([id, definition]) => formFieldToDraft(id, definition));
}

function sameValue(left: unknown, right: unknown): boolean {
  if (left === right) return true;
  if (Array.isArray(left) && Array.isArray(right)) {
    return left.length === right.length && left.every((value, index) => sameValue(value, right[index]));
  }
  if (left && right && typeof left === "object" && typeof right === "object") {
    const leftEntries = Object.entries(left as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b));
    const rightEntries = Object.entries(right as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b));
    return leftEntries.length === rightEntries.length && leftEntries.every(([key, value], index) =>
      key === rightEntries[index]?.[0] && sameValue(value, rightEntries[index]?.[1]));
  }
  return false;
}

function FormSchemaEditor({
  snapshotID,
  schema,
  canManage,
  onSaved,
  onCancel,
}: {
  snapshotID: string;
  schema?: FormSchema;
  canManage: boolean;
  onSaved: (schema: FormSchema) => void;
  onCancel?: () => void;
}) {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const allowNavigation = useRef(false);
  const [key, setKey] = useState(schema?.key ?? "");
  const [fields, setFields] = useState<FormFieldDraft[]>(() => schema ? schemaFields(schema) : [newFormFieldDraft("field_1")]);
  const [validationMessage, setValidationMessage] = useState("");
  const baselineKey = schema?.key ?? "";
  const baselineFields = useMemo(() => schema ? schemaFields(schema) : [newFormFieldDraft("field_1")], [schema]);
  const definitions = useMemo(() => buildFormFieldDefinitions(fields), [fields]);
  const dirty = schema
    ? key !== baselineKey || !definitions || !sameValue(definitions, schema.fields)
    : key !== baselineKey || !sameValue(fields, baselineFields);
  const guard = useUnsavedChangesGuard(dirty, allowNavigation, {
    protectSearch: (currentSearch, nextSearch) => {
      const current = new URLSearchParams(currentSearch);
      const next = new URLSearchParams(nextSearch);
      return current.get("snapshot") !== next.get("snapshot") || current.get("form") !== next.get("form");
    },
  });

  const save = useMutation({
    mutationFn: () => {
      const nextFields = buildFormFieldDefinitions(fields);
      if (!validSchemaKey(key) || !nextFields) throw new Error("Проверьте ключ формы, идентификаторы полей и ограничения.");
      return api.formsEngine.saveSchema({
        snapshot_id: snapshotID,
        ...(schema ? { schema_id: schema.id } : {}),
        key,
        fields: nextFields,
        expected_revision: schema?.revision ?? 0,
      });
    },
    onSuccess: async (saved) => {
      queryClient.setQueryData(schemaDetailQueryKey(snapshotID, saved.id), saved);
      await queryClient.invalidateQueries({ queryKey: schemaQueryKey(snapshotID) });
      toast.success(schema ? "Схема формы сохранена" : "Форма создана");
      if (!schema) allowNavigation.current = true;
      onSaved(saved);
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const updateField = (index: number, change: Partial<FormFieldDraft>) => {
    setValidationMessage("");
    setFields((current) => current.map((field, fieldIndex) => fieldIndex === index ? { ...field, ...change } : field));
  };

  const changeFieldType = (index: number, type: FormFieldType) => {
    setValidationMessage("");
    setFields((current) => current.map((field, fieldIndex) => fieldIndex === index
      ? { ...newFormFieldDraft(field.id), type, required: field.required }
      : field));
  };

  const addField = () => {
    if (fields.length >= 128) {
      setValidationMessage("В одной схеме допускается не более 128 полей.");
      return;
    }
    setValidationMessage("");
    setFields((current) => [...current, newFormFieldDraft(nextFieldKey(current))]);
  };

  const removeField = async (field: FormFieldDraft) => {
    if (fields.length === 1) {
      setValidationMessage("В схеме должно остаться хотя бы одно поле.");
      return;
    }
    if (!(await confirm({
      title: "Удалить поле формы?",
      description: `Поле «${field.id}» перестанет приниматься в новых ответах после сохранения новой версии схемы.`,
      confirmLabel: "Удалить поле",
      destructive: true,
    }))) return;
    setFields((current) => current.filter((item) => item !== field));
    setValidationMessage("");
  };

  const submit = async () => {
    if (!validSchemaKey(key) || !definitions) {
      setValidationMessage("Проверьте ключ формы, уникальные идентификаторы полей и ограничения каждого типа.");
      return;
    }
    if (schema && dirty) {
      const accepted = await confirm({
        title: "Создать новую версию формы?",
        description: "Изменение схемы создаст новую версию. Публичные маршруты привязаны к конкретной версии и до перенастройки будут отклонять новые сессии и отправки.",
        confirmLabel: "Сохранить новую версию",
      });
      if (!accepted) return;
    }
    setValidationMessage("");
    save.mutate();
  };

  return <>
    {guard.dialog}
    <section aria-labelledby="form-schema-editor-title" className="min-w-0">
      <header className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id="form-schema-editor-title" className="text-lg font-semibold">{schema ? schema.key : "Новая форма"}</h2>
            {schema && <Badge variant="outline">v{schema.version}</Badge>}
          </div>
          {schema ? <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>ID <code className="break-all">{schema.id}</code> · ревизия {schema.revision}</span>
            <CopyButton value={schema.id} label="Копировать ID" variant="ghost" />
          </div> : <p className="mt-1 text-xs text-muted-foreground">Идентификатор формы назначится автоматически.</p>}
        </div>
        {!schema && onCancel && <Button type="button" variant="ghost" disabled={save.isPending} onClick={onCancel}>Отмена</Button>}
      </header>

      <div className="space-y-5 py-5">
        {canManage ? <label className="block max-w-xl space-y-1.5 text-sm">
          <span>Ключ формы</span>
          <Input id="form-schema-key" value={key} maxLength={128} disabled={save.isPending} aria-invalid={key.length > 0 && !validSchemaKey(key)} onChange={(event) => { setKey(event.currentTarget.value); setValidationMessage(""); }} placeholder="Например, contact_request" />
          <span className="block text-xs text-muted-foreground">Стабильное имя для интеграций и маршрутов.</span>
        </label> : <p className="text-sm"><span className="text-muted-foreground">Ключ формы: </span>{schema?.key}</p>}

        {schema && dirty && canManage && <p role="note" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">
          Маршруты продолжают ссылаться на закреплённую версию {schema.version}. После сохранения обновите их привязку отдельно.
        </p>}

        <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border pb-3">
          <div>
            <h3 className="font-medium">Поля</h3>
            <p className="mt-1 text-xs text-muted-foreground">Тип и ограничения проверяются Forms при отправке.</p>
          </div>
          {canManage && <Button type="button" variant="outline" onClick={addField} disabled={fields.length >= 128 || save.isPending}><Plus />Добавить поле</Button>}
        </div>

        {fields.length === 0 ? <p className="py-8 text-center text-sm text-muted-foreground">Добавьте хотя бы одно поле.</p> : <ol className="divide-y divide-border">
          {fields.map((field, index) => <li key={`${index}-${field.id}`} className="space-y-3 py-4">
            <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_13rem_auto] sm:items-end">
              <label className="min-w-0 space-y-1.5 text-sm">
                <span>Идентификатор поля</span>
                <Input id={`form-field-key-${index}`} value={field.id} maxLength={64} disabled={!canManage || save.isPending} aria-invalid={field.id.length > 0 && !/^[a-z][a-z0-9_]{0,63}$/u.test(field.id)} onChange={(event) => updateField(index, { id: event.currentTarget.value })} placeholder="email" />
              </label>
              <label className="space-y-1.5 text-sm">
                <span>Тип</span>
                <select id={`form-field-type-${index}`} className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={field.type} disabled={!canManage || save.isPending} onChange={(event) => changeFieldType(index, event.currentTarget.value as FormFieldType)}>
                  {fieldTypes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                </select>
              </label>
              {canManage && <Button type="button" variant="ghost" size="icon" className="size-11 justify-self-end text-destructive" aria-label={`Удалить поле ${field.id || index + 1}`} disabled={save.isPending} onClick={() => void removeField(field)}><Trash2 /></Button>}
            </div>

            <label className="flex min-h-11 w-fit items-center gap-2 text-sm">
              <Checkbox id={`form-field-required-${index}`} checked={field.required} disabled={!canManage || save.isPending} onCheckedChange={(checked) => updateField(index, { required: checked === true })} />
              Обязательное поле
            </label>

            {(field.type === "text" || field.type === "email") && <div className="grid gap-3 sm:grid-cols-3">
              <label className="space-y-1.5 text-sm"><span>Минимум символов</span><Input id={`form-field-min-${index}`} type="number" min={0} max={1048576} value={field.minLength} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { minLength: event.currentTarget.value })} /></label>
              <label className="space-y-1.5 text-sm"><span>Максимум символов</span><Input id={`form-field-max-${index}`} type="number" min={1} max={1048576} value={field.maxLength} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { maxLength: event.currentTarget.value })} /></label>
              <label className="space-y-1.5 text-sm"><span>Шаблон Go RE2</span><Input id={`form-field-pattern-${index}`} value={field.pattern} maxLength={256} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { pattern: event.currentTarget.value })} placeholder="Оставьте пустым без проверки" /></label>
            </div>}

            {field.type === "number" && <div className="grid gap-3 sm:grid-cols-2">
              <label className="space-y-1.5 text-sm"><span>Минимум</span><Input id={`form-field-minimum-${index}`} type="number" step="any" value={field.minimum} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { minimum: event.currentTarget.value })} /></label>
              <label className="space-y-1.5 text-sm"><span>Максимум</span><Input id={`form-field-maximum-${index}`} type="number" step="any" value={field.maximum} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { maximum: event.currentTarget.value })} /></label>
            </div>}

            {field.type === "select" && <label className="block space-y-1.5 text-sm">
              <span>Варианты · по одному на строку</span>
              <Textarea id={`form-field-options-${index}`} value={field.options} rows={4} maxLength={32768} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { options: event.currentTarget.value })} />
            </label>}

            {field.type === "file" && <div className="grid gap-3 sm:grid-cols-2">
              <label className="space-y-1.5 text-sm"><span>Максимум файлов</span><Input id={`form-field-max-files-${index}`} type="number" min={1} max={20} step={1} value={field.maxFiles} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { maxFiles: event.currentTarget.value })} /></label>
              <label className="space-y-1.5 text-sm"><span>Максимум байт на файл</span><Input id={`form-field-max-bytes-${index}`} type="number" min={1} step={1} value={field.maxFileBytes} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { maxFileBytes: event.currentTarget.value })} /></label>
              <label className="space-y-1.5 text-sm sm:col-span-2"><span>Разрешённые MIME-типы · точные значения, по одному на строку</span><Textarea id={`form-field-mime-${index}`} value={field.allowedContentTypes} rows={3} maxLength={4096} disabled={!canManage || save.isPending} onChange={(event) => updateField(index, { allowedContentTypes: event.currentTarget.value })} /><span className="block text-xs text-muted-foreground">Исполняемые типы, включая HTML, SVG, XML и JavaScript, запрещены.</span></label>
            </div>}
          </li>)}
        </ol>}

        {validationMessage && <p role="alert" className="text-sm text-destructive">{validationMessage}</p>}
        {canManage && <div className="flex justify-end border-t border-border pt-4"><Button type="button" onClick={() => void submit()} disabled={!dirty || save.isPending} loading={save.isPending}><Save />Сохранить</Button></div>}
      </div>
    </section>
  </>;
}

export default function FormsPage() {
  useDocumentTitle("Формы");
  const { principal } = useAuth();
  const canManage = hasGroupAccess(principal, "forms_schemas", 2);
  const [params, setParams] = useSearchParams();
  const snapshots = useQuery({
    queryKey: ["website", "snapshots", "all"],
    queryFn: ({ signal }) => api.website.allSnapshots(signal),
  });
  const snapshotItems = snapshots.data ?? [];
  const readySnapshots = snapshotItems.filter((snapshot) => snapshot.state === "ready");
  const requestedSnapshotID = params.get("snapshot") ?? "";
  const selectedSnapshot = readySnapshots.find((snapshot) => snapshot.id === requestedSnapshotID) ?? readySnapshots[0];
  const snapshotID = selectedSnapshot?.id ?? "";

  useEffect(() => {
    if (!snapshotID || requestedSnapshotID === snapshotID) return;
    const next = new URLSearchParams(params);
    next.set("snapshot", snapshotID);
    next.delete("form");
    setParams(next, { replace: true });
  }, [params, requestedSnapshotID, setParams, snapshotID]);

  const schemas = useInfiniteQuery({
    queryKey: schemaQueryKey(snapshotID),
    queryFn: ({ pageParam, signal }) => api.formsEngine.schemas(snapshotID, pageParam ?? undefined, pageSize, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: Boolean(snapshotID),
  });
  const schemaItems = useMemo(() => schemas.data?.pages.flatMap((page) => page.items) ?? [], [schemas.data]);
  const requestedFormID = params.get("form") ?? "";
  const isCreating = requestedFormID === "new";
  const listedSchema = schemaItems.find((schema) => schema.id === requestedFormID);
  const schemaDetail = useQuery({
    queryKey: schemaDetailQueryKey(snapshotID, requestedFormID),
    queryFn: ({ signal }) => api.formsEngine.schema(snapshotID, requestedFormID, signal),
    enabled: Boolean(snapshotID && canonicalUUID.test(requestedFormID) && !isCreating && !listedSchema),
  });
  const selectedSchema = listedSchema ?? schemaDetail.data;

  useEffect(() => {
    if (isCreating || !schemaItems.length || selectedSchema || schemaDetail.isLoading || schemaDetail.error) return;
    if (requestedFormID && canonicalUUID.test(requestedFormID)) return;
    const next = new URLSearchParams(params);
    next.set("form", schemaItems[0]!.id);
    setParams(next, { replace: true });
  }, [isCreating, params, requestedFormID, schemaDetail.error, schemaDetail.isLoading, schemaItems, selectedSchema, setParams]);

  const openForm = (formID: string) => {
    const next = new URLSearchParams(params);
    next.set("form", formID);
    setParams(next);
  };

  const saved = (schema: FormSchema) => {
    if (params.get("form") === schema.id) return;
    const next = new URLSearchParams(params);
    next.set("form", schema.id);
    setParams(next, { replace: true });
  };

  const cancelCreate = () => {
    const next = new URLSearchParams(params);
    next.delete("form");
    setParams(next);
  };

  if (snapshots.isLoading) return <LoadingPage label="Загружаем снапшоты…" />;
  if (snapshots.error) return <div className="page-grid"><ErrorState error={snapshots.error} title="Не удалось загрузить снапшоты" onRetry={() => void snapshots.refetch()} /></div>;
  if (!readySnapshots.length) return <div className="page-grid"><PageHeader title="Формы" description="Схемы определяют типизированные поля, которые принимает публичный движок Forms." /><EmptyState icon={FilePlus2} title="Нет готовых снапшотов" description="Создайте и подготовьте снапшот в разделе «Сайт», затем добавьте в нём схему формы." /></div>;
  if (schemas.isLoading) return <LoadingPage label="Загружаем схемы форм…" />;
  if (schemas.error) return <div className="page-grid"><ErrorState error={schemas.error} title="Не удалось загрузить схемы форм" onRetry={() => void schemas.refetch()} /></div>;
  if (schemaDetail.error && canonicalUUID.test(requestedFormID) && !listedSchema) return <div className="page-grid"><ErrorState error={schemaDetail.error} title="Не удалось загрузить схему формы" onRetry={() => void schemaDetail.refetch()} /></div>;

  const editorSchema = isCreating ? undefined : selectedSchema;
  const editorKey = isCreating ? "new" : editorSchema ? `${editorSchema.id}:${editorSchema.revision}` : "empty";

  return <div className="page-grid">
    <PageHeader title="Формы" description="Схемы задают явные типы, обязательность и ограничения полей. Каждый ответ проверяется по закреплённой версии." actions={<>
      <label className="space-y-1 text-sm"><span className="sr-only">Снапшот</span><select aria-label="Снапшот" className="h-11 min-w-56 rounded-md border border-input bg-background px-3" value={snapshotID} onChange={(event) => { const next = new URLSearchParams(params); next.set("snapshot", event.currentTarget.value); next.delete("form"); setParams(next); }}>{readySnapshots.map((snapshot) => <option key={snapshot.id} value={snapshot.id}>{snapshotLabel(snapshot)}</option>)}</select></label>
      {canManage && <Button type="button" variant="outline" onClick={() => openForm("new")}><Plus />Новая форма</Button>}
    </>} />

    <div className="grid min-w-0 gap-6 xl:grid-cols-[16rem_minmax(0,1fr)]">
      <aside aria-label="Схемы форм" className="min-w-0">
        <div className="mb-2 flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold">Схемы</h2>
          {schemas.hasNextPage && <Button type="button" variant="ghost" size="sm" onClick={() => void schemas.fetchNextPage()} loading={schemas.isFetchingNextPage}>Ещё</Button>}
        </div>
        {schemaItems.length === 0 ? <p className="rounded-md border border-dashed border-border px-3 py-5 text-center text-sm text-muted-foreground">Схем пока нет</p> : <ul className="divide-y divide-border rounded-md border border-border">
          {schemaItems.map((schema) => <li key={schema.id}>
            <button type="button" aria-current={selectedSchema?.id === schema.id ? "page" : undefined} className="flex min-h-14 w-full items-center justify-between gap-2 px-3 py-2 text-left hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring aria-[current=page]:bg-muted" onClick={() => openForm(schema.id)}>
              <span className="min-w-0"><span className="block truncate text-sm font-medium">{schema.key}</span><span className="block text-xs text-muted-foreground">Версия {schema.version}</span></span>
              <Badge variant="outline">{Object.keys(schema.fields).length}</Badge>
            </button>
          </li>)}
        </ul>}
      </aside>

      <div className="min-w-0 rounded-md border border-border bg-card px-4 sm:px-5">
        {schemaDetail.isLoading && canonicalUUID.test(requestedFormID) ? <p role="status" className="py-8 text-center text-sm text-muted-foreground">Загружаем схему…</p> : editorSchema || isCreating ? <FormSchemaEditor key={editorKey} snapshotID={snapshotID} schema={editorSchema} canManage={canManage} onSaved={saved} onCancel={isCreating ? cancelCreate : undefined} /> : <EmptyState icon={FilePlus2} title="Выберите схему" description={schemaItems.length ? "Выберите форму слева или создайте новую." : canManage ? "Создайте первую схему формы." : "Схемы появятся здесь после создания администратором."} action={canManage && schemaItems.length === 0 ? <Button type="button" onClick={() => openForm("new")}><Plus />Создать схему</Button> : undefined} />}
      </div>
    </div>
  </div>;
}
