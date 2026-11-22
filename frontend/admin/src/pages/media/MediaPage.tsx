import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { Grid2X2, LayoutList, Search, Upload, X } from "lucide-react";
import { api } from "@/api";
import type { Media } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import {
  mergeMediaGalleryParams,
  normalizeMediaGalleryParams,
  parseMediaGalleryFilters,
  type MediaGalleryParamUpdates,
} from "@/domains/media/gallery-filters";
import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { MediaDetailDialog } from "./components/MediaDetailDialog";
import { UploadDialog } from "./components/UploadDialog";
import { MediaGalleryResults } from "./components/media-page/MediaGalleryResults";

function useMediaFilterParams() {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawSearch = searchParams.toString();
  const filters = parseMediaGalleryFilters(searchParams);

  useEffect(() => {
    const normalized = normalizeMediaGalleryParams(new URLSearchParams(rawSearch));
    for (const key of ["type", "status", "sort", "from", "to", "min", "max"]) normalized.delete(key);
    if (normalized.toString() !== rawSearch) {
      setSearchParams(normalized, { replace: true });
    }
  }, [rawSearch, setSearchParams]);

  const update = (values: MediaGalleryParamUpdates, replace = false) => {
    setSearchParams((current) => mergeMediaGalleryParams(current, values), { replace });
  };

  return { ...filters, update };
}

export default function MediaPage() {
  useDocumentTitle("Файлы");
  const { q, view, update } = useMediaFilterParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const group = searchParams.get("group") ?? "all";
  const [searchInput, setSearchInput] = useState(q);
  const debouncedSearch = useDebouncedValue(searchInput, 250);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [selected, setSelected] = useState<Media | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);

  useEffect(() => {
    setSearchInput(q);
  }, [q]);

  useEffect(() => {
    if (debouncedSearch !== q) {
      update({ q: debouncedSearch || null }, true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedSearch]);

  const mediaQuery = useInfiniteQuery({
    queryKey: queryKeys.media.list({ q }),
    queryFn: ({ pageParam, signal }) =>
      api.media.list(
        {
          q: q || undefined,
          limit: 50,
          cursor: pageParam ?? undefined,
        },
        signal,
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
  });

  const allItems = useMemo(
    () => mediaQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [mediaQuery.data],
  );
  const groups = useMemo(
    () => [...new Set(allItems.flatMap((media) => media.groups ?? []))].sort(),
    [allItems],
  );
  const items = group === "all" ? allItems : allItems.filter((media) => media.groups?.includes(group));
  const filtersActive = Boolean(q || group !== "all");
  const resultsState = mediaQuery.isLoading
    ? "loading"
    : mediaQuery.error && items.length === 0
      ? "error"
      : items.length === 0
        ? filtersActive ? "empty-filtered" : "empty"
        : mediaQuery.error ? "results-with-error" : "results";

  const selectGroup = (value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value === "all") next.delete("group");
    else next.set("group", value);
    setSearchParams(next);
  };

  const resetFilters = () => {
    setSearchInput("");
    const next = new URLSearchParams(searchParams);
    next.delete("q");
    next.delete("group");
    setSearchParams(next);
  };

  const openMedia = (media: Media) => {
    setSelected(media);
    setDetailOpen(true);
  };

  return (
    <div className="page-grid">
      <PageHeader
        title="Файлы"
        description="Файлы для контента и форм. В документах хранятся ID объектов, поэтому переименование не ломает опубликованные страницы."
        actions={
          <>
            <div className="flex rounded-md border border-border p-0.5">
              <Button
                variant={view === "grid" ? "secondary" : "ghost"}
                size="icon"
                className="size-11"
                onClick={() => update({ view: null })}
                aria-label="Сетка"
                aria-pressed={view === "grid"}
              >
                <Grid2X2 />
              </Button>
              <Button
                variant={view === "list" ? "secondary" : "ghost"}
                size="icon"
                className="size-11"
                onClick={() => update({ view: "list" })}
                aria-label="Список"
                aria-pressed={view === "list"}
              >
                <LayoutList />
              </Button>
            </div>
            <Button onClick={() => setUploadOpen(true)}>
              <Upload />
              Загрузить
            </Button>
          </>
        }
      />

      <section aria-label="Фильтры файлов" className="mb-4 space-y-3">
        <div className="flex flex-col gap-2 sm:flex-row">
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <Input type="search" value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder="Поиск по имени файла" aria-label="Поиск медиа" className="pl-9 pr-9" />
            {searchInput && <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:bg-accent" onClick={() => setSearchInput("")} aria-label="Очистить поиск"><X className="h-3.5 w-3.5" /></button>}
          </div>
          <Select value={group} onValueChange={selectGroup}>
            <SelectTrigger className="w-full sm:w-56" aria-label="Группа доступа"><SelectValue placeholder="Все группы" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Все группы</SelectItem>
              {groups.map((name) => <SelectItem key={name} value={name}>{name}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {filtersActive && <Button variant="ghost" size="sm" onClick={resetFilters}><X />Сбросить фильтры</Button>}
          <span className="text-xs text-muted-foreground" role="status">Загружено: {items.length}{mediaQuery.hasNextPage ? "+" : ""}</span>
          {group !== "all" && <span className="text-2xs text-muted-foreground">Группа применяется к уже загруженным результатам.</span>}
        </div>
      </section>

      <MediaGalleryResults
        state={resultsState}
        error={mediaQuery.error}
        items={items}
        view={view}
        hasNextPage={Boolean(mediaQuery.hasNextPage)}
        fetchingNextPage={mediaQuery.isFetchingNextPage}
        onOpen={openMedia}
        onRetry={() => void mediaQuery.refetch()}
        onLoadMore={() => void mediaQuery.fetchNextPage()}
        onUpload={() => setUploadOpen(true)}
      />
      <UploadDialog open={uploadOpen} onClose={() => setUploadOpen(false)} />
      <MediaDetailDialog
        key={selected ? `${selected.id}:${selected.revision}` : "none"}
        media={selected}
        open={detailOpen}
        onClose={() => setDetailOpen(false)}
      />
    </div>
  );
}
