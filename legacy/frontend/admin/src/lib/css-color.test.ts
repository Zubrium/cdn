import { describe, expect, it } from "vitest";
import { cssColorValue, parseCssColor } from "./css-color";

describe("CSS color controls", () => {
  it("normalizes common hex forms and preserves alpha", () => {
    expect(parseCssColor("#abc")).toEqual({ hex: "#aabbcc", alpha: 100 });
    expect(parseCssColor("#12345680")).toEqual({ hex: "#123456", alpha: 50 });
  });

  it("converts modern HSL syntax to the color input format", () => {
    expect(parseCssColor("hsl(220 70% 42%)")).toEqual({ hex: "#2052b6", alpha: 100 });
    expect(parseCssColor("hsla(0.5turn, 100%, 50%, 0.4)")).toEqual({ hex: "#00ffff", alpha: 40 });
  });

  it("reads RGB channels and percentage or decimal alpha", () => {
    expect(parseCssColor("rgb(255 250 240 / 78%)")).toEqual({ hex: "#fffaf0", alpha: 78 });
    expect(parseCssColor("rgba(10, 20, 30, 0.25)")).toEqual({ hex: "#0a141e", alpha: 25 });
  });

  it("serializes the selected color with preserved transparency", () => {
    expect(cssColorValue("#347b9f", 42)).toBe("rgb(52 123 159 / 42%)");
    expect(cssColorValue("#347b9f", 100)).toBe("#347b9f");
  });

  it("returns null for values that are not color picker inputs", () => {
    expect(parseCssColor("var(--brand-color)")).toBeNull();
    expect(parseCssColor("#12zz00")).toBeNull();
  });
});
