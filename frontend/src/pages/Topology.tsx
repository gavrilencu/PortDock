import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, Globe, Network, Play, RotateCw, Search, Server as ServerIcon, Square, SquareTerminal, X } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Project } from "../lib/types";
import { cn, formatUptime, timeAgo } from "../lib/utils";
import { ContainerLogs } from "../components/ContainerTable";
import { Terminal } from "../components/Terminal";
import { ResourceIcon } from "../components/shared";
import { Badge, Button, Empty, Input, Loading, PageHeader, Select, Tabs, useConfirm } from "../components/ui";
import { intlLocale, t, tk } from "../lib/i18n";

export type Phase = "healthy" | "running" | "starting" | "unhealthy" | "crashloop" | "restarting" | "failed" | "exited" | "paused" | "created";
export type TopoContainer = {
  id: string;
  name: string;
  service?: string;
  image: string;
  state: string;
  health?: string;
  phase: Phase;
  restart_count: number;
  exit_code: number;
  oom_killed: boolean;
  error?: string;
  started_at: string;
  finished_at?: string;
  created_at: string;
  ports: string[];
  networks: Record<string, string>;
  pr?: string;
  deployment?: string;
  server_id: number;
  server_name: string;
};
export type TopoResource = {
  kind: "application" | "database" | "service";
  id: number;
  uuid: string;
  name: string;
  status: string;
  health: "healthy" | "degraded" | "down" | "progressing" | "stopped";
  project: string;
  project_id: number;
  environment: string;
  environment_id: number;
  image: string;
  domains: string[];
  servers: { id: number; name: string; host: string; status: string }[];
  containers: TopoContainer[];
  can_write: boolean;
};

// Colours per phase / health (ArgoCD-like: green healthy, red broken, amber transitional, grey idle).
const phaseStyle: Record<Phase, { dot: string; ring: string; label: string; text: string }> = {
  healthy: { dot: "bg-success", ring: "border-success/50", label: tk("Healthy"), text: "text-success" },
  running: { dot: "bg-success", ring: "border-success/40", label: tk("Running"), text: "text-success" },
  starting: { dot: "bg-info", ring: "border-info/50", label: tk("Starting"), text: "text-info" },
  unhealthy: { dot: "bg-warning", ring: "border-warning/60", label: tk("Unhealthy"), text: "text-warning" },
  crashloop: { dot: "bg-danger", ring: "border-danger/70", label: tk("CrashLoop"), text: "text-danger" },
  restarting: { dot: "bg-warning", ring: "border-warning/60", label: tk("Restarting"), text: "text-warning" },
  failed: { dot: "bg-danger", ring: "border-danger/60", label: tk("Failed"), text: "text-danger" },
  exited: { dot: "bg-faint", ring: "border-border", label: tk("Exited"), text: "text-muted" },
  paused: { dot: "bg-warning", ring: "border-warning/50", label: tk("Paused"), text: "text-warning" },
  created: { dot: "bg-faint", ring: "border-border", label: tk("Created"), text: "text-muted" },
};
const healthStyle: Record<TopoResource["health"], { bar: string; label: string; tone: "success" | "warning" | "danger" | "info" | "neutral"; edge: string }> = {
  healthy: { bar: "bg-success", label: tk("Healthy"), tone: "success", edge: "var(--success)" },
  degraded: { bar: "bg-warning", label: tk("Degraded"), tone: "warning", edge: "var(--warning)" },
  down: { bar: "bg-danger", label: tk("Down"), tone: "danger", edge: "var(--danger)" },
  progressing: { bar: "bg-info", label: tk("Progressing"), tone: "info", edge: "var(--info)" },
  stopped: { bar: "bg-faint", label: tk("Stopped"), tone: "neutral", edge: "var(--border-strong)" },
};
const phaseEdge = (p: Phase) =>
  ({ healthy: "var(--success)", running: "var(--success)", starting: "var(--info)", unhealthy: "var(--warning)", crashloop: "var(--danger)", restarting: "var(--warning)", failed: "var(--danger)", paused: "var(--warning)" })[
    p as string
  ] ?? "var(--border-strong)";

export function uptime(c: TopoContainer) {
  const since = c.state === "running" ? c.started_at : c.finished_at;
  if (!since || since.startsWith("0001")) return "";
  if (c.state !== "running") return timeAgo(since);
  return t("up {time}", { time: formatUptime(Math.max(0, (Date.now() - new Date(since).getTime()) / 1000)) });
}

// ---------- layout ----------

const ROW = 64;
const NODE_H = 52;
const GAP = 28;
const W = { domain: 220, resource: 260, server: 180, container: 290 };

type Layout = {
  height: number;
  width: number;
  x: { domain: number; resource: number; server: number; container: number };
  blocks: {
    r: TopoResource;
    top: number;
    rows: number;
    resourceY: number;
    domains: { host: string; y: number }[];
    servers: { id: number; name: string; host: string; y: number; containers: { c: TopoContainer; y: number }[] }[];
  }[];
};

function layout(resources: TopoResource[]): Layout {
  const hasDomains = resources.some((r) => r.domains.length > 0);
  const x = { domain: 0, resource: hasDomains ? W.domain + 40 : 0, server: 0, container: 0 };
  x.server = x.resource + W.resource + 50;
  x.container = x.server + W.server + 50;
  let top = 0;
  const blocks: Layout["blocks"] = [];
  for (const r of resources) {
    // Group containers by server, keeping servers without containers visible.
    const groups = r.servers.map((s) => ({ ...s, list: r.containers.filter((c) => c.server_id === s.id) }));
    for (const c of r.containers) if (!groups.some((g) => g.id === c.server_id)) groups.push({ id: c.server_id, name: c.server_name, host: "", status: "", list: [c] });
    const containerRows = groups.reduce((n, g) => n + Math.max(g.list.length, 1), 0);
    const rows = Math.max(containerRows, r.domains.length, 1);
    const blockH = rows * ROW;
    let y = top + (blockH - containerRows * ROW) / 2;
    const servers = groups.map((g) => {
      const n = Math.max(g.list.length, 1);
      const s = { id: g.id, name: g.name, host: g.host, y: y + (n * ROW - NODE_H) / 2, containers: g.list.map((c, i) => ({ c, y: y + i * ROW + (ROW - NODE_H) / 2 })) };
      y += n * ROW;
      return s;
    });
    const dTop = top + (blockH - r.domains.length * ROW) / 2;
    blocks.push({
      r,
      top,
      rows,
      resourceY: top + (blockH - NODE_H) / 2,
      domains: r.domains.map((h, i) => ({ host: h, y: dTop + i * ROW + (ROW - NODE_H) / 2 })),
      servers,
    });
    top += blockH + GAP;
  }
  return { height: Math.max(top - GAP, NODE_H), width: x.container + W.container, x, blocks };
}

function edge(x1: number, y1: number, x2: number, y2: number) {
  const mx = (x1 + x2) / 2;
  return `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`;
}

// ---------- graph ----------

export function TopologyGraph({ resources, selected, onSelect }: { resources: TopoResource[]; selected?: string; onSelect: (r: TopoResource, c: TopoContainer) => void }) {
  const L = useMemo(() => layout(resources), [resources]);
  const mid = NODE_H / 2;
  return (
    <div className="overflow-x-auto rounded-xl border border-border bg-canvas p-6">
      <div className="relative" style={{ width: L.width, height: L.height }}>
        <svg className="pointer-events-none absolute inset-0" width={L.width} height={L.height}>
          {L.blocks.map((b) => (
            <g key={b.r.kind + b.r.id}>
              {b.domains.map((d) => (
                <path key={d.host} d={edge(L.x.domain + W.domain, d.y + mid, L.x.resource, b.resourceY + mid)} fill="none" stroke={healthStyle[b.r.health].edge} strokeWidth={1.5} strokeOpacity={0.6} />
              ))}
              {b.servers.map((s) => (
                <g key={s.id}>
                  <path d={edge(L.x.resource + W.resource, b.resourceY + mid, L.x.server, s.y + mid)} fill="none" stroke="var(--border-strong)" strokeWidth={1.5} />
                  {s.containers.map(({ c, y }) => (
                    <path
                      key={c.id}
                      d={edge(L.x.server + W.server, s.y + mid, L.x.container, y + mid)}
                      fill="none"
                      stroke={phaseEdge(c.phase)}
                      strokeWidth={1.5}
                      strokeOpacity={0.75}
                      strokeDasharray={c.phase === "crashloop" || c.phase === "restarting" ? "5 4" : undefined}
                      className={c.phase === "crashloop" ? "topo-flow" : undefined}
                    />
                  ))}
                </g>
              ))}
            </g>
          ))}
        </svg>

        {L.blocks.map((b) => {
          const hs = healthStyle[b.r.health];
          const to = `/${b.r.kind === "application" ? "applications" : b.r.kind === "database" ? "databases" : "services"}/${b.r.id}`;
          return (
            <div key={b.r.kind + b.r.id}>
              {b.domains.map((d) => (
                <a
                  key={d.host}
                  href={d.host}
                  target="_blank"
                  rel="noreferrer"
                  className="absolute flex items-center gap-2 rounded-lg border border-border bg-surface px-3 text-[12px] shadow-xs transition-colors hover:border-border-strong"
                  style={{ left: L.x.domain, top: d.y, width: W.domain, height: NODE_H }}
                  title={d.host}
                >
                  <Globe className="size-4 shrink-0 text-faint" />
                  <span className="truncate">{d.host.replace(/^https?:\/\//, "")}</span>
                </a>
              ))}
              <Link
                to={to}
                className="absolute flex items-center gap-3 overflow-hidden rounded-lg border border-border bg-surface pl-4 pr-3 shadow-card transition-colors hover:border-border-strong"
                style={{ left: L.x.resource, top: b.resourceY, width: W.resource, height: NODE_H }}
              >
                <span className={cn("absolute inset-y-0 left-0 w-1", hs.bar)} />
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-border bg-surface-2 text-muted [&_svg]:size-4">
                  <ResourceIcon kind={b.r.kind} />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-semibold">{b.r.name}</span>
                  <span className="block truncate text-2xs text-muted">
                    {b.r.project} · {b.r.environment}
                  </span>
                </span>
                <Badge tone={hs.tone}>{t(hs.label)}</Badge>
              </Link>
              {b.servers.map((s) => (
                <div
                  key={s.id}
                  className="absolute flex items-center gap-2.5 rounded-lg border border-dashed border-border-strong bg-surface-2/70 px-3"
                  style={{ left: L.x.server, top: s.y, width: W.server, height: NODE_H }}
                >
                  <ServerIcon className="size-4 shrink-0 text-faint" />
                  <span className="min-w-0">
                    <span className="block truncate text-[12px] font-medium">{s.name}</span>
                    <span className="block truncate font-mono text-2xs text-faint">{s.host || t("{n} container|{n} containers", { n: s.containers.length })}</span>
                  </span>
                </div>
              ))}
              {b.servers.map((s) =>
                s.containers.length === 0 ? (
                  <div key={`empty-${s.id}`} className="absolute flex items-center rounded-lg border border-dashed border-border px-3 text-xs text-faint" style={{ left: L.x.container, top: s.y, width: W.container, height: NODE_H }}>
                    {t("No containers — not deployed")}
                  </div>
                ) : (
                  s.containers.map(({ c, y }) => {
                    const ps = phaseStyle[c.phase];
                    return (
                      <button
                        key={c.id}
                        onClick={() => onSelect(b.r, c)}
                        className={cn(
                          "absolute flex items-center gap-2.5 rounded-lg border-2 bg-surface px-3 text-left shadow-xs transition-all hover:shadow-card",
                          ps.ring,
                          selected === c.id && "ring-2 ring-[var(--ring)]",
                        )}
                        style={{ left: L.x.container, top: y, width: W.container, height: NODE_H }}
                      >
                        <span className="relative flex size-2.5 shrink-0">
                          {(c.phase === "crashloop" || c.phase === "restarting" || c.phase === "starting") && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60", ps.dot)} />}
                          <span className={cn("relative inline-flex size-2.5 rounded-full", ps.dot)} />
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-mono text-[11px] font-medium">{c.service || c.name}</span>
                          <span className="flex items-center gap-1.5 text-2xs">
                            <span className={cn("font-medium", ps.text)}>{t(ps.label)}</span>
                            <span className="truncate text-faint">{uptime(c)}</span>
                            {c.pr && c.pr !== "0" && <span className="text-info">PR #{c.pr}</span>}
                          </span>
                        </span>
                        {c.restart_count > 0 && (
                          <span className={cn("rounded px-1.5 text-2xs font-semibold tabular-nums", c.restart_count >= 3 ? "bg-danger-soft text-danger" : "bg-surface-2 text-muted")} title={t("Restarts")}>
                            ↻{c.restart_count}
                          </span>
                        )}
                      </button>
                    );
                  })
                ),
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ---------- container drawer ----------

type EnvItem = { key: string; value: string; secret: boolean; masked: boolean };

function EnvTab({ c }: { c: TopoContainer }) {
  const q = useQuery({ queryKey: ["container-env", c.server_id, c.id], queryFn: () => api.get<{ env: EnvItem[]; revealed: boolean }>(`/servers/${c.server_id}/containers/${c.id}/env`) });
  const [show, setShow] = useState<Record<string, boolean>>({});
  const [filter, setFilter] = useState("");
  if (q.isLoading) return <Loading rows={4} />;
  if (q.error) return <p className="text-xs text-danger">{(q.error as Error).message}</p>;
  const items = (q.data?.env ?? []).filter((e) => e.key.toLowerCase().includes(filter.toLowerCase()));
  return (
    <div className="space-y-3">
      {!q.data?.revealed && <p className="rounded-md border border-border bg-surface-2/60 px-3 py-2 text-xs text-muted">{t("Values are hidden: your role can view this resource but not its configuration.")}</p>}
      <Input placeholder={t("Filter variables…")} value={filter} onChange={(e) => setFilter(e.target.value)} />
      <div className="divide-y divide-border rounded-lg border border-border">
        {items.map((e) => {
          const hidden = e.masked || (e.secret && !show[e.key]);
          return (
            <div key={e.key} className="flex items-start gap-2 px-3 py-2">
              <code className="w-44 shrink-0 break-all pt-0.5 font-mono text-[11px] font-semibold">{e.key}</code>
              <code className="min-w-0 flex-1 break-all pt-0.5 font-mono text-[11px] text-muted">{e.masked ? "••••••••" : hidden ? "••••••••••••" : e.value || <span className="text-faint">{t("(empty)")}</span>}</code>
              {e.secret && !e.masked && (
                <button className="text-faint hover:text-fg" onClick={() => setShow({ ...show, [e.key]: !show[e.key] })} title={hidden ? t("Reveal") : t("Hide")}>
                  {hidden ? <Eye className="size-3.5" /> : <EyeOff className="size-3.5" />}
                </button>
              )}
            </div>
          );
        })}
        {items.length === 0 && <div className="px-3 py-4 text-center text-xs text-faint">{t("No variables")}</div>}
      </div>
    </div>
  );
}

function Row({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="flex gap-3 py-1.5 text-[12px]">
      <span className="w-32 shrink-0 text-muted">{k}</span>
      <span className="min-w-0 flex-1 break-all">{v}</span>
    </div>
  );
}

export function ContainerDrawer({ r, c, onClose }: { r: TopoResource; c: TopoContainer; onClose: () => void }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [tab, setTab] = useState<"summary" | "logs" | "env" | "terminal">(c.phase === "crashloop" || c.phase === "failed" ? "logs" : "summary");
  const ps = phaseStyle[c.phase];
  const action = useMutation({
    mutationFn: (a: string) => api.post(`/servers/${c.server_id}/containers/${encodeURIComponent(c.id)}/${a}`),
    onSuccess: (_, a) => {
      toast.success(a === "stop" ? t("Container stopped") : a === "start" ? t("Container started") : t("Container restarted"));
      qc.invalidateQueries({ queryKey: ["topology"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const running = c.state === "running";
  return (
    <div className="animate-fade fixed inset-y-0 right-0 z-40 flex w-full max-w-[620px] flex-col border-l border-border bg-surface shadow-pop">
      <div className="flex items-start gap-3 border-b border-border px-5 py-4">
        <span className={cn("mt-1.5 inline-flex size-2.5 shrink-0 rounded-full", ps.dot)} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[13px] font-semibold">{c.name}</div>
          <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted">
            <span className={cn("font-medium", ps.text)}>{t(ps.label)}</span>
            <span>·</span>
            <span>{r.name}</span>
            <span>·</span>
            <span>{c.server_name}</span>
            {c.restart_count > 0 && <Badge tone={c.restart_count >= 3 ? "danger" : "neutral"}>{t("{n} restart|{n} restarts", { n: c.restart_count })}</Badge>}
          </div>
        </div>
        <Button size="icon-sm" variant="ghost" onClick={onClose} aria-label={t("Close")}>
          <X />
        </Button>
      </div>
      {r.can_write && (
        <div className="flex gap-2 border-b border-border px-5 py-2.5">
          <Button size="sm" icon={<RotateCw />} loading={action.isPending && action.variables === "restart"} onClick={() => action.mutate("restart")}>
            {t("Restart")}
          </Button>
          {running ? (
            <Button size="sm" icon={<Square />} loading={action.isPending && action.variables === "stop"} onClick={async () => (await confirm({ title: t("Stop {name}?", { name: c.name }), confirmText: t("Stop") })) && action.mutate("stop")}>
              {t("Stop")}
            </Button>
          ) : (
            <Button size="sm" icon={<Play />} loading={action.isPending && action.variables === "start"} onClick={() => action.mutate("start")}>
              {t("Start")}
            </Button>
          )}
        </div>
      )}
      <div className="px-5 pt-3">
        <Tabs
          value={tab}
          onChange={setTab}
          tabs={[
            { id: "summary", label: t("Summary") },
            { id: "logs", label: t("Logs") },
            { id: "env", label: t("Env") },
            { id: "terminal", label: t("Terminal"), icon: <SquareTerminal />, hidden: !r.can_write || !running },
          ]}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-5">
        {tab === "summary" && (
          <div className="divide-y divide-border">
            {(c.phase === "crashloop" || c.phase === "failed" || c.oom_killed || c.error) && (
              <div className="mb-3 rounded-lg border border-danger/30 bg-danger-soft px-3 py-2.5 text-xs text-danger">
                {c.oom_killed
                  ? t("Killed because it ran out of memory (OOM). Raise the memory limit.")
                  : c.error ||
                    (c.restart_count
                      ? t("The process exited with code {code} and was restarted {n} time. Check the logs.|The process exited with code {code} and was restarted {n} times. Check the logs.", { code: c.exit_code, n: c.restart_count })
                      : t("The process exited with code {code}. Check the logs.", { code: c.exit_code }))}
              </div>
            )}
            <Row k={t("Image")} v={<code className="font-mono text-[11px]">{c.image}</code>} />
            <Row k={t("State")} v={`${c.state}${c.health ? ` · ${t("health: {health}", { health: c.health })}` : ""}`} />
            <Row k={t("Exit code")} v={c.state === "running" ? "—" : c.exit_code} />
            <Row k={t("Restarts")} v={c.restart_count} />
            <Row k={running ? t("Started") : t("Finished")} v={running ? new Date(c.started_at).toLocaleString(intlLocale()) : c.finished_at ? new Date(c.finished_at).toLocaleString(intlLocale()) : "—"} />
            <Row k={t("Created")} v={new Date(c.created_at).toLocaleString(intlLocale())} />
            <Row k={t("Ports")} v={c.ports.length ? c.ports.join(", ") : "—"} />
            <Row
              k={t("Networks")}
              v={Object.entries(c.networks).map(([n, ip]) => (
                <div key={n} className="flex items-center gap-1.5">
                  <Network className="size-3 text-faint" /> {n} <span className="font-mono text-faint">{ip}</span>
                </div>
              ))}
            />
            {c.service && <Row k={t("Compose service")} v={c.service} />}
            {c.deployment && <Row k={t("Deployment")} v={<code className="font-mono text-[11px]">{c.deployment}</code>} />}
            <Row k={t("Container ID")} v={<code className="font-mono text-[11px]">{c.id.slice(0, 12)}</code>} />
          </div>
        )}
        {tab === "logs" && <ContainerLogs key={c.id} url={`/servers/${c.server_id}/containers/${encodeURIComponent(c.id)}/logs?tail=500`} className="h-[calc(100vh-260px)]" />}
        {tab === "env" && <EnvTab c={c} />}
        {tab === "terminal" && <Terminal key={c.id} path={`/servers/${c.server_id}/containers/${encodeURIComponent(c.id)}/terminal`} />}
      </div>
    </div>
  );
}

// ---------- page ----------

export function useTopology(params: Record<string, string | number | undefined>) {
  const qs = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== undefined && v !== "" && v !== 0) as [string, string][]).toString();
  return useQuery({
    queryKey: ["topology", qs],
    queryFn: () => api.get<{ resources: TopoResource[]; errors: Record<string, string>; generated_at: string }>(`/topology${qs ? `?${qs}` : ""}`),
    refetchInterval: 5000,
  });
}

export function TopologyPage() {
  const [params, setParams] = useSearchParams();
  const project = Number(params.get("project")) || 0;
  const environment = Number(params.get("environment")) || 0;
  const [q, setQ] = useState("");
  const [health, setHealth] = useState<string>("");
  const [sel, setSel] = useState<{ r: TopoResource; c: TopoContainer } | null>(null);
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
  const topo = useTopology({ project, environment });
  const all = topo.data?.resources ?? [];
  const counts = all.reduce<Record<string, number>>((m, r) => ((m[r.health] = (m[r.health] ?? 0) + 1), m), {});
  const crash = all.flatMap((r) => r.containers).filter((c) => c.phase === "crashloop" || c.phase === "failed").length;
  const list = all.filter((r) => (!health || r.health === health) && `${r.name} ${r.project} ${r.environment} ${r.containers.map((c) => c.name).join(" ")}`.toLowerCase().includes(q.toLowerCase()));
  const envs = projects.data?.find((p) => p.id === project)?.environments ?? [];
  // Keep the drawer in sync with live data.
  const live = sel && all.find((r) => r.kind === sel.r.kind && r.id === sel.r.id);
  const liveC = live?.containers.find((c) => c.id === sel?.c.id);

  const set = (k: string, v: number) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, String(v));
    else next.delete(k);
    if (k === "project") next.delete("environment");
    setParams(next);
  };

  return (
    <>
      <PageHeader
        title={t("Topology")}
        subtitle={t("Live map of every resource, the servers it runs on and its containers. Click a container for logs, environment and actions.")}
        meta={
          crash > 0 ? (
            <Badge tone="danger">{t("{n} container crashing|{n} containers crashing", { n: crash })}</Badge>
          ) : topo.data ? (
            <span className="flex items-center gap-1.5 text-xs text-muted">
              <span className="pulse-dot relative inline-flex size-2 rounded-full bg-success" /> {t("Live · refreshed {time}", { time: timeAgo(topo.data.generated_at) })}
            </span>
          ) : null
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="relative w-64">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
          <Input className="pl-8" placeholder={t("Filter resources or containers…")} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Select className="w-48" value={project} onChange={(e) => set("project", Number(e.target.value))}>
          <option value={0}>{t("All projects")}</option>
          {projects.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
        {project > 0 && (
          <Select className="w-44" value={environment} onChange={(e) => set("environment", Number(e.target.value))}>
            <option value={0}>{t("All environments")}</option>
            {envs.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name}
              </option>
            ))}
          </Select>
        )}
        <div className="ml-auto flex flex-wrap gap-1.5">
          {(["healthy", "degraded", "down", "progressing", "stopped"] as const).map((h) => (
            <button
              key={h}
              onClick={() => setHealth(health === h ? "" : h)}
              className={cn("flex h-7 items-center gap-1.5 rounded-md border px-2.5 text-xs transition-colors", health === h ? "border-border-strong bg-surface-2" : "border-border hover:bg-surface-2/60")}
            >
              <span className={cn("size-2 rounded-full", healthStyle[h].bar)} />
              {t(healthStyle[h].label)}
              <span className="tabular-nums text-muted">{counts[h] ?? 0}</span>
            </button>
          ))}
        </div>
      </div>
      {Object.keys(topo.data?.errors ?? {}).length > 0 && (
        <div className="mb-4 rounded-lg border border-warning/30 bg-warning-soft px-4 py-2.5 text-xs text-warning">
          {Object.entries(topo.data!.errors).map(([s, e]) => (
            <div key={s}>
              {s}: {e}
            </div>
          ))}
        </div>
      )}
      {topo.isLoading ? (
        <Loading />
      ) : list.length === 0 ? (
        <Empty icon={<Network />} title={t("Nothing to show")} description={all.length ? t("No resource matches the filters.") : t("Deploy an application, database or service to see it here.")} />
      ) : (
        <TopologyGraph resources={list} selected={sel?.c.id} onSelect={(r, c) => setSel({ r, c })} />
      )}
      <div className="mt-3 flex flex-wrap gap-3 text-2xs text-muted">
        {(["healthy", "running", "starting", "unhealthy", "restarting", "crashloop", "failed", "exited"] as Phase[]).map((p) => (
          <span key={p} className="flex items-center gap-1.5">
            <span className={cn("size-2 rounded-full", phaseStyle[p].dot)} /> {t(phaseStyle[p].label)}
          </span>
        ))}
      </div>
      {sel && <ContainerDrawer key={sel.c.id} r={live ?? sel.r} c={liveC ?? sel.c} onClose={() => setSel(null)} />}
    </>
  );
}
