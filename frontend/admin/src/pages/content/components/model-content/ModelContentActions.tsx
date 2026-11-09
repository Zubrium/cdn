import { Clock3 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { SiteSnapshot } from "@/types";
import type { ModelContentRevisionConflict, ModelContentSaveState } from "./types";

export interface ModelContentActionsProps {
  snapshots: SiteSnapshot[];
  selectedSnapshotId: string;
  revision: number;
  publishedRevision: number;
  saveState: ModelContentSaveState;
  saveError: string;
  revisionConflict: ModelContentRevisionConflict | null;
  dirty: boolean;
  invalidCount: number;
  publishPending: boolean;
  canPublish: boolean;
  onSnapshotChange: (snapshotId: string) => void;
  onOpenHistory: () => void;
  onSave: () => void;
  onPublish: () => void;
  onDownloadLocalDraft: () => void;
  onReloadServerDraft: () => void;
  onRetrySave: () => void;
}

function displaySnapshot(snapshot: SiteSnapshot): string {
  return `#${snapshot.sequence} · ${snapshot.name}`;
}

export function ModelContentActions({
  snapshots,
  selectedSnapshotId,
  revision,
  publishedRevision,
  saveState,
  saveError,
  revisionConflict,
  dirty,
  invalidCount,
  publishPending,
  canPublish,
  onSnapshotChange,
  onOpenHistory,
  onSave,
  onPublish,
  onDownloadLocalDraft,
  onReloadServerDraft,
  onRetrySave,
}: ModelContentActionsProps) {
  return <>
    <header className="flex min-w-0 flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
      <div className="min-w-0 space-y-1">
        <p className="text-sm text-muted-foreground">Контент снапшота</p>
        <h1 className="truncate text-2xl font-semibold">Контент сайта</h1>
        <p className="text-sm text-muted-foreground">Записи, связи и публикации. Поля редакционного контента необязательны.</p>
      </div>
      <div className="flex w-full min-w-0 flex-wrap items-center gap-2 lg:w-auto">
        <Select value={selectedSnapshotId} onValueChange={onSnapshotChange} disabled={saveState === "saving" || publishPending}>
          <SelectTrigger aria-label="Выбрать снапшот" className="min-w-0 flex-1 lg:w-64 lg:flex-none"><SelectValue /></SelectTrigger>
          <SelectContent>{snapshots.map((snapshot) => <SelectItem key={snapshot.id} value={snapshot.id}>{displaySnapshot(snapshot)}</SelectItem>)}</SelectContent>
        </Select>
        <Button variant="outline" disabled={saveState === "saving" || publishPending} onClick={onOpenHistory}><Clock3 />История</Button>
        <Button variant="outline" disabled={!dirty || saveState === "saving" || publishPending || invalidCount > 0 || saveState === "conflict"} onClick={onSave}>
          {saveState === "saving" ? "Сохранение…" : "Сохранить"}
        </Button>
        {canPublish && <Button disabled={saveState === "saving" || publishPending || invalidCount > 0 || saveState === "conflict" || (revision <= publishedRevision && !dirty)} onClick={onPublish}>
          {publishPending ? "Публикация…" : "Опубликовать"}
        </Button>}
      </div>
    </header>

    <div className="flex min-w-0 flex-wrap items-center gap-2" aria-live="polite">
      <Badge variant={saveState === "error" || saveState === "conflict" || saveState === "invalid" ? "destructive" : dirty ? "warning" : "success"}>
        {saveState === "saving" ? "Сохранение…" : saveState === "conflict" ? "Конфликт ревизии" : saveState === "error" ? "Ошибка сохранения" : dirty ? "Есть несохранённые изменения" : "Черновик сохранён"}
      </Badge>
      <span className="text-xs text-muted-foreground">Черновик {revision} · опубликовано {publishedRevision}</span>
      {saveState === "saved" && <span className="text-xs text-muted-foreground">Сохранено</span>}
    </div>

    {saveError && <section role="alert" className="space-y-2 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm">
      <p>{saveError}</p>
      {saveState === "conflict" && <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" onClick={onDownloadLocalDraft}>Скачать локальный черновик</Button>
        <Button size="sm" variant="ghost" onClick={onReloadServerDraft}>Загрузить сохранённую ревизию</Button>
      </div>}
      {saveState === "conflict" && revisionConflict && <p role="status" className="text-xs font-medium" aria-live="polite">
        Локальная ревизия: {revisionConflict.local} · серверная ревизия: {revisionConflict.server ?? "загружается"}
      </p>}
      {saveState === "error" && <Button size="sm" variant="outline" disabled={invalidCount > 0} onClick={onRetrySave}>Повторить сохранение</Button>}
    </section>}

    {invalidCount > 0 && <p role="alert" className="rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">Проверьте введённые значения ({invalidCount}). Поля пустыми оставлять можно.</p>}
  </>;
}
