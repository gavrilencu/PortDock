import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Download, Search, Send } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { intlLocale, t, tk } from "../lib/i18n";
import type { ActivityEntry } from "../lib/types";
import { timeAgo } from "../lib/utils";
import { useUser } from "./Layout";
import { Badge, Button, Card, Empty, Field, Input, Loading, PasswordInput, Table } from "./ui";

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
  { value: "terminal.", label: tk("Terminal sessions") },
];

function AuditStreaming() {
  const qc = useQueryClient();
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
              <a href={exportURL("csv")}>
                <Button size="sm" icon={<Download />}>
                  CSV
                </Button>
              </a>
              <a href={exportURL("json")}>
                <Button size="sm" icon={<Download />}>
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
