import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import "./index.css";
import { AppLayout } from "./components/Layout";
import { ConfirmProvider } from "./components/ui";
import { LoginPage } from "./pages/Login";
import { DashboardPage } from "./pages/Dashboard";
import { ProjectsPage } from "./pages/Projects";
import { ProjectPage } from "./pages/Project";
import { NewResourcePage } from "./pages/NewResource";
import { ApplicationPage } from "./pages/Application";
import { DeploymentPage, DeploymentsPage } from "./pages/Deployment";
import { DatabasePage } from "./pages/Database";
import { ServicePage } from "./pages/Service";
import { ServersPage } from "./pages/Servers";
import { ServerPage } from "./pages/Server";
import { SettingsPage } from "./pages/Settings";
import { AlertsPage } from "./pages/Alerts";
import { TopologyPage } from "./pages/Topology";
import { UptimePage } from "./pages/Uptime";
import { FleetPage } from "./pages/Fleet";
import { StatusPage } from "./pages/StatusPage";
import { ApiError } from "./lib/api";
import { LocaleRoot, t } from "./lib/i18n";

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
  { path: "/login", element: <LoginPage /> },
  { path: "/status", element: <StatusPage /> },
  {
    element: <AppLayout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "projects", element: <ProjectsPage /> },
      { path: "projects/:id", element: <ProjectPage /> },
      { path: "projects/:id/new", element: <NewResourcePage /> },
      { path: "applications/:id", element: <ApplicationPage /> },
      { path: "applications/:id/deployments/:depId", element: <DeploymentPage /> },
      { path: "deployments", element: <DeploymentsPage /> },
      { path: "databases/:id", element: <DatabasePage /> },
      { path: "services/:id", element: <ServicePage /> },
      { path: "servers", element: <ServersPage /> },
      { path: "servers/:id", element: <ServerPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "alerts", element: <AlertsPage /> },
      { path: "uptime", element: <UptimePage /> },
      { path: "containers", element: <FleetPage /> },
      { path: "topology", element: <TopologyPage /> },
      { path: "*", element: <NotFound /> },
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
