import type { DesignFont, SiteSnapshot } from "@/types";

export type TokenKind = "color" | "length" | "number" | "font" | "shadow";
export interface TokenColor { hex: string; opacity: number }
export interface TokenShadow { x: number; y: number; blur: number; spread: number; color: TokenColor; inset: boolean }
export interface TokenValue { number?: number; unit?: string; color?: TokenColor; font?: string; shadow?: TokenShadow; baseTokenId?: string }
export interface TokenGroup { id: string; name: string }
export interface BaseToken { id: string; name: string; groupId: string; kind: TokenKind; value: TokenValue }
export interface FeatureTokenDefinition { key: string; label: string; group: string; kind: TokenKind; default: TokenValue }
export interface SnapshotFeatureToken { featureKey: string; featureName: string; definition: FeatureTokenDefinition }
export interface LegacyFeatureTokenValue { featureKey: string; featureName: string; tokenKey: string; label: string; kind: TokenKind; version: string; value: TokenValue }
export interface TokenBreakpoint { id: string; name: string; minWidthPx: number; baseValues: Record<string, TokenValue>; featureValues: Record<string, TokenValue>; legacyFeatureValues?: LegacyFeatureTokenValue[] }
export interface StructuredDesignSystem {
  formatVersion: 2;
  groups: TokenGroup[];
  baseTokens: BaseToken[];
  featureValues: Record<string, TokenValue>;
  legacyFeatureValues?: LegacyFeatureTokenValue[];
  breakpoints: TokenBreakpoint[];
  fonts: DesignFont[];
  brandLogoMediaId?: string;
}
export interface SnapshotDesignWorkspace { snapshot: SiteSnapshot; designSystem: StructuredDesignSystem; featureTokens: SnapshotFeatureToken[] }
export const kindNames: Record<TokenKind, string> = { color: "Цвет", length: "Размер или отступ", number: "Число", font: "Шрифт", shadow: "Тень" };
export function initialTokenValue(kind: TokenKind): TokenValue {
  switch (kind) {
    case "color": return { color: { hex: "#000000", opacity: 100 } };
    case "length": return { number: 0, unit: "px" };
    case "number": return { number: 0 };
    case "font": return { font: "system-ui" };
    case "shadow": return { shadow: { x: 0, y: 0, blur: 0, spread: 0, color: { hex: "#000000", opacity: 20 }, inset: false } };
  }
}
export function baseTokenIsReferenced(theme: StructuredDesignSystem, id: string): boolean {
  return [theme.featureValues, ...theme.breakpoints.map((bp) => bp.featureValues)].some((values) => Object.values(values).some((value) => value.baseTokenId === id));
}

/** Names identify controls to people, so ambiguous or empty names are rejected. */
export function designNameErrors(theme: StructuredDesignSystem): string[] {
  const errors: string[] = [];
  const groupNames = new Set<string>();
  for (const group of theme.groups) {
    const name = group.name.trim().toLowerCase();
    if (!name || [...group.name].length > 160) errors.push("Название группы должно содержать от 1 до 160 символов.");
    else if (groupNames.has(name)) errors.push(`Название группы «${group.name.trim()}» повторяется.`);
    groupNames.add(name);
  }
  const tokenNames = new Set<string>();
  for (const token of theme.baseTokens) {
    const name = token.name.trim().toLowerCase();
    const key = `${token.groupId}\u0000${name}`;
    if (!name || [...token.name].length > 160) errors.push("Название токена должно содержать от 1 до 160 символов.");
    else if (tokenNames.has(key)) errors.push(`Название настройки «${token.name.trim()}» повторяется внутри группы.`);
    tokenNames.add(key);
  }
  return errors;
}


export function effectiveBaseValue(theme: StructuredDesignSystem, token: BaseToken, breakpointId: string): TokenValue {
  let value = token.value;
  const selected = theme.breakpoints.find((bp) => bp.id === breakpointId);
  if (!selected) return value;
  for (const bp of [...theme.breakpoints].sort((a, b) => a.minWidthPx - b.minWidthPx)) {
    if (bp.minWidthPx <= selected.minWidthPx && bp.baseValues[token.id]) value = bp.baseValues[token.id]!;
  }
  return value;
}
export function effectiveFeatureValue(theme: StructuredDesignSystem, token: SnapshotFeatureToken, breakpointId: string): TokenValue {
  const key = `${token.featureKey}/${token.definition.key}`;
  let value = theme.featureValues[key] ?? token.definition.default;
  const selected = theme.breakpoints.find((bp) => bp.id === breakpointId);
  if (!selected) return value;
  for (const bp of [...theme.breakpoints].sort((a, b) => a.minWidthPx - b.minWidthPx)) {
    if (bp.minWidthPx <= selected.minWidthPx && bp.featureValues[key]) value = bp.featureValues[key]!;
  }
  return value;
}
