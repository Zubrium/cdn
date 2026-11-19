import type { GroupLevel, Principal } from "@/types";

export function hasGroupAccess(
  principal: Principal | null | undefined,
  group: string,
  minimum: Exclude<GroupLevel, 0> = 1,
): boolean {
  return (principal?.group_access?.[group] ?? 0) >= minimum;
}

export function hasAnyGroupAccess(
  principal: Principal | null | undefined,
  minimum: Exclude<GroupLevel, 0> = 1,
): boolean {
  return Object.values(principal?.group_access ?? {}).some((level) => level >= minimum);
}

export function canManageAdministrators(principal?: Principal | null): boolean {
  return principal?.can_manage_administrators === true;
}

export function canManageSchema(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "features", 2);
}

export function canManageFeatures(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "features", 2);
}

export function canReadFeatures(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "features", 1);
}

export function canPublishContent(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "content", 2);
}

export function canPromoteContent(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "content", 2);
}

export function canManageMedia(principal?: Principal | null): boolean {
  return hasAnyGroupAccess(principal, 1);
}

export function canManageAccess(principal?: Principal | null): boolean {
  return canManageAdministrators(principal);
}

export function canReadFormSchemas(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "forms_schemas", 1);
}

export function canManageFormSchemas(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "forms_schemas", 2);
}

export function canViewSubmissions(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "forms_submissions", 1);
}

export function canManageSubmissions(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "forms_submissions", 2);
}

export function canManageReleases(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "features", 2);
}

export function canCreateFeature(principal?: Principal | null): boolean {
  return hasGroupAccess(principal, "features", 2);
}
