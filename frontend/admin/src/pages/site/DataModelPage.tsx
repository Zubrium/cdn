import { useEffect, useMemo, useRef, useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/common/confirm";
import type { DataModel, DataModelEntity, DataModelField, DataModelFieldType, DataModelRelation } from "@/types";
import { DataModelEntityCard, type DataModelEntityCardActions } from "./components/data-model/DataModelEntityCard";
import { DataModelGraphCanvas } from "./components/data-model/DataModelGraphCanvas";
import { DataModelRelationsEditor } from "./components/data-model/DataModelRelationsEditor";
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

export default function DataModelPage() {
  const { config, update } = useSiteWorkspace();
  const confirm = useConfirm();
  const [viewMode, setViewMode] = useState<"list" | "graph">("list");
  const pendingFocusID = useRef<string | null>(null);
  const model = useMemo(() => modelFor(config), [config]);
  const positioned = model.entities.map((entity, index) => ({ entity, position: positionFor(model, entity, index) }));

  const changeModel = (mutate: (current: DataModel) => DataModel) => {
    update((current) => setModel(current, mutate(modelFor(current))));
  };

  useEffect(() => {
    const controlID = pendingFocusID.current;
    if (!controlID) return;
    pendingFocusID.current = null;
    focusDataModelControl(controlID, (id) => document.getElementById(id));
  }, [config]);

  const addEntity = () => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelEntityNameControlID(id);
    changeModel((current) => addDataModelEntity(current, () => id));
  };

  const addField = (entity: DataModelEntity) => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelFieldNameControlID(id);
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

  const changeEntityCardinality = async (
    entity: DataModelEntity,
    cardinality: NonNullable<DataModelEntity["cardinality"]>,
  ) => {
    if ((entity.cardinality ?? "many") === cardinality) return;
    const toSingleton = cardinality === "singleton";
    if (!(await confirm({
      title: "Изменить количество записей?",
      description: toSingleton
        ? `Сущность «${entity.name}» будет ограничена одной записью в дочернем снапшоте. Переход будет принят только если черновик и публикация содержат не более одной записи; при превышении сервер отклонит изменение без удаления данных. Родительский снапшот останется без изменений.`
        : `Сущность «${entity.name}» сможет содержать несколько записей в дочернем снапшоте. Идентификаторы и существующая запись сохранятся; родительский снапшот останется без изменений.`,
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
      description: `Кардинальность связи «${relation.name}» изменится в дочернем снапшоте. Существующие ссылки могут потребовать исправления в редакторе контента; родительский снапшот останется без изменений.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Изменить тип связи",
      destructive: true,
    }))) return;
    changeModel((current) => updateDataModelRelation(current, relation.id, (item) => ({ ...item, cardinality })));
  };

  const removeRelation = async (relation: DataModelRelation) => {
    const impacts = dataModelRelationBindingImpacts(config, relation.id);
    if (!(await confirm({
      title: "Удалить связь?",
      description: `Связь «${relation.name}» будет удалена в дочернем снапшоте. Унаследованные ссылки останутся в черновиках и будут помечены для явной очистки в разделе «Контент»; родительский снапшот останется без изменений.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить связь",
      destructive: true,
    }))) return;
    pendingFocusID.current = dataModelRelationRemovalFocusTarget(model, relation.id);
    changeModel((current) => removeDataModelRelation(current, relation.id));
  };

  const addRelation = (relation: Omit<DataModelRelation, "id">) => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelRelationNameControlID(id);
    changeModel((current) => addDataModelRelation(current, relation, () => id));
  };

  const cardActions: DataModelEntityCardActions = {
    onAddField: addField,
    onChangeFieldType: (entity, field, type) => void changeFieldType(entity, field, type),
    onChangeEntityCardinality: (entity, cardinality) => void changeEntityCardinality(entity, cardinality),
    onMoveEntity: (entityID, x, y) => changeModel((current) => setDataModelEntityPosition(current, entityID, x, y)),
    onMoveField: (entityID, fieldID, offset) => {
      // Reordering can disable the activated arrow button at the new boundary.
      // Return focus to the stable field-name control after React renders so
      // keyboard users do not lose their place when the action disappears.
      pendingFocusID.current = dataModelFieldNameControlID(fieldID);
      changeModel((current) => moveDataModelField(current, entityID, fieldID, offset));
    },
    onRemoveEntity: (entity) => void removeEntity(entity),
    onRemoveField: (entity, field) => void removeField(entity, field),
    onRenameEntity: (entityID, name) => changeModel((current) => renameDataModelEntity(current, entityID, name)),
    onRenameField: (entityID, fieldID, name) => changeModel((current) => renameDataModelField(current, entityID, fieldID, name)),
    onUpdateField: (entityID, fieldID, transform) => changeModel((current) => updateDataModelField(current, entityID, fieldID, transform)),
  };

  return <div className="min-w-0 space-y-5 p-4 sm:p-6">
    <header className="flex flex-wrap items-start justify-between gap-3">
      <div className="max-w-3xl space-y-1">
        <h1 className="text-xl font-semibold">Модель данных</h1>
        <p className="text-sm text-muted-foreground">Создайте сущности, поля и связи сайта. Значения записей редактируются отдельно в «Контенте»; изменения структуры сохраняются новым снапшотом.</p>
        <p className="text-xs text-muted-foreground">Переименование сохраняет системные ID и привязки. При удалении покажем затронутые размещения; унаследованные значения и ссылки сохранятся до явной очистки в «Контенте».</p>
      </div>
      <Button id={DATA_MODEL_ADD_ENTITY_CONTROL_ID} type="button" className="min-h-11" onClick={addEntity}><Plus className="size-4" />Добавить сущность</Button>
    </header>

    {model.entities.length === 0 ? <section className="rounded-xl border border-dashed border-border p-8 text-center">
      <h2 className="font-medium">Модель пока пуста</h2>
      <p className="mx-auto mt-2 max-w-lg text-sm text-muted-foreground">Добавьте сущность, например «Товар» или «Страница». Её поля станут формой записи в редакторе контента.</p>
      <Button type="button" variant="outline" className="mt-4 min-h-11" onClick={addEntity}><Plus className="mr-2 size-4" />Создать первую сущность</Button>
    </section> : <>
      <fieldset className="flex flex-wrap items-center gap-2">
        <legend className="sr-only">Представление модели данных</legend>
        <Button type="button" variant={viewMode === "list" ? "default" : "outline"} className="min-h-10" aria-pressed={viewMode === "list"} onClick={() => setViewMode("list")}>Карточки модели</Button>
        <Button type="button" variant={viewMode === "graph" ? "default" : "outline"} className="min-h-10" aria-pressed={viewMode === "graph"} onClick={() => setViewMode("graph")}>ER-граф</Button>
      </fieldset>

      {viewMode === "list" ? <section aria-label="Сущности модели, список" className="rounded-xl border border-border bg-muted/10 p-3 sm:p-4">
        <ol className="grid list-none gap-4 p-0 md:grid-cols-2">
          {positioned.map(({ entity, position }) => <li key={entity.id}><DataModelEntityCard {...cardActions} entity={entity} position={position} graphView={false} /></li>)}
        </ol>
      </section> : <DataModelGraphCanvas model={model} {...cardActions} />}

      <DataModelRelationsEditor
        model={model}
        onAddRelation={addRelation}
        onChangeCardinality={(relation, cardinality) => void changeRelationCardinality(relation, cardinality)}
        onRemoveRelation={(relation) => void removeRelation(relation)}
        onRenameRelation={(relationID, name) => changeModel((current) => renameDataModelRelation(current, relationID, name))}
      />
      <p className="text-sm text-muted-foreground">Система генерирует внутренние идентификаторы. Все поля записей необязательны: ограничения типа применяются к переданным значениям. Значения и связи записей редактируются в «Контенте», а в этом редакторе настраивается только структура сайта.</p>
    </>}
  </div>;
}
