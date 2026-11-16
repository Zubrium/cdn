import { expect, it } from "vitest";
import { baseTokenIsReferenced, designNameErrors, effectiveBaseValue, effectiveFeatureValue } from "./types";
import type { BaseToken, SnapshotFeatureToken, StructuredDesignSystem } from "./types";

it("displays the same largest matching inherited breakpoint as runtime CSS", () => {
  const base: BaseToken = { id: "base", name: "Gap", groupId: "g", kind: "length", value: { number: 4, unit: "px" } };
  const feature: SnapshotFeatureToken = { featureKey: "hero", featureName: "Hero", definition: { key: "gap", label: "Gap", group: "Layout", kind: "length", default: { number: 2, unit: "px" } } };
  const theme: StructuredDesignSystem = { formatVersion: 2, groups: [], baseTokens: [base], fonts: [], featureValues: {}, breakpoints: [
    { id: "wide", name: "Wide", minWidthPx: 1200, baseValues: {}, featureValues: {} },
    { id: "medium", name: "Medium", minWidthPx: 700, baseValues: { base: { number: 12, unit: "px" } }, featureValues: { "hero/gap": { baseTokenId: "base" } } },
  ] };
  expect(effectiveBaseValue(theme, base, "wide")).toEqual({ number: 12, unit: "px" });
  expect(effectiveFeatureValue(theme, feature, "wide")).toEqual({ baseTokenId: "base" });
  expect(effectiveBaseValue(theme, base, "")).toEqual(base.value);
  expect(effectiveFeatureValue(theme, feature, "")).toEqual(feature.definition.default);
  expect(baseTokenIsReferenced(theme, "base")).toBe(true);
  expect(baseTokenIsReferenced(theme, "other")).toBe(false);
});

it("detects empty and duplicate names while allowing the same token name in different groups", () => {
  const theme: StructuredDesignSystem = {
    formatVersion: 2,
    groups: [{ id: "a", name: "Цвета" }, { id: "b", name: " Размеры " }],
    baseTokens: [
      { id: "t1", groupId: "a", name: "Основной", kind: "number", value: { number: 1 } },
      { id: "t2", groupId: "b", name: "Основной", kind: "number", value: { number: 2 } },
    ],
    fonts: [], featureValues: {}, breakpoints: [],
  };
  expect(designNameErrors(theme)).toEqual([]);
  theme.groups.push({ id: "c", name: " цвета " });
  theme.baseTokens.push({ id: "t3", groupId: "a", name: " основной ", kind: "number", value: { number: 3 } });
  theme.baseTokens.push({ id: "t4", groupId: "a", name: " ", kind: "number", value: { number: 4 } });
  expect(designNameErrors(theme)).toHaveLength(3);
});
