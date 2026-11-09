import { describe, expect, it } from "vitest";
import { hasCompetingSiteDraftChange, parseSiteDraft, siteDraftKey } from "./workspace-draft";

describe("local site drafts", () => {
  it("isolates drafts by user and snapshot", () => {
    expect(siteDraftKey("a", "one")).not.toBe(siteDraftKey("a", "two"));
    expect(siteDraftKey("a", "one")).not.toBe(siteDraftKey("b", "one"));
  });
  it("rejects malformed or incompatible storage while preserving valid drafts", () => {
    const config = { formatVersion: 3, pages: [], routeTree: { id: "root", type: "site" } };
    expect(parseSiteDraft("{" )).toBeNull();
    expect(parseSiteDraft(JSON.stringify({ version: 2, baseline: config, config }))).toBeNull();
    expect(parseSiteDraft(JSON.stringify({ version: 1, baseline: config, config }))?.config).toEqual(config);
  });

  it("detects another tab changing or clearing this snapshot draft only", () => {
    const key = siteDraftKey("editor", "snapshot-a");
    const expected = "saved locally";
    expect(hasCompetingSiteDraftChange(key, key, expected, "changed in another tab")).toBe(true);
    expect(hasCompetingSiteDraftChange(key, key, expected, null)).toBe(true);
    expect(hasCompetingSiteDraftChange(siteDraftKey("editor", "snapshot-b"), key, expected, "other snapshot")).toBe(false);
    expect(hasCompetingSiteDraftChange(null, key, null, null)).toBe(false);
    expect(hasCompetingSiteDraftChange(key, key, expected, expected)).toBe(false);
  });
});
