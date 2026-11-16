import { ApiError, isApiError } from "../api";
import type { ApiErrorDetail } from "../types";

export function errorMessage(error: unknown): string {
  if (isApiError(error)) return error.message;
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
