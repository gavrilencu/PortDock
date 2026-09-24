import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, XCircle } from "lucide-react";
import { api } from "../lib/api";
import { cn, duration } from "../lib/utils";
import { intlLocale, t, tk } from "../lib/i18n";
import { UptimeBars, uptimeText, type UptimeDay } from "../components/uptime";

interface PublicStatus {
  title: string;
  description: string;
  status: "operational" | "degraded" | "outage";
  monitors: { name: string; status: string; uptime_90d: number | null; days: UptimeDay[]; response_ms: number }[];
  incidents: { monitor: string; started_at: string; resolved_at: string }[];
  updated_at: string;
}

const banner = {
  operational: { text: tk("All systems operational"), cls: "border-success/30 bg-success-soft text-success", icon: CheckCircle2 },
  degraded: { text: tk("Some systems are down"), cls: "border-warning/30 bg-warning-soft text-warning", icon: AlertTriangle },
  outage: { text: tk("Major outage"), cls: "border-danger/30 bg-danger-soft text-danger", icon: XCircle },
};

/** The public status page (no sign-in), at /status. */
export function StatusPage() {
  const q = useQuery({ queryKey: ["public-status"], queryFn: () => api.get<PublicStatus>("/public/status"), refetchInterval: 60_000, retry: false });
  useEffect(() => {
    if (q.data) document.title = q.data.title || t("Status");
  }, [q.data]);

  if (q.isLoading) return <div className="min-h-screen bg-bg" />;
  if (q.error || !q.data)
    return (
      <div className="flex min-h-screen items-center justify-center bg-bg px-4 text-center">
        <div>
          <h1 className="text-lg font-semibold">{t("Status page unavailable")}</h1>
          <p className="mt-1 text-sm text-muted">{t("This status page is not published.")}</p>
        </div>
      </div>
    );
  const s = q.data;
  const b = banner[s.status];
  const Icon = b.icon;
  return (
    <div className="min-h-screen bg-bg text-fg">
      <div className="mx-auto max-w-3xl px-4 py-10 sm:py-16">
        <h1 className="text-2xl font-semibold tracking-tight">{s.title || t("Status")}</h1>
        {s.description && <p className="mt-1.5 text-sm text-muted">{s.description}</p>}

        <div className={cn("mt-8 flex items-center gap-3 rounded-xl border px-5 py-4 text-[15px] font-medium", b.cls)}>
          <Icon className="size-5 shrink-0" />
          {t(b.text)}
        </div>

        <div className="mt-8 divide-y divide-border rounded-xl border border-border bg-surface">
          {s.monitors.length === 0 && <p className="px-5 py-6 text-sm text-muted">{t("No services are listed yet.")}</p>}
          {s.monitors.map((m) => (
            <div key={m.name} className="px-5 py-4">
              <div className="mb-2 flex items-center justify-between gap-3">
                <div className="flex items-center gap-2 text-sm font-medium">
                  {m.status === "down" ? <XCircle className="size-4 text-danger" /> : m.status === "paused" ? <AlertTriangle className="size-4 text-warning" /> : <CheckCircle2 className="size-4 text-success" />}
                  {m.name}
                </div>
                <span className={cn("text-xs font-medium", m.status === "down" ? "text-danger" : "text-subtle")}>
                  {m.status === "down" ? t("Down") : m.status === "paused" ? t("Maintenance") : t("Operational")} · {uptimeText(m.uptime_90d)}
                </span>
              </div>
              <UptimeBars days={m.days} />
              <div className="mt-1 flex justify-between text-2xs text-faint">
                <span>{t("90 days ago")}</span>
                <span>{t("Today")}</span>
              </div>
            </div>
          ))}
        </div>

        <h2 className="mt-10 text-sm font-semibold">{t("Incidents in the last 14 days")}</h2>
        {s.incidents.length === 0 ? (
          <p className="mt-2 text-sm text-muted">{t("No incidents.")}</p>
        ) : (
          <ul className="mt-3 space-y-2">
            {s.incidents.map((i, n) => (
              <li key={n} className="rounded-lg border border-border bg-surface px-4 py-3 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium">{t("{name} was down", { name: i.monitor })}</span>
                  <span className={cn("text-xs", i.resolved_at ? "text-success" : "text-danger")}>
                    {i.resolved_at ? t("Resolved after {d}", { d: duration(i.started_at, i.resolved_at) }) : t("Ongoing")}
                  </span>
                </div>
                <div className="mt-0.5 text-xs text-subtle">{new Date(i.started_at).toLocaleString(intlLocale(), { dateStyle: "medium", timeStyle: "short" })}</div>
              </li>
            ))}
          </ul>
        )}

        <p className="mt-12 text-center text-2xs text-faint">
          {t("Updated {time}", { time: new Date(s.updated_at).toLocaleTimeString(intlLocale(), { hour: "2-digit", minute: "2-digit" }) })} · {t("Powered by")}{" "}
          <a href="https://github.com/gavrilencu/PortDock" target="_blank" rel="noreferrer" className="underline hover:text-muted">
            PortDock
          </a>
        </p>
      </div>
    </div>
  );
}
