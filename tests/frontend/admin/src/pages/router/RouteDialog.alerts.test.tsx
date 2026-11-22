import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useState: vi.fn(),
  useInfiniteQuery: vi.fn(),
  fileSets: vi.fn(),
  schemas: vi.fn(),
}));

vi.mock("react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react")>();
  return { ...actual, useState: mocks.useState };
});
vi.mock("@tanstack/react-query", () => ({ useInfiniteQuery: mocks.useInfiniteQuery }));
vi.mock("@/api", () => ({ api: { media: { fileSets: mocks.fileSets }, formsEngine: { schemas: mocks.schemas } } }));
vi.mock("@/domains/media/MediaPickerDialog", () => ({ MediaPickerDialog: () => null }));
vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogDescription: ({ children }: { children: React.ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: React.ReactNode }) => <footer>{children}</footer>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <header>{children}</header>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
}));

import { RouteDialog } from "../../../../../../frontend/admin/src/pages/router/RouterRoutesPage";

type Draft = {
  host: string;
  path: string;
  match: "exact" | "prefix";
  targetKind: "core-file" | "static-site-artifact" | "public.form-upload" | "public.form-session" | "public.form-submit";
  file: { id: string } | null;
  accessGroup: string;
  fileSetID: string;
  maxBytes: string;
  allowedContentTypes: string[];
  bindForm: boolean;
  formID: string;
  schemaVersion: number;
};

const operations = [{
  id: "public.form-upload",
  contract_version: 1,
  methods: ["POST"],
  max_request_bytes: 4194304,
  allowed_content_types: ["image/png"],
}];

const sessionDraft: Draft = {
  host: "files.example.test",
  path: "/form",
  match: "exact",
  targetKind: "public.form-session",
  file: null,
  accessGroup: "",
  fileSetID: "",
  maxBytes: "",
  allowedContentTypes: [],
  bindForm: false,
  formID: "44444444-4444-4444-8444-444444444444",
  schemaVersion: 3,
};

function renderDialog(draft: Draft, schemas: { error?: boolean; loading?: boolean } = {}) {
  mocks.useState.mockReset();
  mocks.useState.mockReturnValueOnce([draft, vi.fn()]).mockReturnValueOnce([false, vi.fn()]);
  mocks.useInfiniteQuery.mockImplementation((options: { queryKey: readonly unknown[] }) => {
    const base = {
      data: { pages: [{ items: [], next_cursor: null }], pageParams: [null] },
      error: null,
      isLoading: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      refetch: vi.fn(),
      fetchNextPage: vi.fn(),
    };
    if (options.queryKey[1] !== "forms") return base;
    return {
      ...base,
      data: schemas.error ? undefined : base.data,
      error: schemas.error ? new Error("schemas unavailable") : null,
      isLoading: Boolean(schemas.loading),
    };
  });

  return renderToStaticMarkup(
    <RouteDialog
      open
      route={null}
      groups={[]}
      operations={operations}
      operationsLoading={false}
      operationsError={false}
      onRetryOperations={() => {}}
      onClose={() => {}}
      onSave={async () => {}}
      saving={false}
    />,
  );
}

describe("router route dialog alerts", () => {
  beforeEach(() => {
    mocks.useState.mockReset();
    mocks.useInfiniteQuery.mockReset();
  });

  it("announces a failed schema load with role alert", () => {
    const markup = renderDialog(sessionDraft, { error: true });

    expect(markup).toContain('role="alert"');
    expect(markup).toContain("Список схем недоступен");
  });

  it("keeps loading messages as status", () => {
    const markup = renderDialog(sessionDraft, { loading: true });

    expect(markup).toContain('role="status"');
    expect(markup).toContain("Загружаем формы…");
    expect(markup).not.toContain("Список схем недоступен");
  });

  it("announces invalid upload configuration with role alert", () => {
    const markup = renderDialog({ ...sessionDraft, targetKind: "public.form-upload" });

    expect(markup).toContain('role="alert"');
    expect(markup).toContain("Выберите группу, допустимый лимит и хотя бы один тип из списка операции.");
  });
});
