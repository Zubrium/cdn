import type { MediaRenditionContext, MediaRenditionState } from "@/types";

export interface MediaRenditionClient {
  prepareRenditions(mediaId: string, context: MediaRenditionContext): Promise<MediaRenditionState>;
}

export async function prepareMediaRenditions(
  mediaIds: string[],
  contexts: MediaRenditionContext[],
  client: MediaRenditionClient,
): Promise<void> {
  const uniqueContexts = [...new Map(contexts.map((context) => [JSON.stringify(context), context])).values()];
  await Promise.all(mediaIds.flatMap((mediaId) => uniqueContexts.map(async (context) => {
    const state = await client.prepareRenditions(mediaId, context);
    if (state.status === "failed") throw new Error(state.error || "Не удалось подготовить вариант файла");
    if (state.status !== "ready") {
      throw new Error("Подготовка медиа запущена. Повторите выбор файла вручную после завершения обработки.");
    }
  })));
}
