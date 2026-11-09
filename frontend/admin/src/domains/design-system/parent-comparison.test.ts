import { expect, it } from "vitest";
import { compareWithParent } from "./parent-comparison";
import type { BaseToken, SnapshotFeatureToken, StructuredDesignSystem, TokenBreakpoint } from "./types";

const base: BaseToken = { id: "base", name: "Отступ", groupId: "layout", kind: "length", value: { number: 4, unit: "px" } };
const feature: SnapshotFeatureToken = { featureKey: "header", featureName: "Шапка", definition: { key: "gap", label: "Отступ", group: "Размеры", kind: "length", default: { number: 2, unit: "px" } } };
const featureKey = "header/gap";
const breakpoint = (id: string, minWidthPx: number, baseValue?: number): TokenBreakpoint => ({
  id, name: id, minWidthPx, baseValues: baseValue === undefined ? {} : { base: { number: baseValue, unit: "px" } }, featureValues: {},
});
const theme = (overrides: Partial<StructuredDesignSystem> = {}): StructuredDesignSystem => ({
  formatVersion: 2, groups: [], baseTokens: [base], fonts: [], featureValues: {}, breakpoints: [], ...overrides,
});

it("uses each snapshot's cumulative thresholds at the selected width and treats base values separately from 0 px", () => {
  const narrow = breakpoint("child-0", 0, 8);
  const wide = breakpoint("child-900", 900, 16);
  const child = theme({ breakpoints: [wide, narrow] });
  const parent = {
    designSystem: theme({ breakpoints: [breakpoint("parent-1200", 1200, 20), breakpoint("parent-500", 500, 12), breakpoint("parent-700", 700), breakpoint("parent-0", 0, 10)] }),
    featureTokens: [feature],
  };
  expect(compareWithParent(child, undefined, parent).base(base)).toBeUndefined();
  expect(compareWithParent(child, narrow, parent).base(base)).toMatchObject({ kind: "changed", previous: "10 px" });
  expect(compareWithParent(child, wide, parent).base(base)).toMatchObject({ kind: "changed", previous: "12 px" });
});

it("detects a changed inherited base value even when the feature link remains the same", () => {
  const wide = breakpoint("child-900", 900);
  const previousNarrow = { ...breakpoint("parent-500", 500, 12), featureValues: { [featureKey]: { baseTokenId: base.id } } };
  const currentNarrow = { ...breakpoint("child-600", 600, 16), featureValues: previousNarrow.featureValues };
  const child = theme({ breakpoints: [wide, currentNarrow] });
  const parent = { designSystem: theme({ breakpoints: [previousNarrow] }), featureTokens: [feature] };
  expect(compareWithParent(child, undefined, parent).feature(feature)).toBeUndefined();
  expect(compareWithParent(child, wide, parent).feature(feature)).toEqual({
    kind: "changed", previous: "12 px", previousSource: "Связан с «Отступ»",
  });
});

it("compares pinned defaults and changes of source even when the effective values agree", () => {
  const parent = { designSystem: theme(), featureTokens: [feature] };
  const updated = { ...feature, definition: { ...feature.definition, default: { number: 6, unit: "px" } } };
  expect(compareWithParent(theme(), undefined, parent).feature(updated)).toEqual({
    kind: "changed", previous: "2 px", previousSource: "Из фичи",
  });
  const override = theme({ featureValues: { [featureKey]: feature.definition.default } });
  expect(compareWithParent(override, undefined, parent).feature(feature)).toMatchObject({ kind: "changed", previousSource: "Из фичи" });
  const secondBase = { ...base, id: "second", name: "Второй отступ" };
  const child = theme({ baseTokens: [base, secondBase], featureValues: { [featureKey]: { baseTokenId: secondBase.id } } });
  const linkedParent = { ...parent, designSystem: theme({ featureValues: { [featureKey]: { baseTokenId: base.id } } }) };
  expect(compareWithParent(child, undefined, linkedParent).feature(feature)).toEqual({
    kind: "changed", previous: "4 px", previousSource: "Связан с «Отступ»",
  });
});

it("identifies new tokens by stable identity and ignores equivalent color spelling", () => {
  const color: BaseToken = { ...base, kind: "color", value: { color: { hex: "#AbCdEf", opacity: 85 } } };
  const sameColor: BaseToken = { ...color, name: "Новое название", value: { color: { hex: "#abcdef", opacity: 85 } } };
  const parent = { designSystem: theme({ baseTokens: [color] }), featureTokens: [feature] };
  const compare = compareWithParent(theme({ baseTokens: [sameColor] }), undefined, parent);
  expect(compare.base(sameColor)).toBeUndefined();
  expect(compare.base({ ...sameColor, id: "new" })).toEqual({ kind: "new" });
  expect(compare.feature({ ...feature, featureKey: "footer" })).toEqual({ kind: "new" });
});
