import type {
  DataModelEntity,
  DataModelField,
  DataModel,
  JsonValue,
  ModelContentRecord,
  ModelContentRecordInput,
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

export function createModelRecord(entityId: string, sortOrder: number, locale: string): ModelContentRecord {
  return { id: `local:${crypto.randomUUID()}`, translationId: "", locale, entityId, sortOrder, values: {} };
}

export function clearModelRecordData(records: ModelContentRecord[], recordId: string): ModelContentRecord[] {
  return records.map((record) => record.id === recordId
    ? { ...record, values: {} }
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

export function reorderModelRecordsById(
  records: ModelContentRecord[],
  entityId: string,
  orderedRecordIds: string[],
): ModelContentRecord[] {
  const entityRecords = records.filter((record) => record.entityId === entityId);
  if (orderedRecordIds.length !== entityRecords.length) return records;
  if (new Set(orderedRecordIds).size !== orderedRecordIds.length) return records;
  const byID = new Map(entityRecords.map((record) => [record.id, record]));
  const ordered = orderedRecordIds.flatMap((recordId) => {
    const record = byID.get(recordId);
    return record ? [record] : [];
  });
  if (ordered.length !== entityRecords.length) return records;
  if (ordered.every((record, index) => record.id === entityRecords[index]?.id)) return records;
  const orders = new Map(ordered.map((record, sortOrder) => [record.id, sortOrder]));
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
  if (!target) return { placementSelections: 0, affectedFeatureInputs: 0, mediaReferences: 0 };
  const entity = model.entities.find((item) => item.id === target.entityId);
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
  return { placementSelections, affectedFeatureInputs, mediaReferences };
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

export function reorderSelectedRecords(recordIds: string[], recordId: string, direction: -1 | 1): string[] {
  const next = [...recordIds];
  const index = next.indexOf(recordId);
  const nextIndex = index + direction;
  if (index < 0 || nextIndex < 0 || nextIndex >= next.length) return recordIds;
  [next[index], next[nextIndex]] = [next[nextIndex]!, next[index]!];
  return next;
}

export function reorderSelectedRecordsById(recordIds: string[], orderedRecordIds: string[]): string[] {
  if (recordIds.length !== orderedRecordIds.length) return recordIds;
  if (new Set(orderedRecordIds).size !== orderedRecordIds.length) return recordIds;
  const available = new Set(recordIds);
  if (orderedRecordIds.some((recordId) => !available.has(recordId))) return recordIds;
  if (orderedRecordIds.every((recordId, index) => recordId === recordIds[index])) return recordIds;
  return [...orderedRecordIds];
}

export function deleteModelRecord(
  records: ModelContentRecord[],
  selections: ModelContentSelection[],
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
      const sortOrder = orderByID.get(record.id) ?? record.sortOrder;
      return record.sortOrder === sortOrder ? record : { ...record, sortOrder };
    }),
    selections: selections.map((selection) => (selection.recordIds ?? []).includes(recordId)
      ? { ...selection, recordIds: (selection.recordIds ?? []).filter((id) => id !== recordId) }
      : selection),
  };
}

export interface OrphanedModelRecordData {
  unknownFieldKeys: string[];
}

export function orphanedModelRecordData(record: ModelContentRecord, model: DataModel): OrphanedModelRecordData {
  const entity = model.entities.find((item) => item.id === record.entityId);
  if (!entity) return { unknownFieldKeys: Object.keys(record.values) };
  const fieldKeys = new Set(entity.fields.map((field) => field.key));
  return {
    unknownFieldKeys: Object.keys(record.values).filter((key) => !fieldKeys.has(key)),
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
  return records.map((item) => {
    if (item.id !== recordId) return item;
    const values = Object.fromEntries(Object.entries(item.values).filter(([key]) => !invalidFields.has(key)));
    return { ...item, values };
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

export function apiModelRecordInput(record: ModelContentRecord): Omit<ModelContentRecord, "id"> | ModelContentRecordInput {
  if (record.id.startsWith("local:")) {
    return {
      clientKey: record.id,
      ...(record.translationId ? { translationId: record.translationId } : {}),
      entityId: record.entityId,
      sortOrder: record.sortOrder,
      values: record.values,
    };
  }
  return record;
}

export function canonicalRecordID(
  selectedID: string,
  submitted: ModelContentRecord[],
  saved: ModelContentRecord[],
  clientRecordIDs?: Record<string, string>,
): string {
  if (!selectedID.startsWith("local:")) return selectedID;
  const mapped = clientRecordIDs?.[selectedID];
  if (mapped) return mapped;
  const selected = submitted.find((record) => record.id === selectedID);
  if (!selected) return "";
  return saved.find((record) => record.entityId === selected.entityId && record.sortOrder === selected.sortOrder)?.id ?? "";
}

export function remapLocalRecordIDs(
  draft: { records: ModelContentRecord[]; selections: ModelContentSelection[] },
  submitted: ModelContentRecord[],
  saved: ModelContentRecord[],
  clientRecordIDs?: Record<string, string>,
): { records: ModelContentRecord[]; selections: ModelContentSelection[] } {
  const idMap = new Map<string, string>();
  for (const record of submitted) {
    if (!record.id.startsWith("local:")) continue;
    const canonical = canonicalRecordID(record.id, submitted, saved, clientRecordIDs);
    if (canonical) idMap.set(record.id, canonical);
  }
  if (idMap.size === 0) return draft;
  return {
    records: draft.records.map((record) => {
      const id = idMap.get(record.id) ?? record.id;
      return { ...record, id };
    }),
    selections: draft.selections.map((selection) => ({ ...selection, recordIds: (selection.recordIds ?? []).map((id) => idMap.get(id) ?? id) })),
  };
}
