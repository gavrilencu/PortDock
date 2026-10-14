import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CloudDownload, Download, HardDriveDownload, Play, RotateCcw, Save, ShieldCheck, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { api, waitForRestart } from "../lib/api";
import { formatBytes, timeAgo } from "../lib/utils";
import { t, tk } from "../lib/i18n";
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, PasswordInput, Select, Spinner, Switch, Table, useConfirm } from "./ui";
import { useUser } from "./Layout";

type BackupFile = { name: string; size: number; created_at: string };
type S3Object = { name: string; size: number; modified: string };
type InstanceBackup = {
  enabled: boolean;
  schedule: string;
  retention: number;
  upload_s3: boolean;
  passphrase_set: boolean;
  last: { at: string; status: "success" | "failed"; file?: string; message?: string } | null;
  files: BackupFile[];
  s3_configured: boolean;
};

const presets = [
  { value: "0 3 * * *", label: tk("Every day at 03:00") },
  { value: "0 */6 * * *", label: tk("Every 6 hours") },
  { value: "0 * * * *", label: tk("Every hour") },
  { value: "0 3 * * 0", label: tk("Every Sunday at 03:00") },
];

/** Full-screen notice while PortDock restarts to apply a restore. */
export function RestartingOverlay({ open }: { open: boolean }) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-bg/90 backdrop-blur-sm">
      <div className="max-w-sm space-y-3 text-center">
        <Spinner className="mx-auto size-6" />
        <h2 className="text-lg font-semibold">{t("Restoring the backup…")}</h2>
        <p className="text-[13px] text-muted">{t("PortDock restarts with the restored data. This page reloads when it is back.")}</p>
      </div>
    </div>
  );
}

async function afterRestore(setRestarting: (v: boolean) => void) {
  setRestarting(true);
  const back = await waitForRestart();
  if (!back) {
    setRestarting(false);
    toast.error(t("PortDock did not come back. Check that its container restarts (restart: unless-stopped) and look at its logs."));
    return;
  }
  location.href = "/login";
}

/** Settings → Backup storage: scheduled, encrypted backup of PortDock itself. */
export function InstanceBackupCard() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const owner = useUser().role === "owner";
  const q = useQuery({ queryKey: ["instance-backup"], queryFn: () => api.get<InstanceBackup>("/system/backup") });
  const [f, setF] = useState({ enabled: false, schedule: presets[0].value, retention: 7, upload_s3: false, passphrase: "" });
  const [custom, setCustom] = useState(false);
  const [showS3, setShowS3] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restoreName, setRestoreName] = useState<string | null>(null);
  useEffect(() => {
    if (!q.data) return;
    setF({ enabled: q.data.enabled, schedule: q.data.schedule, retention: q.data.retention, upload_s3: q.data.upload_s3, passphrase: "" });
    setCustom(!presets.some((p) => p.value === q.data.schedule));
  }, [q.data]);
  const s3 = useQuery({ queryKey: ["instance-backup-s3"], queryFn: () => api.get<S3Object[]>("/system/backup/s3"), enabled: showS3 });

  const onData = (d: InstanceBackup) => qc.setQueryData(["instance-backup"], d);
  const save = useMutation({
    mutationFn: () => api.put<InstanceBackup>("/system/backup", f),
    onSuccess: (d) => {
      onData(d);
      toast.success(t("Instance backup settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const run = useMutation({
    mutationFn: () => api.post<InstanceBackup>("/system/backup/run"),
    onSuccess: (d) => {
      onData(d);
      if (d.last?.message) toast.warning(t("Backup saved on the server"), { description: d.last.message });
      else toast.success(t("Instance backed up"));
      if (showS3) qc.invalidateQueries({ queryKey: ["instance-backup-s3"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (name: string) => api.del<InstanceBackup>(`/system/backup/files/${encodeURIComponent(name)}`),
    onSuccess: onData,
    onError: (e) => toast.error(e.message),
  });
  const fetchS3 = useMutation({
    mutationFn: (name: string) => api.post<InstanceBackup>(`/system/backup/s3/${encodeURIComponent(name)}/fetch`),
    onSuccess: (d) => {
      onData(d);
      toast.success(t("Copied to the server"));
    },
    onError: (e) => toast.error(e.message),
  });

  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const d = q.data!;

  return (
    <>
      <RestartingOverlay open={restarting} />
      <Card
        title={t("Instance backup")}
        description={t("Backs up PortDock itself: projects, applications, domains, variables, connections and the key that encrypts them. Restore it on a new server from the first-run page.")}
        actions={
          <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} loading={run.isPending} disabled={!d.passphrase_set} onClick={() => run.mutate()}>
            {t("Back up now")}
          </Button>
        }
      >
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <div className="md:col-span-2">
            <Switch checked={f.enabled} onChange={(v) => setF({ ...f, enabled: v })} label={t("Back up on a schedule")} />
          </div>
          <Field label={t("Schedule")} hint={custom ? t("Cron expression (minute hour day month weekday), in the instance timezone.") : undefined}>
            <div className="space-y-2">
              <Select
                value={custom ? "custom" : f.schedule}
                onChange={(e) => {
                  if (e.target.value === "custom") setCustom(true);
                  else {
                    setCustom(false);
                    setF({ ...f, schedule: e.target.value });
                  }
                }}
              >
                {presets.map((p) => (
                  <option key={p.value} value={p.value}>
                    {t(p.label)}
                  </option>
                ))}
                <option value="custom">{t("Custom (cron)")}</option>
              </Select>
              {custom && <Input className="font-mono" value={f.schedule} onChange={(e) => setF({ ...f, schedule: e.target.value })} placeholder="30 2 * * *" />}
            </div>
          </Field>
          <Field label={t("Backups kept")} hint={t("Older backups are deleted, on the server and in S3.")}>
            <Input type="number" min={1} max={365} value={f.retention} onChange={(e) => setF({ ...f, retention: Number(e.target.value) })} />
          </Field>
          <Field
            label={t("Backup passphrase")}
            className="md:col-span-2"
            hint={t("Encrypts the backups. Keep it somewhere safe outside PortDock: without it, a backup cannot be restored. At least 12 characters.")}
          >
            <PasswordInput value={f.passphrase} onChange={(e) => setF({ ...f, passphrase: e.target.value })} placeholder={d.passphrase_set ? t("A passphrase is stored; enter a new one to replace it.") : ""} autoComplete="new-password" />
          </Field>
          <div className="md:col-span-2">
            <Switch
              checked={f.upload_s3}
              disabled={!d.s3_configured && !f.upload_s3}
              onChange={(v) => setF({ ...f, upload_s3: v })}
              label={t("Upload to S3")}
              description={d.s3_configured ? t("Each backup is copied to the S3 destination below (portdock/instance/).") : t("Configure the S3 destination below first.")}
            />
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3 md:col-span-2">
            <LastRun last={d.last} />
            <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        </div>
        <div className="border-t border-border">
          {d.files.length === 0 ? (
            <div className="p-4">
              <Empty compact icon={<ShieldCheck />} title={t("No instance backups yet")} description={t("Set a passphrase, then back up now or on a schedule.")} />
            </div>
          ) : (
            <Table>
              <thead>
                <tr>
                  <th>{t("Backup")}</th>
                  <th className="!text-right">{t("Size")}</th>
                  <th>{t("Created")}</th>
                  <th className="!text-right">{t("Actions")}</th>
                </tr>
              </thead>
              <tbody>
                {d.files.map((b) => (
                  <tr key={b.name}>
                    <td className="font-mono text-[13px]">{b.name}</td>
                    <td className="text-right tabular-nums text-muted">{formatBytes(b.size)}</td>
                    <td className="text-muted" title={b.created_at}>
                      {timeAgo(b.created_at)}
                    </td>
                    <td>
                      <div className="flex justify-end gap-0.5">
                        <a href={`/api/system/backup/files/${encodeURIComponent(b.name)}`} download title={t("Download")} aria-label={t("Download")} className="inline-flex size-7 items-center justify-center rounded-md text-muted hover:bg-surface-2 hover:text-fg">
                          <Download className="size-4" />
                        </a>
                        {owner && (
                          <Button size="icon-sm" variant="ghost" title={t("Restore")} aria-label={t("Restore")} onClick={() => setRestoreName(b.name)}>
                            <RotateCcw />
                          </Button>
                        )}
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          title={t("Delete")}
                          aria-label={t("Delete")}
                          onClick={async () => (await confirm({ title: t("Delete {name}?", { name: b.name }), danger: true, confirmText: t("Delete") })) && del.mutate(b.name)}
                        >
                          <Trash2 className="text-danger" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </div>
        {d.s3_configured && (
          <div className="border-t border-border p-4">
            {!showS3 ? (
              <Button size="sm" icon={<CloudDownload className="size-3.5" />} onClick={() => setShowS3(true)}>
                {t("Show the backups in S3")}
              </Button>
            ) : s3.isLoading ? (
              <Loading rows={2} />
            ) : s3.error ? (
              <ErrorBox error={s3.error} />
            ) : (s3.data ?? []).length === 0 ? (
              <p className="text-[13px] text-muted">{t("No instance backups in S3.")}</p>
            ) : (
              <div className="space-y-2">
                <div className="text-[13px] font-medium">{t("Backups in S3")}</div>
                {(s3.data ?? []).map((o) => (
                  <div key={o.name} className="flex flex-wrap items-center justify-between gap-2 text-[13px]">
                    <span className="font-mono">{o.name}</span>
                    <span className="flex items-center gap-3 text-muted">
                      {formatBytes(o.size)} · {o.modified}
                      {d.files.some((f) => f.name === o.name) ? (
                        <Badge tone="success">{t("On the server")}</Badge>
                      ) : (
                        <Button size="sm" icon={<HardDriveDownload className="size-3.5" />} loading={fetchS3.isPending && fetchS3.variables === o.name} onClick={() => fetchS3.mutate(o.name)}>
                          {t("Copy to the server")}
                        </Button>
                      )}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </Card>
      <RestoreModal name={restoreName} onClose={() => setRestoreName(null)} onRestarting={() => afterRestore(setRestarting)} />
    </>
  );
}

function LastRun({ last }: { last: InstanceBackup["last"] }) {
  if (!last) return <span className="text-[13px] text-subtle">{t("Never backed up")}</span>;
  return (
    <span className="flex items-center gap-2 text-[13px] text-muted">
      {last.status === "success" ? <Badge tone="success">{t("Last backup succeeded")}</Badge> : <Badge tone="danger">{t("Last backup failed")}</Badge>}
      <span title={last.at}>{timeAgo(last.at)}</span>
      {last.message && <span className="max-w-md truncate text-subtle" title={last.message}>· {last.message}</span>}
    </span>
  );
}

function RestoreModal({ name, onClose, onRestarting }: { name: string | null; onClose: () => void; onRestarting: () => void }) {
  const [passphrase, setPassphrase] = useState("");
  const [typed, setTyped] = useState("");
  const restore = useMutation({
    mutationFn: () => api.post(`/system/backup/files/${encodeURIComponent(name!)}/restore`, { passphrase }),
    onSuccess: () => {
      onClose();
      onRestarting();
    },
    onError: (e) => toast.error(e.message),
  });
  useEffect(() => {
    setPassphrase("");
    setTyped("");
  }, [name]);
  return (
    <Modal
      open={!!name}
      onClose={onClose}
      title={t("Restore the instance?")}
      description={t("Everything in PortDock (accounts, projects, settings) goes back to how it was in this backup. Running containers are not touched. PortDock restarts; the current data is kept on the server in restore/replaced-*.")}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="danger" icon={<RotateCcw className="size-4" />} loading={restore.isPending} disabled={typed !== "RESTORE"} onClick={() => restore.mutate()}>
            {t("Restore and restart")}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="font-mono text-[13px]">{name}</div>
        <Field label={t("Backup passphrase")} hint={t("Leave empty to use the stored passphrase.")}>
          <PasswordInput value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
        </Field>
        <Field label={t("Type RESTORE to confirm")}>
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="RESTORE" />
        </Field>
      </div>
    </Modal>
  );
}

/** First-run page: rebuild an instance from a backup instead of creating an owner. */
export function SetupRestore({ onCancel }: { onCancel: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [passphrase, setPassphrase] = useState("");
  const [restarting, setRestarting] = useState(false);
  const restore = useMutation({
    mutationFn: () => {
      const form = new FormData();
      form.append("passphrase", passphrase); // before the file: the server streams it
      form.append("file", file!);
      return api.upload("/setup/restore", form);
    },
    onSuccess: () => afterRestore(setRestarting),
  });
  return (
    <form
      className="w-full max-w-[360px]"
      onSubmit={(e) => {
        e.preventDefault();
        if (file && passphrase) restore.mutate();
      }}
    >
      <RestartingOverlay open={restarting} />
      <h1 className="text-2xl font-semibold tracking-[-0.025em]">{t("Restore from a backup")}</h1>
      <p className="mt-2 text-[13px] leading-relaxed text-subtle">
        {t("Rebuild a PortDock instance from its backup (.tar.gz.age): accounts, projects, settings and connections come back as they were.")}
      </p>
      <div className="mt-8 space-y-4">
        <Field label={t("Backup file")}>
          <label className="flex h-10 cursor-pointer items-center gap-2 rounded-lg border border-dashed border-border-strong px-3 text-[13px] text-muted hover:bg-surface-2">
            <Upload className="size-4 shrink-0" />
            <span className="truncate">{file ? file.name : t("Choose a file")}</span>
            <input type="file" className="sr-only" accept=".age" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          </label>
        </Field>
        <Field label={t("Backup passphrase")}>
          <PasswordInput className="h-10" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} required />
        </Field>
        {restore.error ? <ErrorBox error={restore.error} title={t("Restore failed")} /> : null}
        <Button type="submit" variant="primary" size="lg" className="w-full justify-center" loading={restore.isPending} disabled={!file || !passphrase} icon={<RotateCcw className="size-4" />}>
          {t("Restore instance")}
        </Button>
        <button type="button" onClick={onCancel} className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline">
          {t("Create a new owner account instead")}
        </button>
      </div>
    </form>
  );
}
