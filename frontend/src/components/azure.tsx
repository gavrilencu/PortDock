import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ExternalLink, KeyRound, RefreshCw, Search, Trash2, Webhook } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, AzureConnection, AzureHook, AzureRepo } from "../lib/types";
import { cn, timeAgo } from "../lib/utils";
import { t, tMaybe, tx } from "../lib/i18n";
import { Badge, Button, Card, CopyField, Empty, ErrorBox, Field, Input, Modal, PasswordInput, Select, Skeleton, useConfirm } from "./ui";

/** The Azure DevOps logo (simple-icons, CC0). */
export function AzureMark({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden>
      <path d="M0 8.877L2.247 5.91l8.405-3.416V.022l7.37 5.393L2.966 8.338v8.225L0 15.707zm24-4.45v14.651l-5.753 4.9-9.303-3.057v3.056l-5.978-7.416 15.057 1.798V5.415z" />
    </svg>
  );
}

export function useAzureConnections(enabled = true) {
  return useQuery({ queryKey: ["azure-connections"], queryFn: () => api.get<AzureConnection[]>("/azure/connections"), enabled, retry: false });
}

/** A repository picked for a new application, with the connection it comes from. */
export type AzureSelection = AzureRepo & { connection_id: number };

/** Searchable list of the Git repositories of a connected Azure DevOps organization. */
export function AzureRepoPicker({ connections, selected, onSelect }: { connections: AzureConnection[]; selected?: AzureSelection | null; onSelect: (r: AzureSelection) => void }) {
  const [conn, setConn] = useState(selected?.connection_id ?? connections[0]?.id ?? 0);
  const [q, setQ] = useState("");
  const [project, setProject] = useState("");
  const repos = useQuery({
    queryKey: ["azure-repos", conn],
    queryFn: () => api.get<AzureRepo[]>(`/azure/repositories?connection=${conn}`),
    enabled: !!conn,
    staleTime: 60_000,
    retry: false,
  });
  const projects = useMemo(() => [...new Set((repos.data ?? []).map((r) => r.project.name))], [repos.data]);
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (repos.data ?? []).filter((r) => (!project || r.project.name === project) && (!needle || `${r.project.name} ${r.name}`.toLowerCase().includes(needle)));
  }, [repos.data, q, project]);

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2 border-b border-border p-3">
        {connections.length > 1 && (
          <Select
            value={conn}
            onChange={(e) => {
              setConn(Number(e.target.value));
              setProject("");
            }}
            className="w-48"
          >
            {connections.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
        )}
        {projects.length > 1 && (
          <Select value={project} onChange={(e) => setProject(e.target.value)} className="w-48">
            <option value="">{t("All projects")}</option>
            {projects.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        )}
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
          <Input autoFocus className="pl-8" placeholder={t("Search {n} repository…|Search {n} repositories…", { n: repos.data?.length ?? 0 })} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Button size="icon" title={t("Refresh from Azure DevOps")} onClick={() => repos.refetch()} loading={repos.isFetching}>
          <RefreshCw />
        </Button>
      </div>
      {repos.isLoading ? (
        <div className="space-y-2 p-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-11 w-full" />
          ))}
        </div>
      ) : repos.error ? (
        <div className="p-4">
          <ErrorBox error={repos.error} title={t("Could not load repositories")} />
        </div>
      ) : (
        <div className="max-h-[380px] overflow-y-auto">
          {list.length === 0 ? (
            <div className="px-4 py-10 text-center text-[13px] text-subtle">{q || project ? t("No repositories match your filters.") : t("This organization has no Git repositories the token can read.")}</div>
          ) : (
            list.map((r) => {
              const active = selected?.id === r.id;
              return (
                <button
                  key={r.id}
                  onClick={() => onSelect({ ...r, connection_id: conn })}
                  className={cn("flex w-full items-center gap-3 border-b border-border px-4 py-2.5 text-left transition-colors last:border-0", active ? "bg-surface-2" : "hover:bg-surface-2/50")}
                >
                  <span className="flex size-6 shrink-0 items-center justify-center rounded-md border border-border bg-surface-2 text-[#0078d4]">
                    <AzureMark className="size-3.5" />
                  </span>
                  <div className="min-w-0 flex-1 text-[13px]">
                    <span className="text-subtle">{r.project.name} /</span> <span className="font-medium">{r.name}</span>
                  </div>
                  {r.default_branch && <span className="hidden shrink-0 font-mono text-xs text-subtle sm:block">{r.default_branch}</span>}
                  {active ? <Check className="size-4 shrink-0 text-fg" /> : <span className="w-4" />}
                </button>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}

export function AzureBranchSelect({ connection, project, repo, value, onChange, defaultBranch }: { connection: number; project: string; repo: string; value: string; onChange: (b: string) => void; defaultBranch?: string }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["azure-branches", connection, project, repo],
    queryFn: () => api.get<string[]>(`/azure/branches?connection=${connection}&project=${encodeURIComponent(project)}&repo=${encodeURIComponent(repo)}`),
    enabled: !!connection && !!project && !!repo,
    staleTime: 30_000,
    retry: false,
  });
  useEffect(() => {
    if (data && data.length && !data.includes(value)) onChange(defaultBranch && data.includes(defaultBranch) ? defaultBranch : data[0]);
  }, [data, value, defaultBranch, onChange]);
  if (error) return <ErrorBox error={error} title={t("Could not load branches")} />;
  return (
    <Select value={value} onChange={(e) => onChange(e.target.value)} disabled={isLoading}>
      {isLoading && <option>{t("Loading branches…")}</option>}
      {data?.map((b) => (
        <option key={b} value={b}>
          {b}
          {b === defaultBranch ? `  (${t("default")})` : ""}
        </option>
      ))}
    </Select>
  );
}

/** Link to the repository of an application deployed from Azure DevOps. */
export function AzureRepoLink({ url }: { url: string }) {
  const m = url.match(/\/([^/]+)\/_git\/([^/?#]+)/);
  const label = m ? `${decodeURIComponent(m[1])} / ${decodeURIComponent(m[2])}` : url;
  return (
    <a href={url} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1.5 hover:text-fg">
      <AzureMark className="size-3.5 shrink-0 text-[#0078d4]" />
      <span className="truncate">{label}</span>
      <ExternalLink className="size-3 shrink-0 text-faint" />
    </a>
  );
}

// ---------- Settings: Git sources → Azure DevOps ----------

const scopes = "Code: Read · Service Hooks: Read, query & manage";

export function AzureDevOpsSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const conns = useAzureConnections();
  const [add, setAdd] = useState<{ url: string; token: string; name: string } | null>(null);
  const [rotate, setRotate] = useState<{ c: AzureConnection; token: string } | null>(null);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["azure-connections"] });
    qc.invalidateQueries({ queryKey: ["azure-repos"] });
  };
  const create = useMutation({
    mutationFn: () => api.post<{ connection: AzureConnection; repositories: number }>("/azure/connections", add),
    onSuccess: (r) => {
      toast.success(t("{name} connected", { name: r.connection.name }), { description: t("{n} repository available|{n} repositories available", { n: r.repositories }) });
      setAdd(null);
      refresh();
    },
    onError: (e) => toast.error(e.message),
  });
  const update = useMutation({
    mutationFn: () => api.patch<AzureConnection>(`/azure/connections/${rotate!.c.id}`, { token: rotate!.token }),
    onSuccess: () => {
      toast.success(t("Token replaced"));
      setRotate(null);
      refresh();
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/azure/connections/${id}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<{ public_url: string }>("/settings") });
  const local = !!settings.data && /^https?:\/\/(localhost|127\.)/.test(settings.data.public_url);
  const open = () => setAdd({ url: "", token: "", name: "" });

  return (
    <>
      <Card
        title="Azure DevOps"
        description={t("Connect an Azure DevOps organization with a personal access token to deploy its Git repositories and deploy on every push.")}
        actions={
          <Button size="sm" variant="primary" icon={<AzureMark />} onClick={open}>
            {t("Connect Azure DevOps")}
          </Button>
        }
      >
        {local && (
          <div className="border-b border-border bg-warning-soft px-4 py-2.5 text-xs leading-relaxed text-warning">
            {t("The public URL of this instance is a localhost address, so Azure DevOps cannot deliver push events to it. Repository browsing and manual deploys work; set a public HTTPS address to deploy on push.")}
          </div>
        )}
        {conns.isLoading ? (
          <div className="p-4">
            <Skeleton className="h-14 w-full" />
          </div>
        ) : !conns.data?.length ? (
          <div className="p-4">
            <Empty
              compact
              icon={<AzureMark className="size-5" />}
              title={t("No Azure DevOps organization connected")}
              description={t("PortDock clones the repositories with the token and adds a service hook to each application's repository, so every push deploys.")}
              action={
                <Button variant="primary" icon={<AzureMark />} onClick={open}>
                  {t("Connect Azure DevOps")}
                </Button>
              }
            />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {conns.data.map((c) => (
              <div key={c.id} className="flex flex-wrap items-center justify-between gap-3 p-4">
                <div className="flex min-w-0 items-center gap-3">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2 text-[#0078d4]">
                    <AzureMark />
                  </span>
                  <div className="min-w-0">
                    <div className="text-[13px] font-semibold">{c.name}</div>
                    <div className="truncate text-xs text-subtle">
                      {c.url} · {t("{n} application|{n} applications", { n: c.applications })} · {t("connected {time}", { time: timeAgo(c.created_at) })}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button size="sm" icon={<KeyRound />} onClick={() => setRotate({ c, token: "" })}>
                    {t("Replace token")}
                  </Button>
                  <a href={c.url} target="_blank" rel="noreferrer">
                    <Button size="icon-sm" title={t("Open in Azure DevOps")}>
                      <ExternalLink />
                    </Button>
                  </a>
                  <Button
                    size="icon-sm"
                    variant="danger"
                    title={t("Disconnect")}
                    onClick={async () => {
                      if (await confirm({ title: t("Disconnect {name}?", { name: c.name }), description: t("PortDock forgets the token. Revoke it in Azure DevOps if no longer needed."), danger: true, confirmText: t("Disconnect") }))
                        del.mutate(c.id);
                    }}
                  >
                    <Trash2 />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Modal
        open={!!add}
        onClose={() => setAdd(null)}
        title={t("Connect Azure DevOps")}
        description={t("PortDock checks the token by listing the organization's repositories.")}
        size="lg"
        footer={
          <>
            <Button onClick={() => setAdd(null)}>{t("Cancel")}</Button>
            <Button variant="primary" icon={<AzureMark />} loading={create.isPending} disabled={!add?.url.trim() || !add.token.trim()} onClick={() => create.mutate()}>
              {t("Connect")}
            </Button>
          </>
        }
      >
        {add && (
          <div className="grid gap-4 md:grid-cols-2">
            <Field label={t("Organization")} required hint={t("The name or URL, e.g. contoso or https://dev.azure.com/contoso. For Azure DevOps Server, the collection URL.")} className="md:col-span-2">
              <Input autoFocus value={add.url} onChange={(e) => setAdd({ ...add, url: e.target.value })} placeholder="https://dev.azure.com/contoso" />
            </Field>
            <Field label={t("Personal access token")} required>
              <PasswordInput value={add.token} onChange={(e) => setAdd({ ...add, token: e.target.value })} />
            </Field>
            <Field label={t("Name")} hint={t("Defaults to the organization name.")}>
              <Input value={add.name} onChange={(e) => setAdd({ ...add, name: e.target.value })} placeholder="contoso" />
            </Field>
            <TokenHelp url={add.url} />
          </div>
        )}
      </Modal>

      <Modal
        open={!!rotate}
        onClose={() => setRotate(null)}
        title={t("Replace the token of {name}", { name: rotate?.c.name ?? "" })}
        description={t("Personal access tokens expire. Paste a new one; applications keep deploying with it.")}
        footer={
          <>
            <Button onClick={() => setRotate(null)}>{t("Cancel")}</Button>
            <Button variant="primary" loading={update.isPending} disabled={!rotate?.token.trim()} onClick={() => update.mutate()}>
              {t("Save")}
            </Button>
          </>
        }
      >
        {rotate && (
          <div className="space-y-4">
            <Field label={t("Personal access token")} required>
              <PasswordInput autoFocus value={rotate.token} onChange={(e) => setRotate({ ...rotate, token: e.target.value })} />
            </Field>
            <TokenHelp url={rotate.c.url} />
          </div>
        )}
      </Modal>
    </>
  );
}

function TokenHelp({ url }: { url: string }) {
  const org = url.trim().replace(/\/+$/, "");
  const base = !org ? "" : org.includes("://") ? org : `https://dev.azure.com/${org}`;
  return (
    <div className="rounded-lg border border-border bg-surface-2/50 p-3 text-xs leading-relaxed text-muted md:col-span-2">
      <div className="mb-1 font-medium text-fg">{t("Create the token")}</div>
      {tx("In Azure DevOps open {path}, choose the organization and at least these scopes:", {
        path: base ? (
          <a href={`${base}/_usersSettings/tokens`} target="_blank" rel="noreferrer" className="font-medium text-fg underline">
            {t("User settings → Personal access tokens")}
          </a>
        ) : (
          <span className="font-medium text-fg">{t("User settings → Personal access tokens")}</span>
        ),
      })}
      <div className="mt-1 font-mono text-[11px] text-fg">{scopes}</div>
      <div className="mt-1.5">{t("To add service hooks, the token's user must be allowed to edit subscriptions in the projects (project administrators are).")}</div>
    </div>
  );
}

// ---------- Application → CI/CD ----------

/** The service hook that deploys an Azure DevOps application on push. */
export function AzureHookCard({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const hook = useQuery({ queryKey: ["azure-hook", app.id], queryFn: () => api.get<AzureHook>(`/applications/${app.id}/azure-hook`) });
  const recreate = useMutation({
    mutationFn: () => api.post<AzureHook>(`/applications/${app.id}/azure-hook`),
    onSuccess: (h) => {
      qc.setQueryData(["azure-hook", app.id], h);
      qc.invalidateQueries({ queryKey: ["application", app.id] });
      if (h.error) toast.error(tMaybe(h.error));
      else toast.success(t("Service hook created"));
    },
    onError: (e) => toast.error(e.message),
  });
  const h = hook.data;
  const repoURL = app.git_repository;
  const project = repoURL.match(/^(.*)\/_git\//)?.[1] ?? "";
  return (
    <Card
      title="Azure DevOps"
      description={t("A service hook in the repository's project sends every push to PortDock.")}
      actions={
        <Button size="sm" icon={<RefreshCw />} loading={recreate.isPending} onClick={() => recreate.mutate()}>
          {h?.hook_id ? t("Recreate service hook") : t("Create service hook")}
        </Button>
      }
    >
      <div className="space-y-4 p-5">
        <div className="flex flex-wrap items-center gap-3 text-[13px]">
          <AzureRepoLink url={repoURL} />
          {hook.isLoading ? null : h?.hook_id ? (
            <Badge tone="success">
              <Webhook className="size-3" /> {t("Service hook active")}
            </Badge>
          ) : (
            <Badge tone="warning">{t("No service hook")}</Badge>
          )}
        </div>
        {h?.error && (
          <div className="rounded-lg border border-danger/25 bg-danger-soft px-4 py-3 text-xs text-danger">
            <div className="font-medium">{t("The service hook could not be created")}</div>
            <div className="mt-0.5 break-words">{tMaybe(h.error)}</div>
          </div>
        )}
        {h && !h.hook_id && (
          <div className="space-y-3">
            <p className="text-xs leading-relaxed text-muted">
              {tx("You can also add it by hand: in {path}, add a {type} subscription for {event} on this repository and branch, with this URL and HTTP header.", {
                path: project ? (
                  <a href={`${project}/_settings/serviceHooks`} target="_blank" rel="noreferrer" className="font-medium text-fg underline">
                    {t("Project settings → Service hooks")}
                  </a>
                ) : (
                  <span className="font-medium text-fg">{t("Project settings → Service hooks")}</span>
                ),
                type: <span className="font-medium text-fg">Web Hooks</span>,
                event: <span className="font-medium text-fg">Code pushed</span>,
              })}
            </p>
            <CopyField label={t("URL")} value={h.url} />
            <CopyField label={t("HTTP headers")} value={`${h.header}:${app.webhook_secret}`} secret />
          </div>
        )}
        <p className="text-xs text-subtle">
          {tx("Pushes to {branch} deploy when {auto} is on. Pull request previews are not available for Azure DevOps yet.", {
            branch: <code className="font-mono text-fg">{app.git_branch}</code>,
            auto: <span className="font-medium text-fg">{t("Deploy on push")}</span>,
          })}
        </p>
      </div>
    </Card>
  );
}

/** Hint shown in the new application form when no Azure DevOps organization is connected. */
export function ConnectAzureLink() {
  return (
    <Link to="/settings?tab=git" className="font-medium text-fg underline">
      {t("connect Azure DevOps")}
    </Link>
  );
}
