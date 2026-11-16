import { describe, expect, it } from "vitest";
import { canDeleteGalleryFile } from "./gallery-access";

describe("media gallery access", () => {
  it("shows destructive file actions only to administrators", () => {
    expect(canDeleteGalleryFile("admin")).toBe(true);
    expect(canDeleteGalleryFile("moderator")).toBe(false);
    expect(canDeleteGalleryFile("staff")).toBe(false);
    expect(canDeleteGalleryFile("user")).toBe(false);
    expect(canDeleteGalleryFile(null)).toBe(false);
  });
});
