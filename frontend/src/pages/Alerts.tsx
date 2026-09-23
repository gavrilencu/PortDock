import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing, Check, CircleCheck, Pencil, Plus, Siren, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { duration, timeAgo } from "../lib/utils";
import { AdminOnly } from "../components/Layout";
import { Badge, Button, Card, Empty, Field, Input, Loading, Modal, PageHeader, Select, Switch, Table, Tabs, useConfirm } from "../components/ui";

export interface AlertEvent {
  id: number;
  rule_id: number;
  rule_name: string;
  severity: "warning" | "critical";
  metric: string;
  scope: string;
  ref_id: number;
  server_id: number;
  target_name: string;
  status: "firing" | "resolved";
  value: number;
  message: string;
  started_at: string;
  resolved_at: string;
  acknowledged_by: number;
  acknowledged_by_name: string;
}

export interface AlertRule {
  id: number;
  name: string;
  scope: string;
  target_id: number;
  metric: string;
  threshold: number;
  duration: number;
  severity: "warning" | "critical";
  enabled: boolean;
}

type RulesResponse = {
  rules: AlertRule[];
  metrics: Record<string, string[]>;
  channels: { id: number; name: string; type: string; enabled: boolean; alerts: boolean }[];
};

export function useFiringAlerts() {
  return useQuery({ queryKey: ["alerts", "firing"], queryFn: () => api.get<AlertEvent[]>("/alerts?status=firing"), refetchInterval: 60_000 });
}

const scopeLabel: Record<string, string> = { server: "Server", application: "Application", database: "Database", service: "Service" };
const metricLabel: Record<string, string> = { cpu: "CPU", memory: "Memory", disk: "Disk", load: "Load average", restarts: "Restarts", down: "Down" };
const scopePath: Record<string, string> = { server: "servers", application: "applications", database: "databases", service: "services" };

function SeverityBadge({ s }: { s: string }) {
  return <Badge tone={s === "critical" ? "danger" : "warning"}>{s === "critical" ? "Critical" : "Warning"}</Badge>;
}

function condition(r: AlertRule) {
  switch (r.metric) {
    case "down":
      return `not running for ${r.duration} min`;
    case "restarts":
      return `> ${r.threshold} restarts in ${r.duration} min`;
    case "load":
      return `> ${r.threshold} for ${r.duration} min`;
    default:
      return `> ${r.threshold}% for ${r.duration} min`;
  }
}

function AlertsTable({ events, history }: { events: AlertEvent[]; history?: boolean }) {
  const qc = useQueryClient();
  const ack = useMutation({
    mutationFn: (id: number) => api.post(`/alerts/${id}/ack`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alerts"] }),
    onError: (e) => toast.error(e.message),
  });
  return (
    <Table>
      <thead>
        <tr>
          <th>Alert</th>
          <th>Target</th>
          <th>{history ? "Duration" : "Since"}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {events.map((e) => (
          <tr key={e.id}>
            <td>
              <div className="flex items-center gap-2">
                {e.status === "firing" ? <span className="pulse-dot relative inline-flex size-2 rounded-full bg-danger" /> : <CircleCheck className="size-4 text-success" />}
                <span className="font-medium">{e.rule_name}</span>
                <SeverityBadge s={e.severity} />
              </div>
              <div className="mt-0.5 pl-4 text-xs text-muted">{e.message}</div>
            </td>
            <td>
              <Link to={`/${scopePath[e.scope]}/${e.ref_id}`} className="font-medium hover:underline">
                {e.target_name}
              </Link>
              <div className="text-xs text-muted">{scopeLabel[e.scope]}</div>
            </td>
            <td className="text-xs text-muted">{e.status === "resolved" ? duration(e.started_at, e.resolved_at) : timeAgo(e.started_at)}</td>
            <td className="text-right">
              {e.status === "firing" &&
                (e.acknowledged_by ? (
                  <span className="text-xs text-muted">Acknowledged by {e.acknowledged_by_name}</span>
                ) : (
                  <Button size="sm" icon={<Check />} loading={ack.isPending && ack.variables === e.id} onClick={() => ack.mutate(e.id)}>
                    Acknowledge
                  </Button>
                ))}
            </td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

type Draft = Partial<AlertRule>;

function RuleModal({ draft, metrics, onClose }: { draft: Draft | null; metrics: Record<string, string[]>; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<Draft>({});
  useEffect(() => setF(draft ?? {}), [draft]);
  const scope = f.scope ?? "server";
  const targets = useQuery({
    queryKey: ["alert-targets", scope],
    queryFn: () => api.get<{ id: number; name: string }[]>(`/${scopePath[scope]}`),
    enabled: !!draft,
  });
  const save = useMutation({
    mutationFn: () => (f.id ? api.patch(`/alert-rules/${f.id}`, f) : api.post("/alert-rules", f)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["alert-rules"] });
      toast.success(f.id ? "Rule updated" : "Rule created");
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  const metric = f.metric ?? metrics[scope]?.[0];
  const unit = metric === "restarts" ? "restarts" : metric === "load" ? "" : "%";
  return (
    <Modal
      open={!!draft}
      onClose={onClose}
      title={f.id ? "Edit alert rule" : "New alert rule"}
      description="The alert fires when the condition holds for the whole duration, and resolves as soon as it clears."
      footer={
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          {f.id ? "Save" : "Create rule"}
        </Button>
      }
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Field label="Name" className="md:col-span-2">
          <Input value={f.name ?? ""} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="API memory high" autoFocus />
        </Field>
        <Field label="Applies to">
          <Select value={scope} onChange={(e) => setF({ ...f, scope: e.target.value, metric: metrics[e.target.value][0], target_id: 0 })}>
            {Object.keys(metrics).map((s) => (
              <option key={s} value={s}>
                {scopeLabel[s]}s
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Target">
          <Select value={f.target_id ?? 0} onChange={(e) => setF({ ...f, target_id: Number(e.target.value) })}>
            <option value={0}>All {scopeLabel[scope].toLowerCase()}s</option>
            {targets.data?.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Metric">
          <Select value={metric} onChange={(e) => setF({ ...f, metric: e.target.value })}>
            {metrics[scope]?.map((m) => (
              <option key={m} value={m}>
                {metricLabel[m]}
              </option>
            ))}
          </Select>
        </Field>
        {metric !== "down" && (
          <Field label={`Threshold${unit ? ` (${unit})` : ""}`} hint={metric === "cpu" && scope !== "server" ? "Containers: 100% = one full core." : undefined}>
            <Input type="number" value={f.threshold ?? ""} onChange={(e) => setF({ ...f, threshold: Number(e.target.value) })} />
          </Field>
        )}
        <Field label="For (minutes)" hint={metric === "restarts" ? "Window over which restarts are counted." : "How long the condition must hold."}>
          <Input type="number" min={1} value={f.duration ?? 5} onChange={(e) => setF({ ...f, duration: Number(e.target.value) })} />
        </Field>
        <Field label="Severity">
          <Select value={f.severity ?? "warning"} onChange={(e) => setF({ ...f, severity: e.target.value as "warning" | "critical" })}>
            <option value="warning">Warning</option>
            <option value="critical">Critical</option>
          </Select>
        </Field>
      </div>
    </Modal>
  );
}

function Rules() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useQuery({ queryKey: ["alert-rules"], queryFn: () => api.get<RulesResponse>("/alert-rules") });
  const [draft, setDraft] = useState<Draft | null>(null);
  const toggle = useMutation({
    mutationFn: (r: AlertRule) => api.patch(`/alert-rules/${r.id}`, { enabled: !r.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alert-rules"] }),
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/alert-rules/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alert-rules"] }),
    onError: (e) => toast.error(e.message),
  });
  const channel = useMutation({
    mutationFn: ({ id, alerts }: { id: number; alerts: boolean }) => api.put(`/notifications/${id}/alerts`, { alerts }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alert-rules"] }),
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  const { rules, metrics, channels } = q.data!;
  return (
    <div className="space-y-6">
      <Card
        title="Rules"
        description="Evaluated every minute against the collected metrics."
        actions={
          <AdminOnly>
            <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setDraft({ scope: "server", metric: "cpu", threshold: 80, duration: 5, severity: "warning" })}>
              New rule
            </Button>
          </AdminOnly>
        }
      >
        <Table>
          <thead>
            <tr>
              <th>Rule</th>
              <th>Applies to</th>
              <th>Condition</th>
              <th>Enabled</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.id}>
                <td>
                  <div className="flex items-center gap-2 font-medium">
                    {r.name} <SeverityBadge s={r.severity} />
                  </div>
                </td>
                <td className="text-xs text-muted">{r.target_id ? `${scopeLabel[r.scope]} #${r.target_id}` : `All ${scopeLabel[r.scope].toLowerCase()}s`}</td>
                <td className="text-xs">
                  <span className="font-medium">{metricLabel[r.metric]}</span> <span className="text-muted">{condition(r)}</span>
                </td>
                <td>
                  <Switch checked={r.enabled} onChange={() => toggle.mutate(r)} />
                </td>
                <td className="text-right">
                  <AdminOnly>
                    <div className="flex justify-end gap-1">
                      <Button size="icon-sm" variant="ghost" title="Edit" onClick={() => setDraft(r)}>
                        <Pencil />
                      </Button>
                      <Button size="icon-sm" variant="ghost" title="Delete" onClick={async () => (await confirm({ title: `Delete "${r.name}"?`, danger: true, confirmText: "Delete" })) && del.mutate(r.id)}>
                        <Trash2 className="text-danger" />
                      </Button>
                    </div>
                  </AdminOnly>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>

      <Card title="Delivery" description="Notification channels that receive alert and recovery messages.">
        {channels.length === 0 ? (
          <div className="p-5">
            <Empty
              compact
              icon={<BellRing />}
              title="No notification channels"
              description="Alerts are shown here, but nobody is notified yet."
              action={
                <Link to="/settings?tab=notifications">
                  <Button variant="primary">Add a channel</Button>
                </Link>
              }
            />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {channels.map((c) => (
              <div key={c.id} className="flex items-center gap-3 px-5 py-3">
                <div className="min-w-0 flex-1">
                  <div className="text-[13px] font-medium">{c.name}</div>
                  <div className="text-xs capitalize text-muted">
                    {c.type}
                    {!c.enabled && " · disabled"}
                  </div>
                </div>
                <Switch checked={c.alerts} onChange={(v) => channel.mutate({ id: c.id, alerts: v })} label="Send alerts" />
              </div>
            ))}
          </div>
        )}
      </Card>
      <RuleModal draft={draft} metrics={metrics} onClose={() => setDraft(null)} />
    </div>
  );
}

export function AlertsPage() {
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as "active" | "history" | "rules") || "active";
  const firing = useFiringAlerts();
  const history = useQuery({ queryKey: ["alerts", "resolved"], queryFn: () => api.get<AlertEvent[]>("/alerts?status=resolved"), enabled: tab === "history" });
  const active = firing.data ?? [];
  const critical = active.filter((a) => a.severity === "critical").length;
  return (
    <>
      <PageHeader
        title="Alerts"
        subtitle="Threshold alerts on servers, applications, databases and services."
        meta={
          active.length > 0 ? (
            <Badge tone={critical ? "danger" : "warning"}>
              {active.length} firing{critical ? ` · ${critical} critical` : ""}
            </Badge>
          ) : (
            <Badge tone="success">All clear</Badge>
          )
        }
      />
      <Tabs
        value={tab}
        onChange={(t) => setParams({ tab: t })}
        tabs={[
          { id: "active", label: "Active", icon: <Siren />, count: active.length || undefined },
          { id: "history", label: "History", icon: <CircleCheck /> },
          { id: "rules", label: "Rules", icon: <BellRing /> },
        ]}
      />
      {tab === "active" &&
        (firing.isLoading ? (
          <Loading />
        ) : active.length === 0 ? (
          <Empty icon={<CircleCheck />} title="No active alerts" description="Everything is within its thresholds." />
        ) : (
          <Card>
            <AlertsTable events={active} />
          </Card>
        ))}
      {tab === "history" &&
        (history.isLoading ? (
          <Loading />
        ) : !history.data?.length ? (
          <Empty icon={<CircleCheck />} title="No resolved alerts yet" />
        ) : (
          <Card>
            <AlertsTable events={history.data} history />
          </Card>
        ))}
      {tab === "rules" && <Rules />}
    </>
  );
}
