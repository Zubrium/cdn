import type { Token } from "@/types";

export type StatusFilter = "all" | "active" | "revoked" | "expired";

export function tokenStatus(token: Token): Exclude<StatusFilter, "all"> {
  if (token.revoked_at) return "revoked";
  if (token.expires_at && new Date(token.expires_at).getTime() <= Date.now()) {
    return "expired";
  }
  return "active";
}
