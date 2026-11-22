import { useEffect } from "react";
import { ArrowLeft, Trash2 } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { useDocumentTitle } from "@/components/common/page";
import { scrollToEntryField } from "@/domains/content-editor/EntryForm";
import type { ModelContentRecord, ModelContentWorkspace, WebsiteSnapshot } from "@/types";
import { ContentEntityPage } from "./components/model-content/ContentEntityPage";
import { ContentOverview } from "./components/model-content/ContentOverview";
import { ContentPlacementsPage } from "./components/model-content/ContentPlacementsPage";
import { ContentRecordNotFound, ContentRecordPage } from "./components/model-content/ContentRecordPage";
import { ModelContentActions } from "./components/model-content/ModelContentActions";
import { ModelContentHistoryDialog } from "./components/model-content/ModelContentHistoryDialog";
import { PlacementSelections } from "./components/model-content/PlacementSelections";
import { OrphanedSelectionsNotice } from "./components/model-content-editor/OrphanedSelectionsNotice";
import {
  modelWorkspaceQueryKey,
  useModelContentEditorController,
} from "./components/model-content-editor/useModelContentEditorController";
import { contentHref, entityPath, recordPath } from "./model-content-paths";
import { modelEntityFields } from "./model-content-workspace";

export { PlacementSelections, modelWorkspaceQueryKey };

export type ModelContentEditorView = "overview" | "entity" | "record" | "placements";

export function ModelContentEditor({
  snapshots,
  workspace,
  view,
  entityKey,
  recordId: routeRecordId,
  onSnapshotChange,
}: {
  snapshots: WebsiteSnapshot[];
  workspace: ModelContentWorkspace;
  view: ModelContentEditorView;
  entityKey: string;
  recordId: string;
  onSnapshotChange: (snapshotId: string) => void;
}) {
  const navigate = useNavigate();
  const {
    canPublish, model, entities, mediaEditorProps, draft, savedDraft, revision,
    revisionConflict, saveState, saveError, dirty, invalidCount, publishPending,
    deletePending, historyOpen, setHistoryOpen, recordId,
    search, setSearch, selectedEntity, selectedRecord, recordsForEntity,
    visibleRecords, orphanedEntityRecords, orphanedSelections, validation,
    unsavedDialog, historyQuery, updateDraft, confirm, saveDraft, publish,
    restorePublication, reloadServerDraft, addRecord, removeRecord,
    clearSingletonRecord, removeOrphanedEntityRecord, cleanOrphanedRecordData,
    changeField, cleanOrphanedSelections, downloadLocalDraft,
  } = useModelContentEditorController({
    workspace,
    requestedEntityKey: entityKey,
    requestedRecordId: routeRecordId,
  });

  const snapshotId = workspace.snapshot.id;
  const routeEntity = entities.find((entity) => entity.key === entityKey);
  const entityURL = routeEntity ? contentHref(entityPath(routeEntity.key), snapshotId) : contentHref("/content", snapshotId);
  const disabled = saveState === "saving" || publishPending;
  useDocumentTitle(
    view === "placements"
      ? "Записи на страницах"
      : view === "record"
        ? selectedRecord && routeEntity ? modelRecordTitleForTitle(selectedRecord, routeEntity.name) : "Запись"
        : view === "entity"
          ? routeEntity?.name ?? "Сущность"
          : "Контент",
  );

  useEffect(() => {
    if (view !== "record" || !routeEntity || !recordId) return;
    if (!recordsForEntity.some((record) => record.id === recordId) || recordId === routeRecordId) return;
    navigate(contentHref(recordPath(routeEntity.key, recordId), snapshotId), { replace: true });
  }, [navigate, recordId, recordsForEntity, routeEntity, routeRecordId, snapshotId, view]);

  if (!model) return null;

  const createRecord = () => {
    const next = addRecord();
    if (next && selectedEntity) navigate(contentHref(recordPath(selectedEntity.key, next.id), snapshotId));
  };

  return (
    <div className="page-grid min-w-0 space-y-5">
      {unsavedDialog}

      <ModelContentActions
        snapshots={snapshots}
        selectedSnapshotId={snapshotId}
        revision={revision}
        publishedRevision={workspace.publishedRevision}
        saveState={saveState}
        saveError={saveError}
        revisionConflict={revisionConflict}
        dirty={dirty}
        invalidCount={invalidCount}
        publishPending={publishPending}
        canPublish={canPublish}
        validation={validation}
        fields={selectedEntity ? modelEntityFields(selectedEntity) : model.entities.flatMap(modelEntityFields)}
        selectedRecordId={recordId}
        onFocusField={(fieldPath) => scrollToEntryField(fieldPath)}
        onSnapshotChange={onSnapshotChange}
        onOpenHistory={() => setHistoryOpen(true)}
        onSave={() => void saveDraft()}
        onPublish={() => void publish()}
        onDownloadLocalDraft={downloadLocalDraft}
        onReloadServerDraft={() => void reloadServerDraft()}
        onRetrySave={() => void saveDraft()}
      />

      <nav aria-label="Разделы контента" className="flex gap-1 border-b border-border pb-2">
        <Link
          to={contentHref("/content", snapshotId)}
          aria-current={view === "placements" ? undefined : "page"}
          className={`inline-flex min-h-11 items-center rounded-md px-3 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${view === "placements" ? "text-muted-foreground hover:bg-muted" : "bg-accent text-accent-foreground"}`}
        >Обзор</Link>
        <Link
          to={contentHref("/content/placements", snapshotId)}
          aria-current={view === "placements" ? "page" : undefined}
          className={`inline-flex min-h-11 items-center rounded-md px-3 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${view === "placements" ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-muted"}`}
        >Записи на страницах</Link>
      </nav>

      <div className="mx-auto w-full max-w-6xl">
        {view === "overview" && <>
          <header className="mb-6 border-b border-border pb-5">
            <h1 className="text-2xl font-semibold tracking-tight">Контент</h1>
            <p className="mt-1 max-w-3xl text-sm leading-6 text-muted-foreground">Выберите сущность модели, чтобы работать с записями и связями выбранного снапшота.</p>
          </header>
          <OrphanedSelectionsNotice
            count={orphanedSelections.length}
            disabled={disabled}
            onClean={() => void cleanOrphanedSelections()}
          />
          {orphanedEntityRecords.length > 0 && <section role="alert" aria-labelledby="orphaned-model-records-title" className="mb-6 border-l-2 border-warning bg-warning/5 px-4 py-3">
            <h2 id="orphaned-model-records-title" className="font-medium">Записи удалённых сущностей</h2>
            <p className="mt-1 text-sm text-muted-foreground">Их нельзя опубликовать, пока сущность не восстановлена или запись явно не удалена.</p>
            <ul className="mt-3 divide-y divide-border">
              {orphanedEntityRecords.map((record) => <li key={record.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                <div className="min-w-0 text-sm"><p className="font-medium">Запись удалённой модели</p><p className="text-xs text-muted-foreground">Значений: {Object.keys(record.values).length}</p></div>
                <Button type="button" variant="outline" className="min-h-11 text-destructive" disabled={deletePending || disabled} onClick={() => void removeOrphanedEntityRecord(record)}><Trash2 />Удалить запись</Button>
              </li>)}
            </ul>
          </section>}
          <ContentOverview
            model={model}
            entities={entities}
            records={draft.records}
            placementsCount={workspace.placements.length}
            validation={validation}
            orphanedEntityRecords={orphanedEntityRecords}
            orphanedSelections={orphanedSelections.length}
            snapshotId={snapshotId}
          />
        </>}

        {view === "entity" && (routeEntity ? <ContentEntityPage
          entity={routeEntity}
          recordsForEntity={recordsForEntity}
          visibleRecords={visibleRecords}
          search={search}
          snapshotId={snapshotId}
          validation={validation}
          disabled={disabled}
          onSearchChange={setSearch}
          onAddRecord={createRecord}
          updateDraft={updateDraft}
        /> : <EntityNotFound snapshotId={snapshotId} />)}

        {view === "record" && (routeEntity && selectedRecord ? <ContentRecordPage
          model={model}
          entity={routeEntity}
          record={selectedRecord}
          recordsForEntity={recordsForEntity}
          snapshotId={snapshotId}
          validation={validation}
          mediaPreparationsByFieldId={mediaEditorProps.preparations}
          mediaAcceptByFieldId={mediaEditorProps.accepts}
          disabled={disabled}
          deletePending={deletePending}
          onDeleteRecord={(record) => void removeRecord(record).then((deleted) => {
            if (deleted) navigate(entityURL);
          })}
          onClearSingletonRecord={(record) => void clearSingletonRecord(record)}
          onCleanOrphanedRecordData={(record) => void cleanOrphanedRecordData(record)}
          onChangeField={changeField}
        /> : <ContentRecordNotFound entity={routeEntity} snapshotId={snapshotId} />)}

        {view === "placements" && <ContentPlacementsPage
          model={model}
          workspace={workspace}
          draft={draft}
          savedDraft={savedDraft}
          disabled={disabled}
          updateDraft={updateDraft}
          confirmCleanup={(title, description) => confirm({ title, description, confirmLabel: "Очистить выбор", destructive: true })}
        />}
      </div>

      <ModelContentHistoryDialog
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        items={historyQuery.data?.pages.flatMap((page) => page.items)}
        loading={historyQuery.isLoading}
        loadingMore={historyQuery.isFetchingNextPage}
        hasMore={historyQuery.hasNextPage}
        error={historyQuery.error}
        disabled={disabled || saveState === "conflict"}
        onRetry={() => void historyQuery.refetch()}
        onLoadMore={() => void historyQuery.fetchNextPage()}
        onRestore={(publicationId) => void restorePublication(publicationId)}
      />
    </div>
  );
}

function modelRecordTitleForTitle(record: ModelContentRecord, fallback: string): string {
  for (const value of Object.values(record.values)) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return fallback;
}

function EntityNotFound({ snapshotId }: { snapshotId: string }) {
  return <div className="border-y border-dashed border-border py-14 text-center">
    <h1 className="text-xl font-semibold">Сущность не найдена</h1>
    <p className="mt-2 text-sm text-muted-foreground">Она могла быть удалена из модели или ссылка относится к другому снапшоту.</p>
    <Button asChild variant="outline" className="mt-5 min-h-11"><Link to={contentHref("/content", snapshotId)}><ArrowLeft />К контенту</Link></Button>
  </div>;
}
