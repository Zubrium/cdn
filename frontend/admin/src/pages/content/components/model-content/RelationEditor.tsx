import { useMemo, useState } from "react";
import { Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { DataModelEntity, DataModelRelation, ModelContentRecord } from "@/types";
import { modelRecordTitle } from "../../model-content-workspace";

export interface RelationEditorProps {
  relation: DataModelRelation;
  targetEntity: DataModelEntity;
  targetRecords: ModelContentRecord[];
  currentRecord: ModelContentRecord;
  persistedRecordIDs: ReadonlySet<string>;
  recordIDsOwnedByOther?: ReadonlySet<string>;
  disabled?: boolean;
  onChange: (recordIDs: string[]) => void;
  onOpenTargetRecord?: (record: ModelContentRecord) => void;
  onConfirmCleanup?: (title: string, description: string) => Promise<boolean>;
  validationIssues?: string[];
}

export function RelationEditor({
  relation,
  targetEntity,
  targetRecords,
  currentRecord,
  persistedRecordIDs,
  recordIDsOwnedByOther = new Set<string>(),
  disabled = false,
  onChange,
  onOpenTargetRecord,
  onConfirmCleanup,
  validationIssues = [],
}: RelationEditorProps) {
  const [search, setSearch] = useState("");
  const currentIDs = currentRecord.relations?.[relation.id] ?? [];
  const targetIDs = new Set(targetRecords.map((record) => record.id));
  const unavailableIDs = currentIDs.filter((id) => !targetIDs.has(id));
  const duplicateOwnerIDs = currentIDs.filter((id) => recordIDsOwnedByOther.has(id));
  const single = relation.cardinality === "one-to-one";
  const visibleTargetRecords = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("ru");
    return targetRecords.filter((record) => !needle || modelRecordTitle(record, targetEntity).toLocaleLowerCase("ru").includes(needle));
  }, [search, targetEntity, targetRecords]);

  const clearUnavailable = async () => {
    const accepted = await onConfirmCleanup?.(
      "Удалить недоступные связи?",
      `Недоступных ссылок для удаления: ${unavailableIDs.length}. Значения других записей не изменятся.`,
    );
    if (!accepted) return;
    onChange(currentIDs.filter((id) => targetIDs.has(id)));
  };

  const clearDuplicateOwners = async () => {
    const accepted = await onConfirmCleanup?.(
      "Удалить занятые связи?",
      `Повторных связей для удаления: ${duplicateOwnerIDs.length}. Это исправит конфликт в черновике; опубликованные данные изменятся только после отдельной публикации.`,
    );
    if (!accepted) return;
    onChange(currentIDs.filter((id) => !recordIDsOwnedByOther.has(id)));
  };

  return <fieldset className="min-w-0 space-y-2 border-t border-border pt-4">
    <legend className="text-sm font-medium">{relation.name} <span className="font-normal text-muted-foreground">· {targetEntity.name}</span></legend>
    {unavailableIDs.length > 0 && <div role="alert" className="space-y-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
      <p>Недоступных связанных записей: {unavailableIDs.length}. Их больше нет в сущности «{targetEntity.name}». Удалите ссылки, чтобы сохранить черновик.</p>
      <Button type="button" variant="outline" size="sm" className="min-h-11 text-destructive" disabled={disabled || !onConfirmCleanup} onClick={() => void clearUnavailable()}>Удалить недоступные ссылки</Button>
    </div>}
    {duplicateOwnerIDs.length > 0 && <div role="alert" className="space-y-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
      <p>Конфликтующих связанных записей: {duplicateOwnerIDs.length}. Они уже назначены другим исходным записям. Удалите повторные связи, чтобы сохранить черновик.</p>
      <Button type="button" variant="outline" size="sm" className="min-h-11 text-destructive" disabled={disabled || !onConfirmCleanup} onClick={() => void clearDuplicateOwners()}>Удалить повторные связи</Button>
    </div>}
    {validationIssues.length > 0 && <ul role="alert" className="space-y-1 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{validationIssues.map((message) => <li key={message}>{message}</li>)}</ul>}
    {targetRecords.length === 0 ? <p className="text-sm text-muted-foreground">Сначала добавьте записи сущности «{targetEntity.name}».</p> : (
      <div className="space-y-2">
        {targetRecords.length > 8 && <div className="relative"><Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input value={search} onChange={(event) => setSearch(event.currentTarget.value)} placeholder={`Найти запись сущности «${targetEntity.name}»`} aria-label={`Найти запись сущности ${targetEntity.name}`} className="pl-9" /></div>}
        {visibleTargetRecords.length === 0 ? <p className="text-sm text-muted-foreground">Совпадений нет.</p> : <div className="grid gap-2 sm:grid-cols-2">
        {single && <label className="flex min-h-11 min-w-0 cursor-pointer items-center gap-3 rounded-md border border-border px-3 py-2 text-sm">
          <input type="radio" name={`relation-${currentRecord.id}-${relation.id}`} checked={currentIDs.length === 0} disabled={disabled} onChange={() => onChange([])} />
          <span className="text-muted-foreground">Не связано</span>
        </label>}
        {visibleTargetRecords.map((target) => {
          const checked = currentIDs.includes(target.id);
          const persisted = persistedRecordIDs.has(target.id);
          const ownedByOther = recordIDsOwnedByOther.has(target.id);
          const title = modelRecordTitle(target, targetEntity);
          return <div key={target.id} className="flex min-w-0 flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2">
            <label className="flex min-h-11 min-w-0 flex-1 cursor-pointer items-center gap-3 text-sm">
              <input
                type={single ? "radio" : "checkbox"}
                name={`relation-${currentRecord.id}-${relation.id}`}
                checked={checked}
                disabled={disabled || !persisted || (ownedByOther && !checked)}
                onChange={() => {
                  const nextIDs = single
                    ? [target.id]
                    : checked
                      ? currentIDs.filter((id) => id !== target.id)
                      : [...currentIDs, target.id];
                  onChange(nextIDs);
                }}
              />
              <span className="min-w-0 truncate">{title}{!persisted ? <span className="text-muted-foreground"> · сначала сохраните</span> : ownedByOther && !checked ? <span className="text-muted-foreground"> · уже связана</span> : null}</span>
            </label>
            {onOpenTargetRecord && <Button type="button" variant="ghost" size="sm" className="min-h-11" aria-label={`Открыть запись ${title}`} disabled={disabled} onClick={() => onOpenTargetRecord(target)}>Открыть запись</Button>}
          </div>;
        })}
        </div>}
      </div>
    )}
  </fieldset>;
}
