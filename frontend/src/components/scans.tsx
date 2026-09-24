import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Save, Search, ShieldAlert, ShieldCheck, ScanSearch } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, ImageScan, ScanFinding } from "../lib/types";
import { cn, timeAgo } from "../lib/utils";
import { t, tk, tx } from "../lib/i18n";
import { Badge, Button, Card, Empty, Input, Loading, Select, StatusBadge, Switch, Table } from "./ui";

type Severity = "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN";

const sevLabel: Record<Severity, string> = {
  CRITICAL: tk("Critical"),
  HIGH: tk("High"),
  MEDIUM: tk("Medium"),
  LOW: tk("Low"),
  UNKNOWN: tk("Unknown"),
};
const sevClass: Record<Severity, string> = {
  CRITICAL: "border-danger/30 bg-danger-soft text-danger",
  HIGH: "border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400",
  MEDIUM: "border-warning/30 bg-warning-soft text-warning",
  LOW: "border-info/30 bg-info-soft text-info",
  UNKNOWN: "border-border bg-surface-2 text-muted",
};

function SevBadge({ sev, n }: { sev: Severity; n?: number }) {
  return (
    <span className={cn("inline-flex h-5 items-center gap-1 whitespace-nowrap rounded border px-1.5 text-2xs font-semibold uppercase tracking-wide", sevClass[sev])}>
      {n !== undefined && <span className="tabular-nums">{n}</span>}
      {t(sevLabel[sev])}
    </span>
  );
}

/** Critical, high, medium and low counters of a scan. */
export function ScanCounts({ s }: { s: ImageScan }) {
  if (s.status === "running") return <StatusBadge status="deploying" label={t("Scanning…")} />;
  if (s.status === "failed") return <StatusBadge status="failed" label={t("Failed")} />;
  if (s.critical + s.high + s.medium + s.low + s.unknown === 0)
    return (
      <Badge tone="success">
        <ShieldCheck className="size-3" /> {t("No vulnerabilities")}
      </Badge>
    );
  return (
    <div className="flex flex-wrap gap-1">
      {s.critical > 0 && <SevBadge sev="CRITICAL" n={s.critical} />}
      {s.high > 0 && <SevBadge sev="HIGH" n={s.high} />}
      {s.medium > 0 && <SevBadge sev="MEDIUM" n={s.medium} />}
      {s.low > 0 && <SevBadge sev="LOW" n={s.low} />}
    </div>
  );
}

interface ScansResponse {
  scans: ImageScan[];
  running: boolean;
  scanner: string;
}

export function SecurityTab({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const scans = useQuery({
    queryKey: ["scans", app.id],
    queryFn: () => api.get<ScansResponse>(`/applications/${app.id}/scans`),
    refetchInterval: (q) => (q.state.data?.running ? 3000 : 30_000),
  });
  const [selected, setSelected] = useState<number | null>(null);
  const list = scans.data?.scans ?? [];
  // The latest scan of each image or compose service.
  const latest = useMemo(() => {
    const seen = new Set<string>();
    return list.filter((s) => {
      const k = s.service || "·";
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  }, [list]);
  const current = list.find((s) => s.id === selected) ?? latest.find((s) => s.status === "success") ?? latest[0];

  const scan = useMutation({
    mutationFn: () => api.post(`/applications/${app.id}/scan`),
    onSuccess: () => {
      setSelected(null);
      qc.invalidateQueries({ queryKey: ["scans", app.id] });
      toast.success(t("Scan started"), { description: t("The first scan on a server downloads the vulnerability database, which takes a minute.") });
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <div className="space-y-6">
      <ScanSettings app={app} />
      <Card
        title={t("Vulnerabilities")}
        description={tx("Images are scanned with {trivy} for known vulnerabilities in OS packages and dependencies.", {
          trivy: (
            <a href="https://trivy.dev" target="_blank" rel="noreferrer" className="font-medium text-fg underline">
              Trivy
            </a>
          ),
        })}
        actions={
          app.access.can_write && (
            <Button size="sm" variant="primary" icon={<ScanSearch className="size-3.5" />} loading={scan.isPending} disabled={scans.data?.running} onClick={() => scan.mutate()}>
              {scans.data?.running ? t("Scanning…") : t("Scan now")}
            </Button>
          )
        }
      >
        {scans.isLoading ? (
          <div className="p-5">
            <Loading />
          </div>
        ) : latest.length === 0 ? (
          <div className="p-5">
            <Empty icon={<ShieldAlert />} title={t("Not scanned yet")} description={t("Scan the running images now, or turn on scanning on every deploy.")} />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {latest.map((s) => (
              <button
                key={s.id}
                onClick={() => setSelected(s.id)}
                className={cn("flex w-full flex-wrap items-center gap-3 px-5 py-3 text-left transition-colors", current?.id === s.id ? "bg-surface-2" : "hover:bg-surface-2/50")}
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px] font-medium">{s.service || t("Application image")}</div>
                  <div className="truncate font-mono text-xs text-subtle" title={s.image}>
                    {s.image}
                  </div>
                </div>
                <ScanCounts s={s} />
                <span className="w-28 shrink-0 text-right text-xs text-subtle">{timeAgo(s.finished_at || s.created_at)}</span>
              </button>
            ))}
          </div>
        )}
      </Card>

      {current && <Findings key={current.id} app={app} scan={current} />}

      {list.length > latest.length && (
        <Card title={t("Scan history")}>
          <Table>
            <thead>
              <tr>
                <th>{t("Date")}</th>
                <th>{t("Image")}</th>
                <th>{t("Trigger")}</th>
                <th>{t("Result")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((s) => (
                <tr key={s.id} onClick={() => setSelected(s.id)} className={cn("cursor-pointer", current?.id === s.id && "bg-surface-2")}>
                  <td className="whitespace-nowrap text-xs text-muted">{timeAgo(s.created_at)}</td>
                  <td className="max-w-xs truncate font-mono text-xs" title={s.image}>
                    {s.service ? `${s.service} · ` : ""}
                    {s.image}
                  </td>
                  <td className="text-xs text-muted">{s.trigger === "deploy" ? t("Deployment") : t("Manual")}</td>
                  <td>
                    <ScanCounts s={s} />
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </div>
  );
}

function ScanSettings({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const initial = { scan_on_deploy: app.scan_on_deploy, scan_block: app.scan_block, scan_ignore_unfixed: app.scan_ignore_unfixed };
  const [f, setF] = useState(initial);
  useEffect(() => setF({ scan_on_deploy: app.scan_on_deploy, scan_block: app.scan_block, scan_ignore_unfixed: app.scan_ignore_unfixed }), [app.scan_on_deploy, app.scan_block, app.scan_ignore_unfixed]);
  const dirty = f.scan_on_deploy !== app.scan_on_deploy || f.scan_block !== app.scan_block || f.scan_ignore_unfixed !== app.scan_ignore_unfixed;
  const save = useMutation({
    mutationFn: () => api.patch<ApplicationDetail>(`/applications/${app.id}`, f),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      toast.success(t("Security settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const compose = app.build_pack === "compose";
  return (
    <Card
      title={t("Vulnerability scanning")}
      actions={
        dirty && (
          <Button size="sm" variant="primary" icon={<Save className="size-3.5" />} loading={save.isPending} onClick={() => save.mutate()}>
            {t("Save")}
          </Button>
        )
      }
    >
      <div className="grid gap-5 p-5 md:grid-cols-2">
        <Switch
          checked={f.scan_on_deploy}
          disabled={!app.access.can_write}
          onChange={(v) => setF({ ...f, scan_on_deploy: v })}
          label={t("Scan on every deploy")}
          description={compose ? t("Docker Compose images are scanned once they are running, so they are reported but can't block the deployment.") : t("The new image is scanned before it replaces the running version.")}
        />
        <Switch
          checked={f.scan_ignore_unfixed}
          disabled={!app.access.can_write}
          onChange={(v) => setF({ ...f, scan_ignore_unfixed: v })}
          label={t("Ignore vulnerabilities without a fix")}
          description={t("Only report what an update of the package can fix.")}
        />
        {!compose && (
          <div className="md:col-span-2">
            <div className="mb-1.5 text-[13px] font-medium">{t("Block deployments")}</div>
            <Select value={f.scan_block} disabled={!app.access.can_write || !f.scan_on_deploy} onChange={(e) => setF({ ...f, scan_block: e.target.value as ApplicationDetail["scan_block"] })} className="max-w-md">
              <option value="">{t("Never — only report")}</option>
              <option value="critical">{t("When a critical vulnerability is found")}</option>
              <option value="high">{t("When a critical or high vulnerability is found")}</option>
            </Select>
            <p className="mt-1.5 text-xs text-subtle">{t("A blocked deployment fails before the rollout: the running version stays live. If the scan itself can't run, the deployment continues.")}</p>
          </div>
        )}
      </div>
    </Card>
  );
}

const severities: Severity[] = ["CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN"];

function Findings({ app, scan }: { app: ApplicationDetail; scan: ImageScan }) {
  const data = useQuery({
    queryKey: ["scan", scan.id, scan.status],
    queryFn: () => api.get<{ scan: ImageScan; findings: ScanFinding[] }>(`/applications/${app.id}/scans/${scan.id}`),
    enabled: scan.status !== "running",
  });
  const [sev, setSev] = useState<Severity | "">("");
  const [fixable, setFixable] = useState(false);
  const [q, setQ] = useState("");
  const all = data.data?.findings ?? [];
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return all.filter(
      (f) => (!sev || f.severity === sev) && (!fixable || f.fixed) && (!needle || `${f.id} ${f.package} ${f.title} ${f.target}`.toLowerCase().includes(needle)),
    );
  }, [all, sev, fixable, q]);
  const shown = list.slice(0, 500);
  const title = scan.service ? t("Findings in {name}", { name: scan.service }) : t("Findings");

  if (scan.status === "running")
    return (
      <Card title={title}>
        <p className="px-5 py-6 text-sm text-muted">{t("The scan is running…")}</p>
      </Card>
    );
  if (scan.status === "failed")
    return (
      <Card title={title}>
        <div className="p-5">
          <div className="rounded-lg border border-danger/25 bg-danger-soft px-4 py-3 text-xs text-danger">
            <div className="font-medium">{t("The scan failed")}</div>
            <pre className="mt-1 whitespace-pre-wrap break-words font-mono">{scan.error}</pre>
          </div>
        </div>
      </Card>
    );
  return (
    <Card
      title={title}
      description={
        <span className="font-mono">
          {scan.image} · {timeAgo(scan.finished_at)}
        </span>
      }
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-3">
        <button onClick={() => setSev("")} className={cn("h-7 rounded-md px-2.5 text-xs font-medium", !sev ? "bg-surface-2 text-fg" : "text-muted hover:text-fg")}>
          {t("All")} <span className="tabular-nums text-subtle">{all.length}</span>
        </button>
        {severities.map((s) => {
          const n = all.filter((f) => f.severity === s).length;
          if (!n) return null;
          return (
            <button key={s} onClick={() => setSev(sev === s ? "" : s)} className={cn("h-7 rounded-md px-1.5", sev === s ? "bg-surface-2" : "opacity-80 hover:opacity-100")}>
              <SevBadge sev={s} n={n} />
            </button>
          );
        })}
        <div className="ml-auto flex flex-wrap items-center gap-3">
          <Switch checked={fixable} onChange={setFixable} label={t("With a fix")} />
          <div className="relative w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("CVE, package…")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        </div>
      </div>
      {data.isLoading ? (
        <div className="p-5">
          <Loading />
        </div>
      ) : all.length === 0 ? (
        <div className="p-5">
          <Empty icon={<ShieldCheck />} title={t("No vulnerabilities found")} description={t("Trivy found no known vulnerabilities in this image.")} />
        </div>
      ) : (
        <>
          <Table>
            <thead>
              <tr>
                <th>{t("Severity")}</th>
                <th>{t("Vulnerability")}</th>
                <th>{t("Package")}</th>
                <th>{t("Installed")}</th>
                <th>{t("Fixed in")}</th>
                <th>{t("Where")}</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((f) => (
                <tr key={`${f.id}|${f.package}|${f.installed}`}>
                  <td className="align-top">
                    <div className="py-1">
                      <SevBadge sev={f.severity as Severity} />
                    </div>
                  </td>
                  <td className="max-w-md align-top">
                    <div className="py-1">
                      {f.url ? (
                        <a href={f.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 font-mono text-xs font-medium hover:underline">
                          {f.id}
                          <ExternalLink className="size-3 text-faint" />
                        </a>
                      ) : (
                        <span className="font-mono text-xs font-medium">{f.id}</span>
                      )}
                      {f.title && <div className="mt-0.5 line-clamp-2 text-xs text-muted">{f.title}</div>}
                    </div>
                  </td>
                  <td className="align-top font-mono text-xs">
                    <div className="py-1">{f.package}</div>
                  </td>
                  <td className="align-top font-mono text-xs text-muted">
                    <div className="py-1">{f.installed}</div>
                  </td>
                  <td className="align-top font-mono text-xs">
                    <div className="py-1">{f.fixed ? <span className="text-success">{f.fixed}</span> : <span className="text-faint">—</span>}</div>
                  </td>
                  <td className="max-w-[16rem] align-top text-xs text-subtle">
                    <div className="truncate py-1" title={f.target}>
                      {f.target}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
          {list.length > shown.length && <p className="border-t border-border px-5 py-3 text-xs text-muted">{t("Showing {n} of {total}; filter to see the rest.", { n: shown.length, total: list.length })}</p>}
          {list.length === 0 && <p className="px-5 py-6 text-center text-[13px] text-subtle">{t("No vulnerabilities match your filters.")}</p>}
        </>
      )}
    </Card>
  );
}
