import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Code2, Download, ExternalLink, FileArchive, Globe, Layers, Save, ScanSearch, Search, ShieldAlert, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, ImageScan, ScanFinding, ScanKind, ScanReport } from "../lib/types";
import { cn, formatBytes, timeAgo } from "../lib/utils";
import { t, tk, tMaybe, tx } from "../lib/i18n";
import { Badge, Button, Card, Empty, Field, Input, Loading, Menu, MenuItem, Select, StatusBadge, Switch, Table, Tabs } from "./ui";

type Severity = "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "INFO" | "UNKNOWN";

const sevLabel: Record<Severity, string> = {
  CRITICAL: tk("Critical"),
  HIGH: tk("High"),
  MEDIUM: tk("Medium"),
  LOW: tk("Low"),
  INFO: tk("Info"),
  UNKNOWN: tk("Unknown"),
};
const sevClass: Record<Severity, string> = {
  CRITICAL: "border-danger/30 bg-danger-soft text-danger",
  HIGH: "border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400",
  MEDIUM: "border-warning/30 bg-warning-soft text-warning",
  LOW: "border-info/30 bg-info-soft text-info",
  INFO: "border-border bg-surface-2 text-muted",
  UNKNOWN: "border-border bg-surface-2 text-muted",
};

function SevBadge({ sev, n }: { sev: Severity; n?: number }) {
  return (
    <span className={cn("inline-flex h-5 items-center gap-1 whitespace-nowrap rounded border px-1.5 text-2xs font-semibold uppercase tracking-wide", sevClass[sev] ?? sevClass.UNKNOWN)}>
      {n !== undefined && <span className="tabular-nums">{n}</span>}
      {t(sevLabel[sev] ?? sevLabel.UNKNOWN)}
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
        <ShieldCheck className="size-3" /> {t("No findings")}
      </Badge>
    );
  return (
    <div className="flex flex-wrap gap-1">
      {s.critical > 0 && <SevBadge sev="CRITICAL" n={s.critical} />}
      {s.high > 0 && <SevBadge sev="HIGH" n={s.high} />}
      {s.medium > 0 && <SevBadge sev="MEDIUM" n={s.medium} />}
      {s.low > 0 && <SevBadge sev="LOW" n={s.low} />}
      {s.unknown > 0 && s.kind === "dast" && <SevBadge sev="INFO" n={s.unknown} />}
    </div>
  );
}

export const kindLabel: Record<ScanKind, string> = { image: tk("Image (Trivy)"), sast: tk("Code (SAST)"), dast: tk("Running app (DAST)") };

const reportURL = (appId: number, s: ImageScan, name: string) => `/api/applications/${appId}/scans/${s.id}/reports/${name}`;

/** Download links of the reports of a scan. */
export function ReportLinks({ appId, scan, compact = false }: { appId: number; scan: ImageScan; compact?: boolean }) {
  const reports = scan.reports ?? [];
  if (reports.length === 0) return null;
  const html = reports.find((r) => r.name === "report.html");
  return (
    <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
      {html && !compact && (
        <a href={`${reportURL(appId, scan, "report.html")}?inline=1`} target="_blank" rel="noreferrer">
          <Button size="sm" variant="ghost" icon={<ExternalLink className="size-3.5" />}>
            {t("Report")}
          </Button>
        </a>
      )}
      <Menu
        trigger={
          <Button size="sm" variant="ghost" icon={<Download className="size-3.5" />}>
            {compact ? "" : t("Download")}
          </Button>
        }
      >
        {reports.map((r: ScanReport) => (
          <a key={r.name} href={reportURL(appId, scan, r.name)} download>
            <MenuItem icon={<Download />}>
              <span className="flex w-full items-center justify-between gap-4">
                <span>{t(r.title || r.name)}</span>
                <span className="text-2xs text-faint">{formatBytes(r.size)}</span>
              </span>
            </MenuItem>
          </a>
        ))}
        <a href={`/api/applications/${appId}/scans/${scan.id}/reports.zip`} download>
          <MenuItem icon={<FileArchive />}>{t("All reports (ZIP)")}</MenuItem>
        </a>
      </Menu>
    </div>
  );
}

interface ScansResponse {
  scans: ImageScan[];
  running: boolean;
  scanner: string;
  rules?: string;
  available?: boolean;
  target?: string;
  target_error?: string;
}

export function SecurityTab({ app }: { app: ApplicationDetail }) {
  const [params, setParams] = useSearchParams();
  const kind = (params.get("scan") as ScanKind) || "image";
  return (
    <div className="space-y-5">
      <Tabs
        value={kind}
        onChange={(v) =>
          setParams((p) => {
            p.set("scan", v);
            return p;
          })
        }
        tabs={[
          { id: "image", label: t(kindLabel.image), icon: <Layers /> },
          { id: "sast", label: t(kindLabel.sast), icon: <Code2 /> },
          { id: "dast", label: t(kindLabel.dast), icon: <Globe /> },
        ]}
      />
      <KindPanel key={kind} app={app} kind={kind} />
    </div>
  );
}

const intro: Record<ScanKind, string> = {
  image: tk("Images are scanned with {tool} for known vulnerabilities in OS packages and dependencies."),
  sast: tk("The source is analysed with {tool} for insecure code: injections, unsafe deserialization, weak cryptography, secrets…"),
  dast: tk("{tool} explores the running application and reports what it reveals: missing security headers, insecure cookies, information leaks… The baseline scan doesn't attack the application."),
};
const toolLink: Record<ScanKind, [string, string]> = {
  image: ["Trivy", "https://trivy.dev"],
  sast: ["Semgrep", "https://semgrep.dev"],
  dast: ["OWASP ZAP", "https://www.zaproxy.org"],
};

function KindPanel({ app, kind }: { app: ApplicationDetail; kind: ScanKind }) {
  const qc = useQueryClient();
  const scans = useQuery({
    queryKey: ["scans", app.id, kind],
    queryFn: () => api.get<ScansResponse>(`/applications/${app.id}/scans?kind=${kind}`),
    refetchInterval: (q) => (q.state.data?.running ? 3000 : 30_000),
  });
  const [selected, setSelected] = useState<number | null>(null);
  const list = scans.data?.scans ?? [];
  // The latest scan of each image or compose service (one for SAST and DAST).
  const latest = useMemo(() => {
    const seen = new Set<string>();
    return list.filter((s) => {
      const k = kind === "image" ? s.service || "·" : "·";
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
  }, [list, kind]);
  const current = list.find((s) => s.id === selected) ?? latest.find((s) => s.status === "success") ?? latest[0];

  const scan = useMutation({
    mutationFn: () => api.post(`/applications/${app.id}/scan`, { kind }),
    onSuccess: () => {
      setSelected(null);
      qc.invalidateQueries({ queryKey: ["scans", app.id, kind] });
      toast.success(t("Scan started"), {
        description:
          kind === "dast"
            ? t("A baseline scan takes a few minutes.")
            : kind === "sast"
              ? t("The branch is cloned and analysed; the first run downloads the rules.")
              : t("The first scan on a server downloads the vulnerability database, which takes a minute."),
      });
    },
    onError: (e) => toast.error(e.message),
  });
  const unavailable = kind === "sast" && scans.data?.available === false;
  const [tool, href] = toolLink[kind];

  return (
    <div className="space-y-6">
      <ScanSettings app={app} kind={kind} info={scans.data} />
      <Card
        title={kind === "image" ? t("Vulnerabilities") : kind === "sast" ? t("Static analysis") : t("Dynamic scan")}
        description={tx(intro[kind], {
          tool: (
            <a href={href} target="_blank" rel="noreferrer" className="font-medium text-fg underline">
              {tool}
            </a>
          ),
        })}
        actions={
          app.access.can_write &&
          !unavailable && (
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
        ) : unavailable ? (
          <p className="px-5 py-6 text-sm text-muted">{t("Static analysis needs the source: this application is deployed from an image.")}</p>
        ) : latest.length === 0 ? (
          <div className="p-5">
            <Empty icon={<ShieldAlert />} title={t("Not scanned yet")} description={t("Scan now, or turn on scanning on every deploy.")} />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {latest.map((s) => (
              <div
                key={s.id}
                role="button"
                tabIndex={0}
                onClick={() => setSelected(s.id)}
                className={cn("flex w-full cursor-pointer flex-wrap items-center gap-3 px-5 py-3 text-left transition-colors", current?.id === s.id ? "bg-surface-2" : "hover:bg-surface-2/50")}
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px] font-medium">{scanTitle(s)}</div>
                  <div className="truncate font-mono text-xs text-subtle" title={scanSubtitle(s)}>
                    {scanSubtitle(s)}
                  </div>
                </div>
                <ScanCounts s={s} />
                <ReportLinks appId={app.id} scan={s} />
                <span className="w-24 shrink-0 text-right text-xs text-subtle">{timeAgo(s.finished_at || s.created_at)}</span>
              </div>
            ))}
          </div>
        )}
      </Card>

      {current && !unavailable && <Findings key={current.id} app={app} scan={current} />}

      {list.length > latest.length && (
        <Card title={t("Scan history")}>
          <Table>
            <thead>
              <tr>
                <th>{t("Date")}</th>
                <th>{kind === "dast" ? t("Target") : kind === "sast" ? t("Commit") : t("Image")}</th>
                <th>{t("Trigger")}</th>
                <th>{t("Result")}</th>
                <th className="!text-right">{t("Reports")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((s) => (
                <tr key={s.id} onClick={() => setSelected(s.id)} className={cn("cursor-pointer", current?.id === s.id && "bg-surface-2")}>
                  <td className="whitespace-nowrap text-xs text-muted">{timeAgo(s.created_at)}</td>
                  <td className="max-w-xs truncate font-mono text-xs" title={scanSubtitle(s)}>
                    {scanSubtitle(s)}
                  </td>
                  <td className="text-xs text-muted">{s.trigger === "deploy" ? (s.deployment_id ? t("Deployment #{n}", { n: s.deployment_id }) : t("Deployment")) : t("Manual")}</td>
                  <td>
                    <ScanCounts s={s} />
                  </td>
                  <td>
                    <div className="flex justify-end">
                      <ReportLinks appId={app.id} scan={s} compact />
                    </div>
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

function scanTitle(s: ImageScan) {
  if (s.kind === "sast") return t("Source code");
  if (s.kind === "dast") return t("Running application");
  return s.service || t("Application image");
}

function scanSubtitle(s: ImageScan) {
  if (s.kind === "sast") return s.commit_sha ? s.commit_sha.slice(0, 12) : "—";
  if (s.kind === "dast") return s.target;
  return s.service ? `${s.service} · ${s.image}` : s.image;
}

function ScanSettings({ app, kind, info }: { app: ApplicationDetail; kind: ScanKind; info?: ScansResponse }) {
  const qc = useQueryClient();
  const pick = (a: ApplicationDetail) => ({
    scan_on_deploy: a.scan_on_deploy,
    scan_block: a.scan_block,
    scan_ignore_unfixed: a.scan_ignore_unfixed,
    sast_on_deploy: a.sast_on_deploy,
    sast_block: a.sast_block,
    dast_on_deploy: a.dast_on_deploy,
    dast_target: a.dast_target,
  });
  const [f, setF] = useState(pick(app));
  useEffect(() => setF(pick(app)), [app]);
  const dirty = JSON.stringify(f) !== JSON.stringify(pick(app));
  const save = useMutation({
    mutationFn: () => api.patch<ApplicationDetail>(`/applications/${app.id}`, f),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      qc.invalidateQueries({ queryKey: ["scans", app.id] });
      toast.success(t("Security settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const compose = app.build_pack === "compose";
  const ro = !app.access.can_write;
  return (
    <Card
      title={kind === "image" ? t("Vulnerability scanning") : kind === "sast" ? t("Static analysis settings") : t("Dynamic scan settings")}
      actions={
        dirty && (
          <Button size="sm" variant="primary" icon={<Save className="size-3.5" />} loading={save.isPending} onClick={() => save.mutate()}>
            {t("Save")}
          </Button>
        )
      }
    >
      <div className="grid gap-5 p-5 md:grid-cols-2">
        {kind === "image" && (
          <>
            <Switch
              checked={f.scan_on_deploy}
              disabled={ro}
              onChange={(v) => setF({ ...f, scan_on_deploy: v })}
              label={t("Scan on every deploy")}
              description={compose ? t("Docker Compose images are scanned once they are running, so they are reported but can't block the deployment.") : t("The new image is scanned before it replaces the running version.")}
            />
            <Switch checked={f.scan_ignore_unfixed} disabled={ro} onChange={(v) => setF({ ...f, scan_ignore_unfixed: v })} label={t("Ignore vulnerabilities without a fix")} description={t("Only report what an update of the package can fix.")} />
            {!compose && (
              <div className="md:col-span-2">
                <div className="mb-1.5 text-[13px] font-medium">{t("Block deployments")}</div>
                <Select value={f.scan_block} disabled={ro || !f.scan_on_deploy} onChange={(e) => setF({ ...f, scan_block: e.target.value as ApplicationDetail["scan_block"] })} className="max-w-md">
                  <option value="">{t("Never — only report")}</option>
                  <option value="critical">{t("When a critical vulnerability is found")}</option>
                  <option value="high">{t("When a critical or high vulnerability is found")}</option>
                </Select>
                <p className="mt-1.5 text-xs text-subtle">{t("A blocked deployment fails before the rollout: the running version stays live. If the scan itself can't run, the deployment continues.")}</p>
              </div>
            )}
          </>
        )}
        {kind === "sast" && (
          <>
            <Switch checked={f.sast_on_deploy} disabled={ro} onChange={(v) => setF({ ...f, sast_on_deploy: v })} label={t("Analyse the source on every deploy")} description={t("Right after the checkout, before the build.")} />
            <div>
              <div className="mb-1.5 text-[13px] font-medium">{t("Block deployments")}</div>
              <Select value={f.sast_block} disabled={ro || !f.sast_on_deploy} onChange={(e) => setF({ ...f, sast_block: e.target.value as ApplicationDetail["sast_block"] })}>
                <option value="">{t("Never — only report")}</option>
                <option value="high">{t("When a high finding is found")}</option>
              </Select>
            </div>
            {info?.rules && (
              <p className="text-xs text-subtle md:col-span-2">
                {tx("Rules: {rules}. Change them in Settings → Network & proxy → Security scanners.", { rules: <code className="font-mono text-fg">{info.rules}</code> })}
              </p>
            )}
          </>
        )}
        {kind === "dast" && (
          <>
            <Switch checked={f.dast_on_deploy} disabled={ro} onChange={(v) => setF({ ...f, dast_on_deploy: v })} label={t("Scan after every deploy")} description={t("In the background once the new version is live; the report is attached to the deployment.")} />
            <Field label={t("URL to scan")} hint={info?.target_error ? tMaybe(info.target_error) : tx("Empty: {target}", { target: <code className="font-mono">{info?.target || "—"}</code> })}>
              <Input value={f.dast_target} disabled={ro} onChange={(e) => setF({ ...f, dast_target: e.target.value })} placeholder="https://app.example.com/" />
            </Field>
            <p className="text-xs text-subtle md:col-span-2">{t("The scan runs on the server of the application, inside the PortDock network, so the application is reached directly.")}</p>
          </>
        )}
      </div>
    </Card>
  );
}

const severities: Severity[] = ["CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO", "UNKNOWN"];

function Findings({ app, scan }: { app: ApplicationDetail; scan: ImageScan }) {
  const data = useQuery({
    queryKey: ["scan", scan.id, scan.status],
    queryFn: () => api.get<{ scan: ImageScan; findings: ScanFinding[] }>(`/applications/${app.id}/scans/${scan.id}`),
    enabled: scan.status !== "running",
  });
  const [sev, setSev] = useState<Severity | "">("");
  const [fixable, setFixable] = useState(false);
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const all = data.data?.findings ?? [];
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return all.filter(
      (f) => (!sev || f.severity === sev) && (!fixable || f.fixed) && (!needle || `${f.id} ${f.package} ${f.title} ${f.target} ${f.cwe ?? ""}`.toLowerCase().includes(needle)),
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
          {scanSubtitle(scan)} · {timeAgo(scan.finished_at)}
        </span>
      }
      actions={<ReportLinks appId={app.id} scan={scan} />}
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
          {scan.kind === "image" && <Switch checked={fixable} onChange={setFixable} label={t("With a fix")} />}
          <div className="relative w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={scan.kind === "image" ? t("CVE, package…") : t("Rule, file, CWE…")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        </div>
      </div>
      {data.isLoading ? (
        <div className="p-5">
          <Loading />
        </div>
      ) : all.length === 0 ? (
        <div className="p-5">
          <Empty icon={<ShieldCheck />} title={t("No findings")} description={scan.kind === "image" ? t("Trivy found no known vulnerabilities in this image.") : t("Nothing to report for this scan.")} />
        </div>
      ) : (
        <>
          <Table>
            <thead>
              <tr>
                <th>{t("Severity")}</th>
                <th>{scan.kind === "image" ? t("Vulnerability") : scan.kind === "sast" ? t("Rule") : t("Alert")}</th>
                {scan.kind === "image" && (
                  <>
                    <th>{t("Package")}</th>
                    <th>{t("Installed")}</th>
                    <th>{t("Fixed in")}</th>
                  </>
                )}
                {scan.kind === "dast" && <th className="!text-right">{t("Instances")}</th>}
                <th>{t("Where")}</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((f, i) => {
                const key = `${f.id}|${f.package}|${f.installed}|${f.target}|${f.line ?? ""}|${i}`;
                const expanded = open === key;
                return (
                  <tr key={key} className={cn((f.description || f.solution) && "cursor-pointer")} onClick={() => (f.description || f.solution) && setOpen(expanded ? null : key)}>
                    <td className="align-top">
                      <div className="py-1">
                        <SevBadge sev={f.severity as Severity} />
                      </div>
                    </td>
                    <td className="max-w-xl align-top">
                      <div className="py-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {f.url ? (
                            <a href={f.url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="inline-flex items-center gap-1 break-all font-mono text-xs font-medium hover:underline">
                              {scan.kind === "dast" ? f.title : f.id}
                              <ExternalLink className="size-3 shrink-0 text-faint" />
                            </a>
                          ) : (
                            <span className="break-all font-mono text-xs font-medium">{scan.kind === "dast" ? f.title : f.id}</span>
                          )}
                          {f.cwe && <Badge>{f.cwe}</Badge>}
                        </div>
                        {scan.kind !== "dast" && f.title && <div className={cn("mt-0.5 text-xs text-muted", !expanded && "line-clamp-2")}>{f.title}</div>}
                        {expanded && f.description && <p className="mt-1.5 whitespace-pre-wrap text-xs text-muted">{f.description}</p>}
                        {expanded && f.solution && (
                          <p className="mt-1.5 whitespace-pre-wrap text-xs">
                            <span className="font-medium">{t("Solution")}:</span> <span className="text-muted">{f.solution}</span>
                          </p>
                        )}
                      </div>
                    </td>
                    {scan.kind === "image" && (
                      <>
                        <td className="align-top font-mono text-xs">
                          <div className="py-1">{f.package}</div>
                        </td>
                        <td className="align-top font-mono text-xs text-muted">
                          <div className="py-1">{f.installed}</div>
                        </td>
                        <td className="align-top font-mono text-xs">
                          <div className="py-1">{f.fixed ? <span className="text-success">{f.fixed}</span> : <span className="text-faint">—</span>}</div>
                        </td>
                      </>
                    )}
                    {scan.kind === "dast" && (
                      <td className="align-top text-right text-xs tabular-nums">
                        <div className="py-1">{f.count ?? 1}</div>
                      </td>
                    )}
                    <td className="max-w-[20rem] align-top text-xs text-subtle">
                      <div className="truncate py-1 font-mono" title={f.target}>
                        {f.target}
                        {f.line ? `:${f.line}` : ""}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
          {list.length > shown.length && <p className="border-t border-border px-5 py-3 text-xs text-muted">{t("Showing {n} of {total}; filter to see the rest.", { n: shown.length, total: list.length })}</p>}
          {list.length === 0 && <p className="px-5 py-6 text-center text-[13px] text-subtle">{t("No findings match your filters.")}</p>}
        </>
      )}
    </Card>
  );
}

/** The security reports of a deployment (deployment page). */
export function DeploymentReports({ appId, deploymentId }: { appId: number; deploymentId: number }) {
  const q = useQuery({
    queryKey: ["deployment-reports", deploymentId],
    queryFn: () => api.get<ImageScan[]>(`/deployments/${deploymentId}/reports`),
    refetchInterval: (x) => (x.state.data?.some((s) => s.status === "running") ? 4000 : false),
  });
  const scans = q.data ?? [];
  if (q.isLoading || scans.length === 0) return null;
  const any = scans.some((s) => s.status === "success");
  return (
    <Card
      title={t("Security reports")}
      description={t("The scans of this deployment and their reports.")}
      actions={
        any && (
          <a href={`/api/deployments/${deploymentId}/reports.zip`} download>
            <Button size="sm" icon={<FileArchive className="size-3.5" />}>
              {t("Download all (ZIP)")}
            </Button>
          </a>
        )
      }
    >
      <div className="divide-y divide-border">
        {scans.map((s) => (
          <div key={s.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-[13px] font-medium">{t(kindLabel[s.kind] ?? kindLabel.image)}</div>
              <div className="truncate font-mono text-xs text-subtle">{scanSubtitle(s)}</div>
            </div>
            <ScanCounts s={s} />
            <ReportLinks appId={appId} scan={s} />
          </div>
        ))}
      </div>
    </Card>
  );
}
