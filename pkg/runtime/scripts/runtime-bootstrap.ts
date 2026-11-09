import type { Composition, RuntimeBootstrap } from "./runtime-types";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isComposition(value: unknown): value is Composition {
  if (!isRecord(value) || !isRecord(value.snapshot) || !isRecord(value.page) || !isRecord(value.metadata) || !Array.isArray(value.placements)) return false;
  if (typeof value.snapshot.id !== "string" || typeof value.page.kind !== "string") return false;
  const placementIds = new Set<string>();
  for (const placement of value.placements) {
    if (!isRecord(placement)
      || typeof placement.id !== "string"
      || placementIds.has(placement.id)
      || !["background", "header", "main", "footer"].includes(String(placement.slot))
      || typeof placement.featureKey !== "string"
      || typeof placement.version !== "string"
      || typeof placement.entryUrl !== "string"
      || !isRecord(placement.arguments)) return false;
    placementIds.add(placement.id);
  }
  return true;
}

export function readRuntimeBootstrap(): RuntimeBootstrap {
  const script = document.getElementById("engine-runtime-bootstrap");
  if (!script) return { composition: null, error: "В документе отсутствуют данные для запуска страницы." };
  const isJSON = script instanceof HTMLScriptElement && script.type === "application/json";
  const source = script.textContent ?? "";
  if (!isJSON) return { composition: null, error: "Неверный формат данных серверной страницы." };
  try {
    const value: unknown = JSON.parse(source);
    if (!isComposition(value)) throw new Error("invalid composition");
    return { composition: value, error: null };
  } catch {
    return { composition: null, error: "Данные страницы в серверном документе повреждены." };
  }
}
