import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpCircle, CheckCircle2, ExternalLink, Loader2, RefreshCw, Save, ShieldCheck, XCircle } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "../lib/api";
import { intlLocale, t, tk, tx } from "../lib/i18n";
import { cn, timeAgo } from "../lib/utils";
import { Badge, Button, Card, Field, Input, Loading, Modal, PasswordInput, Select, Switch } from "./ui";

export type Release = { tag: string; name: string; notes: string; url: string; published_at: string; prerelease: boolean };
const statusLabel: Record<string, string> = { running: tk("running"), success: tk("success"), rolled_back: tk("rolled back"), failed: tk("failed") };
type UpdateState = { id: string; status: "" | "running" | "success" | "rolled_back" | "failed"; from: string; to: string; image: string; steps: { time: string; message: string }[]; error?: string; backup?: string; started_at: string; finished_at?: string };
export type UpdateInfo = {
  current: string;
  update_available: boolean;
  latest: Release | null;
  checked_at?: string;
  error?: string;
  state?: UpdateState;
  supported?: boolean;
  unsupported_reason?: string;
  settings?: { repo: string; image: string; default_image: string; channel: string; auto: boolean; token_set: boolean; official_repo: string; overridden: boolean };
};

// Checked every time PortDock is opened or its tab regains focus (the server
// asks GitHub at most every 2 minutes), and every 15 minutes while it stays open.
export function useUpdateInfo() {
  return useQuery({
    queryKey: ["system-update"],
    queryFn: () => api.get<UpdateInfo>("/system/update?fresh=1"),
    refetchInterval: 15 * 60_000,
    refetchOnWindowFocus: "always",
    staleTime: 60_000,
  });
}

/** Minimal, safe rendering of release notes (headings, bullets, code, links as text). */
function Notes({ text }: { text: string }) {
  if (!text.trim()) return <p className="text-[13px] text-muted">{t("No release notes.")}</p>;
  return (
    <div className="max-h-72 space-y-1 overflow-y-auto rounded-lg border border-border bg-surface-2/50 p-4 text-[13px] leading-relaxed">
      {text.split("\n").map((line, i) => {
        const l = line.trimEnd();
        if (/^#{1,6}\s/.test(l)) return <div key={i} className="pt-2 font-semibold first:pt-0">{l.replace(/^#+\s*/, "")}</div>;
        if (/^\s*[-*]\s/.test(l)) return <div key={i} className="flex gap-2 pl-1"><span className="text-faint">•</span><span>{l.replace(/^\s*[-*]\s/, "").replace(/\*\*/g, "")}</span></div>;
        if (!l) return <div key={i} className="h-1" />;
        return <div key={i} className="text-muted">{l.replace(/\*\*/g, "")}</div>;
      })}
    </div>
  );
}

function Progress({ state, target, onDone }: { state?: UpdateState; target: string; onDone: () => void }) {
  const [phase, setPhase] = useState<"working" | "restarting" | "done" | "failed">("working");
  const [steps, setSteps] = useState(state?.steps ?? []);
  const [error, setError] = useState("");
  const done = useRef(false);
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      if (stop || done.current) return;
      try {
        const info = await api.get<UpdateInfo>("/system/update");
        const st = info.state;
        if (st?.steps) setSteps(st.steps);
        if (info.current === target) {
          done.current = true;
          setPhase("done");
          setTimeout(onDone, 1500);
          return;
        }
        if (st && (st.status === "failed" || st.status === "rolled_back") && st.to === target) {
          done.current = true;
          setPhase("failed");
          setError(st.error ?? t("Update failed"));
          return;
        }
        setPhase(phase === "restarting" ? "restarting" : "working");
      } catch {
        setPhase("restarting"); // PortDock is being replaced
      }
      setTimeout(tick, 2000);
    };
    const timer = setTimeout(tick, 1500);
    return () => {
      stop = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);
  return (
    <div className="space-y-3">
      <div
        className={cn(
          "flex items-center gap-3 rounded-lg border px-4 py-3 text-[13px]",
          phase === "done" ? "border-success/30 bg-success-soft text-success" : phase === "failed" ? "border-danger/30 bg-danger-soft text-danger" : "border-border bg-surface-2/60",
        )}
      >
        {phase === "done" ? <CheckCircle2 className="size-5" /> : phase === "failed" ? <XCircle className="size-5" /> : <Loader2 className="size-5 animate-spin" />}
        <div>
          <div className="font-medium">
            {phase === "done" ? t("Updated to {version} — reloading…", { version: target }) : phase === "failed" ? t("The update did not complete") : phase === "restarting" ? t("Restarting PortDock…") : t("Updating to {version}…", { version: target })}
          </div>
          <div className="text-xs opacity-80">
            {phase === "failed" ? error : phase === "restarting" ? t("The new version is starting; this page reconnects automatically.") : t("Your applications keep running during the update.")}
          </div>
        </div>
      </div>
      {steps.length > 0 && (
        <div className="max-h-56 space-y-1 overflow-y-auto rounded-lg border border-border bg-log p-3 font-mono text-2xs text-[#d4d4d8]">
          {steps.map((s, i) => (
            <div key={i}>
              <span className="text-[#71717a]">{new Date(s.time).toLocaleTimeString(intlLocale())}</span> {s.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function UpdateModal({ open, onClose, info }: { open: boolean; onClose: () => void; info?: UpdateInfo }) {
  const qc = useQueryClient();
  const [running, setRunning] = useState(false);
  const [force, setForce] = useState(false);
  const start = useMutation({
    mutationFn: () => api.post<UpdateInfo>("/system/update", { version: info?.latest?.tag, force }),
    onSuccess: () => setRunning(true),
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) setForce(true);
      toast.error(e.message);
    },
  });
  if (!info?.latest) return null;
  const rel = info.latest;
  const admin = info.supported !== undefined;
  return (
    <Modal
      open={open}
      onClose={running ? () => undefined : onClose}
      size="lg"
      title={`PortDock ${rel.tag}`}
      description={rel.published_at ? t("Installed: {version} · released {time}", { version: info.current, time: timeAgo(rel.published_at) }) : t("Installed: {version}", { version: info.current })}
      footer={
        !running && (
          <>
            {rel.url && (
              <a href={rel.url} target="_blank" rel="noreferrer">
                <Button icon={<ExternalLink />}>{t("Release on GitHub")}</Button>
              </a>
            )}
            {admin && info.supported && (
              <Button variant="primary" icon={<ArrowUpCircle />} loading={start.isPending} onClick={() => start.mutate()}>
                {force ? t("Update anyway") : t("Update now")}
              </Button>
            )}
          </>
        )
      }
    >
      {running ? (
        <Progress state={info.state} target={rel.tag} onDone={() => (qc.clear(), location.reload())} />
      ) : (
        <div className="space-y-4">
          <Notes text={rel.notes} />
          {admin ? (
            info.supported ? (
              <div className="flex gap-2.5 rounded-lg border border-border bg-surface-2/60 px-4 py-3 text-xs text-muted">
                <ShieldCheck className="mt-0.5 size-4 shrink-0 text-success" />
                <span>
                  {t(
                    "Your data stays where it is. The database and encryption key are backed up first, the new version is started next to the old one's configuration (volumes, ports, networks, environment), and if it doesn't become healthy PortDock rolls back automatically. Running applications are not restarted.",
                  )}
                  {force && <strong className="mt-1 block text-warning">{t("Deployments are running — updating now interrupts them.")}</strong>}
                </span>
              </div>
            ) : (
              <div className="rounded-lg border border-warning/30 bg-warning-soft px-4 py-3 text-xs text-warning">{info.unsupported_reason}</div>
            )
          ) : (
            <p className="text-xs text-muted">{t("Ask an administrator to install the update.")}</p>
          )}
        </div>
      )}
    </Modal>
  );
}

/** Version footer + update call-to-action in the sidebar. */
export function SidebarUpdate({ collapsed }: { collapsed: boolean }) {
  const q = useUpdateInfo();
  const [open, setOpen] = useState(false);
  const info = q.data;
  if (!info) return null;
  const running = info.state?.status === "running";
  if (collapsed) {
    return info.update_available ? (
      <>
        <button onClick={() => setOpen(true)} title={t("Update to {version}", { version: info.latest?.tag ?? "" })} className="relative mx-auto flex size-8 items-center justify-center rounded-md text-accent hover:bg-surface-2">
          <ArrowUpCircle className="size-4" />
          <span className="absolute right-1 top-1 size-1.5 rounded-full bg-accent" />
        </button>
        <UpdateModal open={open} onClose={() => setOpen(false)} info={info} />
      </>
    ) : null;
  }
  return (
    <>
      {info.update_available ? (
        <button
          onClick={() => setOpen(true)}
          className="group flex w-full items-center gap-2.5 rounded-lg border border-accent/30 bg-accent-soft px-2.5 py-2 text-left transition-colors hover:border-accent/60"
        >
          {running ? <Loader2 className="size-4 animate-spin text-accent" /> : <ArrowUpCircle className="size-4 text-accent" />}
          <span className="min-w-0 flex-1">
            <span className="block text-[12px] font-semibold text-fg">{running ? t("Updating…") : t("Update available")}</span>
            <span className="block text-2xs text-muted">
              {info.current} → {info.latest?.tag}
            </span>
          </span>
        </button>
      ) : (
        <div className="flex items-center justify-between px-2 text-2xs text-faint" title={info.checked_at ? t("Checked {time}", { time: timeAgo(info.checked_at) }) : undefined}>
          <span>PortDock {info.current}</span>
          {info.latest && <span className="flex items-center gap-1 text-success"><CheckCircle2 className="size-3" /> {t("Up to date")}</span>}
        </div>
      )}
      <UpdateModal open={open} onClose={() => setOpen(false)} info={info} />
    </>
  );
}

// ---------- Settings → Updates ----------

export function UpdateSettings() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["system-update"], queryFn: () => api.get<UpdateInfo>("/system/update") });
  const [f, setF] = useState({ repo: "", image: "", token: "", channel: "stable", auto: false });
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const s = q.data?.settings;
    if (s) setF({ repo: s.overridden ? s.repo : "", image: s.image, token: "", channel: s.channel, auto: s.auto });
  }, [q.data]);
  const save = useMutation({
    mutationFn: () => api.put<UpdateInfo>("/system/update/settings", { ...f, repo: f.repo === q.data?.settings?.official_repo ? "" : f.repo }),
    onSuccess: (d) => {
      qc.setQueryData(["system-update"], d);
      toast.success(t("Update settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const check = useMutation({
    mutationFn: () => api.post<UpdateInfo>("/system/update/check"),
    onSuccess: (d) => {
      qc.setQueryData(["system-update"], d);
      d.error ? toast.error(d.error) : d.update_available ? toast.success(t("{version} is available", { version: d.latest?.tag ?? "" })) : toast.success(t("PortDock is up to date"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  const d = q.data!;
  const st = d.state;
  return (
    <div className="space-y-6">
      <Card>
        <div className="flex flex-wrap items-center gap-5 p-5">
          <span className={cn("flex size-12 items-center justify-center rounded-xl border", d.update_available ? "border-accent/30 bg-accent-soft text-accent" : "border-border bg-surface-2")}>
            <ArrowUpCircle className="size-5" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 text-lg font-semibold tracking-[-0.02em]">
              PortDock {d.current}
              {d.update_available ? <Badge tone="accent">{t("{version} available", { version: d.latest?.tag ?? "" })}</Badge> : d.latest ? <Badge tone="success">{t("Up to date")}</Badge> : null}
            </div>
            <div className="mt-1 text-[13px] text-muted">
              {d.error ? <span className="text-warning">{d.error}</span> : d.checked_at ? t("Last checked {time}", { time: timeAgo(d.checked_at) }) : t("Not checked yet")}
              {d.latest && !d.update_available && ` · ${t("latest release {version}", { version: d.latest.tag })}`}
            </div>
          </div>
          <div className="flex gap-2">
            <Button icon={<RefreshCw />} loading={check.isPending} onClick={() => check.mutate()}>
              {t("Check now")}
            </Button>
            {d.update_available && (
              <Button variant="primary" icon={<ArrowUpCircle />} onClick={() => setOpen(true)}>
                {t("Update to {version}", { version: d.latest?.tag ?? "" })}
              </Button>
            )}
          </div>
        </div>
        {d.supported === false && <div className="border-t border-border bg-warning-soft px-5 py-3 text-xs text-warning">{d.unsupported_reason}</div>}
      </Card>

      <Card
        title={t("Preferences")}
        description={
          d.settings?.repo ? (
            tx(
              d.settings.overridden
                ? "Updates come from the official releases at {link} (custom source) — nothing to configure. PortDock checks every 6 hours."
                : "Updates come from the official releases at {link} — nothing to configure. PortDock checks every 6 hours.",
              {
                link: (
                  <a href={`https://github.com/${d.settings.repo}/releases`} target="_blank" rel="noreferrer" className="font-medium text-fg underline underline-offset-2">
                    github.com/{d.settings.repo}
                  </a>
                ),
              },
            )
          ) : (
            t("This is a development build. Installations from a published release find their updates automatically.")
          )
        }
        actions={
          <Button size="sm" variant="primary" icon={<Save />} loading={save.isPending} onClick={() => save.mutate()}>
            {t("Save")}
          </Button>
        }
      >
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("Channel")}>
            <Select value={f.channel} onChange={(e) => setF({ ...f, channel: e.target.value })}>
              <option value="stable">{t("Stable releases")}</option>
              <option value="prerelease">{t("Include pre-releases (beta, rc)")}</option>
            </Select>
          </Field>
          <div className="pt-1">
            <Switch checked={f.auto} onChange={(v) => setF({ ...f, auto: v })} label={t("Install stable updates automatically")} description={t("Between 02:00 and 05:00 in the instance time zone, and never while deployments are running.")} />
          </div>
        </div>
        <details className="group border-t border-border">
          <summary className="cursor-pointer select-none px-5 py-3 text-xs font-medium text-muted hover:text-fg">{t("Advanced: custom release source (forks, private mirrors)")}</summary>
          <div className="grid gap-4 px-5 pb-5 md:grid-cols-2">
            <Field label={t("GitHub repository")} hint={d.settings?.official_repo ? t("owner/name. Leave empty to use the official source ({repo}).", { repo: d.settings.official_repo }) : t("owner/name. Leave empty to use the official source.")}>
              <Input value={f.repo === d.settings?.official_repo ? "" : f.repo} onChange={(e) => setF({ ...f, repo: e.target.value })} placeholder={d.settings?.official_repo || "acme/portdock"} className="font-mono text-xs" />
            </Field>
            <Field label={t("Container image")} hint={t("Without tag. Default: {image}", { image: d.settings?.default_image || "ghcr.io/<owner>/<repo>" })}>
              <Input value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} placeholder={d.settings?.default_image} className="font-mono text-xs" />
            </Field>
            <Field label={t("Access token")} hint={d.settings?.token_set ? t("A token is stored; type to replace it.") : t("Only for private repositories or images.")}>
              <PasswordInput value={f.token} onChange={(e) => setF({ ...f, token: e.target.value })} placeholder={d.settings?.token_set ? "••••••••" : "ghp_…"} />
            </Field>
          </div>
        </details>
      </Card>

      {st?.status && (
        <Card title={t("Last update")} description={t("{from} → {to} · started {time}", { from: st.from || "?", to: st.to, time: timeAgo(st.started_at) })} actions={<Badge tone={st.status === "success" ? "success" : st.status === "running" ? "info" : "danger"}>{statusLabel[st.status] ? t(statusLabel[st.status]) : st.status.replace("_", " ")}</Badge>}>
          <div className="space-y-3 p-5">
            {st.error && <div className="rounded-lg border border-danger/30 bg-danger-soft px-4 py-2.5 text-xs text-danger">{st.error}</div>}
            {st.backup && <div className="text-xs text-muted">{tx("Backup: {file}", { file: <code className="font-mono">{st.backup}</code> })}</div>}
            <div className="max-h-64 space-y-1 overflow-y-auto rounded-lg border border-border bg-log p-3 font-mono text-2xs text-[#d4d4d8]">
              {st.steps.map((s, i) => (
                <div key={i}>
                  <span className="text-[#71717a]">{new Date(s.time).toLocaleString(intlLocale())}</span> {s.message}
                </div>
              ))}
            </div>
          </div>
        </Card>
      )}
      <UpdateModal open={open} onClose={() => setOpen(false)} info={d} />
    </div>
  );
}
