import { describe, expect, it } from "vitest";
import { activeFeatureImportIDForPrincipal } from "../../../../../../frontend/admin/src/pages/site/active-feature-import";

describe("active feature import ownership", () => {
  it("does not expose a saved import when no principal is authenticated", () => {
    expect(activeFeatureImportIDForPrincipal({ principalID: "admin-1", id: "job-1" }, undefined)).toBeNull();
  });

  it("does not expose another principal's saved import", () => {
    expect(activeFeatureImportIDForPrincipal({ principalID: "admin-1", id: "job-1" }, "admin-2")).toBeNull();
  });

  it("returns the saved import for the authenticated principal", () => {
    expect(activeFeatureImportIDForPrincipal({ principalID: "admin-1", id: "job-1" }, "admin-1")).toBe("job-1");
  });
});
