import { Link } from "react-router-dom";
import type { DataInputBinding, DataInputField, DataInputFieldBinding, DataInputPort, DataModel, DataModelField, FeatureArgumentBinding } from "@/types";

type Props = {
  ports: DataInputPort[];
  dataModel?: DataModel;
  bindings: DataInputBinding[];
  arguments?: Record<string, FeatureArgumentBinding>;
  routeCaptures: string[];
  snapshotId: string;
  onChange: (bindings: DataInputBinding[]) => void;
};

const stringFieldTypes = new Set(["text", "longText", "date", "dateTime", "url", "email", "enum"]);

type RelationFieldChoice = {
  field: DataModelField;
  entity: DataModel["entities"][number];
  relationIds: string[];
  multiTargetCount: number;
  label: string;
};

function dataInputWildcardCount(path: string): number {
  return path.split("/").slice(1).filter((segment) => segment === "*").length;
}

function fieldChoiceValue(fieldId: string, relationIds: string[] = []): string {
  return `${relationIds.join(",")}|${fieldId}`;
}

function fieldChoiceLabel(model: DataModel, rootID: string, relationIds: string[], field: DataModelField): string {
  const entities = new Map(model.entities.map((entity) => [entity.id, entity]));
  const relations = new Map((model.relations ?? []).map((relation) => [relation.id, relation]));
  const root = entities.get(rootID);
  const parts = [root?.name ?? "Сущность"];
  let currentEntityID = rootID;
  for (const relationID of relationIds) {
    const relation = relations.get(relationID);
    const target = relation ? entities.get(relation.toEntityId) : undefined;
    if (!relation || !target || relation.fromEntityId !== currentEntityID) continue;
    parts.push(`связь «${relation.name}»`, target.name);
    currentEntityID = target.id;
  }
  parts.push(field.name);
  return parts.join(" → ");
}

function reachableRelationFields(model: DataModel, rootEntityID: string): RelationFieldChoice[] {
  const entities = new Map(model.entities.map((entity) => [entity.id, entity]));
  const relations = model.relations ?? [];
  const root = entities.get(rootEntityID);
  if (!root) return [];

  const choices: RelationFieldChoice[] = [];
  const visit = (entity: DataModel["entities"][number], relationIds: string[], multiTargetCount: number, visited: Set<string>) => {
    for (const field of entity.fields) {
      choices.push({
        field,
        entity,
        relationIds,
        multiTargetCount,
        label: fieldChoiceLabel(model, rootEntityID, relationIds, field),
      });
    }
    if (visited.has(entity.id)) return;
    const nextVisited = new Set(visited).add(entity.id);
    for (const relation of relations) {
      if (relation.fromEntityId !== entity.id) continue;
      const target = entities.get(relation.toEntityId);
      if (!target) continue;
      visit(
        target,
        [...relationIds, relation.id],
        multiTargetCount + (relation.cardinality === "one-to-one" ? 0 : 1),
        nextVisited,
      );
    }
  };
  visit(root, [], 0, new Set());
  return choices;
}

function compatibleInputType(declared: { type?: DataModelField["type"]; values?: string[] }, field: DataModelField): boolean {
  if (field.type !== declared.type) return false;
  if (field.type !== "enum") return true;
  const allowedValues = new Set(declared.values ?? []);
  return (field.options ?? []).every((option) => allowedValues.has(option));
}

export function compatibleScalarFieldChoices(model: DataModel, rootEntityID: string, port: DataInputPort): RelationFieldChoice[] {
  if (port.kind !== "scalar" || !port.type) return [];
  return reachableRelationFields(model, rootEntityID).filter((choice) =>
    choice.multiTargetCount === 0 && compatibleInputType(port, choice.field),
  );
}

function compatibleScalarFieldsIgnoringCardinality(model: DataModel, rootEntityID: string, port: DataInputPort): RelationFieldChoice[] {
  if (port.kind !== "scalar" || !port.type) return [];
  return reachableRelationFields(model, rootEntityID).filter((choice) => compatibleInputType(port, choice.field));
}

export function scalarRelationFieldBinding(choice: RelationFieldChoice): Pick<DataInputBinding, "fieldId" | "relationIds"> {
  return { fieldId: choice.field.id, relationIds: [...choice.relationIds] };
}

export function compatibleRelationFieldChoices(model: DataModel, rootEntityID: string, declared: DataInputField): RelationFieldChoice[] {
  const wildcards = dataInputWildcardCount(declared.path);
  return reachableRelationFields(model, rootEntityID).filter((choice) =>
    compatibleInputType(declared, choice.field) && choice.multiTargetCount === wildcards,
  );
}

export function relationFieldBinding(inputPath: string, choice: RelationFieldChoice): DataInputFieldBinding {
  return { inputPath, fieldId: choice.field.id, relationIds: [...choice.relationIds] };
}

function compatibleRelationFieldsIgnoringCardinality(model: DataModel, rootEntityID: string, declared: DataInputField): RelationFieldChoice[] {
  return reachableRelationFields(model, rootEntityID).filter((choice) => compatibleInputType(declared, choice.field));
}

function readableInputPath(portKey: string, path: string): string {
  const segments = path.split("/").slice(1).map((segment) => segment.replaceAll("~1", "/").replaceAll("~0", "~"));
  return [portKey, ...segments.map((segment) => segment === "*" ? "элемент списка" : segment)].join(" → ");
}

function sameRelationPath(left: string[] | undefined, right: string[]): boolean {
  const first = left ?? [];
  return first.length === right.length && first.every((relationID, index) => relationID === right[index]);
}

function bindingFor(bindings: DataInputBinding[], key: string): DataInputBinding | undefined {
  return bindings.find((binding) => binding.inputKey === key);
}

export function DataInputBindingsEditor({ ports, dataModel, bindings, arguments: argumentBindings, routeCaptures, snapshotId, onChange }: Props) {
  const entities = dataModel?.entities ?? [];
  const pathArguments = Object.entries(argumentBindings ?? {}).filter(([, binding]) => binding.source === "path" && binding.pathArg && routeCaptures.includes(binding.pathArg));

  const setBinding = (next: DataInputBinding) => onChange([...bindings.filter((binding) => binding.inputKey !== next.inputKey), next]);

  return <fieldset className="space-y-3 border-t border-border pt-3">
    <legend className="px-1 text-sm font-medium">Подключение данных модели</legend>
    <p className="text-xs text-muted-foreground">Здесь задаётся только связь входа фичи с моделью. Значения записей и выбор товаров для конкретного размещения настраиваются в редакторе контента.</p>
    {entities.length === 0 && <p className="rounded-md border border-dashed border-border p-3 text-sm text-muted-foreground">
      Сначала создайте сущности и поля модели данных в <Link className="underline underline-offset-4" to={`/site/data-model${snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : ""}`}>редакторе модели</Link>.
    </p>}
    {ports.map((port) => {
      const binding = bindingFor(bindings, port.key);
      const entity = entities.find((item) => item.id === binding?.entityId);
      const lookupFieldID = binding?.lookup?.fieldId;
      const hasUniqueLookupField = entity?.fields.some((field) => field.unique && stringFieldTypes.has(field.type)) ?? false;
      const hasCompatibleLookupField = entity?.fields.some((field) => field.id === lookupFieldID && field.unique && stringFieldTypes.has(field.type)) ?? false;
      const declaredFields = port.fields ?? [];
      const controlID = `data-input-${port.key}`;
      const entityErrorID = `${controlID}-entity-error`;
      const invalidEntity = entities.length > 0 && !entity;
      const selectClass = "h-11 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";
      const scalarCandidates = binding && entity && dataModel && port.kind === "scalar"
        ? compatibleScalarFieldChoices(dataModel, entity.id, port)
        : [];
      const scalarTypeCandidates = binding && entity && dataModel && port.kind === "scalar"
        ? compatibleScalarFieldsIgnoringCardinality(dataModel, entity.id, port)
        : [];
      const selectedScalarChoice = scalarCandidates.find((choice) =>
        choice.field.id === binding?.fieldId && sameRelationPath(binding.relationIds, choice.relationIds),
      );
      return <section key={port.key} className="space-y-3 rounded-md border border-border p-3" aria-labelledby={`${controlID}-title`}>
        <div>
          <h4 id={`${controlID}-title`} className="text-sm font-medium">{port.key}</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">{port.kind === "collection" ? "Коллекция записей" : port.kind === "record" ? "Одна запись" : `Одно поле · ${port.type ?? ""}`}</p>
        </div>
        <label className="block space-y-1.5 text-sm">
          <span className="text-muted-foreground">Сущность модели</span>
          <select id={`${controlID}-entity`} className={selectClass} aria-invalid={invalidEntity} aria-describedby={entityErrorID} required value={binding?.entityId ?? ""} disabled={!entities.length} onChange={(event) => { const entityId = event.currentTarget.value; setBinding({ inputKey: port.key, entityId, ...(port.kind === "record" && binding?.lookup ? { lookup: binding.lookup } : {}) }); }}>
            <option value="">Выберите сущность</option>
            {entities.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.key}</option>)}
          </select>
          <span id={entityErrorID} className={invalidEntity ? "block text-xs text-warning" : "sr-only"} role={invalidEntity ? "alert" : undefined}>
            {invalidEntity ? (binding?.entityId ? "Сохранённая сущность больше не существует в этой модели. Выберите доступную сущность." : "Выберите сущность для этого входа.") : ""}
          </span>
        </label>
        {binding && entity && (port.kind === "scalar" ? <label className="block space-y-1.5 text-sm">
          <span className="text-muted-foreground">Поле модели · {port.type}</span>
          <select aria-label={`Поле модели для scalar ${port.key}`} className={selectClass} aria-invalid={!selectedScalarChoice} aria-describedby={`${controlID}-scalar-field-error`} value={selectedScalarChoice ? fieldChoiceValue(selectedScalarChoice.field.id, selectedScalarChoice.relationIds) : ""} onChange={(event) => {
            const value = event.currentTarget.value;
            const selected = scalarCandidates.find((choice) => fieldChoiceValue(choice.field.id, choice.relationIds) === value);
            if (selected) {
              setBinding({ ...binding, ...scalarRelationFieldBinding(selected), fields: undefined });
            } else {
              setBinding({ ...binding, fieldId: undefined, relationIds: undefined, fields: undefined });
            }
          }}>
            <option value="">Выберите поле</option>
            {scalarCandidates.map((choice) => <option key={fieldChoiceValue(choice.field.id, choice.relationIds)} value={fieldChoiceValue(choice.field.id, choice.relationIds)}>{choice.label}</option>)}
          </select>
          <span id={`${controlID}-scalar-field-error`} className={!selectedScalarChoice ? "block text-xs text-warning" : "sr-only"} role={!selectedScalarChoice ? "alert" : undefined}>
            {!selectedScalarChoice ? binding.fieldId
              ? "Сохранённый путь поля больше не подходит. Выберите поле в корневой сущности или по цепочке связей 1:1."
              : scalarCandidates.length > 0
                ? "Выберите поле для scalar-входа."
                : scalarTypeCandidates.length > 0
                  ? "Scalar-вход поддерживает только поля корневой сущности и связи 1:1. В связях со списком полей scalar-типа нет."
                  : "Нет полей совместимого типа в корневой сущности или её направленных связях 1:1. Добавьте поле либо связь в модели данных."
              : ""}
          </span>
        </label> : <div className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground">Сопоставление полей</p>
          {declaredFields.map((declared, fieldIndex) => {
            const mapping = binding.fields?.find((item) => item.inputPath === declared.path);
            const typeCandidates = dataModel ? compatibleRelationFieldsIgnoringCardinality(dataModel, entity.id, declared) : [];
            const candidates = dataModel ? compatibleRelationFieldChoices(dataModel, entity.id, declared) : [];
            const selectedChoice = candidates.find((choice) => choice.field.id === mapping?.fieldId && sameRelationPath(mapping.relationIds, choice.relationIds));
            const wildcardCount = dataInputWildcardCount(declared.path);
            const inputLabel = readableInputPath(port.key, declared.path);
            const mappingErrorID = `${controlID}-mapping-${fieldIndex}-error`;
            return <label key={declared.path} className="grid gap-1.5 text-sm sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] sm:items-center">
              <span className="min-w-0"><span className="block truncate font-medium">{inputLabel}</span><span className="text-xs text-muted-foreground">{declared.type}</span></span>
              <select aria-label={`Поле модели для ${inputLabel}`} className={selectClass} aria-invalid={!selectedChoice} aria-describedby={mappingErrorID} value={selectedChoice ? fieldChoiceValue(selectedChoice.field.id, selectedChoice.relationIds) : ""} onChange={(event) => {
                const value = event.currentTarget.value;
                const nextFields = (binding.fields ?? []).filter((item) => item.inputPath !== declared.path);
                const selected = candidates.find((choice) => fieldChoiceValue(choice.field.id, choice.relationIds) === value);
                if (selected) nextFields.push(relationFieldBinding(declared.path, selected));
                setBinding({ ...binding, fieldId: undefined, fields: nextFields });
              }}>
                <option value="">Выберите поле</option>
                {candidates.map((choice) => <option key={fieldChoiceValue(choice.field.id, choice.relationIds)} value={fieldChoiceValue(choice.field.id, choice.relationIds)}>{choice.label}</option>)}
              </select>
              <span id={mappingErrorID} className={`col-span-full text-xs ${!selectedChoice ? "text-warning" : "sr-only"}`} role={!selectedChoice ? "alert" : undefined}>
                {!selectedChoice ? mapping
                  ? "Это сопоставление больше не подходит модели. Проверьте тип поля, направление связи и количество связей со множественными записями."
                  : candidates.length > 0
                    ? "Выберите совместимое поле модели для этого входа."
                    : typeCandidates.length > 0
                      ? `Для этого входа нужно ${wildcardCount} связей со множественными записями; выберите путь с совместимой структурой.`
                      : "Нет совместимых полей по направленным связям от выбранной сущности. Проверьте тип входа и связи модели."
                  : ""}
              </span>
            </label>;
          })}
          {declaredFields.some((declared) => (dataModel ? compatibleRelationFieldChoices(dataModel, entity.id, declared).length : 0) === 0) && <p className="text-xs text-muted-foreground">Выбирайте поле по его типу и пути связей. Направление каждой связи видно в вариантах выбора.</p>}
        </div>)}
        {binding && entity && port.kind === "record" && <fieldset className="space-y-2 rounded-md bg-muted/30 p-3">
          <legend className="px-1 text-xs font-medium">Запись из параметра маршрута (необязательно)</legend>
          <p className="text-xs text-muted-foreground">Без этого сопоставления запись выбирается в редакторе контента. Для detail-страницы выберите аргумент, связанный с параметром пути, и уникальное поле модели.</p>
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
              <option value="">Выбирать запись в /content</option>
              {pathArguments.map(([key, value]) => <option key={key} value={key}>{key} ← {value.pathArg}</option>)}
            </select>
          </label>
          {binding.lookup && <label className="block space-y-1 text-sm"><span className="text-muted-foreground">Уникальное поле модели</span>
            <select className={selectClass} aria-invalid={!hasCompatibleLookupField} aria-describedby={`${controlID}-lookup-field-error`} value={lookupFieldID ?? ""} onChange={(event) => { const fieldId = event.currentTarget.value; setBinding({ ...binding, lookup: { ...binding.lookup!, fieldId } }); }}>
              <option value="">Выберите уникальное поле</option>
              {entity.fields.filter((field) => field.unique && stringFieldTypes.has(field.type)).map((field) => <option key={field.id} value={field.id}>{field.name} · {field.key}</option>)}
            </select>
            <span id={`${controlID}-lookup-field-error`} className={!hasCompatibleLookupField ? "block text-xs text-warning" : "sr-only"} role={!hasCompatibleLookupField ? "alert" : undefined}>{!hasCompatibleLookupField ? hasUniqueLookupField ? "Сохранённое поле маршрута больше не является уникальным и совместимым." : "Добавьте уникальное текстовое поле в модели данных." : ""}</span>
          </label>}
        </fieldset>}
      </section>;
    })}
  </fieldset>;
}
