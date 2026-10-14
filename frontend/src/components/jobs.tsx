import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, History, Pencil, Play, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail } from "../lib/types";
import { timeAgo } from "../lib/utils";
import { t, tk } from "../lib/i18n";
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Loading, Modal, Select, Switch, Table, Textarea, useConfirm } from "./ui";
import { useComposeServices } from "./Domains";

type Job = {
  id: number;
  name: string;
  schedule: string;
  command: string;
  service: string;
  timeout: number;
  enabled: boolean;
  last_run_at: string;
  last_status: "" | "running" | "success" | "failed" | "timeout";
  next_run_at: string;
};
type Run = { id: number; trigger: string; status: Job["last_status"]; exit_code: number; output?: string; started_at: string; finished_at: string };
type JobForm = { id?: number; name: string; schedule: string; command: string; service: string; timeout: number };

const presets = [
  { value: "*/15 * * * *", label: tk("Every 15 minutes") },
  { value: "0 * * * *", label: tk("Every hour") },
  { value: "0 3 * * *", label: tk("Every day at 03:00") },
  { value: "0 6 * * 1", label: tk("Every Monday at 06:00") },
  { value: "0 0 1 * *", label: tk("On the 1st of every month") },
];
const timeouts = [60, 300, 900, 3600, 21600, 86400];
const timeoutLabel = (s: number) => (s < 3600 ? t("{n} minute|{n} minutes", { n: Math.round(s / 60) }) : t("{n} hour|{n} hours", { n: Math.round(s / 3600) }));

const statusTone = { success: "success", failed: "danger", timeout: "warning", running: "info", "": "neutral" } as const;
const statusLabel = { success: tk("Succeeded"), failed: tk("Failed"), timeout: tk("Timed out"), running: tk("Running"), "": tk("Never run") };

function StatusBadge({ status }: { status: Job["last_status"] }) {
  return <Badge tone={statusTone[status] ?? "neutral"}>{t(statusLabel[status] ?? status)}</Badge>;
}

function duration(r: Run) {
  if (!r.finished_at) return "…";
  const s = Math.max(0, Math.round((new Date(r.finished_at).getTime() - new Date(r.started_at).getTime()) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

/** Application → Jobs: commands run on a schedule. */
export function JobsTab({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const key = ["jobs", app.id];
  const jobs = useQuery({
    queryKey: key,
    queryFn: () => api.get<Job[]>(`/applications/${app.id}/jobs`),
    refetchInterval: (q) => ((q.state.data ?? []).some((j) => j.last_status === "running") ? 3000 : false),
  });
  const [form, setForm] = useState<JobForm | null>(null);
  const [history, setHistory] = useState<Job | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: key });
  const save = useMutation({
    mutationFn: (f: JobForm) => (f.id ? api.patch(`/jobs/${f.id}`, f) : api.post(`/applications/${app.id}/jobs`, f)),
    onSuccess: (_, f) => {
      setForm(null);
      refresh();
      toast.success(f.id ? t("Job updated") : t("Job created"));
    },
    onError: (e) => toast.error(e.message),
  });
  const toggle = useMutation({
    mutationFn: (j: Job) => api.patch(`/jobs/${j.id}`, { enabled: !j.enabled }),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const run = useMutation({
    mutationFn: (j: Job) => api.post(`/jobs/${j.id}/run`),
    onSuccess: (_, j) => {
      refresh();
      toast.success(t("{name} started", { name: j.name }));
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (j: Job) => api.del(`/jobs/${j.id}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  if (jobs.isLoading) return <Loading />;
  if (jobs.error) return <ErrorBox error={jobs.error} />;
  const list = jobs.data ?? [];
  const canWrite = app.access.can_write;
  const canRun = app.access.can_deploy;
  const openNew = () => setForm({ name: "", schedule: presets[2].value, command: "", service: "", timeout: 3600 });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold tracking-[-0.01em]">{t("Scheduled jobs")}</h2>
          <p className="text-[13px] text-subtle">
            {app.build_pack === "compose"
              ? t("Commands run on a schedule inside a running container of the application, in the instance time zone.")
              : t("Commands run on a schedule in a new container of the application's current image, with its variables and persistent storage, in the instance time zone.")}
          </p>
        </div>
        {canWrite && (
          <Button variant="primary" icon={<Plus />} onClick={openNew}>
            {t("Add job")}
          </Button>
        )}
      </div>
      <Card>
        {list.length === 0 ? (
          <div className="p-4">
            <Empty compact icon={<CalendarClock />} title={t("No scheduled jobs")} description={t("Nightly reports, clean-ups, synchronizations: add a command and a schedule.")} />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Job")}</th>
                <th>{t("Schedule")}</th>
                <th>{t("Last run")}</th>
                <th>{t("Next run")}</th>
                <th className="!text-center">{t("Enabled")}</th>
                <th className="!text-right">{t("Actions")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((j) => (
                <tr key={j.id} className={j.enabled ? undefined : "opacity-60"}>
                  <td className="max-w-sm">
                    <div className="font-medium">{j.name}</div>
                    <div className="truncate font-mono text-xs text-subtle" title={j.command}>
                      {j.service ? `${j.service}: ` : ""}
                      {j.command}
                    </div>
                  </td>
                  <td className="font-mono text-[13px]">{j.schedule}</td>
                  <td>
                    <div className="flex items-center gap-2">
                      <StatusBadge status={j.last_status} />
                      {j.last_run_at && <span className="text-xs text-muted">{timeAgo(j.last_run_at)}</span>}
                    </div>
                  </td>
                  <td className="text-xs text-muted">{j.next_run_at ? new Date(j.next_run_at).toLocaleString() : "—"}</td>
                  <td className="text-center">
                    <div className="inline-flex">
                      <Switch checked={j.enabled} disabled={!canWrite} onChange={() => toggle.mutate(j)} />
                    </div>
                  </td>
                  <td>
                    <div className="flex justify-end gap-0.5">
                      {canRun && (
                        <Button size="icon-sm" variant="ghost" title={t("Run now")} aria-label={t("Run now")} disabled={j.last_status === "running"} loading={run.isPending && run.variables?.id === j.id} onClick={() => run.mutate(j)}>
                          <Play />
                        </Button>
                      )}
                      <Button size="icon-sm" variant="ghost" title={t("Runs")} aria-label={t("Runs")} onClick={() => setHistory(j)}>
                        <History />
                      </Button>
                      {canWrite && (
                        <>
                          <Button size="icon-sm" variant="ghost" title={t("Edit")} aria-label={t("Edit")} onClick={() => setForm({ id: j.id, name: j.name, schedule: j.schedule, command: j.command, service: j.service, timeout: j.timeout })}>
                            <Pencil />
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            title={t("Delete")}
                            aria-label={t("Delete")}
                            onClick={async () => (await confirm({ title: t("Delete {name}?", { name: j.name }), description: t("Its run history is deleted too."), danger: true, confirmText: t("Delete") })) && del.mutate(j)}
                          >
                            <Trash2 className="text-danger" />
                          </Button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <JobModal app={app} form={form} onChange={setForm} onSave={() => form && save.mutate(form)} saving={save.isPending} />
      <RunsModal job={history} onClose={() => setHistory(null)} />
    </div>
  );
}

function JobModal({ app, form, onChange, onSave, saving }: { app: ApplicationDetail; form: JobForm | null; onChange: (f: JobForm | null) => void; onSave: () => void; saving: boolean }) {
  const compose = app.build_pack === "compose";
  const services = useComposeServices(app.id, compose && !!form).data?.services ?? [];
  const custom = !!form && !presets.some((p) => p.value === form.schedule);
  const set = (patch: Partial<JobForm>) => form && onChange({ ...form, ...patch });
  return (
    <Modal
      open={!!form}
      onClose={() => onChange(null)}
      size="lg"
      title={form?.id ? t("Edit job") : t("Add job")}
      footer={
        <>
          <Button onClick={() => onChange(null)}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!form?.name.trim() || !form?.command.trim() || !form?.schedule.trim()} onClick={onSave}>
            {form?.id ? t("Save changes") : t("Add job")}
          </Button>
        </>
      }
    >
      {form && (
        <div className="grid gap-4 md:grid-cols-2">
          <Field label={t("Name")} required>
            <Input autoFocus value={form.name} onChange={(e) => set({ name: e.target.value })} placeholder={t("e.g. nightly report")} />
          </Field>
          <Field label={t("Timeout")} hint={t("The run is stopped after this time.")}>
            <Select value={form.timeout} onChange={(e) => set({ timeout: Number(e.target.value) })}>
              {(timeouts.includes(form.timeout) ? timeouts : [...timeouts, form.timeout].sort((a, b) => a - b)).map((s) => (
                <option key={s} value={s}>
                  {timeoutLabel(s)}
                </option>
              ))}
            </Select>
          </Field>
          <Field label={t("Schedule")} required className="md:col-span-2" hint={custom ? t("Cron expression (minute hour day month weekday), or @hourly, @daily, @every 30m.") : undefined}>
            <div className="space-y-2">
              <Select value={custom ? "custom" : form.schedule} onChange={(e) => set({ schedule: e.target.value === "custom" ? "" : e.target.value })}>
                {presets.map((p) => (
                  <option key={p.value} value={p.value}>
                    {t(p.label)}
                  </option>
                ))}
                <option value="custom">{t("Custom (cron)")}</option>
              </Select>
              {custom && <Input className="font-mono" value={form.schedule} onChange={(e) => set({ schedule: e.target.value })} placeholder="30 2 * * *" />}
            </div>
          </Field>
          {compose && (
            <Field label={t("Service")} className="md:col-span-2" hint={t("The command runs inside a running container of this service.")}>
              <Select value={form.service} onChange={(e) => set({ service: e.target.value })}>
                <option value="">{app.compose_service ? t("Main service ({name})", { name: app.compose_service }) : t("Main service")}</option>
                {services.map((s) => (
                  <option key={s.name} value={s.name}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field label={t("Command")} required className="md:col-span-2" hint={t("Run with sh -c; the exit code decides success. The application's variables are available, e.g. $DATABASE_URL.")}>
            <Textarea rows={4} className="font-mono" value={form.command} onChange={(e) => set({ command: e.target.value })} placeholder="npm run report -- --send" />
          </Field>
        </div>
      )}
    </Modal>
  );
}

function RunsModal({ job, onClose }: { job: Job | null; onClose: () => void }) {
  const runs = useQuery({
    queryKey: ["job-runs", job?.id],
    queryFn: () => api.get<Run[]>(`/jobs/${job!.id}/runs`),
    enabled: !!job,
    refetchInterval: (q) => ((q.state.data ?? []).some((r) => r.status === "running") ? 2000 : false),
  });
  const [open, setOpen] = useState<number | null>(null);
  const detail = useQuery({
    queryKey: ["job-run", open],
    queryFn: () => api.get<Run>(`/job-runs/${open}`),
    enabled: open !== null,
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  });
  return (
    <Modal open={!!job} onClose={() => { setOpen(null); onClose(); }} size="xl" title={t("Runs of {name}", { name: job?.name ?? "" })}>
      {runs.isLoading ? (
        <Loading />
      ) : (runs.data ?? []).length === 0 ? (
        <p className="text-sm text-muted">{t("No runs yet.")}</p>
      ) : (
        <div className="space-y-3">
          <div className="max-h-64 overflow-y-auto rounded-lg border border-border">
            <Table>
              <thead>
                <tr>
                  <th>{t("Started")}</th>
                  <th>{t("Status")}</th>
                  <th>{t("Trigger")}</th>
                  <th className="!text-right">{t("Duration")}</th>
                  <th className="!text-right">{t("Exit code")}</th>
                </tr>
              </thead>
              <tbody>
                {(runs.data ?? []).map((r) => (
                  <tr key={r.id} onClick={() => setOpen(r.id)} className={`cursor-pointer ${open === r.id ? "bg-surface-2" : ""}`}>
                    <td className="text-xs" title={r.started_at}>
                      {new Date(r.started_at).toLocaleString()}
                    </td>
                    <td>
                      <StatusBadge status={r.status} />
                    </td>
                    <td className="text-xs text-muted">{r.trigger === "manual" ? t("Manual") : t("Schedule")}</td>
                    <td className="text-right text-xs tabular-nums">{duration(r)}</td>
                    <td className="text-right text-xs tabular-nums">{r.status === "running" ? "…" : r.exit_code}</td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </div>
          {open !== null && (
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-log p-3 font-mono text-2xs text-[#d4d4d8]">
              {detail.data?.output || (detail.isLoading ? "…" : t("No output."))}
            </pre>
          )}
        </div>
      )}
    </Modal>
  );
}
