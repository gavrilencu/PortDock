import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Box,
  ExternalLink,
  GitBranch,
  GitPullRequest,
  Globe,
  Hammer,
  KeyRound,
  LayoutGrid,
  Play,
  RefreshCw,
  Rocket,
  RotateCw,
  Save,
  ScrollText,
  Settings2,
  Square,
  SquareTerminal,
  Trash2,
  Webhook,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, Container, Deployment, Preview } from "../lib/types";
import {
  Badge,
  Button,
  Card,
  CopyField,
  Empty,
  ErrorBox,
  Field,
  Input,
  Loading,
  PageHeader,
  PasswordInput,
  Select,
  StatusBadge,
  Switch,
  Tabs,
  Textarea,
  useConfirm,
} from "../components/ui";
import { DeploymentsTable } from "../components/shared";
import { BranchSelect, RepoLink } from "../components/github";
import { BuildSettings } from "../components/BuildSettings";
import { AccessCallout, DNSBadge, DomainLinks, DomainsTab, domainURL as rowURL, publishedURLs, useDomains } from "../components/Domains";
import { EnvEditor } from "../components/EnvEditor";
import { ContainerLogs, ContainerTable } from "../components/ContainerTable";
import { Terminal } from "../components/Terminal";
import { domainURL, shortSha, timeAgo } from "../lib/utils";

type Tab = "overview" | "config" | "domains" | "env" | "deployments" | "logs" | "terminal" | "previews" | "webhooks";

export function useApp(id: number) {
  return useQuery({ queryKey: ["application", id], queryFn: () => api.get<ApplicationDetail>(`/applications/${id}`) });
}

export function ApplicationPage() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "overview";
  const setTab = (t: Tab) => setParams({ tab: t });
  const qc = useQueryClient();
  const nav = useNavigate();
  const { data: app, isLoading, error } = useApp(id);

  const deploy = useMutation({
    mutationFn: (force: boolean) => api.post<Deployment>(`/applications/${id}/deploy`, { force_rebuild: force }),
    onSuccess: (d) => {
      qc.invalidateQueries({ queryKey: ["app-deployments", id] });
      nav(`/applications/${id}/deployments/${d.id}`);
    },
    onError: (e) => toast.error(e.message),
  });
  const action = useMutation({
    mutationFn: (a: "start" | "stop" | "restart") => api.post(`/applications/${id}/${a}`),
    onSuccess: (_, a) => {
      toast.success(`Application ${a}ed`);
      qc.invalidateQueries({ queryKey: ["application", id] });
      qc.invalidateQueries({ queryKey: ["containers"] });
    },
    onError: (e) => toast.error(e.message),
  });

  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  const a = app!;
  const running = a.status === "running" || a.status === "degraded";

  return (
    <>
      <PageHeader
        icon={<Box className="size-5" />}
        title={
          <span className="flex items-center gap-3">
            {a.name} <StatusBadge status={a.status} />
          </span>
        }
        subtitle={
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
            {a.source_type === "git" ? (
              <span className="flex items-center gap-1.5">
                <GitBranch className="size-3.5" />
                {a.git_repository.replace(/^https?:\/\//, "")} · {a.git_branch}
              </span>
            ) : (
              <span className="font-mono">{a.docker_image}</span>
            )}
            <span>on {a.server_name}</span>
          </span>
        }
        breadcrumbs={
          <>
            <Link to="/projects" className="hover:text-fg">Projects</Link> /{" "}
            <Link to={`/projects/${a.project.id}?env=${a.environment_id}`} className="hover:text-fg">
              {a.project.name}
            </Link>{" "}
            / <span>{a.environment.name}</span> / <span className="text-fg">{a.name}</span>
          </>
        }
        actions={
          <>
            <DomainLinks appId={a.id} extra={publishedURLs(a, a.server_host)} />
            {running ? (
              <>
                <Button icon={<RotateCw className="size-4" />} onClick={() => action.mutate("restart")} loading={action.isPending && action.variables === "restart"}>
                  Restart
                </Button>
                <Button icon={<Square className="size-4" />} onClick={() => action.mutate("stop")} loading={action.isPending && action.variables === "stop"}>
                  Stop
                </Button>
              </>
            ) : (
              a.current_image && (
                <Button icon={<Play className="size-4" />} onClick={() => action.mutate("start")}>
                  Start
                </Button>
              )
            )}
            <Button icon={<Hammer className="size-4" />} onClick={() => deploy.mutate(true)} title="Rebuild without cache reuse">
              Force rebuild
            </Button>
            <Button variant="primary" icon={<Rocket className="size-4" />} loading={deploy.isPending} onClick={() => deploy.mutate(false)}>
              {a.current_image ? "Redeploy" : "Deploy"}
            </Button>
          </>
        }
      />
      <Tabs
        value={tab}
        onChange={setTab}
        tabs={[
          { id: "overview", label: "Overview", icon: <LayoutGrid /> },
          { id: "config", label: "Configuration", icon: <Settings2 /> },
          { id: "domains", label: "Domains", icon: <Globe /> },
          { id: "env", label: "Environment", icon: <KeyRound /> },
          { id: "deployments", label: "Deployments", icon: <Rocket /> },
          { id: "logs", label: "Logs", icon: <ScrollText /> },
          { id: "terminal", label: "Terminal", icon: <SquareTerminal /> },
          { id: "previews", label: "Previews", icon: <GitPullRequest />, hidden: a.source_type !== "git" },
          { id: "webhooks", label: "CI/CD & Webhooks", icon: <Webhook /> },
        ]}
      />
      {tab === "overview" && (
        <div className="space-y-6">
          <AccessCallout app={a} />
          <Overview app={a} />
        </div>
      )}
      {tab === "config" && <Configuration app={a} />}
      {tab === "domains" && <DomainsTab app={a} />}
      {tab === "env" && <EnvEditor owner={`/applications/${id}`} supportsBuild supportsPreview={a.source_type === "git"} />}
      {tab === "deployments" && <DeploymentsList appId={id} />}
      {tab === "logs" && <AppLogs app={a} />}
      {tab === "terminal" && <AppTerminal app={a} />}
      {tab === "previews" && <Previews app={a} />}
      {tab === "webhooks" && <Webhooks app={a} />}
    </>
  );
}

function useAppContainers(app: ApplicationDetail) {
  return useQuery({ queryKey: ["containers", "app", app.id], queryFn: () => api.get<Container[]>(`/applications/${app.id}/containers`), refetchInterval: 15_000 });
}

function Overview({ app }: { app: ApplicationDetail }) {
  const deps = useQuery({ queryKey: ["app-deployments", app.id, 5], queryFn: () => api.get<Deployment[]>(`/applications/${app.id}/deployments?limit=5`) });
  const containers = useAppContainers(app);
  const domainRows = useDomains(app.id);
  return (
    <div className="grid gap-6 lg:grid-cols-3">
      <div className="space-y-6 lg:col-span-2">
        <Card title="Recent deployments" actions={<Link to="?tab=deployments" className="text-xs text-muted hover:text-fg">All →</Link>}>
          {deps.data?.length ? (
            <DeploymentsTable deployments={deps.data} />
          ) : (
            <div className="p-5">
              <Empty icon={<Rocket />} title="Not deployed yet" description="Click Deploy to build and start this application." />
            </div>
          )}
        </Card>
        <Card title="Containers">
          {containers.isLoading ? <Loading /> : containers.error ? <div className="p-5"><ErrorBox error={containers.error} /></div> : <ContainerTable serverId={app.server_id} containers={containers.data ?? []} compact />}
        </Card>
      </div>
      <div className="space-y-6">
        <Card title="Domains" actions={<Link to="?tab=domains" className="text-xs text-muted hover:text-fg">Manage →</Link>}>
          <div className="space-y-2 p-4">
            {!domainRows.data?.domains.length ? (
              <p className="text-[13px] text-subtle">
                No domain yet.{" "}
                <Link to="?tab=domains" className="font-medium text-fg underline">
                  Add a domain
                </Link>{" "}
                to expose the application over HTTPS.
              </p>
            ) : (
              domainRows.data.domains.map((d) => (
                <a key={d.id} href={rowURL(d)} target="_blank" rel="noreferrer" className="flex h-9 items-center gap-2 rounded-md border border-border bg-surface-2/50 px-3 text-[13px] transition-colors hover:border-border-strong hover:bg-surface-2">
                  <Globe className="size-4 text-subtle" />
                  <span className="flex-1 truncate">{d.host + d.path}</span>
                  <DNSBadge d={d} />
                  <ExternalLink className="size-3.5 text-muted" />
                </a>
              ))
            )}
          </div>
        </Card>
        <Card title="Details">
          <dl className="space-y-3 p-5 text-sm">
            {[
              ["Build pack", <Badge>{app.build_pack}</Badge>],
              ["Port", app.port],
              ["Replicas", app.replicas],
              ["Current image", <code className="break-all text-xs">{app.current_image || "—"}</code>],
              ["Auto deploy", app.auto_deploy ? "On push" : "Off"],
              ["Health check", app.health_check_path || "container running"],
              ["Internal hostname", <code className="text-xs">{app.uuid}</code>],
              ["Updated", timeAgo(app.updated_at)],
            ].map(([k, v], i) => (
              <div key={i} className="flex items-start justify-between gap-4">
                <dt className="text-muted">{k}</dt>
                <dd className="text-right">{v}</dd>
              </div>
            ))}
          </dl>
        </Card>
      </div>
    </div>
  );
}

function Section({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-6 border-b border-border px-5 py-6 last:border-0 lg:grid-cols-3">
      <div>
        <h4 className="text-sm font-semibold">{title}</h4>
        {description && <p className="mt-1 text-xs text-muted">{description}</p>}
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:col-span-2">{children}</div>
    </div>
  );
}

function Configuration({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const [f, setF] = useState(app);
  const [token, setToken] = useState<string | null>(null);
  useEffect(() => setF(app), [app]);
  const set = <K extends keyof ApplicationDetail>(k: K, v: ApplicationDetail[K]) => setF((x) => ({ ...x, [k]: v }));
  const dirty = JSON.stringify(f) !== JSON.stringify(app) || token !== null;

  const save = useMutation({
    mutationFn: () => api.patch<ApplicationDetail>(`/applications/${app.id}`, { ...f, ...(token !== null ? { git_token: token } : {}) }),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      setToken(null);
      toast.success("Configuration saved — redeploy to apply");
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/applications/${app.id}`),
    onSuccess: () => {
      toast.success("Application deleted");
      nav(`/projects/${app.project.id}?env=${app.environment_id}`);
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <div className="space-y-6">
      <Card>
        <Section title="General">
          <Field label="Name">
            <Input value={f.name} onChange={(e) => set("name", e.target.value)} />
          </Field>
          <Field label="Description">
            <Input value={f.description} onChange={(e) => set("description", e.target.value)} />
          </Field>
        </Section>
        {f.source_type === "git" && f.github_app_id ? (
          <Section title="Source" description="Connected through the GitHub App. New commits on the branch deploy automatically.">
            <div className="flex items-center gap-3 rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 text-[13px] sm:col-span-2">
              <RepoLink fullName={f.git_full_name} htmlBase={f.git_repository.replace(new RegExp(`/${f.git_full_name}$`), "") || "https://github.com"} />
            </div>
            <Field label="Branch">
              <BranchSelect appId={f.github_app_id} installationId={f.github_installation_id} repo={f.git_full_name} value={f.git_branch} onChange={(b) => set("git_branch", b)} />
            </Field>
            <Field label="Root directory" hint="For monorepos, e.g. /apps/web">
              <Input value={f.base_directory} onChange={(e) => set("base_directory", e.target.value)} />
            </Field>
            <Field label="Pinned commit" hint="Optional: always deploy this SHA.">
              <Input value={f.git_commit} onChange={(e) => set("git_commit", e.target.value)} placeholder="HEAD" />
            </Field>
          </Section>
        ) : f.source_type === "git" ? (
          <Section title="Source" description="Git repository and revision to build.">
            <Field label="Repository" className="sm:col-span-2">
              <Input value={f.git_repository} onChange={(e) => set("git_repository", e.target.value)} />
            </Field>
            <Field label="Branch">
              <Input value={f.git_branch} onChange={(e) => set("git_branch", e.target.value)} />
            </Field>
            <Field label="Root directory" hint="For monorepos, e.g. /apps/web">
              <Input value={f.base_directory} onChange={(e) => set("base_directory", e.target.value)} />
            </Field>
            <Field label="Pinned commit" hint="Optional: always deploy this SHA.">
              <Input value={f.git_commit} onChange={(e) => set("git_commit", e.target.value)} placeholder="HEAD" />
            </Field>
            <Field label="Access token" hint={app.has_git_token ? "A token is stored. Type a new one to replace it." : "Required for private repositories."}>
              <div className="flex gap-2">
                <div className="flex-1">
                  <PasswordInput
                    value={token ?? ""}
                    placeholder={app.has_git_token && token !== "" ? "•••••••• (stored)" : "ghp_… / glpat-…"}
                    onChange={(e) => setToken(e.target.value || null)}
                  />
                </div>
                {app.has_git_token && (
                  <Button type="button" variant="ghost" onClick={() => setToken("")} disabled={token === ""}>
                    {token === "" ? "Will be removed" : "Remove"}
                  </Button>
                )}
              </div>
            </Field>
          </Section>
        ) : (
          <Section title="Image">
            <Field label="Docker image" className="sm:col-span-2">
              <Input value={f.docker_image} onChange={(e) => set("docker_image", e.target.value)} />
            </Field>
          </Section>
        )}
        {f.source_type === "git" && (
          <div className="border-b border-border px-5 py-6">
            <h4 className="mb-4 text-[13px] font-semibold">Build & runtime</h4>
            <BuildSettings value={f} hidePort onChange={(patch) => setF((x) => ({ ...x, ...patch }))} />
          </div>
        )}
        <Section title="Network" description="Ports of the container. Domains, HTTPS and redirects are managed in the Domains tab.">
          <Field label="Container port" hint="Default internal port for domains.">
            <Input type="number" value={f.port} onChange={(e) => set("port", Number(e.target.value))} />
          </Field>
          <Field label="Host port mappings" hint="e.g. 8080:80 (disables zero-downtime)">
            <Input value={f.ports_mapping} onChange={(e) => set("ports_mapping", e.target.value)} />
          </Field>
        </Section>
        <Section title="Health check & hooks" description="New versions only receive traffic after passing the health check.">
          <Field label="Health check path" hint="HTTP GET must return 2xx. Empty = container running.">
            <Input value={f.health_check_path} onChange={(e) => set("health_check_path", e.target.value)} placeholder="/health" />
          </Field>
          <Field label="Timeout (seconds)">
            <Input type="number" value={f.health_check_timeout} onChange={(e) => set("health_check_timeout", Number(e.target.value))} />
          </Field>
          <Field label="Pre-deploy command" hint="Runs in a one-off container of the new image (e.g. migrations).">
            <Input value={f.pre_deploy_command} onChange={(e) => set("pre_deploy_command", e.target.value)} placeholder="npm run migrate" />
          </Field>
          <Field label="Post-deploy command" hint="Runs inside the new container.">
            <Input value={f.post_deploy_command} onChange={(e) => set("post_deploy_command", e.target.value)} />
          </Field>
        </Section>
        <Section title="Resources & scaling">
          <Field label="CPU limit" hint="e.g. 0.5, 2">
            <Input value={f.cpu_limit} onChange={(e) => set("cpu_limit", e.target.value)} placeholder="unlimited" />
          </Field>
          <Field label="Memory limit" hint="e.g. 512m, 2g">
            <Input value={f.memory_limit} onChange={(e) => set("memory_limit", e.target.value)} placeholder="unlimited" />
          </Field>
          <Field label="Replicas" hint="Load balanced by Traefik.">
            <Input type="number" min={1} max={20} value={f.replicas} onChange={(e) => set("replicas", Number(e.target.value))} />
          </Field>
        </Section>
        <Section title="Advanced">
          <Field label="Custom labels" hint="One key=value per line (e.g. extra Traefik middlewares)." className="sm:col-span-2">
            <Textarea rows={3} value={f.custom_labels} onChange={(e) => set("custom_labels", e.target.value)} />
          </Field>
          <Field label="Custom docker run options" hint="Appended to docker run, e.g. --cap-add SYS_PTRACE -v data:/data" className="sm:col-span-2">
            <Input value={f.custom_docker_options} onChange={(e) => set("custom_docker_options", e.target.value)} />
          </Field>
        </Section>
      </Card>
      <div className="sticky bottom-4 z-10 flex justify-end">
        <Button variant="primary" icon={<Save className="size-4" />} disabled={!dirty} loading={save.isPending} onClick={() => save.mutate()} className="shadow-xl">
          Save configuration
        </Button>
      </div>
      <Card title="Danger zone" className="border-danger/30">
        <div className="flex flex-wrap items-center justify-between gap-4 p-5">
          <p className="text-sm text-muted">Delete this application, its containers, images and deployment history.</p>
          <Button
            variant="danger"
            icon={<Trash2 className="size-4" />}
            loading={del.isPending}
            onClick={async () => {
              if (await confirm({ title: "Delete application", description: "This cannot be undone.", danger: true, confirmText: "Delete", typeToConfirm: app.name })) del.mutate();
            }}
          >
            Delete application
          </Button>
        </div>
      </Card>
    </div>
  );
}

function DeploymentsList({ appId }: { appId: number }) {
  const { data, isLoading } = useQuery({ queryKey: ["app-deployments", appId], queryFn: () => api.get<Deployment[]>(`/applications/${appId}/deployments?limit=100`) });
  if (isLoading) return <Loading />;
  return (
    <Card>
      {data!.length === 0 ? (
        <div className="p-5">
          <Empty icon={<Rocket />} title="No deployments yet" />
        </div>
      ) : (
        <DeploymentsTable deployments={data!} />
      )}
    </Card>
  );
}

function ContainerPicker({ containers, value, onChange }: { containers: Container[]; value: string; onChange: (v: string) => void }) {
  return (
    <Select value={value} onChange={(e) => onChange(e.target.value)} className="max-w-md">
      {containers.map((c) => (
        <option key={c.id} value={c.name}>
          {c.name} ({c.state}){c.labels["shipyard.pr"] && c.labels["shipyard.pr"] !== "0" ? ` · PR #${c.labels["shipyard.pr"]}` : ""}
        </option>
      ))}
    </Select>
  );
}

function AppLogs({ app }: { app: ApplicationDetail }) {
  const { data, isLoading } = useAppContainers(app);
  const [name, setName] = useState("");
  const [nonce, setNonce] = useState(0);
  if (isLoading) return <Loading />;
  if (!data?.length) return <Empty icon={<ScrollText />} title="No containers" description="Deploy the application to see runtime logs." />;
  const current = name || data.find((c) => c.state === "running")?.name || data[0].name;
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <ContainerPicker containers={data} value={current} onChange={setName} />
        <Button size="icon" variant="ghost" onClick={() => setNonce((n) => n + 1)} title="Reconnect">
          <RefreshCw className="size-4" />
        </Button>
      </div>
      <ContainerLogs key={`${current}-${nonce}`} url={`/applications/${app.id}/logs?container=${encodeURIComponent(current)}&tail=500`} className="h-[600px]" />
    </div>
  );
}

function AppTerminal({ app }: { app: ApplicationDetail }) {
  const { data, isLoading } = useAppContainers(app);
  const [name, setName] = useState("");
  if (isLoading) return <Loading />;
  const running = (data ?? []).filter((c) => c.state === "running");
  if (!running.length) return <Empty icon={<SquareTerminal />} title="No running containers" description="Start or deploy the application to open a shell." />;
  const current = name || running[0].name;
  return (
    <div className="space-y-4">
      <ContainerPicker containers={running} value={current} onChange={setName} />
      <Terminal key={current} path={`/servers/${app.server_id}/containers/${encodeURIComponent(current)}/terminal`} />
    </div>
  );
}

function Previews({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const previews = useQuery({ queryKey: ["previews", app.id], queryFn: () => api.get<Preview[]>(`/applications/${app.id}/previews`) });
  const [f, setF] = useState({ preview_enabled: app.preview_enabled, preview_domain: app.preview_domain });
  const save = useMutation({
    mutationFn: () => api.patch<ApplicationDetail>(`/applications/${app.id}`, f),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      toast.success("Preview settings saved");
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (pr: number) => api.del(`/applications/${app.id}/previews/${pr}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["previews", app.id] }),
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="space-y-6">
      <Card title="Pull request previews" description="Every PR targeting the production branch gets its own deployment and URL. Closed PRs are cleaned up automatically.">
        <div className="space-y-4 p-5">
          <Switch checked={f.preview_enabled} onChange={(v) => setF({ ...f, preview_enabled: v })} label="Enable preview deployments" description="Requires the GitHub/GitLab/Gitea webhook with pull request events." />
          <Field label="Preview domain template" hint="Use {{pr}} for the PR number. Empty = <app>-pr-N.<server wildcard domain>.">
            <Input value={f.preview_domain} onChange={(e) => setF({ ...f, preview_domain: e.target.value })} placeholder="pr-{{pr}}.preview.example.com" />
          </Field>
          <div className="flex justify-end">
            <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
              Save
            </Button>
          </div>
        </div>
      </Card>
      <Card title="Active previews">
        {previews.isLoading ? (
          <Loading />
        ) : !previews.data?.length ? (
          <div className="p-5">
            <Empty icon={<GitPullRequest />} title="No previews" description="Open a pull request to create one." />
          </div>
        ) : (
          <div className="divide-y divide-border">
            {previews.data.map((p) => (
              <div key={p.pr_number} className="flex flex-wrap items-center gap-4 px-5 py-3">
                <Badge tone="info">
                  <GitPullRequest className="size-3" />#{p.pr_number}
                </Badge>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{p.last_deployment.commit_message || p.branch}</div>
                  <div className="text-xs text-muted">
                    {p.branch} · {shortSha(p.last_deployment.commit_sha)} · {timeAgo(p.last_deployment.created_at)}
                  </div>
                </div>
                {p.domains?.map((d) => (
                  <a key={d} href={domainURL(d, app.https)} target="_blank" rel="noreferrer" className="flex items-center gap-1 text-xs text-muted hover:text-fg hover:underline">
                    {d} <ExternalLink className="size-3" />
                  </a>
                ))}
                <Link to={`/applications/${app.id}/deployments/${p.last_deployment.id}`}>
                  <StatusBadge status={p.last_deployment.status} />
                </Link>
                <Button
                  size="icon"
                  variant="ghost"
                  onClick={async () => {
                    if (await confirm({ title: `Remove preview #${p.pr_number}?`, danger: true, confirmText: "Remove" })) del.mutate(p.pr_number);
                  }}
                >
                  <Trash2 className="size-4 text-danger" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

function Webhooks({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const toggle = useMutation({
    mutationFn: (auto_deploy: boolean) => api.patch<ApplicationDetail>(`/applications/${app.id}`, { auto_deploy }),
    onSuccess: (a) => qc.setQueryData(["application", app.id], a),
  });
  const [watch, setWatch] = useState(app.watch_paths);
  const saveWatch = useMutation({
    mutationFn: () => api.patch<ApplicationDetail>(`/applications/${app.id}`, { watch_paths: watch }),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      toast.success("Watch paths saved");
    },
  });
  const regen = useMutation({
    mutationFn: () => api.post<ApplicationDetail>(`/applications/${app.id}/webhook-secret`),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      toast.success("Secret regenerated — update your webhooks");
    },
  });
  const ghAction = `name: Deploy
on:
  push:
    branches: [${app.git_branch || "main"}]
jobs:
  test-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      # - run: npm ci && npm test
      - name: Trigger Shipyard deployment
        run: curl -fsS -X POST "\${{ secrets.SHIPYARD_DEPLOY_HOOK }}&commit=\${{ github.sha }}"`;

  return (
    <div className="space-y-6">
      <Card title="Automatic deployments">
        <div className="space-y-5 p-5">
          <Switch checked={app.auto_deploy} onChange={(v) => toggle.mutate(v)} label="Deploy on push" description={`Pushes to ${app.git_branch} trigger a new deployment. Newer pushes supersede queued builds.`} />
          <Field label="Watch paths" hint="Only deploy when changed files match these globs (one per line, ** supported, ! to exclude). Empty = always.">
            <Textarea rows={3} value={watch} onChange={(e) => setWatch(e.target.value)} placeholder={"apps/web/**\npackages/ui/**\n!**/*.md"} />
          </Field>
          {watch !== app.watch_paths && (
            <div className="flex justify-end">
              <Button size="sm" variant="primary" onClick={() => saveWatch.mutate()} loading={saveWatch.isPending}>
                Save watch paths
              </Button>
            </div>
          )}
        </div>
      </Card>
      {app.github_app_id ? (
        <Card title="GitHub App" description="This application receives events through the connected GitHub App — no repository webhook is needed.">
          <div className="grid gap-3 p-5 text-[13px] text-muted sm:grid-cols-3">
            <div className="rounded-lg border border-border bg-surface-2/40 p-3">
              <div className="font-medium text-fg">Push to {app.git_branch}</div>
              <div className="mt-0.5 text-xs">{app.auto_deploy ? "Deploys the new commit automatically." : "Automatic deploys are off."}</div>
            </div>
            <div className="rounded-lg border border-border bg-surface-2/40 p-3">
              <div className="font-medium text-fg">Pull requests</div>
              <div className="mt-0.5 text-xs">{app.preview_enabled ? "Each PR gets a preview deployment." : "Enable previews in the Previews tab."}</div>
            </div>
            <div className="rounded-lg border border-border bg-surface-2/40 p-3">
              <div className="font-medium text-fg">Commit status</div>
              <div className="mt-0.5 text-xs">Shows as “shipyard/deploy” on commits and pull requests.</div>
            </div>
          </div>
        </Card>
      ) : null}
      <Card
        title={app.github_app_id ? "Other Git providers" : "Git provider webhooks"}
        description="Add one of these URLs as a webhook in your repository (content type: application/json). Select push and pull request events."
        actions={
          <Button
            size="sm"
            variant="ghost"
            icon={<RefreshCw className="size-3.5" />}
            onClick={async () => {
              if (await confirm({ title: "Regenerate webhook secret?", description: "Existing webhooks and deploy hooks stop working until updated." })) regen.mutate();
            }}
          >
            Regenerate secret
          </Button>
        }
      >
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <CopyField label="GitHub" value={app.webhooks.github} />
          <CopyField label="GitLab" value={app.webhooks.gitlab} />
          <CopyField label="Gitea / Forgejo" value={app.webhooks.gitea} />
          <CopyField label="Webhook secret (GitHub/Gitea) · Secret token (GitLab)" value={app.webhook_secret} secret />
        </div>
      </Card>
      <Card title="Deploy hook" description="Trigger deployments from any CI pipeline (GitHub Actions, GitLab CI, Jenkins…) after your tests pass. Optional query params: commit, branch, force=true.">
        <div className="space-y-4 p-5">
          <CopyField value={app.webhooks.deploy} secret />
          <div>
            <div className="mb-1.5 text-xs font-medium uppercase tracking-wide text-muted">GitHub Actions example</div>
            <pre className="overflow-x-auto rounded-lg border border-border bg-log p-4 font-mono text-xs text-zinc-300">{ghAction}</pre>
          </div>
        </div>
      </Card>
    </div>
  );
}
