import { ApiError, isApiError } from "../api";
import type { ApiErrorDetail } from "../types";

const API_ERROR_MESSAGES: Record<string, string> = {
  invalid_request: "Некорректный запрос. Проверьте введённые данные.",
  invalid_payload: "Некорректные данные. Проверьте поля и повторите попытку.",
  invalid_query: "Некорректные параметры запроса. Измените фильтры и повторите попытку.",
  invalid_revision: "Данные изменились. Обновите страницу и повторите попытку.",
  conflict: "Данные изменились. Обновите страницу и повторите попытку.",
  not_found: "Запрошенный ресурс не найден.",
  forbidden: "Недостаточно прав для выполнения действия.",
  unauthorized: "Требуется авторизация. Войдите снова.",
  unavailable: "Сервис временно недоступен. Повторите попытку позже.",
};

export function errorMessage(error: unknown): string {
  if (isApiError(error)) {
    if (error.message && error.message !== error.code) return error.message;
    return API_ERROR_MESSAGES[error.code] ?? "Запрос завершился с ошибкой. Повторите попытку.";
  }
  if (error instanceof Error && error.message) return error.message;
  return "Произошла неизвестная ошибка";
}

export function errorDetails(error: unknown): ApiErrorDetail[] {
  return isApiError(error) ? error.details : [];
}

export function requestIdOf(error: unknown): string | undefined {
  return isApiError(error) ? error.requestId : undefined;
}

export function isConflictError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409;
}

export function isForbiddenError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403;
}

export function isNotFoundError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

export function conflictMessage(error: unknown): string {
  return errorMessage(error) || "Данные были изменены. Загрузите актуальную версию.";
}
