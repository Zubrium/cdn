import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useQuery: vi.fn(),
  useQueryClient: vi.fn(),
  useConfirm: vi.fn(),
  contentSnapshots: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: mocks.useQuery,
  useQueryClient: mocks.useQueryClient,
}));
vi.mock("@/api", () => ({ api: { siteBuilder: { contentSnapshots: mocks.contentSnapshots } } }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));
vi.mock("@/components/common/page", () => ({
  ErrorState: () => null,
  LoadingPage: () => null,
  PageHeader: ({ title }: { title: string }) => <h1>{title}</h1>,
  useDocumentTitle: () => {},
}));

import SnapshotsPage from "../../../../../../frontend/admin/src/pages/site/SnapshotsPage";

type RefetchInterval = (query: { state: { data?: { items: Array<{ state: string }> } } }) => number | false;

function renderWithItems(items: Array<{ id: string; state: string }>) {
  mocks.useQuery.mockReturnValue({
    data: { items },
    isLoading: false,
    isFetching: false,
    error: null,
    refetch: vi.fn(),
  });
  const markup = renderToStaticMarkup(<MemoryRouter><SnapshotsPage /></MemoryRouter>);
  const options = mocks.useQuery.mock.calls[0]?.[0] as { refetchInterval: RefetchInterval };
  return { markup, options };
}

describe("snapshots page state polling", () => {
  beforeEach(() => {
    mocks.useQuery.mockReset();
    mocks.useQueryClient.mockReturnValue({ invalidateQueries: vi.fn() });
    mocks.useConfirm.mockReturnValue(vi.fn(async () => true));
  });

  it("polls only while a snapshot is provisioning", () => {
    const { markup, options } = renderWithItems([{ id: "snapshot-1", state: "provisioning" }]);

    expect(markup).toContain("Готовится");
    expect(options.refetchInterval({ state: { data: { items: [{ state: "provisioning" }] } } })).toBe(5000);
    expect(options.refetchInterval({ state: { data: { items: [{ state: "ready" }, { state: "failed" }] } } })).toBe(false);
    expect(options.refetchInterval({ state: {} })).toBe(false);
  });
});
