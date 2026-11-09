import { ArrowDown, ArrowUp } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/input";
import type { DataModel, ModelContentSelection, ModelContentWorkspace } from "@/types";
import {
  modelRecordTitle,
  removeModelContentSelection,
  reorderSelectedRecords,
  setModelContentSelection,
} from "../../model-content-workspace";
import type { ModelContentDraft, ModelContentDraftUpdater } from "./types";

export interface PlacementSelectionsProps {
  model: DataModel;
  workspace: ModelContentWorkspace;
  draft: ModelContentDraft;
  savedDraft: ModelContentDraft;
  disabled: boolean;
  updateDraft: ModelContentDraftUpdater;
  confirmCleanup?: (title: string, description: string) => Promise<boolean>;
}

export function PlacementSelections({
  model,
  workspace,
  draft,
  savedDraft,
  disabled,
  updateDraft,
  confirmCleanup,
}: PlacementSelectionsProps) {
  if (workspace.placements.length === 0) return null;
  return <section className="min-w-0 space-y-3 rounded-xl border border-border bg-card p-4">
    <div><h2 className="font-semibold">Записи размещений</h2><p className="text-sm text-muted-foreground">Выберите публичные записи отдельно для каждого размещения.</p></div>
    {workspace.placements.flatMap((placement) => placement.inputs.map((input) => {
      const entity = model.entities.find((item) => item.id === input.entityId);
      const candidates = draft.records.filter((record) => record.entityId === input.entityId).sort((a, b) => a.sortOrder - b.sortOrder);
      const persistedIDs = new Set(savedDraft.records.map((record) => record.id));
      const selection = draft.selections.find((item) => item.placementId === placement.id && item.inputKey === input.inputKey);
      const mode = selection?.mode ?? (input.kind === "collection" ? "all" : "ids");
      const selectedIds = selection?.recordIds ?? [];
      const labelId = `placement-input-${placement.id}-${input.inputKey}`;
      if (!entity) return <article key={`${placement.id}:${input.inputKey}`} className="space-y-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm" role="alert">
        <p>{placement.pageName} · {placement.featureName}: сущность, связанная с «{input.inputKey}», недоступна. Исправьте связь в редакторе сайта.</p>
        {selection && <Button type="button" size="sm" variant="outline" className="min-h-11 text-destructive" disabled={disabled || !confirmCleanup} onClick={async () => {
          const accepted = await confirmCleanup?.("Удалить выбор недоступной сущности?", `Выбор для «${placement.pageName} · ${placement.featureName}» будет удалён из черновика. Публичные страницы изменятся только после отдельной публикации.`);
          if (!accepted) return;
          updateDraft((current) => ({ ...current, selections: removeModelContentSelection(current.selections, placement.id, input.inputKey) }));
        }}>Удалить выбор недоступной сущности</Button>}
      </article>;
      const inputLabel = `${placement.pageName} · ${placement.featureName} · ${input.inputKey}`;
      const candidateIDs = new Set(candidates.filter((record) => persistedIDs.has(record.id)).map((record) => record.id));
      const singleton = entity.cardinality === "singleton";
      const invalidSelectedIDs = selectedIds.filter((id) => !candidateIDs.has(id));
      const invalidSelectionShape = Boolean(selection && (
        (singleton && input.kind !== "collection")
        || input.lookup
        || (selection.mode === "all" && (input.kind !== "collection" || selectedIds.length > 0))
        || (selection.mode === "ids" && input.kind !== "collection" && selectedIds.length !== 1)
        || (singleton && input.kind === "collection" && selection.mode === "ids" && selectedIds.length > 1)
      ));
      const selectionNeedsCleanup = invalidSelectedIDs.length > 0 || invalidSelectionShape;
      const setSelection = (nextMode: ModelContentSelection["mode"], ids = selectedIds) => updateDraft((current) => ({
        ...current,
        selections: setModelContentSelection(current.selections, placement.id, input.inputKey, nextMode, ids),
      }));
      const setRecordSelection = (recordId: string) => updateDraft((current) => ({
        ...current,
        selections: recordId
          ? setModelContentSelection(current.selections, placement.id, input.inputKey, "ids", [recordId])
          : removeModelContentSelection(current.selections, placement.id, input.inputKey),
      }));
      const clearInvalidSelection = async () => {
        const accepted = await confirmCleanup?.(
          "Очистить некорректный выбор записей?",
          `Выбор для размещения «${placement.pageName} · ${placement.featureName}» будет исправлен в черновике. Недоступные записи и несовместимый выбор удалятся; публичные страницы изменятся только после отдельной публикации.`,
        );
        if (!accepted) return;
        updateDraft((current) => {
          const currentSelection = current.selections.find((item) => item.placementId === placement.id && item.inputKey === input.inputKey);
          if (!currentSelection) return current;
          const currentIDs = currentSelection.recordIds ?? [];
          const shapeInvalid = Boolean(
            (singleton && input.kind !== "collection")
            || input.lookup
            || (currentSelection.mode === "all" && (input.kind !== "collection" || currentIDs.length > 0))
            || (currentSelection.mode === "ids" && input.kind !== "collection" && currentIDs.length !== 1)
            || (singleton && input.kind === "collection" && currentSelection.mode === "ids" && currentIDs.length > 1),
          );
          if (shapeInvalid) return { ...current, selections: removeModelContentSelection(current.selections, placement.id, input.inputKey) };
          const validIDs = currentIDs.filter((id) => candidateIDs.has(id));
          if (currentSelection.mode === "ids" && input.kind !== "collection" && validIDs.length !== 1) {
            return { ...current, selections: removeModelContentSelection(current.selections, placement.id, input.inputKey) };
          }
          return {
            ...current,
            selections: setModelContentSelection(current.selections, placement.id, input.inputKey, currentSelection.mode, validIDs),
          };
        });
      };
      const lookupField = input.lookup ? entity.fields.find((field) => field.id === input.lookup?.fieldId) : null;
      return <article key={`${placement.id}:${input.inputKey}`} className="min-w-0 space-y-3 rounded-lg border border-border p-3">
        <div className="min-w-0"><Label id={labelId} className="text-sm font-medium">{inputLabel}</Label><p className="text-xs text-muted-foreground">{entity.name} · {input.kind === "collection" ? "список записей" : input.kind === "scalar" ? "значение из записи" : "одна запись"}</p></div>
        {selectionNeedsCleanup && <div role="alert" className="space-y-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm"><p>{invalidSelectedIDs.length > 0 ? `Недоступные выбранные записи: ${invalidSelectedIDs.length}. Некоторые ещё не сохранены.` : input.lookup ? "Для входа по маршруту сохранён лишний ручной выбор записей." : singleton && input.kind !== "collection" ? "Единственная запись связывается автоматически; ручной выбор для этого входа не нужен." : singleton ? "Для singleton-списка можно выбрать не более одной записи." : "Формат сохранённого выбора не подходит этому входу."} Исправьте выбор, чтобы сохранить черновик.</p><Button type="button" size="sm" variant="outline" className="min-h-11 text-destructive" disabled={disabled || !confirmCleanup} onClick={() => void clearInvalidSelection()}>Очистить некорректный выбор</Button></div>}
        {input.kind !== "collection" ? (
          input.kind === "record" && input.lookup ? (
            <p role="note" className="rounded-md bg-muted/40 p-3 text-sm text-muted-foreground">Запись определяется по маршруту через аргумент «{input.lookup.argument}» и уникальное поле «{lookupField?.name ?? "удалённое поле"}». Ручной выбор не требуется.</p>
          ) : singleton ? (
            <p role="note" className="rounded-md bg-muted/40 p-3 text-sm text-muted-foreground">{input.kind === "scalar" ? "Значение автоматически берётся из единственной опубликованной записи." : "Единственная опубликованная запись связывается автоматически."} Ручной выбор не требуется.</p>
          ) : (
            <div className="space-y-1.5"><Label htmlFor={`${labelId}-record`} className="text-xs">{input.kind === "scalar" ? "Запись для значения" : "Запись"}</Label><select id={`${labelId}-record`} aria-labelledby={labelId} value={mode === "ids" && candidateIDs.has(selectedIds[0] ?? "") ? selectedIds[0] : ""} onChange={(event) => setRecordSelection(event.currentTarget.value)} disabled={disabled} className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"><option value="">Не выбрано — пустой вход</option>{candidates.map((record) => <option key={record.id} value={record.id} disabled={!persistedIDs.has(record.id)}>{modelRecordTitle(record, entity)}{persistedIDs.has(record.id) ? "" : " · сначала сохраните"}</option>)}</select></div>
          )
        ) : (
          <div className="space-y-3">
            <div className="flex flex-wrap gap-4">
              <label className="flex min-h-11 items-center gap-2 text-sm"><input type="radio" name={`${labelId}-mode`} checked={mode === "all"} disabled={disabled} onChange={() => setSelection("all")} />Все опубликованные записи</label>
              <label className="flex min-h-11 items-center gap-2 text-sm"><input type="radio" name={`${labelId}-mode`} checked={mode === "ids"} disabled={disabled} onChange={() => setSelection("ids", [])} />{singleton ? "Выбрать 0–1 запись" : "Выбрать записи"}</label>
            </div>
            {mode === "ids" && (candidates.length === 0 ? <p className="text-sm text-muted-foreground">В этой сущности пока нет записей.</p> : <div className="space-y-2">
              <ul className="max-h-64 space-y-1 overflow-auto" aria-label={`Выбор записей для ${inputLabel}`}>
                {candidates.map((record) => { const persisted = persistedIDs.has(record.id); return <li key={record.id}><label className="flex min-h-11 min-w-0 items-center gap-3 rounded-md px-2 hover:bg-muted/50"><input type="checkbox" checked={selectedIds.includes(record.id)} disabled={disabled || !persisted} onChange={() => setSelection("ids", selectedIds.includes(record.id) ? selectedIds.filter((id) => id !== record.id) : singleton ? [record.id] : [...selectedIds, record.id])} /><span className="min-w-0 truncate text-sm">{modelRecordTitle(record, entity)}{persisted ? "" : <span className="text-muted-foreground"> · сначала сохраните</span>}</span></label></li>; })}
              </ul>
              {!singleton && selectedIds.length > 0 && <ol className="space-y-1 border-t border-border pt-2" aria-label={`Порядок записей для ${inputLabel}`}>
                {selectedIds.map((id, index) => {
                  const record = candidates.find((item) => item.id === id);
                  if (!record) return null;
                  return <li key={id} className="flex min-w-0 items-center gap-2"><span className="w-6 text-center text-xs text-muted-foreground">{index + 1}</span><span className="min-w-0 flex-1 truncate text-sm">{modelRecordTitle(record, entity)}</span><Button type="button" size="icon-sm" variant="ghost" className="min-h-11 min-w-11" aria-label={`Переместить ${modelRecordTitle(record, entity)} выше в списке`} disabled={disabled || index === 0} onClick={() => setSelection("ids", reorderSelectedRecords(selectedIds, id, -1))}><ArrowUp /></Button><Button type="button" size="icon-sm" variant="ghost" className="min-h-11 min-w-11" aria-label={`Переместить ${modelRecordTitle(record, entity)} ниже в списке`} disabled={disabled || index === selectedIds.length - 1} onClick={() => setSelection("ids", reorderSelectedRecords(selectedIds, id, 1))}><ArrowDown /></Button></li>;
                })}
              </ol>}
            </div>)}
          </div>
        )}
      </article>;
    }))}
  </section>;
}
