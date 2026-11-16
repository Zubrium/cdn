export type RouteSegmentKind = "static" | "param" | "regex" | "catchAll";
export type RouteRegexPreset = "integer" | "number" | "slug" | "uuid" | "date" | "custom";

export interface RouteSegmentDraft {
  kind: RouteSegmentKind;
  staticValue: string;
  name: string;
  preset: RouteRegexPreset;
  customPattern: string;
}

export const routeRegexPresets: ReadonlyArray<{
  value: RouteRegexPreset;
  label: string;
  pattern: string;
  example: string;
}> = [
  { value: "integer", label: "Целые числа", pattern: "[0-9]+", example: "42" },
  { value: "number", label: "Число", pattern: "[0-9]+(?:\\.[0-9]+)?", example: "4.5" },
  { value: "slug", label: "Slug", pattern: "[a-z0-9]+(?:-[a-z0-9]+)*", example: "catalog-product" },
  { value: "uuid", label: "UUID", pattern: "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}", example: "550e8400-e29b-41d4-a716-446655440000" },
  { value: "date", label: "Дата", pattern: "[0-9]{4}-[0-9]{2}-[0-9]{2}", example: "2026-11-14" },
  { value: "custom", label: "Свой шаблон", pattern: "", example: "" },
];

export function routeSegmentPreset(value: RouteRegexPreset) {
  return routeRegexPresets.find((preset) => preset.value === value) ?? routeRegexPresets[0]!;
}

export function buildRouteSegment(draft: RouteSegmentDraft): { value?: string; error?: string; example?: string } {
  if (draft.kind === "static") {
    const value = draft.staticValue.trim();
    if (!value) return { error: "Введите текст сегмента." };
    if (value.includes("/") || value.includes("#") || value.includes("?") || value === "." || value === "..") {
      return { error: "Статический сегмент не может содержать /, #, ? и не может быть точкой." };
    }
    return { value };
  }

  const name = draft.name.trim();
  if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/u.test(name)) {
    return { error: "Имя должно начинаться с латинской буквы и содержать только буквы, цифры и _." };
  }
  if (draft.kind === "param") return { value: `:${name}`, example: "example" };
  if (draft.kind === "catchAll") return { value: `*${name}`, example: "section/page" };

  const preset = routeSegmentPreset(draft.preset);
  const pattern = draft.preset === "custom" ? draft.customPattern.trim() : preset.pattern;
  if (!pattern) return { error: "Введите регулярное выражение." };
  if (pattern.includes("/")) return { error: "Шаблон применяется к одному сегменту и не может содержать /." };
  try {
    const compiled = new RegExp(`^(?:${pattern})$`, "u");
    if (compiled.test("")) return { error: "Шаблон не должен совпадать с пустым сегментом." };
  } catch {
    return { error: "Проверьте синтаксис шаблона. Экспертный режим принимает синтаксис маршрутного движка." };
  }
  return {
    value: `~(?<${name}>${pattern})`,
    example: preset.example || "example",
  };
}

export function appendRouteSegment(path: string, segment: string): string {
  if (path === "/" || !path) return `/${segment}`;
  return `${path.replace(/\/+$/u, "")}/${segment}`;
}

export function routeCaptureNames(value: string): string[] {
  const names: string[] = [];
  for (const segment of value.split("/").filter(Boolean)) {
    if (segment.startsWith(":") || segment.startsWith("*")) {
      const name = segment.slice(1);
      if (name) names.push(name);
      continue;
    }
    for (const match of segment.matchAll(/\(\?<([A-Za-z][A-Za-z0-9_]*)>/gu)) {
      if (match[1]) names.push(match[1]);
    }
  }
  return names;
}

export function routeCapturePreview(value: string): string[] {
  return [...new Set(routeCaptureNames(value))];
}

export function duplicateRouteCaptures(inherited: string[], value: string): string[] {
  const seen = new Set(inherited);
  const duplicates = new Set<string>();
  for (const name of routeCaptureNames(value)) {
    if (seen.has(name)) duplicates.add(name);
    seen.add(name);
  }
  return [...duplicates];
}
