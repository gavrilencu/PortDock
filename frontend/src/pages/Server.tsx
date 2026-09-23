import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Boxes,
  Cpu,
  Download,
  HardDrive,
  Layers,
  LayoutGrid,
  MemoryStick,
  Network,
  Play,
  RefreshCw,
  RotateCw,
  Save,
  Server as ServerIcon,
  Settings2,
  ShieldCheck,
  Square,
  SquareTerminal,
  Trash2,
  Wrench,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Container, ContainerStat, DockerImage, DockerNetwork, Metrics, Server, SSHKey, Volume } from "../lib/types";
import { useLogStream } from "../lib/sse";
import { AdminOnly, useUser } from "../components/Layout";
import { Badge, Button, Card, ErrorBox, Field, Input, Loading, Meter, PageHeader, Select, Stat, StatusBadge, Switch, Table, Tabs, useConfirm } from "../components/ui";
import { ContainerTable } from "../components/ContainerTable";
import { LogViewer } from "../components/LogViewer";
import { Terminal } from "../components/Terminal";
import { formatBytes, formatUptime, timeAgo } from "../lib/utils";
import { MetricsPanel } from "../components/metrics";
import { t } from "../lib/i18n";

type Tab = "overview" | "monitoring" | "containers" | "images" | "volumes" | "networks" | "terminal" | "settings";

export function ServerPage() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "overview";
  const user = useUser();
  const isAdmin = user.role !== "member";
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({ queryKey: ["server", id], queryFn: () => api.get<Server>(`/servers/${id}`) });
  const validate = useMutation({
    mutationFn: () => api.post<Server>(`/servers/${id}/validate`),
    onSuccess: (s) => {
      qc.setQueryData(["server", id], s);
      qc.invalidateQueries({ queryKey: ["servers"] });
      s.status === "reachable" ? toast.success(t("Server is reachable")) : toast.error(s.status_message || t("Server unreachable"));
    },
  });

  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  const s = data!;

  return (
    <>
      <PageHeader
        icon={<ServerIcon className="size-5" />}
        title={
          <span className="flex items-center gap-3">
            {s.name} <StatusBadge status={s.status} />
          </span>
        }
        subtitle={s.is_local ? t("The machine running PortDock") : `${s.user}@${s.host}:${s.port}`}
        breadcrumbs={
          <>
            <Link to="/servers" className="hover:text-fg">{t("Servers")}</Link> / <span className="text-fg">{s.name}</span>
          </>
        }
        actions={
          <Button icon={<RefreshCw className="size-4" />} loading={validate.isPending} onClick={() => validate.mutate()}>
            {t("Validate")}
          </Button>
        }
      />
      <Tabs
        value={tab}
        onChange={(v) => setParams({ tab: v })}
        tabs={[
          { id: "overview", label: t("Overview"), icon: <LayoutGrid /> },
          { id: "monitoring", label: t("Monitoring"), icon: <Activity /> },
          { id: "containers", label: t("Containers"), icon: <Boxes /> },
          { id: "images", label: t("Images"), icon: <Layers /> },
          { id: "volumes", label: t("Volumes"), icon: <HardDrive /> },
          { id: "networks", label: t("Networks"), icon: <Network /> },
          { id: "terminal", label: t("Terminal"), icon: <SquareTerminal />, hidden: !isAdmin },
          { id: "settings", label: t("Settings"), icon: <Settings2 />, hidden: !isAdmin },
        ]}
      />
      {s.status === "unreachable" && tab !== "settings" && (
        <div className="mb-6">
          <ErrorBox error={s.status_message || t("Server is unreachable")} />
        </div>
      )}
      {tab === "overview" && <Overview s={s} />}
      {tab === "monitoring" && <MetricsPanel scope="server" id={s.id} title={t("Server metrics")} />}
      {tab === "containers" && <Containers s={s} />}
      {tab === "images" && <Images s={s} />}
      {tab === "volumes" && <Volumes s={s} />}
      {tab === "networks" && <Networks s={s} />}
      {tab === "terminal" && (
        <div className="space-y-3">
          {s.is_local && <p className="text-sm text-muted">{t("For the local server this opens a shell inside the PortDock container (the Docker CLI there controls the host daemon).")}</p>}
          <Terminal path={`/servers/${id}/terminal`} />
        </div>
      )}
      {tab === "settings" && <ServerSettings s={s} />}
    </>
  );
}

function Overview({ s }: { s: Server }) {
  const qc = useQueryClient();
  const metrics = useQuery({ queryKey: ["metrics", s.id], queryFn: () => api.get<Metrics>(`/servers/${s.id}/metrics`), refetchInterval: 10_000, enabled: s.status === "reachable", retry: false });
  const [setupKey, setSetupKey] = useState(0);
  const [nixpacks, setNixpacks] = useState(false);
  const setupStream = useLogStream(setupKey ? `/servers/${s.id}/setup/stream?k=${setupKey}` : null);
  const setup = useMutation({
    mutationFn: () => api.post(`/servers/${s.id}/setup`, { nixpacks }),
    onError: (e) => toast.error(e.message),
  });
  const proxy = useMutation({
    mutationFn: (a: string) => api.post<{ proxy_status: string }>(`/servers/${s.id}/proxy/${a}`),
    onSuccess: (r) => {
      toast.success(t("Proxy: {status}", { status: r.proxy_status }));
      qc.invalidateQueries({ queryKey: ["server", s.id] });
    },
    onError: (e) => toast.error(e.message),
  });
  useEffect(() => {
    if (setupStream.status) qc.invalidateQueries({ queryKey: ["server", s.id] });
  }, [setupStream.status, qc, s.id]);
  const m = metrics.data;

  return (
    <div className="space-y-6">
      {m && (
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <Stat label={t("CPU load")} icon={<Cpu />} value={m.load1.toFixed(2)} sub={`${t("{n} core|{n} cores", { n: m.cpus })} · ${m.load5.toFixed(2)} / ${m.load15.toFixed(2)}`} />
          <div className="rounded-xl border border-border bg-surface p-4 shadow-card">
            <div className="flex items-center justify-between text-[13px] font-medium text-subtle">
              {t("Memory")} <MemoryStick className="size-4 text-faint" />
            </div>
            <div className="mt-2 text-2xl font-semibold tabular-nums">{formatBytes(m.mem_used)}</div>
            <Meter value={m.mem_used} max={m.mem_total} className="mt-2" />
            <div className="mt-1 text-xs text-muted">{t("of {total}", { total: formatBytes(m.mem_total) })}</div>
          </div>
          <div className="rounded-xl border border-border bg-surface p-4 shadow-card">
            <div className="flex items-center justify-between text-[13px] font-medium text-subtle">
              {t("Disk")} <HardDrive className="size-4 text-faint" />
            </div>
            <div className="mt-2 text-2xl font-semibold tabular-nums">{formatBytes(m.disk_used)}</div>
            <Meter value={m.disk_used} max={m.disk_total} className="mt-2" />
            <div className="mt-1 text-xs text-muted">{t("of {total}", { total: formatBytes(m.disk_total) })}</div>
          </div>
          <Stat label={t("Containers")} icon={<Boxes />} value={`${m.running_containers}/${m.containers}`} sub={t("up {time}", { time: formatUptime(m.uptime) })} />
        </div>
      )}
      <div className="grid gap-6 lg:grid-cols-2">
        <Card title={t("System")}>
          <dl className="space-y-3 p-5 text-sm">
            {[
              [t("Status"), <StatusBadge status={s.status} />],
              ["Docker", s.docker_version || t("not installed")],
              [t("OS"), m?.os ?? "—"],
              [t("Kernel"), m?.kernel ?? "—"],
              [t("Wildcard domain"), s.wildcard_domain || "—"],
              [t("Last check"), timeAgo(s.last_check_at)],
            ].map(([k, v], i) => (
              <div key={i} className="flex justify-between gap-4">
                <dt className="text-muted">{k}</dt>
                <dd className="text-right">{v}</dd>
              </div>
            ))}
          </dl>
        </Card>
        <Card
          title={
            <span className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-subtle" /> {t("Reverse proxy (Traefik)")}
            </span>
          }
          description={t("Routes domains to containers and issues Let's Encrypt certificates.")}
        >
          <div className="space-y-4 p-5">
            <div className="flex items-center justify-between">
              <span className="text-sm text-muted">{t("Status")}</span>
              <StatusBadge status={s.proxy_status === "running" ? "running" : s.proxy_status === "disabled" ? "stopped" : s.proxy_status} />
            </div>
            <AdminOnly>
              <div className="flex flex-wrap gap-2">
                <Button size="sm" icon={<Play className="size-3.5" />} onClick={() => proxy.mutate("start")} loading={proxy.isPending && proxy.variables === "start"}>
                  {t("Start")}
                </Button>
                <Button size="sm" icon={<RotateCw className="size-3.5" />} onClick={() => proxy.mutate("restart")} loading={proxy.isPending && proxy.variables === "restart"}>
                  {t("Recreate")}
                </Button>
                <Button size="sm" variant="danger" icon={<Square className="size-3.5" />} onClick={() => proxy.mutate("stop")}>
                  {t("Stop")}
                </Button>
              </div>
            </AdminOnly>
          </div>
        </Card>
      </div>
      <AdminOnly>
        <Card title={t("Server setup")} description={t("Installs Docker (if missing), prepares directories, the portdock network and the proxy. Safe to re-run.")}>
          <div className="space-y-4 p-5">
            <div className="flex flex-wrap items-center justify-between gap-4">
              <Switch checked={nixpacks} onChange={setNixpacks} label={t("Also install Nixpacks")} description={t("Needed only for the Nixpacks build pack.")} />
              <Button
                variant="primary"
                icon={<Wrench className="size-4" />}
                loading={setup.isPending}
                onClick={() => {
                  setSetupKey((k) => k + 1);
                  setTimeout(() => setup.mutate(), 300);
                }}
              >
                {t("Run setup")}
              </Button>
            </div>
            {setupKey > 0 && <LogViewer lines={setupStream.lines} live={!setupStream.status} className="h-80" />}
          </div>
        </Card>
      </AdminOnly>
    </div>
  );
}

function Containers({ s }: { s: Server }) {
  const [filter, setFilter] = useState<"all" | "managed">("all");
  const cs = useQuery({ queryKey: ["containers", "server", s.id], queryFn: () => api.get<Container[]>(`/servers/${s.id}/containers`), refetchInterval: 10_000, retry: false });
  const stats = useQuery({ queryKey: ["stats", s.id], queryFn: () => api.get<ContainerStat[]>(`/servers/${s.id}/stats`), refetchInterval: 15_000, retry: false });
  const list = useMemo(() => (cs.data ?? []).filter((c) => filter === "all" || c.labels["portdock.managed"] === "true"), [cs.data, filter]);
  if (cs.isLoading) return <Loading />;
  if (cs.error) return <ErrorBox error={cs.error} />;
  return (
    <div className="space-y-6">
      <Card
        title={t("Containers ({n})", { n: list.length })}
        actions={
          <div className="flex gap-1 rounded-lg border border-border p-0.5">
            {(["all", "managed"] as const).map((f) => (
              <button key={f} onClick={() => setFilter(f)} className={`rounded-md px-2.5 py-1 text-xs font-medium capitalize ${filter === f ? "bg-panel-2 text-fg" : "text-muted"}`}>
                {f === "all" ? t("All") : t("Managed")}
              </button>
            ))}
          </div>
        }
      >
        <ContainerTable serverId={s.id} containers={list} />
      </Card>
      {stats.data && stats.data.length > 0 && (
        <Card title={t("Resource usage")}>
          <Table>
            <thead>
              <tr>
                <th>{t("Container")}</th>
                <th>CPU</th>
                <th>{t("Memory")}</th>
                <th>{t("Net I/O")}</th>
                <th>{t("Block I/O")}</th>
                <th>PIDs</th>
              </tr>
            </thead>
            <tbody>
              {stats.data.map((st) => (
                <tr key={st.container}>
                  <td className="font-medium">{st.name}</td>
                  <td className="tabular-nums">{st.cpu}</td>
                  <td className="tabular-nums">
                    {st.mem_usage} <span className="text-muted">({st.mem})</span>
                  </td>
                  <td className="tabular-nums text-muted">{st.net_io}</td>
                  <td className="tabular-nums text-muted">{st.block_io}</td>
                  <td className="tabular-nums text-muted">{st.pids}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </div>
  );
}

function PruneButton({ serverId, scope, label }: { serverId: number; scope: string; label: string }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const prune = useMutation({
    mutationFn: () => api.post<{ output: string }>(`/servers/${serverId}/prune`, { scope }),
    onSuccess: (r) => {
      toast.success(r.output.split("\n").pop() || t("Pruned"));
      qc.invalidateQueries();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <AdminOnly>
      <Button
        size="sm"
        variant="danger"
        icon={<Trash2 className="size-3.5" />}
        loading={prune.isPending}
        onClick={async () => (await confirm({ title: `${label}?`, description: t("Unused Docker data will be removed permanently."), danger: true, confirmText: t("Prune") })) && prune.mutate()}
      >
        {label}
      </Button>
    </AdminOnly>
  );
}

function Images({ s }: { s: Server }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { data, isLoading, error } = useQuery({ queryKey: ["images", s.id], queryFn: () => api.get<DockerImage[]>(`/servers/${s.id}/images`), retry: false });
  const del = useMutation({
    mutationFn: (iid: string) => api.del(`/servers/${s.id}/images/${encodeURIComponent(iid)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["images", s.id] }),
    onError: (e) => toast.error(e.message),
  });
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  return (
    <Card
      title={t("Images ({n})", { n: data!.length })}
      actions={
        <>
          <PruneButton serverId={s.id} scope="builder" label={t("Prune build cache")} />
          <PruneButton serverId={s.id} scope="images" label={t("Prune unused images")} />
        </>
      }
    >
      <Table>
        <thead>
          <tr>
            <th>{t("Repository")}</th>
            <th>{t("Tag")}</th>
            <th>{t("Size")}</th>
            <th>{t("Created")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {data!.map((i) => (
            <tr key={i.id + i.tag}>
              <td className="font-mono text-xs">
                {i.repository} {i.repository.startsWith("portdock/") && <Badge>{t("managed")}</Badge>}
              </td>
              <td className="font-mono text-xs text-muted">{i.tag}</td>
              <td className="tabular-nums text-xs">{i.size}</td>
              <td className="text-xs text-muted">{i.created_at.split(" +")[0]}</td>
              <td className="text-right">
                <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: t("Remove {name}?", { name: `${i.repository}:${i.tag}` }), danger: true, confirmText: t("Remove") })) && del.mutate(i.tag !== "<none>" ? `${i.repository}:${i.tag}` : i.id)}>
                  <Trash2 className="size-4 text-danger" />
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function Volumes({ s }: { s: Server }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { data, isLoading, error } = useQuery({ queryKey: ["volumes", s.id], queryFn: () => api.get<Volume[]>(`/servers/${s.id}/volumes`), retry: false });
  const del = useMutation({
    mutationFn: (name: string) => api.del(`/servers/${s.id}/volumes/${encodeURIComponent(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["volumes", s.id] }),
    onError: (e) => toast.error(e.message),
  });
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  return (
    <Card title={t("Volumes ({n})", { n: data!.length })} actions={<PruneButton serverId={s.id} scope="volumes" label={t("Prune unused volumes")} />}>
      <Table>
        <thead>
          <tr>
            <th>{t("Name")}</th>
            <th>{t("Driver")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {data!.map((v) => (
            <tr key={v.name}>
              <td className="font-mono text-xs">{v.name}</td>
              <td className="text-xs text-muted">{v.driver}</td>
              <td className="text-right">
                <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: t("Delete volume {name}?", { name: v.name }), description: t("All data in the volume is lost."), danger: true, confirmText: t("Delete"), typeToConfirm: v.name })) && del.mutate(v.name)}>
                  <Trash2 className="size-4 text-danger" />
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function Networks({ s }: { s: Server }) {
  const { data, isLoading, error } = useQuery({ queryKey: ["networks", s.id], queryFn: () => api.get<DockerNetwork[]>(`/servers/${s.id}/networks`), retry: false });
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  return (
    <Card title={t("Networks ({n})", { n: data!.length })} actions={<PruneButton serverId={s.id} scope="networks" label={t("Prune unused networks")} />}>
      <Table>
        <thead>
          <tr>
            <th>{t("Name")}</th>
            <th>{t("Driver")}</th>
            <th>{t("Scope")}</th>
            <th>ID</th>
          </tr>
        </thead>
        <tbody>
          {data!.map((n) => (
            <tr key={n.id}>
              <td className="font-medium">
                {n.name} {n.name === "portdock" && <Badge>{t("proxy")}</Badge>}
              </td>
              <td className="text-xs text-muted">{n.driver}</td>
              <td className="text-xs text-muted">{n.scope}</td>
              <td className="font-mono text-xs text-muted">{n.id.slice(0, 12)}</td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function ServerSettings({ s }: { s: Server }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const keys = useQuery({ queryKey: ["ssh-keys"], queryFn: () => api.get<SSHKey[]>("/ssh-keys") });
  const [f, setF] = useState(s);
  useEffect(() => setF(s), [s]);
  const save = useMutation({
    mutationFn: () => api.patch<Server>(`/servers/${s.id}`, f),
    onSuccess: (d) => {
      qc.setQueryData(["server", s.id], d);
      toast.success(t("Server saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const resetKey = useMutation({
    mutationFn: () => api.post(`/servers/${s.id}/reset-host-key`),
    onSuccess: () => toast.success(t("Host key reset — it will be pinned again on next connect")),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/servers/${s.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      nav("/servers");
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="space-y-6">
      <Card title={t("Connection")}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field label={t("Description")}>
            <Input value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} />
          </Field>
          {!s.is_local && (
            <>
              <Field label={t("Host")}>
                <Input value={f.host} onChange={(e) => setF({ ...f, host: e.target.value })} />
              </Field>
              <Field label={t("Port")}>
                <Input type="number" value={f.port} onChange={(e) => setF({ ...f, port: Number(e.target.value) })} />
              </Field>
              <Field label={t("User")}>
                <Input value={f.user} onChange={(e) => setF({ ...f, user: e.target.value })} />
              </Field>
              <Field label={t("SSH key")}>
                <Select value={f.ssh_key_id} onChange={(e) => setF({ ...f, ssh_key_id: Number(e.target.value) })}>
                  {keys.data?.map((k) => (
                    <option key={k.id} value={k.id}>
                      {k.name}
                    </option>
                  ))}
                </Select>
              </Field>
            </>
          )}
          <Field label={t("Wildcard domain")} hint={t("Used for automatic app & preview domains.")}>
            <Input value={f.wildcard_domain} onChange={(e) => setF({ ...f, wildcard_domain: e.target.value })} placeholder="apps.example.com" />
          </Field>
          <div className="flex items-end">
            <Switch checked={f.proxy_enabled} onChange={(v) => setF({ ...f, proxy_enabled: v })} label={t("Manage Traefik proxy")} description={t("Disable if you run your own reverse proxy.")} />
          </div>
          <div className="flex justify-end gap-2 md:col-span-2">
            {!s.is_local && (
              <Button onClick={() => resetKey.mutate()} loading={resetKey.isPending}>
                {t("Reset host key")}
              </Button>
            )}
            <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        </div>
      </Card>
      <Card title={t("Maintenance")} description={t("Reclaim disk space. PortDock also prunes build cache older than 72h daily.")}>
        <div className="flex flex-wrap gap-2 p-5">
          <PruneButton serverId={s.id} scope="containers" label={t("Remove stopped containers")} />
          <PruneButton serverId={s.id} scope="builder" label={t("Prune build cache")} />
          <PruneButton serverId={s.id} scope="all" label={t("System prune (all unused)")} />
          <a href={`/api/servers/${s.id}/metrics`} target="_blank" rel="noreferrer">
            <Button size="sm" variant="ghost" icon={<Download className="size-3.5" />}>
              {t("Raw metrics")}
            </Button>
          </a>
        </div>
      </Card>
      {!s.is_local && (
        <Card title={t("Danger zone")} className="border-danger/30">
          <div className="flex items-center justify-between gap-4 p-5">
            <p className="text-sm text-muted">{t("Remove the server from PortDock. Containers on the server are not touched.")}</p>
            <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={async () => (await confirm({ title: t("Remove {name}?", { name: s.name }), danger: true, confirmText: t("Remove"), typeToConfirm: s.name })) && del.mutate()}>
              {t("Remove server")}
            </Button>
          </div>
        </Card>
      )}
    </div>
  );
}
