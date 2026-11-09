import { describe, expect, it, vi } from "vitest";
import { focusDataModelControl } from "./components/data-model/model-operations";

describe("data model keyboard focus recovery", () => {
  it("restores focus and scrolls the target into its nearest visible area", () => {
    const target = {
      focus: vi.fn(),
      scrollIntoView: vi.fn(),
    };

    expect(focusDataModelControl("data-model-new-relation-from", () => target)).toBe(true);
    expect(target.focus).toHaveBeenCalledWith({ preventScroll: true });
    expect(target.scrollIntoView).toHaveBeenCalledWith({ block: "nearest", inline: "nearest" });
  });

  it("reports a missing focus target without invoking browser behavior", () => {
    expect(focusDataModelControl("missing", () => null)).toBe(false);
  });
});
