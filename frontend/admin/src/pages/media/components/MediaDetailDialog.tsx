import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, Download, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, absolutePublicMediaUrl } from "@/api";
import { useAuth } from "@/app/auth";
import type { Media } from "@/types";
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
import { formatBytes } from "@/lib/format";
import { isImageMedia, MediaThumb, useMediaPreviewUrl } from "@/domains/media/media-preview";
import { useDeleteMedia } from "@/domains/media/use-delete-media";

export function mimeLabel(contentType: string): string {
  if (contentType.startsWith("image/")) return "Изображение";
  if (contentType.startsWith("video/")) return "Видео";
  if (contentType === "application/pdf") return "PDF";
  return "Файл";
}

function copyText(value: string, message: string) {
  if (!navigator.clipboard?.writeText) {
    toast.error("Буфер обмена недоступен");
    return;
  }
  void navigator.clipboard.writeText(value).then(() => toast.success(message), () => toast.error("Не удалось скопировать"));
}

function fileContentURL(media: Media): string {
  return absolutePublicMediaUrl(media.id);
}

function copyFileAddress(media: Media) {
  copyText(fileContentURL(media), "Адрес для авторизованного доступа скопирован");
}

function downloadFile(media: Media) {
  const link = document.createElement("a");
  link.href = fileContentURL(media);
  link.download = media.original_filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
}

async function confirmFileDeletion(media: Media, confirm: ReturnType<typeof useConfirm>): Promise<boolean> {
  return confirm({
    title: "Удалить файл?",
    description: `Файл «${media.original_filename}» и его содержимое будут удалены без возможности восстановления. Ссылки на него в движках после этого перестанут работать.`,
    confirmLabel: "Удалить файл",
    destructive: true,
  });
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
  const canDelete = canDeleteGalleryFile(principal, media?.groups);
  const queryClient = useQueryClient();
  const confirm = useConfirm();
  const [filename, setFilename] = useState(media?.original_filename ?? "");
  const previewUrl = useMediaPreviewUrl(media && (isImageMedia(media) || media.content_type.startsWith("video/")) ? media : null);

  const renameMutation = useMutation({
    mutationFn: (name: string) => api.media.rename(media!.id, name, media!.revision),
    onSuccess: (updated) => {
      toast.success("Имя файла обновлено");
      setFilename(updated.original_filename);
      void queryClient.invalidateQueries({ queryKey: queryKeys.media.all });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const deleteMutation = useDeleteMedia(onClose);

  const requestDelete = async () => {
    if (media && await confirmFileDeletion(media, confirm)) deleteMutation.mutate({ id: media.id, revision: media.revision });
  };

  if (!media) return null;
  const renamed = filename.trim() !== media.original_filename;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="truncate">{media.original_filename}</DialogTitle>
          <DialogDescription>{mimeLabel(media.content_type)} · {media.content_type} · {formatBytes(media.size_bytes)}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_16rem]">
          <div className="overflow-hidden rounded-lg border border-border bg-muted/30">
            {isImageMedia(media) && previewUrl ? (
              <img src={previewUrl} alt={media.original_filename} className="max-h-[45vh] w-full object-contain" />
            ) : media.content_type.startsWith("video/") && previewUrl ? (
              <video src={previewUrl} controls className="max-h-[45vh] w-full bg-black" />
            ) : (
              <div className="flex h-48 items-center justify-center"><MediaThumb media={media} className="h-24 w-24 rounded-md" /></div>
            )}
          </div>

          <div className="space-y-3 text-xs">
            <div className="flex flex-wrap items-center gap-1.5">
              <MediaStatusBadge status={media.status} />
              <Badge variant="muted">{mimeLabel(media.content_type)}</Badge>
            </div>
            <dl className="space-y-1.5">
              <div className="flex justify-between gap-2"><dt className="text-muted-foreground">Размер</dt><dd>{formatBytes(media.size_bytes)}</dd></div>
              <div className="flex justify-between gap-2"><dt className="text-muted-foreground">Revision</dt><dd>{media.revision}</dd></div>
            </dl>
            <div>
              <p className="font-medium">Группы доступа</p>
              <div className="mt-1 flex flex-wrap gap-1">
                {(media.groups ?? []).map((group) => <Badge key={group} variant="secondary">{group}</Badge>)}
              </div>
            </div>
          </div>
        </div>

        <div className="space-y-2">
          <label htmlFor="media-filename" className="text-xs font-medium">Имя файла</label>
          <div className="flex gap-2">
            <Input id="media-filename" value={filename} onChange={(event) => setFilename(event.target.value)} aria-label="Имя файла" />
            <Button variant="outline" onClick={() => renameMutation.mutate(filename.trim())} disabled={!filename.trim() || !renamed} loading={renameMutation.isPending}>
              <Pencil />Сохранить
            </Button>
          </div>
          <p className="text-2xs text-muted-foreground">Меняется только имя; байты файла остаются неизменными.</p>
        </div>

        <p className="text-xs text-muted-foreground">Этот адрес не является публичной ссылкой: для доступа требуется авторизация. Чтобы выдавать файл публично, настройте отдельный маршрут Router для этого файла.</p>
        <DialogFooter className="flex-wrap items-center justify-between gap-2">
          {canDelete && <Button variant="ghost" className="text-destructive hover:text-destructive" onClick={() => void requestDelete()} disabled={deleteMutation.isPending}><Trash2 />Удалить</Button>}
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => copyFileAddress(media)}><Copy />Скопировать адрес для авторизованного доступа</Button>
            <Button variant="outline" onClick={() => downloadFile(media)}><Download />Скачать</Button>
            <Button onClick={onClose}>Закрыть</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function MediaActions({ media, onOpen }: { media: Media; onOpen: () => void }) {
  const { principal } = useAuth();
  const canDelete = canDeleteGalleryFile(principal, media.groups);
  const confirm = useConfirm();
  const deleteMutation = useDeleteMedia();
  const requestDelete = async () => {
    if (await confirmFileDeletion(media, confirm)) deleteMutation.mutate({ id: media.id, revision: media.revision });
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild><Button variant="ghost" size="icon-sm" aria-label={`Действия с ${media.original_filename}`}><MoreHorizontal /></Button></DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuItem onSelect={onOpen}><Pencil className="h-4 w-4" />Подробности</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => copyFileAddress(media)}><Copy className="h-4 w-4" />Скопировать адрес для авторизованного доступа</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => downloadFile(media)}><Download className="h-4 w-4" />Скачать</DropdownMenuItem>
        {canDelete && <><DropdownMenuSeparator /><DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={() => void requestDelete()} disabled={deleteMutation.isPending}><Trash2 className="h-4 w-4" />Удалить…</DropdownMenuItem></>}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
