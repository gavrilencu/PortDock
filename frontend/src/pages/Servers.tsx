import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, KeyRound, Plus, Server as ServerIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Server, SSHKey } from "../lib/types";
import { AdminOnly } from "../components/Layout";
import { Badge, Button, CopyField, Field, Input, Loading, Modal, PageHeader, Select, StatusBadge } from "../components/ui";
import { useServers } from "../components/shared";
import { useRegistries } from "../components/registries";
import { timeAgo } from "../lib/utils";
import { t, tx } from "../lib/i18n";

export function ServersPage() {
  const { data, isLoading } = useServers();
  const [params, setParams] = useSearchParams();
  const open = params.get("new") === "1";
  const setOpen = (v: boolean) => setParams(v ? { new: "1" } : {});
  return (
    <>
      <PageHeader
        title={t("Servers")}
        subtitle={t("Compute capacity available to your organization. Connect any Linux host over SSH, or a Kubernetes cluster.")}
        actions={
          <AdminOnly>
            <Button variant="primary" icon={<Plus />} onClick={() => setOpen(true)}>
              {t("Connect server")}
            </Button>
          </AdminOnly>
        }
      />
      {isLoading ? (
        <Loading />
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {data!.map((s) => (
            <Link key={s.id} to={`/servers/${s.id}`} className="rounded-xl border border-border bg-surface p-5 shadow-card transition-[border,box-shadow] hover:border-border-strong hover:shadow-pop">
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-3">
                  <div className="flex size-10 items-center justify-center rounded-lg border border-border bg-surface-2 text-muted">
                    {s.kind === "kubernetes" ? <Boxes className="size-5" /> : <ServerIcon className="size-5" />}
                  </div>
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 font-semibold">
                      {s.name} {s.is_local && <Badge>{t("Local")}</Badge>} {s.kind === "kubernetes" && <Badge>Kubernetes</Badge>}
                    </div>
                    <div className="truncate font-mono text-xs text-muted">
                      {s.kind === "kubernetes" ? s.host.replace(/^https?:\/\//, "") : s.is_local ? t("this machine") : `${s.user}@${s.host}:${s.port}`}
                    </div>
                  </div>
                </div>
                <StatusBadge status={s.status} />
              </div>
              <div className="mt-5 grid grid-cols-3 gap-3 border-t border-border pt-4 text-xs">
                <div>
                  <div className="text-subtle">{s.kind === "kubernetes" ? "Kubernetes" : "Docker"}</div>
                  <div className="mt-0.5 truncate font-medium">{s.docker_version || "—"}</div>
                </div>
                {s.kind === "kubernetes" ? (
                  <div>
                    <div className="text-subtle">{t("Namespace")}</div>
                    <div className="mt-0.5 truncate font-medium">{s.kube_namespace}</div>
                  </div>
                ) : (
                  <div>
                    <div className="text-subtle">{t("Proxy")}</div>
                    <div className="mt-0.5 font-medium capitalize">{s.proxy_status}</div>
                  </div>
                )}
                <div>
                  <div className="text-subtle">{t("Last check")}</div>
                  <div className="mt-0.5 font-medium">{timeAgo(s.last_check_at)}</div>
                </div>
              </div>
              {s.status === "unreachable" && s.status_message && <p className="mt-4 line-clamp-2 text-xs text-danger">{s.status_message}</p>}
            </Link>
          ))}
        </div>
      )}
      <AddServerModal open={open} onClose={() => setOpen(false)} />
    </>
  );
}

type KubeSettings = Pick<Server, "kube_ingress_class" | "kube_cluster_issuer" | "kube_storage_class" | "kube_volume_size" | "kube_registry_id">;

/** The settings of a cluster that applications are deployed with. */
export function KubeFields({ value, onChange }: { value: KubeSettings; onChange: (v: Partial<KubeSettings>) => void }) {
  const regs = useRegistries();
  return (
    <>
      <Field label={t("Registry of the cluster")} hint={t("Applications built from Git without a registry of their own push their images here, and the cluster pulls them from it.")} className="md:col-span-2">
        <Select value={value.kube_registry_id || 0} onChange={(e) => onChange({ kube_registry_id: Number(e.target.value) })}>
          <option value={0}>{t("None: each application chooses one")}</option>
          {regs.data?.registries.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name} ({r.host})
            </option>
          ))}
        </Select>
      </Field>
      <Field label={t("Ingress class")} hint={t("Empty: the cluster's default (nginx, traefik…).")}>
        <Input value={value.kube_ingress_class} onChange={(e) => onChange({ kube_ingress_class: e.target.value })} placeholder="nginx" />
      </Field>
      <Field label={t("Certificate issuer")} hint={t("A cert-manager ClusterIssuer, for a certificate per HTTPS domain.")}>
        <Input value={value.kube_cluster_issuer} onChange={(e) => onChange({ kube_cluster_issuer: e.target.value })} placeholder="letsencrypt" />
      </Field>
      <Field label={t("Storage class")} hint={t("Empty: the cluster's default.")}>
        <Input value={value.kube_storage_class} onChange={(e) => onChange({ kube_storage_class: e.target.value })} />
      </Field>
      <Field label={t("Volume size")} hint={t("Size of each new application volume (default 5Gi).")}>
        <Input value={value.kube_volume_size} onChange={(e) => onChange({ kube_volume_size: e.target.value })} placeholder="5Gi" />
      </Field>
    </>
  );
}

function AddServerModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const keys = useQuery({ queryKey: ["ssh-keys"], queryFn: () => api.get<SSHKey[]>("/ssh-keys"), enabled: open });
  const [f, setF] = useState({ name: "", host: "", port: 22, user: "root", ssh_key_id: 0, wildcard_domain: "", proxy_enabled: true });
  const [kind, setKind] = useState<"docker" | "kubernetes">("docker");
  const [k, setK] = useState({ kubeconfig: "", kube_namespace: "portdock", kube_ingress_class: "", kube_cluster_issuer: "", kube_storage_class: "", kube_volume_size: "", kube_registry_id: 0 });
  const [newKey, setNewKey] = useState<{ name: string; private_key: string } | null>(null);
  const keyId = f.ssh_key_id || keys.data?.[0]?.id || 0;
  const selectedKey = keys.data?.find((k) => k.id === keyId);

  const createKey = useMutation({
    mutationFn: () => api.post<SSHKey>("/ssh-keys", newKey),
    onSuccess: (k) => {
      qc.invalidateQueries({ queryKey: ["ssh-keys"] });
      setF({ ...f, ssh_key_id: k.id });
      setNewKey(null);
      toast.success(t("SSH key ready — add the public key to the server"));
    },
    onError: (e) => toast.error(e.message),
  });
  const create = useMutation({
    mutationFn: () =>
      api.post<Server>("/servers", kind === "kubernetes" ? { kind, name: f.name, wildcard_domain: f.wildcard_domain, ...k } : { ...f, ssh_key_id: keyId }),
    onSuccess: (s) => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      onClose();
      if (s.status === "reachable") toast.success(t("{name} connected", { name: s.name }));
      else toast.warning(t("Server added but not reachable yet: {message}", { message: s.status_message }));
      nav(`/servers/${s.id}`);
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="lg"
      title={t("Connect a server")}
      description={
        kind === "kubernetes"
          ? t("PortDock deploys applications to one namespace of the cluster, with the credentials of the kubeconfig.")
          : t("PortDock connects over SSH (root or a user with Docker + sudo access).")
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("Cancel")}
          </Button>
          <Button variant="primary" loading={create.isPending} disabled={!f.name || (kind === "kubernetes" ? !k.kubeconfig.trim() : !f.host || !keyId)} onClick={() => create.mutate()}>
            {t("Add & validate")}
          </Button>
        </>
      }
    >
      <div className="mb-4 grid grid-cols-2 gap-2">
        {(["docker", "kubernetes"] as const).map((v) => (
          <button
            key={v}
            type="button"
            onClick={() => setKind(v)}
            className={`flex items-center gap-3 rounded-lg border p-3 text-left text-sm transition-colors ${kind === v ? "border-accent bg-accent/5" : "border-border hover:border-border-strong"}`}
          >
            {v === "docker" ? <ServerIcon className="size-5 shrink-0 text-muted" /> : <Boxes className="size-5 shrink-0 text-muted" />}
            <span>
              <span className="block font-medium">{v === "docker" ? t("Linux server") : t("Kubernetes cluster")}</span>
              <span className="block text-xs text-muted">{v === "docker" ? t("Docker, over SSH") : t("One namespace, with a kubeconfig")}</span>
            </span>
          </button>
        ))}
      </div>
      {kind === "kubernetes" && (
        <div className="grid gap-4 md:grid-cols-2">
          <div className="rounded-lg border border-info/30 bg-info/5 p-4 text-sm md:col-span-2">
            <p className="mb-2 text-muted">{t("Run this where kubectl reaches the cluster: it creates the namespace and a service account limited to it, and prints the kubeconfig to paste below.")}</p>
            <CopyField value={`curl -fsSL https://raw.githubusercontent.com/gavrilencu/PortDock/main/deploy/kubernetes/connect.sh | bash -s -- ${k.kube_namespace || "portdock"}`} />
          </div>
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="prod-cluster" autoFocus />
          </Field>
          <Field label={t("Namespace")} hint={t("Created if it does not exist. Everything PortDock deploys stays in it.")}>
            <Input value={k.kube_namespace} onChange={(e) => setK({ ...k, kube_namespace: e.target.value })} placeholder="portdock" />
          </Field>
          <Field
            label="Kubeconfig"
            hint={t("With an embedded token or certificate (a service account is best). Files of cloud providers that run a program to sign in are refused. Stored encrypted.")}
            className="md:col-span-2"
          >
            <textarea
              className="h-36 w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-xs shadow-xs focus:border-accent focus:outline-none focus:ring-[3px] focus:ring-[var(--ring)]"
              value={k.kubeconfig}
              onChange={(e) => setK({ ...k, kubeconfig: e.target.value })}
              placeholder={"apiVersion: v1\nkind: Config\nclusters: …"}
              spellCheck={false}
            />
          </Field>
          <KubeFields value={k} onChange={(v) => setK({ ...k, ...v })} />
          <Field label={t("Wildcard domain")} hint={t("Optional: apps get <name>.<domain> automatically. Point *.domain to the cluster's ingress.")} className="md:col-span-2">
            <Input value={f.wildcard_domain} onChange={(e) => setF({ ...f, wildcard_domain: e.target.value })} placeholder="apps.example.com" />
          </Field>
          <p className="text-xs text-muted md:col-span-2">
            {t("Images are built on a Docker server and pushed to the application's registry, where the cluster pulls them. Databases, one-click services and Docker Compose applications stay on Docker servers.")}
          </p>
        </div>
      )}
      <div className={kind === "kubernetes" ? "hidden" : "grid gap-4 md:grid-cols-2"}>
        <Field label={t("Name")}>
          <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="prod-1" autoFocus />
        </Field>
        <Field label={t("Host / IP")}>
          <Input value={f.host} onChange={(e) => setF({ ...f, host: e.target.value })} placeholder="203.0.113.10" />
        </Field>
        <Field label={t("SSH user")}>
          <Input value={f.user} onChange={(e) => setF({ ...f, user: e.target.value })} />
        </Field>
        <Field label={t("SSH port")}>
          <Input type="number" value={f.port} onChange={(e) => setF({ ...f, port: Number(e.target.value) })} />
        </Field>
        <Field label={t("Wildcard domain")} hint={t("Optional: apps get <name>.<domain> automatically. Point *.domain to this server.")} className="md:col-span-2">
          <Input value={f.wildcard_domain} onChange={(e) => setF({ ...f, wildcard_domain: e.target.value })} placeholder="apps.example.com" />
        </Field>
        <div className="space-y-3 md:col-span-2">
          <Field label={t("SSH key")}>
            <div className="flex gap-2">
              <Select value={keyId} onChange={(e) => setF({ ...f, ssh_key_id: Number(e.target.value) })} className="flex-1">
                {!keys.data?.length && <option value={0}>{t("No keys yet")}</option>}
                {keys.data?.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name}
                  </option>
                ))}
              </Select>
              <Button icon={<KeyRound className="size-4" />} onClick={() => setNewKey({ name: `${f.name || "server"}-key`, private_key: "" })}>
                {t("New key")}
              </Button>
            </div>
          </Field>
          {newKey && (
            <div className="space-y-3 rounded-lg border border-border bg-surface-2/50 p-4">
              <Field label={t("Key name")}>
                <Input value={newKey.name} onChange={(e) => setNewKey({ ...newKey, name: e.target.value })} />
              </Field>
              <Field label={t("Private key")} hint={t("Leave empty to generate a new Ed25519 key pair.")}>
                <textarea
                  className="h-24 w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-xs shadow-xs focus:border-accent focus:outline-none focus:ring-[3px] focus:ring-[var(--ring)]"
                  value={newKey.private_key}
                  onChange={(e) => setNewKey({ ...newKey, private_key: e.target.value })}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                />
              </Field>
              <div className="flex justify-end gap-2">
                <Button size="sm" variant="ghost" onClick={() => setNewKey(null)}>
                  {t("Cancel")}
                </Button>
                <Button size="sm" variant="primary" loading={createKey.isPending} onClick={() => createKey.mutate()}>
                  {newKey.private_key ? t("Import key") : t("Generate key")}
                </Button>
              </div>
            </div>
          )}
          {selectedKey && (
            <div className="rounded-lg border border-info/30 bg-info/5 p-4 text-sm">
              <p className="mb-2 text-muted">
                {tx("Add this public key to {file} on the server:", { file: <code className="text-fg">~/.ssh/authorized_keys</code> })}
              </p>
              <CopyField value={selectedKey.public_key} />
              <p className="mt-2 font-mono text-xs text-muted">echo "{selectedKey.public_key.slice(0, 40)}…" &gt;&gt; ~/.ssh/authorized_keys</p>
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
}
