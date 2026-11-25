import { afterEach, describe, expect, it, vi } from "vitest";
import type { MediaRenditionContext, WebsiteMediaJob } from "@/types";
import { prepareMediaRenditions, type MediaRenditionClient } from "../../../../../../frontend/admin/src/domains/media/rendition-preparation";

const context: MediaRenditionContext = {
  snapshotId: "snapshot-1", placementId: "placement-1", schemaKind: "dataInputs", fieldPath: "/products/imageSrc",
};
function job(state: WebsiteMediaJob["state"]): WebsiteMediaJob {
  return { id: "job-1", source_file_id: "media-1", profile_digest: "digest", processor_version: "v1", state, ...(state === "ready" ? { outputs: { original: "output-id" } } : {}), created_at: "", updated_at: "" };
}
afterEach(() => vi.useRealTimers());

describe("prepareMediaRenditions", () => {
  it("waits for a pending derivative before accepting the media", async () => {
    vi.useFakeTimers();
    const client: MediaRenditionClient = {
      createMediaDerivative: vi.fn().mockResolvedValue(job("queued")),
      mediaJob: vi.fn().mockResolvedValueOnce(job("running")).mockResolvedValueOnce(job("ready")),
    };
    let complete = false;
    const promise = prepareMediaRenditions(["media-1"], [context, { ...context }], client).then(() => { complete = true; });
    await vi.advanceTimersByTimeAsync(1000);
    expect(complete).toBe(false);
    await vi.advanceTimersByTimeAsync(1000);
    await promise;
    expect(client.createMediaDerivative).toHaveBeenCalledTimes(1);
    expect(client.createMediaDerivative).toHaveBeenCalledWith({ snapshot_id: "snapshot-1", placement_id: "placement-1", schema_kind: "dataInputs", field_path: "/products/imageSrc", source_file_id: "media-1" }, expect.any(String), expect.any(AbortSignal));
    expect(complete).toBe(true);
  });
  it("rejects a failed job instead of applying the new media", async () => {
    const client: MediaRenditionClient = {
      createMediaDerivative: vi.fn().mockResolvedValue({ ...job("rejected"), error_code: "bad_source" }),
      mediaJob: vi.fn(),
    };
    await expect(prepareMediaRenditions(["media-1"], [context], client)).rejects.toThrow();
    expect(client.mediaJob).not.toHaveBeenCalled();
  });
  it("does not accept a result after the user cancels", async () => {
    const controller = new AbortController();
    let finish!: (value: WebsiteMediaJob) => void;
    const client: MediaRenditionClient = {
      createMediaDerivative: vi.fn().mockImplementation(() => new Promise<WebsiteMediaJob>((resolve) => { finish = resolve; })),
      mediaJob: vi.fn(),
    };
    const result = prepareMediaRenditions(["media-1"], [context], client, controller.signal);
    controller.abort();
    finish(job("ready"));
    await expect(result).rejects.toMatchObject({ name: "AbortError" });
  });

  it("does not start a job when the request is already cancelled", async () => {
    const controller = new AbortController();
    controller.abort();
    const client: MediaRenditionClient = { createMediaDerivative: vi.fn(), mediaJob: vi.fn() };
    await expect(prepareMediaRenditions(["media-1"], [context], client, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(client.createMediaDerivative).not.toHaveBeenCalled();
  });

});
