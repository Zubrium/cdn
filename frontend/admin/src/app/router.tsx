import { lazy } from "react";
import {
  createBrowserRouter,
  Navigate,
  Outlet,
  useLocation,
} from "react-router-dom";
import { useAuth } from "@/app/auth";
import { AppShell } from "@/app/layouts/AppShell";
import { LoginPage, NoAccessScreen } from "@/app/pages/LoginPage";
import NotFoundPage, { RouteErrorPage } from "@/app/pages/NotFoundPage";
import { LoadingPage } from "@/components/common/page";
import { canManageAdministrators, hasAnyGroupAccess, hasGroupAccess } from "@/lib/permissions";

const DashboardPage = lazy(() => import("@/pages/dashboard/DashboardPage"));
const ContentHomePage = lazy(() => import("@/pages/content/ContentHomePage"));
const SubmissionsPage = lazy(() => import("@/pages/submissions/SubmissionsPage"));
const FormsPage = lazy(() => import("@/pages/forms/FormsPage"));
const MediaPage = lazy(() => import("@/pages/media/MediaPage"));
const AccessLayout = lazy(() => import("@/pages/access/AccessLayout"));
const UsersPage = lazy(() => import("@/pages/access/UsersPage"));
const SettingsPage = lazy(() => import("@/pages/settings/SettingsPage"));
const PagesPage = lazy(() => import("@/pages/site/PagesPage"));
const PageTemplatesPage = lazy(() => import("@/pages/site/PageTemplatesPage"));
const PageDetailPage = lazy(() => import("@/pages/site/PageDetailPage"));
const FeatureEditorPage = lazy(() => import("@/pages/site/FeatureEditorPage"));
const FeatureRegistryPage = lazy(() => import("@/pages/site/FeatureRegistryPage"));
const DataModelPage = lazy(() => import("@/pages/site/DataModelPage"));
const DataModelEntityPage = lazy(() => import("@/pages/site/DataModelEntityPage"));
const SnapshotManagementLayout = lazy(() => import("@/pages/site/SnapshotManagementLayout"));
const FeatureBuildRegistryPage = lazy(() => import("@/pages/site/FeatureBuildRegistryPage"));
const SnapshotsPage = lazy(() => import("@/pages/site/SnapshotsPage"));
const SiteWorkspace = lazy(() => import("@/pages/site/SiteWorkspace").then((module) => ({ default: module.SiteWorkspace })));
const SiteMetadataPage = lazy(() => import("@/pages/content/SiteMetadataPage"));
const CoordinatorWorkflowsPage = lazy(() => import("@/pages/coordinator/CoordinatorWorkflowsPage"));
const RouterRoutesPage = lazy(() => import("@/pages/router/RouterRoutesPage"));

function RequireAuth() {
  const auth = useAuth();
  const location = useLocation();

  if (auth.status === "checking") return <LoadingPage label="Проверяем сессию…" />;
  if (auth.status === "unavailable") {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-background p-4">
        <div className="w-full max-w-md rounded-lg border border-border bg-card p-6">
          <h1 className="text-lg font-semibold">API временно недоступен</h1>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            {auth.error ?? "Не удалось проверить сессию."}
          </p>
          <div className="mt-4 flex gap-2">
            <button
              type="button"
              className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground"
              onClick={() => void auth.retry()}
            >
              Повторить
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-2 text-sm"
              onClick={() => auth.logout()}
            >
              Выйти
            </button>
          </div>
        </div>
      </div>
    );
  }
  if (auth.status === "anonymous") {
    return (
      <Navigate
        to="/login"
        replace
        state={{ from: `${location.pathname}${location.search}` }}
      />
    );
  }
  if (!auth.principal) return <LoadingPage />;
  return <Outlet />;
}

function RequireSiteManager({ children }: { children?: React.ReactNode }) {
  const auth = useAuth();
  if (!auth.principal) return <LoadingPage />;
  if (!hasGroupAccess(auth.principal, "features", 2)) return <NoAccessScreen />;
  // Rendering children instead of an Outlet keeps the parent route's outlet
  // context intact (useOutletContext inside children must still see it).
  if (children) return <>{children}</>;
  return <Outlet />;
}

function RequireGroup({ group, level = 1, children }: { group: string; level?: 1 | 2; children?: React.ReactNode }) {
  const auth = useAuth();
  if (!auth.principal) return <LoadingPage />;
  if (!hasGroupAccess(auth.principal, group, level)) return <NoAccessScreen />;
  if (children) return <>{children}</>;
  return <Outlet />;
}

function RequireAdministratorManagement({ children }: { children?: React.ReactNode }) {
  const auth = useAuth();
  if (!auth.principal) return <LoadingPage />;
  if (!canManageAdministrators(auth.principal)) return <NoAccessScreen />;
  if (children) return <>{children}</>;
  return <Outlet />;
}

function RequireAnyGroup({ children }: { children?: React.ReactNode }) {
  const auth = useAuth();
  if (!auth.principal) return <LoadingPage />;
  if (!hasAnyGroupAccess(auth.principal)) return <NoAccessScreen />;
  if (children) return <>{children}</>;
  return <Outlet />;
}

export const router = createBrowserRouter(
  [
    {
      path: "/login",
      element: <LoginPage />,
      errorElement: <RouteErrorPage />,
    },
    {
      path: "/",
      element: <RequireAuth />,
      errorElement: <RouteErrorPage />,
      children: [
        {
          element: <AppShell />,
          children: [
            { index: true, element: <DashboardPage /> },
            { path: "content", element: <RequireGroup group="content"><ContentHomePage /></RequireGroup> },
            { path: "content/entities/:entityKey", element: <RequireGroup group="content"><ContentHomePage /></RequireGroup> },
            { path: "content/entities/:entityKey/records/:recordId", element: <RequireGroup group="content"><ContentHomePage /></RequireGroup> },
            { path: "content/placements", element: <RequireGroup group="content"><ContentHomePage /></RequireGroup> },
            { path: "snapshots", element: <RequireSiteManager><SnapshotManagementLayout /></RequireSiteManager>, children: [
              { index: true, element: <Navigate to="tree" replace /> },
              { path: "registry", element: <FeatureBuildRegistryPage /> },
              { path: "tree", element: <SnapshotsPage /> },
            ] },
            { path: "submissions", element: <RequireGroup group="forms_submissions"><SubmissionsPage /></RequireGroup> },
            { path: "submissions/:submissionId", element: <RequireGroup group="forms_submissions"><SubmissionsPage /></RequireGroup> },
            { path: "forms", element: <RequireGroup group="forms_schemas"><FormsPage /></RequireGroup> },
            { path: "media", element: <RequireAnyGroup><MediaPage /></RequireAnyGroup> },
            { path: "coordinator/workflows", element: <RequireGroup group="coordinator_jobs"><CoordinatorWorkflowsPage /></RequireGroup> },
            { path: "coordinator/workflows/:workflowId", element: <RequireGroup group="coordinator_jobs"><CoordinatorWorkflowsPage /></RequireGroup> },
            { path: "router/routes", element: <RequireGroup group="router_routes"><RouterRoutesPage /></RequireGroup> },
            {
              element: <RequireGroup group="features" level={1} />,
              children: [{
                path: "site",
                element: <SiteWorkspace />,
                children: [
                  { index: true, element: <Navigate to="pages" replace /> },
                  { path: "pages", element: <PagesPage /> },
                  { path: "templates", element: <PageTemplatesPage /> },
                  { path: "features", element: <RequireSiteManager><FeatureRegistryPage /></RequireSiteManager> },
                  { path: "pages/:pageId", element: <RequireSiteManager><PageDetailPage /></RequireSiteManager> },
                  { path: "pages/:pageId/features/:placementId", element: <RequireSiteManager><FeatureEditorPage /></RequireSiteManager> },
                  { path: "settings", element: <RequireSiteManager><SiteMetadataPage /></RequireSiteManager> },
                  { path: "data-model", element: <RequireSiteManager><DataModelPage /></RequireSiteManager> },
                  { path: "data-model/:model", element: <RequireSiteManager><DataModelEntityPage /></RequireSiteManager> },
                ],
              }],
            },
            {
              element: <RequireAdministratorManagement />,
              children: [{
              path: "access",
              element: <AccessLayout />,
              children: [
                { index: true, element: <Navigate to="users" replace /> },
                { path: "users", element: <UsersPage /> },
              ],
            }],
            },
            {
              element: <RequireGroup group="features" level={1} />,
              children: [{ path: "settings", element: <SettingsPage /> }],
            },
            { path: "*", element: <NotFoundPage /> },
          ],
        },
      ],
    },
  ],
  {
    basename: "/admin",
    future: {
      v7_fetcherPersist: true,
      v7_normalizeFormMethod: true,
      v7_partialHydration: true,
      v7_relativeSplatPath: true,
      v7_skipActionErrorRevalidation: true,
    },
  },
);
