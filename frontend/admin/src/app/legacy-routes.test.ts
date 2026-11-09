import { describe, expect, it } from "vitest";
import { redirectLocation } from "./legacy-routes";

describe("legacy route redirects", () => {
  it("preserves snapshot selection and fragment when moving site settings into the workspace", () => {
    expect(redirectLocation("/site/settings", "?snapshot=snapshot-42", "#canonical"))
      .toBe("/site/settings?snapshot=snapshot-42#canonical");
  });
});
