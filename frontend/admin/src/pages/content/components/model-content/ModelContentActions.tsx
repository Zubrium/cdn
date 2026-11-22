import { Clock3 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { fieldLabelForPath } from "@/domains/content-editor/entry-form/helpers";
import type { WebsiteSnapshot } from "@/types";
import type { ModelContentRecordValidation, ModelContentRevisionConflict, ModelContentSaveState } from "./types";

export interface ModelContentActionsProps {
  snapshots: WebsiteSnapshot[];
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
  validation?: ModelContentRecordValidation[];
  fields?: SchemaFieldNode[];
  selectedRecordId?: string;
  onFocusField?: (path: string) => void;
  onSnapshotChange: (snapshotId: string) => void;
  onOpenHistory: () => void;
  onSave: () => void;
  onPublish: () => void;
  onDownloadLocalDraft: () => void;
  onReloadServerDraft: () => void;
  onRetrySave: () => void;
}

function displaySnapshot(snapshot: WebsiteSnapshot): string {
  return `#${snapshot.sequence} · ${snapshot.name}`;
}

/** Structural paths use a `values.` prefix; entry anchors use the bare field path. */
export function validationFocusTarget(
  fields: SchemaFieldNode[],
  issue: { path: string; message: string },
): { path: string; label: string } {
  const path = issue.path.startsWith("values.") ? issue.path.slice("values.".length) : issue.path;
  return { path, label: fieldLabelForPath(fields, path) };
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
  validation = [],
  fields = [],
  selectedRecordId = "",
  onFocusField,
  onSnapshotChange,
  onOpenHistory,
  onSave,
  onPublish,
  onDownloadLocalDraft,
  onReloadServerDraft,
  onRetrySave,
}: ModelContentActionsProps) {
  const badge = saveState === "saving" ? { variant: "info" as const, label: "Сохранение…" }
    : saveState === "conflict" ? { variant: "destructive" as const, label: "Конфликт ревизии" }
      : saveState === "error" ? { variant: "destructive" as const, label: "Ошибка сохранения" }
        : saveState === "invalid" ? { variant: "destructive" as const, label: "Есть ошибки" }
          : saveState === "saved" || !dirty ? { variant: "success" as const, label: "Черновик сохранён" }
            : { variant: "warning" as const, label: "Есть несохранённые изменения" };
  const issues = validation.flatMap((item) => item.issues.map((issue, index) => ({
    key: `${item.recordId}:${index}:${issue.path}`,
    current: item.recordId === selectedRecordId,
    target: validationFocusTarget(fields, issue),
    message: issue.message,
  }))).sort((left, right) => Number(right.current) - Number(left.current)).slice(0, 12);

  return <>
    <header className="flex min-w-0 flex-col gap-3 border-b border-border pb-3 lg:flex-row lg:items-center lg:justify-between">
      <div className="grid w-full min-w-0 grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center lg:w-auto">
        <span className="col-span-2 text-xs text-muted-foreground sm:col-auto">Снапшот</span>
        <div className="col-span-2 min-w-0 sm:w-64">
          <Select value={selectedSnapshotId} onValueChange={onSnapshotChange} disabled={saveState === "saving" || publishPending}>
            <SelectTrigger aria-label="Выбрать снапшот" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>{snapshots.map((snapshot) => <SelectItem key={snapshot.id} value={snapshot.id}>{displaySnapshot(snapshot)}</SelectItem>)}</SelectContent>
          </Select>
        </div>
        <Button variant="outline" className="w-full sm:w-auto" disabled={saveState === "saving" || publishPending} onClick={onOpenHistory}><Clock3 />История</Button>
        <Button variant="outline" className="w-full sm:w-auto" loading={saveState === "saving"} disabled={!dirty || saveState === "saving" || publishPending || invalidCount > 0 || saveState === "conflict"} onClick={onSave}>
          {saveState === "saving" ? "Сохранение…" : "Сохранить"}
        </Button>
        {canPublish && <Button className="col-span-2 w-full sm:w-auto" loading={publishPending} disabled={saveState === "saving" || publishPending || invalidCount > 0 || saveState === "conflict" || (revision <= publishedRevision && !dirty)} onClick={onPublish}>
          {publishPending ? "Публикация…" : "Опубликовать"}
        </Button>}
      </div>
      <Badge variant={badge.variant}>{badge.label}</Badge>
    </header>

    <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground" aria-live="polite">
      <span>Черновик {revision} · опубликовано {publishedRevision}</span>
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

    {invalidCount > 0 && <section role="alert" className="space-y-2 rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm">
      <p>Проверьте введённые значения ({invalidCount}). Поля пустыми оставлять можно.</p>
      {issues.length > 0 && <ul className="flex flex-wrap gap-2" aria-label="Ошибки валидации контента">
        {issues.map((issue) => <li key={issue.key}>
          <button
            type="button"
            data-field-path={issue.target.path}
            className="inline-flex min-h-9 items-center rounded-md border border-warning/40 bg-background/60 px-2 text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => onFocusField?.(issue.target.path)}
          >
            {issue.target.label}: {issue.message}
          </button>
        </li>)}
      </ul>}
    </section>}
  </>;
}
