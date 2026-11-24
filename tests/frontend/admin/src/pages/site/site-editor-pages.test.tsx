import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FeatureBuild, Media, SiteConfig } from "@/types";

const mocks = vi.hoisted(() => ({
  api: {
    media: {
      get: vi.fn(),
      list: vi.fn(),
    },
    website: {
      featureBuilds: vi.fn(),
    },
  },
  useConfirm: vi.fn(),
  useSiteWorkspace: vi.fn(),
}));

vi.mock("@/api", () => ({ api: mocks.api }));
vi.mock("@/components/common/confirm", () => ({ useConfirm: mocks.useConfirm }));
vi.mock("@/domains/media/MediaPickerDialog", () => ({
  MediaPickerDialog: () => null,
}));
vi.mock("../../../../../../frontend/admin/src/pages/site/SiteWorkspace", () => ({ useSiteWorkspace: mocks.useSiteWorkspace }));

import RouterPage from "../../../../../../frontend/admin/src/pages/site/RouterPage";
import PageDetailPage from "../../../../../../frontend/admin/src/pages/site/PageDetailPage";
import FeatureEditorPage from "../../../../../../frontend/admin/src/pages/site/FeatureEditorPage";
import FileRoutePage from "../../../../../../frontend/admin/src/pages/site/FileRoutePage";

const snapshotQuery = "snapshot=snapshot-42";
const fileID = "11111111-1111-4111-8111-111111111111";

const config: SiteConfig = {
  formatVersion: 3,
  pages: [
    {
      id: "home",
      name: "Главная",
      kind: "page",
      placements: [{
        id: "home-hero",
        build: { featureKey: "hero", version: "3.2.1" },
        slot: "main",
      }],
    },
  ],
  routeTree: {
    id: "site",
    type: "site",
    children: [
      { id: "home-route", type: "path", path: "/", pageId: "home" },
      {
        id: "price-route",
        type: "path",
        path: "/downloads/price-list.pdf",
        fileId: fileID,
      },
    ],
  },
};

const build: FeatureBuild = {
  id: "build-hero",
  featureId: "feature-hero",
  key: "hero",
  name: "Первый экран",
  version: "3.2.1",
  schemaVersionId: "schema-hero",
  digest: "sha256:hero",
  artifactSize: 1024,
  createdAt: "2026-11-15T09:00:00Z",
  manifest: {
    formatVersion: 2,
    key: "hero",
    name: "Первый экран",
    version: "3.2.1",
    engineApi: "2",
    slots: ["main", "hero"],
    arguments: {},
    dataInputs: [],
  },
};

const file: Media = {
  id: fileID,
  visibility: "public",
  object_key: "public/price-list.pdf",
  original_filename: "Прайс-лист.pdf",
  content_type: "application/pdf",
  size_bytes: 2048,
  status: "ready",
  variants: {},
  created_at: "2026-11-15T09:00:00Z",
  updated_at: "2026-11-15T09:00:00Z",
};

function renderWithProviders(element: React.ReactNode, path: string, queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})) {
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>{element}</Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("deep-link pages workflow", () => {
  beforeEach(() => {
    mocks.api.media.get.mockReset().mockResolvedValue(file);
    mocks.api.media.list.mockReset().mockResolvedValue({ items: [file], next_cursor: null });
    mocks.api.website.featureBuilds.mockReset().mockResolvedValue({ items: [build] });
    mocks.useConfirm.mockReturnValue(vi.fn(async () => false));
    mocks.useSiteWorkspace.mockReturnValue({
      config,
      metadata: {},
      update: vi.fn(),
      updateMetadata: vi.fn(),
    });
  });

  it("keeps the selected snapshot in page and file links", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["site", "feature-builds"], { items: [build] });
    queryClient.setQueryData(["media", "public-file-routes"], { items: [file], next_cursor: null });

    const markup = renderWithProviders(
      <Route path="/site/pages" element={<RouterPage />} />,
      `/site/pages?${snapshotQuery}`,
      queryClient,
    );

    expect(markup).toContain('href="/site/pages/home?snapshot=snapshot-42"');
    expect(markup).toContain('href="/site/pages/files/price-route?snapshot=snapshot-42"');
    expect(markup).toContain("Прайс-лист.pdf");
    expect(markup).not.toContain(fileID);
  });

  it("renders a full page editor with snapshot-preserving feature and back links", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["site", "feature-builds"], { items: [build] });
    mocks.useSiteWorkspace.mockReturnValue({
      config: {
        ...config,
        pages: config.pages.map((page) => ({
          ...page,
          placements: page.placements.map((placement) => ({ ...placement, slot: "hero" })),
        })),
      },
      metadata: {},
      update: vi.fn(),
      updateMetadata: vi.fn(),
    });

    const markup = renderWithProviders(
      <Route path="/site/pages/:pageId" element={<PageDetailPage />} />,
      `/site/pages/home?${snapshotQuery}`,
      queryClient,
    );

    expect(markup).toContain("Главная");
    expect(markup).toContain('maxLength="120"');
    expect(markup).toContain('href="/site/pages?snapshot=snapshot-42"');
    expect(markup).toContain('href="/site/pages/home/features/home-hero?snapshot=snapshot-42"');
    expect(markup).toMatch(/aria-current="page"[^>]*>Фичи</);
    expect(markup).not.toContain("home-route");
  });

  it("explains a missing page without leaking the requested internal ID", () => {
    const markup = renderWithProviders(
      <Route path="/site/pages/:pageId" element={<PageDetailPage />} />,
      "/site/pages/11111111-1111-4111-8111-111111111111?snapshot=snapshot-42",
    );

    expect(markup).toContain("Страница не найдена");
    expect(markup).toContain('href="/site/pages?snapshot=snapshot-42"');
    expect(markup).not.toContain("11111111-1111-4111-8111-111111111111");
  });

  it("opens a feature as a dedicated screen with a query-preserving back link", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["site", "feature-builds"], { items: [build] });

    const markup = renderWithProviders(
      <Route path="/site/pages/:pageId/features/:placementId" element={<FeatureEditorPage />} />,
      `/site/pages/home/features/home-hero?${snapshotQuery}`,
      queryClient,
    );

    expect(markup).toContain("Первый экран");
    expect(markup).toContain("max-w-5xl");
    expect(markup).toContain("Версия 3.2.1");
    expect(markup).toContain("слот main");
    expect(markup).toContain('href="/site/pages/home?snapshot=snapshot-42"');
    expect(markup).not.toContain("home-hero");
  });

  it("renders a file route with its public file and fixed-path contract", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["media", fileID], file);

    const markup = renderWithProviders(
      <Route path="/site/pages/files/:routeId" element={<FileRoutePage />} />,
      `/site/pages/files/price-route?${snapshotQuery}`,
      queryClient,
    );

    expect(markup).toContain("Прайс-лист.pdf");
    expect(markup).toContain("/downloads/price-list.pdf");
    expect(markup).toContain("Параметры маршрута для файлов недоступны");
    expect(markup).not.toContain("Скачивать файлом");
    expect(markup).not.toContain("Открывать в браузере");
    expect(markup).toContain('href="/site/pages?snapshot=snapshot-42"');
    expect(markup).not.toContain(fileID);
    expect(markup).not.toContain("price-route");
  });
});
