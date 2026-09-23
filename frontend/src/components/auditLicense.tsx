import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Download, KeyRound, Radio, Search, Send, Sparkles, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { intlLocale, t, tk, tx } from "../lib/i18n";
import type { ActivityEntry } from "../lib/types";
import { timeAgo } from "../lib/utils";
import { useUser } from "./Layout";
import { UpgradeNotice, useLicense } from "./access";
import { Badge, Button, Card, Empty, Field, Input, Loading, Meter, PasswordInput, Table, Textarea, useConfirm } from "./ui";

// ---------- Audit log ----------

type Filters = { q: string; action: string; from: string; to: string };

function queryString(f: Filters, extra: Record<string, string | number> = {}) {
  const p = new URLSearchParams();
  Object.entries({ ...f, ...extra }).forEach(([k, v]) => v !== "" && v !== undefined && p.set(k, String(v)));
  return p.toString();
}

const actionGroups = [
  { value: "", label: tk("All actions") },
  { value: "login", label: tk("Sign-ins") },
  { value: "deployment.", label: tk("Deployment approvals") },
  { value: "application.", label: tk("Applications") },
  { value: "database.", label: tk("Databases") },
  { value: "user.", label: tk("Users") },
  { value: "2fa.", label: tk("Two-factor") },
  { value: "sso.", label: tk("Single sign-on") },
  { value: "project.", label: tk("Projects & access") },
  { value: "settings.", label: tk("Settings") },
  { value: "license.", label: tk("License") },
  { value: "terminal.", label: tk("Terminal sessions") },
];

function AuditStreaming() {
  const qc = useQueryClient();
  const { has } = useLicense();
  const q = useQuery({
    queryKey: ["audit-settings"],
    queryFn: () => api.get<{ webhook_url: string; webhook_secret_set: boolean; syslog: string; last_error: string; last_sent_at: string; dropped: number }>("/settings/audit"),
  });
  const [f, setF] = useState({ webhook_url: "", webhook_secret: "", syslog: "" });
  useEffect(() => {
    if (q.data) setF({ webhook_url: q.data.webhook_url, webhook_secret: "", syslog: q.data.syslog });
  }, [q.data]);
  const save = useMutation({
    mutationFn: () => api.put("/settings/audit", f),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["audit-settings"] });
      toast.success(t("Audit streaming saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: () => api.post("/settings/audit/test"),
    onSuccess: () => toast.success(t("Test event delivered")),
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading rows={2} />;
  const d = q.data!;
  return (
    <Card
      title={t("Stream to SIEM")}
      description={t("Every audit event is forwarded in real time — to Splunk, Elastic, Microsoft Sentinel, Datadog or any syslog collector.")}
      actions={
        <div className="flex gap-2">
          <Button size="sm" icon={<Send />} loading={test.isPending} disabled={!d.webhook_url && !d.syslog} onClick={() => test.mutate()}>
            {t("Send test")}
          </Button>
          <Button size="sm" variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
            {t("Save")}
          </Button>
        </div>
      }
    >
      <div className="grid gap-4 p-5 md:grid-cols-2">
        {!has("audit_streaming") && (
          <div className="md:col-span-2">
            <UpgradeNotice feature="audit_streaming">{t("Audit log streaming is part of the")}</UpgradeNotice>
          </div>
        )}
        <Field label={t("Webhook URL")} hint={t("JSON POST per event, signed with HMAC-SHA256 in X-PortDock-Signature.")}>
          <Input value={f.webhook_url} onChange={(e) => setF({ ...f, webhook_url: e.target.value })} placeholder="https://siem.example.com/hooks/portdock" />
        </Field>
        <Field label={t("Signing secret")} hint={d.webhook_secret_set ? t("A secret is stored; type to replace it.") : t("Optional but recommended.")}>
          <PasswordInput value={f.webhook_secret} onChange={(e) => setF({ ...f, webhook_secret: e.target.value })} placeholder={d.webhook_secret_set ? "••••••••" : ""} />
        </Field>
        <Field label="Syslog" hint={t("RFC 5424, facility log-audit. udp://host:514 or tcp://host:601.")} className="md:col-span-2">
          <Input value={f.syslog} onChange={(e) => setF({ ...f, syslog: e.target.value })} placeholder="udp://siem.example.com:514" className="font-mono text-xs" />
        </Field>
        {(d.last_error || d.last_sent_at || d.dropped > 0) && (
          <div className="flex flex-wrap items-center gap-3 text-xs md:col-span-2">
            {d.last_sent_at && (
              <span className="flex items-center gap-1 text-success">
                <Check className="size-3.5" /> {t("Last delivered {time}", { time: timeAgo(d.last_sent_at) })}
              </span>
            )}
            {d.last_error && <span className="text-danger">{d.last_error}</span>}
            {d.dropped > 0 && <Badge tone="warning">{t("{n} dropped (buffer full)", { n: d.dropped })}</Badge>}
          </div>
        )}
      </div>
    </Card>
  );
}

export function AuditLog() {
  const user = useUser();
  const admin = user.role !== "member";
  const { has } = useLicense();
  const [f, setF] = useState<Filters>({ q: "", action: "", from: "", to: "" });
  const [applied, setApplied] = useState<Filters>(f);
  const [page, setPage] = useState(0);
  const limit = 50;
  const q = useQuery({
    queryKey: ["activity", applied, page],
    queryFn: () => api.get<{ entries: ActivityEntry[]; has_more: boolean }>(`/activity?${queryString(applied, { limit, offset: page * limit })}`),
    refetchInterval: 30_000,
  });
  const apply = () => {
    setApplied(f);
    setPage(0);
  };
  const exportURL = (format: string) => `/api/activity/export?${queryString(applied, { format })}`;

  return (
    <div className="space-y-6">
      <Card
        title={t("Audit log")}
        description={admin ? t("Every security-relevant action, with the user and IP address.") : t("Your own actions.")}
        actions={
          admin && (
            <div className="flex gap-2">
              <a href={has("audit_export") ? exportURL("csv") : undefined}>
                <Button size="sm" icon={<Download />} disabled={!has("audit_export")}>
                  CSV
                </Button>
              </a>
              <a href={has("audit_export") ? exportURL("json") : undefined}>
                <Button size="sm" icon={<Download />} disabled={!has("audit_export")}>
                  JSON
                </Button>
              </a>
            </div>
          )
        }
      >
        <form
          className="flex flex-wrap items-end gap-2 border-b border-border p-4"
          onSubmit={(e) => {
            e.preventDefault();
            apply();
          }}
        >
          <div className="relative min-w-52 flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("Search user, action, target or IP…")} value={f.q} onChange={(e) => setF({ ...f, q: e.target.value })} />
          </div>
          <select className="h-8 rounded-md border border-border bg-surface px-2 text-[13px]" value={f.action} onChange={(e) => setF({ ...f, action: e.target.value })}>
            {actionGroups.map((g) => (
              <option key={g.value} value={g.value}>
                {t(g.label)}
              </option>
            ))}
          </select>
          <Input type="date" className="w-36" value={f.from} onChange={(e) => setF({ ...f, from: e.target.value })} title={t("From date")} />
          <Input type="date" className="w-36" value={f.to} onChange={(e) => setF({ ...f, to: e.target.value })} title={t("To date")} />
          <Button type="submit">{t("Apply")}</Button>
        </form>
        {admin && !has("audit_export") && (
          <div className="border-b border-border p-4">
            <UpgradeNotice feature="audit_export">{t("Exporting the audit log is part of the")}</UpgradeNotice>
          </div>
        )}
        {q.isLoading ? (
          <Loading />
        ) : !q.data?.entries.length ? (
          <div className="p-5">
            <Empty compact title={t("No matching events")} />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("When")}</th>
                <th>{t("Who")}</th>
                <th>{t("Action")}</th>
                <th>{t("Target")}</th>
                <th>{t("IP address")}</th>
              </tr>
            </thead>
            <tbody>
              {q.data.entries.map((a) => (
                <tr key={a.id}>
                  <td className="whitespace-nowrap text-xs text-muted" title={new Date(a.created_at).toLocaleString(intlLocale())}>
                    {timeAgo(a.created_at)}
                  </td>
                  <td>
                    <div className="text-[13px]">{a.user_name}</div>
                    {a.user_email && <div className="text-2xs text-muted">{a.user_email}</div>}
                  </td>
                  <td>
                    <code className={`text-xs ${/failed|denied|reject|disable|delete|remove/.test(a.action) ? "text-danger" : ""}`}>{a.action}</code>
                  </td>
                  <td className="max-w-80 truncate text-xs text-muted">{a.target}</td>
                  <td className="font-mono text-2xs text-muted">{a.ip || "—"}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
        {(page > 0 || q.data?.has_more) && (
          <div className="flex items-center justify-between border-t border-border px-4 py-2.5 text-xs text-muted">
            <span>{t("Page {page}", { page: page + 1 })}</span>
            <div className="flex gap-2">
              <Button size="sm" disabled={page === 0} onClick={() => setPage(page - 1)}>
                {t("Newer")}
              </Button>
              <Button size="sm" disabled={!q.data?.has_more} onClick={() => setPage(page + 1)}>
                {t("Older")}
              </Button>
            </div>
          </div>
        )}
      </Card>
      {admin && <AuditStreaming />}
    </div>
  );
}

// ---------- License ----------

type LicenseState = {
  state: {
    plan: { id: string; name: string; max_servers: number; max_users: number; features: string[] };
    source: "license" | "trial" | "community";
    license?: { id: string; customer: string; email?: string; plan: string; issued_at: string; expires_at: string };
    expires_at: string;
    in_grace: boolean;
    expired: boolean;
    max_servers: number;
    max_users: number;
    features: string[];
    message?: string;
  };
  usage: { servers: number; users: number };
  trial_available: boolean;
  plans: { id: string; name: string; max_servers: number; max_users: number; features: string[] }[];
  feature_names: Record<string, string>;
};

export function LicenseSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useQuery({ queryKey: ["license"], queryFn: () => api.get<LicenseState>("/license") });
  const [key, setKey] = useState("");
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["license"] });
    qc.invalidateQueries({ queryKey: ["auth-status"] });
  };
  const install = useMutation({
    mutationFn: () => api.put("/license", { key }),
    onSuccess: () => {
      refresh();
      setKey("");
      toast.success(t("License installed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const trial = useMutation({
    mutationFn: () => api.post("/license/trial"),
    onSuccess: () => {
      refresh();
      toast.success(t("Enterprise trial started — 30 days, all features"));
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: () => api.del("/license"),
    onSuccess: () => {
      refresh();
      toast.success(t("License removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  const { state: st, usage, plans, feature_names, trial_available } = q.data!;
  const limit = (n: number) => (n > 0 ? n : "∞");
  const allFeatures = Object.keys(feature_names);

  return (
    <div className="space-y-6">
      <Card>
        <div className="flex flex-wrap items-start gap-5 p-5">
          <span className="flex size-12 items-center justify-center rounded-xl border border-border bg-surface-2">
            <Sparkles className="size-5" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-lg font-semibold tracking-[-0.02em]">{st.plan.name}</span>
              <Badge tone={st.source === "license" ? "success" : st.source === "trial" ? "info" : "neutral"}>
                {st.source === "license" ? t("Licensed") : st.source === "trial" ? t("Trial") : t("Free")}
              </Badge>
              {st.in_grace && <Badge tone="warning">{t("Grace period")}</Badge>}
              {st.expired && <Badge tone="danger">{t("Expired")}</Badge>}
            </div>
            <div className="mt-1 text-[13px] text-muted">
              {st.license ? (
                <>
                  {tx("Licensed to {customer} · {id} · valid until {date}", {
                    customer: <span className="font-medium text-fg">{st.license.customer}</span>,
                    id: st.license.id,
                    date: new Date(st.license.expires_at).toLocaleDateString(intlLocale()),
                  })}
                </>
              ) : st.source === "trial" ? (
                <>{t("Trial ends {date}", { date: new Date(st.expires_at).toLocaleDateString(intlLocale()) })}</>
              ) : (
                t("Free for small teams. Upgrade for multi-server deployments, SSO, approvals and more.")
              )}
            </div>
            {st.message && <div className="mt-2 text-xs text-warning">{st.message}</div>}
          </div>
          <div className="flex gap-2">
            {trial_available && (
              <Button variant="primary" icon={<Sparkles />} loading={trial.isPending} onClick={() => trial.mutate()}>
                {t("Start 30-day Enterprise trial")}
              </Button>
            )}
            {st.license && (
              <Button
                variant="danger"
                icon={<Trash2 />}
                onClick={async () => (await confirm({ title: t("Remove the license?"), description: t("The instance falls back to the Community plan. Nothing is deleted."), danger: true, confirmText: t("Remove") })) && remove.mutate()}
              >
                {t("Remove")}
              </Button>
            )}
          </div>
        </div>
        <div className="grid gap-5 border-t border-border p-5 sm:grid-cols-2">
          {[
            { label: t("Servers"), used: usage.servers, max: st.max_servers },
            { label: t("Active users"), used: usage.users, max: st.max_users },
          ].map((u) => (
            <div key={u.label}>
              <div className="mb-1.5 flex justify-between text-[13px]">
                <span className="text-muted">{u.label}</span>
                <span className="font-medium tabular-nums">
                  {u.used} / {limit(u.max)}
                </span>
              </div>
              <Meter value={u.used} max={u.max > 0 ? u.max : Math.max(u.used, 1) * 4} />
            </div>
          ))}
        </div>
      </Card>

      <Card title={t("Install a license key")} description={t("Keys are verified offline with PortDock's signing key — no connection to a license server is needed.")}>
        <div className="space-y-3 p-5">
          <Textarea rows={3} value={key} onChange={(e) => setKey(e.target.value)} placeholder="SY1.eyJpZCI6…" className="font-mono text-xs" />
          <div className="flex justify-end">
            <Button variant="primary" icon={<KeyRound />} disabled={!key.trim()} loading={install.isPending} onClick={() => install.mutate()}>
              {t("Install license")}
            </Button>
          </div>
        </div>
      </Card>

      <Card title={t("Plans")}>
        <Table>
          <thead>
            <tr>
              <th />
              {plans.map((p) => (
                <th key={p.id} className={p.id === st.plan.id ? "text-fg" : undefined}>
                  {p.name} {p.id === st.plan.id && <Badge tone="accent">{t("current")}</Badge>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              <td className="text-muted">{t("Servers")}</td>
              {plans.map((p) => (
                <td key={p.id}>{limit(p.max_servers)}</td>
              ))}
            </tr>
            <tr>
              <td className="text-muted">{t("Users")}</td>
              {plans.map((p) => (
                <td key={p.id}>{limit(p.max_users)}</td>
              ))}
            </tr>
            <tr>
              <td className="text-muted">{t("Core platform (Git deploys, databases, backups, domains, metrics, alerts, 2FA)")}</td>
              {plans.map((p) => (
                <td key={p.id}>
                  <Check className="size-4 text-success" />
                </td>
              ))}
            </tr>
            {allFeatures.map((f) => (
              <tr key={f}>
                <td className="text-muted">{feature_names[f]}</td>
                {plans.map((p) => (
                  <td key={p.id}>{p.features.includes(f) ? <Check className="size-4 text-success" /> : <span className="text-faint">—</span>}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </Table>
        <div className="flex items-center gap-2 border-t border-border px-5 py-3 text-xs text-muted">
          <Radio className="size-3.5" /> {t("Downgrades never delete configuration or stop running workloads — only adding new paid features is blocked.")}
        </div>
      </Card>
    </div>
  );
}
