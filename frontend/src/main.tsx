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
import { ApiError } from "./lib/api";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      refetchOnWindowFocus: true,
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 2,
    },
  },
});

const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
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
      { path: "*", element: <div className="py-20 text-center text-muted">Page not found.</div> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ConfirmProvider>
        <RouterProvider router={router} />
        <Toaster theme="system" position="bottom-right" richColors closeButton />
      </ConfirmProvider>
    </QueryClientProvider>
  </StrictMode>,
);
