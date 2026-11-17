import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Download, MoreHorizontal, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, absolutePublicMediaUrl } from "@/api";
import { useAuth } from "@/app/auth";
import type { FileReference, Media } from "@/types";
import { queryKeys } from "@/lib/query-keys";
import { errorMessage } from "@/lib/errors";
import { canDeleteGalleryFile } from "@/domains/media/gallery-access";
import { useConfirm } from "@/components/common/confirm";
import { MediaStatusBadge } from "@/components/common/status-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
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
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/misc";
import { formatBytes, formatDateTime } from "@/lib/format";
import { isImageMedia, MediaThumb, useMediaPreviewUrl } from "@/domains/media/media-preview";

export function mimeLabel(contentType: string): string {
  if (contentType.startsWith("image/")) return "Изображение";
  if (contentType.startsWith("video/")) return "Видео";
  if (contentType === "application/pdf") return "PDF";
  return "Файл";
}

function copyText(value: string, message: string) {
  if (navigator.clipboard?.writeText) {
    void navigator.clipboard.writeText(value).then(
      () => toast.success(message),
      () => toast.error("Не удалось скопировать"),
    );
    return;
  }
  toast.error("Буфер обмена недоступен");
}

async function mediaLink(media: Media): Promise<string> {
  if (media.visibility !== "private") return absolutePublicMediaUrl(media.id);
  const limits = await api.media.limits();
  if (limits.linkMaxTTLSeconds < 1) throw new Error("Срок действия ссылки не настроен");
  return (await api.media.createLink(media.id, limits.linkMaxTTLSeconds)).url;
}

async function copyMediaLink(media: Media) {
  try { copyText(await mediaLink(media), media.visibility === "private" ? "Временная ссылка скопирована" : "Ссылка скопирована"); }
  catch (error) { toast.error(errorMessage(error)); }
}

async function downloadMedia(media: Media) {
  try {
    const url = new URL(await mediaLink(media), window.location.origin);
    url.searchParams.set("download", "1");
    const link = document.createElement("a");
    link.href = url.toString();
    link.download = media.original_filename;
    link.rel = "noopener";
    link.target = "_blank";
    document.body.appendChild(link);
    link.click();
    link.remove();
  } catch (error) { toast.error(errorMessage(error)); }
}

const referenceLabels: Record<string,string> = {
  snapshot:"Снапшот", "published-content":"Опубликованный контент", draft:"Черновик контента", history:"История контента", submission:"Заявка", "content-version":"Версия контента", "legacy-config":"Настройка сайта", "legacy-design":"Настройка дизайна", "legacy-metadata-draft":"Черновик метаданных", "legacy-metadata-dev":"Метаданные", "legacy-metadata-prod":"Метаданные",
};
function referenceLabel(ref:FileReference):string {return referenceLabels[ref.kind] ?? "Связанные данные";}
function UsageList({ mediaId }: { mediaId: string }) {
  const usageQuery = useQuery({ queryKey:[...queryKeys.media.detail(mediaId),"file-references"], queryFn:({signal})=>api.media.references(mediaId,signal) });
  if (usageQuery.isLoading) return <Skeleton className="h-4 w-2/3" aria-label="Загрузка использования"/>;
  if (usageQuery.error) return <p className="text-xs text-destructive">Не удалось загрузить использование: {errorMessage(usageQuery.error)}</p>;
  const refs=usageQuery.data?.items ?? [];
  if (!refs.length) return <p className="text-xs text-muted-foreground">Известные использования не найдены. Внешние системы могут хранить собственные ссылки.</p>;
  return <ul className="space-y-1.5">{refs.map(ref=><li key={`${ref.kind}-${ref.id}`} className="text-xs"><Badge variant="secondary">{referenceLabel(ref)}</Badge> <span className="text-muted-foreground">{ref.description}</span></li>)}</ul>;
}
async function confirmFileDeletion(media: Media, confirm: ReturnType<typeof useConfirm>): Promise<{revision:string} | null> {
  try {
    const usage=await api.media.references(media.id);
    const accepted=await confirm({title:"Удалить файл?",description:<div className="space-y-2"><p>Файл «{media.original_filename}» будет удалён. Его ссылки будут очищены в затронутых снапшотах, черновиках, заявках и истории; сами записи сохранятся.</p>{usage.items.length>0?<div className="rounded-md border border-warning/40 bg-warning/10 p-2 text-xs"><p className="font-medium">Известные использования: {usage.items.length}</p><ul className="mt-1 max-h-40 space-y-0.5 overflow-auto">{usage.items.map(ref=><li key={`${ref.kind}-${ref.id}`}>{referenceLabel(ref)} · {ref.description}</li>)}</ul></div>:<p className="text-muted-foreground">Известные использования не найдены.</p>}<p className="text-muted-foreground">Внешние ссылки других систем определить невозможно.</p></div>,confirmLabel:"Удалить и очистить ссылки",destructive:true});
    return accepted ? {revision:usage.revision} : null;
  } catch(error){toast.error(`Не удалось проверить использования: ${errorMessage(error)}`);return null;}
}

export function MediaDetailDialog({
  media,
  open,
  onClose,
}: {
  media: Media | null;
  open: boolean;
  onClose: () => void;
}) {
  const { principal } = useAuth();
  const canDelete = canDeleteGalleryFile(principal?.role);
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [filename, setFilename] = useState(media?.original_filename ?? "");
  const previewUrl = useMediaPreviewUrl(media && (isImageMedia(media) || media.content_type.startsWith("video/")) ? media : null);

  useEffect(() => {
    setFilename(media?.original_filename ?? "");
  }, [media]);


  const renameMutation = useMutation({
    mutationFn: (name: string) => api.media.rename(media!.id, name),
    onSuccess: (updated) => {
      toast.success("Имя файла обновлено");
      setFilename(updated.original_filename);
      void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const deleteMutation = useMutation({
    mutationFn: (revision: string) => api.media.remove(media!.id, true, revision),
    onSuccess: () => {
      toast.success("Файл удалён");
      void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
      onClose();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const retryMutation = useMutation({
    mutationFn: () => api.media.retry(media!.id),
    onSuccess: () => {
      toast.success("Обработка запущена повторно");
      void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const requestDelete = async () => {
    if (!media) return;
    const result = await confirmFileDeletion(media, confirm);
    if (result) deleteMutation.mutate(result.revision);
  };

  if (!media) return null;

  const renamed = filename.trim() !== media.original_filename;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="truncate">{media.original_filename}</DialogTitle>
          <DialogDescription>
            {mimeLabel(media.content_type)} · {media.content_type} ·{" "}
            {formatBytes(media.size_bytes)}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_16rem]">
          <div className="overflow-hidden rounded-lg border border-border bg-muted/30">
            {isImageMedia(media) && media.status === "ready" && previewUrl ? (
              <img
                src={previewUrl}
                alt={media.original_filename}
                className="max-h-[45vh] w-full object-contain"
              />
            ) : media.content_type.startsWith("video/") && media.status === "ready" && previewUrl ? (
              <video
                src={previewUrl}
                controls
                className="max-h-[45vh] w-full bg-black"
              />
            ) : (
              <div className="flex h-48 items-center justify-center">
                <MediaThumb media={media} className="h-24 w-24 rounded-md" />
              </div>
            )}
          </div>

          <div className="space-y-3 text-xs">
            <div className="flex items-center gap-1.5">
              <MediaStatusBadge status={media.status} />
              <Badge variant="muted">{mimeLabel(media.content_type)}</Badge>
              <Badge variant="muted">{media.visibility === "private" ? "Закрытый" : "Публичный"}</Badge>
            </div>
            <dl className="space-y-1.5">
              <div className="flex justify-between gap-2">
                <dt className="text-muted-foreground">Размер</dt>
                <dd>{formatBytes(media.size_bytes)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted-foreground">Загружен</dt>
                <dd>{formatDateTime(media.created_at)}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted-foreground">Обновлён</dt>
                <dd>{formatDateTime(media.updated_at)}</dd>
              </div>
            </dl>
            <div className="flex flex-wrap gap-1">
              {["thumbnail", "medium", "large"].map((variant) => (
                <Badge
                  key={variant}
                  variant={media.variants[variant] ? "success" : "muted"}
                >
                  {variant}
                </Badge>
              ))}
            </div>
            {media.status === "failed" && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => retryMutation.mutate()}
                loading={retryMutation.isPending}
              >
                <RefreshCw />
                Повторить обработку
              </Button>
            )}
          </div>
        </div>

        <div className="rounded-lg border border-border p-3">
          <p className="text-xs font-semibold">Использование</p>
          <div className="mt-2">
            <UsageList mediaId={media.id} />
          </div>
        </div>

        <div className="space-y-2">
          <label htmlFor="media-filename" className="text-xs font-medium">
            Отображаемое имя
          </label>
          <div className="flex gap-2">
            <Input
              id="media-filename"
              value={filename}
              onChange={(event) => setFilename(event.target.value)}
              aria-label="Отображаемое имя файла"
            />
            <Button
              variant="outline"
              onClick={() => renameMutation.mutate(filename.trim())}
              disabled={!filename.trim() || !renamed}
              loading={renameMutation.isPending}
            >
              <Pencil />
              Сохранить
            </Button>
          </div>
          <p className="text-2xs text-muted-foreground">
            Меняется только отображаемое имя. Ссылка на файл и опубликованный
            контент продолжают работать.
          </p>
        </div>

        <DialogFooter className="flex-wrap items-center justify-between gap-2">
          {canDelete && (
            <Button
              variant="ghost"
              className="text-destructive hover:text-destructive"
              onClick={() => void requestDelete()}
              disabled={deleteMutation.isPending}
            >
              <Trash2 />
              Удалить
            </Button>
          )}
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              onClick={() => void copyMediaLink(media)}
            >
              <Copy />
              {media.visibility === "private" ? "Копировать временную ссылку" : "Копировать ссылку"}
            </Button>
            <Button variant="outline" onClick={() => void downloadMedia(media)}>
              <Download />
              Скачать
            </Button>
            <Button onClick={onClose}>Закрыть</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function MediaActions({
  media,
  onOpen,
}: {
  media: Media;
  onOpen: () => void;
}) {
  const { principal } = useAuth();
  const canDelete = canDeleteGalleryFile(principal?.role);
  const queryClient = useQueryClient();
  const confirm = useConfirm();

  const deleteMutation = useMutation({
    mutationFn: (revision: string) => api.media.remove(media.id, true, revision),
    onSuccess: () => {
      toast.success("Файл удалён");
      void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const requestDelete = async () => {
    const result = await confirmFileDeletion(media, confirm);
    if (result) deleteMutation.mutate(result.revision);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Действия с ${media.original_filename}`}
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuItem onSelect={onOpen}>
          <Pencil className="h-4 w-4" />
          Подробности
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() => void copyMediaLink(media)}
        >
          <Copy className="h-4 w-4" />
          {media.visibility === "private" ? "Копировать временную ссылку" : "Копировать ссылку"}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void downloadMedia(media)}>
          <Download className="h-4 w-4" />
          Скачать
        </DropdownMenuItem>
        {canDelete && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onSelect={() => void requestDelete()}
              disabled={deleteMutation.isPending}
            >
              <Trash2 className="h-4 w-4" />
              Удалить…
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
