import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CalendarClock, Database as DatabaseIcon, DatabaseBackup, Download, Globe, History, LayoutGrid, Play, Plus, RotateCw, Save, ScrollText, Settings2, Square, SquareTerminal, Trash2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Backup, BackupRun, DatabaseDetail } from "../lib/types";
import { Badge, Button, Card, CopyField, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, StatusBadge, Switch, Table, Tabs, useConfirm } from "../components/ui";
import { ContainerLogs } from "../components/ContainerTable";
import { DNSBadge } from "../components/Domains";
import { Terminal } from "../components/Terminal";
import { formatBytes, timeAgo } from "../lib/utils";
import { MetricsPanel } from "../components/metrics";
import { t, tk } from "../lib/i18n";

type Tab = "overview" | "metrics" | "backups" | "logs" | "terminal" | "settings";

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
        </div>
      )}
      {tab === "metrics" && <MetricsPanel scope="database" id={db.id} />}
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

function Backups({ db }: { db: DatabaseDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const backups = useQuery({ queryKey: ["backups", db.id], queryFn: () => api.get<Backup[]>(`/databases/${db.id}/backups`) });
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
  if (backups.isLoading) return <Loading />;
  return (
    <div className="space-y-6">
      <Card
        title={t("Scheduled backups")}
        description={t("Dumps are stored on the server under the PortDock data directory, with optional S3 upload.")}
        actions={
          <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEdit({ schedule: "0 3 * * *", retention: 7, enabled: true, upload_s3: false })}>
            {t("Add schedule")}
          </Button>
        }
      >
        {backups.data!.length === 0 ? (
          <div className="p-5">
            <Empty icon={<CalendarClock />} title={t("No backup schedules")} description={t("Create a schedule, or run a one-off backup after adding one.")} />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {backups.data!.map((b) => (
              <BackupItem key={b.id} b={b} onEdit={() => setEdit(b)} onDelete={async () => (await confirm({ title: t("Delete this schedule?"), danger: true, confirmText: t("Delete") })) && del.mutate(b.id)} db={db} />
            ))}
          </div>
        )}
      </Card>
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
            <Switch checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label={t("Enabled")} />
          </div>
        )}
      </Modal>
    </div>
  );
}

function BackupItem({ b, db, onEdit, onDelete }: { b: Backup; db: DatabaseDetail; onEdit: () => void; onDelete: () => void }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [open, setOpen] = useState(false);
  const runs = useQuery({ queryKey: ["backup-runs", b.id], queryFn: () => api.get<BackupRun[]>(`/backups/${b.id}/runs`), enabled: open, refetchInterval: open ? 5000 : false });
  const run = useMutation({
    mutationFn: () => api.post(`/backups/${b.id}/run`),
    onSuccess: () => {
      toast.success(t("Backup started"));
      setOpen(true);
      setTimeout(() => qc.invalidateQueries({ queryKey: ["backup-runs", b.id] }), 1500);
    },
    onError: (e) => toast.error(e.message),
  });
  const restore = useMutation({
    mutationFn: (rid: number) => api.post(`/backup-runs/${rid}/restore`),
    onSuccess: () => toast.success(t("Restore completed")),
    onError: (e) => toast.error(e.message),
  });
  return (
    <div>
      <div className="flex flex-wrap items-center gap-4 px-5 py-3">
        <code className="rounded-md bg-panel-2 px-2 py-1 font-mono text-xs">{b.schedule}</code>
        <div className="flex-1 text-sm text-muted">
          {t("keep {count} · last run {time}", { count: b.retention, time: timeAgo(b.last_run_at) })} {b.upload_s3 && <Badge tone="info">S3</Badge>} {!b.enabled && <Badge>{t("paused")}</Badge>}
        </div>
        <Button size="sm" icon={<History className="size-3.5" />} onClick={() => setOpen(!open)}>
          {t("History")}
        </Button>
        <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} loading={run.isPending} onClick={() => run.mutate()}>
          {t("Run now")}
        </Button>
        <Button size="icon" variant="ghost" onClick={onEdit}>
          <Settings2 className="size-4" />
        </Button>
        <Button size="icon" variant="ghost" onClick={onDelete}>
          <Trash2 className="size-4 text-danger" />
        </Button>
      </div>
      {open && (
        <div className="border-t border-border bg-panel-2/30">
          {runs.isLoading ? (
            <Loading />
          ) : !runs.data?.length ? (
            <p className="px-5 py-4 text-sm text-muted">{t("No runs yet.")}</p>
          ) : (
            <Table>
              <thead>
                <tr>
                  <th>{t("Status")}</th>
                  <th>{t("When")}</th>
                  <th>{t("Size")}</th>
                  <th>{t("File / message")}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {runs.data.map((r) => (
                  <tr key={r.id}>
                    <td>
                      <StatusBadge status={r.status === "running" ? "deploying" : r.status} />
                    </td>
                    <td className="text-xs text-muted">{timeAgo(r.created_at)}</td>
                    <td className="text-xs tabular-nums">{r.size ? formatBytes(r.size) : "—"}</td>
                    <td className="max-w-md truncate font-mono text-xs text-muted" title={r.message || r.file}>
                      {r.message || r.file.split("/").pop()}
                    </td>
                    <td>
                      {r.status === "success" && (
                        <div className="flex justify-end gap-1">
                          <a href={`/api/backup-runs/${r.id}/download`}>
                            <Button size="sm" variant="ghost" icon={<Download className="size-3.5" />}>
                              {t("Download")}
                            </Button>
                          </a>
                          <Button
                            size="sm"
                            variant="ghost"
                            loading={restore.isPending && restore.variables === r.id}
                            onClick={async () => {
                              if (await confirm({ title: t("Restore this backup?"), description: t("Data in {name} will be overwritten by this dump.", { name: db.name }), danger: true, confirmText: t("Restore"), typeToConfirm: db.name }))
                                restore.mutate(r.id);
                            }}
                          >
                            {t("Restore")}
                          </Button>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </div>
      )}
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
