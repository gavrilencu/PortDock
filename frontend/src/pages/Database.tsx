import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CalendarClock, Database as DatabaseIcon, DatabaseBackup, Download, FlaskConical, Globe, LayoutGrid, Play, Plus, RotateCw, Save, ScrollText, Settings2, Square, SquareTerminal, TerminalSquare, Trash2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Backup, BackupRunsResponse, DatabaseDetail } from "../lib/types";
import { Badge, Button, Card, CopyButton, CopyField, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, StatusBadge, Switch, Table, Tabs, useConfirm } from "../components/ui";
import { ContainerLogs } from "../components/ContainerTable";
import { DNSBadge } from "../components/Domains";
import { Terminal } from "../components/LazyTerminal";
import { cn, formatBytes, timeAgo } from "../lib/utils";
import { MetricsPanel } from "../components/metrics";
import { SQLConsole } from "../components/sqlconsole";
import { intlLocale, t, tk, tMaybe, tx } from "../lib/i18n";
import { PointInTimeRecovery } from "../components/pitr";
import { Replicas } from "../components/replicas";

type Tab = "overview" | "metrics" | "query" | "backups" | "logs" | "terminal" | "settings";

const actionDone: Record<string, string> = {
  start: tk("Database started"),
  stop: tk("Database stopped"),
  restart: tk("Database restarted"),
  recreate: tk("Database recreated"),
};

export function DatabasePage() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "overview";
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({ queryKey: ["database", id], queryFn: () => api.get<DatabaseDetail>(`/databases/${id}`) });
  const action = useMutation({
    mutationFn: (a: string) => api.post(`/databases/${id}/${a}`),
    onSuccess: (_, a) => {
      toast.success(actionDone[a] ? t(actionDone[a]) : t("Done"));
      qc.invalidateQueries({ queryKey: ["database", id] });
    },
    onError: (e) => toast.error(e.message),
  });
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  const db = data!;
  const running = db.status === "running";

  return (
    <>
      <PageHeader
        icon={<DatabaseIcon className="size-5" />}
        title={
          <span className="flex items-center gap-3">
            {db.name} <StatusBadge status={db.status} />
          </span>
        }
        subtitle={`${db.engine_name} · ${db.image} · ${t("on {server}", { server: db.server_name })}`}
        breadcrumbs={
          <>
            <Link to="/projects" className="hover:text-fg">{t("Projects")}</Link> / <Link to={`/projects/${db.project.id}?env=${db.environment_id}`} className="hover:text-fg">{db.project.name}</Link> / <span>{db.environment.name}</span> /{" "}
            <span className="text-fg">{db.name}</span>
          </>
        }
        actions={
          running ? (
            <>
              <Button icon={<RotateCw className="size-4" />} onClick={() => action.mutate("restart")} loading={action.isPending && action.variables === "restart"}>
                {t("Restart")}
              </Button>
              <Button icon={<Square className="size-4" />} onClick={() => action.mutate("stop")} loading={action.isPending && action.variables === "stop"}>
                {t("Stop")}
              </Button>
            </>
          ) : (
            <Button variant="primary" icon={<Play className="size-4" />} onClick={() => action.mutate("start")} loading={action.isPending}>
              {t("Start")}
            </Button>
          )
        }
      />
      <Tabs
        value={tab}
        onChange={(v) => setParams({ tab: v })}
        tabs={[
          { id: "overview", label: t("Connection"), icon: <LayoutGrid /> },
          { id: "metrics", label: t("Metrics"), icon: <Activity /> },
          { id: "query", label: t("SQL console"), icon: <TerminalSquare />, hidden: !["postgres", "mysql", "mariadb", "mssql", "clickhouse"].includes(db.engine) },
          { id: "backups", label: t("Backups"), icon: <DatabaseBackup /> },
          { id: "logs", label: t("Logs"), icon: <ScrollText /> },
          { id: "terminal", label: t("Terminal"), icon: <SquareTerminal /> },
          { id: "settings", label: t("Settings"), icon: <Settings2 /> },
        ]}
      />
      {tab === "overview" && (
        <div className="grid gap-6 lg:grid-cols-2">
          <Card title={t("Internal connection")} description={t("From applications on the same server (Docker network), use host {host}.", { host: db.hostname })}>
            <div className="space-y-4 p-5">
              <CopyField label={t("Connection URL")} value={db.internal_url} secret />
              <div className="grid gap-4 sm:grid-cols-2">
                <CopyField label={t("Host")} value={db.hostname} />
                <CopyField label={t("Port")} value={String(db.port)} />
                {db.username && <CopyField label={t("User")} value={db.username} />}
                <CopyField label={t("Password")} value={db.password} secret />
                {db.database_name && <CopyField label={t("Database")} value={db.database_name} />}
              </div>
            </div>
          </Card>
          <PublicAccess db={db} />
          {db.engine === "postgres" && <Replicas db={db} />}
        </div>
      )}
      {tab === "metrics" && <MetricsPanel scope="database" id={db.id} />}
      {tab === "query" && <SQLConsole db={db} />}
      {tab === "backups" && <Backups db={db} />}
      {tab === "logs" && <ContainerLogs url={`/databases/${id}/logs?tail=500`} className="h-[600px]" />}
      {tab === "terminal" && (running ? <Terminal path={`/servers/${db.server_id}/containers/${db.uuid}/terminal`} /> : <Empty icon={<SquareTerminal />} title={t("Database is not running")} />)}
      {tab === "settings" && <DatabaseSettings db={db} onRecreate={() => action.mutate("recreate")} />}
    </>
  );
}

/** Example command-line client invocation for the public connection, when there is a common one. */
function cliExample(db: DatabaseDetail): { cmd: string; secret: boolean } | null {
  const url = db.public_url;
  switch (db.engine) {
    case "postgres":
      return { cmd: `psql "${url}"`, secret: true };
    case "mysql":
    case "mariadb":
      return { cmd: `mysql -h ${db.public_address} -P ${db.public_port} -u ${db.username} -p${db.database_name ? " " + db.database_name : ""}`, secret: false };
    case "mongodb":
      return { cmd: `mongosh "${url}"`, secret: true };
    case "redis":
    case "valkey":
    case "keydb":
      return { cmd: `redis-cli -u "${url}"`, secret: true };
    default:
      return null;
  }
}

function PublicAccess({ db }: { db: DatabaseDetail }) {
  const qc = useQueryClient();
  const canWrite = db.access.can_write;
  const [enabled, setEnabled] = useState(db.public_port > 0);
  const [port, setPort] = useState(String(db.public_port || db.port));
  const [host, setHost] = useState(db.public_host);
  useEffect(() => {
    setEnabled(db.public_port > 0);
    setPort(String(db.public_port || db.port));
    setHost(db.public_host);
  }, [db.public_port, db.public_host, db.port]);

  const dns = useQuery({
    queryKey: ["database-dns", db.id, db.public_host],
    queryFn: () => api.get<DatabaseDetail>(`/databases/${db.id}?dns=1`),
    enabled: !!db.public_host && db.public_port > 0,
    retry: false,
    staleTime: 60_000,
  });

  const portNum = Number(port);
  const portValid = Number.isInteger(portNum) && portNum >= 1 && portNum <= 65535;
  const wantPort = enabled ? portNum : 0;
  const wantHost = enabled ? host.trim() : db.public_host;
  const dirty = wantPort !== db.public_port || wantHost !== db.public_host;

  const save = useMutation({
    mutationFn: () => api.patch<DatabaseDetail>(`/databases/${db.id}`, { public_port: wantPort, public_host: wantHost }),
    onSuccess: (d) => {
      qc.setQueryData(["database", db.id], d);
      qc.invalidateQueries({ queryKey: ["database", db.id] });
      qc.invalidateQueries({ queryKey: ["database-dns", db.id] });
      if (d.recreated) toast.success(t("Public access saved — the container was recreated"));
      else if (wantPort !== db.public_port && db.status !== "running") toast.success(t("Public access saved — applied on the next start"));
      else toast.success(t("Public access saved"));
    },
    onError: (e) => toast.error(e.message),
  });

  const exposed = db.public_port > 0 && !!db.public_url;
  const example = exposed ? cliExample(db) : null;
  const dnsData = dns.data?.dns_status ? dns.data : null;

  return (
    <Card title={t("Public access")} description={t("Connect to the database from outside the server, e.g. from your computer or another cloud.")}>
      <div className="space-y-4 p-5">
        <Switch
          checked={enabled}
          disabled={!canWrite}
          onChange={setEnabled}
          label={t("Accessible from the internet")}
          description={t("Publishes the database port on the server. Off: only applications on the internal network can connect.")}
        />
        {enabled && (
          <div className="grid gap-4 sm:grid-cols-[8rem_1fr]">
            <Field label={t("Port")} hint={t("Default: {port}", { port: db.port })}>
              <Input type="number" min={1} max={65535} value={port} disabled={!canWrite} onChange={(e) => setPort(e.target.value)} />
            </Field>
            <Field label={t("Domain (optional)")} hint={t("Create a DNS A record pointing to the server; connections use this domain.")}>
              <Input value={host} disabled={!canWrite} onChange={(e) => setHost(e.target.value)} placeholder="db.example.com" className="font-mono" />
            </Field>
          </div>
        )}
        {enabled && db.public_host && db.public_port > 0 && (dns.isFetching || dnsData) && (
          <div className="flex items-center gap-2 text-xs">
            {dns.isFetching ? (
              <span className="text-muted">{t("Checking DNS…")}</span>
            ) : (
              dnsData && (
                <>
                  <DNSBadge d={{ dns_status: dnsData.dns_status!, dns_detail: dnsData.dns_detail ?? "", dns_checked_at: "" }} />
                  <span className="text-subtle">{dnsData.dns_detail}</span>
                </>
              )
            )}
          </div>
        )}
        {dnsData?.dns_status === "proxied" && <p className="text-xs text-warning">{t("Database connections are not HTTP: turn off the Cloudflare proxy (grey cloud) for this record.")}</p>}
        {canWrite && (
          <div className="flex justify-end">
            <Button variant="primary" icon={<Save className="size-4" />} disabled={!dirty || (enabled && !portValid)} loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        )}
        {exposed ? (
          <div className="space-y-4 border-t border-border pt-4">
            <CopyField label={t("Public URL")} value={db.public_url} secret />
            <div className="grid gap-4 sm:grid-cols-2">
              <CopyField label={t("Host")} value={db.public_address} />
              <CopyField label={t("Port")} value={String(db.public_port)} />
            </div>
            {example && <CopyField label={t("Connect from a terminal")} value={example.cmd} secret={example.secret} />}
            {!db.public_host && db.public_address === "localhost" && (
              <p className="flex items-start gap-1.5 text-xs text-muted">
                <Globe className="mt-0.5 size-3.5 shrink-0" />
                {t("The server's public address is unknown. Set a domain above or the public IP in Settings.")}
              </p>
            )}
          </div>
        ) : (
          !enabled && <p className="text-sm text-muted">{t("Not publicly exposed. Only reachable from the internal network.")}</p>
        )}
        {(enabled || exposed) && (
          <div className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning-soft p-3 text-xs text-warning">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            <span>{t("Anyone on the internet can try to connect to this port. Use a strong password and restrict access with a firewall (allow only trusted IP addresses).")}</span>
          </div>
        )}
      </div>
    </Card>
  );
}

const runKind: Record<string, string> = {
  manual: tk("Manual"),
  scheduled: tk("Scheduled"),
  "pre-restore": tk("Before restore"),
};
const runStatus: Record<string, { status: string; label: string }> = {
  pending: { status: "queued", label: tk("Pending") },
  running: { status: "deploying", label: tk("Running") },
  success: { status: "success", label: tk("Success") },
  failed: { status: "failed", label: tk("Failed") },
};

function Backups({ db }: { db: DatabaseDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const backups = useQuery({ queryKey: ["backups", db.id], queryFn: () => api.get<Backup[]>(`/databases/${db.id}/backups`) });
  const runs = useQuery({
    queryKey: ["backup-runs", db.id],
    queryFn: () => api.get<BackupRunsResponse>(`/databases/${db.id}/backup-runs`),
    refetchInterval: (q) =>
      q.state.data?.runs.some((r) => [r.status, r.restore_status, r.verify_status].some((s) => s === "pending" || s === "running")) ? 2000 : 15_000,
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ["backup-runs", db.id] });
  const [edit, setEdit] = useState<Partial<Backup> | null>(null);
  const save = useMutation({
    mutationFn: (b: Partial<Backup>) => (b.id ? api.patch(`/backups/${b.id}`, b) : api.post(`/databases/${db.id}/backups`, b)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["backups", db.id] });
      setEdit(null);
      toast.success(t("Backup schedule saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (bid: number) => api.del(`/backups/${bid}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups", db.id] }),
  });
  const now = useMutation({
    mutationFn: () => api.post(`/databases/${db.id}/backup-now`),
    onSuccess: () => {
      refresh();
      toast.success(t("Backup queued"), { description: t("It starts as soon as other backups of this database have finished.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const restore = useMutation({
    mutationFn: (rid: number) => api.post(`/backup-runs/${rid}/restore`),
    onSuccess: () => {
      refresh();
      toast.success(t("Restore queued"), { description: t("A safety backup of the current data is taken first.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const verify = useMutation({
    mutationFn: (rid: number) => api.post(`/backup-runs/${rid}/verify`),
    onSuccess: () => {
      refresh();
      toast.success(t("Test restore queued"), { description: t("The backup is restored into a temporary container; the database is not touched.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: (rid: number) => api.del(`/backup-runs/${rid}`),
    onSuccess: () => {
      refresh();
      toast.success(t("Backup deleted"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (backups.isLoading || runs.isLoading) return <Loading />;
  const info = runs.data!;
  return (
    <div className="space-y-6">
      <Card
        title={t("Backups")}
        description={tx("Stored on {server} in {directory}. Backups and restores of this database run one at a time; new requests wait as pending.", {
          server: <span className="font-medium text-fg">{info.server}</span>,
          directory: <code className="font-mono text-fg">{info.directory}</code>,
        })}
        actions={
          info.supported && (
            <Button size="sm" variant="primary" icon={<DatabaseBackup className="size-3.5" />} loading={now.isPending} onClick={() => now.mutate()}>
              {t("Back up now")}
            </Button>
          )
        }
      >
        {!info.supported ? (
          <p className="px-5 py-4 text-sm text-muted">{t("Backups are not available for this database engine.")}</p>
        ) : (info.runs ?? []).length === 0 ? (
          <div className="p-5">
            <Empty icon={<DatabaseBackup />} title={t("No backups yet")} description={t("Back up now, or add a schedule below.")} />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Status")}</th>
                <th>{t("Created")}</th>
                <th>{t("Type")}</th>
                <th className="!text-right">{t("Size")}</th>
                <th>{t("File on the server")}</th>
                <th className="!text-right">{t("Actions")}</th>
              </tr>
            </thead>
            <tbody>
              {(info.runs ?? []).map((r) => {
                const st = runStatus[r.status] ?? { status: r.status, label: r.status };
                const testing = r.verify_status === "pending" || r.verify_status === "running";
                const busy = r.status === "pending" || r.status === "running" || r.restore_status === "pending" || r.restore_status === "running" || testing;
                return (
                  <tr key={r.id}>
                    <td className="align-top">
                      <div className="flex flex-col items-start gap-1 py-1">
                        <StatusBadge status={st.status} label={t(st.label)} />
                        {r.restore_status === "pending" && <Badge tone="info">{t("Restore pending")}</Badge>}
                        {r.restore_status === "running" && <Badge tone="info">{r.restore_message ? tMaybe(r.restore_message) : t("Restoring…")}</Badge>}
                        {r.restore_status === "success" && <Badge tone="success">{t("Restored {time}", { time: timeAgo(r.restored_at) })}</Badge>}
                        {r.restore_status === "failed" && <Badge tone="danger">{t("Restore failed")}</Badge>}
                        {r.verify_status === "pending" && <Badge tone="info">{t("Test restore pending")}</Badge>}
                        {r.verify_status === "running" && <Badge tone="info">{r.verify_message ? tMaybe(r.verify_message) : t("Testing…")}</Badge>}
                        {r.verify_status === "success" && (
                          <Badge tone="success" className="gap-1">
                            <FlaskConical className="size-3" />
                            {t("Restore tested {time}", { time: timeAgo(r.verified_at) })}
                          </Badge>
                        )}
                        {r.verify_status === "failed" && <Badge tone="danger">{t("Test restore failed")}</Badge>}
                      </div>
                    </td>
                    <td className="align-top text-xs text-muted" title={new Date(r.created_at).toLocaleString(intlLocale())}>
                      <div className="py-2">{timeAgo(r.created_at)}</div>
                    </td>
                    <td className="align-top text-xs text-muted">
                      <div className="py-2">{runKind[r.kind] ? t(runKind[r.kind]) : r.kind}</div>
                    </td>
                    <td className="align-top text-right text-xs tabular-nums">
                      <div className="py-2">{r.size ? formatBytes(r.size) : "—"}</div>
                    </td>
                    <td className="max-w-md align-top">
                      <div className="py-1.5">
                        {r.file && r.status === "success" ? (
                          <div className="flex min-w-0 items-center gap-1">
                            <code className="truncate font-mono text-xs text-muted" title={r.file}>
                              {r.file.split("/").pop()}
                            </code>
                            <CopyButton value={r.file} />
                          </div>
                        ) : (
                          <span className="text-xs text-faint">{r.status === "failed" ? "" : "—"}</span>
                        )}
                        {r.message && <p className={cn("mt-1 text-xs", r.status === "failed" ? "text-danger" : "text-warning")}>{tMaybe(r.message)}</p>}
                        {r.restore_status === "failed" && r.restore_message && <p className="mt-1 text-xs text-danger">{tMaybe(r.restore_message)}</p>}
                        {r.verify_status === "success" && r.verify_message && <p className="mt-1 text-xs text-muted">{r.verify_message}</p>}
                        {r.verify_status === "failed" && r.verify_message && <p className="mt-1 whitespace-pre-wrap text-xs text-danger">{tMaybe(r.verify_message)}</p>}
                      </div>
                    </td>
                    <td className="align-top">
                      <div className="flex justify-end gap-1 py-1">
                        {r.status === "success" && (
                          <>
                            <a href={`/api/backup-runs/${r.id}/download`} download>
                              <Button size="sm" variant="ghost" icon={<Download className="size-3.5" />}>
                                {t("Download")}
                              </Button>
                            </a>
                            {info.verify && (
                              <Button size="sm" variant="ghost" disabled={busy} loading={verify.isPending && verify.variables === r.id} onClick={() => verify.mutate(r.id)} title={t("Restore into a temporary container and count what it holds")}>
                                <FlaskConical className="size-3.5" />
                                {t("Test restore")}
                              </Button>
                            )}
                            {info.restore && (
                              <Button
                                size="sm"
                                variant="ghost"
                                disabled={busy}
                                loading={restore.isPending && restore.variables === r.id}
                                onClick={async () => {
                                  if (
                                    await confirm({
                                      title: t("Restore this backup?"),
                                      description: t("The data in {name} is replaced by this backup. A safety backup of the current data is taken first, so you can go back.", { name: db.name }),
                                      danger: true,
                                      confirmText: t("Restore"),
                                      typeToConfirm: db.name,
                                    })
                                  )
                                    restore.mutate(r.id);
                                }}
                              >
                                <RotateCw className="size-3.5" />
                                {t("Restore")}
                              </Button>
                            )}
                          </>
                        )}
                        {!busy && (
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            title={t("Delete backup")}
                            onClick={async () => {
                              if (await confirm({ title: t("Delete this backup?"), description: t("The file is removed from the server."), danger: true, confirmText: t("Delete") })) remove.mutate(r.id);
                            }}
                          >
                            <Trash2 className="text-danger" />
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
        )}
        {info.supported && !info.restore && (
          <p className="border-t border-border px-5 py-3 text-xs text-muted">{t("This engine's backups can be downloaded but not restored from PortDock: restore them with the engine's own tools.")}</p>
        )}
      </Card>

      <Card
        title={t("Scheduled backups")}
        description={t("Backups on a schedule, each keeping its last N files, with optional S3 upload.")}
        actions={
          <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setEdit({ schedule: "0 3 * * *", retention: 7, enabled: true, upload_s3: false })}>
            {t("Add schedule")}
          </Button>
        }
      >
        {backups.data!.length === 0 ? (
          <div className="p-5">
            <Empty icon={<CalendarClock />} title={t("No backup schedules")} description={t("Add a schedule to back up this database automatically.")} />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {backups.data!.map((b) => (
              <BackupItem
                key={b.id}
                b={b}
                onRun={refresh}
                onEdit={() => setEdit(b)}
                onDelete={async () => (await confirm({ title: t("Delete this schedule?"), danger: true, confirmText: t("Delete") })) && del.mutate(b.id)}
              />
            ))}
          </div>
        )}
      </Card>
      {db.engine === "postgres" && <PointInTimeRecovery db={db} />}
      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        title={edit?.id ? t("Edit schedule") : t("New backup schedule")}
        footer={
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate(edit!)}>
            {t("Save")}
          </Button>
        }
      >
        {edit && (
          <div className="space-y-4">
            <Field label={t("Cron schedule")} hint={t("Standard 5-field cron (UTC). e.g. 0 3 * * * = daily at 03:00, @hourly, @every 6h")}>
              <Input value={edit.schedule} onChange={(e) => setEdit({ ...edit, schedule: e.target.value })} className="font-mono" />
            </Field>
            <Field label={t("Keep last N backups")}>
              <Input type="number" value={edit.retention} onChange={(e) => setEdit({ ...edit, retention: Number(e.target.value) })} />
            </Field>
            <Switch checked={!!edit.upload_s3} onChange={(v) => setEdit({ ...edit, upload_s3: v })} label={t("Upload to S3")} description={t("Configure the S3 destination in Settings → Backups.")} />
            {info.verify && <VerifySchedule value={edit.verify_schedule ?? ""} onChange={(v) => setEdit({ ...edit, verify_schedule: v })} />}
            <Switch checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label={t("Enabled")} />
          </div>
        )}
      </Modal>
    </div>
  );
}

const verifyPresets = [
  { value: "", label: tk("Never") },
  { value: "0 5 * * 0", label: tk("Test restore weekly") },
  { value: "0 5 * * *", label: tk("Test restore daily") },
];

// VerifySchedule picks when the latest backup of a schedule is test-restored.
function VerifySchedule({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const preset = verifyPresets.some((p) => p.value === value);
  const [custom, setCustom] = useState(!preset);
  return (
    <Field label={t("Test restores")} hint={t("Restores the latest backup into a temporary container (no network, the database untouched), counts its tables and alerts when it fails.")}>
      <div className="flex gap-2">
        <Select
          value={custom ? "custom" : value}
          onChange={(e) => {
            if (e.target.value === "custom") {
              setCustom(true);
              onChange(value || "0 5 * * 0");
            } else {
              setCustom(false);
              onChange(e.target.value);
            }
          }}
        >
          <option value="">{t("Never")}</option>
          <option value="0 5 * * 0">{t("Weekly, Sunday at 05:00")}</option>
          <option value="0 5 * * *">{t("Daily at 05:00")}</option>
          <option value="custom">{t("Custom schedule")}</option>
        </Select>
        {custom && <Input value={value} onChange={(e) => onChange(e.target.value)} className="font-mono" placeholder="0 5 * * 0" />}
      </div>
    </Field>
  );
}

function BackupItem({ b, onRun, onEdit, onDelete }: { b: Backup; onRun: () => void; onEdit: () => void; onDelete: () => void }) {
  const run = useMutation({
    mutationFn: () => api.post(`/backups/${b.id}/run`),
    onSuccess: () => {
      toast.success(t("Backup queued"));
      onRun();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="flex flex-wrap items-center gap-4 px-5 py-3">
      <code className="rounded-md bg-panel-2 px-2 py-1 font-mono text-xs">{b.schedule}</code>
      <div className="flex-1 text-sm text-muted">
        {t("keep {count} · last run {time}", { count: b.retention, time: timeAgo(b.last_run_at) })} {b.upload_s3 && <Badge tone="info">S3</Badge>}{" "}
        {b.verify_schedule && (
          <Badge tone="success" className="gap-1">
            <FlaskConical className="size-3" />
            {verifyPresets.find((p) => p.value === b.verify_schedule) ? t(verifyPresets.find((p) => p.value === b.verify_schedule)!.label) : b.verify_schedule}
          </Badge>
        )}{" "}
        {!b.enabled && <Badge>{t("paused")}</Badge>}
      </div>
      <Button size="sm" icon={<Play className="size-3.5" />} loading={run.isPending} onClick={() => run.mutate()}>
        {t("Run now")}
      </Button>
      <Button size="icon" variant="ghost" onClick={onEdit}>
        <Settings2 className="size-4" />
      </Button>
      <Button size="icon" variant="ghost" onClick={onDelete}>
        <Trash2 className="size-4 text-danger" />
      </Button>
    </div>
  );
}

function DatabaseSettings({ db, onRecreate }: { db: DatabaseDetail; onRecreate: () => void }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const [f, setF] = useState(db);
  useEffect(() => setF(db), [db]);
  const save = useMutation({
    // Public port and domain are edited in the Public access card, not here.
    mutationFn: () => api.patch<DatabaseDetail>(`/databases/${db.id}`, { name: f.name, image: f.image, custom_args: f.custom_args, cpu_limit: f.cpu_limit, memory_limit: f.memory_limit }),
    onSuccess: async (d) => {
      qc.setQueryData(["database", db.id], d);
      if (await confirm({ title: t("Settings saved"), description: t("Recreate the container now to apply image and resource changes? Data is kept in the volume."), confirmText: t("Recreate now") })) onRecreate();
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (volumes: boolean) => api.del(`/databases/${db.id}?volumes=${volumes}`),
    onSuccess: () => nav(`/projects/${db.project.id}?env=${db.environment_id}`),
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="space-y-6">
      <Card title={t("Configuration")}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field label={t("Image")} hint={t("Upgrading major versions may require a manual migration.")}>
            <Input value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} />
          </Field>
          <Field label={t("Custom arguments")} hint={t("Appended to the container command.")}>
            <Input value={f.custom_args} onChange={(e) => setF({ ...f, custom_args: e.target.value })} />
          </Field>
          <Field label={t("CPU limit")}>
            <Input value={f.cpu_limit} onChange={(e) => setF({ ...f, cpu_limit: e.target.value })} placeholder={t("unlimited")} />
          </Field>
          <Field label={t("Memory limit")}>
            <Input value={f.memory_limit} onChange={(e) => setF({ ...f, memory_limit: e.target.value })} placeholder={t("unlimited")} />
          </Field>
          <div className="flex justify-end md:col-span-2">
            <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        </div>
      </Card>
      <Card title={t("Danger zone")} className="border-danger/30">
        <div className="flex flex-wrap items-center justify-between gap-4 p-5">
          <p className="text-sm text-muted">{t("Delete the database container. Optionally delete its data volume too.")}</p>
          <div className="flex gap-2">
            <Button variant="danger" onClick={async () => (await confirm({ title: t("Delete database (keep data)?"), danger: true, confirmText: t("Delete") })) && del.mutate(false)}>
              {t("Delete, keep volume")}
            </Button>
            <Button
              variant="danger"
              icon={<Trash2 className="size-4" />}
              onClick={async () => (await confirm({ title: t("Delete database and ALL data?"), description: t("The data volume will be destroyed."), danger: true, confirmText: t("Delete everything"), typeToConfirm: db.name })) && del.mutate(true)}
            >
              {t("Delete with data")}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
