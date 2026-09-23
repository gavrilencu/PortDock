import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileKey2, RefreshCw, Save, ShieldCheck, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { timeAgo } from "../lib/utils";
import { CertBadge } from "./Domains";
import { Badge, Button, Card, Empty, Field, Input, Loading, Modal, PasswordInput, Select, Table, Textarea, useConfirm } from "./ui";

type CertRow = {
  domain_id: number;
  host: string;
  application_id: number;
  application: string;
  mode: string;
  status: string;
  issuer: string;
  expires_at: string;
  days_left: number | null;
  checked_at: string;
  detail: string;
};
type CustomCert = { id: number; name: string; domains: string; issuer: string; not_before: string; not_after: string; created_at: string };
type Inventory = { domains: CertRow[]; custom: CustomCert[]; renew_days: number };
type Provider = { id: string; name: string; env: string[]; hint?: string };
type CertSettings = {
  acme_email: string;
  acme_server: string;
  dns_provider: string;
  dns_env: Record<string, string>;
  dns_resolvers: string;
  renew_days: number;
  providers: Provider[];
  acme_servers: { id: string; name: string }[];
};

const modeLabel: Record<string, string> = { auto: "Let's Encrypt", dns: "Let's Encrypt · DNS", custom: "Uploaded" };

function Inventory() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["certificates"], queryFn: () => api.get<Inventory>("/certificates") });
  const [filter, setFilter] = useState("");
  const check = useMutation({
    mutationFn: () => api.post<Inventory>("/certificates/check"),
    onSuccess: (d) => {
      qc.setQueryData(["certificates"], d);
      const bad = d.domains.filter((x) => ["expired", "missing", "expiring"].includes(x.status)).length;
      bad ? toast.warning(`${bad} certificate(s) need attention`) : toast.success("All certificates are healthy");
    },
    onError: (e) => toast.error(e.message),
  });
  const rows = useMemo(() => (q.data?.domains ?? []).filter((r) => `${r.host} ${r.application}`.toLowerCase().includes(filter.toLowerCase())), [q.data, filter]);
  if (q.isLoading) return <Loading />;
  const d = q.data!;
  const counts = d.domains.reduce<Record<string, number>>((m, r) => ((m[r.status || "unchecked"] = (m[r.status || "unchecked"] ?? 0) + 1), m), {});
  return (
    <Card
      title="Certificates"
      description={`Issued and renewed automatically. PortDock checks every 6 hours and forces a new certificate when fewer than ${d.renew_days} days remain.`}
      actions={
        <Button size="sm" icon={<RefreshCw />} loading={check.isPending} onClick={() => check.mutate()}>
          Check now
        </Button>
      }
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <Input className="max-w-xs" placeholder="Filter domains…" value={filter} onChange={(e) => setFilter(e.target.value)} />
        {Object.entries(counts).map(([k, n]) => (
          <Badge key={k} tone={k === "valid" ? "success" : k === "expiring" ? "warning" : k === "expired" || k === "missing" ? "danger" : "neutral"}>
            {n} {k}
          </Badge>
        ))}
      </div>
      {rows.length === 0 ? (
        <div className="p-5">
          <Empty compact icon={<ShieldCheck />} title="No HTTPS domains" description="Certificates appear here once applications have HTTPS domains." />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>Domain</th>
              <th>Source</th>
              <th>Status</th>
              <th>Issuer</th>
              <th>Checked</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.domain_id}>
                <td>
                  <div className="font-medium">{r.host}</div>
                  <Link to={`/applications/${r.application_id}?tab=domains`} className="text-xs text-muted hover:underline">
                    {r.application}
                  </Link>
                </td>
                <td className="text-xs text-muted">{modeLabel[r.mode] ?? r.mode}</td>
                <td>
                  <CertBadge d={{ https: true, cert_status: r.status as never, cert_expires_at: r.expires_at, cert_issuer: r.issuer, cert_error: r.detail }} />
                  {r.detail && r.status !== "valid" && <div className="mt-1 max-w-sm text-2xs text-muted">{r.detail}</div>}
                </td>
                <td className="max-w-56 truncate text-xs text-muted">{r.issuer || "—"}</td>
                <td className="text-xs text-muted">{r.checked_at ? timeAgo(r.checked_at) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function CustomCertificates() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useQuery({ queryKey: ["certificates"], queryFn: () => api.get<Inventory>("/certificates") });
  const [form, setForm] = useState<{ name: string; certificate: string; private_key: string } | null>(null);
  const upload = useMutation({
    mutationFn: () => api.post("/custom-certificates", form),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["certificates"] });
      setForm(null);
      toast.success("Certificate uploaded and deployed to every proxy");
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/custom-certificates/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["certificates"] }),
    onError: (e) => toast.error(e.message),
  });
  const readFile = (key: "certificate" | "private_key") => (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (f && form) f.text().then((t) => setForm({ ...form, [key]: t }));
  };
  const custom = q.data?.custom ?? [];
  return (
    <Card
      title="Uploaded certificates"
      description="Your own certificates (corporate CA, EV, OV). Set a domain's certificate to “Uploaded certificate” to use them. You're notified 30, 14, 7 and 3 days before they expire."
      actions={
        <Button size="sm" variant="primary" icon={<Upload />} onClick={() => setForm({ name: "", certificate: "", private_key: "" })}>
          Upload
        </Button>
      }
    >
      {custom.length === 0 ? (
        <div className="p-5">
          <Empty compact icon={<FileKey2 />} title="No uploaded certificates" />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>Certificate</th>
              <th>Covers</th>
              <th>Expires</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {custom.map((c) => {
              const days = Math.floor((new Date(c.not_after).getTime() - Date.now()) / 86_400_000);
              return (
                <tr key={c.id}>
                  <td>
                    <div className="font-medium">{c.name}</div>
                    <div className="text-xs text-muted">{c.issuer}</div>
                  </td>
                  <td className="max-w-72 text-xs text-muted">{c.domains.split(",").join(", ")}</td>
                  <td>
                    <Badge tone={days < 7 ? "danger" : days < 30 ? "warning" : "success"}>{days} days</Badge>
                    <div className="mt-0.5 text-2xs text-muted">{new Date(c.not_after).toLocaleDateString()}</div>
                  </td>
                  <td className="text-right">
                    <Button size="icon-sm" variant="ghost" title="Delete" onClick={async () => (await confirm({ title: `Delete ${c.name}?`, description: "Domains using it fall back to the proxy's default certificate.", danger: true, confirmText: "Delete" })) && del.mutate(c.id)}>
                      <Trash2 className="text-danger" />
                    </Button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </Table>
      )}
      <Modal
        open={!!form}
        onClose={() => setForm(null)}
        size="lg"
        title="Upload certificate"
        description="PEM format. Include the intermediate certificates after the server certificate. The key is stored encrypted."
        footer={
          <Button variant="primary" loading={upload.isPending} disabled={!form?.certificate || !form?.private_key} onClick={() => upload.mutate()}>
            Upload & deploy
          </Button>
        }
      >
        {form && (
          <div className="space-y-4">
            <Field label="Name">
              <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Corporate wildcard 2026" />
            </Field>
            <Field label="Certificate chain (PEM)" hint={<input type="file" accept=".pem,.crt,.cer" onChange={readFile("certificate")} className="text-xs" />}>
              <Textarea rows={6} className="font-mono text-2xs" value={form.certificate} onChange={(e) => setForm({ ...form, certificate: e.target.value })} placeholder="-----BEGIN CERTIFICATE-----" />
            </Field>
            <Field label="Private key (PEM, unencrypted)" hint={<input type="file" accept=".pem,.key" onChange={readFile("private_key")} className="text-xs" />}>
              <Textarea rows={5} className="font-mono text-2xs" value={form.private_key} onChange={(e) => setForm({ ...form, private_key: e.target.value })} placeholder="-----BEGIN PRIVATE KEY-----" />
            </Field>
          </div>
        )}
      </Modal>
    </Card>
  );
}

function ACMESettings() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["cert-settings"], queryFn: () => api.get<CertSettings>("/settings/certificates") });
  const [f, setF] = useState<Partial<CertSettings>>({});
  const [customCA, setCustomCA] = useState(false);
  const [other, setOther] = useState(false);
  useEffect(() => {
    if (q.data) {
      setF(q.data);
      setCustomCA(!!q.data.acme_server && !q.data.acme_servers.some((s) => s.id === q.data.acme_server));
      setOther(!!q.data.dns_provider && !q.data.providers.some((p) => p.id === q.data.dns_provider));
    }
  }, [q.data]);
  const save = useMutation({
    mutationFn: () => api.put<CertSettings>("/settings/certificates", f),
    onSuccess: (d) => {
      qc.setQueryData(["cert-settings"], d);
      toast.success("Certificate settings saved", { description: "Proxies are reconfigured automatically (a few seconds of restart)." });
    },
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading rows={2} />;
  const d = q.data!;
  const provider = other ? d.providers.find((p) => p.id === "other") : d.providers.find((p) => p.id === f.dns_provider);
  const env = f.dns_env ?? {};
  const envKeys = provider?.id === "other" ? Object.keys(env).concat("") : (provider?.env ?? []);
  return (
    <Card
      title="Certificate authority"
      description="How certificates are requested. Changing these settings reconfigures the proxy on every server."
      actions={
        <Button size="sm" variant="primary" icon={<Save />} loading={save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
      }
    >
      <div className="grid gap-5 p-5 md:grid-cols-2">
        <Field label="Contact email" hint="Receives expiry warnings from the certificate authority.">
          <Input value={f.acme_email ?? ""} onChange={(e) => setF({ ...f, acme_email: e.target.value })} placeholder="ops@example.com" />
        </Field>
        <Field label="Renew when fewer than (days)" hint="Traefik renews at 30 days; below this threshold PortDock forces a renewal and alerts.">
          <Input type="number" min={3} max={60} value={f.renew_days ?? 14} onChange={(e) => setF({ ...f, renew_days: Number(e.target.value) })} />
        </Field>
        <Field label="Certificate authority">
          <Select
            value={customCA ? "custom" : (f.acme_server ?? "")}
            onChange={(e) => {
              if (e.target.value === "custom") {
                setCustomCA(true);
                return;
              }
              setCustomCA(false);
              setF({ ...f, acme_server: e.target.value });
            }}
          >
            {d.acme_servers.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
            <option value="custom">Custom ACME server (step-ca, internal PKI…)</option>
          </Select>
        </Field>
        {customCA && (
          <Field label="ACME directory URL">
            <Input value={f.acme_server ?? ""} onChange={(e) => setF({ ...f, acme_server: e.target.value })} placeholder="https://ca.corp.example/acme/acme/directory" className="font-mono text-xs" />
          </Field>
        )}
      </div>
      <div className="border-t border-border p-5">
        <div className="mb-1 text-[13px] font-semibold">DNS challenge</div>
        <p className="mb-4 text-xs text-muted">Needed for wildcard certificates (*.apps.example.com) and for domains that aren't reachable from the internet. Credentials are stored encrypted and written to a 0600 file on each server.</p>
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="DNS provider" hint={provider?.hint}>
            <Select
              value={other ? "other" : (f.dns_provider ?? "")}
              onChange={(e) => {
                setOther(e.target.value === "other");
                setF({ ...f, dns_provider: e.target.value === "other" ? "" : e.target.value, dns_env: {} });
              }}
            >
              <option value="">Disabled</option>
              {d.providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
          {provider?.id === "other" && (
            <Field label="Provider code" hint="As listed in the Traefik / lego documentation, e.g. ns1, linode, porkbun.">
              <Input value={f.dns_provider ?? ""} onChange={(e) => setF({ ...f, dns_provider: e.target.value })} className="font-mono text-xs" />
            </Field>
          )}
          {envKeys.map((k, i) =>
            provider?.id === "other" ? (
              <div key={i} className="grid grid-cols-2 gap-2 md:col-span-2">
                <Input
                  placeholder="VARIABLE_NAME"
                  className="font-mono text-xs"
                  value={k}
                  onChange={(e) => {
                    const next = { ...env };
                    const v = next[k] ?? "";
                    delete next[k];
                    if (e.target.value) next[e.target.value.toUpperCase()] = v;
                    setF({ ...f, dns_env: next });
                  }}
                />
                <PasswordInput placeholder="value" value={env[k] ?? ""} disabled={!k} onChange={(e) => setF({ ...f, dns_env: { ...env, [k]: e.target.value } })} />
              </div>
            ) : (
              <Field key={k} label={<code className="text-xs">{k}</code>}>
                {k === "GCE_SERVICE_ACCOUNT" ? (
                  <Textarea rows={3} className="font-mono text-2xs" value={env[k] ?? ""} onChange={(e) => setF({ ...f, dns_env: { ...env, [k]: e.target.value } })} />
                ) : (
                  <PasswordInput value={env[k] ?? ""} onChange={(e) => setF({ ...f, dns_env: { ...env, [k]: e.target.value } })} />
                )}
              </Field>
            ),
          )}
          {(f.dns_provider || other) && (
            <Field label="Propagation resolvers" hint="Optional. For internal zones use your own DNS servers, e.g. 10.0.0.10:53.">
              <Input value={f.dns_resolvers ?? ""} onChange={(e) => setF({ ...f, dns_resolvers: e.target.value })} placeholder="1.1.1.1:53,8.8.8.8:53" className="font-mono text-xs" />
            </Field>
          )}
        </div>
      </div>
    </Card>
  );
}

export function CertificatesSettings() {
  return (
    <div className="space-y-6">
      <Inventory />
      <ACMESettings />
      <CustomCertificates />
    </div>
  );
}
