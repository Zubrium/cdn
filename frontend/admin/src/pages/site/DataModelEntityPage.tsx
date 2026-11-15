import { useEffect, useMemo, useRef } from "react";
import { ArrowDown, ArrowLeft, ArrowUp, GripVertical, Plus, Trash2 } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useConfirm } from "@/components/common/confirm";
import { useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SortableItem, SortableList } from "@/components/ui/sortable-list";
import type { DataModel, DataModelEntity, DataModelField, DataModelFieldType } from "@/types";
import { useSiteWorkspace } from "./SiteWorkspace";
import {
  addDataModelField,
  changeDataModelFieldType,
  dataModelAddFieldControlID,
  dataModelEntityBindingImpacts,
  dataModelEntityNameControlID,
  dataModelFieldBindingImpacts,
  dataModelFieldNameControlID,
  describeBindingImpacts,
  fieldTypes,
  focusDataModelControl,
  modelFor,
  moveDataModelField,
  parseDataModelEnumOptions,
  removeDataModelEntity,
  removeDataModelField,
  renameDataModelEntity,
  renameDataModelField,
  reorderDataModelFields,
  updateDataModelEntityCardinality,
  updateDataModelField,
} from "./components/data-model/model-operations";

export default function DataModelEntityPage() {
  const { model: entityKey = "" } = useParams();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const { config, update } = useSiteWorkspace();
  const confirm = useConfirm();
  const model = useMemo(() => modelFor(config), [config]);
  const entity = model.entities.find((item) => item.key === entityKey);
  const snapshotId = searchParams.get("snapshot") ?? "";
  const suffix = snapshotId ? `?snapshot=${encodeURIComponent(snapshotId)}` : "";
  const pendingFocusID = useRef<string | null>(null);
  useDocumentTitle(entity?.name ?? "Модель");

  useEffect(() => {
    const controlID = pendingFocusID.current;
    if (!controlID) return;
    pendingFocusID.current = null;
    focusDataModelControl(controlID, (id) => document.getElementById(id));
  }, [config]);

  if (!entity) {
    return <div className="flex h-full min-h-0 items-center justify-center overflow-y-auto p-6 text-center">
      <div>
        <h1 className="text-xl font-semibold">Модель не найдена</h1>
        <p className="mt-2 text-sm text-muted-foreground">Она могла быть удалена из рабочего черновика.</p>
        <Button asChild variant="outline" className="mt-5 min-h-11"><Link to={`/site/data-model${suffix}`}><ArrowLeft />К моделям</Link></Button>
      </div>
    </div>;
  }

  const changeModel = (mutate: (current: DataModel) => DataModel) =>
    update((current) => ({ ...current, dataModel: mutate(modelFor(current)) }));

  const addField = () => {
    const id = crypto.randomUUID();
    pendingFocusID.current = dataModelFieldNameControlID(id);
    changeModel((current) => addDataModelField(current, entity.id, () => id));
  };
  const removeField = async (field: DataModelField) => {
    const impacts = dataModelFieldBindingImpacts(config, entity.id, field.id);
    if (!(await confirm({
      title: "Удалить поле?",
      description: `Поле «${field.name}» будет удалено из дочернего снапшота. Унаследованные значения потребуют явной очистки.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить поле",
      destructive: true,
    }))) return;
    pendingFocusID.current = dataModelAddFieldControlID(entity.id);
    changeModel((current) => removeDataModelField(current, entity.id, field.id));
  };
  const changeFieldType = async (field: DataModelField, type: DataModelFieldType) => {
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
  const changeCardinality = async (cardinality: NonNullable<DataModelEntity["cardinality"]>) => {
    if ((entity.cardinality ?? "many") === cardinality) return;
    const toSingleton = cardinality === "singleton";
    if (!(await confirm({
      title: "Изменить количество записей?",
      description: toSingleton
        ? "Переход допустим только если черновик и публикация содержат не более одной записи. Данные не удаляются автоматически."
        : "Модель сможет содержать несколько записей. Существующая запись сохранится.",
      confirmLabel: "Изменить",
      destructive: toSingleton,
    }))) return;
    changeModel((current) => updateDataModelEntityCardinality(current, entity.id, cardinality));
  };
  const deleteEntity = async () => {
    const impacts = dataModelEntityBindingImpacts(config, entity.id);
    if (!(await confirm({
      title: "Удалить модель?",
      description: `Модель «${entity.name}» и её поля будут удалены в дочернем снапшоте. Записи останутся в «Контенте» до явной очистки.${describeBindingImpacts(impacts)}`,
      confirmLabel: "Удалить модель",
      destructive: true,
    }))) return;
    changeModel((current) => removeDataModelEntity(current, entity.id));
    navigate(`/site/data-model${suffix}`);
  };

  return <div className="h-full min-h-0 overflow-y-auto bg-background">
    <div className="mx-auto w-full max-w-5xl px-4 py-5 sm:px-7 sm:py-7">
      <nav aria-label="Путь к модели" className="flex min-w-0 flex-wrap items-center gap-1 text-sm text-muted-foreground">
        <Link to={`/site/data-model${suffix}`} className="inline-flex min-h-11 items-center gap-1.5 px-1 hover:text-foreground"><ArrowLeft className="size-4" />Модель данных</Link>
        <span aria-hidden="true">/</span>
        <span className="min-w-0 truncate px-1 text-foreground" aria-current="page">{entity.name}</span>
      </nav>

      <header className="mt-3 border-b border-border pb-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <h1 className="min-w-0 flex-1">
            <label className="sr-only" htmlFor={dataModelEntityNameControlID(entity.id)}>Название модели</label>
            <input
              id={dataModelEntityNameControlID(entity.id)}
              autoFocus
              className="block w-full min-w-0 rounded-sm border-0 bg-transparent p-0 text-2xl font-semibold tracking-tight outline-none focus-visible:ring-2 focus-visible:ring-ring"
              value={entity.name}
              maxLength={120}
              onChange={(event) => changeModel((current) => renameDataModelEntity(current, entity.id, event.currentTarget.value))}
            />
          </h1>
          <Button type="button" variant="ghost" className="min-h-11 text-destructive" onClick={() => void deleteEntity()}><Trash2 />Удалить модель</Button>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-2">
          <span className="text-xs text-muted-foreground">Записей</span>
          <label className="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded-md border border-border px-3 text-sm">
            <input type="radio" name="entity-cardinality" checked={(entity.cardinality ?? "many") === "many"} onChange={() => void changeCardinality("many")} />
            Коллекция
          </label>
          <label className="inline-flex min-h-11 cursor-pointer items-center gap-2 rounded-md border border-border px-3 text-sm">
            <input type="radio" name="entity-cardinality" checked={entity.cardinality === "singleton"} onChange={() => void changeCardinality("singleton")} />
            Одна запись
          </label>
          <span className="text-xs text-muted-foreground">{entity.fields.length} {entity.fields.length === 1 ? "поле" : "полей"}</span>
        </div>
      </header>

      <section aria-labelledby="model-fields-title" className="py-6">
        <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border pb-3">
          <div>
            <h2 id="model-fields-title" className="text-lg font-semibold">Поля</h2>
            <p className="mt-1 text-sm text-muted-foreground">Порядок полей определяет порядок формы записи.</p>
          </div>
          <Button type="button" id={dataModelAddFieldControlID(entity.id)} className="min-h-11" onClick={addField}><Plus />Добавить поле</Button>
        </div>

        {entity.fields.length === 0 ? <div className="border-b border-dashed border-border py-12 text-center">
          <p className="font-medium">Полей пока нет</p>
          <p className="mt-1 text-sm text-muted-foreground">Добавьте первое поле, чтобы редактор контента мог создать форму записи.</p>
          <Button type="button" variant="outline" className="mt-5 min-h-11" onClick={addField}><Plus />Добавить поле</Button>
        </div> : <SortableList
          items={entity.fields.map((field) => field.id)}
          onReorder={(orderedIDs) => changeModel((current) => reorderDataModelFields(current, entity.id, orderedIDs))}
          getItemLabel={(id) => entity.fields.find((field) => field.id === id)?.name || "поле"}
        >
          <ol className="divide-y divide-border border-b border-border" aria-label={`Поля модели ${entity.name}`}>
            {entity.fields.map((field, index) => <SortableItem key={field.id} id={field.id}>{(handle) => <div className="space-y-3 py-4">
              <div className="flex min-w-0 items-center gap-1">
                <Button
                  ref={handle.setActivatorNodeRef}
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-11 shrink-0 touch-none cursor-grab text-muted-foreground active:cursor-grabbing"
                  aria-label={`Перетащить поле ${field.name}`}
                  {...handle.attributes}
                  {...handle.listeners}
                ><GripVertical /></Button>
                <div className="min-w-0 flex-1">
                  <label className="sr-only" htmlFor={dataModelFieldNameControlID(field.id)}>Название поля {index + 1}</label>
                  <Input id={dataModelFieldNameControlID(field.id)} className="min-h-11" value={field.name} maxLength={120} onChange={(event) => changeModel((current) => renameDataModelField(current, entity.id, field.id, event.currentTarget.value))} />
                </div>
                <Button type="button" size="icon" variant="ghost" className="size-11 shrink-0" aria-label={`Переместить ${field.name} выше`} disabled={index === 0} onClick={() => changeModel((current) => moveDataModelField(current, entity.id, field.id, -1))}><ArrowUp /></Button>
                <Button type="button" size="icon" variant="ghost" className="size-11 shrink-0" aria-label={`Переместить ${field.name} ниже`} disabled={index === entity.fields.length - 1} onClick={() => changeModel((current) => moveDataModelField(current, entity.id, field.id, 1))}><ArrowDown /></Button>
                <Button type="button" size="icon" variant="ghost" className="size-11 shrink-0 text-destructive" aria-label={`Удалить поле ${field.name}`} onClick={() => void removeField(field)}><Trash2 /></Button>
              </div>

              <div className="grid gap-3 pl-12 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]">
                <label className="space-y-1.5 text-sm">
                  <span className="text-xs text-muted-foreground">Тип</span>
                  <select className="h-11 w-full rounded-md border border-input bg-background px-3 text-sm" value={field.type} onChange={(event) => void changeFieldType(field, event.currentTarget.value as DataModelFieldType)}>
                    {fieldTypes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                  </select>
                </label>
                <div className="flex flex-wrap items-center gap-4">
                  <label className="flex min-h-11 items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      className="size-4 accent-primary"
                      disabled={field.type === "longText" || field.type === "media" || field.type === "boolean"}
                      checked={Boolean(field.unique)}
                      onChange={(event) => changeModel((current) => updateDataModelField(current, entity.id, field.id, (item) => ({ ...item, unique: event.currentTarget.checked })))}
                    />
                    Уникальное значение
                  </label>
                </div>
              </div>

              {field.type === "enum" && <label className="block space-y-1.5 pl-12 text-sm">
                <span className="text-xs text-muted-foreground">Варианты, по одному на строку</span>
                <textarea
                  className="min-h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                  value={(field.options ?? []).join("\n")}
                  onChange={(event) => changeModel((current) => updateDataModelField(current, entity.id, field.id, (item) => ({ ...item, options: parseDataModelEnumOptions(event.currentTarget.value) })))}
                />
              </label>}
            </div>}</SortableItem>)}
          </ol>
        </SortableList>}
      </section>
    </div>
  </div>;
}
