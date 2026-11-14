import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Link2, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useConfirm } from "@/components/common/confirm";
import { useDocumentTitle } from "@/components/common/page";
import type { DataModel, DataModelEntity, DataModelField, DataModelFieldType, DataModelRelation } from "@/types";
import { DataModelEntityCard, type DataModelEntityCardActions } from "./components/data-model/DataModelEntityCard";
import { DataModelGraphCanvas } from "./components/data-model/DataModelGraphCanvas";
import { useSiteWorkspace } from "./SiteWorkspace";
import {
  addDataModelEntity,
  addDataModelField,
  addDataModelRelation,
  changeDataModelFieldType,
  DATA_MODEL_ADD_ENTITY_CONTROL_ID,
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
  fieldTypes,
  findDataModelRelationConflict,
  focusDataModelControl,
  modelFor,
  moveDataModelField,
  positionFor,
  removeDataModelEntity,
  removeDataModelField,
  removeDataModelRelation,
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
  const [selection, setSelection] = useState<Selection>(() => model.entities[0] ? { kind: "entity", id: model.entities[0].id } : null);
  const [relationDialogOpen, setRelationDialogOpen] = useState(false);
  const [relationFrom, setRelationFrom] = useState("");
  const [relationTo, setRelationTo] = useState("");
  const [relationName, setRelationName] = useState("");
  const [relationCardinality, setRelationCardinality] = useState<DataModelRelation["cardinality"]>("one-to-many");
  const pendingFocusID = useRef<string | null>(null);
  const selectedEntity = selection?.kind === "entity" ? model.entities.find((entity) => entity.id === selection.id) : undefined;
  const selectedRelation = selection?.kind === "relation" ? (model.relations ?? []).find((relation) => relation.id === selection.id) : undefined;
  const proposedRelation = {
    name: relationName.trim(),
    fromEntityId: relationFrom,
    toEntityId: relationTo,
    cardinality: relationCardinality,
  } satisfies Omit<DataModelRelation, "id">;
  const relationConflict = findDataModelRelationConflict(model, proposedRelation);

  const changeModel = (mutate: (current: DataModel) => DataModel) => {
    update((current) => setModel(current, mutate(modelFor(current))));
  };

  useEffect(() => {
    const controlID = pendingFocusID.current;
    if (!controlID) return;
    pendingFocusID.current = null;
    focusDataModelControl(controlID, (id) => document.getElementById(id));
  }, [config]);

  useEffect(() => {
    if (selection?.kind === "entity" && !model.entities.some((entity) => entity.id === selection.id)) setSelection(null);
    if (selection?.kind === "relation" && !(model.relations ?? []).some((relation) => relation.id === selection.id)) setSelection(null);
    if (!selection && model.entities[0]) setSelection({ kind: "entity", id: model.entities[0].id });
  }, [model.entities, model.relations, selection]);

  const addEntity = () => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelEntityNameControlID(id);
    setSelection({ kind: "entity", id });
    changeModel((current) => addDataModelEntity(current, () => id));
  };

  const addField = (entity: DataModelEntity) => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelFieldNameControlID(id);
    setSelection({ kind: "entity", id: entity.id });
    changeModel((current) => addDataModelField(current, entity.id, () => id));
  };

  const removeEntity = async (entity: DataModelEntity) => {
    const related = (model.relations ?? []).filter((relation) => relation.fromEntityId === entity.id || relation.toEntityId === entity.id).length;
    const impacts = dataModelEntityBindingImpacts(config, entity.id);
    if (!(await confirm({
      title: "Удалить сущность?",
      description: `Сущность «${entity.name}» и ${related} ${related === 1 ? "связь" : "связей"} будут удалены в создаваемом дочернем снапшоте. Унаследованные записи этой сущности останутся в черновике и потребуют явного удаления или восстановления модели в разделе «Контент». Родительский снапшот останется без изменений.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить сущность",
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
      description: `Поле «${field.name}» будет удалено из дочернего снапшота. Унаследованные значения останутся в черновиках и будут помечены для явной очистки в разделе «Контент»; родительский снапшот останется без изменений.${describeBindingImpacts(impacts)}`,
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
      description: `Тип поля «${field.name}» меняется с «${fieldTypes.find((item) => item.value === field.type)?.label}» на «${fieldTypes.find((item) => item.value === type)?.label}». Изменение попадёт в дочерний снапшот; родительский снапшот останется без изменений.${describeBindingImpacts(impacts)}${impacts.length === 0 ? " Проверьте совместимость унаследованных значений в разделе «Контент»." : ""}`,
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
        ? `Сущность «${entity.name}» будет ограничена одной записью в дочернем снапшоте. Переход будет принят только если черновик и публикация содержат не более одной записи; при превышении сервер отклонит изменение без удаления данных.`
        : `Сущность «${entity.name}» сможет содержать несколько записей в дочернем снапшоте. Идентификаторы и существующая запись сохранятся.`,
      confirmLabel: "Изменить количество",
      destructive: toSingleton,
    }))) return;
    changeModel((current) => updateDataModelEntityCardinality(current, entity.id, cardinality));
  };

  const changeRelationCardinality = async (relation: DataModelRelation, cardinality: DataModelRelation["cardinality"]) => {
    if (relation.cardinality === cardinality) return;
    const impacts = dataModelRelationBindingImpacts(config, relation.id);
    if (!(await confirm({
      title: "Изменить тип связи?",
      description: `Кардинальность связи «${relation.name}» изменится в дочернем снапшоте. Существующие ссылки могут потребовать исправления в редакторе контента.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Изменить тип",
      destructive: true,
    }))) return;
    changeModel((current) => updateDataModelRelation(current, relation.id, (item) => ({ ...item, cardinality })));
  };

  const removeRelation = async (relation: DataModelRelation) => {
    const impacts = dataModelRelationBindingImpacts(config, relation.id);
    if (!(await confirm({
      title: "Удалить связь?",
      description: `Связь «${relation.name}» будет удалена в дочернем снапшоте. Унаследованные ссылки останутся в черновиках и будут помечены для явной очистки в разделе «Контент».${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить связь",
      destructive: true,
    }))) return;
    pendingFocusID.current = dataModelRelationRemovalFocusTarget(model, relation.id);
    setSelection(null);
    changeModel((current) => removeDataModelRelation(current, relation.id));
  };

  const openRelationDialog = () => {
    setRelationFrom(model.entities[0]?.id ?? "");
    setRelationTo(model.entities[1]?.id ?? "");
    setRelationName("");
    setRelationCardinality("one-to-many");
    setRelationDialogOpen(true);
  };

  const createRelation = () => {
    if (!relationFrom || !relationTo || !relationName.trim()) return;
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelRelationNameControlID(id);
    changeModel((current) => addDataModelRelation(current, {
      ...proposedRelation,
      name: proposedRelation.name,
    }, () => id));
    setSelection({ kind: "relation", id });
    setRelationDialogOpen(false);
  };

  const cardActions: DataModelEntityCardActions = {
    onAddField: addField,
    onChangeFieldType: (entity, field, type) => void changeFieldType(entity, field, type),
    onChangeEntityCardinality: (entity, cardinality) => void changeEntityCardinality(entity, cardinality),
    onMoveEntity: (entityID, x, y) => changeModel((current) => setDataModelEntityPosition(current, entityID, x, y)),
    onMoveField: (entityID, fieldID, offset) => {
      pendingFocusID.current = dataModelFieldNameControlID(fieldID);
      changeModel((current) => moveDataModelField(current, entityID, fieldID, offset));
    },
    onRemoveEntity: (entity) => void removeEntity(entity),
    onRemoveField: (entity, field) => void removeField(entity, field),
    onRenameEntity: (entityID, name) => changeModel((current) => renameDataModelEntity(current, entityID, name)),
    onRenameField: (entityID, fieldID, name) => changeModel((current) => renameDataModelField(current, entityID, fieldID, name)),
    onUpdateField: (entityID, fieldID, transform) => changeModel((current) => updateDataModelField(current, entityID, fieldID, transform)),
  };

  const moveSelectedEntity = (dx: number, dy: number) => {
    if (!selectedEntity) return;
    const index = model.entities.findIndex((entity) => entity.id === selectedEntity.id);
    const position = positionFor(model, selectedEntity, index);
    cardActions.onMoveEntity(selectedEntity.id, Math.max(0, position.x + dx), Math.max(0, position.y + dy));
  };

  return <div className="flex h-full min-h-0 flex-col gap-4 p-4 sm:p-5">
    <header className="flex flex-wrap items-start justify-between gap-3">
      <div className="max-w-3xl">
        <h1 className="text-xl font-semibold">Модель данных</h1>
        <p className="mt-1 text-sm text-muted-foreground">Выберите сущность или связь на канвасе и настройте её справа. Записи редактируются отдельно в «Контенте».</p>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" className="min-h-11" disabled={model.entities.length < 2} onClick={openRelationDialog}><Link2 />Создать связь</Button>
        <Button id={DATA_MODEL_ADD_ENTITY_CONTROL_ID} type="button" className="min-h-11" onClick={addEntity}><Plus />Добавить сущность</Button>
      </div>
    </header>

    {model.entities.length === 0 ? <section className="flex min-h-[28rem] flex-1 flex-col items-center justify-center rounded-xl border border-dashed border-border bg-card p-8 text-center">
      <h2 className="font-medium">Модель пока пуста</h2>
      <p className="mt-2 max-w-lg text-sm text-muted-foreground">Добавьте сущность, например «Товар» или «Страница». Её поля станут формой записи в редакторе контента.</p>
      <Button type="button" className="mt-4 min-h-11" onClick={addEntity}><Plus />Создать первую сущность</Button>
    </section> : <div className="grid min-h-0 flex-1 gap-4 xl:grid-cols-[minmax(0,1fr)_24rem]">
      <DataModelGraphCanvas
        model={model}
        selectedEntityId={selectedEntity?.id}
        selectedRelationId={selectedRelation?.id}
        onSelectEntity={(id) => setSelection({ kind: "entity", id })}
        onSelectRelation={(id) => setSelection({ kind: "relation", id })}
        onMoveEntity={cardActions.onMoveEntity}
      />
      <aside className="min-w-0 overflow-y-auto rounded-xl border border-border bg-card p-3 xl:max-h-[calc(100dvh-13rem)]" aria-label="Инспектор модели данных">
        {selectedEntity ? <>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2 border-b border-border pb-3">
            <div><h2 className="text-sm font-semibold">Сущность</h2><p className="text-xs text-muted-foreground">Перетащите карточку или используйте кнопки</p></div>
            <div className="flex gap-1">
              <Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть сущность ${selectedEntity.name || "без названия"} влево`} onClick={() => moveSelectedEntity(-30, 0)}><ArrowLeft /></Button>
              <Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть сущность ${selectedEntity.name || "без названия"} вверх`} onClick={() => moveSelectedEntity(0, -30)}><ArrowUp /></Button>
              <Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть сущность ${selectedEntity.name || "без названия"} вниз`} onClick={() => moveSelectedEntity(0, 30)}><ArrowDown /></Button>
              <Button type="button" size="icon-sm" variant="outline" aria-label={`Сдвинуть сущность ${selectedEntity.name || "без названия"} вправо`} onClick={() => moveSelectedEntity(30, 0)}><ArrowRight /></Button>
            </div>
          </div>
          <DataModelEntityCard {...cardActions} entity={selectedEntity} position={positionFor(model, selectedEntity, model.entities.indexOf(selectedEntity))} graphView={false} />
        </> : selectedRelation ? <RelationInspector
          relation={selectedRelation}
          model={model}
          onChangeName={(value) => changeModel((current) => renameDataModelRelation(current, selectedRelation.id, value))}
          onChangeCardinality={(value) => void changeRelationCardinality(selectedRelation, value)}
          onRemove={() => void removeRelation(selectedRelation)}
        /> : <div className="flex min-h-48 items-center justify-center p-5 text-center text-sm text-muted-foreground">Выберите сущность или связь на канвасе.</div>}
      </aside>
    </div>}

    <Dialog open={relationDialogOpen} onOpenChange={setRelationDialogOpen}>
      <DialogContent>
        <DialogHeader><DialogTitle>Создать связь</DialogTitle><DialogDescription>Связь показывает, как записи одной сущности ссылаются на записи другой.</DialogDescription></DialogHeader>
        <div className="grid gap-3">
          <label className="space-y-1.5 text-sm"><span>От сущности</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relationFrom} onChange={(event) => setRelationFrom(event.currentTarget.value)}>{model.entities.map((entity) => <option key={entity.id} value={entity.id}>{entity.name || "Без названия"}</option>)}</select></label>
          <label className="space-y-1.5 text-sm"><span>К сущности</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relationTo} onChange={(event) => setRelationTo(event.currentTarget.value)}>{model.entities.map((entity) => <option key={entity.id} value={entity.id}>{entity.name || "Без названия"}</option>)}</select></label>
          <label className="space-y-1.5 text-sm"><span>Тип связи</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relationCardinality} onChange={(event) => setRelationCardinality(event.currentTarget.value as DataModelRelation["cardinality"])}><option value="one-to-one">Одна к одной</option><option value="one-to-many">Одна ко многим</option><option value="many-to-many">Многие ко многим</option></select></label>
          <label className="space-y-1.5 text-sm"><span>Название связи</span><Input className="min-h-11" value={relationName} onChange={(event) => setRelationName(event.currentTarget.value)} placeholder="Например: содержит" />{relationConflict && <span role="alert" className="text-xs text-destructive">Такая связь уже существует. Измените название или направление.</span>}</label>
        </div>
        <DialogFooter><Button type="button" variant="outline" className="min-h-11" onClick={() => setRelationDialogOpen(false)}>Отмена</Button><Button type="button" className="min-h-11" disabled={!relationFrom || !relationTo || !relationName.trim() || Boolean(relationConflict) || relationFrom === relationTo && model.entities.length > 1} onClick={createRelation}>Создать связь</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </div>;
}

export function RelationInspector({ relation, model, onChangeName, onChangeCardinality, onRemove }: {
  relation: DataModelRelation;
  model: DataModel;
  onChangeName: (value: string) => void;
  onChangeCardinality: (value: DataModelRelation["cardinality"]) => void;
  onRemove: () => void;
}) {
  const from = model.entities.find((entity) => entity.id === relation.fromEntityId)?.name ?? "Удалённая сущность";
  const to = model.entities.find((entity) => entity.id === relation.toEntityId)?.name ?? "Удалённая сущность";
  const duplicate = findDataModelRelationConflict(model, relation, relation.id);
  const errorID = `data-model-relation-name-error-${relation.id}`;
  return <div className="space-y-4">
    <div className="border-b border-border pb-3"><h2 className="text-sm font-semibold">Связь</h2><p className="mt-1 text-sm text-muted-foreground">{from} → {to}</p></div>
    <label className="block space-y-1.5 text-sm"><span>Название</span><Input id={dataModelRelationNameControlID(relation.id)} className="min-h-11" required aria-invalid={Boolean(duplicate)} aria-describedby={duplicate ? errorID : undefined} value={relation.name} onChange={(event) => onChangeName(event.currentTarget.value)} />{duplicate && <span id={errorID} className="text-xs text-destructive" role="alert">Такая связь уже есть. Измените название или направление.</span>}</label>
    <label className="block space-y-1.5 text-sm"><span>Кардинальность</span><select className="h-11 w-full rounded-md border border-input bg-background px-3" value={relation.cardinality} onChange={(event) => onChangeCardinality(event.currentTarget.value as DataModelRelation["cardinality"])}><option value="one-to-one">Одна к одной</option><option value="one-to-many">Одна ко многим</option><option value="many-to-many">Многие ко многим</option></select></label>
    <p className="rounded-md bg-muted/40 p-3 text-xs text-muted-foreground">Значения связи выбираются в «Контенте». Если связь используется входами фич, при изменении покажем затронутые размещения.</p>
    <Button type="button" variant="outline" className="min-h-11 text-destructive" onClick={onRemove}>Удалить связь</Button>
  </div>;
}
