import type { Media } from "@/types";
import { MediaStatusBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes, formatDate, formatDateTime } from "@/lib/format";
import { MediaThumb } from "@/domains/media/media-preview";
import { MediaActions, mimeLabel } from "./MediaDetailDialog";

export function MediaGridCard({
  media,
  onOpen,
}: {
  media: Media;
  onOpen: () => void;
}) {
  return (
    <div className="group overflow-hidden rounded-lg border border-border bg-card">
      <button
        type="button"
        onClick={onOpen}
        className="block w-full text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Открыть ${media.original_filename}`}
      >
        <div className="relative aspect-[4/3] bg-muted/40">
          <MediaThumb media={media} />
          <span className="absolute left-2 top-2">
            <MediaStatusBadge status={media.status} />
          </span>
          {media.visibility === "private" && <span className="absolute right-2 top-2"><Badge variant="secondary">Закрытый</Badge></span>}
        </div>
        <div className="p-3">
          <p
            className="truncate text-xs font-medium"
            title={media.original_filename}
          >
            {media.original_filename}
          </p>
          <p className="mt-0.5 truncate text-2xs text-muted-foreground">
            {mimeLabel(media.content_type)} · {formatBytes(media.size_bytes)}
          </p>
          <p className="mt-0.5 text-2xs text-muted-foreground">
            {formatDate(media.created_at)}
          </p>
        </div>
      </button>
      <div className="flex items-center justify-between border-t border-border px-2 py-1">
        <span className="truncate px-1 text-2xs text-muted-foreground">
          {media.content_type}
        </span>
        <MediaActions media={media} onOpen={onOpen} />
      </div>
    </div>
  );
}

export function MediaTable({
  items,
  onOpen,
}: {
  items: Media[];
  onOpen: (media: Media) => void;
}) {
  return (
    <div className="table-wrap">
      <table className="data-table min-w-[760px]">
        <thead>
          <tr>
            <th scope="col">Файл</th>
            <th scope="col">Тип</th>
            <th scope="col">Размер</th>
            <th scope="col">Статус</th>
            <th scope="col">Загружен</th>
            <th scope="col"><span className="sr-only">Действия</span></th>
          </tr>
        </thead>
        <tbody>
          {items.map((media) => (
            <tr key={media.id} className="group">
              <td>
                <div className="flex min-w-0 items-center gap-2.5">
                  <button
                    type="button"
                    onClick={() => onOpen(media)}
                    className="h-9 w-9 shrink-0 overflow-hidden rounded border border-border bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    aria-label={`Открыть ${media.original_filename}`}
                  >
                    <MediaThumb media={media} />
                  </button>
                  <button
                    type="button"
                    onClick={() => onOpen(media)}
                    className="min-w-0 rounded-sm text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <span className="block max-w-[22rem] truncate text-sm font-medium hover:text-primary">
                      {media.original_filename}
                    </span>
                    <code className="block truncate text-2xs text-muted-foreground">
                      {media.content_type}
                    </code>
                  </button>
                </div>
              </td>
              <td>
                <span className="text-xs text-muted-foreground">
                  {mimeLabel(media.content_type)}
                </span>
              </td>
              <td className="text-xs text-muted-foreground">
                {formatBytes(media.size_bytes)}
              </td>
              <td>
                <MediaStatusBadge status={media.status} />
              </td>
              <td className="text-xs text-muted-foreground">
                {formatDateTime(media.created_at)}
              </td>
              <td>
                <div className="flex items-center justify-end gap-1">
                  <Button variant="ghost" size="sm" onClick={() => onOpen(media)}>
                    Открыть
                  </Button>
                  <MediaActions media={media} onOpen={() => onOpen(media)} />
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
