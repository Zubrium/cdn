import { describe, expect, it, vi } from "vitest";
import type { MediaRenditionContext, MediaRenditionState } from "@/types";
import { prepareMediaRenditions, type MediaRenditionClient } from "../../../../../../frontend/admin/src/domains/media/rendition-preparation";

const context: MediaRenditionContext = {
  snapshotId: "snapshot-1",
  featureKey: "catalog",
  buildVersion: "2.0.0",
  schemaKind: "dataInputs",
  fieldPath: "/products/imageSrc",
  buildDigest: "digest-1",
};

function state(status: MediaRenditionState["status"], error?: string): MediaRenditionState {
  return { mediaId: "media-1", status, error };
}

describe("prepareMediaRenditions", () => {
  it("accepts a rendition only when preparation is already ready", async () => {
    const prepareRenditions = vi.fn<MediaRenditionClient["prepareRenditions"]>().mockResolvedValue(state("ready"));

    await prepareMediaRenditions(["media-1"], [context, { ...context }], { prepareRenditions });

    expect(prepareRenditions).toHaveBeenCalledTimes(1);
  });

  it("requires a manual retry while preparation is pending or processing", async () => {
    const prepareRenditions = vi.fn<MediaRenditionClient["prepareRenditions"]>().mockResolvedValue(state("pending"));

    await expect(prepareMediaRenditions(["media-1"], [context], { prepareRenditions }))
      .rejects.toThrow("Повторите выбор файла вручную");
    expect(prepareRenditions).toHaveBeenCalledTimes(1);
  });

  it("reports failed renditions", async () => {
    const client: MediaRenditionClient = {
      prepareRenditions: vi.fn().mockResolvedValue(state("failed", "invalid image")),
    };

    await expect(prepareMediaRenditions(["media-1"], [context], client)).rejects.toThrow("invalid image");
  });
});
