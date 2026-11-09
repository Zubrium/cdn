import type {
  DataModelEntity,
  DataModelField,
  DataModelRelation,
  DataModel,
  JsonValue,
  ModelContentRecord,
  ModelContentSelection,
  ModelContentWorkspace,
} from "@/types";
import type { SchemaFieldNode } from "@/domains/content-schema/types";

const FIELD_TYPE_MAP: Record<DataModelField["type"], SchemaFieldNode["type"]> = {
  text: "string",
  longText: "text",
  integer: "integer",
  number: "number",
  boolean: "boolean",
  date: "date",
  dateTime: "dateTime",
  url: "string",
  email: "string",
  enum: "enum",
  media: "media",
};

export function modelEntityFields(entity: DataModelEntity): SchemaFieldNode[] {
  return entity.fields.map((field) => ({
    id: field.id,
    key: field.key,
    label: field.name,
    type: FIELD_TYPE_MAP[field.type],
    description: "",
    required: false,
    nullable: false,
    isPrivate: false,
    placeholder: "",
    defaultValue: "",
    ...(field.type === "url" ? { stringFormat: "url" } : {}),
    ...(field.type === "email" ? { stringFormat: "email" } : {}),
    enumValues: (field.options ?? []).map((value, index) => ({
      id: `${field.id}-${index}`,
      value,
      label: value,
    })),
    children: [],
    itemType: "string",
    allowedBlocks: [],
  }));
}

export function modelRecordTitle(record: ModelContentRecord, entity: DataModelEntity): string {
  const readableFields = entity.fields.filter((item) => item.type !== "media");
  const preferredFields = readableFields.filter((field) => /(?:title|name|label|heading|caption|назван\p{L}*|имя|заголов\p{L}*|наименован\p{L}*)/iu.test(`${field.key} ${field.name}`));
  const preferredIDs = new Set(preferredFields.map((field) => field.id));
  for (const field of [...preferredFields, ...readableFields.filter((item) => !preferredIDs.has(item.id))]) {
    const value = record.values[field.key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return `Запись ${record.sortOrder + 1}`;
}

export function selectedModelRecord(
  entity: DataModelEntity | null,
  recordsForEntity: ModelContentRecord[],
  recordId: string,
): ModelContentRecord | null {
  if (!entity) return null;
  if (entity.cardinality === "singleton") return recordsForEntity.length === 1 ? recordsForEntity[0] ?? null : null;
  return recordsForEntity.find((record) => record.id === recordId) ?? null;
}

export function createModelRecord(entityId: string, sortOrder: number): ModelContentRecord {
  // This key is local to the unsaved editor row; it is stripped from PUT so
  // the service can generate the canonical record UUID.
  return { id: `local:${crypto.randomUUID()}`, entityId, sortOrder, values: {}, relations: {} };
}

export function clearModelRecordData(records: ModelContentRecord[], recordId: string): ModelContentRecord[] {
  return records.map((record) => record.id === recordId
    ? { ...record, values: {}, relations: {} }
    : record);
}

export function reorderModelRecords(
  records: ModelContentRecord[],
  entityId: string,
  recordId: string,
  direction: -1 | 1,
): ModelContentRecord[] {
  const entityRecords = records.filter((record) => record.entityId === entityId).sort((a, b) => a.sortOrder - b.sortOrder);
  const index = entityRecords.findIndex((record) => record.id === recordId);
  const nextIndex = index + direction;
  if (index < 0 || nextIndex < 0 || nextIndex >= entityRecords.length) return records;
  [entityRecords[index], entityRecords[nextIndex]] = [entityRecords[nextIndex]!, entityRecords[index]!];
  const orders = new Map(entityRecords.map((record, sortOrder) => [record.id, sortOrder]));
  return records.map((record) => orders.has(record.id) ? { ...record, sortOrder: orders.get(record.id)! } : record);
}

export function setModelContentSelection(
  selections: ModelContentSelection[],
  placementId: string,
  inputKey: string,
  mode: "all" | "ids",
  recordIds: string[] = [],
): ModelContentSelection[] {
  const next = selections.filter((item) => item.placementId !== placementId || item.inputKey !== inputKey);
  // An absent record selection means the runtime may resolve it from the route.
  next.push({ placementId, inputKey, mode, recordIds: mode === "ids" ? [...new Set(recordIds)] : [] });
  return next;
}

export function removeModelContentSelection(
  selections: ModelContentSelection[],
  placementId: string,
  inputKey: string,
): ModelContentSelection[] {
  return selections.filter((item) => item.placementId !== placementId || item.inputKey !== inputKey);
}

export function orphanedModelContentSelections(
  selections: ModelContentSelection[],
  placements: ModelContentWorkspace["placements"],
): ModelContentSelection[] {
  const availableInputs = new Set(placements.flatMap((placement) =>
    placement.inputs.map((input) => `${placement.id}\u0000${input.inputKey}`),
  ));
  const seen = new Set<string>();
  return selections.filter((selection) => {
    const key = `${selection.placementId}\u0000${selection.inputKey}`;
    if (!availableInputs.has(key) || seen.has(key)) return true;
    seen.add(key);
    return false;
  });
}

export function cleanOrphanedModelContentSelections(
  selections: ModelContentSelection[],
  placements: ModelContentWorkspace["placements"],
): ModelContentSelection[] {
  const availableInputs = new Set(placements.flatMap((placement) =>
    placement.inputs.map((input) => `${placement.id}\u0000${input.inputKey}`),
  ));
  const seen = new Set<string>();
  return selections.filter((selection) => {
    const key = `${selection.placementId}\u0000${selection.inputKey}`;
    if (!availableInputs.has(key) || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export interface ModelRecordDeletionImpact {
  incomingRelations: number;
  outgoingRelations: number;
  placementSelections: number;
  affectedFeatureInputs: number;
  mediaReferences: number;
}

export function modelRecordDeletionImpact(
  records: ModelContentRecord[],
  selections: ModelContentSelection[],
  model: DataModel,
  recordId: string,
  placements: ModelContentWorkspace["placements"] = [],
): ModelRecordDeletionImpact {
  const target = records.find((record) => record.id === recordId);
  if (!target) return { incomingRelations: 0, outgoingRelations: 0, placementSelections: 0, affectedFeatureInputs: 0, mediaReferences: 0 };
  const entity = model.entities.find((item) => item.id === target.entityId);
  const incomingRelations = records.reduce((total, record) => total + Object.values(record.relations ?? {})
    .reduce((count, ids) => count + ids.filter((id) => id === recordId).length, 0), 0);
  const outgoingRelations = Object.values(target.relations ?? {})
    .reduce((count, ids) => count + ids.length, 0);
  const placementSelections = selections.reduce((total, selection) =>
    total + (selection.recordIds ?? []).filter((id) => id === recordId).length, 0);
  const selectionByInput = new Map(selections.map((selection) => [`${selection.placementId}\u0000${selection.inputKey}`, selection]));
  const affectedFeatureInputs = placements.reduce((total, placement) => total + placement.inputs.filter((input) => {
    if (input.entityId !== target.entityId) return false;
    const selection = selectionByInput.get(`${placement.id}\u0000${input.inputKey}`);
    if (input.lookup) return true;
    if (!selection) return input.kind === "collection";
    if (selection.mode === "all") return input.kind === "collection";
    return (selection.recordIds ?? []).includes(recordId);
  }).length, 0);
  const mediaReferences = entity?.fields.filter((field) => field.type === "media"
    && typeof target.values[field.key] === "string"
    && Boolean((target.values[field.key] as string).trim())).length ?? 0;
  return { incomingRelations, outgoingRelations, placementSelections, affectedFeatureInputs, mediaReferences };
}

export interface UniqueModelValueIssue {
  recordId: string;
  fieldKey: string;
  message: string;
}

export function duplicateUniqueModelValueIssues(
  records: ModelContentRecord[],
  model: DataModel,
): UniqueModelValueIssue[] {
  const issues = new Map<string, UniqueModelValueIssue>();
  for (const entity of model.entities) {
    for (const field of entity.fields.filter((item) => item.unique)) {
      const values = new Map<string, string[]>();
      for (const record of records) {
        if (record.entityId !== entity.id) continue;
        const value = record.values[field.key];
        if (value === undefined || value === null || value === "") continue;
        let key: string;
        if (field.type === "integer" || field.type === "number") {
          if (typeof value !== "number" || !Number.isFinite(value)) continue;
          key = `number:${Object.is(value, -0) ? "-0" : value}`;
        } else {
          if (typeof value !== "string") continue;
          key = `string:${value}`;
        }
        const matchingRecords = values.get(key) ?? [];
        if (matchingRecords.length > 0) {
          for (const duplicateID of [...matchingRecords, record.id]) {
            issues.set(`${duplicateID}\u0000${field.key}`, {
              recordId: duplicateID,
              fieldKey: field.key,
              message: "Значение должно быть уникальным в этой сущности.",
            });
          }
        }
        matchingRecords.push(record.id);
        values.set(key, matchingRecords);
      }
    }
  }
  return [...issues.values()];
}

export interface ModelContentRelationIssue {
  recordId: string;
  relationId: string;
  message: string;
}

export function modelContentRelationIssues(
  records: ModelContentRecord[],
  model: DataModel,
): ModelContentRelationIssue[] {
  const relations = new Map((model.relations ?? []).map((relation) => [relation.id, relation]));
  const recordsByID = new Map(records.map((record) => [record.id, record]));
  const issues = new Map<string, ModelContentRelationIssue>();
  const owners = new Map<string, Set<string>>();
  const addIssue = (recordId: string, relationId: string, message: string) => {
    issues.set(`${recordId}\u0000${relationId}\u0000${message}`, { recordId, relationId, message });
  };

  for (const record of records) {
    for (const [relationId, targetIDs] of Object.entries(record.relations ?? {})) {
      const relation = relations.get(relationId);
      if (!relation || relation.fromEntityId !== record.entityId || relation.cardinality === "many-to-many") continue;
      for (const targetID of targetIDs) {
        const key = `${relationId}\u0000${targetID}`;
        const targetOwners = owners.get(key) ?? new Set<string>();
        targetOwners.add(record.id);
        owners.set(key, targetOwners);
      }
    }
  }

  for (const record of records) {
    for (const [relationId, targetIDs] of Object.entries(record.relations ?? {})) {
      const relation = relations.get(relationId);
      if (!relation || relation.fromEntityId !== record.entityId) continue;
      if (relation.cardinality === "one-to-one" && targetIDs.length > 1) {
        addIssue(record.id, relationId, "Для связи 1:1 выбрана более чем одна запись.");
      }
      const seenTargets = new Set<string>();
      for (const targetID of targetIDs) {
        if (seenTargets.has(targetID)) addIssue(record.id, relationId, "Одна запись выбрана несколько раз.");
        seenTargets.add(targetID);
        const target = recordsByID.get(targetID);
        if (!target) {
          addIssue(record.id, relationId, "Связанная запись не найдена.");
          continue;
        }
        if (target.entityId !== relation.toEntityId) {
          addIssue(record.id, relationId, "Связь указывает на запись другой сущности.");
          continue;
        }
        if (relation.cardinality === "many-to-many") continue;
        const targetOwners = owners.get(`${relationId}\u0000${targetID}`);
        if (targetOwners && targetOwners.size > 1) {
          addIssue(record.id, relationId, "Запись уже связана с другой исходной записью.");
          for (const ownerID of targetOwners) {
            addIssue(ownerID, relationId, "Запись уже связана с другой исходной записью.");
          }
        }
      }
    }
  }
  return [...issues.values()];
}

export function reorderSelectedRecords(recordIds: string[], recordId: string, direction: -1 | 1): string[] {
  const next = [...recordIds];
  const index = next.indexOf(recordId);
  const nextIndex = index + direction;
  if (index < 0 || nextIndex < 0 || nextIndex >= next.length) return recordIds;
  [next[index], next[nextIndex]] = [next[nextIndex]!, next[index]!];
  return next;
}

export function deleteModelRecord(
  records: ModelContentRecord[],
  selections: ModelContentSelection[],
  _model: { relations?: DataModelRelation[] },
  recordId: string,
): { records: ModelContentRecord[]; selections: ModelContentSelection[] } {
  const deleted = records.find((record) => record.id === recordId);
  if (!deleted) return { records, selections };
  const remaining = records.filter((record) => record.id !== recordId);
  const orderByID = new Map<string, number>();
  for (const entityID of new Set(remaining.map((record) => record.entityId))) {
    remaining
      .filter((record) => record.entityId === entityID)
      .sort((left, right) => left.sortOrder - right.sortOrder)
      .forEach((record, sortOrder) => orderByID.set(record.id, sortOrder));
  }
  return {
    records: remaining.map((record) => {
      const relations = { ...(record.relations ?? {}) };
      let changed = record.sortOrder !== orderByID.get(record.id);
      for (const relationId of Object.keys(relations)) {
        const ids = relations[relationId];
        if (!ids?.includes(recordId)) continue;
        relations[relationId] = ids.filter((id) => id !== recordId);
        changed = true;
      }
      return changed ? { ...record, sortOrder: orderByID.get(record.id) ?? record.sortOrder, relations } : record;
    }),
    selections: selections.map((selection) => (selection.recordIds ?? []).includes(recordId)
      ? { ...selection, recordIds: (selection.recordIds ?? []).filter((id) => id !== recordId) }
      : selection),
  };
}

export interface OrphanedModelRecordData {
  unknownFieldKeys: string[];
  unknownRelationIds: string[];
}

export function orphanedModelRecordData(record: ModelContentRecord, model: DataModel): OrphanedModelRecordData {
  const entity = model.entities.find((item) => item.id === record.entityId);
  if (!entity) return { unknownFieldKeys: Object.keys(record.values), unknownRelationIds: Object.keys(record.relations ?? {}) };
  const fieldKeys = new Set(entity.fields.map((field) => field.key));
  const relationIds = new Set((model.relations ?? [])
    .filter((relation) => relation.fromEntityId === entity.id)
    .map((relation) => relation.id));
  return {
    unknownFieldKeys: Object.keys(record.values).filter((key) => !fieldKeys.has(key)),
    unknownRelationIds: Object.keys(record.relations ?? {}).filter((id) => !relationIds.has(id)),
  };
}

export function removeOrphanedModelRecordData(
  records: ModelContentRecord[],
  model: DataModel,
  recordId: string,
): ModelContentRecord[] {
  const record = records.find((item) => item.id === recordId);
  if (!record) return records;
  const entity = model.entities.find((item) => item.id === record.entityId);
  if (!entity) return records;
  const orphans = orphanedModelRecordData(record, model);
  const invalidFields = new Set(orphans.unknownFieldKeys);
  const invalidRelations = new Set(orphans.unknownRelationIds);
  return records.map((item) => {
    if (item.id !== recordId) return item;
    const values = Object.fromEntries(Object.entries(item.values).filter(([key]) => !invalidFields.has(key)));
    const relations = Object.fromEntries(Object.entries(item.relations ?? {}).filter(([id]) => !invalidRelations.has(id)));
    return { ...item, values, relations };
  });
}

export function updateModelRecordField(
  records: ModelContentRecord[],
  recordId: string,
  fieldKey: string,
  value: JsonValue | undefined,
): ModelContentRecord[] {
  return records.map((record) => {
    if (record.id !== recordId) return record;
    const values = { ...record.values };
    if (value === null || value === "" || value === undefined) delete values[fieldKey];
    else values[fieldKey] = value;
    return { ...record, values };
  });
}

export function updateModelRecordRelation(
  records: ModelContentRecord[],
  recordId: string,
  relationId: string,
  recordIds: string[],
): ModelContentRecord[] {
  return records.map((record) => record.id === recordId
    ? { ...record, relations: { ...(record.relations ?? {}), [relationId]: [...new Set(recordIds)] } }
    : record);
}

export function modelRecordsEqual(left: ModelContentRecord[], right: ModelContentRecord[]): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function modelSelectionsEqual(left: ModelContentSelection[], right: ModelContentSelection[]): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function modelDraftHasRevisionConflict(
  localBaseRevision: number,
  serverRevision: number,
  local: Pick<ModelContentWorkspace, "records" | "selections">,
  server: Pick<ModelContentWorkspace, "records" | "selections">,
): boolean {
  return localBaseRevision !== serverRevision
    && (!modelRecordsEqual(local.records, server.records) || !modelSelectionsEqual(local.selections, server.selections));
}

export function apiModelRecordInput(record: ModelContentRecord): Omit<ModelContentRecord, "id"> | ModelContentRecord {
  if (record.id.startsWith("local:")) {
    return {
      entityId: record.entityId,
      sortOrder: record.sortOrder,
      values: record.values,
      ...(record.relations ? { relations: record.relations } : {}),
    };
  }
  return record;
}

export function canonicalRecordID(
  selectedID: string,
  submitted: ModelContentRecord[],
  saved: ModelContentRecord[],
): string {
  if (!selectedID.startsWith("local:")) return selectedID;
  const selected = submitted.find((record) => record.id === selectedID);
  if (!selected) return "";
  return saved.find((record) => record.entityId === selected.entityId && record.sortOrder === selected.sortOrder)?.id ?? "";
}

export function remapLocalRecordIDs(
  draft: { records: ModelContentRecord[]; selections: ModelContentSelection[] },
  submitted: ModelContentRecord[],
  saved: ModelContentRecord[],
): { records: ModelContentRecord[]; selections: ModelContentSelection[] } {
  const idMap = new Map<string, string>();
  for (const record of submitted) {
    if (!record.id.startsWith("local:")) continue;
    const canonical = canonicalRecordID(record.id, submitted, saved);
    if (canonical) idMap.set(record.id, canonical);
  }
  if (idMap.size === 0) return draft;
  return {
    records: draft.records.map((record) => {
      const id = idMap.get(record.id) ?? record.id;
      const relations = record.relations
        ? Object.fromEntries(Object.entries(record.relations).map(([key, ids]) => [key, ids.map((itemID) => idMap.get(itemID) ?? itemID)]))
        : undefined;
      return { ...record, id, ...(relations ? { relations } : {}) };
    }),
    selections: draft.selections.map((selection) => ({ ...selection, recordIds: (selection.recordIds ?? []).map((id) => idMap.get(id) ?? id) })),
  };
}
