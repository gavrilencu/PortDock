import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing, Check, CircleCheck, Pencil, Plus, Siren, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { t, tk } from "../lib/i18n";
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

const scopeLabel: Record<string, string> = { server: tk("Server"), application: tk("Application"), database: tk("Database"), service: tk("Service") };
const scopePlural: Record<string, string> = { server: tk("Servers"), application: tk("Applications"), database: tk("Databases"), service: tk("Services") };
const scopeAll: Record<string, string> = { server: tk("All servers"), application: tk("All applications"), database: tk("All databases"), service: tk("All services") };
const metricLabel: Record<string, string> = { cpu: tk("CPU"), memory: tk("Memory"), disk: tk("Disk"), load: tk("Load average"), restarts: tk("Restarts"), down: tk("Down") };
const scopePath: Record<string, string> = { server: "servers", application: "applications", database: "databases", service: "services" };

function SeverityBadge({ s }: { s: string }) {
  return <Badge tone={s === "critical" ? "danger" : "warning"}>{s === "critical" ? t("Critical") : t("Warning")}</Badge>;
}

function condition(r: AlertRule) {
  switch (r.metric) {
    case "down":
      return t("not running for {min} min", { min: r.duration });
    case "restarts":
      return t("> {threshold} restarts in {min} min", { threshold: r.threshold, min: r.duration });
    case "load":
      return t("> {threshold} for {min} min", { threshold: r.threshold, min: r.duration });
    default:
      return t("> {threshold}% for {min} min", { threshold: r.threshold, min: r.duration });
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
          <th>{t("Alert")}</th>
          <th>{t("Target")}</th>
          <th>{history ? t("Duration") : t("Since")}</th>
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
              <div className="text-xs text-muted">{scopeLabel[e.scope] ? t(scopeLabel[e.scope]) : e.scope}</div>
            </td>
            <td className="text-xs text-muted">{e.status === "resolved" ? duration(e.started_at, e.resolved_at) : timeAgo(e.started_at)}</td>
            <td className="text-right">
              {e.status === "firing" &&
                (e.acknowledged_by ? (
                  <span className="text-xs text-muted">{t("Acknowledged by {name}", { name: e.acknowledged_by_name })}</span>
                ) : (
                  <Button size="sm" icon={<Check />} loading={ack.isPending && ack.variables === e.id} onClick={() => ack.mutate(e.id)}>
                    {t("Acknowledge")}
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
      toast.success(f.id ? t("Rule updated") : t("Rule created"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  const metric = f.metric ?? metrics[scope]?.[0];
  const thresholdLabel = metric === "restarts" ? t("Threshold (restarts)") : metric === "load" ? t("Threshold") : t("Threshold (%)");
  return (
    <Modal
      open={!!draft}
      onClose={onClose}
      title={f.id ? t("Edit alert rule") : t("New alert rule")}
      description={t("The alert fires when the condition holds for the whole duration, and resolves as soon as it clears.")}
      footer={
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          {f.id ? t("Save") : t("Create rule")}
        </Button>
      }
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Field label={t("Name")} className="md:col-span-2">
          <Input value={f.name ?? ""} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="API memory high" autoFocus />
        </Field>
        <Field label={t("Applies to")}>
          <Select value={scope} onChange={(e) => setF({ ...f, scope: e.target.value, metric: metrics[e.target.value][0], target_id: 0 })}>
            {Object.keys(metrics).map((s) => (
              <option key={s} value={s}>
                {scopePlural[s] ? t(scopePlural[s]) : s}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Target")}>
          <Select value={f.target_id ?? 0} onChange={(e) => setF({ ...f, target_id: Number(e.target.value) })}>
            <option value={0}>{t(scopeAll[scope])}</option>
            {targets.data?.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Metric")}>
          <Select value={metric} onChange={(e) => setF({ ...f, metric: e.target.value })}>
            {metrics[scope]?.map((m) => (
              <option key={m} value={m}>
                {metricLabel[m] ? t(metricLabel[m]) : m}
              </option>
            ))}
          </Select>
        </Field>
        {metric !== "down" && (
          <Field label={thresholdLabel} hint={metric === "cpu" && scope !== "server" ? t("Containers: 100% = one full core.") : undefined}>
            <Input type="number" value={f.threshold ?? ""} onChange={(e) => setF({ ...f, threshold: Number(e.target.value) })} />
          </Field>
        )}
        <Field label={t("For (minutes)")} hint={metric === "restarts" ? t("Window over which restarts are counted.") : t("How long the condition must hold.")}>
          <Input type="number" min={1} value={f.duration ?? 5} onChange={(e) => setF({ ...f, duration: Number(e.target.value) })} />
        </Field>
        <Field label={t("Severity")}>
          <Select value={f.severity ?? "warning"} onChange={(e) => setF({ ...f, severity: e.target.value as "warning" | "critical" })}>
            <option value="warning">{t("Warning")}</option>
            <option value="critical">{t("Critical")}</option>
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
        title={t("Rules")}
        description={t("Evaluated every minute against the collected metrics.")}
        actions={
          <AdminOnly>
            <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setDraft({ scope: "server", metric: "cpu", threshold: 80, duration: 5, severity: "warning" })}>
              {t("New rule")}
            </Button>
          </AdminOnly>
        }
      >
        <Table>
          <thead>
            <tr>
              <th>{t("Rule")}</th>
              <th>{t("Applies to")}</th>
              <th>{t("Condition")}</th>
              <th>{t("Enabled")}</th>
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
                <td className="text-xs text-muted">{r.target_id ? `${t(scopeLabel[r.scope])} #${r.target_id}` : t(scopeAll[r.scope])}</td>
                <td className="text-xs">
                  <span className="font-medium">{metricLabel[r.metric] ? t(metricLabel[r.metric]) : r.metric}</span> <span className="text-muted">{condition(r)}</span>
                </td>
                <td>
                  <Switch checked={r.enabled} onChange={() => toggle.mutate(r)} />
                </td>
                <td className="text-right">
                  <AdminOnly>
                    <div className="flex justify-end gap-1">
                      <Button size="icon-sm" variant="ghost" title={t("Edit")} onClick={() => setDraft(r)}>
                        <Pencil />
                      </Button>
                      <Button size="icon-sm" variant="ghost" title={t("Delete")} onClick={async () => (await confirm({ title: t("Delete \"{name}\"?", { name: r.name }), danger: true, confirmText: t("Delete") })) && del.mutate(r.id)}>
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

      <Card title={t("Delivery")} description={t("Notification channels that receive alert and recovery messages.")}>
        {channels.length === 0 ? (
          <div className="p-5">
            <Empty
              compact
              icon={<BellRing />}
              title={t("No notification channels")}
              description={t("Alerts are shown here, but nobody is notified yet.")}
              action={
                <Link to="/settings?tab=notifications">
                  <Button variant="primary">{t("Add a channel")}</Button>
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
                    {!c.enabled && ` · ${t("disabled")}`}
                  </div>
                </div>
                <Switch checked={c.alerts} onChange={(v) => channel.mutate({ id: c.id, alerts: v })} label={t("Send alerts")} />
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
        title={t("Alerts")}
        subtitle={t("Threshold alerts on servers, applications, databases and services.")}
        meta={
          active.length > 0 ? (
            <Badge tone={critical ? "danger" : "warning"}>
              {t("{n} firing|{n} firing", { n: active.length })}
              {critical ? ` · ${t("{n} critical|{n} critical", { n: critical })}` : ""}
            </Badge>
          ) : (
            <Badge tone="success">{t("All clear")}</Badge>
          )
        }
      />
      <Tabs
        value={tab}
        onChange={(t) => setParams({ tab: t })}
        tabs={[
          { id: "active", label: t("Active"), icon: <Siren />, count: active.length || undefined },
          { id: "history", label: t("History"), icon: <CircleCheck /> },
          { id: "rules", label: t("Rules"), icon: <BellRing /> },
        ]}
      />
      {tab === "active" &&
        (firing.isLoading ? (
          <Loading />
        ) : active.length === 0 ? (
          <Empty icon={<CircleCheck />} title={t("No active alerts")} description={t("Everything is within its thresholds.")} />
        ) : (
          <Card>
            <AlertsTable events={active} />
          </Card>
        ))}
      {tab === "history" &&
        (history.isLoading ? (
          <Loading />
        ) : !history.data?.length ? (
          <Empty icon={<CircleCheck />} title={t("No resolved alerts yet")} />
        ) : (
          <Card>
            <AlertsTable events={history.data} history />
          </Card>
        ))}
      {tab === "rules" && <Rules />}
    </>
  );
}
