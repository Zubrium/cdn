import type { DataModel, DataModelEntity, DataModelField, DataModelFieldType, SiteConfig } from "@/types";

export const fieldTypes: Array<{ value: DataModelFieldType; label: string }> = [
  { value: "text", label: "Короткий текст" },
  { value: "longText", label: "Длинный текст" },
  { value: "integer", label: "Целое число" },
  { value: "number", label: "Число" },
  { value: "boolean", label: "Да / нет" },
  { value: "date", label: "Дата" },
  { value: "dateTime", label: "Дата и время" },
  { value: "url", label: "Ссылка" },
  { value: "email", label: "Электронная почта" },
  { value: "enum", label: "Вариант из списка" },
  { value: "media", label: "Медиафайл" },
];

export const entityCardinalities: Array<{ value: NonNullable<DataModelEntity["cardinality"]>; label: string }> = [
  { value: "many", label: "Коллекция" },
  { value: "singleton", label: "Одна запись" },
];

const detailSEOFieldLabels: Record<string, string> = {
  titleFieldId: "заголовок страницы",
  descriptionFieldId: "описание страницы",
  ogTitleFieldId: "заголовок ссылки",
  ogDescriptionFieldId: "описание ссылки",
  ogImageFieldId: "изображение ссылки",
};

type IDFactory = () => string;

function generatedID(): string {
  return crypto.randomUUID();
}

export function emptyDataModel(): DataModel {
  return { formatVersion: 1, entities: [], positions: {} };
}

export function modelFor(config: SiteConfig): DataModel {
  return config.dataModel ?? emptyDataModel();
}

export function validModelName(value: string): boolean {
  return value.trim().length > 0 && Array.from(value).length <= 120;
}

export const DATA_MODEL_ADD_ENTITY_CONTROL_ID = "data-model-add-entity";

export function dataModelEntityNameControlID(entityID: string): string {
  return `data-model-entity-name-${entityID}`;
}

export function dataModelFieldNameControlID(fieldID: string): string {
  return `data-model-field-name-${fieldID}`;
}

export function dataModelAddFieldControlID(entityID: string): string {
  return `data-model-add-field-${entityID}`;
}

export function dataModelEntityRemovalFocusTarget(model: DataModel, entityID: string): string {
  const index = model.entities.findIndex((entity) => entity.id === entityID);
  const survivor = index < 0 ? undefined : model.entities[index + 1] ?? model.entities[index - 1];
  return survivor ? dataModelEntityNameControlID(survivor.id) : DATA_MODEL_ADD_ENTITY_CONTROL_ID;
}

export function dataModelFieldRemovalFocusTarget(entity: DataModelEntity, fieldID: string): string {
  const index = entity.fields.findIndex((field) => field.id === fieldID);
  const survivor = index < 0 ? undefined : entity.fields[index + 1] ?? entity.fields[index - 1];
  return survivor ? dataModelFieldNameControlID(survivor.id) : dataModelAddFieldControlID(entity.id);
}

export function focusDataModelControl(
  id: string,
  resolveControl: (controlID: string) => Pick<HTMLElement, "focus"> & Partial<Pick<HTMLElement, "scrollIntoView">> | null,
): boolean {
  const control = resolveControl(id);
  if (!control) return false;
  control.focus({ preventScroll: true });
  // Keep focus restoration controlled, then scroll only as much as needed to
  // make the target visible inside its nearest scroll area.
  control.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  return true;
}

function generatedKey(prefix: string, id: string, values: Array<{ key: string }>): string {
  // Record values are keyed by field key. Deriving it from the generated ID
  // prevents a deleted field's inherited value from being mistaken for a new
  // field if the editor is used to add another field later.
  const suffix = id.toLowerCase().replace(/[^a-z0-9]+/g, "") || "generated";
  const base = `${prefix}_${suffix}`;
  let key = base;
  let collision = 2;
  while (values.some((item) => item.key === key)) key = `${base}_${collision++}`;
  return key;
}

function defaultPosition(index: number) {
  return { x: 36 + (index % 3) * 360, y: 36 + Math.floor(index / 3) * 310 };
}

export function positionFor(model: DataModel, entity: DataModelEntity, index: number) {
  return model.positions?.[entity.id] ?? defaultPosition(index);
}

export function setModel(config: SiteConfig, model: DataModel): SiteConfig {
  return { ...config, dataModel: model };
}

export function addDataModelEntity(
  model: DataModel,
  idFactory: IDFactory = generatedID,
  name = "Новая модель",
  cardinality: NonNullable<DataModelEntity["cardinality"]> = "many",
): DataModel {
  const id = idFactory();
  const entity: DataModelEntity = {
    id,
    key: generatedKey("model", id, model.entities),
    name: validModelName(name) ? name.trim() : "Новая модель",
    cardinality,
    fields: [],
  };
  return {
    ...model,
    entities: [...model.entities, entity],
    positions: { ...(model.positions ?? {}), [id]: defaultPosition(model.entities.length) },
  };
}

export function updateDataModelEntityCardinality(
  model: DataModel,
  entityID: string,
  cardinality: NonNullable<DataModelEntity["cardinality"]>,
): DataModel {
  return updateDataModelEntity(model, entityID, (entity) => ({ ...entity, cardinality }));
}

export function updateDataModelEntity(
  model: DataModel,
  entityID: string,
  transform: (entity: DataModelEntity) => DataModelEntity,
): DataModel {
  if (!model.entities.some((entity) => entity.id === entityID)) return model;
  return {
    ...model,
    entities: model.entities.map((entity) => entity.id === entityID ? transform(entity) : entity),
  };
}

export function renameDataModelEntity(model: DataModel, entityID: string, name: string): DataModel {
  return updateDataModelEntity(model, entityID, (entity) => ({ ...entity, name }));
}

export function addDataModelField(model: DataModel, entityID: string, idFactory: IDFactory = generatedID): DataModel {
  const entity = model.entities.find((item) => item.id === entityID);
  if (!entity) return model;
  const id = idFactory();
  const field: DataModelField = {
    id,
    key: generatedKey("field", id, entity.fields),
    name: "Новое поле",
    type: "text",
  };
  return updateDataModelEntity(model, entityID, (current) => ({ ...current, fields: [...current.fields, field] }));
}

export function updateDataModelField(
  model: DataModel,
  entityID: string,
  fieldID: string,
  transform: (field: DataModelField) => DataModelField,
): DataModel {
  return updateDataModelEntity(model, entityID, (entity) => ({
    ...entity,
    fields: entity.fields.map((field) => field.id === fieldID ? transform(field) : field),
  }));
}

export function renameDataModelField(model: DataModel, entityID: string, fieldID: string, name: string): DataModel {
  return updateDataModelField(model, entityID, fieldID, (field) => ({ ...field, name }));
}

export function changeDataModelFieldType(
  model: DataModel,
  entityID: string,
  fieldID: string,
  type: DataModelFieldType,
): DataModel {
  return updateDataModelField(model, entityID, fieldID, (field) => {
    const next = {
      ...field,
      type,
      unique: type === "longText" || type === "media" || type === "boolean" ? false : field.unique,
    };
    if (type === "enum") next.options = field.options?.length ? field.options : ["Вариант 1"];
    else delete next.options;
    return next;
  });
}

export function parseDataModelEnumOptions(value: string): string[] {
  return [...new Set(value.split(/\r?\n/).map((option) => option.trim()).filter(Boolean))];
}

export function removeDataModelField(model: DataModel, entityID: string, fieldID: string): DataModel {
  return updateDataModelEntity(model, entityID, (entity) => ({
    ...entity,
    fields: entity.fields.filter((field) => field.id !== fieldID),
  }));
}

export function moveDataModelField(model: DataModel, entityID: string, fieldID: string, offset: -1 | 1): DataModel {
  const entity = model.entities.find((item) => item.id === entityID);
  if (!entity) return model;
  const from = entity.fields.findIndex((field) => field.id === fieldID);
  const to = from + offset;
  if (from < 0 || to < 0 || to >= entity.fields.length) return model;
  const fields = [...entity.fields];
  [fields[from], fields[to]] = [fields[to]!, fields[from]!];
  return updateDataModelEntity(model, entityID, (current) => ({ ...current, fields }));
}

export function reorderDataModelFields(model: DataModel, entityID: string, orderedFieldIDs: string[]): DataModel {
  const entity = model.entities.find((item) => item.id === entityID);
  if (!entity || orderedFieldIDs.length !== entity.fields.length) return model;
  const byID = new Map(entity.fields.map((field) => [field.id, field]));
  const fields = orderedFieldIDs.flatMap((fieldID) => {
    const field = byID.get(fieldID);
    return field ? [field] : [];
  });
  if (fields.length !== entity.fields.length) return model;
  if (fields.every((field, index) => field.id === entity.fields[index]?.id)) return model;
  return updateDataModelEntity(model, entityID, (current) => ({ ...current, fields }));
}

export interface DataModelBindingImpact {
  pageName: string;
  featureKey: string;
  version: string;
  inputKey: string;
  paths: string[];
}

export function dataModelEntityBindingImpacts(config: SiteConfig, entityID: string): DataModelBindingImpact[] {
  const model = config.dataModel ?? emptyDataModel();
  const entityFieldIDs = new Set(model.entities.find((entity) => entity.id === entityID)?.fields.map((field) => field.id) ?? []);
  return (config.pages ?? []).flatMap((page) => page.placements.flatMap((placement) =>
    (placement.dataBindings ?? []).flatMap((binding) => {
      const paths = new Set<string>();
      const rootEntity = binding.entityId === entityID;
      if (rootEntity && binding.fieldId) paths.add("значение");
      if (binding.fieldId && entityFieldIDs.has(binding.fieldId)) paths.add("значение");
      for (const field of binding.fields ?? []) {
        if (entityFieldIDs.has(field.fieldId)) paths.add(field.inputPath);
      }
      if (rootEntity) {
        for (const [slot, fieldID] of Object.entries(binding.detailSEO ?? {})) {
          if (fieldID && entityFieldIDs.has(fieldID)) paths.add(`SEO: ${detailSEOFieldLabels[slot] ?? slot}`);
        }
      }
      if (rootEntity && binding.lookup) paths.add(`маршрут: ${binding.lookup.argument}`);
      if (binding.lookup && entityFieldIDs.has(binding.lookup.fieldId)) paths.add(`маршрут: ${binding.lookup.argument}`);
      if (rootEntity && paths.size === 0) paths.add("сущность целиком");
      if (paths.size === 0) return [];
      return [{
        pageName: page.name || "Без названия",
        featureKey: placement.build.featureKey,
        version: placement.build.version,
        inputKey: binding.inputKey,
        paths: [...paths],
      }];
    }),
  ));
}

export function dataModelFieldBindingImpacts(config: SiteConfig, entityID: string, fieldID: string): DataModelBindingImpact[] {
  const ownsField = config.dataModel?.entities.some((entity) => entity.id === entityID && entity.fields.some((field) => field.id === fieldID)) ?? false;
  if (!ownsField) return [];
  return (config.pages ?? []).flatMap((page) => page.placements.flatMap((placement) =>
    (placement.dataBindings ?? []).flatMap((binding) => {
      const paths = [
        ...(binding.entityId === entityID && binding.fieldId === fieldID ? ["значение"] : []),
        ...(binding.entityId === entityID ? (binding.fields ?? []).filter((field) => field.fieldId === fieldID).map((field) => field.inputPath) : []),
        ...(binding.entityId === entityID ? Object.entries(binding.detailSEO ?? {}).filter(([, boundFieldID]) => boundFieldID === fieldID).map(([slot]) => `SEO: ${detailSEOFieldLabels[slot] ?? slot}`) : []),
        ...(binding.entityId === entityID && binding.lookup?.fieldId === fieldID ? [`маршрут: ${binding.lookup.argument}`] : []),
      ];
      return paths.length === 0 ? [] : [{
        pageName: page.name || "Без названия",
        featureKey: placement.build.featureKey,
        version: placement.build.version,
        inputKey: binding.inputKey,
        paths,
      }];
    }),
  ));
}

export function describeBindingImpacts(impacts: DataModelBindingImpact[]): string {
  if (impacts.length === 0) return "";
  const affected = impacts.map((impact) =>
    `«${impact.pageName}» · ${impact.featureKey}@${impact.version} · ${impact.inputKey} (${impact.paths.join(", ")})`,
  );
  return `\n\nЗатронуты структурные привязки:\n${affected.join("\n")}\n\nПерепривяжите эти входы в редакторе сайта до сохранения дочернего снапшота.`;
}

export function removeDataModelEntity(model: DataModel, entityID: string): DataModel {
  const positions = { ...(model.positions ?? {}) };
  delete positions[entityID];
  return {
    ...model,
    entities: model.entities.filter((entity) => entity.id !== entityID),
    positions,
  };
}

export function setDataModelEntityPosition(model: DataModel, entityID: string, x: number, y: number): DataModel {
  if (!model.entities.some((entity) => entity.id === entityID)) return model;
  const clamp = (value: number) => Number.isFinite(value) ? Math.min(100000, Math.max(0, value)) : 0;
  return {
    ...model,
    positions: { ...(model.positions ?? {}), [entityID]: { x: clamp(x), y: clamp(y) } },
  };
}
