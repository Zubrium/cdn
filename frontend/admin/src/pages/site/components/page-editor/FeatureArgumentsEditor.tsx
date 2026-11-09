import { useEffect, useId, useRef, useState } from "react";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/input";
import { MediaPickerDialog } from "@/domains/media/MediaPickerDialog";
import type { FeatureArgumentBinding, JsonValue, MediaRenditionContext } from "@/types";
import { appendSchemaPath, initialValue, pointerSegment, type SchemaField } from "./schema";

export function addArgumentArrayItem(values: JsonValue[], itemSchema: SchemaField): JsonValue[] {
  return [...values, initialValue(itemSchema)];
}

export function updateArgumentArrayItem(values: JsonValue[], index: number, value: JsonValue): JsonValue[] {
  if (index < 0 || index >= values.length) return values;
  return values.map((item, itemIndex) => itemIndex === index ? value : item);
}

export function removeArgumentArrayItem(values: JsonValue[], index: number): JsonValue[] {
  if (index < 0 || index >= values.length) return values;
  return values.filter((_, itemIndex) => itemIndex !== index);
}

export function moveArgumentArrayItem(values: JsonValue[], index: number, direction: -1 | 1): JsonValue[] {
  const target = index + direction;
  if (index < 0 || index >= values.length || target < 0 || target >= values.length) return values;
  const next = [...values];
  [next[index], next[target]] = [next[target]!, next[index]!];
  return next;
}

function enumValueKey(value: JsonValue): string {
  return JSON.stringify(value);
}

function enumValueLabel(value: JsonValue, index: number): string {
  if (value !== null && typeof value === "object") return `Вариант ${index + 1}`;
  return String(value);
}

function parseNumberValue(type: string | undefined, raw: string): number | undefined {
  if (raw.trim() === "") return undefined;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || (type === "integer" && !Number.isInteger(parsed))) return undefined;
  return parsed;
}

const routeValueTypes = new Set(["string", "integer", "number", "boolean"]);

function supportsRouteValue(type: string | undefined): boolean {
  return type === undefined || routeValueTypes.has(type);
}

function supportsQueryBinding(schema: SchemaField): boolean {
  if (schema.type === "array") return supportsRouteValue(schema.items?.type);
  return supportsRouteValue(schema.type);
}

function OptionalValueClear({ value, label, required, onChange }: {
  value: JsonValue | undefined;
  label: string;
  required: boolean;
  onChange: (value: JsonValue | undefined) => void;
}) {
  if (required || value === undefined) return null;
  return <Button type="button" variant="ghost" className="min-h-11" aria-label={`Очистить значение ${label}`} onClick={() => onChange(undefined)}>
    Очистить значение
  </Button>;
}

function NumericArgumentInput({ schema, value, label, required, descriptionId, onChange }: {
  schema: SchemaField;
  value: JsonValue | undefined;
  label: string;
  required: boolean;
  descriptionId?: string;
  onChange: (value: JsonValue | undefined) => void;
}) {
  const [draft, setDraft] = useState(() => typeof value === "number" ? String(value) : "");
  const [invalid, setInvalid] = useState(false);

  useEffect(() => {
    setDraft(typeof value === "number" ? String(value) : "");
    setInvalid(false);
  }, [value]);

  const errorId = useId();
  const error = invalid ? schema.type === "integer" ? "Введите целое число." : "Введите корректное число." : "";
  return <div className="space-y-1.5">
    <Label htmlFor={errorId} className="text-sm">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</Label>
    <Input
      id={errorId}
      type="number"
      step={schema.type === "integer" ? "1" : "any"}
      className="min-h-11"
      value={draft}
      required={required}
      aria-invalid={invalid}
      aria-describedby={[descriptionId, invalid ? `${errorId}-error` : undefined].filter(Boolean).join(" ") || undefined}
      onChange={(event) => {
        const raw = event.currentTarget.value;
        setDraft(raw);
        if (raw === "") {
          setInvalid(required);
          if (!required) onChange(undefined);
          return;
        }
        const parsed = parseNumberValue(schema.type, raw);
        setInvalid(parsed === undefined);
        if (parsed !== undefined) onChange(parsed);
      }}
    />
    {error && <span id={`${errorId}-error`} role="alert" className="block text-xs text-destructive">{error}</span>}
  </div>;
}

function ArgumentArrayValue({ schema, value, label, required, argumentPath, preparation, onChange }: {
  schema: SchemaField;
  value: JsonValue | undefined;
  label: string;
  required: boolean;
  argumentPath: string;
  preparation?: MediaRenditionContext;
  onChange: (value: JsonValue | undefined) => void;
}) {
  const id = useId();
  const itemKeyCounter = useRef(0);
  const itemKeys = useRef<string[]>([]);
  const removeButtons = useRef(new Map<string, HTMLButtonElement>());
  const pendingFocus = useRef<string | "add" | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const [status, setStatus] = useState("");
  const values = Array.isArray(value) ? value : [];
  const itemSchema = schema.items;
  if (itemKeys.current.length > values.length) itemKeys.current = itemKeys.current.slice(0, values.length);
  while (itemKeys.current.length < values.length) {
    itemKeys.current.push(`${id}-item-${itemKeyCounter.current++}`);
  }

  useEffect(() => {
    const target = pendingFocus.current;
    if (!target) return;
    pendingFocus.current = null;
    if (target === "add") addButton.current?.focus();
    else removeButtons.current.get(target)?.focus();
  }, [values.length]);

  if (!itemSchema) {
    return <p role="alert" className="text-sm text-destructive">Для массива «{label}» в манифесте не задан тип элемента.</p>;
  }

  const update = (next: JsonValue[]) => onChange(next);
  const addItem = () => {
    const nextKey = `${id}-item-${itemKeyCounter.current++}`;
    itemKeys.current = [...itemKeys.current, nextKey];
    pendingFocus.current = nextKey;
    update(addArgumentArrayItem(values, itemSchema));
    setStatus(`Добавлен элемент ${values.length + 1}`);
  };
  const removeItem = (index: number) => {
    const nextKeys = itemKeys.current.filter((_, itemIndex) => itemIndex !== index);
    itemKeys.current = nextKeys;
    pendingFocus.current = nextKeys[Math.min(index, nextKeys.length - 1)] ?? "add";
    update(removeArgumentArrayItem(values, index));
    setStatus(`Удалён элемент ${index + 1}`);
  };
  const moveItem = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= values.length) return;
    const nextKeys = [...itemKeys.current];
    [nextKeys[index], nextKeys[target]] = [nextKeys[target]!, nextKeys[index]!];
    itemKeys.current = nextKeys;
    update(moveArgumentArrayItem(values, index, direction));
    setStatus(`Элемент перемещён на позицию ${target + 1}`);
  };

  return <fieldset className="min-w-0 space-y-3 rounded-md border border-border p-3" aria-describedby={schema.description ? `${id}-description` : undefined}>
    <legend className="px-1 text-sm font-medium">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</legend>
    {schema.description && <p id={`${id}-description`} className="text-xs text-muted-foreground">{schema.description}</p>}
    <ol aria-label={`Элементы ${label}`} className="space-y-3">
      {values.map((item, index) => {
        const itemLabel = `${label} · элемент ${index + 1}`;
        const itemPath = appendSchemaPath(argumentPath, "items");
        const itemKey = itemKeys.current[index]!;
        return <li key={itemKey} className="min-w-0 rounded-md bg-muted/20 p-3">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs font-medium text-muted-foreground">Элемент {index + 1}</p>
            <div className="flex flex-wrap gap-1">
              <Button type="button" variant="outline" className="min-h-11" aria-label={`Переместить ${itemLabel} выше`} disabled={index === 0} onClick={() => moveItem(index, -1)}><ArrowUp /><span className="sr-only">Выше</span></Button>
              <Button type="button" variant="outline" className="min-h-11" aria-label={`Переместить ${itemLabel} ниже`} disabled={index === values.length - 1} onClick={() => moveItem(index, 1)}><ArrowDown /><span className="sr-only">Ниже</span></Button>
              <Button type="button" variant="ghost" className="min-h-11" aria-label={`Удалить ${itemLabel}`} ref={(node) => {
                if (node) removeButtons.current.set(itemKey, node);
                else removeButtons.current.delete(itemKey);
              }} onClick={() => removeItem(index)}><Trash2 /><span className="sr-only">Удалить</span></Button>
            </div>
          </div>
          <ArgumentValueEditor
            schema={itemSchema}
            value={item}
            label={itemLabel}
            required
            argumentPath={itemPath}
            preparation={preparation ? { ...preparation, fieldPath: itemPath } : undefined}
            onChange={(next) => update(updateArgumentArrayItem(values, index, next ?? initialValue(itemSchema)))}
          />
        </li>;
      })}
    </ol>
    <div className="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" className="min-h-11" aria-label={`Добавить элемент в ${label}`} ref={addButton} onClick={addItem}><Plus />Добавить элемент</Button>
      <OptionalValueClear value={value} label={label} required={required} onChange={onChange} />
      {status && <span role="status" aria-live="polite" className="text-xs text-muted-foreground">{status}</span>}
    </div>
  </fieldset>;
}

function MediaArgumentValue({ schema, value, label, required, controlID, describedBy, preparation, onChange }: {
  schema: SchemaField;
  value: string;
  label: string;
  required: boolean;
  controlID: string;
  describedBy?: string;
  preparation?: MediaRenditionContext;
  onChange: (value: JsonValue | undefined) => void;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const selectedValue = value || undefined;
  return <div className="space-y-2 rounded-md border border-border p-3">
    <p id={`${controlID}-label`} className="text-sm font-medium">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</p>
    {schema.description && <p id={`${controlID}-description`} className="text-xs text-muted-foreground">{schema.description}</p>}
    <Button id={controlID} aria-label={`Выбрать медиа для ${label}`} aria-labelledby={`${controlID}-label`} aria-describedby={describedBy} type="button" variant="outline" className="min-h-11 w-full justify-start" onClick={() => setPickerOpen(true)}>{selectedValue ? `Выбрано медиа: ${selectedValue}` : "Выбрать файл"}</Button>
    <MediaPickerDialog open={pickerOpen} onClose={() => setPickerOpen(false)} onSelect={(items) => { const selected = items[0]; if (selected) onChange(selected.id); }} accept={schema["x-media"]?.accept === "raster-image" ? "raster-image" : "image"} initialSelected={selectedValue ? [selectedValue] : []} preparation={preparation} />
    <OptionalValueClear value={value || undefined} label={label} required={required} onChange={onChange} />
  </div>;
}

export function ArgumentField({ schema, argumentKey, binding, captures, required = false, onChange, preparation }: { schema: SchemaField; argumentKey: string; binding?: FeatureArgumentBinding; captures: string[]; required?: boolean; onChange: (binding?: FeatureArgumentBinding) => void; preparation?: MediaRenditionContext }) {
  const id = useId();
  const pathId = `${id}-path`;
  const queryId = `${id}-query`;
  const descriptionId = `${id}-description`;
  const name = schema.title ?? argumentKey;
  const source = schema.type === "string" && schema.format === "media" ? "literal" : binding?.source ?? "literal";
  const value = binding?.source === "literal" ? binding.value : schema.default ?? initialValue(schema);
  const isPathScalar = ["string", "integer", "number", "boolean"].includes(schema.type ?? "") && captures.length > 0;
  const canUseQuery = schema.type !== "string" || schema.format !== "media" ? supportsQueryBinding(schema) : false;
  const hasStalePath = binding?.source === "path" && (!captures.includes(binding.pathArg ?? "") || !isPathScalar);
  const hasStaleQuery = binding?.source === "query" && !canUseQuery;
  const emptyQueryKey = binding?.source === "query" && !binding.queryKey?.trim();
  const isMediaArgument = schema.type === "string" && schema.format === "media";
  const hasStaleMediaSource = isMediaArgument && binding !== undefined && binding.source !== "literal";
  const rootPath = `/${pointerSegment(argumentKey)}`;
  const literalChange = (next: JsonValue | undefined) => onChange(next === undefined ? undefined : { source: "literal", value: next });
  return <div className="space-y-3 rounded-md border border-border p-3">
    <div className="flex flex-wrap items-center justify-between gap-2"><div><p className="text-sm font-medium leading-none">{name}{required && <span className="ml-1 text-destructive">· обязательно</span>}</p>{source !== "literal" && schema.description && <p id={descriptionId} className="mt-1 text-xs text-muted-foreground">{schema.description}</p>}</div>{schema.format !== "media" && <select aria-label={`Источник аргумента ${name}`} className="min-h-11 rounded-md border border-input bg-background px-2 text-xs" value={source} onChange={(event) => {
      const next = event.currentTarget.value;
      if (next === "path") onChange({ source: "path", pathArg: captures[0] });
      else if (next === "query") onChange({ source: "query", queryKey: name } as FeatureArgumentBinding);
      else onChange({ source: "literal", value: schema.default ?? initialValue(schema) });
    }}><option value="literal">Постоянное</option>{(isPathScalar || hasStalePath) && <option value="path">{hasStalePath ? "Из пути · параметр недоступен" : "Из пути"}</option>}{(canUseQuery || hasStaleQuery) && <option value="query">{hasStaleQuery ? "Из query · тип больше не поддерживается" : "Из query"}</option>}</select>}</div>
    {source === "path" && <div className="space-y-1"><Label htmlFor={pathId} className="text-xs text-muted-foreground">Параметр пути</Label><select id={pathId} aria-invalid={hasStalePath} aria-describedby={[schema.description ? descriptionId : undefined, hasStalePath ? `${pathId}-error` : undefined].filter(Boolean).join(" ") || undefined} className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={binding?.source === "path" ? binding.pathArg : captures[0] ?? ""} onChange={(event) => onChange({ source: "path", pathArg: event.currentTarget.value })}>
      {!captures.length && <option value="">Нет параметров пути</option>}
      {hasStalePath && <option value={binding?.source === "path" ? binding.pathArg : ""} disabled>{binding?.source === "path" ? `${binding.pathArg} · маршрут недоступен` : "Параметр маршрута недоступен"}</option>}
      {captures.map((capture) => <option key={capture} value={capture}>{capture}</option>)}
    </select>{hasStalePath && <p id={`${pathId}-error`} role="alert" className="text-xs text-warning">Сохранённый параметр пути больше не доступен для страницы. Выберите другой источник или постоянное значение.</p>}</div>}
    {source === "query" && <div className="space-y-1"><Label htmlFor={queryId} className="text-xs text-muted-foreground">Ключ query-строки</Label><Input id={queryId} aria-invalid={emptyQueryKey} aria-describedby={[schema.description ? descriptionId : undefined, emptyQueryKey ? `${queryId}-error` : undefined].filter(Boolean).join(" ") || undefined} className="min-h-11" value={binding?.source === "query" ? binding.queryKey : name} onChange={(event) => onChange({ source: "query", queryKey: event.currentTarget.value })} placeholder="page" />{emptyQueryKey && <p id={`${queryId}-error`} role="alert" className="text-xs text-warning">Укажите имя query-параметра.</p>}{required && <p className="text-xs text-muted-foreground">Если параметр отсутствует в URL, обязательный аргумент не будет передан и страница не пройдёт проверку.</p>}</div>}
    {hasStaleQuery && <p role="alert" className="text-xs text-warning">Тип аргумента изменился и больше не может быть заполнен query-параметром. Выберите постоянное значение.</p>}
    {hasStaleMediaSource && <p role="alert" className="text-xs text-warning">Аргумент медиа больше нельзя брать из маршрута или query. Выберите существующий файл, чтобы заменить старую связь.</p>}
    {source === "literal" && <ArgumentValueEditor schema={schema} value={value} label={name} required={required} argumentPath={rootPath} preparation={preparation ? { ...preparation, fieldPath: rootPath } : undefined} onChange={literalChange} />}
  </div>;
}

function ArgumentValueEditor({ schema, value, label, required, argumentPath, preparation, onChange }: {
  schema: SchemaField;
  value: JsonValue | undefined;
  label: string;
  required: boolean;
  argumentPath: string;
  preparation?: MediaRenditionContext;
  onChange: (value: JsonValue | undefined) => void;
}) {
  const controlID = useId();
  const descriptionID = `${controlID}-description`;
  const describedBy = schema.description ? descriptionID : undefined;
  const selectedValue = value !== undefined ? value : schema.default;

  if (schema.type === "object") {
    const current = selectedValue !== null && typeof selectedValue === "object" && !Array.isArray(selectedValue) ? selectedValue : {};
    const children = schema.properties ?? {};
    const requiredChildren = new Set(schema.required ?? []);
    return <fieldset className="min-w-0 space-y-3 rounded-md border border-border p-3" aria-describedby={describedBy}>
      <legend className="px-1 text-sm font-medium">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</legend>
      {schema.description && <p id={descriptionID} className="text-xs text-muted-foreground">{schema.description}</p>}
      {Object.entries(children).length ? <div className="space-y-3">
        {Object.entries(children).map(([key, childSchema]) => {
          const nextValue = (childValue: JsonValue | undefined) => {
            const next = { ...current };
            if (childValue === undefined) delete next[key];
            else next[key] = childValue;
            onChange(next);
          };
          const childPath = appendSchemaPath(argumentPath, key);
          return <ArgumentValueEditor
            key={key}
            schema={childSchema}
            value={current[key]}
            label={`${label} · ${childSchema.title ?? key}`}
            required={requiredChildren.has(key)}
            argumentPath={childPath}
            preparation={preparation ? { ...preparation, fieldPath: childPath } : undefined}
            onChange={nextValue}
          />;
        })}
      </div> : <p className="text-sm text-muted-foreground">В манифесте для объекта не заданы поля.</p>}
      <OptionalValueClear value={value} label={label} required={required} onChange={onChange} />
    </fieldset>;
  }

  if (schema.type === "array") {
    return <ArgumentArrayValue schema={schema} value={selectedValue} label={label} required={required} argumentPath={argumentPath} preparation={preparation} onChange={onChange} />;
  }

  if (schema.enum?.length) {
    const selectedKey = selectedValue !== undefined ? enumValueKey(selectedValue) : "";
    return <div className="space-y-2 rounded-md border border-border p-3">
      <label className="block space-y-1.5 text-sm" htmlFor={controlID}>
        <span className="block font-medium">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</span>
        <select id={controlID} className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={selectedKey} required={required} aria-describedby={describedBy} onChange={(event) => {
          if (event.currentTarget.value === "") onChange(undefined);
          else onChange(JSON.parse(event.currentTarget.value) as JsonValue);
        }}>
          {!required && <option value="">Не задано</option>}
          {schema.enum.map((option, index) => <option key={`${index}-${enumValueKey(option)}`} value={enumValueKey(option)}>{enumValueLabel(option, index)}</option>)}
        </select>
      </label>
      {schema.description && <p id={descriptionID} className="text-xs text-muted-foreground">{schema.description}</p>}
      <OptionalValueClear value={value} label={label} required={required} onChange={onChange} />
    </div>;
  }

  if (schema.type === "boolean") {
    const selected = typeof selectedValue === "boolean" ? String(selectedValue) : "";
    return <div className="space-y-2 rounded-md border border-border p-3">
      <label className="block space-y-1.5 text-sm" htmlFor={controlID}>
        <span className="block font-medium">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</span>
        <select id={controlID} className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={selected} required={required} aria-describedby={describedBy} onChange={(event) => onChange(event.currentTarget.value === "" ? undefined : event.currentTarget.value === "true")}>
          {!required && <option value="">Не задано</option>}
          <option value="true">Да</option>
          <option value="false">Нет</option>
        </select>
      </label>
      {schema.description && <p id={descriptionID} className="text-xs text-muted-foreground">{schema.description}</p>}
      <OptionalValueClear value={value} label={label} required={required} onChange={onChange} />
    </div>;
  }

  if (schema.type === "integer" || schema.type === "number") {
    return <div className="space-y-2 rounded-md border border-border p-3">
      <NumericArgumentInput schema={schema} value={typeof value === "number" ? value : undefined} label={label} required={required} descriptionId={describedBy} onChange={onChange} />
      {schema.description && <p id={descriptionID} className="text-xs text-muted-foreground">{schema.description}</p>}
      <OptionalValueClear value={value} label={label} required={required} onChange={onChange} />
    </div>;
  }

  if (schema.type === "string" && schema.format === "media") {
    const selected = typeof value === "string" ? value : "";
    return <MediaArgumentValue schema={schema} value={selected} label={label} required={required} controlID={controlID} describedBy={describedBy} preparation={preparation} onChange={onChange} />;
  }

  if (schema.type === "string" || schema.type === undefined) {
    const stringValue = typeof selectedValue === "string" ? selectedValue : "";
    const inputType = schema.format === "email" ? "email" : schema.format === "uri" || schema.format === "url" ? "url" : schema.format === "date" ? "date" : "text";
    return <div className="space-y-2 rounded-md border border-border p-3">
      <label className="block space-y-1.5 text-sm" htmlFor={controlID}>
        <span className="block font-medium">{label}{required && <span className="ml-1 text-destructive">· обязательно</span>}</span>
        <Input id={controlID} type={inputType} className="min-h-11" value={stringValue} required={required} aria-describedby={describedBy} onChange={(event) => onChange(event.currentTarget.value)} />
      </label>
      {schema.description && <p id={descriptionID} className="text-xs text-muted-foreground">{schema.description}</p>}
      <OptionalValueClear value={value} label={label} required={required} onChange={onChange} />
    </div>;
  }

  return <p role="alert" className="text-sm text-destructive">Поле «{label}» имеет неподдерживаемый тип в визуальном редакторе.</p>;
}



export function FeatureArgumentsEditor({ fields, requiredKeys, arguments: bindings, captures, preparation, onChange }: {
  fields: Record<string, SchemaField>;
  requiredKeys: ReadonlySet<string>;
  arguments?: Record<string, FeatureArgumentBinding>;
  captures: string[];
  preparation?: Omit<MediaRenditionContext, "fieldPath">;
  onChange: (key: string, binding?: FeatureArgumentBinding) => void;
}) {
  return <fieldset className="space-y-2 border-t border-border pt-3">
    <legend className="px-1 text-sm font-medium">Аргументы размещения</legend>
    {Object.entries(fields).map(([key, schema]) => <ArgumentField
      key={key}
      schema={schema}
      argumentKey={key}
      required={requiredKeys.has(key)}
      binding={bindings?.[key]}
      captures={captures}
      onChange={(binding) => onChange(key, binding)}
      preparation={preparation ? { ...preparation, fieldPath: `/${pointerSegment(key)}` } : undefined}
    />)}
  </fieldset>;
}
