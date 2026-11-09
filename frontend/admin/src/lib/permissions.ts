import type { Role } from "@/types";

export function canManageSchema(role?: Role | null): boolean {
  return role === "admin" || role === "moderator";
}

export function canManageFeatures(role?: Role | null): boolean {
  return role === "admin" || role === "moderator";
}

export function canPublishContent(role?: Role | null): boolean {
	return role === "admin" || role === "moderator" || role === "staff" || role === "user";
}

export function canPromoteContent(role?: Role | null): boolean {
  return role === "admin" || role === "moderator";
}

export function canManageMedia(role?: Role | null): boolean {
	return role === "admin" || role === "moderator" || role === "staff" || role === "user";
}

export function canManageAccess(role?: Role | null): boolean {
  return role === "admin";
}

export function canManageSettings(role?: Role | null): boolean {
  return role === "admin";
}

export function canViewSubmissions(role?: Role | null): boolean {
	return role === "admin" || role === "moderator" || role === "staff" || role === "user";
}

export function canManageSubmissions(role?: Role | null): boolean {
  return role === "admin" || role === "moderator";
}

export function canManageReleases(role?: Role | null): boolean {
  return role === "admin" || role === "moderator";
}

export function canCreateFeature(role?: Role | null): boolean {
  return role === "admin" || role === "moderator";
}
