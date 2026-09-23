import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Check, CircleHelp, Globe, Pencil, Plus, RefreshCw, Search, Trash2, X, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, AppDomain, ComposeService, DNSRecord, DNSStatus, DomainsResponse, WWWRedirect, CertMode } from "../lib/types";
import { cn, timeAgo } from "../lib/utils";
import { intlLocale, t, tk, tx } from "../lib/i18n";
import { Badge, Button, Card, CopyButton, Empty, Field, Input, Loading, Menu, MenuItem, Modal, Select, Switch, Table, useConfirm } from "./ui";

const dnsTone: Record<DNSStatus, { label: string; tone: "success" | "warning" | "danger" | "info" | "neutral" }> = {
  ok: { label: tk("Verified"), tone: "success" },
  local: { label: tk("Local"), tone: "info" },
  proxied: { label: tk("Proxied"), tone: "info" },
  mismatch: { label: tk("Wrong IP"), tone: "warning" },
  unresolved: { label: tk("Not found"), tone: "danger" },
  unknown: { label: tk("Not checked"), tone: "neutral" },
};

const wwwLabel: Record<WWWRedirect, string> = { none: tk("Disabled"), "to-www": "→ www", "to-apex": tk("→ non-www") };

export function DNSBadge({ d }: { d: Pick<AppDomain, "dns_status" | "dns_detail" | "dns_checked_at"> }) {
  const s = dnsTone[d.dns_status] ?? dnsTone.unknown;
  return (
    <span title={d.dns_detail ? `${d.dns_detail}${d.dns_checked_at ? ` · ${t("checked {time}", { time: timeAgo(d.dns_checked_at) })}` : ""}` : undefined}>
      <Badge tone={s.tone} className="cursor-help">
        <span className="size-1.5 rounded-full bg-current" />
        {t(s.label)}
      </Badge>
    </span>
  );
}

export function domainURL(d: Pick<AppDomain, "host" | "path" | "https">) {
  return `${d.https ? "https" : "http"}://${d.host}${d.path}`;
}

export function useDomains(appId: number) {
  return useQuery({ queryKey: ["domains", appId], queryFn: () => api.get<DomainsResponse>(`/applications/${appId}/domains`) });
}

interface DomainForm {
  id?: number;
  domain: string;
  port: string;
  service: string;
  https: boolean;
  www_redirect: WWWRedirect;
  indexing: boolean;
  cert_mode: CertMode;
}

const certTone: Record<string, "success" | "warning" | "danger" | "info" | "neutral"> = {
  valid: "success",
  expiring: "warning",
  pending: "info",
  local: "neutral",
  expired: "danger",
  missing: "danger",
  error: "danger",
};
const certText: Record<string, string> = {
  valid: tk("Valid"),
  expiring: tk("Expiring"),
  pending: tk("Issuing…"),
  local: tk("Self-signed"),
  expired: tk("Expired"),
  missing: tk("Not issued"),
  error: tk("Error"),
};

export function CertBadge({ d }: { d: Pick<AppDomain, "https" | "cert_status" | "cert_expires_at" | "cert_issuer" | "cert_error"> }) {
  if (!d.https) return <span className="text-xs text-faint">{t("HTTP only")}</span>;
  if (!d.cert_status) return <span className="text-xs text-faint">{t("Not checked")}</span>;
  const days = d.cert_expires_at ? Math.floor((new Date(d.cert_expires_at).getTime() - Date.now()) / 86_400_000) : null;
  const title = [d.cert_issuer, d.cert_expires_at && t("Expires {date}", { date: new Date(d.cert_expires_at).toLocaleDateString(intlLocale()) }), d.cert_error].filter(Boolean).join("\n");
  return (
    <span className="inline-flex flex-col gap-0.5" title={title}>
      <Badge tone={certTone[d.cert_status] ?? "neutral"}>
        <ShieldCheck /> {certText[d.cert_status] ? t(certText[d.cert_status]) : d.cert_status}
      </Badge>
      {days !== null && d.cert_status !== "local" && <span className="text-2xs text-muted">{days >= 0 ? t("{n} day left|{n} days left", { n: days }) : t("{n} day ago|{n} days ago", { n: -days })}</span>}
    </span>
  );
}

/** Services of a compose application's deployed compose file (empty before the first deploy). */
export function useComposeServices(appId: number, compose: boolean) {
  return useQuery({
    queryKey: ["compose-services", appId],
    queryFn: () => api.get<{ services: ComposeService[] }>(`/applications/${appId}/compose-services`),
    enabled: compose,
    staleTime: 60_000,
  });
}

export function DomainsTab({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { data, isLoading } = useDomains(app.id);
  const compose = app.build_pack === "compose";
  const services = useComposeServices(app.id, compose).data?.services ?? [];
  const [q, setQ] = useState("");
  const [form, setForm] = useState<DomainForm | null>(null);
  const [showRecords, setShowRecords] = useState(false);
  const [checking, setChecking] = useState<number | null>(null);

  const onResult = (r: DomainsResponse) => {
    qc.setQueryData(["domains", app.id], r);
    qc.invalidateQueries({ queryKey: ["application", app.id] });
    if (r.routing_warning) toast.warning(r.routing_warning);
  };

  const save = useMutation({
    mutationFn: (f: DomainForm) => {
      const body = { domain: f.domain, port: f.port ? Number(f.port) : 0, https: f.https, www_redirect: f.www_redirect, indexing: f.indexing, cert_mode: f.cert_mode, ...(compose && { service: f.service }) };
      return f.id ? api.patch<DomainsResponse>(`/domains/${f.id}`, body) : api.post<DomainsResponse>(`/applications/${app.id}/domains`, body);
    },
    onSuccess: (r, f) => {
      onResult(r);
      setForm(null);
      toast.success(f.id ? t("Domain updated") : t("Domain added"), { description: r.requires_deploy ? t("Redeploy to apply the change.") : t("Routing was updated — no redeploy needed.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const renew = useMutation({
    mutationFn: (id: number) => api.post(`/domains/${id}/renew`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["domains", app.id] });
      toast.success(t("A new certificate is being requested"), { description: t("The proxy restarts for a few seconds; the certificate usually arrives within a minute.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del<DomainsResponse>(`/domains/${id}`),
    onSuccess: (r) => {
      onResult(r);
      toast.success(t("Domain removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const checkAll = useMutation({
    mutationFn: () => api.post<DomainsResponse>(`/applications/${app.id}/domains/check`),
    onSuccess: (r) => {
      qc.setQueryData(["domains", app.id], r);
      const bad = r.domains.filter((d) => d.dns_status !== "ok" && d.dns_status !== "proxied").length;
      bad ? toast.warning(t("{n} domain needs DNS attention|{n} domains need DNS attention", { n: bad })) : toast.success(t("All domains point to this server"));
    },
    onError: (e) => toast.error(e.message),
  });
  const checkOne = async (d: AppDomain) => {
    setChecking(d.id);
    try {
      const upd = await api.post<AppDomain>(`/domains/${d.id}/check`);
      qc.setQueryData<DomainsResponse>(["domains", app.id], (old) => old && { ...old, domains: old.domains.map((x) => (x.id === upd.id ? upd : x)) });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setChecking(null);
    }
  };
  const setHTTPS = useMutation({
    mutationFn: (enabled: boolean) => api.post<DomainsResponse>(`/applications/${app.id}/domains/https`, { enabled }),
    onSuccess: (r) => {
      onResult(r);
      toast.success(t("HTTPS settings updated"));
    },
    onError: (e) => toast.error(e.message),
  });
  const list = useMemo(() => (data?.domains ?? []).filter((d) => (d.host + d.path).includes(q.toLowerCase())), [data, q]);
  if (isLoading) return <Loading />;
  const domains = data!.domains;
  const allHTTPS = domains.length > 0 && domains.every((d) => d.https);

  const openNew = () => setForm({ domain: "", port: "", service: "", https: app.https, www_redirect: "none", indexing: true, cert_mode: "auto" });
  const portOf = (d: AppDomain) => d.port || (compose ? services.find((s) => s.name === (d.service || app.compose_service))?.port || "—" : app.port);
  const suggest = useSuggestDomain(app.id);
  const openGenerated = async () => {
    try {
      const s = await suggest();
      setForm({ domain: s.domain, port: "", service: "", https: s.domain.startsWith("https://"), www_redirect: "none", indexing: s.kind !== "localhost", cert_mode: "auto" });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold tracking-[-0.01em]">{t("Domains")}</h2>
          <p className="text-[13px] text-subtle">
            {t("{n} domain · traffic is served by Traefik with automatic Let's Encrypt certificates|{n} domains · traffic is served by Traefik with automatic Let's Encrypt certificates", { n: domains.length })}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative w-60">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input className="pl-8" placeholder={t("Search domains")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <Button icon={<RefreshCw />} loading={checkAll.isPending} disabled={!domains.length} onClick={() => checkAll.mutate()}>
            {t("Check all DNS")}
          </Button>
          <Button icon={<Globe />} disabled={!domains.length} onClick={() => setShowRecords(true)}>
            {t("DNS records")}
          </Button>
          <Button icon={<Globe />} onClick={openGenerated} title={t("Generate a domain on the server's wildcard, sslip.io or *.localhost")}>
            {t("Generate domain")}
          </Button>
          <Button variant="primary" icon={<Plus />} onClick={openNew}>
            {t("Add domain")}
          </Button>
        </div>
      </div>

      {domains.length > 0 && (
        <div className="flex items-center justify-end gap-3">
          <span className="flex items-center gap-1.5 text-[13px] text-muted">
            {t("Redirect HTTP to HTTPS")}
            <span title={t("Issue TLS certificates and permanently redirect plain HTTP requests on every domain.")}>
              <CircleHelp className="size-3.5 text-faint" />
            </span>
          </span>
          <Select className="w-36" value={allHTTPS ? "on" : "off"} onChange={(e) => setHTTPS.mutate(e.target.value === "on")}>
            <option value="on">{t("Enabled")}</option>
            <option value="off">{t("Disabled")}</option>
          </Select>
        </div>
      )}

      <Card>
        {domains.length === 0 ? (
          <div className="p-4">
            <Empty
              compact
              icon={<Globe />}
              title={t("No domains yet")}
              description={t("Add a domain to expose this application over HTTPS. Point its DNS A/AAAA record to the server first.")}
              action={
                <div className="flex gap-2">
                  <Button icon={<Globe />} onClick={openGenerated}>
                    {t("Generate domain")}
                  </Button>
                  <Button variant="primary" icon={<Plus />} onClick={openNew}>
                    {t("Add domain")}
                  </Button>
                </div>
              }
            />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Domain")}</th>
                {compose && <th>{t("Service")}</th>}
                <th>{t("Protocol redirect")}</th>
                <th>{t("Domain redirect")}</th>
                <th className="!text-right">{t("Internal port")}</th>
                <th className="!text-center">{t("Search indexing")}</th>
                <th>{t("DNS status")}</th>
                <th>{t("Certificate")}</th>
                <th className="!text-right">{t("Actions")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((d) => (
                <tr key={d.id}>
                  <td className="max-w-md">
                    <a href={domainURL(d)} target="_blank" rel="noreferrer" className="inline-flex max-w-full items-center gap-2 font-medium hover:underline">
                      <Globe className="size-4 shrink-0 text-faint" />
                      <span className="truncate">{domainURL(d)}</span>
                    </a>
                  </td>
                  {compose && (
                    <td>
                      <Badge>{d.service || app.compose_service || t("Main service")}</Badge>
                    </td>
                  )}
                  <td className="text-muted">
                    {d.https ? (
                      <span className="inline-flex items-center gap-1">
                        HTTP <ArrowRight className="size-3" /> HTTPS
                      </span>
                    ) : (
                      t("None")
                    )}
                  </td>
                  <td className="text-muted">{t(wwwLabel[d.www_redirect])}</td>
                  <td className="text-right tabular-nums">{portOf(d)}</td>
                  <td className="text-center">{d.indexing ? <Check className="mx-auto size-4 text-success" /> : <X className="mx-auto size-4 text-faint" />}</td>
                  <td>
                    <DNSBadge d={d} />
                  </td>
                  <td>
                    <CertBadge d={d} />
                  </td>
                  <td>
                    <div className="flex justify-end gap-0.5">
                      {d.https && d.cert_mode !== "custom" && d.cert_status !== "local" && (
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          title={t("Renew certificate now")}
                          loading={renew.isPending && renew.variables === d.id}
                          onClick={async () => {
                            if (await confirm({ title: t("Renew the certificate of {host}?", { host: d.host }), description: t("The proxy on the server restarts for a few seconds to request a fresh certificate. Renewals are automatic — use this only if a certificate is stuck."), confirmText: t("Renew now") }))
                              renew.mutate(d.id);
                          }}
                        >
                          <ShieldCheck />
                        </Button>
                      )}
                      <Button size="icon-sm" variant="ghost" title={t("Re-check DNS")} loading={checking === d.id} onClick={() => checkOne(d)}>
                        <RefreshCw />
                      </Button>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        title={t("Edit")}
                        onClick={() => setForm({ id: d.id, domain: domainURL(d), port: d.port ? String(d.port) : "", service: d.service, https: d.https, www_redirect: d.www_redirect, indexing: d.indexing, cert_mode: (d.cert_mode || "auto") as CertMode })}
                      >
                        <Pencil />
                      </Button>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        title={t("Remove")}
                        onClick={async () => {
                          if (await confirm({ title: t("Remove {domain}?", { domain: d.host + d.path }), description: t("Traffic to this domain stops reaching the application immediately."), danger: true, confirmText: t("Remove domain") }))
                            remove.mutate(d.id);
                        }}
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
      </Card>

      {domains.some((d) => d.dns_status === "mismatch" || d.dns_status === "unresolved") && (
        <div className="rounded-lg border border-warning/25 bg-warning-soft px-4 py-3 text-[13px] text-muted">
          {tx("Some domains don't point to this server yet. Certificates can only be issued once DNS resolves to {addresses}.", {
            addresses: <span className="font-mono text-fg">{data!.server_addresses.join(", ") || t("the server's public IP")}</span>,
          })}{" "}
          <button className="font-medium text-fg underline" onClick={() => setShowRecords(true)}>
            {t("Show required DNS records")}
          </button>
        </div>
      )}

      <DomainModal form={form} onChange={setForm} onSave={() => form && save.mutate(form)} saving={save.isPending} appPort={app.port} compose={compose ? { services, main: app.compose_service } : null} />
      <DNSRecordsModal appId={app.id} open={showRecords} onClose={() => setShowRecords(false)} />
    </div>
  );
}

function DomainModal({
  form,
  onChange,
  onSave,
  saving,
  appPort,
  compose,
}: {
  form: DomainForm | null;
  onChange: (f: DomainForm | null) => void;
  onSave: () => void;
  saving: boolean;
  appPort: number;
  compose: { services: ComposeService[]; main: string } | null;
}) {
  const set = (patch: Partial<DomainForm>) => form && onChange({ ...form, ...patch });
  const target = compose && form ? compose.services.find((s) => s.name === (form.service || compose.main)) : undefined;
  const defaultPort = compose ? (form?.service ? target?.port || 0 : appPort || target?.port || 0) : appPort;
  const host = form?.domain.replace(/^https?:\/\//, "").split("/")[0].replace(/^www\./, "") || "example.com";
  return (
    <Modal
      open={!!form}
      onClose={() => onChange(null)}
      title={form?.id ? t("Edit domain") : t("Add domain")}
      description={t("Changes reach the proxy within seconds — no redeploy needed.")}
      footer={
        <>
          <Button onClick={() => onChange(null)}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!form?.domain.trim()} onClick={onSave}>
            {form?.id ? t("Save changes") : t("Add domain")}
          </Button>
        </>
      }
    >
      {form && (
        <form
          className="space-y-5"
          onSubmit={(e) => {
            e.preventDefault();
            onSave();
          }}
        >
          <Field label={t("Domain")} required hint={t("A hostname, optionally with a path prefix, e.g. https://app.example.com or example.com/api")}>
            <Input autoFocus value={form.domain} onChange={(e) => set({ domain: e.target.value })} placeholder="https://app.example.com" />
          </Field>
          {compose && (
            <Field label={t("Service")} hint={t("The service of docker-compose.yml that receives this domain's traffic, e.g. the frontend or the backend.")}>
              {compose.services.length > 0 ? (
                <Select value={form.service} onChange={(e) => set({ service: e.target.value })}>
                  <option value="">{compose.main ? t("Main service ({name})", { name: compose.main }) : t("Main service")}</option>
                  {compose.services.map((s) => (
                    <option key={s.name} value={s.name}>
                      {s.port ? `${s.name} · ${s.port}` : s.name}
                    </option>
                  ))}
                </Select>
              ) : (
                <Input value={form.service} onChange={(e) => set({ service: e.target.value.trim() })} placeholder={t("Main service")} />
              )}
            </Field>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label={t("Internal port")}
              hint={defaultPort ? t("Container port that receives the traffic. Default: {port}.", { port: defaultPort }) : t("Container port that receives the traffic. Required: the service declares no single port.")}
            >
              <Input type="number" value={form.port} onChange={(e) => set({ port: e.target.value })} placeholder={defaultPort ? String(defaultPort) : ""} />
            </Field>
            <Field label={t("Domain redirect")} hint={t("Serve both the apex and www names, redirecting one to the other.")}>
              <Select value={form.www_redirect} onChange={(e) => set({ www_redirect: e.target.value as WWWRedirect })}>
                <option value="none">{t("Disabled")}</option>
                <option value="to-www">{host} → www.{host}</option>
                <option value="to-apex">www.{host} → {host}</option>
              </Select>
            </Field>
          </div>
          <div className="space-y-4 rounded-lg border border-border bg-surface-2/40 p-4">
            <Switch checked={form.https} onChange={(v) => set({ https: v })} label="HTTPS" description={t("Issue a Let's Encrypt certificate and redirect HTTP → HTTPS.")} />
            {form.https && (
              <Field label={t("Certificate")} hint={form.cert_mode === "dns" ? t("Uses the DNS provider from Settings → Certificates. Required for wildcard and internal-only domains.") : form.cert_mode === "custom" ? t("Served from a certificate uploaded in Settings → Certificates.") : t("Let's Encrypt over HTTP-01; the domain must be reachable from the internet on port 80.")}>
                <Select value={form.cert_mode} onChange={(e) => set({ cert_mode: e.target.value as CertMode })}>
                  <option value="auto">{t("Let's Encrypt — automatic")}</option>
                  <option value="dns">{t("Let's Encrypt — DNS challenge")}</option>
                  <option value="custom">{t("Uploaded certificate")}</option>
                </Select>
              </Field>
            )}
            <Switch checked={form.indexing} onChange={(v) => set({ indexing: v })} label={t("Allow search engine indexing")} description={t("When off, responses carry X-Robots-Tag: noindex, nofollow.")} />
          </div>
        </form>
      )}
    </Modal>
  );
}

function DNSRecordsModal({ appId, open, onClose }: { appId: number; open: boolean; onClose: () => void }) {
  const { data, isLoading } = useQuery({
    queryKey: ["dns-records", appId],
    queryFn: () => api.get<{ records: DNSRecord[]; server_addresses: boolean }>(`/applications/${appId}/dns-records`),
    enabled: open,
  });
  return (
    <Modal open={open} onClose={onClose} title={t("DNS records")} description={t("Create these records at your DNS provider (registrar, Cloudflare, Route 53…).")} size="lg">
      {isLoading ? (
        <Loading rows={2} />
      ) : !data?.server_addresses ? (
        <p className="text-[13px] text-muted">
          {tx("The server's public IP is unknown. Set it in {path} to generate the records.", {
            path: <span className="font-medium text-fg">{t("Settings → General → Network addresses")}</span>,
          })}
        </p>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border">
          <Table>
            <thead>
              <tr>
                <th>{t("Type")}</th>
                <th>{t("Name")}</th>
                <th>{t("Value")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.records.map((r, i) => (
                <tr key={i}>
                  <td>
                    <Badge>{r.type}</Badge>
                  </td>
                  <td className="font-mono text-xs">{r.name}</td>
                  <td className={cn("font-mono text-xs")}>{r.value}</td>
                  <td className="text-right">
                    <CopyButton value={r.value} />
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
    </Modal>
  );
}

/** Compact “Links” dropdown listing the application's URLs. */
export function DomainLinks({ appId, extra = [] }: { appId: number; extra?: string[] }) {
  const { data } = useDomains(appId);
  const count = (data?.domains.length ?? 0) + extra.length;
  if (!count) return null;
  return (
    <Menu
      trigger={
        <Button icon={<Globe />} trailing={<span className="text-2xs text-subtle">{count}</span>}>
          {t("Links")}
        </Button>
      }
    >
      {extra.map((u) => (
        <a key={u} href={u} target="_blank" rel="noreferrer">
          <MenuItem icon={<Globe />}>
            <span className="truncate">{u}</span>
          </MenuItem>
        </a>
      ))}
      {(data?.domains ?? []).map((d) => (
        <a key={d.id} href={domainURL(d)} target="_blank" rel="noreferrer">
          <MenuItem icon={<Globe />}>
            <span className="truncate">{domainURL(d)}</span>
          </MenuItem>
        </a>
      ))}
    </Menu>
  );
}

interface Suggestion {
  domain: string;
  kind: "wildcard" | "sslip" | "localhost";
}

export function useSuggestDomain(appId: number) {
  return () => api.get<Suggestion>(`/applications/${appId}/domains/suggest`);
}

/** Host-port URLs of an application (e.g. "8080:80" → http://localhost:8080). */
export function publishedURLs(app: ApplicationDetail, serverHost: string): string[] {
  return app.ports_mapping
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean)
    .map((p) => {
      const parts = p.split(":");
      const hostPort = parts.length >= 2 ? parts[parts.length - 2] : parts[0];
      return `http://${serverHost}:${hostPort.replace(/\/.*/, "")}`;
    });
}

/**
 * Shown on the overview when nothing routes traffic to the application: offers
 * a generated domain (Coolify-style) or publishing the container port.
 */
export function AccessCallout({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const { data } = useDomains(app.id);
  const suggest = useSuggestDomain(app.id);
  const generate = useMutation({
    mutationFn: async () => {
      const s = await suggest();
      return { s, r: await api.post<DomainsResponse>(`/applications/${app.id}/domains`, { domain: s.domain }) };
    },
    onSuccess: ({ s, r }) => {
      qc.setQueryData(["domains", app.id], r);
      qc.invalidateQueries({ queryKey: ["application", app.id] });
      if (r.routing_warning) toast.warning(r.routing_warning);
      else
        toast.success(t("Domain added"), {
          description: s.kind === "localhost" ? t("Open {url} in your browser (*.localhost resolves to this machine).", { url: s.domain }) : t("Open {url}", { url: s.domain }),
          action: { label: t("Open"), onClick: () => window.open(s.domain, "_blank") },
        });
    },
    onError: (e) => toast.error(e.message),
  });
  const publish = useMutation({
    mutationFn: async () => {
      await api.patch(`/applications/${app.id}`, { ports_mapping: `${app.port}:${app.port}` });
      await api.post(`/applications/${app.id}/deploy`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["application", app.id] });
      toast.success(t("Redeploying with port {port} published", { port: app.port }), { description: t("Available at {url} once the deployment finishes.", { url: `http://${app.server_host}:${app.port}` }) });
    },
    onError: (e) => toast.error(e.message),
  });

  if (!data || data.domains.length > 0 || app.ports_mapping.trim() || app.build_pack === "compose") return null;
  return (
    <div className="flex flex-wrap items-center gap-4 rounded-xl border border-warning/30 bg-warning-soft px-5 py-4">
      <Globe className="size-5 shrink-0 text-warning" />
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-semibold">{t("Not reachable from your browser yet")}</div>
        <p className="mt-0.5 text-xs leading-relaxed text-muted">
          {tx("The container only listens inside the Docker network at {address}. Add a domain to route it through the proxy, or publish port {port} directly on the server.", {
            address: (
              <code className="font-mono text-fg">
                http://{app.uuid}:{app.port}
              </code>
            ),
            port: app.port,
          })}
        </p>
      </div>
      <div className="flex gap-2">
        <Button size="sm" onClick={() => publish.mutate()} loading={publish.isPending}>
          {t("Publish port {port}", { port: app.port })}
        </Button>
        <Button size="sm" variant="primary" icon={<Globe />} onClick={() => generate.mutate()} loading={generate.isPending}>
          {t("Generate domain")}
        </Button>
      </div>
    </div>
  );
}
