import { describe, expect, it } from "vitest";
import { mediaRenditionContext } from "./useModelContentEditorModel";

describe("mediaRenditionContext", () => {
  it("keeps the rendition API context free of manifest picker filters", () => {
    const declared = {
      featureKey: "product-card",
      buildVersion: "2.1.0",
      schemaKind: "dataInputs" as const,
      fieldPath: "/product/image",
      buildDigest: "sha256:build",
      accept: "raster-image",
    };

    expect(mediaRenditionContext(declared, "snapshot-1")).toEqual({
      snapshotId: "snapshot-1",
      featureKey: "product-card",
      buildVersion: "2.1.0",
      schemaKind: "dataInputs",
      fieldPath: "/product/image",
      buildDigest: "sha256:build",
    });
    expect(mediaRenditionContext(declared, "snapshot-1")).not.toHaveProperty("accept");
  });
});
