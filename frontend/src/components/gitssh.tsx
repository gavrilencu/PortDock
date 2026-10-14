import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail } from "../lib/types";
import { t } from "../lib/i18n";
import { timeAgo } from "../lib/utils";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Modal, Switch, Table, Textarea, useConfirm } from "./ui";

// Git over SSH: the deploy key of an application and the host keys of Git servers.

/** isSSHRepo reports whether a repository URL is cloned over SSH. */
export function isSSHRepo(url: string) {
  const u = url.trim();
  return u.startsWith("ssh://") || (/^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/]/.test(u) && !u.includes("://"));
}

function KeyHelp() {
  return <p className="text-xs text-muted">{t("Add it to the repository as a read-only deploy key (GitLab: Settings → Repository → Deploy keys; Bitbucket: Repository settings → Access keys; Gitea: Settings → Deploy keys).")}</p>;
}

/** DeployKeyField manages the deploy key in the application's settings. */
export function DeployKeyField({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [importing, setImporting] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["application", app.id] });
  const create = useMutation({
    mutationFn: (privateKey: string) => api.post<{ public_key: string }>(`/applications/${app.id}/deploy-key`, { private_key: privateKey }),
    onSuccess: () => {
      refresh();
      setImporting(null);
      toast.success(t("Deploy key saved"), { description: t("Add its public key to the repository before the next deployment.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/applications/${app.id}/deploy-key`),
    onSuccess: () => {
      refresh();
      toast.success(t("Deploy key removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Field label={t("Deploy key")} className="sm:col-span-2" hint={app.git_ssh_public_key ? undefined : t("The repository is cloned over SSH with a key of this application.")}>
      <div className="space-y-2">
        {app.git_ssh_public_key ? (
          <>
            <CopyField value={app.git_ssh_public_key} />
            <KeyHelp />
          </>
        ) : (
          <p className="text-xs text-warning">{t("No deploy key yet: deployments of this repository fail until you add one.")}</p>
        )}
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            size="sm"
            icon={<KeyRound className="size-3.5" />}
            loading={create.isPending && importing === null}
            onClick={async () => {
              if (!app.git_ssh_public_key || (await confirm({ title: t("Replace the deploy key?"), description: t("The current key stops working: add the new public key to the repository."), confirmText: t("Generate") })))
                create.mutate("");
            }}
          >
            {app.git_ssh_public_key ? t("Generate a new key") : t("Generate a deploy key")}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => setImporting("")}>
            {t("Use my own key")}
          </Button>
          {app.git_ssh_public_key && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              icon={<Trash2 className="size-3.5 text-danger" />}
              onClick={async () => {
                if (await confirm({ title: t("Remove the deploy key?"), danger: true, confirmText: t("Remove") })) remove.mutate();
              }}
            >
              {t("Remove")}
            </Button>
          )}
        </div>
      </div>
      <Modal
        open={importing !== null}
        onClose={() => setImporting(null)}
        title={t("Use my own key")}
        description={t("Paste an unencrypted OpenSSH private key (ed25519 or RSA). It is stored encrypted and only used to clone this repository.")}
        footer={
          <Button variant="primary" loading={create.isPending} disabled={!importing?.trim()} onClick={() => create.mutate(importing ?? "")}>
            {t("Save")}
          </Button>
        }
      >
        <Textarea value={importing ?? ""} onChange={(e) => setImporting(e.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" className="min-h-40" />
      </Modal>
    </Field>
  );
}

/** NewDeployKey shows the key generated for a new application before its first deployment. */
export function NewDeployKey({ app, publicKey, deploy, onDone }: { app: ApplicationDetail; publicKey: string; deploy: boolean; onDone: () => void }) {
  const run = useMutation({
    mutationFn: () => api.post(`/applications/${app.id}/deploy`),
    onSuccess: () => {
      toast.success(t("{name}: first deployment queued", { name: app.name }));
      onDone();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Modal
      open
      onClose={onDone}
      title={t("Add the deploy key to the repository")}
      description={t("PortDock clones {repo} over SSH with this key.", { repo: app.git_repository })}
      footer={
        <>
          <Button variant="ghost" onClick={onDone}>
            {t("Later")}
          </Button>
          {deploy && (
            <Button variant="primary" loading={run.isPending} onClick={() => run.mutate()}>
              {t("Key added — deploy now")}
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-3">
        <CopyField value={publicKey} />
        <KeyHelp />
      </div>
    </Modal>
  );
}

interface GitHost {
  host: string;
  keys: string;
  fingerprints: string;
  source: "admin" | "first use";
  created_at: string;
}

/** GitHostKeys lists the trusted host keys of Git servers (Settings → Git sources). */
export function GitHostKeys() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useQuery({ queryKey: ["git-hosts"], queryFn: () => api.get<{ hosts: GitHost[]; strict: boolean }>("/git/known-hosts") });
  const [add, setAdd] = useState<{ host: string; scanned?: { host: string; keys: string; fingerprints: string[] }; manual: boolean; name: string; keys: string } | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["git-hosts"] });
  const scan = useMutation({
    mutationFn: (host: string) => api.post<{ host: string; keys: string; fingerprints: string[] }>("/git/known-hosts/scan", { host }),
    onSuccess: (r) => setAdd((a) => a && { ...a, scanned: r }),
    onError: (e) => toast.error(e.message),
  });
  const trust = useMutation({
    mutationFn: (b: { host: string; keys: string }) => api.post("/git/known-hosts", b),
    onSuccess: () => {
      refresh();
      setAdd(null);
      toast.success(t("Host key trusted"));
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (host: string) => api.del(`/git/known-hosts?host=${encodeURIComponent(host)}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const strict = useMutation({
    mutationFn: (v: boolean) => api.put("/git/known-hosts/strict", { strict: v }),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const hosts = q.data?.hosts ?? [];
  return (
    <Card
      title={t("SSH host keys")}
      description={t("Repositories cloned over SSH (git@host:… or ssh://…) are checked against these keys, so a server pretending to be your Git host is refused.")}
      actions={
        <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setAdd({ host: "", manual: false, name: "", keys: "" })}>
          {t("Add host")}
        </Button>
      }
    >
      {hosts.length === 0 ? (
        <div className="p-5">
          <Empty compact icon={<KeyRound />} title={t("No Git hosts yet")} description={t("A host is added on its first clone (trust on first use), or here after you compare its fingerprint.")} />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("Host")}</th>
              <th>{t("Fingerprints")}</th>
              <th>{t("Added")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {hosts.map((h) => (
              <tr key={h.host}>
                <td className="align-top font-mono text-xs">{h.host}</td>
                <td className="align-top">
                  {h.fingerprints.split("\n").map((f) => (
                    <div key={f} className="font-mono text-2xs text-muted">
                      {f}
                    </div>
                  ))}
                </td>
                <td className="align-top text-xs text-muted">
                  <Badge tone={h.source === "admin" ? "success" : "warning"}>{h.source === "admin" ? t("Verified") : t("First use")}</Badge>
                  <div className="mt-1">{timeAgo(h.created_at)}</div>
                </td>
                <td className="text-right align-top">
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    title={t("Remove")}
                    onClick={async () => {
                      if (await confirm({ title: t("Remove {name}?", { name: h.host }), description: t("Its key is read again on the next clone, or refused in strict mode."), danger: true, confirmText: t("Remove") })) del.mutate(h.host);
                    }}
                  >
                    <Trash2 className="text-danger" />
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <div className="border-t border-border px-5 py-3">
        <Switch
          checked={!!q.data?.strict}
          onChange={(v) => strict.mutate(v)}
          label={t("Only trust hosts listed here")}
          description={t("Off: an unknown host is trusted on its first clone and its fingerprint written to the deployment log. On: unknown hosts are refused.")}
        />
      </div>
      <Modal
        open={!!add}
        onClose={() => setAdd(null)}
        title={t("Add a Git host")}
        description={t("Read the host's keys, compare the fingerprints with the ones your Git administrator publishes, then trust them.")}
        footer={
          add && (
            <Button
              variant="primary"
              loading={trust.isPending}
              disabled={add.manual ? !add.name.trim() || !add.keys.trim() : !add.scanned}
              onClick={() => (add.manual ? trust.mutate({ host: add.name.trim(), keys: add.keys }) : add.scanned && trust.mutate({ host: add.scanned.host, keys: add.scanned.keys }))}
            >
              {t("Trust these keys")}
            </Button>
          )
        }
      >
        {add && (
          <div className="space-y-4">
            {!add.manual ? (
              <>
                <Field label={t("Host")} hint={t("host, host:port or a repository URL, e.g. ssh://git@gitlab.corp:2222/team/app.git")}>
                  <div className="flex gap-2">
                    <Input value={add.host} onChange={(e) => setAdd({ ...add, host: e.target.value, scanned: undefined })} className="font-mono text-xs" placeholder="gitlab.corp.local" />
                    <Button loading={scan.isPending} disabled={!add.host.trim()} onClick={() => scan.mutate(add.host.trim())}>
                      {t("Read keys")}
                    </Button>
                  </div>
                </Field>
                {add.scanned && (
                  <div className="rounded-lg border border-border bg-surface-2/60 p-3">
                    <div className="mb-1 font-mono text-xs">{add.scanned.host}</div>
                    {add.scanned.fingerprints.map((f) => (
                      <div key={f} className="font-mono text-2xs text-muted">
                        {f}
                      </div>
                    ))}
                  </div>
                )}
                <button type="button" className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline" onClick={() => setAdd({ ...add, manual: true })}>
                  {t("Paste known_hosts lines instead")}
                </button>
              </>
            ) : (
              <>
                <Field label={t("Host")} hint={t("As known_hosts names it: host, or [host]:port for another port than 22.")}>
                  <Input value={add.name} onChange={(e) => setAdd({ ...add, name: e.target.value })} className="font-mono text-xs" placeholder="[gitlab.corp]:2222" />
                </Field>
                <Field label={t("known_hosts lines")}>
                  <Textarea value={add.keys} onChange={(e) => setAdd({ ...add, keys: e.target.value })} className="min-h-28" placeholder="[gitlab.corp]:2222 ssh-ed25519 AAAA…" />
                </Field>
              </>
            )}
          </div>
        )}
      </Modal>
    </Card>
  );
}
