import type { MediaRenditionContext, WebsiteMediaJob } from "@/types";
import { uid } from "@/lib/utils";

export interface MediaRenditionClient {
  createMediaDerivative(input: { snapshot_id: string; placement_id: string; schema_kind: "arguments" | "dataInputs"; field_path: string; source_file_id: string }, idempotencyKey: string, signal?: AbortSignal): Promise<WebsiteMediaJob>;
  mediaJob(id: string, signal?: AbortSignal): Promise<WebsiteMediaJob>;
}

function waitForPoll(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException("Cancelled", "AbortError")); return; }
    const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, 1000);
    const abort = () => { clearTimeout(timer); reject(new DOMException("Cancelled", "AbortError")); };
    signal.addEventListener("abort", abort, { once: true });
  });
}

export async function prepareMediaRenditions(
  mediaIds: string[],
  contexts: MediaRenditionContext[],
  client: MediaRenditionClient,
  signal?: AbortSignal,
): Promise<void> {
  const uniqueContexts = [...new Map(contexts.map((context) => [JSON.stringify(context), context])).values()];
  const controller = new AbortController();
  const cancel = () => controller.abort();
  if (signal?.aborted) controller.abort();
  signal?.addEventListener("abort", cancel, { once: true });
  try {
    if (controller.signal.aborted) throw new DOMException("Cancelled", "AbortError");
    await Promise.all(mediaIds.flatMap((mediaId) => uniqueContexts.map(async (context) => {
      let state = await client.createMediaDerivative({
        snapshot_id: context.snapshotId, placement_id: context.placementId, schema_kind: context.schemaKind,
        field_path: context.fieldPath, source_file_id: mediaId,
      }, uid("media"), controller.signal);
      while (state.state === "queued" || state.state === "running") {
        await waitForPoll(controller.signal);
        state = await client.mediaJob(state.id, controller.signal);
      }
      if (controller.signal.aborted) throw new DOMException("Cancelled", "AbortError");
      if (state.state !== "ready" || !state.outputs || Object.keys(state.outputs).length === 0) {
        throw new Error("Не удалось подготовить варианты файла. Повторите выбор после проверки обработки.");
      }
    })));
  } catch (error) {
    controller.abort();
    throw error;
  } finally {
    signal?.removeEventListener("abort", cancel);
  }
}
