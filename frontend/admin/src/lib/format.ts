import type {
  FeatureStatus,
  FeatureType,
  MediaStatus,
  Role,
  SubmissionStatus,
  VersionStatus,
} from "../types";

const dateTimeFormatter = new Intl.DateTimeFormat("ru-RU", {
  day: "2-digit",
  month: "short",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

const dateFormatter = new Intl.DateTimeFormat("ru-RU", {
  day: "2-digit",
  month: "short",
  year: "numeric",
});

function parse(value: string | null | undefined): Date | null {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

export function formatDateTime(value: string | null | undefined): string {
  const date = parse(value);
  return date ? dateTimeFormatter.format(date) : "—";
}

export function formatDate(value: string | null | undefined): string {
  const date = parse(value);
  return date ? dateFormatter.format(date) : "—";
}

export function fullTimestamp(value: string | null | undefined): string {
  const date = parse(value);
  return date ? `${date.toISOString()} · ${date.toLocaleString("ru-RU")}` : "—";
}

export function formatDateTimeOrDash(value: string | null | undefined): string {
  return value ? formatDateTime(value) : "—";
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} Б`;
  const units = ["КБ", "МБ", "ГБ"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const formatted = value >= 100 ? value.toFixed(0) : value.toFixed(1);
  return `${formatted.replace(".", ",")} ${units[unit]}`;
}

export function pluralRu(
  count: number,
  forms: [string, string, string],
): string {
  const abs = Math.abs(count) % 100;
  const last = abs % 10;
  if (abs > 10 && abs < 20) return forms[2];
  if (last === 1) return forms[0];
  if (last >= 2 && last <= 4) return forms[1];
  return forms[2];
}

export function truncate(value: string, max = 80): string {
  if (value.length <= max) return value;
  return `${value.slice(0, max - 1).trimEnd()}…`;
}

export const roleLabels: Record<Role, string> = {
  admin: "Администратор",
  moderator: "Модератор",
  staff: "Контент-редактор",
  user: "Пользователь",
};

export const roleShortLabels: Record<Role, string> = {
  admin: "Админ",
  moderator: "Модератор",
  staff: "Редактор",
  user: "Пользователь",
};

export const featureTypeLabels: Record<FeatureType, string> = {
  feature: "Фича",
  form: "Форма",
};

export const featureTypeHints: Record<FeatureType, string> = {
  feature: "Входные данные связываются с моделью выбранного снапшота",
  form: "Приём заявок с публичной формы",
};

export const featureStatusLabels: Record<FeatureStatus, string> = {
  active: "Активна",
  archived: "В архиве",
};

export const versionStatusLabels: Record<VersionStatus, string> = {
  draft: "Черновик",
  published: "Опубликовано",
};

export const mediaStatusLabels: Record<MediaStatus, string> = {
  processing: "Обработка",
  ready: "Готово",
  failed: "Ошибка",
  deleted: "Удалено",
};

export const submissionStatusLabels: Record<SubmissionStatus, string> = {
  new: "Новая",
  in_progress: "В работе",
  processed: "Обработана",
  rejected: "Отклонена",
};

export const tokenRoleHints: Record<Role, string> = {
  admin: "Полный доступ, включая удаление заявок и управление всеми токенами",
  moderator: "Настройки сайта, контент, медиа и заявки; создаёт только пользовательские токены",
  staff: "Контент, файлы и заявки; без управления настройками сайта",
  user: "Контент, файлы и заявки; без управления настройками сайта",
};

export function formatPercent(value: number): string {
  return `${Math.round(value)}%`;
}

export function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text);
  }
  return new Promise((resolve, reject) => {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    try {
      const ok = document.execCommand("copy");
      document.body.removeChild(area);
      if (ok) resolve();
      else reject(new Error("copy failed"));
    } catch (error) {
      document.body.removeChild(area);
      reject(error instanceof Error ? error : new Error("copy failed"));
    }
  });
}

export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 2000);
}

export function formatDateForInput(value: string | null | undefined): string {
  const date = parse(value);
  if (!date) return "";
  const yyyy = date.getFullYear();
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

/** Converts a date input value to an RFC 3339 UTC instant at the given edge. */
export function dateInputToRfc3339(
  value: string,
  edge: "start" | "end",
): string | undefined {
  if (!value) return undefined;
  const date = new Date(`${value}T00:00:00.000Z`);
  if (Number.isNaN(date.getTime())) return undefined;
  if (edge === "end") date.setUTCDate(date.getUTCDate() + 1);
  return date.toISOString();
}
