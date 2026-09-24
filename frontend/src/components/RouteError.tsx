import { isRouteErrorResponse, Link, useRouteError } from "react-router";
import { AlertTriangle, RefreshCw } from "lucide-react";
import { Button } from "./ui";
import { t } from "../lib/i18n";

/** Shown when a page throws while rendering; the rest of the app keeps working. */
export function RouteError() {
  const err = useRouteError();
  const message = isRouteErrorResponse(err) ? `${err.status} ${err.statusText}` : err instanceof Error ? err.message : String(err);
  console.error(err);
  return (
    <div className="mx-auto max-w-lg py-20 text-center">
      <AlertTriangle className="mx-auto size-8 text-amber-400" />
      <h1 className="mt-4 text-lg font-semibold">{t("This page could not be displayed")}</h1>
      <p className="mt-2 text-sm text-muted">{t("Something went wrong while showing it. Reload the page; if it keeps happening, report the message below.")}</p>
      <pre className="mt-4 overflow-x-auto rounded-lg border border-border bg-surface-2 p-3 text-left font-mono text-xs text-muted">{message}</pre>
      <div className="mt-5 flex justify-center gap-2">
        <Button variant="primary" icon={<RefreshCw className="size-4" />} onClick={() => location.reload()}>
          {t("Reload")}
        </Button>
        <Link to="/" className="inline-flex h-9 items-center rounded-lg border border-border px-3 text-sm text-muted hover:text-fg">
          {t("Go to the dashboard")}
        </Link>
      </div>
    </div>
  );
}
