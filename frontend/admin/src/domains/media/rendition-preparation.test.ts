import { describe, expect, it, vi } from "vitest";
import type { MediaRenditionContext, MediaRenditionState } from "@/types";
import { prepareMediaRenditions, type MediaRenditionClient } from "./rendition-preparation";

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
  it("waits until every selected file/profile is ready before applying selection", async () => {
    const prepareRenditions = vi.fn<MediaRenditionClient["prepareRenditions"]>().mockResolvedValue(state("pending"));
    const renditionState = vi.fn<MediaRenditionClient["renditionState"]>()
      .mockResolvedValueOnce(state("processing"))
      .mockResolvedValue(state("ready"));
    const wait = vi.fn(async () => {});

    await prepareMediaRenditions(["media-1"], [context, { ...context }], { prepareRenditions, renditionState }, wait);

    expect(prepareRenditions).toHaveBeenCalledTimes(1);
    expect(renditionState).toHaveBeenCalledTimes(2);
    expect(wait).toHaveBeenCalledTimes(2);
  });

  it("reports failed or stalled renditions instead of accepting an unusable file", async () => {
    const failedClient: MediaRenditionClient = {
      prepareRenditions: vi.fn().mockResolvedValue(state("failed", "invalid image")),
      renditionState: vi.fn(),
    };
    await expect(prepareMediaRenditions(["media-1"], [context], failedClient, async () => {})).rejects.toThrow("invalid image");

    const pendingClient: MediaRenditionClient = {
      prepareRenditions: vi.fn().mockResolvedValue(state("pending")),
      renditionState: vi.fn().mockResolvedValue(state("pending")),
    };
    await expect(prepareMediaRenditions(["media-1"], [context], pendingClient, async () => {})).rejects.toThrow("дольше ожидаемого");
    expect(pendingClient.renditionState).toHaveBeenCalledTimes(120);
  });
});
