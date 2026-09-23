import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ExternalLink, KeyRound, Lock, Plus, RefreshCw, Search, Settings2, Trash2, Webhook } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { GitHubApp, GitHubBranch, GitHubRepo } from "../lib/types";
import { cn, timeAgo } from "../lib/utils";
import { Badge, Button, Card, CopyField, Empty, ErrorBox, Field, Input, Modal, PasswordInput, Select, Skeleton, Textarea, useConfirm } from "./ui";

export function GitHubMark({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="currentColor" aria-hidden>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

export function useGitHubApps(enabled = true) {
  return useQuery({ queryKey: ["github-apps"], queryFn: () => api.get<GitHubApp[]>("/github/apps"), enabled, retry: false });
}

interface RepoListResponse {
  repositories: GitHubRepo[];
  errors: { account: string; error: string }[];
}

export function useGitHubRepos() {
  return useQuery({ queryKey: ["github-repos"], queryFn: () => api.get<RepoListResponse>("/github/repositories"), staleTime: 60_000 });
}

/** Searchable list of every repository the connected GitHub Apps can access. */
export function RepoPicker({ selected, onSelect }: { selected?: GitHubRepo | null; onSelect: (r: GitHubRepo) => void }) {
  const qc = useQueryClient();
  const { data, isLoading, error, isFetching } = useGitHubRepos();
  const [q, setQ] = useState("");
  const [owner, setOwner] = useState("");
  const owners = useMemo(() => [...new Set((data?.repositories ?? []).map((r) => r.owner))].sort(), [data]);
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (data?.repositories ?? []).filter((r) => (!owner || r.owner === owner) && (!needle || `${r.full_name} ${r.description}`.toLowerCase().includes(needle)));
  }, [data, q, owner]);

  const refresh = async () => {
    const fresh = await api.get<RepoListResponse>("/github/repositories?refresh=1");
    qc.setQueryData(["github-repos"], fresh);
  };

  if (isLoading)
    return (
      <div className="space-y-2 p-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    );
  if (error) return <div className="p-4"><ErrorBox error={error} title="Could not load repositories" /></div>;
  if (!data || (data.repositories.length === 0 && data.errors.length === 0))
    return (
      <div className="p-4">
        <Empty
          compact
          icon={<GitHubMark className="size-5" />}
          title="No repositories available"
          description="Connect a GitHub App and install it on the accounts or organizations whose repositories you want to deploy."
          action={
            <Link to="/settings?tab=git">
              <Button variant="primary" icon={<GitHubMark />}>
                Connect GitHub
              </Button>
            </Link>
          }
        />
      </div>
    );

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2 border-b border-border p-3">
        {owners.length > 1 && (
          <Select value={owner} onChange={(e) => setOwner(e.target.value)} className="w-48">
            <option value="">All accounts</option>
            {owners.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </Select>
        )}
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
          <Input autoFocus className="pl-8" placeholder={`Search ${data.repositories.length} repositories…`} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Button size="icon" title="Refresh from GitHub" onClick={refresh} loading={isFetching}>
          <RefreshCw />
        </Button>
      </div>
      {data.errors.length > 0 && (
        <div className="border-b border-border bg-warning-soft px-4 py-2 text-xs text-warning">
          Could not list repositories for {data.errors.map((e) => e.account).join(", ")}: {data.errors[0].error}
        </div>
      )}
      <div className="max-h-[380px] overflow-y-auto">
        {list.length === 0 ? (
          <div className="px-4 py-10 text-center text-[13px] text-subtle">{q || owner ? "No repositories match your filters." : "No repositories are accessible yet. Grant the GitHub App access to repositories, then refresh."}</div>
        ) : (
          list.map((r) => {
            const active = selected?.id === r.id && selected.installation_id === r.installation_id;
            return (
              <button
                key={`${r.installation_id}-${r.id}`}
                onClick={() => onSelect(r)}
                className={cn("flex w-full items-center gap-3 border-b border-border px-4 py-2.5 text-left transition-colors last:border-0", active ? "bg-surface-2" : "hover:bg-surface-2/50")}
              >
                <img src={r.owner_avatar} alt="" className="size-6 shrink-0 rounded-full border border-border" loading="lazy" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5 text-[13px]">
                    <span className="text-subtle">{r.owner} /</span>
                    <span className="truncate font-medium">{r.name}</span>
                    {r.private && <Lock className="size-3 shrink-0 text-faint" />}
                    {r.archived && <Badge>archived</Badge>}
                  </div>
                  {r.description && <div className="truncate text-xs text-subtle">{r.description}</div>}
                </div>
                <div className="hidden shrink-0 items-center gap-3 text-xs text-subtle sm:flex">
                  {r.language && <span>{r.language}</span>}
                  <span>{timeAgo(r.pushed_at)}</span>
                </div>
                {active ? <Check className="size-4 shrink-0 text-fg" /> : <span className="w-4" />}
              </button>
            );
          })
        )}
      </div>
    </div>
  );
}

export function useBranches(appId?: number, installationId?: number, repo?: string) {
  return useQuery({
    queryKey: ["github-branches", appId, installationId, repo],
    queryFn: () => api.get<GitHubBranch[]>(`/github/branches?app=${appId}&installation=${installationId}&repo=${encodeURIComponent(repo!)}`),
    enabled: !!appId && !!installationId && !!repo,
    staleTime: 30_000,
  });
}

export function BranchSelect({ appId, installationId, repo, value, onChange, defaultBranch }: { appId?: number; installationId?: number; repo?: string; value: string; onChange: (b: string) => void; defaultBranch?: string }) {
  const { data, isLoading, error } = useBranches(appId, installationId, repo);
  useEffect(() => {
    if (data && data.length && !data.some((b) => b.name === value)) onChange(defaultBranch && data.some((b) => b.name === defaultBranch) ? defaultBranch : data[0].name);
  }, [data, value, defaultBranch, onChange]);
  if (error) return <ErrorBox error={error} title="Could not load branches" />;
  return (
    <Select value={value} onChange={(e) => onChange(e.target.value)} disabled={isLoading}>
      {isLoading && <option>Loading branches…</option>}
      {data?.map((b) => (
        <option key={b.name} value={b.name}>
          {b.name}
          {b.name === defaultBranch ? "  (default)" : ""}
          {b.protected ? "  · protected" : ""}
        </option>
      ))}
    </Select>
  );
}

// ---------- Settings: Git sources ----------

function submitManifest(action: string, manifest: string) {
  const form = document.createElement("form");
  form.method = "post";
  form.action = action;
  const input = document.createElement("input");
  input.type = "hidden";
  input.name = "manifest";
  input.value = manifest;
  form.appendChild(input);
  document.body.appendChild(form);
  form.submit();
}

export function GitSourcesSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [params, setParams] = useSearchParams();
  const apps = useGitHubApps();
  const [connect, setConnect] = useState<{ name: string; organization: string; enterprise_url: string; advanced: boolean } | null>(null);
  const [manual, setManual] = useState<{ app_id: string; private_key: string; webhook_secret: string; enterprise_url: string } | null>(null);
  const [hook, setHook] = useState<{ app: GitHubApp; secret: string } | null>(null);

  useEffect(() => {
    const err = params.get("error");
    if (err) toast.error(err);
    if (params.get("installed")) {
      toast.success("GitHub App installed — repositories are now available");
      qc.invalidateQueries({ queryKey: ["github-repos"] });
    }
    if (err || params.get("installed")) setParams({ tab: "git" }, { replace: true });
  }, [params, setParams, qc]);

  const start = useMutation({
    mutationFn: () => api.post<{ action_url: string; manifest: string }>("/github/apps/manifest", connect),
    onSuccess: (r) => submitManifest(r.action_url, r.manifest),
    onError: (e) => toast.error(e.message),
  });
  const register = useMutation({
    mutationFn: () => api.post<GitHubApp>("/github/apps", { ...manual, app_id: Number(manual?.app_id) }),
    onSuccess: (a) => {
      toast.success(`${a.name} connected`);
      setManual(null);
      qc.invalidateQueries({ queryKey: ["github-apps"] });
      qc.invalidateQueries({ queryKey: ["github-repos"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const sync = useMutation({
    mutationFn: (id: number) => api.post<GitHubApp>(`/github/apps/${id}/sync`),
    onSuccess: () => {
      toast.success("Installations synchronized");
      qc.invalidateQueries({ queryKey: ["github-apps"] });
      qc.invalidateQueries({ queryKey: ["github-repos"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const rotate = useMutation({
    mutationFn: (a: GitHubApp) => api.post<{ secret: string; app: GitHubApp }>(`/github/apps/${a.id}/webhook-secret`),
    onSuccess: (r) => {
      setHook({ app: r.app, secret: r.secret });
      qc.invalidateQueries({ queryKey: ["github-apps"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/github/apps/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["github-apps"] });
      qc.invalidateQueries({ queryKey: ["github-repos"] });
    },
    onError: (e) => toast.error(e.message),
  });

  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<{ public_url: string }>("/settings") });
  const local = !!settings.data && /^https?:\/\/(localhost|127\.)/.test(settings.data.public_url);

  return (
    <div className="space-y-6">
      <Card
        title="GitHub"
        description="Connect a GitHub App to browse repositories, deploy on every push, build pull request previews and report deployment status on commits."
        actions={
          <>
            <Button size="sm" icon={<KeyRound />} onClick={() => setManual({ app_id: "", private_key: "", webhook_secret: "", enterprise_url: "" })}>
              Use existing app
            </Button>
            <Button size="sm" variant="primary" icon={<GitHubMark />} onClick={() => setConnect({ name: "", organization: "", enterprise_url: "", advanced: false })}>
              Connect GitHub
            </Button>
          </>
        }
      >
        {local && (
          <div className="border-b border-border bg-warning-soft px-4 py-2.5 text-xs leading-relaxed text-warning">
            The public URL of this instance is a localhost address, so GitHub cannot deliver push webhooks to it. Repository browsing and manual deploys work; set <code>PORTDOCK_PUBLIC_URL</code> to a public HTTPS address to enable automatic deployments.
          </div>
        )}
        {apps.isLoading ? (
          <div className="p-4">
            <Skeleton className="h-20 w-full" />
          </div>
        ) : !apps.data?.length ? (
          <div className="p-4">
            <Empty
              compact
              icon={<GitHubMark className="size-5" />}
              title="No GitHub App connected"
              description="PortDock creates a private GitHub App for your organization with read-only access to code and permission to post commit statuses."
              action={
                <Button variant="primary" icon={<GitHubMark />} onClick={() => setConnect({ name: "", organization: "", enterprise_url: "", advanced: false })}>
                  Connect GitHub
                </Button>
              }
            />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {apps.data.map((a) => (
              <div key={a.id} className="p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="flex items-center gap-3">
                    <span className="flex size-9 items-center justify-center rounded-lg border border-border bg-surface-2">
                      <GitHubMark />
                    </span>
                    <div>
                      <div className="flex items-center gap-2 text-[13px] font-semibold">
                        {a.name}
                        {a.html_url !== "https://github.com" && <Badge tone="info">Enterprise Server</Badge>}
                      </div>
                      <div className="text-xs text-subtle">
                        App ID {a.app_id} · owned by {a.owner || "—"} · {a.applications} application{a.applications === 1 ? "" : "s"}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <a href={a.install_url} target="_blank" rel="noreferrer">
                      <Button size="sm" icon={<Plus />}>
                        Install on account
                      </Button>
                    </a>
                    <Button size="sm" icon={<RefreshCw />} loading={sync.isPending && sync.variables === a.id} onClick={() => sync.mutate(a.id)}>
                      Sync
                    </Button>
                    <a href={a.settings_url} target="_blank" rel="noreferrer">
                      <Button size="icon-sm" title="Open on GitHub">
                        <ExternalLink />
                      </Button>
                    </a>
                    <Button
                      size="icon-sm"
                      variant="danger"
                      title="Disconnect"
                      onClick={async () => {
                        if (await confirm({ title: `Disconnect ${a.name}?`, description: "PortDock forgets the app credentials. Delete the app on GitHub separately if no longer needed.", danger: true, confirmText: "Disconnect" }))
                          del.mutate(a.id);
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </div>
                <div className="mt-4 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                  {a.installations.length === 0 ? (
                    <div className="rounded-lg border border-dashed border-border-strong px-4 py-3 text-xs text-subtle sm:col-span-2 lg:col-span-3">
                      Not installed on any account yet.{" "}
                      <a href={a.install_url} target="_blank" rel="noreferrer" className="font-medium text-fg underline">
                        Install the app
                      </a>{" "}
                      and choose which repositories PortDock can access, then press Sync.
                    </div>
                  ) : (
                    a.installations.map((i) => (
                      <div key={i.id} className="flex items-center gap-3 rounded-lg border border-border bg-surface-2/40 px-3 py-2">
                        {i.account_avatar ? <img src={i.account_avatar} alt="" className="size-7 rounded-full border border-border" /> : <GitHubMark className="size-5" />}
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-[13px] font-medium">{i.account_login}</div>
                          <div className="text-xs text-subtle">
                            {i.account_type} · {i.repository_selection === "all" ? "all repositories" : "selected repositories"}
                          </div>
                        </div>
                        <a href={`${a.html_url}/${i.account_type === "Organization" ? `organizations/${i.account_login}/` : ""}settings/installations/${i.installation_id}`} target="_blank" rel="noreferrer" title="Configure repository access">
                          <Button size="icon-sm" variant="ghost">
                            <Settings2 />
                          </Button>
                        </a>
                      </div>
                    ))
                  )}
                </div>
                {a.webhook_configured ? (
                  <div className="mt-4 flex max-w-2xl items-end gap-2">
                    <div className="flex-1">
                      <CopyField label={<span className="flex items-center gap-1.5"><Webhook className="size-3.5" /> Webhook URL</span>} value={a.webhook_url} />
                    </div>
                    <Button size="sm" onClick={() => rotate.mutate(a)} loading={rotate.isPending}>
                      Rotate secret
                    </Button>
                  </div>
                ) : (
                  <div className="mt-4 flex flex-wrap items-center gap-3 rounded-lg border border-warning/30 bg-warning-soft px-4 py-3 text-[13px]">
                    <Webhook className="size-4 text-warning" />
                    <div className="min-w-0 flex-1">
                      <div className="font-medium text-fg">Webhooks are not configured</div>
                      <div className="text-xs text-muted">
                        {a.public_url
                          ? "Enable them to deploy automatically on every push and build pull request previews."
                          : "This app was created while PortDock had a private URL. Set PORTDOCK_PUBLIC_URL to a public HTTPS address, restart PortDock, then enable webhooks here."}
                      </div>
                    </div>
                    {a.public_url && (
                      <Button size="sm" variant="primary" loading={rotate.isPending} onClick={() => rotate.mutate(a)}>
                        Enable webhooks
                      </Button>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </Card>

      <Modal
        open={!!connect}
        onClose={() => setConnect(null)}
        title="Connect GitHub"
        description="You'll be redirected to GitHub to create a private GitHub App with the right permissions, then to install it on your account or organization."
        footer={
          <>
            <Button onClick={() => setConnect(null)}>Cancel</Button>
            <Button variant="primary" icon={<GitHubMark />} loading={start.isPending} onClick={() => start.mutate()}>
              Continue to GitHub
            </Button>
          </>
        }
      >
        {connect && (
          <div className="space-y-4">
            <Field label="App name" hint="Must be unique on GitHub. Leave empty to generate one.">
              <Input value={connect.name} onChange={(e) => setConnect({ ...connect, name: e.target.value })} placeholder="Acme PortDock" autoFocus />
            </Field>
            <Field label="Organization" hint="Create the app under an organization instead of your personal account.">
              <Input value={connect.organization} onChange={(e) => setConnect({ ...connect, organization: e.target.value })} placeholder="acme-inc" />
            </Field>
            {connect.advanced ? (
              <Field label="GitHub Enterprise Server URL" hint="Only for self-hosted GitHub. Leave empty for github.com.">
                <Input value={connect.enterprise_url} onChange={(e) => setConnect({ ...connect, enterprise_url: e.target.value })} placeholder="https://github.acme.com" />
              </Field>
            ) : (
              <button className="text-xs font-medium text-subtle hover:text-fg" onClick={() => setConnect({ ...connect, advanced: true })}>
                Using GitHub Enterprise Server?
              </button>
            )}
            <div className="rounded-lg border border-border bg-surface-2/50 p-3 text-xs leading-relaxed text-muted">
              <div className="mb-1 font-medium text-fg">Permissions requested</div>
              Contents: read · Metadata: read · Pull requests: read · Commit statuses: write
              <br />
              Events: push, pull_request
            </div>
          </div>
        )}
      </Modal>

      <Modal
        open={!!hook}
        onClose={() => setHook(null)}
        title="Configure the webhook on GitHub"
        description="Copy these values into your GitHub App settings. The secret is shown only once."
        size="lg"
        footer={
          <>
            {hook && (
              <a href={hook.app.settings_url} target="_blank" rel="noreferrer">
                <Button icon={<ExternalLink />}>Open app on GitHub</Button>
              </a>
            )}
            <Button variant="primary" onClick={() => setHook(null)}>
              Done
            </Button>
          </>
        }
      >
        {hook && (
          <div className="space-y-4">
            <ol className="list-decimal space-y-1.5 pl-5 text-[13px] text-muted">
              <li>
                Open the app on GitHub → <span className="font-medium text-fg">Edit</span> (or Settings → Developer settings → GitHub Apps).
              </li>
              <li>
                Under <span className="font-medium text-fg">Webhook</span>, tick <span className="font-medium text-fg">Active</span> and paste the URL and secret below.
              </li>
              <li>
                Under <span className="font-medium text-fg">Permissions &amp; events → Subscribe to events</span>, select <span className="font-medium text-fg">Push</span> and <span className="font-medium text-fg">Pull request</span>, then save.
              </li>
            </ol>
            <CopyField label="Webhook URL" value={hook.app.webhook_url} />
            <CopyField label="Webhook secret" value={hook.secret} secret />
          </div>
        )}
      </Modal>

      <Modal
        open={!!manual}
        onClose={() => setManual(null)}
        title="Use an existing GitHub App"
        description="For organizations that manage GitHub Apps centrally. Set the app's webhook URL to the one shown after connecting."
        size="lg"
        footer={
          <>
            <Button onClick={() => setManual(null)}>Cancel</Button>
            <Button variant="primary" loading={register.isPending} disabled={!manual?.app_id || !manual.private_key} onClick={() => register.mutate()}>
              Connect app
            </Button>
          </>
        }
      >
        {manual && (
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="App ID" required>
              <Input value={manual.app_id} onChange={(e) => setManual({ ...manual, app_id: e.target.value.replace(/\D/g, "") })} placeholder="123456" />
            </Field>
            <Field label="Webhook secret" hint="Required for automatic deployments.">
              <PasswordInput value={manual.webhook_secret} onChange={(e) => setManual({ ...manual, webhook_secret: e.target.value })} />
            </Field>
            <Field label="Private key (PEM)" required className="md:col-span-2">
              <Textarea rows={6} value={manual.private_key} onChange={(e) => setManual({ ...manual, private_key: e.target.value })} placeholder="-----BEGIN RSA PRIVATE KEY-----" />
            </Field>
            <Field label="GitHub Enterprise Server URL" hint="Leave empty for github.com." className="md:col-span-2">
              <Input value={manual.enterprise_url} onChange={(e) => setManual({ ...manual, enterprise_url: e.target.value })} placeholder="https://github.acme.com" />
            </Field>
          </div>
        )}
      </Modal>
    </div>
  );
}

export function RepoLink({ fullName, htmlBase = "https://github.com" }: { fullName: string; htmlBase?: string }) {
  return (
    <a href={`${htmlBase}/${fullName}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 hover:text-fg">
      <GitHubMark className="size-3.5" />
      {fullName}
      <ExternalLink className="size-3 text-faint" />
    </a>
  );
}

