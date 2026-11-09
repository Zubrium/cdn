import { ImagePlus, Upload } from "lucide-react";
import type { Media } from "@/types";
import { EmptyState, ErrorState, InlineAlert, TableSkeleton } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/misc";
import { MediaGridCard, MediaTable } from "../MediaResults";

type ResultsState = "invalid-range" | "loading" | "error" | "empty" | "empty-filtered" | "results" | "results-with-error";

type Props = {
  state: ResultsState;
  error: unknown;
  items: Media[];
  view: "grid" | "list";
  hasNextPage: boolean;
  fetchingNextPage: boolean;
  onOpen: (media: Media) => void;
  onRetry: () => void;
  onLoadMore: () => void;
  onUpload: () => void;
};

export function MediaGalleryResults({ state, error, items, view, hasNextPage, fetchingNextPage, onOpen, onRetry, onLoadMore, onUpload }: Props) {
  return (
    <section aria-label="Результаты медиатеки">
      {state === "invalid-range" ? (
        <p className="rounded-lg border border-border bg-muted/30 p-4 text-sm text-muted-foreground" role="status">Исправьте диапазон фильтров, чтобы загрузить список файлов.</p>
      ) : state === "loading" ? (
        view === "grid" ? <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4" role="status" aria-label="Загружаем файлы" aria-busy="true">{Array.from({ length: 8 }).map((_, index) => <Skeleton key={index} className="aspect-[4/3] w-full rounded-lg" />)}</div> : <div role="status" aria-label="Загружаем файлы" aria-busy="true"><TableSkeleton rows={6} /></div>
      ) : state === "error" ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : state === "empty" || state === "empty-filtered" ? (
        <EmptyState icon={ImagePlus} title={state === "empty-filtered" ? "Файлы не найдены" : "Медиатека пуста"} description={state === "empty-filtered" ? "Измените фильтры или загрузите новый файл." : "Загрузите изображения, PDF или видео, чтобы использовать их в контенте и формах."} action={<Button onClick={onUpload}><Upload />Загрузить файлы</Button>} />
      ) : (
        <>
          {state === "results-with-error" && <InlineAlert tone="warning"><div className="flex flex-wrap items-center justify-between gap-2"><span>Не удалось обновить список. Уже загруженные файлы доступны.</span><Button variant="outline" size="sm" onClick={onRetry}>Повторить</Button></div></InlineAlert>}
          {view === "grid" ? <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">{items.map((media) => <MediaGridCard key={media.id} media={media} onOpen={() => onOpen(media)} />)}</div> : <><div className="hidden md:block"><MediaTable items={items} onOpen={onOpen} /></div><div className="grid grid-cols-2 gap-3 md:hidden">{items.map((media) => <MediaGridCard key={media.id} media={media} onOpen={() => onOpen(media)} />)}</div></>}
          {hasNextPage && <div className="mt-4 flex justify-center"><Button variant="outline" onClick={onLoadMore} loading={fetchingNextPage}>Показать ещё</Button></div>}
        </>
      )}
    </section>
  );
}
