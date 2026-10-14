import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Pencil, Plus, Save, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { LogAgentStatus, LogDrain, LogDrainType, Project, Settings } from "../lib/types";
import { t } from "../lib/i18n";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Loading, Modal, PasswordInput, Select, Switch, Table, Textarea, useConfirm } from "./ui";

// Settings → Observability: Prometheus scraping, log drains and the
// OpenTelemetry collector of the applications.

export function ObservabilitySettings() {
  return (
    <div className="space-y-6">
      <PrometheusCard />
      <LogDrainsCard />
      <OpenTelemetryCard />
    </div>
  );
}

function PrometheusCard() {
  const url = `${window.location.origin}/api/metrics`;
  const config = `scrape_configs:
  - job_name: portdock
    scheme: ${window.location.protocol.replace(":", "")}
    metrics_path: /api/metrics
    authorization:
      credentials: pd_…   # an API token with the read scope
    static_configs:
      - targets: ["${window.location.host}"]`;
  return (
    <Card
      title={t("Prometheus metrics")}
      description={t("The latest values of servers, applications, databases and services, plus deployments, jobs, backups, certificates, uptime monitors and alerts, in the Prometheus text format.")}
    >
      <div className="space-y-4 p-5">
        <CopyField label={t("Endpoint")} value={url} />
        <div>
          <div className="mb-1.5 text-xs font-medium">{t("Scrape configuration")}</div>
          <pre className="overflow-x-auto rounded-lg border border-border bg-surface-2/60 p-3 font-mono text-2xs leading-relaxed">{config}</pre>
        </div>
        <p className="text-xs text-muted">
          {t("Authenticate with an API token of a service account (read scope). A token limited to some projects exports only those.")}{" "}
          <Link to="/settings?tab=tokens" className="underline-offset-2 hover:text-fg hover:underline">
            {t("API tokens")} →
          </Link>
        </p>
      </div>
    </Card>
  );
}

const drainTypes: { type: LogDrainType; name: string; mark: string; color: string; placeholder: string; hint: string }[] = [
  { type: "loki", name: "Grafana Loki", mark: "LK", color: "bg-[#f46800] text-white", placeholder: "http://loki:3100", hint: "Base URL; lines get the project, environment, app, kind, server and stream labels." },
  { type: "elasticsearch", name: "Elasticsearch / OpenSearch", mark: "ES", color: "bg-[#00bfb3] text-white", placeholder: "https://elasticsearch:9200", hint: "Bulk API; one index per day unless you set another." },
  { type: "gelf", name: "Graylog (GELF)", mark: "GL", color: "bg-[#ff3633] text-white", placeholder: "udp://graylog:12201", hint: "A GELF UDP or TCP input: udp://, tcp:// or tls://host:port." },
  { type: "splunk", name: "Splunk HEC", mark: "SP", color: "bg-[#1a1c20] text-[#65a637]", placeholder: "https://splunk:8088", hint: "HTTP Event Collector base URL and token." },
  { type: "syslog", name: "Syslog (RFC 5424)", mark: "SL", color: "bg-[#4b5563] text-white", placeholder: "tls://siem.example.com:6514", hint: "udp://, tcp:// or tls://host:port; the application is the APP-NAME." },
  { type: "http", name: "HTTP (JSON lines)", mark: "HT", color: "bg-inverse text-inverse-fg", placeholder: "https://logs.example.com/ingest", hint: "Batches of newline-delimited JSON, POSTed to the URL." },
];

function DrainMark({ type }: { type: LogDrainType }) {
  const d = drainTypes.find((x) => x.type === type) ?? drainTypes[5];
  return <span className={`flex size-8 shrink-0 items-center justify-center rounded-md text-[10px] font-semibold ${d.color}`}>{d.mark}</span>;
}

type DrainForm = Partial<LogDrain> & { password?: string; token?: string; headersText?: string };

function headersOf(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const i = line.indexOf(":");
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return out;
}

function DrainModal({ initial, onClose }: { initial: DrainForm | null; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects"), enabled: !!initial });
  const [f, setF] = useState<DrainForm>({});
  useEffect(() => setF(initial ? { ...initial, headersText: (initial.header_names ?? []).map((h) => `${h}: `).join("\n") } : {}), [initial]);
  const kind = drainTypes.find((d) => d.type === f.type);
  const editing = !!f.id;
  const upd = (patch: DrainForm) => setF({ ...f, ...patch });
  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = {
        name: f.name || kind?.name,
        type: f.type,
        url: f.url,
        username: f.username ?? "",
        password: f.password ?? "",
        token: f.token ?? "",
        tenant: f.tenant ?? "",
        index: f.index ?? "",
        sourcetype: f.sourcetype ?? "",
        skip_verify: !!f.skip_verify,
        include_proxy: !!f.include_proxy,
        project_ids: f.project_ids ?? [],
      };
      if (f.type === "http") body.headers = headersOf(f.headersText ?? "");
      return editing ? api.patch(`/log-drains/${f.id}`, body) : api.post("/log-drains", body);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["log-drains"] });
      toast.success(editing ? t("Destination updated") : t("Destination added"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  const http = f.type && !["gelf", "syslog"].includes(f.type);
  const selected = new Set(f.project_ids ?? []);
  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="lg"
      title={editing ? t("Edit {name}", { name: f.name ?? "" }) : kind ? t("Add {name}", { name: kind.name }) : t("Add a log destination")}
      description={kind ? t(kind.hint) : t("Where the logs of every container PortDock runs are shipped, with their project, environment and application.")}
      footer={
        kind && (
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
            {editing ? t("Save changes") : t("Add destination")}
          </Button>
        )
      }
    >
      {!kind ? (
        <div className="grid gap-2 sm:grid-cols-2">
          {drainTypes.map((d) => (
            <button key={d.type} onClick={() => upd({ type: d.type })} className="flex items-center gap-3 rounded-lg border border-border bg-surface p-3 text-left transition-colors hover:border-border-strong hover:bg-surface-2">
              <DrainMark type={d.type} />
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{d.name}</div>
                <div className="truncate font-mono text-2xs text-muted">{d.placeholder}</div>
              </div>
            </button>
          ))}
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          <Field label={t("Name")}>
            <Input value={f.name ?? ""} onChange={(e) => upd({ name: e.target.value })} placeholder={kind.name} />
          </Field>
          <Field label={t("Address")}>
            <Input value={f.url ?? ""} onChange={(e) => upd({ url: e.target.value })} placeholder={kind.placeholder} className="font-mono text-xs" />
          </Field>
          {(f.type === "loki" || f.type === "elasticsearch" || f.type === "http") && (
            <>
              <Field label={t("Username")} hint={t("Basic authentication; leave empty to use a token.")}>
                <Input value={f.username ?? ""} onChange={(e) => upd({ username: e.target.value })} autoComplete="off" />
              </Field>
              <Field label={t("Password")} hint={editing && f.password_set ? t("Leave empty to keep the stored secret.") : undefined}>
                <PasswordInput value={f.password ?? ""} onChange={(e) => upd({ password: e.target.value })} placeholder={editing && f.password_set ? "••••••••" : ""} autoComplete="new-password" />
              </Field>
            </>
          )}
          {http && (
            <Field
              label={f.type === "splunk" ? t("HEC token") : f.type === "elasticsearch" ? t("API key") : t("Bearer token")}
              hint={editing && f.token_set ? t("Leave empty to keep the stored secret.") : f.type === "elasticsearch" ? t("Base64 API key, sent as Authorization: ApiKey …") : undefined}
            >
              <PasswordInput value={f.token ?? ""} onChange={(e) => upd({ token: e.target.value })} placeholder={editing && f.token_set ? "••••••••" : ""} autoComplete="new-password" />
            </Field>
          )}
          {f.type === "loki" && (
            <Field label={t("Tenant")} hint={t("X-Scope-OrgID of a multi-tenant Loki.")}>
              <Input value={f.tenant ?? ""} onChange={(e) => upd({ tenant: e.target.value })} />
            </Field>
          )}
          {(f.type === "elasticsearch" || f.type === "splunk") && (
            <Field label={t("Index")} hint={f.type === "elasticsearch" ? t("Default: portdock-%Y.%m.%d") : t("Optional; the token's default index otherwise.")}>
              <Input value={f.index ?? ""} onChange={(e) => upd({ index: e.target.value })} className="font-mono text-xs" />
            </Field>
          )}
          {f.type === "splunk" && (
            <Field label={t("Source type")} hint={t("Default: portdock")}>
              <Input value={f.sourcetype ?? ""} onChange={(e) => upd({ sourcetype: e.target.value })} className="font-mono text-xs" />
            </Field>
          )}
          {f.type === "http" && (
            <Field label={t("Headers")} hint={t("One per line, Name: value. An empty value keeps the stored one.")} className="md:col-span-2">
              <Textarea value={f.headersText ?? ""} onChange={(e) => upd({ headersText: e.target.value })} className="min-h-16" placeholder="X-Api-Key: …" />
            </Field>
          )}
          <Field label={t("Projects")} hint={t("None selected: every project.")} className="md:col-span-2">
            <div className="flex flex-wrap gap-1.5">
              {(projects.data ?? []).map((p) => (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => {
                    const next = new Set(selected);
                    if (next.has(p.id)) next.delete(p.id);
                    else next.add(p.id);
                    upd({ project_ids: [...next] });
                  }}
                  className={`rounded-md border px-2 py-1 text-xs transition-colors ${selected.has(p.id) ? "border-accent bg-accent/10 text-fg" : "border-border text-muted hover:text-fg"}`}
                >
                  {p.name}
                </button>
              ))}
            </div>
          </Field>
          <div className="space-y-3 md:col-span-2">
            <Switch checked={!!f.include_proxy} onChange={(v) => upd({ include_proxy: v })} label={t("Include the proxy logs")} description={t("Traefik's own logs (certificates, routing errors).")} />
            {(http || f.url?.startsWith("tls://")) && (
              <Switch checked={!!f.skip_verify} onChange={(v) => upd({ skip_verify: v })} label={t("Skip TLS verification")} description={t("Only for internal certificates that the servers do not trust.")} />
            )}
          </div>
        </div>
      )}
    </Modal>
  );
}

function LogDrainsCard() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [edit, setEdit] = useState<DrainForm | null>(null);
  const drains = useQuery({ queryKey: ["log-drains"], queryFn: () => api.get<LogDrain[]>("/log-drains") });
  const status = useQuery({
    queryKey: ["log-drains", "status"],
    queryFn: () => api.get<{ servers: LogAgentStatus[]; image: string }>("/log-drains/status"),
    refetchInterval: 10_000,
    enabled: (drains.data?.length ?? 0) > 0,
  });
  const toggle = useMutation({
    mutationFn: (d: LogDrain) => api.patch(`/log-drains/${d.id}`, { enabled: !d.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["log-drains"] }),
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/log-drains/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["log-drains"] });
      toast.success(t("Destination removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: () => api.post<{ message: string; servers: string[] }>("/log-drains/test"),
    onSuccess: (r) => {
      toast.success(t("Test line written on {servers}", { servers: r.servers.join(", ") }), { description: r.message });
      setTimeout(() => qc.invalidateQueries({ queryKey: ["log-drains", "status"] }), 5000);
    },
    onError: (e) => toast.error(e.message),
  });
  if (drains.isLoading) return <Loading />;
  const list = drains.data ?? [];
  const totals = (id: number) => {
    const key = `drain_${id}`;
    return (status.data?.servers ?? []).reduce((a, s) => ({ sent: a.sent + (s.destinations[key]?.sent ?? 0), errors: a.errors + (s.destinations[key]?.errors ?? 0) }), { sent: 0, errors: 0 });
  };
  return (
    <Card
      title={t("Log drains")}
      description={t("Ship the logs of every container to Loki, Elasticsearch, Graylog, Splunk, syslog or HTTP. An agent (Vector) on each server reads them from Docker, labels each line with its project, environment and application, and buffers up to 256 MB per destination while it is unreachable.")}
      actions={
        list.length > 0 && (
          <div className="flex gap-2">
            <Button size="sm" icon={<Send />} loading={test.isPending} onClick={() => test.mutate()}>
              {t("Send a test line")}
            </Button>
            <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
              {t("Add destination")}
            </Button>
          </div>
        )
      }
    >
      {list.length === 0 ? (
        <div className="p-5">
          <Empty
            compact
            icon={<Activity />}
            title={t("No log destinations")}
            description={t("Search the logs of past deployments in the tools your organization already uses.")}
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
                {t("Add destination")}
              </Button>
            }
          />
        </div>
      ) : (
        <>
          <Table>
            <thead>
              <tr>
                <th>{t("Destination")}</th>
                <th>{t("Projects")}</th>
                <th>{t("Sent")}</th>
                <th>{t("Enabled")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((d) => {
                const tot = totals(d.id);
                return (
                  <tr key={d.id}>
                    <td>
                      <div className="flex items-center gap-3">
                        <DrainMark type={d.type} />
                        <div className="min-w-0">
                          <div className="font-medium">{d.name}</div>
                          <div className="truncate font-mono text-2xs text-muted">{d.url}</div>
                        </div>
                      </div>
                    </td>
                    <td className="text-xs text-muted">{d.project_ids.length === 0 ? t("All") : t("{n} project|{n} projects", { n: d.project_ids.length })}</td>
                    <td className="text-xs tabular-nums">
                      {d.enabled ? tot.sent.toLocaleString() : "—"}
                      {tot.errors > 0 && (
                        <Badge tone="danger" className="ml-2">
                          {t("{n} error|{n} errors", { n: tot.errors })}
                        </Badge>
                      )}
                    </td>
                    <td>
                      <Switch checked={d.enabled} onChange={() => toggle.mutate(d)} />
                    </td>
                    <td className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button size="sm" variant="ghost" icon={<Pencil />} onClick={() => setEdit(d)} aria-label={t("Edit")} />
                        <Button
                          size="sm"
                          variant="ghost"
                          icon={<Trash2 />}
                          aria-label={t("Delete")}
                          onClick={async () => {
                            if (await confirm({ title: t("Remove {name}?", { name: d.name }), description: t("Logs stop being shipped there."), danger: true, confirmText: t("Remove") })) del.mutate(d.id);
                          }}
                        />
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
          <div className="border-t border-border px-5 py-3">
            <div className="mb-2 text-2xs font-medium uppercase tracking-wider text-faint">{t("Agents")}</div>
            <div className="space-y-1.5">
              {(status.data?.servers ?? []).map((s) => (
                <div key={s.server_id} className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="font-medium">{s.server}</span>
                  <Badge tone={s.state === "running" ? "success" : "warning"}>{s.state === "running" ? t("Running") : s.state === "missing" ? t("Not started") : s.state}</Badge>
                  {s.error && <span className="text-danger">{s.error}</span>}
                </div>
              ))}
              {status.isLoading && <div className="text-xs text-muted">{t("Checking the servers…")}</div>}
            </div>
            {status.data && <div className="mt-2 font-mono text-2xs text-faint">{status.data.image}</div>}
          </div>
        </>
      )}
      <DrainModal initial={edit} onClose={() => setEdit(null)} />
    </Card>
  );
}

function OpenTelemetryCard() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<Settings>("/settings") });
  const [f, setF] = useState({ otel_endpoint: "", otel_protocol: "http/protobuf", otel_headers: "" });
  useEffect(() => {
    if (settings.data) setF({ otel_endpoint: settings.data.otel_endpoint ?? "", otel_protocol: settings.data.otel_protocol || "http/protobuf", otel_headers: "" });
  }, [settings.data]);
  const save = useMutation({
    mutationFn: () => api.patch<Settings>("/settings", f),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      toast.success(t("OpenTelemetry settings saved"), { description: t("Applications get them at their next deployment.") });
    },
    onError: (e) => toast.error(e.message),
  });
  if (settings.isLoading) return <Loading />;
  return (
    <Card
      title={t("OpenTelemetry")}
      description={t("Applications instrumented with an OpenTelemetry SDK send their traces, metrics and logs to your collector without any configuration: PortDock sets the OTLP endpoint and the resource attributes (service name, project, environment, commit) at each deployment.")}
    >
      <div className="grid gap-4 p-5 md:grid-cols-2">
        <Field label={t("OTLP endpoint")} hint={t("Empty: nothing is added to the applications.")}>
          <Input value={f.otel_endpoint} onChange={(e) => setF({ ...f, otel_endpoint: e.target.value })} placeholder="http://otel-collector:4318" className="font-mono text-xs" />
        </Field>
        <Field label={t("Protocol")}>
          <Select value={f.otel_protocol} onChange={(e) => setF({ ...f, otel_protocol: e.target.value })}>
            <option value="http/protobuf">HTTP (protobuf) — 4318</option>
            <option value="grpc">gRPC — 4317</option>
            <option value="http/json">HTTP (JSON)</option>
          </Select>
        </Field>
        <Field label={t("Headers")} hint={settings.data?.otel_headers_set ? t("Headers are stored; enter new ones to replace them.") : t("Optional, e.g. api-key=… (stored encrypted).")} className="md:col-span-2">
          <PasswordInput value={f.otel_headers} onChange={(e) => setF({ ...f, otel_headers: e.target.value })} placeholder={settings.data?.otel_headers_set ? "••••••••" : "authorization=Bearer …"} />
        </Field>
        <p className="text-xs text-muted md:col-span-2">
          {t("An application that sets OTEL_EXPORTER_OTLP_ENDPOINT itself, or OTEL_SDK_DISABLED, gets nothing; other OTEL_* variables it sets are kept.")}
        </p>
        <div className="flex justify-end md:col-span-2">
          <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
            {t("Save")}
          </Button>
        </div>
      </div>
    </Card>
  );
}
