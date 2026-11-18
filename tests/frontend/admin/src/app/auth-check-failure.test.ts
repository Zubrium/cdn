import { describe, expect, it } from "vitest";
import { ApiError } from "../../../../../frontend/admin/src/api";
import { classifyAuthProbeFailure } from "../../../../../frontend/admin/src/app/auth-state";

describe("authentication probe failures", () => {
  it("treats a missing session as anonymous without an expired-session notice", () => {
    expect(classifyAuthProbeFailure(new ApiError(401, "unauthorized", "Unauthorized"))).toEqual({
      status: "anonymous",
      message: null,
    });
  });

  it("shows infrastructure failures as unavailable", () => {
    expect(classifyAuthProbeFailure(new Error("API unavailable"))).toEqual({
      status: "unavailable",
      message: "API unavailable",
    });
  });
});
