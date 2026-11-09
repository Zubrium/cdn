import type { SiteSnapshot, SnapshotCreationSummary } from "@/types";

const originLabels: Record<string, string> = {
  root: "Самостоятельный сайт",
  child: "Изменения сайта",
  content_fork: "Новый снапшот из черновиков контента",
  sitepack_import: "Импорт sitepack",
};

const changeLabels: Record<string, string> = {
  pages_routes: "Обновлены страницы и маршруты",
  features: "Изменён состав фич",
  site_metadata: "Изменены настройки сайта",
  design_system: "Изменена дизайн-система",
};

export function snapshotCreationLines(summary?: SnapshotCreationSummary): string[] {
  if (!summary) return [];
  const lines = [originLabels[summary.origin] ?? "Создание снапшота"];
  const featureChanges = summary.featureChanges ?? [];

  for (const change of summary.changes ?? []) {
    if (change === "features" && featureChanges.length) continue;
    lines.push(changeLabels[change] ?? `Изменено: ${change}`);
  }
  for (const change of featureChanges) {
    if (change.action === "added") lines.push(`Добавлена фича ${change.featureKey}${change.version ? `@${change.version}` : ""}`);
    else if (change.action === "removed") lines.push(`Удалена фича ${change.featureKey}${change.previousVersion ? `@${change.previousVersion}` : ""}`);
    else lines.push(`Обновлена фича ${change.featureKey}: ${change.previousVersion ?? "?"} → ${change.version ?? "?"}`);
  }

  for (const transfer of summary.inheritedModelContent ?? []) {
    const parts: string[] = [];
    if (transfer.publishedRecords > 0) parts.push(`${transfer.publishedRecords} опубликованных`);
    if (transfer.draftRecords > 0) parts.push(`${transfer.draftRecords} черновых`);
    if (parts.length) lines.push(`Данные ${transfer.entityKey}: ${parts.join(", ")} записей`);
  }

  return lines;
}

export function snapshotCreatorLabel(snapshot: Pick<SiteSnapshot, "createdByName">): string {
  return snapshot.createdByName?.trim() || "Недоступен";
}

export function formatSnapshotDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Дата недоступна";
  return new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date);
}
