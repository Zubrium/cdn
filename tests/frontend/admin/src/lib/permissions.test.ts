import { describe, expect, it } from "vitest";
import type { Principal } from "@/types";
import {
  canManageAccess,
  canManageFeatures,
  canManageFormSchemas,
  canManageMedia,
  canManageSubmissions,
  canPublishContent,
  canReadFormSchemas,
  canViewSubmissions,
  hasAnyGroupAccess,
  hasGroupAccess,
} from "../../../../../frontend/admin/src/lib/permissions";

const principal = (patch: Partial<Principal> = {}): Principal => ({
  id: "principal-1",
  name: "Editor",
  active: true,
  can_manage_administrators: false,
  group_access: {},
  ...patch,
});

describe("direct group permissions", () => {
  it("uses the 0/1/2 group levels without role inheritance", () => {
    const subject = principal({ group_access: { features: 1, content: 2 } });

    expect(hasGroupAccess(subject, "features", 1)).toBe(true);
    expect(hasGroupAccess(subject, "features", 2)).toBe(false);
    expect(hasGroupAccess(subject, "content", 2)).toBe(true);
    expect(hasGroupAccess(subject, "unknown", 1)).toBe(false);
  });

  it("grants each UI capability from its owning group or management flag", () => {
    const subject = principal({
      group_access: { features: 2, content: 1, forms_schemas: 2, forms_submissions: 2, media: 2 },
    });
    expect(canManageFeatures(subject)).toBe(true);
    expect(canPublishContent(subject)).toBe(false);
    expect(canReadFormSchemas(subject)).toBe(true);
    expect(canManageFormSchemas(subject)).toBe(true);
    expect(canViewSubmissions(subject)).toBe(true);
    expect(canManageSubmissions(subject)).toBe(true);
    expect(canManageMedia(subject)).toBe(true);
    expect(canManageAccess(subject)).toBe(false);
    expect(canManageAccess(principal({ can_manage_administrators: true }))).toBe(true);
  });

  it("fails closed for missing principals and empty grants", () => {
    for (const subject of [null, undefined, principal()]) {
      expect(hasAnyGroupAccess(subject)).toBe(false);
      expect(canManageFeatures(subject)).toBe(false);
      expect(canPublishContent(subject)).toBe(false);
      expect(canManageAccess(subject)).toBe(false);
    }
  });
});
