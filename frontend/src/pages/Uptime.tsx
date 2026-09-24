import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ExternalLink, Lock, MoreHorizontal, Pause, Pencil, Play, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Application, Monitor, MonitorCheck, MonitorIncident, Settings } from "../lib/types";
import { cn, duration, splitList, timeAgo } from "../lib/utils";
import { intlLocale, t } from "../lib/i18n";
import { useUser } from "../components/Layout";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Loading, Menu, MenuItem, MenuSeparator, Modal, PageHeader, Select, StatusDot, Switch, Table, Textarea, useConfirm } from "../components/ui";
import { ResponseBars, UptimeBars, uptimeText, type UptimeDay } from "../components/uptime";

const statusDot: Record<string, string> = { up: "running", down: "failed", pending: "queued", paused: "paused" };

type Draft = Partial<Monitor>;

const emptyDraft = (): Draft => ({ name: "", url: "https://", application_id: 0, interval_seconds: 60, timeout_seconds: 10, expected_status: "200-399", keyword: "", ssl_warn_days: 14, failures_to_alert: 2, enabled: true, public: true });

export function UptimePage() {
  const user = useUser();
  const admin = user.role !== "member";
  const qc = useQueryClient();
  const confirm = useConfirm();
  const monitors = useQuery({ queryKey: ["monitors"], queryFn: () => api.get<Monitor[]>("/monitors"), refetchInterval: 15_000 });
  const [edit, setEdit] = useState<Draft | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["monitors"] });

  const save = useMutation({
    mutationFn: (d: Draft) => (d.id ? api.patch<Monitor>(`/monitors/${d.id}`, d) : api.post<Monitor>("/monitors", d)),
    onSuccess: () => {
      setEdit(null);
      refresh();
      toast.success(t("Monitor saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const toggle = useMutation({
    mutationFn: (m: Monitor) => api.patch(`/monitors/${m.id}`, { enabled: !m.enabled }),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/monitors/${id}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const check = useMutation({
    mutationFn: (id: number) => api.post<{ ok: boolean; status_code: number; response_ms: number; error: string }>(`/monitors/${id}/check`),
    onSuccess: (r) => {
      refresh();
      qc.invalidateQueries({ queryKey: ["monitor"] });
      if (r.ok) toast.success(t("Up · {ms} ms", { ms: r.response_ms }));
      else toast.error(r.error || t("Down"));
    },
    onError: (e) => toast.error(e.message),
  });

  const list = monitors.data ?? [];
  const down = list.filter((m) => m.status === "down").length;

  return (
    <>
      <PageHeader
        title={t("Uptime")}
        subtitle={t("PortDock checks your sites from this server and alerts you when one goes down.")}
        actions={
          admin && (
            <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit(emptyDraft())}>
              {t("Add monitor")}
            </Button>
          )
        }
      />
      <div className="space-y-6">
        {admin && <StatusPageSettings />}
        <Card
          title={t("Monitors")}
          description={list.length ? (down ? t("{n} down|{n} down", { n: down }) : t("All monitors are up")) : undefined}
        >
          {monitors.isLoading ? (
            <div className="p-5">
              <Loading />
            </div>
          ) : list.length === 0 ? (
            <div className="p-5">
              <Empty
                icon={<Activity />}
                title={t("No monitors yet")}
                description={t("Add the URLs of your sites and APIs: status code, keyword, response time and TLS certificate are checked.")}
                action={
                  admin && (
                    <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit(emptyDraft())}>
                      {t("Add monitor")}
                    </Button>
                  )
                }
              />
            </div>
          ) : (
            <div className="divide-y divide-border">
              {list.map((m) => (
                <div key={m.id}>
                  <div className={cn("flex flex-wrap items-center gap-4 px-5 py-3", open === m.id && "bg-surface-2/50")}>
                    <button className="flex min-w-0 flex-1 items-center gap-3 text-left" onClick={() => setOpen(open === m.id ? null : m.id)}>
                      <StatusDot status={statusDot[m.status] ?? "queued"} className="size-2.5" />
                      <div className="min-w-0">
                        <div className="flex items-center gap-2 text-[13px] font-medium">
                          {m.name}
                          {!m.public && <Badge>{t("private")}</Badge>}
                          {m.status === "down" && <Badge tone="danger">{t("Down")}</Badge>}
                          {m.status === "paused" && <Badge tone="warning">{t("Paused")}</Badge>}
                        </div>
                        <div className="truncate text-xs text-subtle">
                          {m.url}
                          {m.last_error && m.status !== "up" && <span className="text-danger"> · {m.last_error}</span>}
                        </div>
                      </div>
                    </button>
                    <ResponseBars checks={m.recent} slots={40} className="hidden w-48 lg:flex" />
                    <div className="w-20 text-right text-xs">
                      <div className="font-medium tabular-nums">{uptimeText(m.uptime_24h)}</div>
                      <div className="text-subtle">24h</div>
                    </div>
                    <div className="w-20 text-right text-xs">
                      <div className="font-medium tabular-nums">{uptimeText(m.uptime_30d)}</div>
                      <div className="text-subtle">{t("30 days")}</div>
                    </div>
                    <div className="hidden w-20 text-right text-xs sm:block">
                      <div className="font-medium tabular-nums">{m.last_response_ms ? `${m.last_response_ms} ms` : "—"}</div>
                      <div className="text-subtle">{m.last_checked_at ? timeAgo(m.last_checked_at) : t("not checked")}</div>
                    </div>
                    <SSLBadge m={m} />
                    {admin && (
                      <Menu
                        trigger={
                          <Button size="icon-sm" variant="ghost" aria-label={t("Actions")}>
                            <MoreHorizontal />
                          </Button>
                        }
                      >
                        <MenuItem icon={<RefreshCw />} onClick={() => check.mutate(m.id)}>
                          {t("Check now")}
                        </MenuItem>
                        <MenuItem icon={<Pencil />} onClick={() => setEdit(m)}>
                          {t("Edit")}
                        </MenuItem>
                        <MenuItem icon={m.enabled ? <Pause /> : <Play />} onClick={() => toggle.mutate(m)}>
                          {m.enabled ? t("Pause") : t("Resume")}
                        </MenuItem>
                        <MenuSeparator />
                        <MenuItem
                          icon={<Trash2 />}
                          danger
                          onClick={async () => {
                            if (await confirm({ title: t("Delete {name}?", { name: m.name }), description: t("Its history and incidents are deleted too."), danger: true, confirmText: t("Delete") })) del.mutate(m.id);
                          }}
                        >
                          {t("Delete")}
                        </MenuItem>
                      </Menu>
                    )}
                  </div>
                  {open === m.id && <MonitorDetail id={m.id} />}
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>
      <MonitorModal draft={edit} onClose={() => setEdit(null)} onSave={(d) => save.mutate(d)} saving={save.isPending} />
    </>
  );
}

function SSLBadge({ m }: { m: Monitor }) {
  if (!m.ssl_expires_at) return <span className="hidden w-24 md:block" />;
  const days = Math.floor((new Date(m.ssl_expires_at).getTime() - Date.now()) / 86_400_000);
  const tone = days < 0 ? "danger" : days <= m.ssl_warn_days ? "warning" : "neutral";
  return (
    <span className="hidden w-24 justify-end md:flex" title={t("TLS certificate valid until {date}", { date: new Date(m.ssl_expires_at).toLocaleDateString(intlLocale()) })}>
      <Badge tone={tone}>
        <Lock className="size-3" /> {days < 0 ? t("expired") : t("{n} days", { n: days })}
      </Badge>
    </span>
  );
}

interface MonitorDetailResponse {
  monitor: Monitor;
  days: (UptimeDay & { checks: number; failures: number; avg_ms: number })[];
  checks: MonitorCheck[];
  incidents: MonitorIncident[];
  uptime_7d: number | null;
  uptime_90d: number | null;
}

function MonitorDetail({ id }: { id: number }) {
  const q = useQuery({ queryKey: ["monitor", id], queryFn: () => api.get<MonitorDetailResponse>(`/monitors/${id}`), refetchInterval: 30_000 });
  if (q.isLoading || !q.data)
    return (
      <div className="px-5 pb-4">
        <Loading rows={2} />
      </div>
    );
  const d = q.data;
  const avg = d.checks.length ? Math.round(d.checks.filter((c) => c.ok).reduce((s, c) => s + c.response_ms, 0) / Math.max(1, d.checks.filter((c) => c.ok).length)) : 0;
  return (
    <div className="space-y-5 border-t border-border bg-surface-2/30 px-5 py-4">
      <div className="grid gap-3 sm:grid-cols-4">
        {[
          [t("24 hours"), uptimeText(d.monitor.uptime_24h)],
          [t("7 days"), uptimeText(d.uptime_7d)],
          [t("30 days"), uptimeText(d.monitor.uptime_30d)],
          [t("Average response (24h)"), avg ? `${avg} ms` : "—"],
        ].map(([k, v]) => (
          <div key={k} className="rounded-lg border border-border bg-surface px-3 py-2">
            <div className="text-xs text-subtle">{k}</div>
            <div className="text-base font-semibold tabular-nums">{v}</div>
          </div>
        ))}
      </div>
      <div>
        <div className="mb-1.5 flex items-center justify-between text-xs text-subtle">
          <span>{t("Last 90 days")}</span>
          <span>{uptimeText(d.uptime_90d)}</span>
        </div>
        <UptimeBars days={d.days} />
      </div>
      {d.checks.length > 0 && (
        <div>
          <div className="mb-1.5 text-xs text-subtle">{t("Response time, last 24 hours")}</div>
          <ResponseBars checks={d.checks.slice(-240)} className="h-16" />
        </div>
      )}
      <div>
        <div className="mb-1.5 text-xs text-subtle">{t("Incidents")}</div>
        {d.incidents.length === 0 ? (
          <p className="text-xs text-muted">{t("No incidents in the last 90 days.")}</p>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Started")}</th>
                <th>{t("Duration")}</th>
                <th>{t("Cause")}</th>
              </tr>
            </thead>
            <tbody>
              {d.incidents.map((i) => (
                <tr key={i.id}>
                  <td className="whitespace-nowrap text-xs">{new Date(i.started_at).toLocaleString(intlLocale())}</td>
                  <td className="whitespace-nowrap text-xs">{i.resolved_at ? duration(i.started_at, i.resolved_at) : <Badge tone="danger">{t("ongoing")}</Badge>}</td>
                  <td className="text-xs text-muted">{i.error}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </div>
    </div>
  );
}

function MonitorModal({ draft, onClose, onSave, saving }: { draft: Draft | null; onClose: () => void; onSave: (d: Draft) => void; saving: boolean }) {
  const [d, setD] = useState<Draft | null>(draft);
  useEffect(() => setD(draft), [draft]);
  const apps = useQuery({ queryKey: ["applications"], queryFn: () => api.get<Application[]>("/applications"), enabled: !!draft });
  const withDomains = useMemo(() => (apps.data ?? []).filter((a) => splitList(a.domains).length > 0), [apps.data]);
  if (!d) return <Modal open={false} onClose={onClose} title="">{null}</Modal>;
  const set = (p: Draft) => setD({ ...d, ...p });
  return (
    <Modal
      open={!!draft}
      onClose={onClose}
      title={d.id ? t("Edit monitor") : t("Add monitor")}
      size="lg"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" icon={<Save className="size-4" />} loading={saving} onClick={() => onSave(d)}>
            {t("Save")}
          </Button>
        </>
      }
    >
      <div className="grid gap-4 md:grid-cols-2">
        {withDomains.length > 0 && (
          <Field label={t("Application")} hint={t("Optional: fills in the URL from its domain.")} className="md:col-span-2">
            <Select
              value={d.application_id ?? 0}
              onChange={(e) => {
                const a = withDomains.find((x) => x.id === Number(e.target.value));
                const host = a ? splitList(a.domains)[0] : "";
                set({ application_id: a?.id ?? 0, ...(a ? { url: host.includes("://") ? host : `${a.https ? "https" : "http"}://${host}`, name: d.name || a.name } : {}) });
              }}
            >
              <option value={0}>{t("None")}</option>
              {withDomains.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} — {splitList(a.domains)[0]}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <Field label={t("Name")} required>
          <Input autoFocus value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder={t("Website")} />
        </Field>
        <Field label={t("URL")} required>
          <Input value={d.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://example.com/health" />
        </Field>
        <Field label={t("Check every")}>
          <Select value={d.interval_seconds} onChange={(e) => set({ interval_seconds: Number(e.target.value), timeout_seconds: Math.min(d.timeout_seconds ?? 10, Number(e.target.value) - 1) })}>
            {[30, 60, 120, 300, 600, 1800, 3600].map((s) => (
              <option key={s} value={s}>
                {s < 60 ? t("{n} seconds", { n: s }) : s < 3600 ? t("{n} minute|{n} minutes", { n: s / 60 }) : t("1 hour")}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Timeout (seconds)")}>
          <Input type="number" min={1} max={60} value={d.timeout_seconds} onChange={(e) => set({ timeout_seconds: Number(e.target.value) })} />
        </Field>
        <Field label={t("Accepted status codes")} hint={t("e.g. 200-399, 200,204 or 2xx")}>
          <Input value={d.expected_status} onChange={(e) => set({ expected_status: e.target.value })} className="font-mono" />
        </Field>
        <Field label={t("Keyword")} hint={t("Optional: the page must contain this text.")}>
          <Input value={d.keyword} onChange={(e) => set({ keyword: e.target.value })} />
        </Field>
        <Field label={t("Alert after failed checks")} hint={t("Consecutive failures before the monitor is down.")}>
          <Input type="number" min={1} max={10} value={d.failures_to_alert} onChange={(e) => set({ failures_to_alert: Number(e.target.value) })} />
        </Field>
        <Field label={t("Warn before the certificate expires (days)")} hint={t("0 turns the warning off.")}>
          <Input type="number" min={0} max={90} value={d.ssl_warn_days} onChange={(e) => set({ ssl_warn_days: Number(e.target.value) })} />
        </Field>
        <div className="space-y-3 md:col-span-2">
          <Switch checked={!!d.public} onChange={(v) => set({ public: v })} label={t("Show on the status page")} description={t("Only the name and the uptime are public, never the URL.")} />
          <Switch checked={!!d.enabled} onChange={(v) => set({ enabled: v })} label={t("Enabled")} />
        </div>
        <p className="text-xs text-subtle md:col-span-2">{t("Alerts use the notification channels subscribed to monitor.down, monitor.up and monitor.ssl_expiring (Settings → Notifications).")}</p>
      </div>
    </Modal>
  );
}

function StatusPageSettings() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<Settings>("/settings") });
  const [f, setF] = useState<{ status_page_enabled: string; status_page_title: string; status_page_description: string } | null>(null);
  useEffect(() => {
    if (settings.data && !f)
      setF({ status_page_enabled: settings.data.status_page_enabled ?? "", status_page_title: settings.data.status_page_title ?? "", status_page_description: settings.data.status_page_description ?? "" });
  }, [settings.data, f]);
  const save = useMutation({
    mutationFn: (v: typeof f) => api.patch<Settings>("/settings", v),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      toast.success(t("Status page saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (!f || !settings.data) return null;
  const on = f.status_page_enabled === "true";
  const link = `${settings.data.public_url.replace(/\/$/, "")}/status`;
  const dirty =
    f.status_page_enabled !== (settings.data.status_page_enabled ?? "") ||
    f.status_page_title !== (settings.data.status_page_title ?? "") ||
    f.status_page_description !== (settings.data.status_page_description ?? "");
  return (
    <Card
      title={t("Status page")}
      description={t("A public page for your customers with the state and the 90-day uptime of the public monitors.")}
      actions={
        <>
          {on && (
            <a href="/status" target="_blank" rel="noreferrer">
              <Button size="sm" icon={<ExternalLink className="size-3.5" />}>
                {t("Open")}
              </Button>
            </a>
          )}
          {dirty && (
            <Button size="sm" variant="primary" icon={<Save className="size-3.5" />} loading={save.isPending} onClick={() => save.mutate(f)}>
              {t("Save")}
            </Button>
          )}
        </>
      }
    >
      <div className="grid gap-4 p-5 md:grid-cols-2">
        <div className="md:col-span-2">
          <Switch checked={on} onChange={(v) => setF({ ...f, status_page_enabled: v ? "true" : "" })} label={t("Publish the status page")} />
        </div>
        {on && (
          <>
            <Field label={t("Title")}>
              <Input value={f.status_page_title} onChange={(e) => setF({ ...f, status_page_title: e.target.value })} placeholder={settings.data.instance_name || "Acme status"} />
            </Field>
            <CopyField label={t("Address")} value={link} />
            <Field label={t("Description")} className="md:col-span-2">
              <Textarea rows={2} value={f.status_page_description} onChange={(e) => setF({ ...f, status_page_description: e.target.value })} placeholder={t("Live status of our services.")} />
            </Field>
          </>
        )}
      </div>
    </Card>
  );
}
