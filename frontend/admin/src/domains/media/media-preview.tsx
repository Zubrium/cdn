import { FileText, Film, ImageOff, File } from "lucide-react";
import type { Media } from "@/types";
import { publicMediaPath } from "@/api";
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

/** Admin previews use the authenticated Core file endpoint on the same origin. */
export function useMediaPreviewUrl(media: Media | null, variant?: string): string | null {
  if (!media) return null;
  return mediaPreviewUrl(media, variant);
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
