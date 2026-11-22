import { Link } from "react-router-dom";
import { ChevronDown } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatSnapshotDate, snapshotCreationLines, snapshotCreatorLabel } from "@/domains/snapshots/provenance";
import type { SiteSnapshot } from "@/types";

interface SnapshotTreeItemProps {
  snapshot: SiteSnapshot;
  canCreate: boolean;
  busy: boolean;
  onCreateChild: (snapshotId: string) => void;
  onDelete: (snapshotId: string) => void;
}

export function SnapshotTreeItem({ snapshot, canCreate, busy, onCreateChild, onDelete }: SnapshotTreeItemProps) {
  const ready = snapshot.state === "ready";
  const context = `snapshot=${encodeURIComponent(snapshot.id)}&page_id=${encodeURIComponent(snapshot.pageId)}`;
  const parentLabel = snapshot.parentSnapshotId
    ? snapshot.parentSequence && snapshot.parentName
      ? `Родитель: #${snapshot.parentSequence} · ${snapshot.parentName}`
      : "Есть родительский снапшот"
    : "Корневой снапшот";

  return (
    <li>
      <details className="group">
        <summary className="flex min-h-16 cursor-pointer list-none items-center gap-3 px-4 py-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
          <ChevronDown className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" aria-hidden="true" />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">#{snapshot.sequence} · {snapshot.name}</span>
            <span className="mt-0.5 block truncate text-xs text-muted-foreground">{parentLabel} · page_id {snapshot.pageId} · {formatSnapshotDate(snapshot.createdAt)}</span>
          </span>
          {snapshot.state === "provisioning" && <Badge variant="warning">Готовится</Badge>}
          {snapshot.state === "failed" && <Badge variant="destructive">Ошибка подготовки</Badge>}
        </summary>
        <div className="border-t border-border/70 px-4 pb-4 pt-3">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="text-xs text-muted-foreground">Инициатор: {snapshotCreatorLabel(snapshot)}</p>
              {!ready && <p role="status" className="mt-1 text-xs text-warning">{snapshot.state === "provisioning" ? "Копируется содержимое родительского снапшота. Редактирование и публикация будут доступны после завершения." : "Подготовка не завершилась. Снапшот нельзя редактировать или публиковать."}</p>}
              {snapshot.creationSummary && (
                <ul aria-label={`Состав изменений снапшота #${snapshot.sequence}`} className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                  {snapshotCreationLines(snapshot.creationSummary).map((line, index) => <li key={`${index}-${line}`}>{line}</li>)}
                </ul>
              )}
            </div>
          </div>
          <div role="group" aria-label={`Действия для снапшота #${snapshot.sequence}`} className="mt-3 flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!ready || !canCreate || busy}
              title={!canCreate ? "Введите название дочернего снапшота" : undefined}
              onClick={() => onCreateChild(snapshot.id)}
            >
              {busy ? "Создаём…" : "Создать потомка"}
            </Button>
            {ready && <>
              <Button type="button" size="sm" variant="outline" asChild><Link to={`/site/pages?${context}`}>Страницы</Link></Button>
              <Button type="button" size="sm" variant="outline" asChild><Link to={`/site/features?${context}`}>Состав фичей</Link></Button>
              <Button type="button" size="sm" variant="outline" asChild><Link to={`/content?${context}`}>Контент</Link></Button>
            </>}
            <Button type="button" size="sm" variant="ghost" className="text-destructive hover:text-destructive" disabled={busy || snapshot.state === "provisioning"} title="Проверки защиты удаления выполняет сервер" onClick={() => onDelete(snapshot.id)}>Удалить</Button>
          </div>
        </div>
      </details>
    </li>
  );
}
