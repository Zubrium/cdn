import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  api: {
    coordinator: {
      workflows: {
        list: vi.fn(),
        detail: vi.fn(),
        permissions: vi.fn(),
        retryTask: vi.fn(),
      },
    },
  },
}));

vi.mock("@/api", () => ({
  api: mocks.api,
  ApiError: class ApiError extends Error { isForbidden = false; },
}));

import CoordinatorWorkflowsPage from "../../../../../../frontend/admin/src/pages/coordinator/CoordinatorWorkflowsPage";
import { queryKeys } from "../../../../../../frontend/admin/src/lib/query-keys";

const workflowID = "11111111-1111-4111-8111-111111111111";
const rootTaskID = "22222222-2222-4222-8222-222222222222";
const failedTaskID = "33333333-3333-4333-8333-333333333333";
const joinTaskID = "44444444-4444-4444-8444-444444444444";
const createdAt = "2026-11-20T10:00:00.000Z";

const workflowSummary = {
  id: workflowID,
  owner_engine_id: "engine.website",
  state: "failed" as const,
  task_count: 3,
  created_at: createdAt,
  updated_at: createdAt,
};

const workflowDetail = {
  workflow: { ...workflowSummary, root_task_id: rootTaskID },
  tasks: {
    items: [
      {
        id: rootTaskID,
        workflow_id: workflowID,
        kind: "worker" as const,
        worker: { component_id: "worker.archive", operation_id: "feature-import", contract_version: 1 },
        depends_on: [],
        state: "succeeded" as const,
        attempts: 1,
        max_attempts: 3,
        available_at: createdAt,
        created_at: createdAt,
        updated_at: createdAt,
      },
      {
        id: failedTaskID,
        workflow_id: workflowID,
        kind: "worker" as const,
        worker: { component_id: "worker.media", operation_id: "derivative", contract_version: 1 },
        depends_on: [rootTaskID],
        state: "failed" as const,
        attempts: 3,
        max_attempts: 3,
        available_at: createdAt,
        error_code: "task_failed",
        created_at: createdAt,
        updated_at: createdAt,
      },
      {
        id: joinTaskID,
        workflow_id: workflowID,
        kind: "join" as const,
        depends_on: [failedTaskID],
        state: "cancelled" as const,
        attempts: 0,
        max_attempts: 1,
        available_at: createdAt,
        created_at: createdAt,
        updated_at: createdAt,
      },
    ],
    next_cursor: null,
  },
};

function renderPage(path: string, canRetryTasks = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
  if (path === "/coordinator/workflows") {
    client.setQueryData(queryKeys.coordinator.workflowList({ state: "" }), {
      pages: [{ items: [workflowSummary], next_cursor: null }],
      pageParams: [""],
    });
  } else {
    client.setQueryData(queryKeys.coordinator.workflow(workflowID), {
      pages: [workflowDetail],
      pageParams: [""],
    });
    client.setQueryData(queryKeys.coordinator.workflowPermissions, { can_retry_tasks: canRetryTasks });
  }
  const router = createMemoryRouter([
    { path: "/coordinator/workflows", element: createElement(CoordinatorWorkflowsPage) },
    { path: "/coordinator/workflows/:workflowId", element: createElement(CoordinatorWorkflowsPage) },
  ], { initialEntries: [path] });
  return renderToStaticMarkup(createElement(QueryClientProvider, { client }, createElement(RouterProvider, { router })));
}

describe("Coordinator workflow screens", () => {
  beforeEach(() => {
    mocks.api.coordinator.workflows.list.mockResolvedValue({ items: [workflowSummary], next_cursor: null });
    mocks.api.coordinator.workflows.detail.mockResolvedValue(workflowDetail);
    mocks.api.coordinator.workflows.permissions.mockResolvedValue({ can_retry_tasks: true });
    mocks.api.coordinator.workflows.retryTask.mockResolvedValue(undefined);
  });

  it("lists workflow summaries and links to the task graph", () => {
    const rendered = renderPage("/coordinator/workflows");

    expect(rendered).toContain("Workflows Coordinator");
    expect(rendered).toContain('href="/coordinator/workflows/' + workflowID + '"');
    expect(rendered).toContain("engine.website");
    expect(rendered).toContain("3");
  });

  it("shows worker and join tasks and limits retry to failed worker tasks with permission", () => {
    const rendered = renderPage(`/coordinator/workflows/${workflowID}`);

    expect(rendered).toContain("Граф задач");
    expect(rendered).toContain("worker.media · derivative/v1");
    expect(rendered).toContain("Барьер");
    expect(rendered).toContain("Зависит от");
    expect(rendered.match(/Retry задачи/g)).toHaveLength(1);
  });

  it("disables task retry when the credential lacks write access", () => {
    const rendered = renderPage(`/coordinator/workflows/${workflowID}`, false);

    expect(rendered).toMatch(/<button[^>]*disabled=""[^>]*>.*Retry задачи/s);
  });
});
