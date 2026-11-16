import type { Role } from "@/types";

/** Only administrators may delete files, matching the API role gate. */
export function canDeleteGalleryFile(role?: Role | null): boolean {
  return role === "admin";
}
