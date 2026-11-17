import { FileText, Film, ImageOff, File } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import type { Media } from "@/types";
import { api, publicMediaPath } from "@/api";
import { cn } from "@/lib/utils";

export function mediaPreviewUrl(media: Media, variant?: string): string {
  const chosen = variant && media.variants[variant] ? variant : undefined;
  return publicMediaPath(media.id, chosen);
}

export function isImageMedia(media: Media): boolean {
  return media.content_type.startsWith("image/");
}

export function isVideoMedia(media: Media): boolean {
  return media.content_type.startsWith("video/");
}

/** Private previews receive short-lived signed URLs, never public S3 paths. */
export function useMediaPreviewUrl(media: Media | null, variant?: string): string | null {
  const isPrivate = media?.visibility === "private";
  const limits = useQuery({ queryKey: ["file-limits"], queryFn: ({ signal }) => api.media.limits(signal), enabled: isPrivate });
  const ttlSeconds = limits.data?.linkMaxTTLSeconds ?? 0;
  const link = useQuery({
    queryKey: ["private-media-preview", media?.id, ttlSeconds],
    queryFn: () => api.media.createLink(media!.id, ttlSeconds),
    enabled: Boolean(isPrivate && media && ttlSeconds > 0),
    staleTime: ttlSeconds * 800,
    refetchInterval: ttlSeconds > 0 ? ttlSeconds * 800 : false,
  });
  if (!media) return null;
  if (!isPrivate) return mediaPreviewUrl(media, variant);
  if (!link.data) return null;
  const url = new URL(link.data.url, window.location.origin);
  if (variant && media.variants[variant]) url.searchParams.set("variant", variant);
  return url.toString();
}

export function MediaThumb({
  media,
  variant,
  className,
  imageClassName,
}: {
  media: Media;
  variant?: string;
  className?: string;
  imageClassName?: string;
}) {
  const url = useMediaPreviewUrl(isImageMedia(media) ? media : null, variant);
  if (media.status === "ready" && isImageMedia(media) && url) {
    return (
      <img
        src={url}
        alt={media.original_filename}
        loading="lazy"
        className={cn("h-full w-full object-cover", imageClassName)}
        onError={(event) => {
          event.currentTarget.style.display = "none";
        }}
      />
    );
  }
  const Icon = isVideoMedia(media)
    ? Film
    : media.content_type === "application/pdf"
      ? FileText
      : media.status === "failed"
        ? ImageOff
        : File;
  return (
    <div
      className={cn(
        "flex h-full w-full items-center justify-center bg-muted/40 text-muted-foreground",
        className,
      )}
      aria-label={media.original_filename}
    >
      <Icon className="h-7 w-7" />
    </div>
  );
}
