import type { DataModel, DataModelEntity, DataModelField, DataModelFieldType, DataModelRelation, SiteConfig } from "@/types";

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

export const cardinalities: Array<{ value: DataModelRelation["cardinality"]; label: string }> = [
  { value: "one-to-one", label: "Один к одному" },
  { value: "one-to-many", label: "Один ко многим" },
  { value: "many-to-many", label: "Многие ко многим" },
];

export const entityCardinalities: Array<{ value: NonNullable<DataModelEntity["cardinality"]>; label: string }> = [
  { value: "many", label: "Много записей" },
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
  return { formatVersion: 1, entities: [], relations: [], positions: {} };
}

export function modelFor(config: SiteConfig): DataModel {
  return config.dataModel ?? emptyDataModel();
}

export function validModelName(value: string): boolean {
  return value.trim().length > 0 && Array.from(value).length <= 120;
}

export const DATA_MODEL_ADD_ENTITY_CONTROL_ID = "data-model-add-entity";
export const DATA_MODEL_NEW_RELATION_FROM_CONTROL_ID = "data-model-new-relation-from";

export function dataModelEntityNameControlID(entityID: string): string {
  return `data-model-entity-name-${entityID}`;
}

export function dataModelFieldNameControlID(fieldID: string): string {
  return `data-model-field-name-${fieldID}`;
}

export function dataModelAddFieldControlID(entityID: string): string {
  return `data-model-add-field-${entityID}`;
}

export function dataModelRelationNameControlID(relationID: string): string {
  return `data-model-relation-name-${relationID}`;
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

export function dataModelRelationRemovalFocusTarget(model: DataModel, relationID: string): string {
  const relations = model.relations ?? [];
  const index = relations.findIndex((relation) => relation.id === relationID);
  const survivor = index < 0 ? undefined : relations[index + 1] ?? relations[index - 1];
  return survivor ? dataModelRelationNameControlID(survivor.id) : DATA_MODEL_NEW_RELATION_FROM_CONTROL_ID;
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

export function addDataModelEntity(model: DataModel, idFactory: IDFactory = generatedID): DataModel {
  const id = idFactory();
  const entity: DataModelEntity = {
    id,
    key: generatedKey("entity", id, model.entities),
    name: "Новая сущность",
    cardinality: "many",
    fields: [],
  };
  return {
    ...model,
    entities: [...model.entities, entity],
    relations: model.relations ?? [],
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

export function addDataModelRelation(
  model: DataModel,
  relation: Omit<DataModelRelation, "id">,
  idFactory: IDFactory = generatedID,
): DataModel {
  const name = relation.name.trim();
  const hasSource = model.entities.some((entity) => entity.id === relation.fromEntityId);
  const hasTarget = model.entities.some((entity) => entity.id === relation.toEntityId);
  if (!name || !hasSource || !hasTarget || findDataModelRelationConflict(model, { ...relation, name })) return model;
  return {
    ...model,
    relations: [...(model.relations ?? []), { ...relation, id: idFactory(), name }],
  };
}

export function findDataModelRelationConflict(
  model: DataModel,
  relation: Omit<DataModelRelation, "id">,
  ignoreRelationID?: string,
): DataModelRelation | undefined {
  const name = relation.name.trim().toLocaleLowerCase();
  if (!name) return undefined;
  return (model.relations ?? []).find((existing) =>
    existing.id !== ignoreRelationID &&
    existing.fromEntityId === relation.fromEntityId &&
    existing.toEntityId === relation.toEntityId &&
    existing.name.trim().toLocaleLowerCase() === name,
  );
}

export function updateDataModelRelation(
  model: DataModel,
  relationID: string,
  transform: (relation: DataModelRelation) => DataModelRelation,
): DataModel {
  return {
    ...model,
    relations: (model.relations ?? []).map((relation) => relation.id === relationID ? transform(relation) : relation),
  };
}

export function renameDataModelRelation(model: DataModel, relationID: string, name: string): DataModel {
  return updateDataModelRelation(model, relationID, (relation) => ({ ...relation, name }));
}

export function dataModelRelationBindingImpacts(config: SiteConfig, relationID: string): DataModelBindingImpact[] {
  return (config.pages ?? []).flatMap((page) => page.placements.flatMap((placement) =>
    (placement.dataBindings ?? []).flatMap((binding) => {
      const paths = [
        ...(binding.relationIds?.includes(relationID) ? ["значение"] : []),
        ...(binding.fields ?? []).filter((field) => field.relationIds?.includes(relationID)).map((field) => field.inputPath),
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

export type DataModelDrag = { id: string; pointerId: number; x: number; y: number; originX: number; originY: number };

export function finalDataModelDragPosition(
  drag: DataModelDrag | null,
  preview: { id: string; x: number; y: number } | null,
  pointerID: number,
  cancelled = false,
): { id: string; x: number; y: number } | undefined {
  if (cancelled || !drag || drag.pointerId !== pointerID || preview?.id !== drag.id) return undefined;
  return { id: preview.id, x: preview.x, y: preview.y };
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
  const removedRelationIDs = new Set((model.relations ?? [])
    .filter((relation) => relation.fromEntityId === entityID || relation.toEntityId === entityID)
    .map((relation) => relation.id));
  return (config.pages ?? []).flatMap((page) => page.placements.flatMap((placement) =>
    (placement.dataBindings ?? []).flatMap((binding) => {
      const paths = new Set<string>();
      const rootEntity = binding.entityId === entityID;
      if (rootEntity && binding.fieldId) paths.add("значение");
      if (binding.fieldId && entityFieldIDs.has(binding.fieldId)) paths.add("значение");
      if (binding.fieldId && (binding.relationIds ?? []).some((id) => removedRelationIDs.has(id))) paths.add("значение");
      for (const field of binding.fields ?? []) {
        if (entityFieldIDs.has(field.fieldId) || field.relationIds?.some((id) => removedRelationIDs.has(id))) paths.add(field.inputPath);
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
  const relationTargetEntity = (sourceEntityID: string, relationIDs: string[] = []): string | null => {
    let currentEntityID = sourceEntityID;
    for (const relationID of relationIDs) {
      const relation = config.dataModel?.relations?.find((item) => item.id === relationID);
      if (!relation || relation.fromEntityId !== currentEntityID) return null;
      currentEntityID = relation.toEntityId;
    }
    return currentEntityID;
  };
  return (config.pages ?? []).flatMap((page) => page.placements.flatMap((placement) =>
    (placement.dataBindings ?? []).flatMap((binding) => {
      const paths = [
        ...(binding.entityId === entityID && binding.fieldId === fieldID ? ["значение"] : []),
        ...(binding.fields ?? []).filter((field) => field.fieldId === fieldID && relationTargetEntity(binding.entityId, field.relationIds) === entityID).map((field) => field.inputPath),
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

export function removeDataModelRelation(model: DataModel, relationID: string): DataModel {
  return { ...model, relations: (model.relations ?? []).filter((relation) => relation.id !== relationID) };
}

export function removeDataModelEntity(model: DataModel, entityID: string): DataModel {
  const positions = { ...(model.positions ?? {}) };
  delete positions[entityID];
  return {
    ...model,
    entities: model.entities.filter((entity) => entity.id !== entityID),
    relations: (model.relations ?? []).filter((relation) => relation.fromEntityId !== entityID && relation.toEntityId !== entityID),
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
