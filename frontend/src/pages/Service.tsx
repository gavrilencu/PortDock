import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Boxes, ExternalLink, FileCode, Globe, KeyRound, LayoutGrid, Play, Rocket, RotateCw, Save, Settings2, Square, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Container, ServiceDetail } from "../lib/types";
import { useLogStream } from "../lib/sse";
import { Button, Card, ErrorBox, Field, Input, Loading, PageHeader, StatusBadge, Tabs, Textarea, useConfirm } from "../components/ui";
import { ContainerTable } from "../components/ContainerTable";
import { LogViewer } from "../components/LogViewer";
import { EnvEditor } from "../components/EnvEditor";
import { domainURL, splitList } from "../lib/utils";
import { MetricsPanel } from "../components/metrics";

type Tab = "overview" | "metrics" | "compose" | "env" | "settings";

export function ServicePage() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "overview";
  const qc = useQueryClient();
  const [streamKey, setStreamKey] = useState(0);
  const { data, isLoading, error } = useQuery({ queryKey: ["service", id], queryFn: () => api.get<ServiceDetail>(`/services/${id}`) });
  const containers = useQuery({ queryKey: ["containers", "service", id], queryFn: () => api.get<Container[]>(`/services/${id}/containers`), refetchInterval: 10_000 });
  const deploying = data?.status === "deploying";
  const stream = useLogStream(deploying || streamKey > 0 ? `/services/${id}/stream?k=${streamKey}` : null);

  const deploy = useMutation({
    mutationFn: () => api.post(`/services/${id}/deploy`),
    onSuccess: () => {
      setStreamKey((k) => k + 1);
      setParams({ tab: "overview" });
    },
    onError: (e) => toast.error(e.message),
  });
  const action = useMutation({
    mutationFn: (a: string) => api.post(`/services/${id}/${a}`),
    onSuccess: (_, a) => {
      toast.success(`Service ${a} ok`);
      qc.invalidateQueries({ queryKey: ["service", id] });
      qc.invalidateQueries({ queryKey: ["containers"] });
    },
    onError: (e) => toast.error(e.message),
  });

  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  const s = data!;
  const running = s.status === "running" || s.status === "degraded";

  return (
    <>
      <PageHeader
        icon={<Boxes className="size-5" />}
        title={
          <span className="flex items-center gap-3">
            {s.name} <StatusBadge status={s.status} />
          </span>
        }
        subtitle={`${s.template === "custom" ? "Docker Compose" : s.template} · on ${s.server_name}`}
        breadcrumbs={
          <>
            <Link to="/projects" className="hover:text-fg">Projects</Link> / <Link to={`/projects/${s.project.id}?env=${s.environment_id}`} className="hover:text-fg">{s.project.name}</Link> / <span>{s.environment.name}</span> /{" "}
            <span className="text-fg">{s.name}</span>
          </>
        }
        actions={
          <>
            {running ? (
              <>
                <Button icon={<RotateCw className="size-4" />} onClick={() => action.mutate("restart")}>
                  Restart
                </Button>
                <Button icon={<Square className="size-4" />} onClick={() => action.mutate("stop")}>
                  Stop
                </Button>
              </>
            ) : (
              s.status === "stopped" && (containers.data?.length ?? 0) > 0 && (
                <Button icon={<Play className="size-4" />} onClick={() => action.mutate("start")}>
                  Start
                </Button>
              )
            )}
            <Button variant="primary" icon={<Rocket className="size-4" />} loading={deploy.isPending || deploying} onClick={() => deploy.mutate()}>
              Deploy
            </Button>
          </>
        }
      />
      <Tabs
        value={tab}
        onChange={(t) => setParams({ tab: t })}
        tabs={[
          { id: "overview", label: "Overview", icon: <LayoutGrid /> },
          { id: "metrics", label: "Metrics", icon: <Activity /> },
          { id: "compose", label: "Compose", icon: <FileCode /> },
          { id: "env", label: "Environment", icon: <KeyRound /> },
          { id: "settings", label: "Settings", icon: <Settings2 /> },
        ]}
      />
      {tab === "overview" && (
        <div className="space-y-6">
          {splitList(s.domain).length > 0 && (
            <div className="flex flex-wrap gap-2">
              {splitList(s.domain).map((d) => (
                <a key={d} href={domainURL(d)} target="_blank" rel="noreferrer" className="flex h-9 items-center gap-2 rounded-md border border-border bg-surface px-3 text-[13px] shadow-xs hover:border-border-strong">
                  <Globe className="size-4 text-subtle" /> {d} <ExternalLink className="size-3.5 text-muted" />
                </a>
              ))}
            </div>
          )}
          {(stream.lines.length > 0 || deploying) && <LogViewer lines={stream.lines} live={!stream.status} className="h-80" filename={`service-${s.id}.log`} />}
          <Card title="Containers">
            {containers.isLoading ? <Loading /> : containers.error ? <div className="p-5"><ErrorBox error={containers.error} /></div> : <ContainerTable serverId={s.server_id} containers={containers.data ?? []} />}
          </Card>
        </div>
      )}
      {tab === "metrics" && <MetricsPanel scope="service" id={s.id} />}
      {tab === "compose" && <ComposeEditor s={s} onDeploy={() => deploy.mutate()} />}
      {tab === "env" && (
        <div className="space-y-3">
          <p className="text-sm text-muted">Variables are available for compose interpolation (<code>${"{VAR}"}</code>) and injected into the exposed service.</p>
          <EnvEditor owner={`/services/${id}`} />
        </div>
      )}
      {tab === "settings" && <ServiceSettings s={s} />}
    </>
  );
}

function ComposeEditor({ s, onDeploy }: { s: ServiceDetail; onDeploy: () => void }) {
  const qc = useQueryClient();
  const [compose, setCompose] = useState(s.compose);
  useEffect(() => setCompose(s.compose), [s.compose]);
  const save = useMutation({
    mutationFn: () => api.patch<ServiceDetail>(`/services/${s.id}`, { ...s, compose }),
    onSuccess: (d) => {
      qc.setQueryData(["service", s.id], d);
      toast.success("Compose saved");
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Card
      title="docker-compose.yml"
      description="PortDock adds routing labels and the proxy network automatically via an override file."
      actions={
        <>
          <Button size="sm" icon={<Save className="size-3.5" />} loading={save.isPending} disabled={compose === s.compose} onClick={() => save.mutate()}>
            Save
          </Button>
          <Button size="sm" variant="primary" icon={<Rocket className="size-3.5" />} onClick={() => save.mutateAsync().then(onDeploy)}>
            Save & deploy
          </Button>
        </>
      }
    >
      <div className="p-5">
        <Textarea rows={28} value={compose} onChange={(e) => setCompose(e.target.value)} />
      </div>
    </Card>
  );
}

function ServiceSettings({ s }: { s: ServiceDetail }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const [f, setF] = useState({ name: s.name, description: s.description, domain: s.domain });
  const save = useMutation({
    mutationFn: () => api.patch<ServiceDetail>(`/services/${s.id}`, { ...s, ...f }),
    onSuccess: (d) => {
      qc.setQueryData(["service", s.id], d);
      toast.success("Saved — redeploy to apply domain changes");
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (volumes: boolean) => api.del(`/services/${s.id}?volumes=${volumes}`),
    onSuccess: () => nav(`/projects/${s.project.id}?env=${s.environment_id}`),
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="space-y-6">
      <Card title="General">
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label="Name">
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field label="Domain" hint="Routed to the service declared in x-portdock.">
            <Input value={f.domain} onChange={(e) => setF({ ...f, domain: e.target.value })} />
          </Field>
          <Field label="Description" className="md:col-span-2">
            <Input value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} />
          </Field>
          <div className="flex justify-end md:col-span-2">
            <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
              Save
            </Button>
          </div>
        </div>
      </Card>
      <Card title="Danger zone" className="border-danger/30">
        <div className="flex flex-wrap items-center justify-between gap-4 p-5">
          <p className="text-sm text-muted">Stop and remove the stack. Volumes can be kept or deleted.</p>
          <div className="flex gap-2">
            <Button variant="danger" onClick={async () => (await confirm({ title: "Delete service (keep volumes)?", danger: true, confirmText: "Delete" })) && del.mutate(false)}>
              Delete, keep volumes
            </Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={async () => (await confirm({ title: "Delete service and volumes?", danger: true, confirmText: "Delete everything", typeToConfirm: s.name })) && del.mutate(true)}>
              Delete with volumes
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
