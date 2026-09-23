import { useMemo } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Bell, Box, Check, CircleCheck, FolderKanban, Rocket, Server as ServerIcon, Timer } from "lucide-react";
import { api } from "../lib/api";
import type { Dashboard, Deployment, NotificationChannel } from "../lib/types";
import { Button, Card, Empty, ErrorBox, Loading, PageHeader, Stat, StatusBadge, Table } from "../components/ui";
import { DeploymentsTable } from "../components/shared";
import { DeployActivityChart } from "../components/DeployActivityChart";
import { useUser } from "../components/Layout";
import { cn, timeAgo } from "../lib/utils";
import { t } from "../lib/i18n";

function median(xs: number[]) {
  if (!xs.length) return 0;
  const s = [...xs].sort((a, b) => a - b);
  const m = Math.floor(s.length / 2);
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

function fmtSeconds(s: number) {
  if (!s) return "—";
  if (s < 60) return t("{s}s", { s: Math.round(s) });
  return t("{m}m {s}s", { m: Math.floor(s / 60), s: Math.round(s % 60) });
}

export function DashboardPage() {
  const user = useUser();
  const dash = useQuery({ queryKey: ["dashboard"], queryFn: () => api.get<Dashboard>("/dashboard"), refetchInterval: 30_000 });
  const deps = useQuery({ queryKey: ["deployments", ""], queryFn: () => api.get<Deployment[]>("/deployments"), refetchInterval: 30_000 });
  const channels = useQuery({ queryKey: ["notifications"], queryFn: () => api.get<NotificationChannel[]>("/notifications"), enabled: user.role !== "member", retry: false });

  const stats = useMemo(() => {
    const since = Date.now() - 14 * 86400_000;
    const recent = (deps.data ?? []).filter((d) => new Date(d.created_at).getTime() >= since);
    const done = recent.filter((d) => d.status === "success" || d.status === "failed");
    const ok = done.filter((d) => d.status === "success");
    const times = ok.filter((d) => d.started_at && d.finished_at).map((d) => (new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000);
    return { total: recent.length, rate: done.length ? Math.round((ok.length / done.length) * 100) : null, median: median(times) };
  }, [deps.data]);

  if (dash.isLoading) return <Loading rows={4} />;
  if (dash.error) return <ErrorBox error={dash.error} />;
  const d = dash.data!;
  const online = d.servers.filter((s) => s.status === "reachable").length;

  const steps = [
    { done: online > 0, title: t("Connect a server"), text: t("Validate the local machine or add a remote server over SSH."), to: "/servers" },
    { done: d.counts.projects > 0, title: t("Create a project"), text: t("Projects group resources into environments such as production and staging."), to: "/projects?new=1" },
    { done: d.counts.applications > 0, title: t("Deploy an application"), text: t("Connect a Git repository or a Docker image and ship the first release."), to: "/projects" },
    { done: (channels.data?.length ?? 0) > 0, title: t("Set up notifications"), text: t("Get alerted in Slack, Discord, Telegram or email when deployments fail."), to: "/settings?tab=notifications" },
  ];
  const onboarding = steps.filter((s) => s.done).length < steps.length && user.role !== "member";

  return (
    <>
      <PageHeader title={t("Overview")} subtitle={t("Health, delivery and activity across your organization.")} />

      {onboarding && (
        <Card className="mb-6" title={t("Get started with PortDock")} description={t("{done} of {n} steps completed", { done: steps.filter((s) => s.done).length, n: steps.length })}>
          <div className="grid divide-y divide-border md:grid-cols-4 md:divide-x md:divide-y-0">
            {steps.map((s, i) => (
              <Link key={s.title} to={s.to} className="group flex gap-3 p-4 transition-colors hover:bg-surface-2/50">
                <span
                  className={cn(
                    "mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full border text-2xs font-semibold",
                    s.done ? "border-transparent bg-inverse text-inverse-fg" : "border-border-strong text-subtle",
                  )}
                >
                  {s.done ? <Check className="size-3" /> : i + 1}
                </span>
                <div className="min-w-0">
                  <div className={cn("flex items-center gap-1 text-[13px] font-medium", s.done && "text-subtle line-through decoration-faint")}>
                    {s.title}
                    {!s.done && <ArrowRight className="size-3.5 text-faint opacity-0 transition-opacity group-hover:opacity-100" />}
                  </div>
                  <p className="mt-0.5 text-xs leading-relaxed text-subtle">{s.text}</p>
                </div>
              </Link>
            ))}
          </div>
        </Card>
      )}

      <div className="grid grid-cols-2 gap-4 xl:grid-cols-4">
        <Stat
          label={t("Applications")}
          icon={<Box />}
          value={d.counts.applications}
          sub={`${t("{n} resource running|{n} resources running", { n: d.counts.running })} · ${t("{n} database|{n} databases", { n: d.counts.databases })} · ${t("{n} service|{n} services", { n: d.counts.services })}`}
        />
        <Stat
          label={t("Deployments · 14 days")}
          icon={<Rocket />}
          value={stats.total}
          sub={`${t("{n} today", { n: d.counts.deployments_today })}${d.counts.failed_today ? ` · ${t("{n} failed", { n: d.counts.failed_today })}` : ""}`}
        />
        <Stat
          label={t("Success rate · 14 days")}
          icon={<CircleCheck />}
          value={stats.rate === null ? "—" : `${stats.rate}%`}
          sub={stats.rate === null ? t("No completed deployments yet") : stats.rate >= 95 ? t("Healthy delivery pipeline") : t("Review failing deployments")}
        />
        <Stat label={t("Median build time")} icon={<Timer />} value={fmtSeconds(stats.median)} sub={t("Successful deployments, 14 days")} />
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2" title={t("Deployment activity")} description={t("Completed deployments per day, last 14 days")}>
          <div className="p-4 pt-3">{deps.isLoading ? <div className="skeleton h-[240px]" /> : <DeployActivityChart deployments={deps.data ?? []} />}</div>
        </Card>
        <Card
          title={t("Infrastructure")}
          description={t("{online} of {n} server online|{online} of {n} servers online", { online, n: d.servers.length })}
          actions={
            <Link to="/servers">
              <Button size="sm" variant="ghost" trailing={<ArrowRight />}>
                {t("Servers")}
              </Button>
            </Link>
          }
        >
          <Table>
            <tbody>
              {d.servers.map((s) => (
                <tr key={s.id} className="!border-t-0 [&+tr]:!border-t">
                  <td className="!pl-4">
                    <Link to={`/servers/${s.id}`} className="flex items-center gap-3">
                      <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-border bg-surface-2 text-subtle">
                        <ServerIcon className="size-4" />
                      </span>
                      <span className="min-w-0">
                        <span className="block truncate font-medium">{s.name}</span>
                        <span className="block truncate text-xs text-subtle">
                          {s.docker_version ? `Docker ${s.docker_version}` : t("Docker unavailable")} · {timeAgo(s.last_check_at)}
                        </span>
                      </span>
                    </Link>
                  </td>
                  <td className="text-right">
                    <StatusBadge status={s.status} />
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      </div>

      <Card
        className="mt-6"
        title={t("Recent deployments")}
        actions={
          <Link to="/deployments">
            <Button size="sm" variant="ghost" trailing={<ArrowRight />}>
              {t("View all")}
            </Button>
          </Link>
        }
      >
        {d.recent_deployments.length === 0 ? (
          <div className="p-4">
            <Empty
              compact
              icon={<Rocket />}
              title={t("No deployments yet")}
              description={t("Deploy an application to see its build history here.")}
              action={
                <Link to="/projects">
                  <Button variant="primary" icon={<FolderKanban />}>
                    {t("Go to projects")}
                  </Button>
                </Link>
              }
            />
          </div>
        ) : (
          <DeploymentsTable deployments={d.recent_deployments.slice(0, 8)} showApp />
        )}
      </Card>

      {user.role !== "member" && channels.data && channels.data.length === 0 && !onboarding && (
        <div className="mt-6 flex items-center gap-3 rounded-lg border border-border bg-surface px-4 py-3 text-[13px] text-muted">
          <Bell className="size-4 text-faint" />
          {t("No notification channels configured — failures will go unnoticed.")}
          <Link to="/settings?tab=notifications" className="ml-auto font-medium text-fg hover:underline">
            {t("Configure")}
          </Link>
        </div>
      )}
    </>
  );
}
