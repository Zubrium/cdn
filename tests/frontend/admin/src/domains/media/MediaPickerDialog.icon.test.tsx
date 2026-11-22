import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useInfiniteQuery: vi.fn(),
  list: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({ useInfiniteQuery: mocks.useInfiniteQuery }));
vi.mock("@/api", () => ({ api: { media: { list: mocks.list } } }));
vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogDescription: ({ children }: { children: React.ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <footer>{children}</footer>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <header>{children}</header>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
}));

import { MediaPickerDialog } from "../../../../../../frontend/admin/src/domains/media/MediaPickerDialog";

function renderPicker() {
  return renderToStaticMarkup(
    <MemoryRouter>
      <MediaPickerDialog open onClose={() => {}} onSelect={() => {}} />
    </MemoryRouter>,
  );
}

describe("media picker library link", () => {
  beforeEach(() => {
    mocks.useInfiniteQuery.mockReturnValue({
      data: { pages: [{ items: [], next_cursor: null }], pageParams: [null] },
      isLoading: false,
      error: null,
      hasNextPage: false,
      isFetchingNextPage: false,
      refetch: vi.fn(),
      fetchNextPage: vi.fn(),
    });
  });

  it("uses an external-link icon instead of an upload icon", () => {
    const markup = renderPicker();

    expect(markup).toContain("Открыть медиатеку");
    expect(markup).toContain("lucide-external-link");
    expect(markup).not.toContain("lucide-upload");
  });
});
