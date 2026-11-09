import type { ModelContentWorkspace, SiteSnapshot } from "@/types";
import { OrphanedSelectionsNotice } from "./components/model-content-editor/OrphanedSelectionsNotice";
import { ModelContentActions } from "./components/model-content/ModelContentActions";
import { ModelContentHistoryDialog } from "./components/model-content/ModelContentHistoryDialog";
import { ModelContentRecordWorkspace } from "./components/model-content/ModelContentRecordWorkspace";
import { PlacementSelections } from "./components/model-content/PlacementSelections";
import { useModelContentEditorController, modelWorkspaceQueryKey } from "./components/model-content-editor/useModelContentEditorController";

export { PlacementSelections, modelWorkspaceQueryKey };

export function ModelContentEditor({
  snapshots,
  workspace,
  onSnapshotChange,
}: {
  snapshots: SiteSnapshot[];
  workspace: ModelContentWorkspace;
  onSnapshotChange: (snapshotId: string) => void;
}) {
  const {
    canPublish, model, entities, mediaEditorProps, draft, savedDraft, revision,
    revisionConflict, saveState, saveError, dirty, invalidCount, publishPending,
    deletePending, historyOpen, setHistoryOpen, entityId, recordId,
    setRecordId, search, setSearch, selectedEntity, selectedRecord,
    recordsForEntity, visibleRecords, orphanedEntityRecords, orphanedSelections,
    validation, unsavedDialog, historyQuery, updateDraft, confirm, saveDraft,
    publish, restorePublication, reloadServerDraft, addRecord, removeRecord,
    clearSingletonRecord, removeOrphanedEntityRecord, cleanOrphanedRecordData,
    changeField, cleanOrphanedSelections, downloadLocalDraft, selectEntity, openTargetRecord,
  } = useModelContentEditorController({ workspace });
  if (!model) return null;
  return (
    <div className="page-grid min-w-0 space-y-5">
      {unsavedDialog}

      <ModelContentActions
        snapshots={snapshots}
        selectedSnapshotId={workspace.snapshot.id}
        revision={revision}
        publishedRevision={workspace.publishedRevision}
        saveState={saveState}
        saveError={saveError}
        revisionConflict={revisionConflict}
        dirty={dirty}
        invalidCount={invalidCount}
        publishPending={publishPending}
        canPublish={canPublish}
        onSnapshotChange={onSnapshotChange}
        onOpenHistory={() => setHistoryOpen(true)}
        onSave={() => void saveDraft()}
        onPublish={() => void publish()}
        onDownloadLocalDraft={downloadLocalDraft}
        onReloadServerDraft={() => void reloadServerDraft()}
        onRetrySave={() => void saveDraft()}
      />

      <ModelContentRecordWorkspace
        model={model}
        draft={draft}
        savedDraft={savedDraft}
        entities={entities}
        selectedEntity={selectedEntity}
        selectedRecord={selectedRecord}
        entityId={entityId}
        recordId={recordId}
        search={search}
        recordsForEntity={recordsForEntity}
        visibleRecords={visibleRecords}
        orphanedEntityRecords={orphanedEntityRecords}
        validation={validation}
        mediaPreparationsByFieldId={mediaEditorProps.preparations}
        mediaAcceptByFieldId={mediaEditorProps.accepts}
        disabled={saveState === "saving" || publishPending}
        deletePending={deletePending}
        onSelectEntity={selectEntity}
        onSelectRecord={setRecordId}
        onSearchChange={setSearch}
        onAddRecord={addRecord}
        onDeleteRecord={(record) => void removeRecord(record)}
        onClearSingletonRecord={(record) => void clearSingletonRecord(record)}
        onDeleteOrphanedEntityRecord={(record) => void removeOrphanedEntityRecord(record)}
        onCleanOrphanedRecordData={(record) => void cleanOrphanedRecordData(record)}
        onChangeField={changeField}
        onOpenTargetRecord={openTargetRecord}
        onConfirmCleanup={(title, description) => confirm({
          title,
          description,
          confirmLabel: "Удалить ссылки",
          destructive: true,
        })}
        updateDraft={updateDraft}
        beforeContent={
          <OrphanedSelectionsNotice
            count={orphanedSelections.length}
            disabled={saveState === "saving" || publishPending}
            onClean={() => void cleanOrphanedSelections()}
          />
        }
        placementSelections={
          <PlacementSelections
            model={model}
            workspace={workspace}
            draft={draft}
            savedDraft={savedDraft}
            disabled={saveState === "saving" || publishPending}
            updateDraft={updateDraft}
            confirmCleanup={(title, description) => confirm({
              title,
              description,
              confirmLabel: "Очистить выбор",
              destructive: true,
            })}
          />
        }
      />

      <ModelContentHistoryDialog
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        items={historyQuery.data?.items}
        loading={historyQuery.isLoading}
        error={historyQuery.error}
        disabled={saveState === "saving" || publishPending || saveState === "conflict"}
        onRetry={() => void historyQuery.refetch()}
        onRestore={(publicationId) => void restorePublication(publicationId)}
      />
    </div>
  );
}
