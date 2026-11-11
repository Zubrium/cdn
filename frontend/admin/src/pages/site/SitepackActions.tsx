import { useId, useRef, useState, type ChangeEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Download, FileArchive, Upload } from "lucide-react";
import { api, isAbortError, isApiError, type SitepackImportReport } from "@/api";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/errors";
import { downloadBlob } from "@/lib/format";
import type { ApiErrorDetail, SiteSnapshot } from "@/types";

/** Import creates an unassigned snapshot; assignments always require a separate action. */
export function SitepackImportAction() {
  const pickerId = useId();
  const abort = useRef<AbortController | null>(null);
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [errorDetails, setErrorDetails] = useState<ApiErrorDetail[]>([]);
  const [report, setReport] = useState<SitepackImportReport | null>(null);

  const selectFile = (event: ChangeEvent<HTMLInputElement>) => {
    const next = event.target.files?.[0] ?? null;
    setFile(next);
    setError("");
    setErrorDetails([]);
    setReport(null);
    event.target.value = "";
  };

  const importSnapshot = async () => {
    if (!file || busy) return;
    const controller = new AbortController();
    abort.current = controller;
    setBusy(true);
    setError("");
    setErrorDetails([]);
    try {
      const imported = await api.siteBuilder.importSitepack(file, controller.signal);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] }),
        queryClient.invalidateQueries({ queryKey: ["content", "snapshots"] }),
        queryClient.invalidateQueries({ queryKey: ["site", "feature-builds"] }),
      ]);
      setReport(imported);
      setFile(null);
    } catch (reason) {
      // A cancellation can happen after the server has committed. Refresh the
      // list so the user can verify the result instead of blindly retrying.
      if (isAbortError(reason)) {
        setError("Загрузка прервана. Проверьте список снапшотов: сервер мог завершить импорт.");
        void queryClient.invalidateQueries({ queryKey: ["site", "snapshot-choices"] });
      } else {
        setError(errorMessage(reason));
        if (isApiError(reason)) setErrorDetails(reason.details);
      }
    } finally {
      abort.current = null;
      setBusy(false);
    }
  };

  return (
    <section aria-labelledby="sitepack-title" className="space-y-3 border-t border-border pt-5">
      <div>
        <h2 id="sitepack-title" className="text-sm font-semibold">Перенос снапшота</h2>
        <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
          Импорт создаст отдельный снапшот. Пользователи, заявки и назначения веток в архив не входят.
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <input id={pickerId} type="file" accept=".sitepack,.zip,application/zip" onChange={selectFile} disabled={busy} className="peer sr-only" aria-label="Архив снапшота" />
        <Button asChild variant="outline" className={busy ? "pointer-events-none opacity-50" : "peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2"}>
          <label htmlFor={pickerId} aria-disabled={busy}><FileArchive />Выбрать архив</label>
        </Button>
        {file && <span className="max-w-full truncate text-sm text-muted-foreground">{file.name}</span>}
        {file && <Button type="button" disabled={busy} onClick={() => void importSnapshot()}><Upload />{busy ? "Импортируем…" : "Импортировать"}</Button>}
        {busy && <Button type="button" variant="ghost" onClick={() => abort.current?.abort()}>Прервать</Button>}
      </div>
      {busy && <p role="status" aria-live="polite" className="text-sm text-muted-foreground">Проверяем архив и создаём снапшот. Большие файлы могут занять время.</p>}
      {error && <SitepackImportFailureDetails message={error} details={errorDetails} />}
      {report && <SitepackImportSuccessReport report={report} />}
    </section>
  );
}

export function sitepackDiagnosticText(detail: ApiErrorDetail): string {
  return detail.path ? `${detail.path}: ${detail.message}` : detail.message;
}

export function SitepackImportFailureDetails({ message, details }: { message: string; details: ApiErrorDetail[] }) {
  return <div role="alert" className="space-y-1 text-sm text-destructive">
    <p>{message}</p>
    {details.map((detail, index) => <p key={`${detail.path ?? "sitepack"}-${detail.code ?? index}`} className="text-xs">{sitepackDiagnosticText(detail)}</p>)}
  </div>;
}

const sitepackKindLabels: Record<SitepackImportReport["items"][number]["kind"], string> = {
  build: "Сборка",
  file: "Файл",
  model_entity: "Сущность модели",
  model_field: "Поле модели",
  model_relation: "Связь модели",
  model_record: "Запись модели",
};

export function SitepackImportSuccessReport({ report }: { report: SitepackImportReport }) {
  return <div role="status" className="space-y-3 rounded-lg border border-border bg-muted/30 p-3 text-sm">
    <div className="space-y-2">
      <p className="font-medium">Импортирован снапшот #{report.snapshot.sequence} · {report.snapshot.name}</p>
      <p className="text-muted-foreground">Новых сборок: {report.newBuilds}, повторно использовано: {report.reusedBuilds}. Новых файлов: {report.newFiles}, повторно использовано: {report.reusedFiles}.</p>
      <p className="text-muted-foreground">Записей модели: {report.modelPublishedRecords} опубликовано, {report.modelDraftRecords} в черновике. История: {report.modelHistoryRevisions} публикаций; ID модели и записей переназначены.</p>
      <p className="text-muted-foreground">Снапшот пока не назначен ветке.</p>
      <div className="flex flex-wrap gap-2"><Button size="sm" variant="outline" asChild><Link to={`/site/pages?snapshot=${report.snapshot.id}`}>Открыть структуру</Link></Button><Button size="sm" variant="outline" asChild><Link to={`/snapshots/branches?snapshot=${report.snapshot.id}`}>Перейти к управлению ветками</Link></Button></div>
    </div>
    <SitepackImportItems items={report.items ?? []} truncated={report.itemsTruncated ?? report.fileIdsTruncated ?? false} />
  </div>;
}

export function SitepackImportItems({ items, truncated }: { items: SitepackImportReport["items"]; truncated: boolean }) {
  return <>
    {!!items.length && <ul aria-label="Изменения при импорте" className="max-h-64 space-y-2 overflow-auto border-t border-border pt-3">
      {items.map((item, index) => <li key={`${item.kind}-${item.path}-${index}`} className="rounded-md bg-background/70 px-2 py-1.5">
        <p className="font-medium">{sitepackKindLabels[item.kind] ?? "Элемент"}: {item.message}</p>
        <p className="break-all text-xs text-muted-foreground">{item.path}{item.count ? ` · ссылок: ${item.count}` : ""}</p>
        {item.sourceId && item.targetId && <p className="break-all text-xs text-muted-foreground">ID: {item.sourceId} → {item.targetId}</p>}
      </li>)}
    </ul>}
    {truncated && <p className="border-t border-border pt-2 text-xs text-muted-foreground">Показаны первые {items.length} элементов. Сводные числа выше учитывают весь архив.</p>}
  </>;
}

export function SitepackExportAction({ snapshot }: { snapshot: SiteSnapshot }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const exportSnapshot = async () => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      type SavePicker = (options: { suggestedName: string; types: Array<{ description: string; accept: Record<string, string[]> }> }) => Promise<{ createWritable: () => Promise<WritableStream<Uint8Array>> }>;
      const picker = (window as Window & { showSaveFilePicker?: SavePicker }).showSaveFilePicker;
      // The picker is invoked immediately after the click, before fetching,
      // as required by browsers for a user-initiated file operation.
      const destination = picker ? await (await picker({
        suggestedName: `snapshot-${snapshot.sequence}.sitepack`,
        types: [{ description: "Снапшот Zubrium", accept: { "application/zip": [".sitepack"] } }],
      })).createWritable() : undefined;
      const result = await api.siteBuilder.exportSitepack(snapshot.id, destination);
      if (result.blob) downloadBlob(result.blob, result.filename);
    } catch (reason) {
      if (!isAbortError(reason)) setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  };
  return <span className="inline-flex flex-col items-start gap-1"><Button size="sm" type="button" variant="outline" disabled={busy} onClick={() => void exportSnapshot()}><Download />{busy ? "Готовим…" : "Экспорт"}</Button>{error && <span role="alert" className="max-w-56 text-xs text-destructive">{error}</span>}</span>;
}
