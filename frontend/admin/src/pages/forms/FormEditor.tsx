import { useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/api";
import { CopyButton, useUnsavedChangesGuard } from "@/components/common/page";
import { useConfirm } from "@/components/common/confirm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Checkbox, Input, Textarea } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";
import type { FormSchema, RouterPublicOperation, RouterRoute } from "@/types";
import { buildFormFieldDefinitions, formFieldToDraft, newFormFieldDraft, type FormFieldDraft, type FormFieldType } from "@/domains/forms/schema-draft";
import { formPublicationSteps, formPublicationWarnings, formSaveErrorMessage, formUploadSettingsError, hasFileFields, normalizedFormBasePath, validFormKey, validRouterHost, type FormUploadSettings } from "@/domains/forms/publication";
import { schemaDetailQueryKey, schemaQueryKey } from "./forms-query";

const fieldTypes: { value: FormFieldType; label: string }[] = [
  { value: "text", label: "Текст" },
  { value: "email", label: "Электронная почта" },
  { value: "number", label: "Число" },
  { value: "boolean", label: "Да / нет" },
  { value: "select", label: "Список вариантов" },
  { value: "file", label: "Файл" },
];

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

const fieldIDPattern = /^[a-z][a-z0-9_]{0,63}$/u;

/** Shared text limits survive a type switch even when the constraints are temporarily hidden. */
export function changeFormFieldType(field: FormFieldDraft, type: FormFieldType): FormFieldDraft {
  return {
    ...newFormFieldDraft(field.id),
    type,
    required: field.required,
    minLength: field.minLength,
    maxLength: field.maxLength,
    pattern: field.pattern,
  };
}

function fieldConstraintError(field: FormFieldDraft): string {
  switch (field.type) {
    case "text":
    case "email":
      return "Проверьте длину и шаблон RE2 поля: минимум не больше максимума, шаблон не длиннее 256 символов.";
    case "number":
      return "Проверьте числовые границы поля: минимум не должен превышать максимум.";
    case "select":
      return "Проверьте варианты поля: хотя бы один уникальный вариант длиной до 256 символов.";
    case "file":
      return "Проверьте лимиты и MIME-типы поля: исполняемые типы запрещены.";
    default:
      return "Проверьте ограничения поля.";
  }
}

/** Minimal field-indexed error map for the existing string-only validation model. */
export function formFieldErrors(fields: FormFieldDraft[]): Record<string, string> {
  const errors: Record<string, string> = {};
  const firstIndexByID = new Map<string, number>();
  fields.forEach((field, index) => {
    const key = String(index);
    if (!fieldIDPattern.test(field.id)) {
      errors[key] = "Проверьте идентификатор: строчные латинские буквы, цифры и «_», до 64 символов.";
      return;
    }
    const firstIndex = firstIndexByID.get(field.id);
    if (firstIndex !== undefined) {
      const message = "Проверьте идентификатор: он должен быть уникальным в схеме.";
      errors[key] = message;
      errors[String(firstIndex)] = message;
      return;
    }
    firstIndexByID.set(field.id, index);
    if (!buildFormFieldDefinitions([field])) errors[key] = fieldConstraintError(field);
  });
  return errors;
}

export function FormSchemaEditor({
  schema,
  canManage,
  onSaved,
  onCancel,
}: {
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
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const baselineKey = schema?.key ?? "";
  const baselineFields = useMemo(() => schema ? schemaFields(schema) : [newFormFieldDraft("field_1")], [schema]);
  const definitions = useMemo(() => buildFormFieldDefinitions(fields), [fields]);
  const dirty = schema
    ? key !== baselineKey || !definitions || !sameValue(definitions, schema.fields)
    : key !== baselineKey || !sameValue(fields, baselineFields);
  const guard = useUnsavedChangesGuard(dirty, allowNavigation);

  const save = useMutation({
    mutationFn: () => {
      const nextFields = buildFormFieldDefinitions(fields);
      if (!validFormKey(key) || !nextFields) throw new Error("Проверьте ключ формы, идентификаторы полей и ограничения.");
      return api.formsEngine.saveSchema({
        ...(schema ? { schema_id: schema.id } : {}),
        key,
        fields: nextFields,
        expected_revision: schema?.revision ?? 0,
      });
    },
    onSuccess: async (saved) => {
      queryClient.setQueryData(schemaDetailQueryKey(saved.id), saved);
      await queryClient.invalidateQueries({ queryKey: schemaQueryKey });
      toast.success(schema ? "Схема формы сохранена" : "Форма создана");
      if (!schema) allowNavigation.current = true;
      onSaved(saved);
    },
    onError: (error) => {
      const message = formSaveErrorMessage(error);
      setValidationMessage(message);
      toast.error(message);
    },
  });

  const updateField = (index: number, change: Partial<FormFieldDraft>) => {
    setValidationMessage("");
    setFieldErrors({});
    setFields((current) => current.map((field, fieldIndex) => fieldIndex === index ? { ...field, ...change } : field));
  };

  const changeFieldType = (index: number, type: FormFieldType) => {
    setValidationMessage("");
    setFieldErrors({});
    setFields((current) => current.map((field, fieldIndex) => fieldIndex === index ? changeFormFieldType(field, type) : field));
  };

  const addField = () => {
    if (fields.length >= 128) {
      setValidationMessage("В одной схеме допускается не более 128 полей.");
      return;
    }
    setValidationMessage("");
    setFieldErrors({});
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
    setFieldErrors({});
  };

  const submit = async () => {
    if (!validFormKey(key) || !definitions) {
      setFieldErrors(formFieldErrors(fields));
      setValidationMessage("Проверьте ключ формы, уникальные идентификаторы полей и ограничения каждого типа. Ключ не может быть пустым, начинаться или заканчиваться пробелом, содержать NUL и превышать 128 байт в UTF-8.");
      return;
    }
    if (schema && dirty) {
      const accepted = await confirm({
        title: "Создать новую версию формы?",
        description: "Изменение схемы создаст новую версию. Router закреплён на конкретной версии и не обновляется автоматически: обновите привязку маршрутов вручную после сохранения.",
        confirmLabel: "Сохранить новую версию",
      });
      if (!accepted) return;
    }
    setValidationMessage("");
    setFieldErrors({});
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
          <Input id="form-schema-key" value={key} maxLength={128} disabled={save.isPending} aria-invalid={key.length > 0 && !validFormKey(key)} onChange={(event) => { setKey(event.currentTarget.value); setValidationMessage(""); setFieldErrors({}); }} placeholder="Например, Contact.Request-2026" />
          <span className="block text-xs text-muted-foreground">Глобально уникальное имя для интеграций и маршрутов.</span>
        </label> : <p className="text-sm"><span className="text-muted-foreground">Ключ формы: </span>{schema?.key}</p>}

        <p role="note" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">
          Изменение схемы создаст новую версию. Публичные маршруты Router закреплены на версии: привязка обновляется вручную, автоматического обновления нет.
        </p>

        <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border pb-3">
          <div>
            <h3 className="font-medium">Поля</h3>
            <p className="mt-1 text-xs text-muted-foreground">Тип и ограничения проверяются Forms при отправке.</p>
          </div>
          {canManage && <Button type="button" variant="outline" onClick={addField} disabled={fields.length >= 128 || save.isPending}><Plus />Добавить поле</Button>}
        </div>

        {fields.length === 0 ? <p className="py-8 text-center text-sm text-muted-foreground">Добавьте хотя бы одно поле.</p> : <ol className="divide-y divide-border">
          {fields.map((field, index) => {
            const error = fieldErrors[String(index)];
            const errorId = error ? `form-field-error-${index}` : undefined;
            const errorProps = { "aria-invalid": Boolean(error) || undefined, "aria-describedby": errorId };
            const keyErrorProps = { ...errorProps, "aria-invalid": Boolean(error) || (field.id.length > 0 && !fieldIDPattern.test(field.id)) || undefined };
            const deleteHint = fields.length === 1 ? "В схеме должно остаться хотя бы одно поле" : null;
            return <li key={`${index}-${field.id}`} className="space-y-3 py-4">
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_13rem_auto] sm:items-end">
                <label className="min-w-0 space-y-1.5 text-sm">
                  <span>Идентификатор поля</span>
                  <Input id={`form-field-key-${index}`} value={field.id} maxLength={64} disabled={!canManage || save.isPending} {...keyErrorProps} onChange={(event) => updateField(index, { id: event.currentTarget.value })} placeholder="email" />
                </label>
                <label className="space-y-1.5 text-sm">
                  <span>Тип</span>
                  <select id={`form-field-type-${index}`} className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" value={field.type} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => changeFieldType(index, event.currentTarget.value as FormFieldType)}>
                    {fieldTypes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                  </select>
                </label>
                {canManage && <Button type="button" variant="ghost" size="icon" className="size-11 justify-self-end text-destructive" aria-label={`Удалить поле ${field.id || index + 1}`} title={deleteHint ?? `Удалить поле ${field.id || index + 1}`} disabled={fields.length === 1 || save.isPending} aria-describedby={deleteHint ? `form-field-delete-hint-${index}` : undefined} onClick={() => void removeField(field)}>
                  <Trash2 />
                  {deleteHint && <span id={`form-field-delete-hint-${index}`} className="sr-only">{deleteHint}</span>}
                </Button>}
              </div>

              <label className="flex min-h-11 w-fit items-center gap-2 text-sm">
                <Checkbox id={`form-field-required-${index}`} checked={field.required} disabled={!canManage || save.isPending} onCheckedChange={(checked) => updateField(index, { required: checked === true })} />
                Обязательное поле
              </label>

              {(field.type === "text" || field.type === "email") && <div className="grid gap-3 sm:grid-cols-3">
                <label className="space-y-1.5 text-sm"><span>Минимум символов</span><Input id={`form-field-min-${index}`} type="number" min={0} max={1048576} value={field.minLength} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { minLength: event.currentTarget.value })} /></label>
                <label className="space-y-1.5 text-sm"><span>Максимум символов</span><Input id={`form-field-max-${index}`} type="number" min={1} max={1048576} value={field.maxLength} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { maxLength: event.currentTarget.value })} /></label>
                <label className="space-y-1.5 text-sm"><span>Шаблон Go RE2</span><Input id={`form-field-pattern-${index}`} value={field.pattern} maxLength={256} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { pattern: event.currentTarget.value })} placeholder="Оставьте пустым без проверки" /></label>
              </div>}

              {field.type === "number" && <div className="grid gap-3 sm:grid-cols-2">
                <label className="space-y-1.5 text-sm"><span>Минимум</span><Input id={`form-field-minimum-${index}`} type="number" step="any" value={field.minimum} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { minimum: event.currentTarget.value })} /></label>
                <label className="space-y-1.5 text-sm"><span>Максимум</span><Input id={`form-field-maximum-${index}`} type="number" step="any" value={field.maximum} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { maximum: event.currentTarget.value })} /></label>
              </div>}

              {field.type === "select" && <label className="block space-y-1.5 text-sm">
                <span>Варианты · по одному на строку</span>
                <Textarea id={`form-field-options-${index}`} value={field.options} rows={4} maxLength={32768} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { options: event.currentTarget.value })} />
              </label>}

              {field.type === "file" && <div className="grid gap-3 sm:grid-cols-2">
                <label className="space-y-1.5 text-sm"><span>Максимум файлов</span><Input id={`form-field-max-files-${index}`} type="number" min={1} max={20} step={1} value={field.maxFiles} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { maxFiles: event.currentTarget.value })} /></label>
                <label className="space-y-1.5 text-sm"><span>Максимум байт на файл</span><Input id={`form-field-max-bytes-${index}`} type="number" min={1} step={1} value={field.maxFileBytes} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { maxFileBytes: event.currentTarget.value })} /></label>
                <label className="space-y-1.5 text-sm sm:col-span-2"><span>Разрешённые MIME-типы · точные значения, по одному на строку</span><Textarea id={`form-field-mime-${index}`} value={field.allowedContentTypes} rows={3} maxLength={4096} disabled={!canManage || save.isPending} {...errorProps} onChange={(event) => updateField(index, { allowedContentTypes: event.currentTarget.value })} /><span className="block text-xs text-muted-foreground">Исполняемые типы, включая HTML, SVG, XML и JavaScript, запрещены.</span></label>
              </div>}

              {error && <p id={errorId} role="alert" className="text-xs text-destructive">{error}</p>}
            </li>;
          })}
        </ol>}

        {validationMessage && <p role="alert" className="text-sm text-destructive">{validationMessage}</p>}
        {canManage && <div className="flex justify-end border-t border-border pt-4"><Button type="button" onClick={() => void submit()} disabled={!dirty || save.isPending} loading={save.isPending}><Save />Сохранить</Button></div>}
      </div>
    </section>
  </>;
}

function parseMimeTypes(value: string): string[] {
  return value.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean);
}

export function PublishFormDialog({
  schema,
  operations,
  routes,
  initialBasePath,
  onClose,
  onPublished,
}: {
  schema: FormSchema;
  operations: RouterPublicOperation[];
  routes: RouterRoute[];
  initialBasePath?: string;
  onClose: () => void;
  onPublished: (host: string, basePath: string, version: number) => void;
}) {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [host, setHost] = useState("");
  const [basePath, setBasePath] = useState(initialBasePath ?? normalizedFormBasePath(schema.key));
  const fileField = Object.values(schema.fields).find((field) => field.type === "file");
  const [accessGroup, setAccessGroup] = useState("");
  const [fileSetID, setFileSetID] = useState("");
  const [maxBytes, setMaxBytes] = useState(() => String(fileField?.max_file_bytes ?? ""));
  const [allowedTypes, setAllowedTypes] = useState(() => (fileField?.allowed_content_types ?? []).join(", "));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [decisions, setDecisions] = useState<Record<string, "update" | "skip">>({});
  const upload = useMemo<FormUploadSettings | undefined>(() => fileField ? {
    accessGroup,
    fileSetID,
    maxBytes: Number(maxBytes),
    allowedContentTypes: parseMimeTypes(allowedTypes),
  } : undefined, [fileField, accessGroup, fileSetID, maxBytes, allowedTypes]);
  const uploadError = fileField ? formUploadSettingsError(upload, operations) : null;
  const hostError = validRouterHost(host.trim()) ? null : "Укажите hostname без порта, например forms.example.test.";
  const basePathError = basePath.trim().startsWith("/") ? null : "Базовый путь должен начинаться с «/».";
  const steps = useMemo(() => formPublicationSteps({ schema, host: host.trim(), basePath: normalizedFormBasePath(schema.key, basePath), operations, existingRoutes: routes, upload, decisions }), [schema, host, basePath, operations, routes, upload, decisions]);
  const warnings = formPublicationWarnings(steps, schema.version);
  const pending = steps.filter((step) => step.action !== "skip");

  const publish = async () => {
    if (hostError || basePathError || pending.length === 0 || uploadError) return;
    if (pending.some((step) => step.action === "update")) {
      const accepted = await confirm({
        title: "Обновить привязку маршрутов?",
        description: `Существующие публичные маршруты будут закреплены на версии ${schema.version} формы «${schema.key}».`,
        confirmLabel: "Обновить привязку",
      });
      if (!accepted) return;
    }
    setBusy(true);
    setError("");
    try {
      for (const step of pending) {
        if (step.action === "update" && step.existing) {
          await api.router.routes.replaceTarget(step.existing.id, step.target, step.existing.revision);
        } else {
          await api.router.routes.create({ host: host.trim(), path: step.path, match: "exact", target: step.target }, `form:${schema.id}:${step.operationId}:${crypto.randomUUID()}`);
        }
      }
      await queryClient.invalidateQueries({ queryKey: ["router", "routes"] });
      onPublished(host.trim(), normalizedFormBasePath(schema.key, basePath), schema.version);
      toast.success(`Форма опубликована: ${host.trim()}${normalizedFormBasePath(schema.key, basePath)} · версия ${schema.version}`);
    } catch (cause) {
      setError(errorMessage(cause));
      toast.error(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  };

  return <Dialog open onOpenChange={(next) => !next && !busy && onClose()}>
    <DialogContent className="max-w-xl">
      <DialogHeader>
        <DialogTitle>Опубликовать форму</DialogTitle>
        <DialogDescription>Создаются явные публичные маршруты сессии и отправки с привязкой к форме и текущей версии схемы. Загрузка добавляется только при наличии файловых полей.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="space-y-1.5 text-sm"><span>Host</span><Input id="form-publish-host" value={host} maxLength={253} disabled={busy} aria-invalid={Boolean(hostError)} aria-describedby={hostError ? "form-publish-host-error" : undefined} onChange={(event) => { setHost(event.currentTarget.value); setError(""); }} placeholder="forms.example.com" /></label>
          <label className="space-y-1.5 text-sm"><span>Базовый путь</span><Input id="form-publish-base-path" value={basePath} maxLength={1024} disabled={busy} aria-invalid={Boolean(basePathError)} aria-describedby={basePathError ? "form-publish-base-path-error" : undefined} onChange={(event) => { setBasePath(event.currentTarget.value); setError(""); }} placeholder={normalizedFormBasePath(schema.key)} /></label>
        </div>
        {hostError && <p id="form-publish-host-error" role="alert" className="text-sm text-destructive">{hostError}</p>}
        {basePathError && <p id="form-publish-base-path-error" role="alert" className="text-sm text-destructive">{basePathError}</p>}
        {fileField && <fieldset className="space-y-3 rounded-md border border-border p-3" disabled={busy}>
          <legend className="px-1 text-sm font-medium">Настройки загрузки вложений</legend>
          <p className="text-xs leading-5 text-muted-foreground">Маршрут загрузки создаётся только для форм с файловыми полями. Backend отклоняет его без группы доступа, набора файлов, лимита и MIME-типов.</p>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1.5 text-sm"><span>Группа доступа</span><Input value={accessGroup} maxLength={64} aria-describedby={uploadError ? "form-publish-upload-error" : undefined} onChange={(event) => { setAccessGroup(event.currentTarget.value); setError(""); }} placeholder="uploads" /></label>
            <label className="space-y-1.5 text-sm"><span>UUID набора файлов</span><Input value={fileSetID} maxLength={36} aria-describedby={uploadError ? "form-publish-upload-error" : undefined} onChange={(event) => { setFileSetID(event.currentTarget.value); setError(""); }} placeholder="01938f7a-68af-7c10-a51d-78b6ca125399" /></label>
            <label className="space-y-1.5 text-sm"><span>Лимит файла, байт</span><Input type="number" min={1} max={operations.find((operation) => operation.id === "public.form-upload")?.max_request_bytes} value={maxBytes} aria-describedby={uploadError ? "form-publish-upload-error" : undefined} onChange={(event) => { setMaxBytes(event.currentTarget.value); setError(""); }} /></label>
            <label className="space-y-1.5 text-sm"><span>Разрешённые MIME-типы</span><Input value={allowedTypes} aria-describedby={uploadError ? "form-publish-upload-error" : undefined} onChange={(event) => { setAllowedTypes(event.currentTarget.value); setError(""); }} placeholder="application/pdf, image/png" /></label>
          </div>
          {uploadError && <p id="form-publish-upload-error" role="alert" className="text-xs text-destructive">{uploadError}</p>}
        </fieldset>}
        {warnings.map((warning, index) => <p key={index} role="note" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">{warning}</p>)}
        <ul className="divide-y divide-border rounded-md border border-border text-sm">
          {steps.map((step) => <li key={step.operationId} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
            <span className="break-all font-mono text-xs">{host.trim()}{step.path}</span>
            <span className="flex flex-wrap items-center gap-2">
              <span className="text-xs text-muted-foreground">{step.operationId}</span>
              <Badge variant="outline">{step.action === "create" ? "Создать" : step.action === "update" ? "Обновить" : "Пропустить"}</Badge>
              {step.existing && !(step.operationId === "public.form-upload" && uploadError) && <Button type="button" size="sm" variant={decisions[step.operationId] === "update" ? "secondary" : "outline"} disabled={busy} onClick={() => setDecisions((current) => ({ ...current, [step.operationId]: current[step.operationId] === "update" ? "skip" : "update" }))}>
                {decisions[step.operationId] === "update" ? "Не обновлять" : "Обновить привязку"}
              </Button>}
            </span>
            {step.reason && <span className="w-full text-xs text-muted-foreground">{step.reason}</span>}
          </li>)}
        </ul>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
      <div className="flex justify-end gap-2"><Button type="button" variant="outline" disabled={busy} onClick={onClose}>Отмена</Button><Button type="button" disabled={busy || pending.length === 0 || Boolean(hostError) || Boolean(basePathError) || Boolean(uploadError)} loading={busy} onClick={() => void publish()}>Опубликовать</Button></div>
    </DialogContent>
  </Dialog>;
}

export function formHasFileFields(schema: FormSchema | undefined): boolean {
  return schema ? hasFileFields(schema) : false;
}
