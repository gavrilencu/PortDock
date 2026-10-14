import { lazy, StrictMode, Suspense, type ComponentType } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import "./index.css";
import { AppLayout } from "./components/Layout";
import { ConfirmProvider, Loading } from "./components/ui";
import { RouteError } from "./components/RouteError";
import { LoginPage } from "./pages/Login";
import { DashboardPage } from "./pages/Dashboard";
import { ApiError } from "./lib/api";
import { LocaleRoot, t } from "./lib/i18n";

// Every page except the login and the dashboard is its own chunk, loaded
// when first visited: the sign-in screen no longer downloads the terminal,
// the topology map or the SQL console.
function page(load: () => Promise<{ default: ComponentType }>) {
  const C = lazy(load);
  return (
    <Suspense fallback={<Loading />}>
      <C />
    </Suspense>
  );
}
const ProjectsPage = page(() => import("./pages/Projects").then((m) => ({ default: m.ProjectsPage })));
const ProjectPage = page(() => import("./pages/Project").then((m) => ({ default: m.ProjectPage })));
const NewResourcePage = page(() => import("./pages/NewResource").then((m) => ({ default: m.NewResourcePage })));
const ApplicationPage = page(() => import("./pages/Application").then((m) => ({ default: m.ApplicationPage })));
const DeploymentPage = page(() => import("./pages/Deployment").then((m) => ({ default: m.DeploymentPage })));
const DeploymentsPage = page(() => import("./pages/Deployment").then((m) => ({ default: m.DeploymentsPage })));
const DatabasePage = page(() => import("./pages/Database").then((m) => ({ default: m.DatabasePage })));
const ServicePage = page(() => import("./pages/Service").then((m) => ({ default: m.ServicePage })));
const ServersPage = page(() => import("./pages/Servers").then((m) => ({ default: m.ServersPage })));
const ServerPage = page(() => import("./pages/Server").then((m) => ({ default: m.ServerPage })));
const SettingsPage = page(() => import("./pages/Settings").then((m) => ({ default: m.SettingsPage })));
const AlertsPage = page(() => import("./pages/Alerts").then((m) => ({ default: m.AlertsPage })));
const TopologyPage = page(() => import("./pages/Topology").then((m) => ({ default: m.TopologyPage })));
const UptimePage = page(() => import("./pages/Uptime").then((m) => ({ default: m.UptimePage })));
const FleetPage = page(() => import("./pages/Fleet").then((m) => ({ default: m.FleetPage })));
const StatusPage = page(() => import("./pages/StatusPage").then((m) => ({ default: m.StatusPage })));
const ApiDocsPage = page(() => import("./pages/ApiDocs").then((m) => ({ default: m.ApiDocsPage })));

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      refetchOnWindowFocus: true,
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 2,
    },
  },
});

function NotFound() {
  return <div className="py-20 text-center text-muted">{t("Page not found.")}</div>;
}

const router = createBrowserRouter([
  { path: "/login", element: <LoginPage />, errorElement: <RouteError /> },
  { path: "/status", element: StatusPage, errorElement: <RouteError /> },
  {
    element: <AppLayout />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <DashboardPage />, errorElement: <RouteError /> },
      { path: "projects", element: ProjectsPage, errorElement: <RouteError /> },
      { path: "projects/:id", element: ProjectPage, errorElement: <RouteError /> },
      { path: "projects/:id/new", element: NewResourcePage, errorElement: <RouteError /> },
      { path: "applications/:id", element: ApplicationPage, errorElement: <RouteError /> },
      { path: "applications/:id/deployments/:depId", element: DeploymentPage, errorElement: <RouteError /> },
      { path: "deployments", element: DeploymentsPage, errorElement: <RouteError /> },
      { path: "databases/:id", element: DatabasePage, errorElement: <RouteError /> },
      { path: "services/:id", element: ServicePage, errorElement: <RouteError /> },
      { path: "servers", element: ServersPage, errorElement: <RouteError /> },
      { path: "servers/:id", element: ServerPage, errorElement: <RouteError /> },
      { path: "settings", element: SettingsPage, errorElement: <RouteError /> },
      { path: "alerts", element: AlertsPage, errorElement: <RouteError /> },
      { path: "uptime", element: UptimePage, errorElement: <RouteError /> },
      { path: "containers", element: FleetPage, errorElement: <RouteError /> },
      { path: "topology", element: TopologyPage, errorElement: <RouteError /> },
      { path: "api-docs", element: ApiDocsPage, errorElement: <RouteError /> },
      { path: "*", element: <NotFound />, errorElement: <RouteError /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <LocaleRoot>
        <ConfirmProvider>
          <RouterProvider router={router} />
          <Toaster theme="system" position="bottom-right" richColors closeButton />
        </ConfirmProvider>
      </LocaleRoot>
    </QueryClientProvider>
  </StrictMode>,
);
