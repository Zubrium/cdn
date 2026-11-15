import { Link } from "react-router-dom";
import type {
  DataInputBinding,
  DataInputField,
  DataInputFieldBinding,
  DataInputPort,
  DataModel,
  DataModelField,
  FeatureArgumentBinding,
} from "@/types";
import type { SchemaField } from "./page-editor/schema";

type Props = {
  ports: DataInputPort[];
  dataModel?: DataModel;
  bindings: DataInputBinding[];
  arguments?: Record<string, FeatureArgumentBinding>;
  argumentFields?: Record<string, SchemaField>;
  routeCaptures: string[];
  snapshotId: string;
  onChange: (bindings: DataInputBinding[]) => void;
};

const stringFieldTypes = new Set(["text", "longText", "date", "dateTime", "url", "email", "enum"]);

export type ModelFieldChoice = {
  field: DataModelField;
  entity: DataModel["entities"][number];
  label: string;
};

function compatibleInputType(declared: { type?: DataModelField["type"]; values?: string[] }, field: DataModelField): boolean {
  if (field.type !== declared.type) return false;
  if (field.type !== "enum") return true;
  const allowedValues = new Set(declared.values ?? []);
  return (field.options ?? []).every((option) => allowedValues.has(option));
}

function directFieldChoices(model: DataModel, entityID: string): ModelFieldChoice[] {
  const entity = model.entities.find((item) => item.id === entityID);
  if (!entity) return [];
  return entity.fields.map((field) => ({ field, entity, label: `${entity.name} → ${field.name}` }));
}

export function compatibleScalarFieldChoices(model: DataModel, entityID: string, port: DataInputPort): ModelFieldChoice[] {
  if (port.kind !== "scalar" || !port.type) return [];
  return directFieldChoices(model, entityID).filter((choice) => compatibleInputType(port, choice.field));
}

export function scalarFieldBinding(choice: ModelFieldChoice): Pick<DataInputBinding, "fieldId"> {
  return { fieldId: choice.field.id };
}

export function compatibleFieldChoices(model: DataModel, entityID: string, declared: DataInputField): ModelFieldChoice[] {
  if (!supportedInputPath(declared.path)) return [];
  return directFieldChoices(model, entityID).filter((choice) => compatibleInputType(declared, choice.field));
}

export function fieldBinding(inputPath: string, choice: ModelFieldChoice): DataInputFieldBinding {
  return { inputPath, fieldId: choice.field.id };
}

function supportedInputPath(path: string): boolean {
  return !path.split("/").slice(1).includes("*");
}

function readableInputPath(portKey: string, path: string): string {
  const segments = path.split("/").slice(1).map((segment) => segment.replaceAll("~1", "/").replaceAll("~0", "~"));
  return [readableName(portKey), ...segments.map((segment) => readableName(segment))].join(" → ");
}

function readableName(value: string): string {
  const spaced = value.replace(/[_-]+/gu, " ").replace(/([a-z0-9])([A-Z])/gu, "$1 $2").trim();
  return spaced ? spaced.charAt(0).toLocaleUpperCase("ru") + spaced.slice(1) : "Значение";
}

function fieldTypeLabel(type: DataModelField["type"] | undefined): string {
  const labels: Partial<Record<DataModelField["type"], string>> = {
    text: "текст",
    longText: "длинный текст",
    integer: "целое число",
    number: "число",
    boolean: "да или нет",
    date: "дата",
    dateTime: "дата и время",
    url: "ссылка",
    email: "email",
    enum: "варианты",
    media: "файл",
  };
  return type ? labels[type] ?? type : "значение";
}

function bindingFor(bindings: DataInputBinding[], key: string): DataInputBinding | undefined {
  return bindings.find((binding) => binding.inputKey === key);
}

export function DataInputBindingsEditor({
  ports,
  dataModel,
  bindings,
  arguments: argumentBindings,
  argumentFields = {},
  routeCaptures,
  snapshotId,
  onChange,
}: Props) {
  const entities = dataModel?.entities ?? [];
  const pathArguments = Object.entries(argumentBindings ?? {}).filter(([, binding]) =>
    binding.source === "path" && binding.pathArg && routeCaptures.includes(binding.pathArg),
  );
  const setBinding = (next: DataInputBinding) =>
    onChange([...bindings.filter((binding) => binding.inputKey !== next.inputKey), next]);

  return <fieldset className="space-y-3 border-t border-border pt-3">
    <legend className="px-1 text-sm font-medium">Подключение данных модели</legend>
    <p className="text-xs leading-5 text-muted-foreground">Каждый вход фичи ссылается только на поля выбранной модели.</p>
    {entities.length === 0 && <p className="rounded-md border border-dashed border-border p-3 text-sm text-muted-foreground">
      Сначала создайте модель и поля в <Link className="underline underline-offset-4" to={`/site/data-model${snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : ""}`}>редакторе моделей</Link>.
    </p>}
    {ports.map((port) => {
      const binding = bindingFor(bindings, port.key);
      const entity = entities.find((item) => item.id === binding?.entityId);
      const lookupFieldID = binding?.lookup?.fieldId;
      const hasCompatibleLookupField = entity?.fields.some((field) =>
        field.id === lookupFieldID && field.unique && stringFieldTypes.has(field.type),
      ) ?? false;
      const controlID = `data-input-${port.key}`;
      const entityErrorID = `${controlID}-entity-error`;
      const invalidEntity = entities.length > 0 && !entity;
      const selectClass = "h-11 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";
      const scalarCandidates = binding && entity && dataModel && port.kind === "scalar"
        ? compatibleScalarFieldChoices(dataModel, entity.id, port)
        : [];
      const selectedScalarChoice = scalarCandidates.find((choice) => choice.field.id === binding?.fieldId);

      return <section key={port.key} className="space-y-3 border-t border-border/70 pt-4" aria-labelledby={`${controlID}-title`}>
        <div>
          <h4 id={`${controlID}-title`} className="text-sm font-medium">{readableName(port.key)}</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">{port.kind === "collection" ? "Коллекция записей" : port.kind === "record" ? "Одна запись" : `Одно поле · ${fieldTypeLabel(port.type)}`}</p>
        </div>

        <label className="block space-y-1.5 text-sm">
          <span className="text-muted-foreground">Модель</span>
          <select
            id={`${controlID}-entity`}
            className={selectClass}
            aria-invalid={invalidEntity}
            aria-describedby={entityErrorID}
            required
            value={binding?.entityId ?? ""}
            disabled={!entities.length}
            onChange={(event) => {
              const entityId = event.currentTarget.value;
              setBinding({ inputKey: port.key, entityId, ...(port.kind === "record" && binding?.lookup ? { lookup: binding.lookup } : {}) });
            }}
          >
            <option value="">Выберите модель</option>
            {entities.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
          </select>
          <span id={entityErrorID} className={invalidEntity ? "block text-xs text-warning" : "sr-only"} role={invalidEntity ? "alert" : undefined}>
            {invalidEntity ? (binding?.entityId ? "Сохранённая модель больше не существует. Выберите доступную модель." : "Выберите модель для этого входа.") : ""}
          </span>
        </label>

        {entity && <p className="bg-muted/40 px-3 py-2 text-xs leading-5 text-muted-foreground">
          {port.kind === "collection"
            ? `Источник: опубликованные записи модели «${entity.name}». Выбор и порядок записей настраиваются в «Контенте».`
            : port.kind === "record"
              ? `Источник: одна запись модели «${entity.name}». ${binding?.lookup ? "Она определяется по параметру маршрута." : "Запись выбирается в «Контенте»."}`
              : `Источник: поле записи модели «${entity.name}».`}
        </p>}

        {binding && entity && (port.kind === "scalar" ? <label className="block space-y-1.5 text-sm">
          <span className="text-muted-foreground">Поле · {fieldTypeLabel(port.type)}</span>
          <select
            aria-label={`Поле модели для ${readableName(port.key)}`}
            className={selectClass}
            aria-invalid={!selectedScalarChoice}
            value={selectedScalarChoice?.field.id ?? ""}
            onChange={(event) => {
              const choice = scalarCandidates.find((item) => item.field.id === event.currentTarget.value);
              setBinding(choice
                ? { ...binding, ...scalarFieldBinding(choice), fields: undefined }
                : { ...binding, fieldId: undefined, fields: undefined });
            }}
          >
            <option value="">Выберите поле</option>
            {scalarCandidates.map((choice) => <option key={choice.field.id} value={choice.field.id}>{choice.label}</option>)}
          </select>
          {!selectedScalarChoice && <span className="text-xs text-warning" role="alert">
            {binding.fieldId
              ? "Сохранённое поле больше не подходит этому входу."
              : scalarCandidates.length > 0
                ? "Выберите поле для этого входа."
                : "В модели нет полей совместимого типа."}
          </span>}
        </label> : <div className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground">Сопоставление полей модели</p>
          {(port.fields ?? []).map((declared, fieldIndex) => {
            const mapping = binding.fields?.find((item) => item.inputPath === declared.path);
            const candidates = dataModel ? compatibleFieldChoices(dataModel, entity.id, declared) : [];
            const selectedChoice = candidates.find((choice) => choice.field.id === mapping?.fieldId);
            const inputLabel = readableInputPath(port.key, declared.path);
            const mappingErrorID = `${controlID}-mapping-${fieldIndex}-error`;
            return <label key={declared.path} className="grid gap-1.5 text-sm sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] sm:items-center">
              <span className="min-w-0"><span className="block truncate font-medium">{inputLabel}</span><span className="text-xs text-muted-foreground">{fieldTypeLabel(declared.type)}</span></span>
              <select
                aria-label={`Поле модели для ${inputLabel}`}
                className={selectClass}
                aria-invalid={!selectedChoice}
                aria-describedby={mappingErrorID}
                value={selectedChoice?.field.id ?? ""}
                onChange={(event) => {
                  const nextFields = (binding.fields ?? []).filter((item) => item.inputPath !== declared.path);
                  const choice = candidates.find((item) => item.field.id === event.currentTarget.value);
                  if (choice) nextFields.push(fieldBinding(declared.path, choice));
                  setBinding({ ...binding, fieldId: undefined, fields: nextFields });
                }}
              >
                <option value="">Выберите поле</option>
                {candidates.map((choice) => <option key={choice.field.id} value={choice.field.id}>{choice.label}</option>)}
              </select>
              <span id={mappingErrorID} className={`col-span-full text-xs ${selectedChoice ? "sr-only" : "text-warning"}`} role={selectedChoice ? undefined : "alert"}>
                {!selectedChoice
                  ? !supportedInputPath(declared.path)
                    ? "Путь коллекции через связи больше не поддерживается. Поле нужно сопоставить напрямую."
                    : mapping
                      ? "Сохранённое сопоставление больше не подходит модели."
                      : candidates.length > 0
                        ? "Выберите совместимое поле модели."
                        : "В выбранной модели нет поля совместимого типа."
                  : ""}
              </span>
            </label>;
          })}
        </div>)}

        {binding && entity && port.kind === "record" && <fieldset className="space-y-2 border-l-2 border-border pl-3">
          <legend className="px-1 text-xs font-medium">Запись из параметра маршрута</legend>
          <p className="text-xs leading-5 text-muted-foreground">Без этого сопоставления запись выбирается в редакторе контента.</p>
          <label className="block space-y-1 text-sm"><span className="text-muted-foreground">Аргумент пути</span>
            <select className={selectClass} value={binding.lookup?.argument ?? ""} disabled={pathArguments.length === 0} onChange={(event) => {
              const argument = event.currentTarget.value;
              if (!argument) {
                setBinding({ ...binding, lookup: undefined, detailSEO: undefined });
                return;
              }
              const fieldId = binding.lookup?.fieldId ?? entity.fields.find((field) => field.unique && stringFieldTypes.has(field.type))?.id ?? "";
              setBinding({ ...binding, lookup: { argument, fieldId } });
            }}>
              <option value="">Выбирать запись в «Контенте»</option>
              {pathArguments.map(([key, value]) => <option key={key} value={key}>{argumentFields[key]?.title ?? "Параметр фичи"} ← {value.pathArg}</option>)}
            </select>
          </label>
          {binding.lookup && <label className="block space-y-1 text-sm"><span className="text-muted-foreground">Уникальное поле модели</span>
            <select className={selectClass} aria-invalid={!hasCompatibleLookupField} value={lookupFieldID ?? ""} onChange={(event) => setBinding({ ...binding, lookup: { ...binding.lookup!, fieldId: event.currentTarget.value } })}>
              <option value="">Выберите уникальное поле</option>
              {entity.fields.filter((field) => field.unique && stringFieldTypes.has(field.type)).map((field) => <option key={field.id} value={field.id}>{field.name}</option>)}
            </select>
            {!hasCompatibleLookupField && <span className="text-xs text-warning" role="alert">Добавьте уникальное текстовое поле в модель.</span>}
          </label>}
        </fieldset>}
      </section>;
    })}
  </fieldset>;
}
