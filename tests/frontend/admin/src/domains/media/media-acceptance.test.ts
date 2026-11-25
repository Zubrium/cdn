import { describe, expect, it } from "vitest";
import type { Media } from "@/types";
import { acceptsMedia } from "../../../../../../frontend/admin/src/domains/media/media-acceptance";

const media = (name: string, contentType: string): Media => ({
  id: "file-1",
  original_filename: name,
  content_type: contentType,
  size_bytes: 12,
  status: "ready",
  variants: {},
  revision: 1,
  created_at: "2026-11-18T10:00:00Z",
  updated_at: "2026-11-18T10:00:00Z",
});

describe("media picker filters", () => {
  it("recognizes ZIP archives by extension or archive MIME type", () => {
    expect(acceptsMedia(media("feature.ZIP", "application/octet-stream"), "zip")).toBe(true);
    expect(acceptsMedia(media("feature", "application/zip"), "zip")).toBe(true);
    expect(acceptsMedia(media("feature.tar.gz", "application/gzip"), "zip")).toBe(false);
  });

  it("preserves the existing image, raster, video, font and all filters", () => {
    expect(acceptsMedia(media("photo.jpg", "image/jpeg"), "image")).toBe(true);
    expect(acceptsMedia(media("photo.svg", "image/svg+xml"), "raster-image")).toBe(false);
    expect(acceptsMedia(media("movie.mp4", "video/mp4"), "video")).toBe(true);
    expect(acceptsMedia(media("font.woff2", "font/woff2"), "font")).toBe(true);
    expect(acceptsMedia(media("data.bin", "application/octet-stream"), "all")).toBe(true);
    expect(acceptsMedia(media("photo.jpg", "image/jpeg"), "none")).toBe(false);
  });
});
