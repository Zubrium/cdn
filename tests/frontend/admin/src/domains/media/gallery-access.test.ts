import { describe, expect, it } from "vitest";
import type { Principal } from "@/types";
import { canDeleteGalleryFile } from "../../../../../../frontend/admin/src/domains/media/gallery-access";

const principal = (group_access: Record<string, 0 | 1 | 2>): Principal => ({
  id: "principal-1",
  name: "Editor",
  active: true,
  can_manage_administrators: false,
  group_access,
});

describe("media gallery access", () => {
  it("allows deletion only when one of the file's groups grants level 2", () => {
    expect(canDeleteGalleryFile(principal({ media: 2 }), ["media"])).toBe(true);
    expect(canDeleteGalleryFile(principal({ media: 1, content: 2 }), ["media"])).toBe(false);
    expect(canDeleteGalleryFile(principal({ content: 2 }), ["media", "content"])).toBe(true);
    expect(canDeleteGalleryFile(principal({ media: 2 }), [])).toBe(false);
    expect(canDeleteGalleryFile(null, ["media"])).toBe(false);
  });
});
