import { describe, expect, it } from "vitest";
import {
  DEFAULT_MEDIA_GALLERY_FILTERS,
  hasActiveMediaGalleryFilters,
  mediaGalleryRangeError,
  mediaGalleryResultsState,
  megabytesToBytes,
  mergeMediaGalleryParams,
  normalizeMediaGalleryParams,
  parseMediaGalleryFilters,
} from "./gallery-filters";

describe("media gallery filters", () => {
  it("parses supported filters and safely ignores unknown query values", () => {
    const parsed = parseMediaGalleryFilters(new URLSearchParams(
      "q=%20logo%20&type=image&status=ready&sort=size_desc&from=2026-02-28&to=2026-03-01&min=0.5&max=2&view=list",
    ));

    expect(parsed).toEqual({
      q: "logo",
      type: "image",
      status: "ready",
      sort: "size_desc",
      from: "2026-02-28",
      to: "2026-03-01",
      min: "0.5",
      max: "2",
      view: "list",
    });

    expect(parseMediaGalleryFilters(new URLSearchParams(
      "type=archive&status=deleted&sort=script&view=compact&from=2026-02-30&min=-1&max=Infinity",
    ))).toEqual(DEFAULT_MEDIA_GALLERY_FILTERS);
  });

  it("normalizes gallery parameters without dropping unrelated query state", () => {
    const normalized = normalizeMediaGalleryParams(new URLSearchParams(
      "tab=uploads&type=unknown&sort=new&view=grid&q=%20%20&from=2026-02-30",
    ));

    expect(normalized.toString()).toBe("tab=uploads");
  });

  it("merges filters without discarding other screen parameters and omits defaults", () => {
    const updated = mergeMediaGalleryParams(
      new URLSearchParams("tab=uploads&type=image&view=list"),
      { type: "all", status: "ready", view: "grid", q: "brand mark" },
    );

    expect(updated.toString()).toBe("tab=uploads&status=ready&q=brand+mark");
  });

  it("converts decimal MB safely and detects reversed date or size ranges", () => {
    expect(megabytesToBytes("1,5")).toBe(1_572_864);
    expect(megabytesToBytes("-1")).toBeUndefined();
    expect(megabytesToBytes("1e40")).toBeUndefined();
    expect(mediaGalleryRangeError({ from: "2026-02-03", to: "2026-02-02", min: "", max: "" }))
      .toContain("Дата начала");
    expect(mediaGalleryRangeError({ from: "", to: "", min: "4", max: "3" }))
      .toContain("Минимальный размер");
    expect(mediaGalleryRangeError({ from: "2026-02-02", to: "2026-02-03", min: "3", max: "4" }))
      .toBeNull();
  });

  it("does not count display mode as a filter", () => {
    expect(hasActiveMediaGalleryFilters(DEFAULT_MEDIA_GALLERY_FILTERS)).toBe(false);
    expect(hasActiveMediaGalleryFilters({ ...DEFAULT_MEDIA_GALLERY_FILTERS, view: "list" })).toBe(false);
    expect(hasActiveMediaGalleryFilters({ ...DEFAULT_MEDIA_GALLERY_FILTERS, sort: "old" })).toBe(true);
  });

  it("keeps the gallery result state specific to loading, errors, and empty results", () => {
    expect(mediaGalleryResultsState({ invalidRange: true, loading: true, error: false, itemCount: 0, filtersActive: true }))
      .toBe("invalid-range");
    expect(mediaGalleryResultsState({ invalidRange: false, loading: true, error: false, itemCount: 0, filtersActive: false }))
      .toBe("loading");
    expect(mediaGalleryResultsState({ invalidRange: false, loading: false, error: true, itemCount: 0, filtersActive: false }))
      .toBe("error");
    expect(mediaGalleryResultsState({ invalidRange: false, loading: false, error: false, itemCount: 0, filtersActive: true }))
      .toBe("empty-filtered");
    expect(mediaGalleryResultsState({ invalidRange: false, loading: false, error: false, itemCount: 0, filtersActive: false }))
      .toBe("empty");
    expect(mediaGalleryResultsState({ invalidRange: false, loading: false, error: false, itemCount: 1, filtersActive: false }))
      .toBe("results");
    expect(mediaGalleryResultsState({ invalidRange: false, loading: false, error: true, itemCount: 1, filtersActive: false }))
      .toBe("results-with-error");
  });
});
