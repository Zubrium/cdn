import type { Principal } from "@/types";
import { hasGroupAccess } from "@/lib/permissions";

/** File actions are visible only when one of that file's groups grants write. */
export function canDeleteGalleryFile(principal?: Principal | null, groups: string[] = []): boolean {
  return groups.some((group) => hasGroupAccess(principal, group, 2));
}
