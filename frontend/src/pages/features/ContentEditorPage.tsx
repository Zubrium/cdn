import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  CloudUpload,
  Eye,
  FileClock,
  FileUp,
  History,
  Loader2,
  Monitor,
  MoreHorizontal,
  Redo2,
  RefreshCw,
  Save,
  Send,
  Smartphone,
  Tablet,
  Undo2,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/api";
import { useAuth } from "@/app/auth";
import type {
  EntryDetail,
  EntryVersion,
  JsonObject,
  JsonValue,
} from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { errorMessage } from "@/lib/errors";
import { formatDateTime, truncate } from "@/lib/format";
import { cn, isRecord } from "@/lib/utils";
import { entryTitleField } from "@/lib/json";
import { useFeatureContext } from "@/pages/features/FeatureLayout";
import { useConfirm } from "@/components/common/confirm";
import {
  EmptyState,
  ErrorState,
  InlineAlert,
  LoadingPage,
  useDocumentTitle,
  useUnsavedChangesGuard,
} from "@/components/common/page";
import { VersionStatusBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/misc";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ContentPreview } from "@/domains/content-editor/ContentPreview";
import {
  EntryFieldsForm,
  entryFieldAnchorId,
  validateEntryFields,
} from "@/domains/content-editor/EntryForm";
import {
  blocksKeyFromFields,
  buildDocumentData,
  deriveDocumentTitle,
  documentMetadata,
  parseBlocks,
  starterMetadata,
} from "@/domains/content-editor/blocks";
import type { DocumentState } from "@/domains/content-editor/types";
import { useContentAutosave } from "@/domains/content-editor/useContentAutosave";
import { useEditorHistory } from "@/domains/content-editor/useEditorHistory";
import { parsedSchema } from "@/domains/content-schema/schema";
import type { SchemaFieldNode } from "@/domains/content-schema/types";
import { DiffList, DiffSummaryBadges } from "@/domains/versioning/DiffView";
import { diffJson } from "@/domains/versioning/diff";

type PreviewWidth = "desktop" | "tablet" | "mobile";

const widthOptions: Array<{
  value: PreviewWidth;
  label: string;
  icon: typeof Monitor;
}> = [
  { value: "desktop", label: "Десктоп", icon: Monitor },
  { value: "tablet", label: "Планшет", icon: Tablet },
  { value: "mobile", label: "Телефон", icon: Smartphone },
];

function collectMediaIds(
  state: DocumentState,
  fields: SchemaFieldNode[],
): string[] {
  const ids = new Set<string>();
  state.blocks.forEach((block) => {
    if (block.type === "image" && block.data.mediaId) ids.add(block.data.mediaId);
    if (block.type === "video" && block.data.mediaId) ids.add(block.data.mediaId);
    if (block.type === "gallery") {
      block.data.mediaIds.forEach((id) => ids.add(id));
    }
  });
  const addMedia = (value: unknown) => {
    if (typeof value === "string" && value) ids.add(value);
    if (Array.isArray(value)) value.forEach(addMedia);
  };
  const walk = (field: SchemaFieldNode, value: JsonValue | undefined) => {
    if (field.type === "media" || field.type === "file") {
      addMedia(value);
      return;
    }
    if (field.type === "object") {
      if (!isRecord(value)) return;
      field.children.forEach((child) => walk(child, value[child.key]));
      return;
    }
    if (field.type === "array") {
      if (!Array.isArray(value)) return;
      if (field.itemType === "media" || field.itemType === "file") addMedia(value);
      if (field.itemType === "object") {
        value.forEach((item) => {
          if (!isRecord(item)) return;
          field.children.forEach((child) => walk(child, item[child.key]));
        });
      }
    }
  };
  fields.forEach((field) => walk(field, state.metadata[field.key]));
  return Array.from(ids);
}

function SaveStateBadge({
  status,
  dirty,
  hasSavedDraft,
  hasPublished,
}: {
  status: string;
  dirty: boolean;
  hasSavedDraft: boolean;
  hasPublished: boolean;
}) {
  if (status === "saving") {
    return (
      <Badge variant="warning" role="status" aria-live="polite">
        <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
        Сохранение
      </Badge>
    );
  }
  if (status === "conflict") {
    return (
      <Badge variant="destructive" role="status" aria-live="polite">
        <AlertTriangle className="h-3 w-3" aria-hidden="true" />
        Конфликт версий
      </Badge>
    );
  }
  if (status === "error") {
    return (
      <Badge variant="destructive" role="status" aria-live="polite">
        <AlertTriangle className="h-3 w-3" aria-hidden="true" />
        Ошибка сохранения
      </Badge>
    );
  }
  if (dirty) {
    return (
      <Badge variant="warning" role="status" aria-live="polite">
        <CloudUpload className="h-3 w-3" aria-hidden="true" />
        Есть изменения
      </Badge>
    );
  }
  return hasSavedDraft ? (
    <Badge variant="success" role="status" aria-live="polite">
      <Check className="h-3 w-3" aria-hidden="true" />
      Сохранено
    </Badge>
  ) : hasPublished ? (
    <Badge variant="success" role="status" aria-live="polite">
      <Check className="h-3 w-3" aria-hidden="true" />
      Опубликовано
    </Badge>
  ) : (
    <Badge variant="muted" role="status" aria-live="polite">Черновик отсутствует</Badge>
  );
}

export default function ContentEditorPage() {
  const { feature } = useFeatureContext();
  const { principal } = useAuth();
  const canPromote = principal?.role === "admin" || principal?.role === "moderator";
  const { entryId } = useParams<{ entryId: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const isNew = !entryId;
  const featureArchived = feature.status === "archived";

  useDocumentTitle(
    isNew ? `Новая запись — ${feature.name}` : `Запись — ${feature.name}`,
  );

  const schemaQuery = useQuery({
    queryKey: queryKeys.content.catalog,
    queryFn: ({ signal }) => api.siteBuilder.contentFeatures(signal),
  });
  const activeContentFeature = schemaQuery.data?.items.find((item) => item.id === feature.id);
  const entryQuery = useQuery({
    queryKey: queryKeys.entries.detail(entryId ?? ""),
    queryFn: ({ signal }) => api.entries.get(entryId!, signal),
    enabled: Boolean(entryId),
  });

  const schema = activeContentFeature?.schema ?? null;
  const fields = useMemo(() => parsedSchema(schema).fields, [schema]);
  const blocksKey = useMemo(() => blocksKeyFromFields(fields), [fields]);

  const {
    value: editorState,
    update: applyUpdate,
    replace: replaceState,
    undo,
    redo,
    canUndo,
    canRedo,
  } = useEditorHistory<DocumentState>({ metadata: {}, blocks: [] });

  const editorIdentity = `${feature.id}:${entryId ?? "new"}`;
  const [seededIdentity, setSeededIdentity] = useState<string | null>(null);
  const seeded = seededIdentity === editorIdentity;
  const guardBypassRef = useRef(false);
  const [preview, setPreview] = useState(false);
  const [previewWidth, setPreviewWidth] = useState<PreviewWidth>("desktop");
  const [historyOpen, setHistoryOpen] = useState(false);
  const [selectedVersion, setSelectedVersion] = useState<EntryVersion | null>(
    null,
  );
  const [blockWarnings, setBlockWarnings] = useState<string[]>([]);
  const importInput = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);
  const [promotionError, setPromotionError] = useState<ApiError | null>(null);

  const data = useMemo(
    () =>
      buildDocumentData(editorState.metadata, editorState.blocks, blocksKey),
    [editorState, blocksKey],
  );

  const autosave = useContentAutosave({
    featureId: feature.id,
    entryId: entryId ?? null,
    revision:
      entryQuery.data && entryQuery.data.id === entryId
        ? entryQuery.data.revision
        : 1,
    data,
    enabled: seeded && !featureArchived,
    onCreated: (entry) => {
      queryClient.setQueryData(queryKeys.entries.detail(entry.id), entry);
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.list(feature.id),
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.overview });
      // The dirty flag has not re-rendered yet, so let the guard through
      // explicitly; otherwise the user is stuck on /content/new with the
      // "unsaved changes" dialog right after the first autosave.
      guardBypassRef.current = true;
      navigate(`/features/${feature.id}/content/${entry.id}`, { replace: true });
      toast.success("Черновик создан");
    },
    onSaved: (entry) => {
      queryClient.setQueryData(queryKeys.entries.detail(entry.id), entry);
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.list(feature.id),
      });
    },
  });

  const { markLoaded } = autosave;

  /**
   * Fills the editor with a document and resets the autosave baseline, so the
   * loaded state is never treated as a local change.
   */
  const applySeed = useCallback(
    (next: DocumentState, entryIdValue: string | null, revision: number) => {
      const baseline = JSON.stringify(
        buildDocumentData(next.metadata, next.blocks, blocksKey),
      );
      replaceState(next);
      markLoaded(baseline, entryIdValue, revision);
    },
    [blocksKey, markLoaded, replaceState],
  );

  const stateFromData = useCallback(
    (source: JsonObject | null): DocumentState => {
      if (!source) {
        return { metadata: starterMetadata(fields, blocksKey), blocks: [] };
      }
      const parsed = parseBlocks(
        source[blocksKey ?? ""] as JsonValue | undefined,
      );
      setBlockWarnings(parsed.warnings);
      return {
        metadata: documentMetadata(source, blocksKey),
        blocks: parsed.blocks,
      };
    },
    [blocksKey, fields],
  );

  const buildState = useCallback(
    (entry: EntryDetail | null | undefined): DocumentState =>
      stateFromData(
        entry
          ? entry.draft?.data ?? entry.dev?.data ?? entry.prod?.data ?? null
          : null,
      ),
    [stateFromData],
  );

  // Seed the editor once the schema and the entry are known. The baseline is
  // set at the same time, so the freshly loaded document is not "dirty".
  useEffect(() => {
    if (seeded) return;
    if (schemaQuery.isLoading) return;
    if (entryId) {
      if (entryQuery.isLoading) return;
      if (!entryQuery.data) return;
    }
    const entry = entryQuery.data;
    if (entryId && (!entry || entry.id !== entryId)) return;

    applySeed(
      buildState(entry),
      entry?.id ?? entryId ?? null,
      entry?.revision ?? 1,
    );
    setSeededIdentity(editorIdentity);
  }, [
    seeded,
    editorIdentity,
    schemaQuery.isLoading,
    entryId,
    entryQuery.isLoading,
    entryQuery.data,
    applySeed,
    buildState,
  ]);

  const dirty = autosave.dirty;
  const hasDraft = Boolean(entryQuery.data?.draft);
  const hasPublishableContent = hasDraft || dirty;
  const guard = useUnsavedChangesGuard(dirty, guardBypassRef);

  const updateMetadata = useCallback(
    (key: string, value: JsonValue) => {
      applyUpdate(
        (current) => ({
          ...current,
          metadata: { ...current.metadata, [key]: value },
        }),
        true,
      );
    },
    [applyUpdate],
  );


  const title = useMemo(
    () => deriveDocumentTitle(editorState.metadata, editorState.blocks, fields),
    [editorState, fields],
  );

  const titleField = useMemo(() => {
    // Keep the title card and the preview/list derivation in sync: both use
    // the schema-aware "first sensible string" pick from lib/json.
    const picked = entryTitleField(fields);
    if (!picked) return undefined;
    return fields.find(
      (field) => field.key === picked.key && field.type !== "blocks",
    );
  }, [fields]);
  const blockingIssues = useMemo(
    () => validateEntryFields(fields, editorState.metadata),
    [fields, editorState.metadata],
  );
  const focusField = useCallback((path: string) => {
    let topLevelKey = path.split(".")[0] ?? path;
    for (const field of fields) {
      if (
        (path === field.key || path.startsWith(`${field.key}.`)) &&
        field.key.length > topLevelKey.length
      ) {
        topLevelKey = field.key;
      }
    }
    const section = document.getElementById(entryFieldAnchorId(topLevelKey));
    section?.scrollIntoView({ block: "center" });
    section?.focus({ preventScroll: true });
  }, [fields]);
  const preservedBlocks = editorState.blocks;
  const editableFields = useMemo(() => {
    const titleKey = titleField?.key ?? null;
    return fields.filter(
      (field) =>
        field.type !== "blocks" &&
        field.key !== blocksKey &&
        field.key !== titleKey &&
        field.key.trim(),
    );
  }, [fields, blocksKey, titleField]);



  const publishMutation = useMutation({
    mutationFn: async () => {
      const saved = await autosave.flush();
      if (!saved) {
        throw new Error(
          autosave.conflict
            ? "Сначала разрешите конфликт версий: черновик не сохранён."
            : "Не удалось сохранить черновик перед публикацией.",
        );
      }
      const currentId = autosave.getEntryId();
      if (!currentId) throw new Error("Черновик ещё не создан.");
      return api.entries.publish(currentId, autosave.getRevision());
    },
    onSuccess: (entry) => {
      queryClient.setQueryData(queryKeys.entries.detail(entry.id), entry);
      autosave.markPublished(entry.id, entry.revision);
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.list(feature.id),
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.versions(entry.id),
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.overview });
      toast.success(
        entry.dev
          ? `Запись опубликована в dev: v${entry.dev.version}`
          : "Запись опубликована в dev",
      );
    },
    onError: (error) => {
      toast.error(errorMessage(error), {
        description:
          "Публичная версия не изменилась. Проверьте состояние черновика и повторите публикацию после исправления проблемы.",
      });
    },
  });

  const promoteMutation = useMutation({
    mutationFn: async () => {
      const currentId = autosave.getEntryId();
      if (!currentId) throw new Error("Черновик ещё не создан.");
      return api.entries.promote(currentId, autosave.getRevision());
    },
    onSuccess: (entry) => {
      setPromotionError(null);
      queryClient.setQueryData(queryKeys.entries.detail(entry.id), entry);
      void queryClient.invalidateQueries({ queryKey: queryKeys.entries.list(feature.id) });
      void queryClient.invalidateQueries({ queryKey: queryKeys.overview });
      toast.success(entry.prod ? `Запись в prod: v${entry.prod.version}` : "Запись продвинута в prod");
    },
    onError: (error) => {
      if (error instanceof ApiError && error.details.length > 0) {
        setPromotionError(error);
        return;
      }
      toast.error(errorMessage(error));
    },
  });

  const handlePublish = async () => {
    if (!hasPublishableContent) {
      toast.info("Нет новых изменений для публикации", {
        description: "Измените содержимое, чтобы создать черновик для публикации.",
      });
      return;
    }
    if (blockingIssues.length > 0) {
      toast.error("Заполните обязательные поля перед публикацией", {
        description: blockingIssues
            .slice(0, 3)
            .map((issue) => `${issue.message} (${issue.path})`)
            .join(". "),
      });
      return;
    }
    const mediaIds = collectMediaIds(editorState, fields);
    if (mediaIds.length > 0) {
      const results = await Promise.allSettled(
        mediaIds.map((id) => api.media.get(id)),
      );
      const unavailable = results
        .map((result, index) => ({ result, id: mediaIds[index]! }))
        .filter(
          ({ result }) =>
            result.status === "rejected" ||
            (result.status === "fulfilled" && result.value.status !== "ready"),
        )
        .map(({ id }) => id);
      if (unavailable.length > 0) {
        toast.error("Связанные медиа недоступны для публикации", {
          description: `Не удалось подтвердить готовность: ${unavailable.join(", ")}`,
        });
        return;
      }
    }
    const confirmed = await confirm({
      title: "Опубликовать запись?",
      description: (
        <div className="space-y-2">
          <p>
            Публикация создаёт новую неизменяемую версию и переключает публичный
            API на неё. Версия сохранится в истории; последующие правки создадут
            новый черновик.
          </p>
          <p>
            Чтобы обновить страницы сайта, сохраните черновик и создайте снапшот в разделе «Сайт → Снапшоты».
          </p>
          {entryQuery.data?.prod && (
            <p>
              Текущая опубликованная версия: v{entryQuery.data.prod.version}.
            </p>
          )}
          {mediaIds.length > 0 && (
            <p>Проверено связанных медиа: {mediaIds.length}.</p>
          )}
        </div>
      ),
      confirmLabel: "Опубликовать",
    });
    if (!confirmed) return;
    publishMutation.mutate();
  };

  const handleSaveNow = async () => {
    const ok = await autosave.save(true);
    if (ok) {
      toast.success("Черновик сохранён");
    } else if (autosave.conflict) {
      toast.error("Конфликт версий", { description: autosave.conflict });
    } else {
      toast.error(errorMessage(autosave.error));
    }
  };

  const handleImportFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (!file) return;
    setImporting(true);
    try {
      const parsed: unknown = JSON.parse(await file.text());
      if (Array.isArray(parsed)) {
        if (parsed.length === 0 || parsed.some((item) => !isRecord(item))) {
          throw new Error("Массив должен содержать хотя бы один объект записи.");
        }
        if (dirty) {
          const confirmed = await confirm({
            title: "Импортировать записи и закрыть редактор?",
            description: "Импорт создаст записи и вернёт вас к списку. Текущие несохранённые изменения этой записи будут отброшены.",
            confirmLabel: "Импортировать и закрыть",
            destructive: true,
          });
          if (!confirmed) return;
        }
        const result = await api.entries.import(feature.id, parsed as JsonObject[]);
        await queryClient.invalidateQueries({ queryKey: queryKeys.entries.list(feature.id) });
        toast.success(`Создано черновиков: ${result.imported}`);
        guardBypassRef.current = true;
        navigate(`/features/${feature.id}/content`, { replace: true });
        return;
      }
      if (!isRecord(parsed)) throw new Error("Файл должен содержать JSON-объект или массив объектов.");
      if (entryId || dirty) {
        const currentDraftExists = Boolean(entryQuery.data?.draft);
        const confirmed = await confirm({
          title: entryId ? "Заменить содержимое записи?" : "Заменить несохранённые изменения?",
          description: entryId
            ? dirty
              ? "JSON заменит сохранённый черновик и текущие несохранённые изменения. Опубликованные версии не изменятся."
              : currentDraftExists
                ? "JSON полностью заменит данные текущего черновика. Опубликованные версии не изменятся."
                : "JSON создаст черновик из файла. Опубликованные версии не изменятся."
            : "JSON создаст запись из файла и заменит текущие несохранённые изменения.",
          confirmLabel: entryId && currentDraftExists ? "Заменить черновик" : "Импортировать",
          destructive: true,
        });
        if (!confirmed) return;
      }
      const result = await api.entries.import(
        feature.id,
        parsed as JsonObject,
        entryId,
        entryId ? autosave.getRevision() : undefined,
      );
      const imported = result.items[0];
      if (!imported) throw new Error("Сервер не вернул импортированную запись.");
      queryClient.setQueryData(queryKeys.entries.detail(imported.id), imported);
      void queryClient.invalidateQueries({ queryKey: queryKeys.entries.list(feature.id) });
      await queryClient.invalidateQueries({ queryKey: queryKeys.overview });
      if (entryId) {
        applySeed(stateFromData(imported.draft?.data ?? null), imported.id, imported.revision);
      } else {
        guardBypassRef.current = true;
        navigate(`/features/${feature.id}/content/${imported.id}`, { replace: true });
      }
      toast.success("JSON проверен и загружен в черновик");
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setImporting(false);
    }
  };

  /**
   * Conflict resolution. Both options first read the current server state:
   * "reload" discards the local changes, "keep mine" re-sends the local
   * document on top of the fresh server revision.
   */
  const handleReloadFromServer = async () => {
    if (!entryId) {
      toast.info("Черновик ещё не создан — серверных изменений нет.");
      return;
    }
    const result = await entryQuery.refetch();
    const latest = result.data;
    if (!latest) {
      toast.error("Не удалось загрузить актуальное состояние записи.");
      return;
    }
    applySeed(buildState(latest), latest.id, latest.revision);
    autosave.clearConflict();
    toast.success("Документ загружен из сервера");
  };

  const handleKeepLocal = async () => {
    if (!entryId) {
      autosave.clearConflict();
      await autosave.save(true);
      return;
    }
    const result = await entryQuery.refetch();
    const latest = result.data;
    if (!latest) {
      toast.error("Не удалось загрузить актуальное состояние записи.");
      return;
    }
    autosave.clearConflict();
    autosave.adoptServerRevision(latest.revision);
    const ok = await autosave.save(true);
    if (ok) {
      toast.success("Локальная версия сохранена поверх серверной");
    } else {
      toast.error(errorMessage(autosave.error));
    }
  };

  const versionsQuery = useQuery({
    queryKey: queryKeys.entries.versions(entryId ?? ""),
    queryFn: ({ signal }) => api.entries.versions(entryId!, signal),
    enabled: historyOpen && Boolean(entryId),
  });

  const discardDraftMutation = useMutation({
    mutationFn: () => api.entries.discardDraft(entryId!),
    onSuccess: (entry) => {
      queryClient.setQueryData(queryKeys.entries.detail(entry.id), entry);
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.versions(entry.id),
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.list(feature.id),
      });
      applySeed(buildState(entry), entry.id, entry.revision);
      toast.success("Черновик отменён. Загружена опубликованная версия.");
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const rollbackMutation = useMutation({
    mutationFn: (version: number) => api.entries.rollback(entryId!, version, "prod"),
    onSuccess: (entry) => {
      queryClient.setQueryData(queryKeys.entries.detail(entry.id), entry);
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.versions(entry.id),
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.entries.list(feature.id),
      });
      toast.success(
        `Опубликованная версия переключена на v${entry.prod?.version ?? "—"}.`,
      );
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const handleDiscardDraft = async () => {
    const confirmed = await confirm({
      title: "Отменить черновик?",
      description:
        "Несохранённый черновик будет отменён, редактор вернётся к опубликованной версии. Сама опубликованная версия останется без изменений, история версий сохранится.",
      confirmLabel: "Отменить черновик",
      destructive: true,
    });
    if (confirmed) discardDraftMutation.mutate();
  };

  const handleRestoreVersion = async (version: EntryVersion) => {
    const confirmed = await confirm({
      title: `Восстановить v${version.version} как черновик?`,
      description:
        "Текущий черновик будет заменён содержимым выбранной версии. Изменения сохранятся автоматически. Опубликованная версия не изменится.",
      confirmLabel: "Восстановить",
    });
    if (!confirmed) return;
    replaceState(stateFromData(version.data));
    setHistoryOpen(false);
    setSelectedVersion(null);
    toast.success(`Версия v${version.version} загружена в черновик`);
  };

  const publishedData = entryQuery.data?.prod?.data ?? null;
  const diffEntries = useMemo(() => {
    if (!selectedVersion) return [];
    return diffJson(publishedData, selectedVersion.data);
  }, [publishedData, selectedVersion]);

  if (schemaQuery.isLoading || (entryId && entryQuery.isLoading)) {
    return <LoadingPage label="Загружаем редактор…" />;
  }

  if (schemaQuery.error) {
    return (
      <ErrorState
        error={schemaQuery.error}
        title="Не удалось загрузить схему раздела"
        onRetry={() => void schemaQuery.refetch()}
      />
    );
  }

  if (!activeContentFeature) {
    return (
      <EmptyState
        title="Фича не входит в активный dev-снапшот"
        description="Её содержимое можно редактировать после добавления в dev-снапшот."
        action={<Button variant="outline" asChild><Link to="/content">К контенту</Link></Button>}
      />
    );
  }

  if (entryId && entryQuery.error) {
    const notFound =
      entryQuery.error instanceof ApiError && entryQuery.error.status === 404;
    return (
      <div className="space-y-4">
        <ErrorState
          error={entryQuery.error}
          title={notFound ? "Запись не найдена" : "Не удалось загрузить запись"}
          onRetry={() => void entryQuery.refetch()}
        />
        <Button variant="outline" asChild>
          <Link to={`/features/${feature.id}/content`}>
            <ArrowLeft />
            К списку записей
          </Link>
        </Button>
      </div>
    );
  }

  const entry = entryQuery.data;
  const archived = Boolean(entry?.archived_at) || featureArchived;
  const hasPublished = Boolean(entry?.prod);
  // A draft newer than the published dev version is not part of dev yet, so
  // promoting would silently push stale content to prod.
  const pendingDraft = Boolean(
    entry?.draft && (!entry.dev || entry.draft.version > entry.dev.version),
  );
  const previewHasDraft = dirty || hasDraft;
  const previewStatus = previewHasDraft
    ? dirty
      ? "Есть несохранённые изменения"
      : "Черновик"
    : entry?.prod
      ? "Опубликовано в prod"
      : entry?.dev
        ? "Опубликовано в dev"
        : "Не опубликовано";
  const previewStatusVariant = previewHasDraft
    ? "warning"
    : entry?.prod || entry?.dev
      ? "success"
      : "muted";

  return (
    <div className="space-y-5">
      <header className="space-y-4 border-b border-border pb-5">
        <div className="flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
          <div className="min-w-0 space-y-2">
            <Link
              to={`/features/${feature.id}/content`}
              className="inline-flex min-h-8 items-center gap-1.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
            >
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
              К списку записей
            </Link>
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <h1 className="min-w-0 break-words text-xl font-semibold tracking-tight sm:text-2xl">
                {isNew ? "Новая запись" : title}
              </h1>
              <SaveStateBadge
                status={autosave.status}
                dirty={dirty}
                hasSavedDraft={hasDraft}
                hasPublished={Boolean(entry?.dev || entry?.prod)}
              />
              {entry?.archived_at && <Badge variant="muted">В архиве</Badge>}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {entry?.draft && <Badge variant="warning">Черновик v{entry.draft.version}</Badge>}
              {entry?.dev && <Badge variant="secondary">dev v{entry.dev.version}</Badge>}
              {entry?.prod && <Badge variant="success">prod v{entry.prod.version}</Badge>}
              <span className="text-2xs text-muted-foreground" aria-live="polite">
                {dirty
                  ? "Изменения ещё не сохранены"
                  : autosave.lastSavedAt
                    ? `Сохранено ${formatDateTime(autosave.lastSavedAt.toISOString())}`
                    : hasDraft
                      ? "Черновик сохранён"
                      : isNew
                        ? "Сохранение начнётся после первого изменения"
                        : "Нет несохранённых изменений"}
              </span>
            </div>
          </div>

          <div className="flex min-w-0 flex-wrap items-center gap-2 xl:justify-end">
            <input
              ref={importInput}
              type="file"
              accept="application/json,.json"
              className="sr-only"
              aria-label="Импортировать контент из JSON"
              onChange={(event) => void handleImportFile(event)}
            />
            <Button
              variant="outline"
              size="sm"
              onClick={() => void handleSaveNow()}
              loading={autosave.status === "saving"}
              disabled={
                archived || !dirty || importing || autosave.status === "saving" || autosave.status === "conflict"
              }
            >
              <Save />
              Сохранить
            </Button>
            <Button
              variant={preview ? "secondary" : "outline"}
              size="sm"
              onClick={() => setPreview((current) => !current)}
              aria-pressed={preview}
            >
              {preview ? <X /> : <Eye />}
              {preview ? "К редактору" : "Предпросмотр"}
            </Button>
            <Button
              size="sm"
              onClick={() => void handlePublish()}
              loading={publishMutation.isPending}
              disabled={
                archived ||
                importing ||
                blockingIssues.length > 0 ||
                !hasPublishableContent ||
                publishMutation.isPending
              }
              title={
                blockingIssues.length > 0
                  ? "Исправьте ошибки формы, чтобы опубликовать"
                  : !hasPublishableContent
                    ? "Сначала измените содержимое, чтобы создать черновик"
                    : undefined
              }
            >
              <Send />
              Опубликовать в dev
            </Button>
            {canPromote && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => promoteMutation.mutate()}
                loading={promoteMutation.isPending}
                disabled={archived || importing || !entry?.dev || pendingDraft || dirty}
                title={
                  !entry?.dev
                    ? "Сначала опубликуйте запись в dev"
                    : dirty || pendingDraft
                      ? "Сначала опубликуйте черновик в dev — иначе в prod уйдёт предыдущая версия"
                      : "Продвинуть текущую dev-версию в prod"
                }
              >
                Продвинуть в prod
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Дополнительные действия"
                >
                  <MoreHorizontal aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuItem
                  onSelect={() => importInput.current?.click()}
                  disabled={archived || importing}
                >
                  <FileUp className="h-4 w-4" />
                  Импорт JSON
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => setHistoryOpen(true)}
                  disabled={!entryId}
                >
                  <History className="h-4 w-4" />
                  История версий
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => undo()} disabled={!canUndo || importing}>
                  <Undo2 className="h-4 w-4" />
                  Отменить изменение
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => redo()} disabled={!canRedo || importing}>
                  <Redo2 className="h-4 w-4" />
                  Повторить изменение
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => void handleDiscardDraft()}
                  disabled={!hasDraft || !hasPublished || archived}
                >
                  <FileClock className="h-4 w-4" />
                  Отменить черновик
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      </header>

      {feature.type === "form" && (
        <p className="text-xs text-muted-foreground">
          Здесь редактируются тексты формы. Заявки находятся в разделе «Заявки».
        </p>
      )}

      {promotionError && (
        <InlineAlert tone="danger" title="Контент не подходит для активной prod-схемы">
          <p>{promotionError.message}</p>
          <ul className="mt-2 list-disc space-y-1 pl-5">
            {promotionError.details.map((detail, index) => (
              <li key={`${detail.path ?? "field"}-${index}`}>
                {detail.path && <code className="font-medium">{detail.path}: </code>}
                {detail.message}
              </li>
            ))}
          </ul>
        </InlineAlert>
      )}

      {autosave.conflict && (
        <InlineAlert
          tone="danger"
          title="Конфликт версий"
          actions={
            <>
              <Button size="sm" variant="outline" onClick={() => void handleReloadFromServer()}>
                <RefreshCw />
                Обновить из сервера
              </Button>
              <Button size="sm" variant="outline" onClick={() => void handleKeepLocal()}>
                <Save />
                Оставить мой вариант
              </Button>
            </>
          }
        >
          {autosave.conflict} Локальные изменения остались в редакторе и не были отправлены повторно.
        </InlineAlert>
      )}

      {autosave.status === "error" && Boolean(autosave.error) && (
        <InlineAlert
          tone="danger"
          title="Не удалось сохранить черновик"
          actions={
            <Button size="sm" variant="outline" onClick={() => void handleSaveNow()}>
              <RefreshCw />
              Повторить
            </Button>
          }
        >
          {errorMessage(autosave.error)} Изменения остались в редакторе.
        </InlineAlert>
      )}

      {archived && (
        <InlineAlert tone="warning" title="Редактирование заблокировано">
          {entry?.archived_at
            ? "Запись в архиве. Снимите её с архива, чтобы продолжить редактирование."
            : "Раздел в архиве. Снимите его с архива в настройках, чтобы продолжить работу."}
        </InlineAlert>
      )}

      {preview ? (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-sm font-medium">
                {previewHasDraft ? "Предпросмотр черновика" : "Предпросмотр записи"}
              </h2>
              <Badge variant={previewStatusVariant}>{previewStatus}</Badge>
            </div>
            <div
              role="group"
              aria-label="Ширина предпросмотра"
              className="flex items-center gap-1 rounded-full bg-muted p-1"
            >
              {widthOptions.map((option) => {
                const Icon = option.icon;
                return (
                  <button
                    key={option.value}
                    type="button"
                    onClick={() => setPreviewWidth(option.value)}
                    aria-pressed={previewWidth === option.value}
                    aria-label={option.label}
                    className={cn(
                      "flex h-8 w-9 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-background hover:text-foreground",
                      previewWidth === option.value && "bg-background text-foreground shadow-sm",
                    )}
                  >
                    <Icon className="h-4 w-4" />
                    <span className="sr-only">{option.label}</span>
                  </button>
                );
              })}
            </div>
          </div>
          <div className="min-h-48 overflow-hidden rounded-xl bg-card p-3 sm:p-6">
            <ContentPreview
              metadata={editorState.metadata}
              blocks={editorState.blocks}
              fields={fields}
              width={previewWidth}
            />
          </div>
        </div>
      ) : (
        <div className="mx-auto w-full max-w-4xl space-y-8">
          {titleField ? (
            <section
              id={entryFieldAnchorId(titleField.key)}
              tabIndex={-1}
              role="group"
              aria-labelledby="entry-title-label"
              aria-describedby={blockingIssues.some((issue) => issue.path === titleField.key) ? "entry-title-error" : undefined}
              className="scroll-mt-8 space-y-2 border-b border-border pb-7 focus-visible:rounded-md"
            >
              <Label
                id="entry-title-label"
                htmlFor="entry-title"
                className="text-sm font-medium"
              >
                Название записи
                {titleField.required && <span className="ml-0.5 text-destructive">*</span>}
              </Label>
              {blockingIssues.some((issue) => issue.path === titleField.key) && (
                <p id="entry-title-error" className="flex items-center gap-1.5 text-xs text-destructive">
                  <span aria-hidden="true">!</span>
                  {blockingIssues.find((issue) => issue.path === titleField.key)?.message}
                </p>
              )}
              <Input
                id="entry-title"
                aria-invalid={blockingIssues.some((issue) => issue.path === titleField.key)}
                aria-describedby={blockingIssues.some((issue) => issue.path === titleField.key) ? "entry-title-error" : undefined}
                className="h-auto rounded-lg px-4 py-3 text-lg font-semibold sm:text-xl"
                value={
                  typeof editorState.metadata[titleField.key] === "string"
                    ? (editorState.metadata[titleField.key] as string)
                    : ""
                }
                placeholder={titleField.placeholder || "Без названия"}
                disabled={archived || importing}
                maxLength={titleField.maxLength}
                onChange={(event) =>
                  updateMetadata(titleField.key, event.target.value)
                }
              />
            </section>
          ) : (
            <p className="text-sm text-muted-foreground">В схеме нет строкового поля для названия.</p>
          )}

          {blocksKey && (
            <section className="space-y-2 border-b border-border pb-6">
              <h2 className="text-sm font-medium">Устаревшие блоки</h2>
              {blockWarnings.length > 0 && (
                <InlineAlert tone="warning" title="Часть блочных данных не загружена">
                  <ul className="list-disc pl-4">
                    {blockWarnings.map((warning) => (
                      <li key={warning}>{warning}</li>
                    ))}
                  </ul>
                </InlineAlert>
              )}
              <p className="text-xs leading-5 text-muted-foreground">
                {preservedBlocks.length} сохранённых блоков останутся без изменений.
              </p>
            </section>
          )}

          {blockingIssues.length > 0 && (
            <div
              className="space-y-2 rounded-lg border border-destructive/30 bg-destructive/5 p-3"
              aria-live="polite"
            >
              <p className="flex items-center gap-2 text-sm font-medium text-destructive">
                <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden="true" />
                {blockingIssues.length} {blockingIssues.length === 1 ? "ошибка" : blockingIssues.length < 5 ? "ошибки" : "ошибок"} · публикация недоступна
              </p>
              <ul className="flex flex-wrap gap-2 text-xs">
                {blockingIssues.slice(0, 8).map((issue) => (
                  <li key={issue.path + issue.message}>
                    <button
                      type="button"
                      className="rounded-full border border-border bg-background px-2.5 py-1.5 text-left text-muted-foreground hover:text-foreground"
                      aria-label={`${issue.path}: ${issue.message}`}
                      onClick={() => focusField(issue.path)}
                    >
                      <span className="font-medium text-foreground">{issue.path}</span>
                      <span className="px-1" aria-hidden="true">·</span>
                      {issue.message}
                    </button>
                  </li>
                ))}
                {blockingIssues.length > 8 && (
                  <li className="self-center text-muted-foreground">
                    Ещё {blockingIssues.length - 8}
                  </li>
                )}
              </ul>
            </div>
          )}

          <EntryFieldsForm
            fields={editableFields}
            metadata={editorState.metadata}
            onChange={updateMetadata}
            disabled={archived || importing}
            issues={blockingIssues}
            featureKey={activeContentFeature.key}
            buildDigest={activeContentFeature.buildDigest}
            schemaKind="content"
          />
        </div>
      )}

      <Dialog open={historyOpen} onOpenChange={setHistoryOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>История версий</DialogTitle>
            <DialogDescription>
              Опубликованные версии неизменяемы. Откат переключает публичную версию
              на ранее опубликованную.
            </DialogDescription>
          </DialogHeader>

          {versionsQuery.isLoading ? (
            <div className="space-y-2">
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-12 w-full" />
            </div>
          ) : versionsQuery.error ? (
            <ErrorState
              error={versionsQuery.error}
              title="Не удалось загрузить историю"
              onRetry={() => void versionsQuery.refetch()}
            />
          ) : (versionsQuery.data?.items.length ?? 0) === 0 ? (
            <EmptyState
              title="История пуста"
              description="Версии появятся после первого сохранения черновика."
            />
          ) : (
            <div className="space-y-4">
              <ul className="divide-y divide-border rounded-md border border-border">
                {versionsQuery.data?.items.map((version) => (
                  <li
                    key={version.id}
                    className="flex flex-wrap items-center justify-between gap-2 px-3 py-2.5"
                  >
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-medium">v{version.version}</span>
                        <VersionStatusBadge status={version.status} />
                        <span className="text-2xs text-muted-foreground">
                          {formatDateTime(version.created_at)}
                        </span>
                      </div>
                      <p className="mt-0.5 truncate text-2xs text-muted-foreground">
                        {truncate(JSON.stringify(version.data), 120)}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setSelectedVersion(version)}
                      >
                        Сравнить
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => void handleRestoreVersion(version)}
                      >
                        Как черновик
                      </Button>
                      {canPromote && version.status === "published" && (
                        <Button
                          size="sm"
                          variant="ghost"
                          loading={rollbackMutation.isPending}
                          onClick={() => rollbackMutation.mutate(version.version)}
                        >
                          Опубликовать снова
                        </Button>
                      )}
                    </div>
                  </li>
                ))}
              </ul>

              {selectedVersion && (
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h3 className="text-sm font-semibold">
                      Сравнение: опубликованная ↔ v{selectedVersion.version}
                    </h3>
                    <DiffSummaryBadges entries={diffEntries} />
                  </div>
                  <DiffList entries={diffEntries} />
                </div>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>

      {guard.dialog}
    </div>
  );
}
