import { ApiError } from "@/api";
import { errorMessage } from "@/lib/errors";

export type AuthProbeFailure =
  | { status: "anonymous"; message: null }
  | { status: "unavailable"; message: string };

export function classifyAuthProbeFailure(error: unknown): AuthProbeFailure {
  if (error instanceof ApiError && error.status === 401) {
    return { status: "anonymous", message: null };
  }

  return { status: "unavailable", message: errorMessage(error) };
}
