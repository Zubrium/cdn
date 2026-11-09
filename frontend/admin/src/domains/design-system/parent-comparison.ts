import { effectiveBaseValue, effectiveFeatureValue } from "./types";
import type { BaseToken, SnapshotDesignWorkspace, SnapshotFeatureToken, StructuredDesignSystem, TokenBreakpoint, TokenColor, TokenKind, TokenValue } from "./types";

export type ParentTokenDifference =
  | { kind: "new" }
  | { kind: "changed"; previous: string; previousSource?: string };

type ResolvedToken = { kind: TokenKind; label: string; source?: { key: string; label: string } };

const systemFonts = new Map([
  ["system-ui", "Системный"],
  ["sans-serif", "Без засечек"],
  ["serif", "С засечками"],
  ["monospace", "Моноширинный"],
]);

function colorLabel(color: TokenColor): string {
  return `${color.hex.toLowerCase()} · непрозрачность ${color.opacity}%`;
}

function valueLabel(kind: TokenKind, value: TokenValue): string {
  if (kind === "color" && value.color) return colorLabel(value.color);
  if (kind === "length" && value.number !== undefined) return `${value.number} ${value.unit}`;
  if (kind === "number" && value.number !== undefined) return String(value.number);
  if (kind === "font" && value.font) return systemFonts.get(value.font) ?? value.font;
  if (kind === "shadow" && value.shadow) {
    const { x, y, blur, spread, color, inset } = value.shadow;
    return `${inset ? "Внутренняя" : "Внешняя"} · смещение ${x} × ${y} px · размытие ${blur} px · расширение ${spread} px · ${colorLabel(color)}`;
  }
  return "Значение недоступно";
}

function difference(current: ResolvedToken, previous?: ResolvedToken): ParentTokenDifference | undefined {
  if (!previous) return { kind: "new" };
  if (current.kind === previous.kind && current.label === previous.label && current.source?.key === previous.source?.key) return undefined;
  return { kind: "changed", previous: previous.label, previousSource: previous.source?.label };
}

function featureKey(token: SnapshotFeatureToken): string {
  return `${token.featureKey}/${token.definition.key}`;
}

function resolvedFeature(theme: StructuredDesignSystem, token: SnapshotFeatureToken, breakpoint?: TokenBreakpoint): ResolvedToken {
  const kind = token.definition.kind;
  const breakpointId = breakpoint?.id ?? "";
  const value = effectiveFeatureValue(theme, token, breakpointId);
  if (value.baseTokenId) {
    const base = theme.baseTokens.find((item) => item.id === value.baseTokenId);
    return {
      kind,
      label: base ? valueLabel(kind, effectiveBaseValue(theme, base, breakpointId)) : "Значение недоступно",
      source: { key: value.baseTokenId, label: base ? `Связан с «${base.name}»` : "Связан с базовым" },
    };
  }
  const key = featureKey(token);
  const hasOverride = theme.featureValues[key] || breakpoint && theme.breakpoints.some(
    (item) => item.minWidthPx <= breakpoint.minWidthPx && item.featureValues[key],
  );
  return {
    kind,
    label: valueLabel(kind, value),
    source: hasOverride ? { key: "literal", label: "Своё значение" } : { key: "default", label: "Из фичи" },
  };
}

export function compareWithParent(
  theme: StructuredDesignSystem,
  breakpoint: TokenBreakpoint | undefined,
  parent: Pick<SnapshotDesignWorkspace, "designSystem" | "featureTokens">,
) {
  const previousTheme = parent.designSystem;
  // The parent's thresholds may have different IDs or widths.
  const previousBreakpoint = breakpoint && previousTheme.breakpoints.reduce<TokenBreakpoint | undefined>(
    (closest, item) => item.minWidthPx <= breakpoint.minWidthPx && (!closest || item.minWidthPx > closest.minWidthPx) ? item : closest,
    undefined,
  );

  return {
    base(token: BaseToken): ParentTokenDifference | undefined {
      const previous = previousTheme.baseTokens.find((item) => item.id === token.id);
      if (!previous) return { kind: "new" };
      return difference(
        { kind: token.kind, label: valueLabel(token.kind, effectiveBaseValue(theme, token, breakpoint?.id ?? "")) },
        { kind: previous.kind, label: valueLabel(previous.kind, effectiveBaseValue(previousTheme, previous, previousBreakpoint?.id ?? "")) },
      );
    },
    feature(token: SnapshotFeatureToken): ParentTokenDifference | undefined {
      const previous = parent.featureTokens.find((item) => featureKey(item) === featureKey(token));
      return difference(
        resolvedFeature(theme, token, breakpoint),
        previous && resolvedFeature(previousTheme, previous, previousBreakpoint),
      );
    },
  };
}
