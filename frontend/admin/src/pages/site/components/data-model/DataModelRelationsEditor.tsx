import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { DataModel, DataModelRelation } from "@/types";
import {
  DATA_MODEL_NEW_RELATION_FROM_CONTROL_ID,
  cardinalities,
  findDataModelRelationConflict,
  dataModelRelationNameControlID,
  validModelName,
} from "./model-operations";

interface Props {
  model: DataModel;
  onAddRelation: (relation: Omit<DataModelRelation, "id">) => void;
  onChangeCardinality: (relation: DataModelRelation, cardinality: DataModelRelation["cardinality"]) => void;
  onRemoveRelation: (relation: DataModelRelation) => void;
  onRenameRelation: (relationID: string, name: string) => void;
}

export function DataModelRelationsEditor({ model, onAddRelation, onChangeCardinality, onRemoveRelation, onRenameRelation }: Props) {
  const [relationFrom, setRelationFrom] = useState("");
  const [relationTo, setRelationTo] = useState("");
  const [relationName, setRelationName] = useState("");
  const [relationCardinality, setRelationCardinality] = useState<DataModelRelation["cardinality"]>("one-to-many");

  useEffect(() => {
    if (relationFrom && !model.entities.some((entity) => entity.id === relationFrom)) setRelationFrom("");
    if (relationTo && !model.entities.some((entity) => entity.id === relationTo)) setRelationTo("");
  }, [model.entities, relationFrom, relationTo]);

  const proposedRelation = {
    name: relationName,
    fromEntityId: relationFrom,
    toEntityId: relationTo,
    cardinality: relationCardinality,
  } satisfies Omit<DataModelRelation, "id">;
  const relationConflict = findDataModelRelationConflict(model, proposedRelation);
  const canAdd = Boolean(
    model.entities.some((entity) => entity.id === relationFrom) &&
    model.entities.some((entity) => entity.id === relationTo) &&
    validModelName(relationName) &&
    !relationConflict,
  );
  const formError = relationConflict
    ? "Такая связь уже есть. Измените название или направление."
    : relationName.length > 0 && !validModelName(relationName)
      ? "Укажите название длиной до 120 символов."
      : null;
  const formErrorID = "data-model-new-relation-error";
  const formDescribedBy = formError ? formErrorID : undefined;

  return <section className="space-y-3 rounded-xl border border-border p-4" aria-labelledby="data-model-relations-heading">
    <div>
      <h2 id="data-model-relations-heading" className="font-medium">Связи сущностей</h2>
      <p className="mt-1 text-sm text-muted-foreground">Связь описывает отношение между типами записей. Значения связей будут выбираться в редакторе контента.</p>
    </div>
    {model.entities.length < 2 ? <p className="text-sm text-muted-foreground">Добавьте ещё одну сущность, чтобы связать данные.</p> : <fieldset className="grid gap-3 rounded-lg border border-border p-3 md:grid-cols-[1fr_1fr_1fr_1fr_auto]">
      <legend className="px-1 text-sm font-medium">Новая связь</legend>
      <label className="space-y-1 text-sm">
        <span>От сущности</span>
        <select id={DATA_MODEL_NEW_RELATION_FROM_CONTROL_ID} className="min-h-11 w-full rounded-md border border-input bg-background px-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" aria-invalid={Boolean(relationConflict)} aria-describedby={formDescribedBy} value={relationFrom} onChange={(event) => setRelationFrom(event.currentTarget.value)}>
          <option value="">Выберите</option>{model.entities.map((entity) => <option key={entity.id} value={entity.id}>{entity.name || "Без названия"}</option>)}
        </select>
      </label>
      <label className="space-y-1 text-sm">
        <span>К сущности</span>
        <select id="data-model-new-relation-to" className="min-h-11 w-full rounded-md border border-input bg-background px-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" aria-invalid={Boolean(relationConflict)} aria-describedby={formDescribedBy} value={relationTo} onChange={(event) => setRelationTo(event.currentTarget.value)}>
          <option value="">Выберите</option>{model.entities.map((entity) => <option key={entity.id} value={entity.id}>{entity.name || "Без названия"}</option>)}
        </select>
      </label>
      <label className="space-y-1 text-sm">
        <span>Тип связи</span>
        <select id="data-model-new-relation-cardinality" className="min-h-11 w-full rounded-md border border-input bg-background px-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" value={relationCardinality} onChange={(event) => setRelationCardinality(event.currentTarget.value as DataModelRelation["cardinality"])}>
          {cardinalities.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
      <label className="space-y-1 text-sm">
        <span>Название связи</span>
        <Input id="data-model-new-relation-name" className="min-h-11" required aria-invalid={Boolean(formError)} aria-describedby={formDescribedBy} value={relationName} onChange={(event) => setRelationName(event.currentTarget.value)} placeholder="Например: содержит" />
      </label>
      <Button type="button" className="min-h-11 self-end" disabled={!canAdd} onClick={() => {
        if (!canAdd) return;
        onAddRelation(proposedRelation);
        setRelationName("");
      }}><Plus className="size-4" />Связать</Button>
      {formError && <p id={formErrorID} className="col-span-full text-sm text-destructive" role="alert">{formError}</p>}
    </fieldset>}

    {(model.relations ?? []).length > 0 && <ul aria-label="Связи модели" className="divide-y divide-border rounded-lg border border-border">
      {(model.relations ?? []).map((relation) => {
        const from = model.entities.find((entity) => entity.id === relation.fromEntityId)?.name ?? "Удалённая сущность";
        const to = model.entities.find((entity) => entity.id === relation.toEntityId)?.name ?? "Удалённая сущность";
        const hasDuplicate = Boolean(findDataModelRelationConflict(model, relation, relation.id));
        const nameError = hasDuplicate
          ? "Такая связь уже есть. Измените название или направление."
          : !validModelName(relation.name)
            ? "Укажите название длиной до 120 символов."
            : null;
        const nameErrorID = `data-model-relation-name-error-${relation.id}`;
        return <li key={relation.id} className="grid gap-2 p-3 sm:grid-cols-[minmax(0,1fr)_14rem_auto] sm:items-end">
          <label className="space-y-1 text-sm">
            <span className="text-muted-foreground">Связь: {from} → {to}</span>
            <Input id={dataModelRelationNameControlID(relation.id)} className="min-h-10" required aria-invalid={Boolean(nameError)} aria-describedby={nameError ? nameErrorID : undefined} aria-label={`Название связи ${from} — ${to}`} value={relation.name} onChange={(event) => onRenameRelation(relation.id, event.currentTarget.value)} />
            {nameError && <span id={nameErrorID} className="text-destructive" role="alert">{nameError}</span>}
          </label>
          <label className="space-y-1 text-sm">
            <span className="text-muted-foreground">Кардинальность</span>
            <select className="min-h-10 w-full rounded-md border border-input bg-background px-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2" aria-label={`Тип связи ${from} — ${relation.name} — ${to}`} value={relation.cardinality} onChange={(event) => onChangeCardinality(relation, event.currentTarget.value as DataModelRelation["cardinality"])}>
              {cardinalities.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          <Button type="button" variant="ghost" className="min-h-10 justify-self-start text-destructive sm:justify-self-end" aria-label={`Удалить связь ${from} — ${relation.name} — ${to}`} onClick={() => onRemoveRelation(relation)}>Удалить связь</Button>
        </li>;
      })}
    </ul>}
  </section>;
}
