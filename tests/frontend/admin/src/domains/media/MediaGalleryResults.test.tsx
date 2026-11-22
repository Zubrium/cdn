import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { MediaGalleryResults } from "../../../../../../frontend/admin/src/pages/media/components/media-page/MediaGalleryResults";

function render(state: "empty" | "empty-filtered", hasNextPage: boolean) {
  return renderToStaticMarkup(
    <MediaGalleryResults
      state={state}
      error={null}
      items={[]}
      view="grid"
      hasNextPage={hasNextPage}
      fetchingNextPage={false}
      onOpen={vi.fn()}
      onRetry={vi.fn()}
      onLoadMore={vi.fn()}
      onUpload={vi.fn()}
    />,
  );
}

describe("media gallery filtered-empty state", () => {
  it("keeps load-more available when matching files may be on the next page", () => {
    const markup = render("empty-filtered", true);

    expect(markup).toContain("Файлы не найдены");
    expect(markup).toContain("Показать ещё");
  });

  it("does not offer load-more when there is no next page", () => {
    expect(render("empty-filtered", false)).not.toContain("Показать ещё");
    expect(render("empty", true)).not.toContain("Показать ещё");
  });
});
