import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CalendarClock, Database as DatabaseIcon, DatabaseBackup, Download, History, LayoutGrid, Play, Plus, RotateCw, Save, ScrollText, Settings2, Square, SquareTerminal, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Backup, BackupRun, DatabaseDetail } from "../lib/types";
import { Badge, Button, Card, CopyField, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, StatusBadge, Switch, Table, Tabs, useConfirm } from "../components/ui";
import { ContainerLogs } from "../components/ContainerTable";
import { Terminal } from "../components/Terminal";
import { formatBytes, timeAgo } from "../lib/utils";
import { MetricsPanel } from "../components/metrics";

type Tab = "overview" | "metrics" | "backups" | "logs" | "terminal" | "settings";

export function DatabasePage() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "overview";
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({ queryKey: ["database", id], queryFn: () => api.get<DatabaseDetail>(`/databases/${id}`) });
  const action = useMutation({
    mutationFn: (a: string) => api.post(`/databases/${id}/${a}`),
    onSuccess: (_, a) => {
      toast.success(`Database ${a} ok`);
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
        subtitle={`${db.engine_name} · ${db.image} · on ${db.server_name}`}
        breadcrumbs={
          <>
            <Link to="/projects" className="hover:text-fg">Projects</Link> / <Link to={`/projects/${db.project.id}?env=${db.environment_id}`} className="hover:text-fg">{db.project.name}</Link> / <span>{db.environment.name}</span> /{" "}
            <span className="text-fg">{db.name}</span>
          </>
        }
        actions={
          running ? (
            <>
              <Button icon={<RotateCw className="size-4" />} onClick={() => action.mutate("restart")} loading={action.isPending && action.variables === "restart"}>
                Restart
              </Button>
              <Button icon={<Square className="size-4" />} onClick={() => action.mutate("stop")} loading={action.isPending && action.variables === "stop"}>
                Stop
              </Button>
            </>
          ) : (
            <Button variant="primary" icon={<Play className="size-4" />} onClick={() => action.mutate("start")} loading={action.isPending}>
              Start
            </Button>
          )
        }
      />
      <Tabs
        value={tab}
        onChange={(t) => setParams({ tab: t })}
        tabs={[
          { id: "overview", label: "Connection", icon: <LayoutGrid /> },
          { id: "metrics", label: "Metrics", icon: <Activity /> },
          { id: "backups", label: "Backups", icon: <DatabaseBackup /> },
          { id: "logs", label: "Logs", icon: <ScrollText /> },
          { id: "terminal", label: "Terminal", icon: <SquareTerminal /> },
          { id: "settings", label: "Settings", icon: <Settings2 /> },
        ]}
      />
      {tab === "overview" && (
        <div className="grid gap-6 lg:grid-cols-2">
          <Card title="Internal connection" description={`From applications on the same server (Docker network), use host ${db.hostname}.`}>
            <div className="space-y-4 p-5">
              <CopyField label="Connection URL" value={db.internal_url} secret />
              <div className="grid gap-4 sm:grid-cols-2">
                <CopyField label="Host" value={db.hostname} />
                <CopyField label="Port" value={String(db.port)} />
                {db.username && <CopyField label="User" value={db.username} />}
                <CopyField label="Password" value={db.password} secret />
                {db.database_name && <CopyField label="Database" value={db.database_name} />}
              </div>
            </div>
          </Card>
          <Card title="Public access" description="Expose the database on a host port (configure in Settings). Protect it with a firewall.">
            <div className="p-5">
              {db.public_url ? <CopyField label="Public URL" value={db.public_url} secret /> : <p className="text-sm text-muted">Not publicly exposed. Only reachable from the internal network.</p>}
            </div>
          </Card>
        </div>
      )}
      {tab === "metrics" && <MetricsPanel scope="database" id={db.id} />}
      {tab === "backups" && <Backups db={db} />}
      {tab === "logs" && <ContainerLogs url={`/databases/${id}/logs?tail=500`} className="h-[600px]" />}
      {tab === "terminal" && (running ? <Terminal path={`/servers/${db.server_id}/containers/${db.uuid}/terminal`} /> : <Empty icon={<SquareTerminal />} title="Database is not running" />)}
      {tab === "settings" && <DatabaseSettings db={db} onRecreate={() => action.mutate("recreate")} />}
    </>
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
      toast.success("Backup schedule saved");
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
        title="Scheduled backups"
        description="Dumps are stored on the server under the Shipyard data directory, with optional S3 upload."
        actions={
          <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEdit({ schedule: "0 3 * * *", retention: 7, enabled: true, upload_s3: false })}>
            Add schedule
          </Button>
        }
      >
        {backups.data!.length === 0 ? (
          <div className="p-5">
            <Empty icon={<CalendarClock />} title="No backup schedules" description="Create a schedule, or run a one-off backup after adding one." />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {backups.data!.map((b) => (
              <BackupItem key={b.id} b={b} onEdit={() => setEdit(b)} onDelete={async () => (await confirm({ title: "Delete this schedule?", danger: true, confirmText: "Delete" })) && del.mutate(b.id)} db={db} />
            ))}
          </div>
        )}
      </Card>
      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        title={edit?.id ? "Edit schedule" : "New backup schedule"}
        footer={
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate(edit!)}>
            Save
          </Button>
        }
      >
        {edit && (
          <div className="space-y-4">
            <Field label="Cron schedule" hint="Standard 5-field cron (UTC). e.g. 0 3 * * * = daily at 03:00, @hourly, @every 6h">
              <Input value={edit.schedule} onChange={(e) => setEdit({ ...edit, schedule: e.target.value })} className="font-mono" />
            </Field>
            <Field label="Keep last N backups">
              <Input type="number" value={edit.retention} onChange={(e) => setEdit({ ...edit, retention: Number(e.target.value) })} />
            </Field>
            <Switch checked={!!edit.upload_s3} onChange={(v) => setEdit({ ...edit, upload_s3: v })} label="Upload to S3" description="Configure the S3 destination in Settings → Backups." />
            <Switch checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Enabled" />
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
      toast.success("Backup started");
      setOpen(true);
      setTimeout(() => qc.invalidateQueries({ queryKey: ["backup-runs", b.id] }), 1500);
    },
    onError: (e) => toast.error(e.message),
  });
  const restore = useMutation({
    mutationFn: (rid: number) => api.post(`/backup-runs/${rid}/restore`),
    onSuccess: () => toast.success("Restore completed"),
    onError: (e) => toast.error(e.message),
  });
  return (
    <div>
      <div className="flex flex-wrap items-center gap-4 px-5 py-3">
        <code className="rounded-md bg-panel-2 px-2 py-1 font-mono text-xs">{b.schedule}</code>
        <div className="flex-1 text-sm text-muted">
          keep {b.retention} · last run {timeAgo(b.last_run_at)} {b.upload_s3 && <Badge tone="info">S3</Badge>} {!b.enabled && <Badge>paused</Badge>}
        </div>
        <Button size="sm" icon={<History className="size-3.5" />} onClick={() => setOpen(!open)}>
          History
        </Button>
        <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} loading={run.isPending} onClick={() => run.mutate()}>
          Run now
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
            <p className="px-5 py-4 text-sm text-muted">No runs yet.</p>
          ) : (
            <Table>
              <thead>
                <tr>
                  <th>Status</th>
                  <th>When</th>
                  <th>Size</th>
                  <th>File / message</th>
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
                              Download
                            </Button>
                          </a>
                          <Button
                            size="sm"
                            variant="ghost"
                            loading={restore.isPending && restore.variables === r.id}
                            onClick={async () => {
                              if (await confirm({ title: "Restore this backup?", description: `Data in ${db.name} will be overwritten by this dump.`, danger: true, confirmText: "Restore", typeToConfirm: db.name }))
                                restore.mutate(r.id);
                            }}
                          >
                            Restore
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
    mutationFn: () => api.patch<DatabaseDetail>(`/databases/${db.id}`, f),
    onSuccess: async (d) => {
      qc.setQueryData(["database", db.id], d);
      if (await confirm({ title: "Settings saved", description: "Recreate the container now to apply image, port and resource changes? Data is kept in the volume.", confirmText: "Recreate now" })) onRecreate();
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
      <Card title="Configuration">
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label="Name">
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field label="Image" hint="Upgrading major versions may require a manual migration.">
            <Input value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} />
          </Field>
          <Field label="Public port" hint="0 = not exposed">
            <Input type="number" value={f.public_port} onChange={(e) => setF({ ...f, public_port: Number(e.target.value) })} />
          </Field>
          <Field label="Custom arguments" hint="Appended to the container command.">
            <Input value={f.custom_args} onChange={(e) => setF({ ...f, custom_args: e.target.value })} />
          </Field>
          <Field label="CPU limit">
            <Input value={f.cpu_limit} onChange={(e) => setF({ ...f, cpu_limit: e.target.value })} placeholder="unlimited" />
          </Field>
          <Field label="Memory limit">
            <Input value={f.memory_limit} onChange={(e) => setF({ ...f, memory_limit: e.target.value })} placeholder="unlimited" />
          </Field>
          <div className="flex justify-end md:col-span-2">
            <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
              Save
            </Button>
          </div>
        </div>
      </Card>
      <Card title="Danger zone" className="border-danger/30">
        <div className="flex flex-wrap items-center justify-between gap-4 p-5">
          <p className="text-sm text-muted">Delete the database container. Optionally delete its data volume too.</p>
          <div className="flex gap-2">
            <Button variant="danger" onClick={async () => (await confirm({ title: "Delete database (keep data)?", danger: true, confirmText: "Delete" })) && del.mutate(false)}>
              Delete, keep volume
            </Button>
            <Button
              variant="danger"
              icon={<Trash2 className="size-4" />}
              onClick={async () => (await confirm({ title: "Delete database and ALL data?", description: "The data volume will be destroyed.", danger: true, confirmText: "Delete everything", typeToConfirm: db.name })) && del.mutate(true)}
            >
              Delete with data
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
