import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Check, ImagePlus, Search, Upload } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { api } from "@/api";
import type { Media } from "@/types";
import type { MediaRenditionContext } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { formatBytes } from "@/lib/format";
import { cn, uid } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { EmptyState, ErrorState, LoadingPage } from "@/components/common/page";
import { MediaStatusBadge } from "@/components/common/status-badges";
import { MediaThumb } from "./media-preview";
import { prepareMediaRenditions } from "./rendition-preparation";

type Accept = "image" | "raster-image" | "video" | "font" | "all";

function accepts(media: Media, accept: Accept): boolean {
  if (accept === "image") return media.content_type.startsWith("image/");
  if (accept === "raster-image") return ["image/jpeg", "image/png", "image/webp"].includes(media.content_type);
  if (accept === "video") return media.content_type.startsWith("video/");
  if (accept === "font") return media.content_type === "font/woff2";
  return true;
}

export function MediaPickerDialog({
  open,
  onClose,
  onSelect,
  mode = "single",
  accept = "all",
  initialSelected = [],
  title = "Выбрать медиа",
  preparation,
  preparations = [],
}: {
  open: boolean;
  onClose: () => void;
  onSelect: (media: Media[]) => void;
  mode?: "single" | "multiple";
  accept?: Accept;
  initialSelected?: string[];
  title?: string;
  preparation?: MediaRenditionContext;
  preparations?: MediaRenditionContext[];
}) {
  const [search, setSearch] = useState("");
  const [isPreparing, setIsPreparing] = useState(false);
  const [selectedIds, setSelectedIds] = useState<string[]>(initialSelected);
  const [selectedItems, setSelectedItems] = useState<Record<string, Media>>({});
  const debouncedSearch = useDebouncedValue(search.trim(), 250);

  useEffect(() => {
    if (open) {
      setSelectedIds(initialSelected);
      setSelectedItems({});
      setSearch("");
    }
    // initialSelected intentionally only applied when opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const mediaQuery = useInfiniteQuery({
    queryKey: queryKeys.media.list({ status: "ready", q: debouncedSearch, limit: 50 }),
    queryFn: ({ pageParam, signal }) =>
      api.media.list(
        {
          status: "ready",
          q: debouncedSearch || undefined,
          limit: 50,
          cursor: pageParam ?? undefined,
        },
        signal,
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
    enabled: open,
  });
  const loadedItems = useMemo(
    () => mediaQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [mediaQuery.data],
  );
  const items = useMemo(
    () =>
      loadedItems.filter(
        (media) =>
          media.visibility !== "private" && accepts(media, accept) &&
          (!search.trim() ||
            media.original_filename.toLowerCase().includes(search.trim().toLowerCase())),
      ),
    [loadedItems, accept, search],
  );

  const selectedMedia = useMemo(
    () => selectedIds
      .map((id) => selectedItems[id] ?? loadedItems.find((media) => media.id === id))
      .filter((media): media is Media => Boolean(media && media.visibility !== "private")),
    [loadedItems, selectedIds, selectedItems],
  );

  const toggle = (media: Media) => {
    if (mode === "single") {
      setSelectedIds([media.id]);
      setSelectedItems({ [media.id]: media });
      return;
    }
    if (selectedIds.includes(media.id)) {
      setSelectedIds((current) => current.filter((id) => id !== media.id));
      setSelectedItems((current) => {
        const next = { ...current };
        delete next[media.id];
        return next;
      });
      return;
    }
    setSelectedIds((current) => [...current, media.id]);
    setSelectedItems((current) => ({ ...current, [media.id]: media }));
  };

  const confirm = async () => {
    if (isPreparing) return;
    const selection = selectedMedia;
    const contexts = [...preparations, ...(preparation ? [preparation] : [])]
      .filter((item, index, all) => all.findIndex((candidate) => JSON.stringify(candidate) === JSON.stringify(item)) === index);
    if (selection.length > 0 && contexts.length > 0) {
      setIsPreparing(true);
      try {
        await prepareMediaRenditions(selection.map((media) => media.id), contexts, api.media);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "Не удалось подготовить медиа");
        return;
      } finally {
        setIsPreparing(false);
      }
    }
    onSelect(selection);
    onClose();
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !isPreparing && onClose()}>
      <DialogContent className="max-w-5xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            Выбираются ID медиаобъектов, а не URL. После выбора блок сохраняет
            ссылку на файл. Для сайта доступны публичные файлы. Новые файлы добавляются
            отдельно в медиатеке.
          </DialogDescription>
        </DialogHeader>

        {isPreparing && <p role="status" className="text-sm text-muted-foreground">Готовим варианты файлов для размещённых фич…</p>}

        <div className="flex flex-col gap-2 sm:flex-row">
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Поиск по имени файла"
              className="pl-9"
              aria-label="Поиск медиа"
            />
          </div>
          <Button asChild variant="outline">
            <Link to="/media" target="_blank" rel="noreferrer">
              <Upload />Открыть медиатеку
            </Link>
          </Button>
        </div>

        <div className="max-h-[50vh] min-h-[16rem] overflow-y-auto rounded-md border border-border p-3 thin-scrollbar">
          {mediaQuery.isLoading ? (
            <LoadingPage label="Загружаем медиатеку…" />
          ) : mediaQuery.error ? (
            <ErrorState
              error={mediaQuery.error}
              onRetry={() => void mediaQuery.refetch()}
            />
          ) : items.length === 0 ? (
            <EmptyState
              icon={ImagePlus}
              title={search ? "Файлы не найдены" : "Нет доступных файлов"}
              description={
                search
                  ? "Измените запрос или добавьте файл в медиатеке."
                  : "Добавьте изображение или видео в медиатеке, затем выберите его здесь."
              }
              action={
                <Button asChild variant="outline">
                  <Link to="/media" target="_blank" rel="noreferrer">
                    <Upload />Открыть медиатеку
                  </Link>
                </Button>
              }
              className="border-0"
            />
          ) : (
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 md:grid-cols-4">
              {items.map((media) => {
                const selected = selectedIds.includes(media.id);
                return (
                  <button
                    key={media.id}
                    type="button"
                    onClick={() => toggle(media)}
                    aria-pressed={selected}
                    className={cn(
                      "group relative overflow-hidden rounded-md border text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      selected
                        ? "border-primary ring-1 ring-primary/30"
                        : "border-border hover:border-primary/40",
                    )}
                  >
                    <div className="aspect-[4/3] bg-muted/40">
                      <MediaThumb media={media} />
                    </div>
                    <div className="p-2">
                      <p className="truncate text-xs font-medium" title={media.original_filename}>
                        {media.original_filename}
                      </p>
                      <p className="mt-0.5 text-2xs text-muted-foreground">
                        {formatBytes(media.size_bytes)} · {media.content_type}
                      </p>
                    </div>
                    {selected && (
                      <span className="absolute right-2 top-2 flex h-5 w-5 items-center justify-center rounded-full bg-primary text-primary-foreground">
                        <Check className="h-3 w-3" />
                      </span>
                    )}
                    {media.status !== "ready" && (
                      <span className="absolute left-2 top-2">
                        <MediaStatusBadge status={media.status} />
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          )}
        </div>

        {mediaQuery.hasNextPage && (
          <div className="flex justify-center">
            <Button
              type="button"
              variant="outline"
              onClick={() => void mediaQuery.fetchNextPage()}
              loading={mediaQuery.isFetchingNextPage}
              disabled={mediaQuery.isFetchingNextPage}
            >
              Загрузить ещё
            </Button>
          </div>
        )}

        <DialogFooter className="items-center sm:justify-between">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="muted">Выбрано: {selectedIds.length}</Badge>
            {mode === "multiple" && <span>Можно выбрать несколько файлов</span>}
          </div>
          <div className="flex gap-2">
            <Button variant="outline" onClick={onClose} disabled={isPreparing}>
              Отмена
            </Button>
            <Button onClick={() => void confirm()} disabled={selectedIds.length === 0 || isPreparing}>
              {isPreparing ? <span>Подготовка…</span> : <><Check />Выбрать</>}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function createPendingUploadId(): string {
  return uid("upload");
}
