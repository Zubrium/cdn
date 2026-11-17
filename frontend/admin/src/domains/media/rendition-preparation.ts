import type { MediaRenditionContext, MediaRenditionState } from "@/types";

export interface MediaRenditionClient {
  prepareRenditions(mediaId: string, context: MediaRenditionContext): Promise<MediaRenditionState>;
  renditionState(mediaId: string, context: MediaRenditionContext): Promise<MediaRenditionState>;
}

const pollIntervalMs = 500;
const maxPolls = 120;

const sleep = (duration: number) => new Promise<void>((resolve) => window.setTimeout(resolve, duration));

export async function prepareMediaRenditions(
  mediaIds: string[],
  contexts: MediaRenditionContext[],
  client: MediaRenditionClient,
  wait: (duration: number) => Promise<void> = sleep,
): Promise<void> {
  const uniqueContexts = [...new Map(contexts.map((context) => [JSON.stringify(context), context])).values()];
  await Promise.all(mediaIds.flatMap((mediaId) => uniqueContexts.map(async (context) => {
    let state = await client.prepareRenditions(mediaId, context);
    for (let poll = 0; state.status !== "ready" && poll < maxPolls; poll += 1) {
      if (state.status === "failed") throw new Error(state.error || "Не удалось подготовить вариант файла");
      await wait(pollIntervalMs);
      state = await client.renditionState(mediaId, context);
    }
    if (state.status !== "ready") {
      throw new Error("Подготовка медиа занимает дольше ожидаемого. Повторите выбор позже.");
    }
  })));
}
