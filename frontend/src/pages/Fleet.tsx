import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, Container as ContainerIcon, HardDrive, Layers, RefreshCw, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Container, DockerImage, FleetResource, FleetServer, Volume } from "../lib/types";
import { cn } from "../lib/utils";
import { t } from "../lib/i18n";
import { useUser } from "../components/Layout";
import { ContainerTable } from "../components/ContainerTable";
import { Badge, Button, Card, Empty, Input, Loading, PageHeader, Select, StatusDot, Switch, Table, Tabs, useConfirm } from "../components/ui";

type Tab = "containers" | "images" | "volumes";

const resourcePath: Record<string, string> = { application: "applications", database: "databases", service: "services" };

/** Every container, image and volume of every server, in one place. */
export function FleetPage() {
  const user = useUser();
  const admin = user.role !== "member";
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "containers";
  const server = Number(params.get("server")) || 0;
  const setServer = (id: number) => setParams((p) => {
    if (id) p.set("server", String(id));
    else p.delete("server");
    return p;
  });
  return (
    <>
      <PageHeader title={t("Containers")} subtitle={t("The containers, images and volumes of every connected server, with the resource they belong to.")} />
      {admin && (
        <Tabs
          value={tab}
          onChange={(v) => setParams((p) => {
            p.set("tab", v);
            return p;
          })}
          tabs={[
            { id: "containers", label: t("Containers"), icon: <ContainerIcon /> },
            { id: "images", label: t("Images"), icon: <Layers /> },
            { id: "volumes", label: t("Volumes"), icon: <HardDrive /> },
          ]}
        />
      )}
      {tab === "containers" || !admin ? <Containers server={server} setServer={setServer} /> : tab === "images" ? <Images server={server} setServer={setServer} /> : <Volumes server={server} setServer={setServer} />}
    </>
  );
}

function ServerFilter({ servers, value, onChange }: { servers: FleetServer[]; value: number; onChange: (id: number) => void }) {
  return (
    <Select value={value} onChange={(e) => onChange(Number(e.target.value))} className="w-56">
      <option value={0}>{t("All servers")}</option>
      {servers.map((s) => (
        <option key={s.id} value={s.id}>
          {s.name}
        </option>
      ))}
    </Select>
  );
}

/** One card per server: reachable or not, and how many items it has. */
function ServerStrip({ servers, value, onChange, unit }: { servers: FleetServer[]; value: number; onChange: (id: number) => void; unit: (n: number) => string }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {servers.map((s) => (
        <button
          key={s.id}
          onClick={() => onChange(value === s.id ? 0 : s.id)}
          className={cn("rounded-xl border bg-surface px-4 py-3 text-left transition-colors", value === s.id ? "border-fg/40 bg-surface-2" : "border-border hover:bg-surface-2/50")}
        >
          <div className="flex items-center gap-2 text-[13px] font-medium">
            <StatusDot status={s.error ? "failed" : "running"} />
            <span className="truncate">{s.name}</span>
          </div>
          <div className="mt-0.5 truncate text-xs text-subtle">{s.host}</div>
          {s.error ? <div className="mt-1 line-clamp-2 text-xs text-danger" title={s.error}>{s.error}</div> : <div className="mt-1 text-xs text-muted">{unit(s.count)}</div>}
        </button>
      ))}
    </div>
  );
}

interface FleetContainers {
  servers: FleetServer[];
  containers: Container[];
}

function Containers({ server, setServer }: { server: number; setServer: (id: number) => void }) {
  const [stats, setStats] = useState(true);
  const [state, setState] = useState<"all" | "running" | "stopped">("all");
  const [managed, setManaged] = useState(false);
  const [q, setQ] = useState("");
  const data = useQuery({
    queryKey: ["fleet", "containers", stats],
    queryFn: () => api.get<FleetContainers>(`/fleet/containers${stats ? "?stats=1" : ""}`),
    refetchInterval: 15_000,
  });
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (data.data?.containers ?? []).filter(
      (c) =>
        (!server || c.server_id === server) &&
        (state === "all" || (state === "running" ? c.state === "running" : c.state !== "running")) &&
        (!managed || c.resource) &&
        (!needle || `${c.name} ${c.image} ${c.server_name} ${c.resource?.name ?? ""} ${c.resource?.project ?? ""}`.toLowerCase().includes(needle)),
    );
  }, [data.data, server, state, managed, q]);
  if (data.isLoading) return <Loading />;
  const servers = data.data?.servers ?? [];
  const running = list.filter((c) => c.state === "running").length;
  return (
    <div className="space-y-5">
      <ServerStrip servers={servers} value={server} onChange={setServer} unit={(n) => t("{n} container|{n} containers", { n })} />
      <Card
        title={t("{n} container|{n} containers", { n: list.length })}
        description={t("{n} running", { n: running })}
        actions={
          <Button size="icon-sm" variant="ghost" title={t("Refresh")} loading={data.isFetching} onClick={() => data.refetch()}>
            <RefreshCw />
          </Button>
        }
      >
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <div className="relative w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("Name, image, resource…")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <ServerFilter servers={servers} value={server} onChange={setServer} />
          <Select value={state} onChange={(e) => setState(e.target.value as typeof state)} className="w-40">
            <option value="all">{t("All states")}</option>
            <option value="running">{t("Running")}</option>
            <option value="stopped">{t("Stopped")}</option>
          </Select>
          <Switch checked={managed} onChange={setManaged} label={t("Only PortDock resources")} />
          <Switch checked={stats} onChange={setStats} label={t("CPU and memory")} />
        </div>
        <ContainerTable serverId={0} containers={list} showServer />
      </Card>
    </div>
  );
}

interface FleetImage extends DockerImage {
  server_id: number;
  server_name: string;
  in_use: number;
}

function Images({ server, setServer }: { server: number; setServer: (id: number) => void }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [q, setQ] = useState("");
  const [unused, setUnused] = useState(false);
  const data = useQuery({ queryKey: ["fleet", "images"], queryFn: () => api.get<{ servers: FleetServer[]; images: FleetImage[] }>("/fleet/images") });
  const del = useMutation({
    mutationFn: (im: FleetImage) => api.del(`/servers/${im.server_id}/images/${encodeURIComponent(im.id)}`),
    onSuccess: () => {
      toast.success(t("Image removed"));
      qc.invalidateQueries({ queryKey: ["fleet", "images"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (data.data?.images ?? []).filter(
      (im) => (!server || im.server_id === server) && (!unused || im.in_use === 0) && (!needle || `${im.repository}:${im.tag} ${im.id}`.toLowerCase().includes(needle)),
    );
  }, [data.data, server, unused, q]);
  if (data.isLoading) return <Loading />;
  const servers = data.data?.servers ?? [];
  return (
    <div className="space-y-5">
      <ServerStrip servers={servers} value={server} onChange={setServer} unit={(n) => t("{n} image|{n} images", { n })} />
      <Card title={t("{n} image|{n} images", { n: list.length })}>
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <div className="relative w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("Repository, tag…")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <ServerFilter servers={servers} value={server} onChange={setServer} />
          <Switch checked={unused} onChange={setUnused} label={t("Only unused")} />
        </div>
        {list.length === 0 ? (
          <div className="p-5">
            <Empty icon={<Layers />} title={t("No images")} />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Server")}</th>
                <th>{t("Repository")}</th>
                <th>{t("Tag")}</th>
                <th>ID</th>
                <th className="!text-right">{t("Size")}</th>
                <th>{t("Created")}</th>
                <th>{t("Used by")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((im) => (
                <tr key={`${im.server_id}-${im.id}-${im.repository}-${im.tag}`}>
                  <td className="whitespace-nowrap">
                    <Link to={`/servers/${im.server_id}`} className="text-[13px] font-medium hover:underline">
                      {im.server_name}
                    </Link>
                  </td>
                  <td className="max-w-80 truncate font-mono text-xs">{im.repository}</td>
                  <td className="font-mono text-xs text-muted">{im.tag}</td>
                  <td className="font-mono text-xs text-faint">{im.id.replace("sha256:", "").slice(0, 12)}</td>
                  <td className="whitespace-nowrap text-right text-xs tabular-nums">{im.size}</td>
                  <td className="whitespace-nowrap text-xs text-muted">{im.created_at}</td>
                  <td className="text-xs">{im.in_use ? <Badge tone="success">{t("{n} container|{n} containers", { n: im.in_use })}</Badge> : <span className="text-faint">{t("unused")}</span>}</td>
                  <td className="text-right">
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title={t("Remove")}
                      onClick={async () => {
                        if (await confirm({ title: t("Remove {name}?", { name: `${im.repository}:${im.tag}` }), description: t("From {server}. Images used by containers can't be removed.", { server: im.server_name }), danger: true, confirmText: t("Remove") }))
                          del.mutate(im);
                      }}
                    >
                      <Trash2 className="text-danger" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  );
}

interface FleetVolume extends Volume {
  server_id: number;
  server_name: string;
  resource: FleetResource | null;
}

function Volumes({ server, setServer }: { server: number; setServer: (id: number) => void }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [q, setQ] = useState("");
  const data = useQuery({ queryKey: ["fleet", "volumes"], queryFn: () => api.get<{ servers: FleetServer[]; volumes: FleetVolume[] }>("/fleet/volumes") });
  const del = useMutation({
    mutationFn: (v: FleetVolume) => api.del(`/servers/${v.server_id}/volumes/${encodeURIComponent(v.name)}`),
    onSuccess: () => {
      toast.success(t("Volume removed"));
      qc.invalidateQueries({ queryKey: ["fleet", "volumes"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (data.data?.volumes ?? []).filter((v) => (!server || v.server_id === server) && (!needle || `${v.name} ${v.resource?.name ?? ""}`.toLowerCase().includes(needle)));
  }, [data.data, server, q]);
  if (data.isLoading) return <Loading />;
  const servers = data.data?.servers ?? [];
  return (
    <div className="space-y-5">
      <ServerStrip servers={servers} value={server} onChange={setServer} unit={(n) => t("{n} volume|{n} volumes", { n })} />
      <Card title={t("{n} volume|{n} volumes", { n: list.length })}>
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <div className="relative w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("Name, resource…")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <ServerFilter servers={servers} value={server} onChange={setServer} />
        </div>
        {list.length === 0 ? (
          <div className="p-5">
            <Empty icon={<Boxes />} title={t("No volumes")} />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Server")}</th>
                <th>{t("Name")}</th>
                <th>{t("Resource")}</th>
                <th>{t("Driver")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((v) => (
                <tr key={`${v.server_id}-${v.name}`}>
                  <td className="whitespace-nowrap">
                    <Link to={`/servers/${v.server_id}`} className="text-[13px] font-medium hover:underline">
                      {v.server_name}
                    </Link>
                  </td>
                  <td className="max-w-96 truncate font-mono text-xs">{v.name}</td>
                  <td className="text-xs">
                    {v.resource ? (
                      <Link to={`/${resourcePath[v.resource.kind]}/${v.resource.id}`} className="hover:underline">
                        {v.resource.project} / {v.resource.name}
                      </Link>
                    ) : (
                      <span className="text-faint">—</span>
                    )}
                  </td>
                  <td className="text-xs text-muted">{v.driver}</td>
                  <td className="text-right">
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title={t("Remove")}
                      onClick={async () => {
                        if (await confirm({ title: t("Remove volume {name}?", { name: v.name }), description: t("Its data is deleted from {server} and can't be recovered.", { server: v.server_name }), danger: true, confirmText: t("Remove"), typeToConfirm: v.name }))
                          del.mutate(v);
                      }}
                    >
                      <Trash2 className="text-danger" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  );
}
