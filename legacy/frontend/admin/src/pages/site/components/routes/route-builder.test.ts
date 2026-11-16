import { describe, expect, it } from "vitest";
import { childNodeFromSegment } from "./route-utils";
import {
  appendRouteSegment,
  buildRouteSegment,
  duplicateRouteCaptures,
  routeCapturePreview,
  type RouteSegmentDraft,
} from "./route-builder";

function draft(patch: Partial<RouteSegmentDraft>): RouteSegmentDraft {
  return {
    kind: "static",
    staticValue: "",
    name: "value",
    preset: "slug",
    customPattern: "",
    ...patch,
  };
}

describe("route builder", () => {
  it("builds static, parameter, regex, and catch-all segments in the existing route syntax", () => {
    expect(buildRouteSegment(draft({ staticValue: "catalog" }))).toEqual({ value: "catalog" });
    expect(buildRouteSegment(draft({ kind: "param", name: "slug" }))).toEqual({ value: ":slug", example: "example" });
    expect(buildRouteSegment(draft({ kind: "catchAll", name: "path" }))).toEqual({ value: "*path", example: "section/page" });

    const integer = buildRouteSegment(draft({ kind: "regex", name: "id", preset: "integer" }));
    expect(integer).toEqual({ value: "~(?<id>[0-9]+)", example: "42" });
    const node = childNodeFromSegment(integer.value!, "route-id");
    expect(node.node).toMatchObject({ type: "regex", pattern: "(?<id>[0-9]+)" });
  });

  it("rejects empty, multi-segment, and empty-matching patterns", () => {
    expect(buildRouteSegment(draft({ staticValue: "" })).error).toContain("Введите текст");
    expect(buildRouteSegment(draft({ kind: "regex", preset: "custom", customPattern: "a/b" })).error).toContain("не может содержать /");
    expect(buildRouteSegment(draft({ kind: "regex", preset: "custom", customPattern: "a*" })).error).toContain("пустым");
  });

  it("appends friendly segments and previews extracted values from raw expert syntax", () => {
    expect(appendRouteSegment("/", "catalog")).toBe("/catalog");
    expect(appendRouteSegment("/catalog", ":slug")).toBe("/catalog/:slug");
    expect(routeCapturePreview("/catalog/:slug/~(?<code>[A-Z]+)/*rest")).toEqual(["slug", "code", "rest"]);
    expect(routeCapturePreview("/about")).toEqual([]);
    expect(duplicateRouteCaptures(["category"], "/:category/:id")).toEqual(["category"]);
  });
});
