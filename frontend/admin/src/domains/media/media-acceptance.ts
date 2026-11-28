import type { Media } from "@/types";

export type MediaAccept = "image" | "raster-image" | "video" | "font" | "zip" | "all" | "none";

export function manifestMediaAccept(accept: unknown): "image" | "raster-image" | "video" | "all" {
  if (accept === "file") return "all";
  if (accept === "video" || accept === "raster-image") return accept;
  return "image";
}

export function acceptsMedia(media: Media, accept: MediaAccept): boolean {
  if (accept === "none") return false;
  if (accept === "image") return media.content_type.startsWith("image/");
  if (accept === "raster-image") return ["image/jpeg", "image/png", "image/webp"].includes(media.content_type);
  if (accept === "video") return media.content_type.startsWith("video/");
  if (accept === "font") return media.content_type === "font/woff2";
  if (accept === "zip") {
    return media.original_filename.toLowerCase().endsWith(".zip") ||
      ["application/zip", "application/x-zip-compressed"].includes(media.content_type.toLowerCase());
  }
  return true;
}
