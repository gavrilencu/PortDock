import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DatabaseBackup, History, Settings2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { DatabaseDetail } from "../lib/types";
import { intlLocale, t, tMaybe } from "../lib/i18n";
import { formatBytes, timeAgo } from "../lib/utils";
import { Badge, Button, Card, Field, Input, Modal, StatusBadge, Switch, Table, useConfirm } from "./ui";

// Point-in-time recovery of a PostgreSQL database (Backups tab).

interface PITRBackup {
  id: number;
  status: "running" | "success" | "failed";
  file: string;
  size: number;
  start_wal: string;
  message: string;
  started_at: string;
  finished_at: string;
}

interface PITRRestore {
  id: number;
  target_time: string;
  status: "pending" | "running" | "success" | "failed";
  message: string;
  timeline: number;
  finished_at: string;
}

interface PITRInfo {
  supported: boolean;
  schedule: string;
  retention: number;
  s3: boolean;
  s3_configured: boolean;
  synced_at: string;
  archive_error: string;
  status: {
    enabled: boolean;
    from: string;
    to: string;
    archived_count: number;
    last_archived_wal: string;
    last_archived_at: string;
    failed_count: number;
    backups: PITRBackup[];
    restore: PITRRestore | null;
    directory: string;
  };
}

const when = (iso: string) => (iso ? new Date(iso).toLocaleString(intlLocale()) : "—");

// toLocalInput formats a time for <input type="datetime-local">.
function toLocalInput(iso: string) {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export function PointInTimeRecovery({ db }: { db: DatabaseDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const key = ["pitr", db.id];
  const q = useQuery({
    queryKey: key,
    queryFn: () => api.get<PITRInfo>(`/databases/${db.id}/pitr`),
    refetchInterval: (s) => {
      const st = s.state.data?.status;
      const busy = st?.backups.some((b) => b.status === "running") || st?.restore?.status === "pending" || st?.restore?.status === "running";
      return busy ? 3000 : 20_000;
    },
  });
  const [settings, setSettings] = useState<{ enabled: boolean; schedule: string; retention: number; s3: boolean } | null>(null);
  const [target, setTarget] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: key });
  const save = useMutation({
    mutationFn: (body: { enabled: boolean; schedule: string; retention: number; s3: boolean }) => api.put(`/databases/${db.id}/pitr`, body),
    onSuccess: (_, body) => {
      refresh();
      setSettings(null);
      toast.success(body.enabled ? t("Point-in-time recovery saved") : t("Point-in-time recovery turned off"));
    },
    onError: (e) => toast.error(e.message),
  });
  const backup = useMutation({
    mutationFn: () => api.post(`/databases/${db.id}/pitr/backup`),
    onSuccess: () => {
      refresh();
      toast.success(t("Base backup started"));
    },
    onError: (e) => toast.error(e.message),
  });
  const restore = useMutation({
    mutationFn: (iso: string) => api.post(`/databases/${db.id}/pitr/restore`, { target_time: iso }),
    onSuccess: () => {
      refresh();
      setTarget(null);
      toast.success(t("Point-in-time restore started"), { description: t("The database restarts and replays its changes up to that time.") });
    },
    onError: (e) => toast.error(e.message),
  });
  if (!q.data?.supported) return null;
  const info = q.data;
  const st = info.status;
  const restoring = st.restore?.status === "pending" || st.restore?.status === "running";
  const canRestore = st.enabled && !!st.from && !!st.to && db.status === "running";
  const openSettings = (enabled: boolean) => setSettings({ enabled, schedule: info.schedule || "0 2 * * *", retention: info.retention || 7, s3: info.s3 });

  return (
    <Card
      title={t("Point-in-time recovery")}
      description={t("Continuous WAL archiving and scheduled base backups: restore the database as it was at any second of the recovery window, for example just before a bad migration or a deleted table.")}
      actions={
        st.enabled ? (
          <div className="flex flex-wrap gap-2">
            <Button size="sm" icon={<DatabaseBackup className="size-3.5" />} loading={backup.isPending} onClick={() => backup.mutate()}>
              {t("Base backup now")}
            </Button>
            <Button size="sm" variant="primary" icon={<History className="size-3.5" />} disabled={!canRestore || restoring} onClick={() => setTarget(toLocalInput(st.to))}>
              {t("Restore to a point in time")}
            </Button>
            <Button size="icon-sm" variant="ghost" title={t("Settings")} onClick={() => openSettings(true)}>
              <Settings2 />
            </Button>
          </div>
        ) : (
          <Button size="sm" variant="primary" onClick={() => openSettings(true)}>
            {t("Enable")}
          </Button>
        )
      }
    >
      {!st.enabled ? (
        <p className="px-5 py-4 text-sm text-muted">
          {t("Off. Logical backups restore the state of a backup; point-in-time recovery reaches any moment in between. Enabling it restarts the database once.")}
        </p>
      ) : (
        <div className="space-y-4 p-5">
          <div className="grid gap-3 text-sm sm:grid-cols-3">
            <div>
              <div className="text-2xs uppercase tracking-wider text-faint">{t("Recovery window")}</div>
              <div className="mt-1">{st.from ? `${when(st.from)} → ${when(st.to)}` : t("After the first base backup")}</div>
            </div>
            <div>
              <div className="text-2xs uppercase tracking-wider text-faint">{t("WAL archiving")}</div>
              <div className="mt-1 flex flex-wrap items-center gap-2">
                {info.archive_error ? <Badge tone="danger">{t("Failing")}</Badge> : <Badge tone="success">{t("Working")}</Badge>}
                <span className="text-xs text-muted">{st.last_archived_at ? t("last segment {time}", { time: timeAgo(st.last_archived_at) }) : "—"}</span>
              </div>
              {info.archive_error && <p className="mt-1 text-xs text-danger">{info.archive_error}</p>}
            </div>
            <div>
              <div className="text-2xs uppercase tracking-wider text-faint">{t("Schedule")}</div>
              <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
                <code className="rounded-md bg-panel-2 px-1.5 py-0.5 font-mono">{info.schedule}</code>
                <span className="text-muted">{t("keep {count}", { count: info.retention })}</span>
                {info.s3 && <Badge tone="info">{info.synced_at ? t("S3 · synced {time}", { time: timeAgo(info.synced_at) }) : "S3"}</Badge>}
              </div>
            </div>
          </div>
          {st.restore && (
            <div className="rounded-lg border border-border bg-surface-2/60 px-3 py-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge status={{ pending: "queued", running: "deploying", success: "success", failed: "failed" }[st.restore.status]} label={t("Restore to {time}", { time: when(st.restore.target_time) })} />
                {st.restore.finished_at && <span className="text-muted">{timeAgo(st.restore.finished_at)}</span>}
              </div>
              {st.restore.message && <p className={st.restore.status === "failed" ? "mt-1 text-danger" : "mt-1 text-muted"}>{tMaybe(st.restore.message)}</p>}
            </div>
          )}
          {st.backups.length > 0 && (
            <Table>
              <thead>
                <tr>
                  <th>{t("Base backup")}</th>
                  <th>{t("Finished")}</th>
                  <th className="!text-right">{t("Size")}</th>
                  <th>{t("First WAL segment")}</th>
                </tr>
              </thead>
              <tbody>
                {st.backups.map((b) => (
                  <tr key={b.id}>
                    <td>
                      <StatusBadge status={{ running: "deploying", success: "success", failed: "failed" }[b.status]} label={b.status === "running" ? t("Running") : b.status === "success" ? t("Success") : t("Failed")} />
                      {b.message && <p className="mt-1 text-xs text-danger">{b.message}</p>}
                    </td>
                    <td className="text-xs text-muted" title={when(b.finished_at)}>
                      {b.finished_at ? timeAgo(b.finished_at) : "—"}
                    </td>
                    <td className="text-right text-xs tabular-nums">{b.size ? formatBytes(b.size) : "—"}</td>
                    <td className="font-mono text-2xs text-muted">{b.start_wal || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
          <p className="text-xs text-muted">{t("Archive and base backups: {dir} on the database's server.", { dir: st.directory })}</p>
        </div>
      )}

      <Modal
        open={!!settings}
        onClose={() => setSettings(null)}
        title={t("Point-in-time recovery")}
        footer={
          settings && (
            <>
              {st.enabled && (
                <Button
                  variant="danger"
                  onClick={async () => {
                    if (await confirm({ title: t("Turn off point-in-time recovery?"), description: t("WAL archiving stops (the database restarts once). The archive and base backups already taken stay on the server."), danger: true, confirmText: t("Turn off") }))
                      save.mutate({ ...settings, enabled: false });
                  }}
                >
                  {t("Turn off")}
                </Button>
              )}
              <Button variant="primary" loading={save.isPending} onClick={() => save.mutate({ ...settings, enabled: true })}>
                {st.enabled ? t("Save") : t("Enable and restart the database")}
              </Button>
            </>
          )
        }
      >
        {settings && (
          <div className="space-y-4">
            <Field label={t("Base backups")} hint={t("Cron, in the instance time zone. A base backup a day keeps recoveries short.")}>
              <Input value={settings.schedule} onChange={(e) => setSettings({ ...settings, schedule: e.target.value })} className="font-mono" />
            </Field>
            <Field label={t("Keep the last N base backups")} hint={t("The recovery window starts at the oldest one kept; older WAL is removed.")}>
              <Input type="number" min={1} max={365} value={settings.retention} onChange={(e) => setSettings({ ...settings, retention: Number(e.target.value) })} />
            </Field>
            <Switch
              checked={settings.s3}
              disabled={!info.s3_configured}
              onChange={(v) => setSettings({ ...settings, s3: v })}
              label={t("Copy to S3")}
              description={info.s3_configured ? t("The archive and base backups are mirrored to the bucket every 5 minutes and after each base backup.") : t("Configure the S3 destination in Settings → Backup storage.")}
            />
          </div>
        )}
      </Modal>

      <RestoreModal
        open={target !== null}
        value={target ?? ""}
        from={st.from}
        to={st.to}
        name={db.name}
        loading={restore.isPending}
        onChange={setTarget}
        onClose={() => setTarget(null)}
        onRestore={(iso) => restore.mutate(iso)}
      />
    </Card>
  );
}

function RestoreModal(props: { open: boolean; value: string; from: string; to: string; name: string; loading: boolean; onChange: (v: string) => void; onClose: () => void; onRestore: (iso: string) => void }) {
  const [typed, setTyped] = useState("");
  useEffect(() => setTyped(""), [props.open]);
  const iso = props.value ? new Date(props.value).toISOString() : "";
  const inWindow = !!iso && iso >= props.from && iso <= new Date().toISOString();
  return (
    <Modal
      open={props.open}
      onClose={props.onClose}
      title={t("Restore to a point in time")}
      description={t("The database stops, its data is replaced by the state at this time and it starts again. The current data is kept in the data volume until the next restore, and put back automatically if the recovery fails.")}
      footer={
        <Button variant="danger" disabled={!inWindow || typed !== props.name} loading={props.loading} onClick={() => props.onRestore(iso)}>
          {t("Restore")}
        </Button>
      }
    >
      <div className="space-y-4">
        <Field label={t("Time (your time zone)")} hint={props.from ? t("Between {from} and {to}.", { from: when(props.from), to: when(props.to) }) : undefined}>
          <Input type="datetime-local" step={1} value={props.value} min={props.from ? toLocalInput(props.from) : undefined} max={props.to ? toLocalInput(props.to) : undefined} onChange={(e) => props.onChange(e.target.value)} />
        </Field>
        <Field label={t("Type {name} to confirm", { name: props.name })}>
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
        </Field>
      </div>
    </Modal>
  );
}
