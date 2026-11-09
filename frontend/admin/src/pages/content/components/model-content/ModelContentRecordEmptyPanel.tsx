import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { DataModelEntity, ModelContentRecord } from "@/types";

export interface ModelContentRecordEmptyPanelProps {
  selectedEntity: DataModelEntity | null;
  recordsForEntity: ModelContentRecord[];
  onAddRecord: () => void;
}

export function ModelContentRecordEmptyPanel({ selectedEntity, recordsForEntity, onAddRecord }: ModelContentRecordEmptyPanelProps) {
  if (selectedEntity?.cardinality === "singleton") {
    return <section role="alert" className="rounded-xl border border-warning/50 bg-warning/10 p-5">
      <h2 className="font-medium">Некорректное количество записей</h2>
      <p className="mt-2 text-sm text-muted-foreground">Для сущности «{selectedEntity.name}» сервер должен предоставить ровно одну запись. Сейчас записей: {recordsForEntity.length}. Данные сохранены; сохранение и публикация заблокированы, пока целостность модели не восстановлена.</p>
      {recordsForEntity.length > 1 && <ul className="mt-3 space-y-1 text-xs text-muted-foreground">{recordsForEntity.map((record) => <li key={record.id} className="break-all">ID записи: {record.id}</li>)}</ul>}
    </section>;
  }

  return <section className="rounded-xl border border-dashed border-border p-8 text-center">
    <h2 className="font-medium">Выберите запись или добавьте новую</h2>
    <p className="mt-2 text-sm text-muted-foreground">Идентификаторы создаются системой и не требуют ручного ввода.</p>
    {selectedEntity && recordsForEntity.length === 0 && <Button className="mt-4" onClick={onAddRecord}><Plus />Добавить запись</Button>}
  </section>;
}
