import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { Grid2X2, LayoutList, Upload } from "lucide-react";
import { api } from "@/api";
import type { Media } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import {
  hasActiveMediaGalleryFilters,
  mediaGalleryRangeError,
  mediaGalleryResultsState,
  megabytesToBytes,
  mergeMediaGalleryParams,
  normalizeMediaGalleryParams,
  parseMediaGalleryFilters,
  type MediaGalleryParamUpdates,
} from "@/domains/media/gallery-filters";
import { PageHeader, useDocumentTitle } from "@/components/common/page";
import { Button } from "@/components/ui/button";

import { MediaDetailDialog } from "./components/MediaDetailDialog";
import { UploadDialog } from "./components/UploadDialog";
import { MediaFilters } from "./components/media-page/MediaFilters";
import { MediaGalleryResults } from "./components/media-page/MediaGalleryResults";

function useMediaFilterParams() {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawSearch = searchParams.toString();
  const filters = parseMediaGalleryFilters(searchParams);

  useEffect(() => {
    const normalized = normalizeMediaGalleryParams(new URLSearchParams(rawSearch));
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
  const { q, type, status, sort, from, to, min, max, view, update } =
    useMediaFilterParams();
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

  const minSize = megabytesToBytes(min);
  const maxSize = megabytesToBytes(max);
  const rangeError = mediaGalleryRangeError({ from, to, min, max });

  const mediaQuery = useInfiniteQuery({
    queryKey: queryKeys.media.list({
      q,
      type,
      status,
      sort,
      from,
      to,
      min: minSize,
      max: maxSize,
    }),
    queryFn: ({ pageParam, signal }) =>
      api.media.list(
        {
          q: q || undefined,
          type: type === "all" ? "" : type,
          status: status === "all" ? "" : status,
          sort,
          created_from: from || undefined,
          created_to: to || undefined,
          min_size: minSize,
          max_size: maxSize,
          limit: 50,
          cursor: pageParam ?? undefined,
        },
        signal,
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
    enabled: !rangeError,
  });

  const items = useMemo(
    () => mediaQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [mediaQuery.data],
  );

  const filtersActive = hasActiveMediaGalleryFilters({ q, type, status, sort, from, to, min, max, view });
  const resultsState = mediaGalleryResultsState({
    invalidRange: Boolean(rangeError),
    loading: mediaQuery.isLoading,
    error: Boolean(mediaQuery.error),
    itemCount: items.length,
    filtersActive,
  });

  const resetFilters = () => {
    setSearchInput("");
    update({
      q: null,
      type: null,
      status: null,
      sort: null,
      from: null,
      to: null,
      min: null,
      max: null,
    });
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
                size="icon-sm"
                onClick={() => update({ view: null })}
                aria-label="Сетка"
                aria-pressed={view === "grid"}
              >
                <Grid2X2 />
              </Button>
              <Button
                variant={view === "list" ? "secondary" : "ghost"}
                size="icon-sm"
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

      <MediaFilters
        searchInput={searchInput}
        onSearchInputChange={setSearchInput}
        type={type}
        status={status}
        sort={sort}
        from={from}
        to={to}
        min={min}
        max={max}
        filtersActive={filtersActive}
        itemCount={items.length}
        hasNextPage={Boolean(mediaQuery.hasNextPage)}
        rangeError={rangeError}
        onUpdate={update}
        onReset={resetFilters}
      />

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
        media={selected}
        open={detailOpen}
        onClose={() => setDetailOpen(false)}
      />
    </div>
  );
}
