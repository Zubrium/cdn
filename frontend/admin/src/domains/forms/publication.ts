import type { FormSchema, FormSubmissionFilter, RouterPublicOperation, RouterRoute } from "@/types";
import { ApiError } from "@/api";
import { errorMessage } from "@/lib/errors";

export interface FormRouteSuggestion {
  id: string;
  host?: string;
  path: string;
  operationId?: string;
  operationCompiled?: boolean;
  formId?: string;
  schemaVersion?: number;
}

export interface FormUploadSettings {
  accessGroup: string;
  fileSetID: string;
  maxBytes: number;
  allowedContentTypes: string[];
}

export interface FormPublicationTarget {
  match: "exact" | "prefix";
  targetKind: "public.form-upload" | "public.form-session" | "public.form-submit";
  fileSetID?: string;
  accessGroup?: string;
  maxBytes?: number;
  allowedContentTypes?: string[];
}

export interface FormPublicationStep {
  operationId: string;
  path: string;
  action: "create" | "update" | "skip";
  target: {
    kind: "registered-operation";
    operation_id: string;
    operation_config: Record<string, unknown>;
  };
  existing?: RouterRoute;
  reason?: string;
}

export interface FormPublicationInput {
  schema: FormSchema;
  host: string;
  basePath: string;
  operations: RouterPublicOperation[];
  existingRoutes?: RouterRoute[];
  decisions?: Record<string, "create" | "update" | "skip">;
  upload?: FormUploadSettings;
}

const keyBytes = new TextEncoder();
const accessGroupPattern = /^[a-z][a-z0-9_-]{0,63}$/;
const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function normalizedFormBasePath(key: string, value?: string): string {
  const defaultValue = `/forms/${key}`;
  if (!value || !value.trim() || !value.trim().startsWith("/")) return defaultValue;
  const trimmed = value.trim();
  return trimmed.length > 1 && trimmed.endsWith("/") ? trimmed.slice(0, -1) : trimmed;
}

// Mirrors validText(value, 128) in the Forms schema handler/service:
// non-empty, <=128 UTF-8 bytes, no surrounding whitespace and no NUL.
export function validFormKey(value: string): boolean {
  return value.length > 0 && value === value.trim() && !value.includes("\x00") && keyBytes.encode(value).length <= 128;
}

function fieldsRecord(schema: FormSchema): Record<string, unknown> {
  return schema.fields && typeof schema.fields === "object" && !Array.isArray(schema.fields)
    ? schema.fields as Record<string, unknown>
    : {};
}

export function hasFileFields(schema: FormSchema): boolean {
  return Object.values(fieldsRecord(schema)).some((field) =>
    field !== null && typeof field === "object" && !Array.isArray(field) && (field as Record<string, unknown>).type === "file",
  );
}

function operationByID(operations: RouterPublicOperation[], id: string): RouterPublicOperation | undefined {
  return operations.find((operation) => operation.id === id);
}

function sessionConfig(input: FormPublicationInput): Record<string, unknown> {
  return { enabled: true, form_id: input.schema.id, schema_version: input.schema.version };
}

function submitConfig(input: FormPublicationInput): Record<string, unknown> {
  return { enabled: true, form_id: input.schema.id, schema_version: input.schema.version };
}

export function validFormUploadSettings(settings: FormUploadSettings | undefined, operations: RouterPublicOperation[]): boolean {
  return formUploadSettingsError(settings, operations) === null;
}

export function formUploadSettingsError(settings: FormUploadSettings | undefined, operations: RouterPublicOperation[]): string | null {
  const operation = operationByID(operations, "public.form-upload");
  if (!operation) return "Операция public.form-upload не скомпилирована в текущем профиле Router.";
  if (!settings) return "Укажите настройки загрузки: группу доступа, набор файлов, лимит и MIME-типы.";
  if (!accessGroupPattern.test(settings.accessGroup)) return "Укажите группу доступа в формате [a-z][a-z0-9_-].";
  if (!canonicalUUID.test(settings.fileSetID)) return "Укажите корректный UUID набора файлов для вложений формы.";
  if (!Number.isSafeInteger(settings.maxBytes) || settings.maxBytes < 1 || settings.maxBytes > operation.max_request_bytes) {
    return `Укажите лимит файла от 1 до ${operation.max_request_bytes} байт.`;
  }
  const allowed = operation.allowed_content_types;
  if (settings.allowedContentTypes.length === 0 || settings.allowedContentTypes.some((type) => !allowed.includes(type))) {
    return "Выберите хотя бы один MIME-тип из разрешённых операцией public.form-upload.";
  }
  return null;
}

function uploadConfig(input: FormPublicationInput): Record<string, unknown> {
  const settings = input.upload;
  if (!settings) return {};
  return {
    enabled: true,
    access_group: settings.accessGroup,
    file_set_id: settings.fileSetID,
    max_bytes: settings.maxBytes,
    allowed_content_types: [...settings.allowedContentTypes],
    // Backend public.form-upload разбирает привязку только из вложенного `form`.
    form: sessionConfig(input),
  };
}

function routeBase(route: RouterRoute): string {
  return `${route.host}${route.path}`;
}

export function formPublicationSteps(input: FormPublicationInput): FormPublicationStep[] {
  const specs: Array<{ operationId: string; path: string; config: Record<string, unknown>; blockedReason?: string }> = [
    { operationId: "public.form-session", path: input.basePath, config: sessionConfig(input) },
    { operationId: "public.form-submit", path: `${input.basePath}/submit`, config: submitConfig(input) },
  ];
  if (hasFileFields(input.schema)) {
    specs.push({
      operationId: "public.form-upload",
      path: `${input.basePath}/upload`,
      config: uploadConfig(input),
      blockedReason: formUploadSettingsError(input.upload, input.operations) ?? undefined,
    });
  }
  return specs.map(({ operationId, path, config, blockedReason }) => {
    const operation = operationByID(input.operations, operationId);
    const existing = input.existingRoutes?.find((route) =>
      routeBase(route) === `${input.host}${path}` && route.target.kind === "registered-operation" &&
      route.target.operation_id === operationId,
    );
    const decision = input.decisions?.[operationId];
    if (blockedReason) {
      return {
        operationId,
        path,
        action: "skip",
        existing,
        reason: blockedReason,
        target: { kind: "registered-operation", operation_id: operationId, operation_config: {} },
      };
    }
    if (existing && decision !== "update") {
      return {
        operationId,
        path,
        action: "skip",
        existing,
        reason: decision === "skip" ? "Маршрут пропущен оператором" : "Маршрут уже существует",
        target: { kind: "registered-operation", operation_id: operationId, operation_config: config },
      };
    }
    if (!operation) {
      return {
        operationId,
        path,
        action: "skip",
        reason: "Операция не скомпилирована в текущем профиле",
        target: { kind: "registered-operation", operation_id: operationId, operation_config: config },
      };
    }
    return {
      operationId,
      path,
      action: existing ? "update" : "create",
      existing,
      target: { kind: "registered-operation", operation_id: operationId, operation_config: config },
    };
  });
}

export function staleFormBindingWarning(
  boundVersion: number | undefined,
  currentVersion: number,
  published: boolean,
): string | null {
  if (!published) return "Маршрут ещё не опубликован: опубликуйте форму в Router, чтобы фича получила рабочую ссылку. Предупреждение не блокирует сохранение страницы.";
  if (boundVersion === undefined) return "Маршрут не закреплён на версии формы. Проверьте привязку в Router.";
  if (boundVersion < currentVersion) {
    return `Маршрут закреплён на версии ${boundVersion}, текущая версия формы ${currentVersion}. Обновите привязку в Router вручную; предупреждение не блокирует сохранение страницы.`;
  }
  return null;
}

const routeHostPattern = /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*$/u;

export function validRouterHost(host: string): boolean {
  return host.length > 0 && host.length <= 253 && routeHostPattern.test(host);
}

export function formPublicationWarnings(steps: FormPublicationStep[], currentVersion: number): string[] {
  return steps.flatMap((step) => {
    if (!step.existing) return [];
    const config = step.existing.target.kind === "registered-operation" ? step.existing.target.operation_config : {};
    const binding = isRecord(config) && isRecord(config.form) ? config.form : config;
    const boundVersion = isRecord(binding) && typeof binding.schema_version === "number" && Number.isSafeInteger(binding.schema_version)
      ? binding.schema_version
      : undefined;
    const warning = staleFormBindingWarning(boundVersion, currentVersion, true);
    return warning ? [`${step.operationId}: ${warning}`] : [];
  });
}

export function formSaveErrorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) {
    return "Ключ формы уже используется другой формой. Выберите уникальный ключ.";
  }
  return errorMessage(error);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function routeSuggestions(
  routes: RouterRoute[],
  operations: RouterPublicOperation[],
): FormRouteSuggestion[] {
  const compiled = new Set(operations.map((operation) => operation.id));
  return routes
    .map((route) => {
      const target = route.target;
      if (target.kind !== "registered-operation") {
        return {
          id: route.id,
          host: route.host,
          path: route.path,
        } satisfies FormRouteSuggestion;
      }
      const config = target.operation_config;
      const formBinding = isRecord(config) && isRecord(config.form) ? config.form : config;
      const formID = isRecord(formBinding) && typeof formBinding.form_id === "string" ? formBinding.form_id : undefined;
      const schemaVersion = isRecord(formBinding) && typeof formBinding.schema_version === "number" && Number.isSafeInteger(formBinding.schema_version)
        ? formBinding.schema_version
        : undefined;
      return {
        id: route.id,
        host: route.host,
        path: route.path,
        operationId: target.operation_id,
        operationCompiled: compiled.has(target.operation_id),
        formId: formID,
        schemaVersion,
      } satisfies FormRouteSuggestion;
    })
    .sort((left, right) => `${left.host ?? ""}${left.path}`.localeCompare(`${right.host ?? ""}${right.path}`));
}

export function submissionFilterKey(filter: FormSubmissionFilter): string {
  return `${filter.key ?? ""}|${filter.state ?? ""}`;
}
