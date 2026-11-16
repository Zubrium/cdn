import { describe, expect, it } from "vitest";
import type { Role } from "@/types";
import {
  canCreateFeature,
  canManageAccess,
  canManageFeatures,
  canManageMedia,
  canManageReleases,
  canManageSchema,
  canManageSettings,
  canManageSubmissions,
  canPromoteContent,
  canPublishContent,
  canViewSubmissions,
} from "./permissions";

const roleCases: Array<{ role: Role; site: boolean; submissions: boolean; administrator: boolean }> = [
  { role: "admin", site: true, submissions: true, administrator: true },
  { role: "moderator", site: true, submissions: true, administrator: false },
  { role: "staff", site: false, submissions: false, administrator: false },
  { role: "user", site: false, submissions: false, administrator: false },
];

describe("role permission matrix", () => {
  it("keeps site structure and feature management to admin and moderator", () => {
    for (const testCase of roleCases) {
      expect(canManageSchema(testCase.role), testCase.role).toBe(testCase.site);
      expect(canManageFeatures(testCase.role), testCase.role).toBe(testCase.site);
      expect(canPromoteContent(testCase.role), testCase.role).toBe(testCase.site);
      expect(canCreateFeature(testCase.role), testCase.role).toBe(testCase.site);
      expect(canManageReleases(testCase.role), testCase.role).toBe(testCase.site);
    }
  });

  it("lets every active role edit and publish content, use media, and read submissions", () => {
    for (const { role } of roleCases) {
      expect(canPublishContent(role), role).toBe(true);
      expect(canManageMedia(role), role).toBe(true);
      expect(canViewSubmissions(role), role).toBe(true);
    }
  });

  it("restricts submission changes to admin and moderator and settings to admin", () => {
    for (const testCase of roleCases) {
      expect(canManageSubmissions(testCase.role), testCase.role).toBe(testCase.submissions);
      expect(canManageAccess(testCase.role), testCase.role).toBe(testCase.administrator);
      expect(canManageSettings(testCase.role), testCase.role).toBe(testCase.administrator);
    }
  });

  it("fails closed when a role is absent or unknown", () => {
    const helpers = [
      canManageSchema,
      canManageFeatures,
      canPublishContent,
      canPromoteContent,
      canManageMedia,
      canManageAccess,
      canManageSettings,
      canViewSubmissions,
      canManageSubmissions,
      canManageReleases,
      canCreateFeature,
    ];

    for (const helper of helpers) {
      expect(helper(undefined)).toBe(false);
      expect(helper(null)).toBe(false);
      expect(helper("owner" as Role)).toBe(false);
    }
  });
});
