import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { formatSnapshotDate, snapshotCreationLines, snapshotCreatorLabel } from "@/domains/snapshots/provenance";
import type { SiteSnapshot } from "@/types";
import { SitepackExportAction } from "../../SitepackActions";

interface SnapshotTreeItemProps {
  snapshot: SiteSnapshot;
  assignedBranchNames: string[];
  protectedSnapshot: boolean;
  canCreate: boolean;
  busy: boolean;
  onCreateChild: (snapshotId: string) => void;
  onDelete: (snapshotId: string) => void;
}

export function SnapshotTreeItem({
  snapshot,
  assignedBranchNames,
  protectedSnapshot,
  canCreate,
  busy,
  onCreateChild,
  onDelete,
}: SnapshotTreeItemProps) {
  const parentLabel = snapshot.parentSnapshotId
    ? snapshot.parentSequence && snapshot.parentName
      ? `Родитель: #${snapshot.parentSequence} · ${snapshot.parentName}`
      : "Есть родительский снапшот"
    : "Корневой снапшот";

  return (
    <li className="p-4">
      <article className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-medium">#{snapshot.sequence} · {snapshot.name}</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {parentLabel} · {assignedBranchNames.length ? `Ветки: ${assignedBranchNames.join(", ")}` : "Не назначен ветке"}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            Создан {formatSnapshotDate(snapshot.createdAt)} · Инициатор: {snapshotCreatorLabel(snapshot)}
          </p>
          {snapshot.creationSummary && (
            <ul aria-label={`Состав изменений снапшота #${snapshot.sequence}`} className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
              {snapshotCreationLines(snapshot.creationSummary).map((line, index) => <li key={`${index}-${line}`}>{line}</li>)}
            </ul>
          )}
        </div>
        <div role="group" aria-label={`Действия для снапшота #${snapshot.sequence}`} className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" disabled={!canCreate || busy} onClick={() => onCreateChild(snapshot.id)}>
            {busy ? "Создаём…" : "Создать потомка"}
          </Button>
          <Button type="button" size="sm" variant="outline" asChild>
            <Link to={`/site/pages?snapshot=${snapshot.id}`}>Страницы</Link>
          </Button>
          <Button type="button" size="sm" variant="outline" asChild>
            <Link to={`/site/features?snapshot=${snapshot.id}`}>Состав фичей</Link>
          </Button>
          <Button type="button" size="sm" variant="outline" asChild>
            <Link to={`/content?snapshot=${snapshot.id}`}>Контент</Link>
          </Button>
          <SitepackExportAction snapshot={snapshot} />
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={busy || protectedSnapshot}
            title={protectedSnapshot ? "Используется веткой или нужен для отката" : undefined}
            onClick={() => onDelete(snapshot.id)}
          >
            Удалить
          </Button>
        </div>
      </article>
    </li>
  );
}
