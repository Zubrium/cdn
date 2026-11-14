import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, GripVertical, Link2, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { SortableItem, SortableList } from "@/components/ui/sortable-list";
import { useConfirm } from "@/components/common/confirm";
import { useDocumentTitle } from "@/components/common/page";
import type { DataModel, DataModelEntity, DataModelField, DataModelFieldType, DataModelRelation } from "@/types";
import { DataModelGraphCanvas } from "./components/data-model/DataModelGraphCanvas";
import { useSiteWorkspace } from "./SiteWorkspace";
import {
  addDataModelEntity,
  addDataModelField,
  addDataModelRelation,
  cardinalities,
  changeDataModelFieldType,
  dataModelEntityBindingImpacts,
  dataModelEntityNameControlID,
  dataModelEntityRemovalFocusTarget,
  dataModelFieldBindingImpacts,
  dataModelFieldNameControlID,
  dataModelFieldRemovalFocusTarget,
  dataModelRelationBindingImpacts,
  dataModelRelationNameControlID,
  dataModelRelationRemovalFocusTarget,
  describeBindingImpacts,
  entityCardinalities,
  fieldTypes,
  findDataModelRelationConflict,
  focusDataModelControl,
  modelFor,
  moveDataModelField,
  positionFor,
  removeDataModelEntity,
  removeDataModelField,
  removeDataModelRelation,
  reorderDataModelFields,
  renameDataModelEntity,
  renameDataModelField,
  renameDataModelRelation,
  setDataModelEntityPosition,
  setModel,
  updateDataModelField,
  updateDataModelEntityCardinality,
  updateDataModelRelation,
} from "./components/data-model/model-operations";

export * from "./components/data-model/model-operations";

type Selection =
  | { kind: "entity"; id: string }
  | { kind: "relation"; id: string }
  | null;

export default function DataModelPage() {
  useDocumentTitle("Модель данных");
  const { config, update } = useSiteWorkspace();
  const confirm = useConfirm();
  const model = useMemo(() => modelFor(config), [config]);
  const [selection, setSelection] = useState<Selection>(null);
  const [relationDialogOpen, setRelationDialogOpen] = useState(false);
  const [relationFrom, setRelationFrom] = useState("");
  const [relationTo, setRelationTo] = useState("");
  const [relationName, setRelationName] = useState("");
  const [relationCardinality, setRelationCardinality] = useState<DataModelRelation["cardinality"]>("one-to-many");
  const pendingFocusID = useRef<string | null>(null);
  const selectedEntity = selection?.kind === "entity" ? model.entities.find((entity) => entity.id === selection.id) : undefined;
  const selectedRelation = selection?.kind === "relation" ? (model.relations ?? []).find((relation) => relation.id === selection.id) : undefined;

  const changeModel = (mutate: (current: DataModel) => DataModel) => update((current) => setModel(current, mutate(modelFor(current))));
  useEffect(() => {
    const controlID = pendingFocusID.current;
    if (!controlID) return;
    pendingFocusID.current = null;
    focusDataModelControl(controlID, (id) => document.getElementById(id));
  }, [config]);
  useEffect(() => {
    if (selection?.kind === "entity" && !model.entities.some((entity) => entity.id === selection.id)) setSelection(null);
    if (selection?.kind === "relation" && !(model.relations ?? []).some((relation) => relation.id === selection.id)) setSelection(null);
  }, [model.entities, model.relations, selection]);

  const addEntity = (x = 80, y = 80) => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelEntityNameControlID(id);
    changeModel((current) => setDataModelEntityPosition(addDataModelEntity(current, () => id), id, x, y));
    setSelection({ kind: "entity", id });
  };
  const addField = (entity: DataModelEntity) => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelFieldNameControlID(id);
    changeModel((current) => addDataModelField(current, entity.id, () => id));
  };
  const removeEntity = async (entity: DataModelEntity) => {
    const impacts = dataModelEntityBindingImpacts(config, entity.id);
    const related = (model.relations ?? []).filter((relation) => relation.fromEntityId === entity.id || relation.toEntityId === entity.id).length;
    if (!(await confirm({
      title: "Удалить таблицу?",
      description: `Таблица «${entity.name}», ${related} связей и её привязки будут удалены в дочернем снапшоте. Записи останутся в «Контенте» до явной очистки.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить таблицу",
      destructive: true,
    }))) return;
    pendingFocusID.current = dataModelEntityRemovalFocusTarget(model, entity.id);
    setSelection(null);
    changeModel((current) => removeDataModelEntity(current, entity.id));
  };
  const removeField = async (entity: DataModelEntity, field: DataModelField) => {
    const impacts = dataModelFieldBindingImpacts(config, entity.id, field.id);
    if (!(await confirm({
      title: "Удалить поле?",
      description: `Поле «${field.name}» будет удалено из дочернего снапшота. Унаследованные значения потребуют явной очистки.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить поле",
      destructive: true,
    }))) return;
    pendingFocusID.current = dataModelFieldRemovalFocusTarget(entity, field.id);
    changeModel((current) => removeDataModelField(current, entity.id, field.id));
  };
  const changeFieldType = async (entity: DataModelEntity, field: DataModelField, type: DataModelFieldType) => {
    if (field.type === type) return;
    const impacts = dataModelFieldBindingImpacts(config, entity.id, field.id);
    if (!(await confirm({
      title: "Изменить тип поля?",
      description: `«${field.name}» изменится с «${fieldTypes.find((item) => item.value === field.type)?.label}» на «${fieldTypes.find((item) => item.value === type)?.label}».${describeBindingImpacts(impacts)}`,
      confirmLabel: "Изменить тип",
      destructive: true,
    }))) return;
    changeModel((current) => changeDataModelFieldType(current, entity.id, field.id, type));
  };
  const changeEntityCardinality = async (entity: DataModelEntity, cardinality: NonNullable<DataModelEntity["cardinality"]>) => {
    if ((entity.cardinality ?? "many") === cardinality) return;
    const toSingleton = cardinality === "singleton";
    if (!(await confirm({
      title: "Изменить количество записей?",
      description: toSingleton
        ? "Переход допустим только если черновик и публикация содержат не более одной записи. Данные не удаляются автоматически."
        : "Сущность сможет содержать несколько записей. Существующая запись сохранится.",
      confirmLabel: "Изменить",
      destructive: toSingleton,
    }))) return;
    changeModel((current) => updateDataModelEntityCardinality(current, entity.id, cardinality));
  };
  const removeRelation = async (relation: DataModelRelation) => {
    const impacts = dataModelRelationBindingImpacts(config, relation.id);
    if (!(await confirm({
      title: "Удалить связь?",
      description: `Связь «${relation.name}» будет удалена из дочернего снапшота.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить связь",
      destructive: true,
    }))) return;
    pendingFocusID.current = dataModelRelationRemovalFocusTarget(model, relation.id);
    setSelection(null);
    changeModel((current) => removeDataModelRelation(current, relation.id));
  };
  const changeRelationCardinality = async (relation: DataModelRelation, cardinality: DataModelRelation["cardinality"]) => {
    if (relation.cardinality === cardinality) return;
    const impacts = dataModelRelationBindingImpacts(config, relation.id);
    if (!(await confirm({
      title: "Изменить тип связи?",
      description: `Кардинальность «${relation.name}» изменится. Существующие связи записей могут потребовать исправления.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Изменить",
      destructive: true,
    }))) return;
    changeModel((current) => updateDataModelRelation(current, relation.id, (item) => ({ ...item, cardinality })));
  };
  const connectEntities = (sourceEntityId: string, targetEntityId: string) => {
    const existing = (model.relations ?? []).find((relation) => relation.fromEntityId === sourceEntityId && relation.toEntityId === targetEntityId);
    if (existing) {
      setSelection({ kind: "relation", id: existing.id });
      return;
    }
    const id = crypto.randomUUID();
    const source = model.entities.find((entity) => entity.id === sourceEntityId)?.name ?? "Таблица";
    const target = model.entities.find((entity) => entity.id === targetEntityId)?.name ?? "Таблица";
    changeModel((current) => addDataModelRelation(current, {
      name: `${source} → ${target}`,
      fromEntityId: sourceEntityId,
      toEntityId: targetEntityId,
      cardinality: "one-to-many",
    }, () => id));
    setSelection({ kind: "relation", id });
  };
  const openRelationDialog = () => {
    setRelationFrom(model.entities[0]?.id ?? "");
    setRelationTo(model.entities[1]?.id ?? "");
    setRelationName("");
    setRelationCardinality("one-to-many");
    setRelationDialogOpen(true);
  };
  const proposedRelation = {
    name: relationName.trim(),
    fromEntityId: relationFrom,
    toEntityId: relationTo,
    cardinality: relationCardinality,
  } satisfies Omit<DataModelRelation, "id">;
  const relationConflict = findDataModelRelationConflict(model, proposedRelation);
  const createRelation = () => {
    if (!relationFrom || !relationTo || !relationName.trim() || relationConflict) return;
    const id = crypto.randomUUID();
    changeModel((current) => addDataModelRelation(current, proposedRelation, () => id));
    setSelection({ kind: "relation", id });
    setRelationDialogOpen(false);
  };
  const moveSelectedEntity = (dx: number, dy: number) => {
    if (!selectedEntity) return;
    const index = model.entities.findIndex((entity) => entity.id === selectedEntity.id);
    const position = positionFor(model, selectedEntity, index);
    changeModel((current) => setDataModelEntityPosition(current, selectedEntity.id, Math.max(0, position.x + dx), Math.max(0, position.y + dy)));
  };

  return <div className="relative h-full min-h-0 overflow-hidden bg-background">
    <DataModelGraphCanvas
      model={model}
      selectedEntityId={selectedEntity?.id}
      selectedRelationId={selectedRelation?.id}
      onSelectEntity={(id) => setSelection({ kind: "entity", id })}
      onSelectRelation={(id) => setSelection({ kind: "relation", id })}
      onMoveEntity={(id, x, y) => changeModel((current) => setDataModelEntityPosition(current, id, x, y))}
      onCreateEntity={addEntity}
      onCreateRelation={connectEntities}
      onOpenRelationDialog={openRelationDialog}
    />

    {!selection && model.entities.length === 0 && <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
      <div className="max-w-sm text-center"><h1 className="text-xl font-semibold">Модель пока пуста</h1><p className="mt-2 text-sm text-muted-foreground">Нажмите ПКМ по пустому месту или используйте кнопку «Таблица», затем выберите таблицу для настройки.</p></div>
    </div>}

    {(selectedEntity || selectedRelation) && <aside className="absolute inset-y-0 right-0 z-30 w-full max-w-[22rem] overflow-y-auto border-l border-border bg-background shadow-xl" aria-label="Инспектор модели данных">
      <div className="sticky top-0 z-10 flex min-h-14 items-center justify-between border-b border-border bg-background/95 px-4 backdrop-blur-sm">
        <h2 className="text-sm font-semibold">{selectedEntity ? "Таблица" : "Связь"}</h2>
        <Button type="button" size="icon" variant="ghost" className="size-11" aria-label="Закрыть инспектор" onClick={() => setSelection(null)}><X /></Button>
      </div>
      {selectedEntity ? <EntityInspector
        entity={selectedEntity}
        onRename={(name) => changeModel((current) => renameDataModelEntity(current, selectedEntity.id, name))}
        onCardinality={(cardinality) => void changeEntityCardinality(selectedEntity, cardinality)}
        onMove={moveSelectedEntity}
        onAddField={() => addField(selectedEntity)}
        onRenameField={(fieldID, name) => changeModel((current) => renameDataModelField(current, selectedEntity.id, fieldID, name))}
        onChangeFieldType={(field, type) => void changeFieldType(selectedEntity, field, type)}
        onUpdateField={(fieldID, transform) => changeModel((current) => updateDataModelField(current, selectedEntity.id, fieldID, transform))}
        onMoveField={(fieldID, offset) => changeModel((current) => moveDataModelField(current, selectedEntity.id, fieldID, offset))}
        onReorderFields={(orderedIDs) => changeModel((current) => reorderDataModelFields(current, selectedEntity.id, orderedIDs))}
        onRemoveField={(field) => void removeField(selectedEntity, field)}
        onRemove={() => void removeEntity(selectedEntity)}
      /> : selectedRelation ? <RelationInspector
        relation={selectedRelation}
        model={model}
        onChangeName={(value) => changeModel((current) => renameDataModelRelation(current, selectedRelation.id, value))}
        onChangeCardinality={(value) => void changeRelationCardinality(selectedRelation, value)}
        onRemove={() => void removeRelation(selectedRelation)}
      /> : null}
    </aside>}

    <Dialog open={relationDialogOpen} onOpenChange={setRelationDialogOpen}>
      <DialogContent>
        <DialogHeader><DialogTitle>Создать связь</DialogTitle><DialogDescription>Это резервный способ без перетаскивания стрелки.</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <label className="space-y-1.5 text-sm"><span>От таблицы</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relationFrom} onChange={(event) => setRelationFrom(event.currentTarget.value)}>{model.entities.map((entity) => <option key={entity.id} value={entity.id}>{entity.name}</option>)}</select></label>
          <label className="space-y-1.5 text-sm"><span>К таблице</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relationTo} onChange={(event) => setRelationTo(event.currentTarget.value)}>{model.entities.map((entity) => <option key={entity.id} value={entity.id}>{entity.name}</option>)}</select></label>
          <label className="space-y-1.5 text-sm"><span>Название</span><Input className="min-h-11" value={relationName} onChange={(event) => setRelationName(event.currentTarget.value)} placeholder="Например: содержит" />{relationConflict && <span role="alert" className="text-xs text-destructive">Такая связь уже существует.</span>}</label>
          <label className="space-y-1.5 text-sm"><span>Кардинальность</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relationCardinality} onChange={(event) => setRelationCardinality(event.currentTarget.value as DataModelRelation["cardinality"])}>{cardinalities.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
        </div>
        <DialogFooter><Button type="button" variant="outline" className="min-h-11" onClick={() => setRelationDialogOpen(false)}>Отмена</Button><Button type="button" className="min-h-11" disabled={!relationFrom || !relationTo || !relationName.trim() || Boolean(relationConflict) || relationFrom === relationTo} onClick={createRelation}><Link2 />Создать</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </div>;
}

export function EntityInspector({
  entity,
  onRename,
  onCardinality,
  onMove,
  onAddField,
  onRenameField,
  onChangeFieldType,
  onUpdateField,
  onMoveField,
  onReorderFields,
  onRemoveField,
  onRemove,
}: {
  entity: DataModelEntity;
  onRename: (value: string) => void;
  onCardinality: (value: NonNullable<DataModelEntity["cardinality"]>) => void;
  onMove: (dx: number, dy: number) => void;
  onAddField: () => void;
  onRenameField: (fieldID: string, value: string) => void;
  onChangeFieldType: (field: DataModelField, type: DataModelFieldType) => void;
  onUpdateField: (fieldID: string, transform: (field: DataModelField) => DataModelField) => void;
  onMoveField: (fieldID: string, offset: -1 | 1) => void;
  onReorderFields: (orderedFieldIDs: string[]) => void;
  onRemoveField: (field: DataModelField) => void;
  onRemove: () => void;
}) {
  return <div className="space-y-5 p-4">
    <div className="space-y-3">
      <div className="space-y-1.5"><label htmlFor={dataModelEntityNameControlID(entity.id)} className="text-xs text-muted-foreground">Название</label><Input id={dataModelEntityNameControlID(entity.id)} className="min-h-11" value={entity.name} onChange={(event) => onRename(event.currentTarget.value)} /></div>
      <label className="block space-y-1.5 text-sm"><span className="text-xs text-muted-foreground">Записей</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={entity.cardinality ?? "many"} onChange={(event) => onCardinality(event.currentTarget.value as NonNullable<DataModelEntity["cardinality"]>)}>{entityCardinalities.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <div className="flex items-center justify-between gap-2"><span className="text-xs text-muted-foreground">Положение</span><div className="flex gap-1"><Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть ${entity.name} влево`} onClick={() => onMove(-24, 0)}><ArrowLeft /></Button><Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть ${entity.name} вверх`} onClick={() => onMove(0, -24)}><ArrowUp /></Button><Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть ${entity.name} вниз`} onClick={() => onMove(0, 24)}><ArrowDown /></Button><Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть ${entity.name} вправо`} onClick={() => onMove(24, 0)}><ArrowRight /></Button></div></div>
    </div>
    <div>
      <div className="flex items-center justify-between border-b border-border pb-2"><h2 className="text-xs font-medium text-muted-foreground">Поля</h2><Button type="button" size="sm" variant="ghost" className="min-h-10" onClick={onAddField}><Plus />Поле</Button></div>
      <SortableList
        items={entity.fields.map((field) => field.id)}
        onReorder={onReorderFields}
        getItemLabel={(id) => entity.fields.find((field) => field.id === id)?.name || "поле"}
      >
        <ol className="divide-y divide-border" aria-label={`Порядок полей таблицы ${entity.name}`}>
          {entity.fields.map((field, index) => <SortableItem key={field.id} id={field.id}>{(handle) => <div className="space-y-2 py-3">
            <div className="flex items-center gap-1">
              <Button
                ref={handle.setActivatorNodeRef}
                type="button"
                size="icon-sm"
                variant="ghost"
                className="touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
                aria-label={`Перетащить поле ${field.name}`}
                {...handle.attributes}
                {...handle.listeners}
              ><GripVertical /></Button>
              <Input id={dataModelFieldNameControlID(field.id)} className="min-h-10 min-w-0 flex-1" value={field.name} onChange={(event) => onRenameField(field.id, event.currentTarget.value)} />
              <Button type="button" size="icon-sm" variant="ghost" aria-label={`Переместить ${field.name} выше`} disabled={index === 0} onClick={() => onMoveField(field.id, -1)}><ArrowUp /></Button>
              <Button type="button" size="icon-sm" variant="ghost" aria-label={`Переместить ${field.name} ниже`} disabled={index === entity.fields.length - 1} onClick={() => onMoveField(field.id, 1)}><ArrowDown /></Button>
              <Button type="button" size="icon-sm" variant="ghost" className="text-destructive" aria-label={`Удалить ${field.name}`} onClick={() => onRemoveField(field)}><X /></Button>
            </div>
            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 pl-9">
              <select className="h-10 min-w-0 rounded-md border border-input bg-background px-2 text-sm" aria-label={`Тип ${field.name}`} value={field.type} onChange={(event) => onChangeFieldType(field, event.currentTarget.value as DataModelFieldType)}>{fieldTypes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>
              <label className="flex min-h-10 items-center gap-2 text-xs"><input type="checkbox" className="size-4 accent-primary" disabled={field.type === "longText" || field.type === "media" || field.type === "boolean"} checked={Boolean(field.unique)} onChange={(event) => onUpdateField(field.id, (current) => ({ ...current, unique: event.currentTarget.checked }))} />уникальное</label>
            </div>
            {field.type === "enum" && <label className="block space-y-1 pl-9 text-xs"><span className="text-muted-foreground">Варианты, по одному на строку</span><textarea className="min-h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={(field.options ?? []).join("\n")} onChange={(event) => onUpdateField(field.id, (current) => ({ ...current, options: [...new Set(event.currentTarget.value.split("\n").map((value) => value.trim()).filter(Boolean))] }))} /></label>}
          </div>}</SortableItem>)}
        </ol>
      </SortableList>
      {entity.fields.length === 0 && <p className="py-5 text-sm text-muted-foreground">Полей пока нет.</p>}
    </div>
    <Button type="button" variant="outline" className="min-h-11 w-full text-destructive" onClick={onRemove}>Удалить таблицу</Button>
  </div>;
}

export function RelationInspector({ relation, model, onChangeName, onChangeCardinality, onRemove }: {
  relation: DataModelRelation;
  model: DataModel;
  onChangeName: (value: string) => void;
  onChangeCardinality: (value: DataModelRelation["cardinality"]) => void;
  onRemove: () => void;
}) {
  const from = model.entities.find((entity) => entity.id === relation.fromEntityId)?.name ?? "Удалённая таблица";
  const to = model.entities.find((entity) => entity.id === relation.toEntityId)?.name ?? "Удалённая таблица";
  const duplicate = findDataModelRelationConflict(model, relation, relation.id);
  const errorID = `data-model-relation-name-error-${relation.id}`;
  return <div className="space-y-5 p-4">
    <p className="text-sm text-muted-foreground">{from} → {to}</p>
    <label className="block space-y-1.5 text-sm"><span>Название</span><Input id={dataModelRelationNameControlID(relation.id)} className="min-h-11" required aria-invalid={Boolean(duplicate)} aria-describedby={duplicate ? errorID : undefined} value={relation.name} onChange={(event) => onChangeName(event.currentTarget.value)} />{duplicate && <span id={errorID} className="text-xs text-destructive" role="alert">Такая связь уже есть. Измените название.</span>}</label>
    <label className="block space-y-1.5 text-sm"><span>Кардинальность</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relation.cardinality} onChange={(event) => onChangeCardinality(event.currentTarget.value as DataModelRelation["cardinality"])}>{cardinalities.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
    <Button type="button" variant="outline" className="min-h-11 text-destructive" onClick={onRemove}>Удалить связь</Button>
  </div>;
}
