import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
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
  Lock,
  ShieldCheck,
  Network,
  History,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, Container, Deployment, Preview, Server } from "../lib/types";
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
  StatusDot,
  Switch,
  Tabs,
  Textarea,
  useConfirm,
} from "../components/ui";
import { DeploymentsTable } from "../components/shared";
import { BranchSelect, RepoLink } from "../components/github";
import { AzureBranchSelect, AzureHookCard, AzureRepoLink } from "../components/azure";
import { BuildSettings } from "../components/BuildSettings";
import { AccessCallout, DNSBadge, DomainLinks, DomainsTab, domainURL as rowURL, publishedURLs, useDomains } from "../components/Domains";
import { EnvEditor } from "../components/EnvEditor";
import { ContainerLogs, ContainerTable } from "../components/ContainerTable";
import { useRegistries } from "../components/registries";
import { AccessNotice } from "../components/access";
import { Terminal } from "../components/Terminal";
import { domainURL, shortSha, timeAgo } from "../lib/utils";
import { MetricsPanel } from "../components/metrics";
import { ResourceTopology } from "../components/resourceTopology";
import { VersionsBoard } from "../components/versions";
import { t, tx } from "../lib/i18n";

type Tab = "overview" | "topology" | "metrics" | "config" | "domains" | "env" | "versions" | "deployments" | "logs" | "terminal" | "previews" | "webhooks";

export function useApp(id: number) {
  return useQuery({ queryKey: ["application", id], queryFn: () => api.get<ApplicationDetail>(`/applications/${id}`) });
}

export function ApplicationPage() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab) || "overview";
  const setTab = (v: Tab) => setParams({ tab: v });
  const qc = useQueryClient();
  const nav = useNavigate();
  const { data: app, isLoading, error } = useApp(id);

  const deploy = useMutation({
    mutationFn: (force: boolean) => api.post<Deployment>(`/applications/${id}/deploy`, { force_rebuild: force }),
    onSuccess: (d) => {
      qc.invalidateQueries({ queryKey: ["app-deployments", id] });
      if (d.status === "pending_approval") toast.success(t("Deployment requested — a maintainer has to approve it"));
      nav(`/applications/${id}/deployments/${d.id}`);
    },
    onError: (e) => toast.error(e.message),
  });
  const action = useMutation({
    mutationFn: (a: "start" | "stop" | "restart") => api.post(`/applications/${id}/${a}`),
    onSuccess: (_, a) => {
      toast.success({ start: t("Application started"), stop: t("Application stopped"), restart: t("Application restarted") }[a]);
      qc.invalidateQueries({ queryKey: ["application", id] });
      qc.invalidateQueries({ queryKey: ["containers"] });
    },
    onError: (e) => toast.error(e.message),
  });

  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  const a = app!;
  const running = a.status === "running" || a.status === "degraded";
  const acc = a.access;

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
            <span>{t("on {server}", { server: a.server_name })}</span>
          </span>
        }
        breadcrumbs={
          <>
            <Link to="/projects" className="hover:text-fg">{t("Projects")}</Link> /{" "}
            <Link to={`/projects/${a.project.id}?env=${a.environment_id}`} className="hover:text-fg">
              {a.project.name}
            </Link>{" "}
            / <span>{a.environment.name}</span>
            {a.environment.protected && <Lock className="mx-1 inline size-3 text-warning" />} / <span className="text-fg">{a.name}</span>
          </>
        }
        actions={
          <>
            <DomainLinks appId={a.id} extra={publishedURLs(a, a.server_host)} />
            {!acc.can_write ? null : running ? (
              <>
                <Button icon={<RotateCw className="size-4" />} onClick={() => action.mutate("restart")} loading={action.isPending && action.variables === "restart"}>
                  {t("Restart")}
                </Button>
                <Button icon={<Square className="size-4" />} onClick={() => action.mutate("stop")} loading={action.isPending && action.variables === "stop"}>
                  {t("Stop")}
                </Button>
              </>
            ) : (
              a.current_image && (
                <Button icon={<Play className="size-4" />} onClick={() => action.mutate("start")}>
                  {t("Start")}
                </Button>
              )
            )}
            {acc.can_deploy && !acc.deploy_needs_approval && (
              <Button icon={<Hammer className="size-4" />} onClick={() => deploy.mutate(true)} title={t("Rebuild without cache reuse")}>
                {t("Force rebuild")}
              </Button>
            )}
            {acc.can_deploy && (
              <Button variant="primary" icon={acc.deploy_needs_approval ? <ShieldCheck className="size-4" /> : <Rocket className="size-4" />} loading={deploy.isPending} onClick={() => deploy.mutate(false)}>
                {acc.deploy_needs_approval ? t("Request deployment") : a.current_image ? t("Redeploy") : t("Deploy")}
              </Button>
            )}
          </>
        }
      />
      <Tabs
        value={tab}
        onChange={setTab}
        tabs={[
          { id: "overview", label: t("Overview"), icon: <LayoutGrid /> },
          { id: "topology", label: t("Topology"), icon: <Network /> },
          { id: "metrics", label: t("Metrics"), icon: <Activity /> },
          { id: "config", label: t("Configuration"), icon: <Settings2 /> },
          { id: "domains", label: t("Domains"), icon: <Globe /> },
          { id: "env", label: t("Environment"), icon: <KeyRound />, hidden: !a.access.can_write },
          { id: "versions", label: t("Versions"), icon: <History /> },
          { id: "deployments", label: t("Deployments"), icon: <Rocket /> },
          { id: "logs", label: t("Logs"), icon: <ScrollText /> },
          { id: "terminal", label: t("Terminal"), icon: <SquareTerminal />, hidden: !a.access.can_write },
          { id: "previews", label: t("Previews"), icon: <GitPullRequest />, hidden: a.source_type !== "git" },
          { id: "webhooks", label: t("CI/CD & Webhooks"), icon: <Webhook /> },
        ]}
      />
      {tab === "overview" && (
        <div className="space-y-6">
          <AccessCallout app={a} />
          <Overview app={a} />
        </div>
      )}
      {tab === "topology" && <ResourceTopology kind="application" id={a.id} />}
      {tab === "metrics" && <MetricsPanel scope="application" id={a.id} />}
      {tab !== "overview" && tab !== "metrics" && tab !== "deployments" && tab !== "logs" && <AccessNotice access={a.access} />}
      {tab === "config" && <Configuration app={a} />}
      {tab === "domains" && <DomainsTab app={a} />}
      {tab === "env" && <EnvEditor owner={`/applications/${id}`} supportsBuild supportsPreview={a.source_type === "git"} app={a} />}
      {tab === "versions" && <VersionsBoard app={a} />}
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
        <Card title={t("Recent deployments")} actions={<Link to="?tab=deployments" className="text-xs text-muted hover:text-fg">{t("All →")}</Link>}>
          {deps.data?.length ? (
            <DeploymentsTable deployments={deps.data} />
          ) : (
            <div className="p-5">
              <Empty icon={<Rocket />} title={t("Not deployed yet")} description={t("Click Deploy to build and start this application.")} />
            </div>
          )}
        </Card>
        <Card title={t("Containers")}>
          {containers.isLoading ? <Loading /> : containers.error ? <div className="p-5"><ErrorBox error={containers.error} /></div> : <ContainerTable serverId={app.server_id} containers={containers.data ?? []} compact />}
        </Card>
      </div>
      <div className="space-y-6">
        <Card title={t("Domains")} actions={<Link to="?tab=domains" className="text-xs text-muted hover:text-fg">{t("Manage →")}</Link>}>
          <div className="space-y-2 p-4">
            {!domainRows.data?.domains.length ? (
              <p className="text-[13px] text-subtle">
                {tx("No domain yet. {link} to expose the application over HTTPS.", {
                  link: (
                    <Link to="?tab=domains" className="font-medium text-fg underline">
                      {t("Add a domain")}
                    </Link>
                  ),
                })}
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
        <Card title={t("Details")}>
          <dl className="space-y-3 p-5 text-sm">
            {[
              [t("Build pack"), <Badge>{app.build_pack}</Badge>],
              [
                (app.servers?.length ?? 1) > 1 ? t("Servers") : t("Server"),
                <span className="flex flex-wrap justify-end gap-1">
                  {(app.servers ?? []).map((s) => (
                    <Link key={s.id} to={`/servers/${s.id}`}>
                      <Badge>{s.name}</Badge>
                    </Link>
                  ))}
                </span>,
              ],
              ...(app.registry_name ? [[t("Registry"), <code className="break-all text-xs">{app.registry_repository}</code>]] : []),
              [t("Port"), app.port],
              [t("Replicas"), app.replicas],
              [t("Current image"), <code className="break-all text-xs">{app.current_image || "—"}</code>],
              [t("Auto deploy"), app.auto_deploy ? t("On push") : t("Off")],
              [t("Health check"), app.health_check_path || t("container running")],
              [t("Internal hostname"), <code className="text-xs">{app.uuid}</code>],
              [t("Updated"), timeAgo(app.updated_at)],
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
  const extraIds = (a: ApplicationDetail) => (a.servers ?? []).map((s) => s.id).filter((id) => id !== a.server_id);
  const [serverIds, setServerIds] = useState<number[]>(extraIds(app));
  useEffect(() => {
    setF(app);
    setServerIds(extraIds(app));
  }, [app]);
  const set = <K extends keyof ApplicationDetail>(k: K, v: ApplicationDetail[K]) => setF((x) => ({ ...x, [k]: v }));
  const dirty = JSON.stringify(f) !== JSON.stringify(app) || token !== null || JSON.stringify(serverIds) !== JSON.stringify(extraIds(app));

  const save = useMutation({
    mutationFn: () => api.patch<ApplicationDetail>(`/applications/${app.id}`, { ...f, server_ids: serverIds, ...(token !== null ? { git_token: token } : {}) }),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      setToken(null);
      toast.success(t("Configuration saved — redeploy to apply"));
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/applications/${app.id}`),
    onSuccess: () => {
      toast.success(t("Application deleted"));
      nav(`/projects/${app.project.id}?env=${app.environment_id}`);
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <div className="space-y-6">
      <Card>
        <Section title={t("General")}>
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => set("name", e.target.value)} />
          </Field>
          <Field label={t("Description")}>
            <Input value={f.description} onChange={(e) => set("description", e.target.value)} />
          </Field>
        </Section>
        {f.source_type === "git" && f.github_app_id ? (
          <Section title={t("Source")} description={t("Connected through the GitHub App. New commits on the branch deploy automatically.")}>
            <div className="flex items-center gap-3 rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 text-[13px] sm:col-span-2">
              <RepoLink fullName={f.git_full_name} htmlBase={f.git_repository.replace(new RegExp(`/${f.git_full_name}$`), "") || "https://github.com"} />
            </div>
            <Field label={t("Branch")}>
              <BranchSelect appId={f.github_app_id} installationId={f.github_installation_id} repo={f.git_full_name} value={f.git_branch} onChange={(b) => set("git_branch", b)} />
            </Field>
            <Field label={t("Root directory")} hint={t("For monorepos, e.g. /apps/web")}>
              <Input value={f.base_directory} onChange={(e) => set("base_directory", e.target.value)} />
            </Field>
            <Field label={t("Pinned commit")} hint={t("Optional: always deploy this SHA.")}>
              <Input value={f.git_commit} onChange={(e) => set("git_commit", e.target.value)} placeholder="HEAD" />
            </Field>
          </Section>
        ) : f.source_type === "git" && f.azure_connection_id ? (
          <Section title={t("Source")} description={t("Connected through Azure DevOps. New commits on the branch deploy automatically.")}>
            <div className="flex items-center gap-3 rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 text-[13px] sm:col-span-2">
              <AzureRepoLink url={f.git_repository} />
            </div>
            <Field label={t("Branch")}>
              <AzureBranchSelect connection={f.azure_connection_id} project={f.azure_project_id} repo={f.azure_repo_id} value={f.git_branch} onChange={(b) => set("git_branch", b)} />
            </Field>
            <Field label={t("Root directory")} hint={t("For monorepos, e.g. /apps/web")}>
              <Input value={f.base_directory} onChange={(e) => set("base_directory", e.target.value)} />
            </Field>
            <Field label={t("Pinned commit")} hint={t("Optional: always deploy this SHA.")}>
              <Input value={f.git_commit} onChange={(e) => set("git_commit", e.target.value)} placeholder="HEAD" />
            </Field>
          </Section>
        ) : f.source_type === "git" ? (
          <Section title={t("Source")} description={t("Git repository and revision to build.")}>
            <Field label={t("Repository")} className="sm:col-span-2">
              <Input value={f.git_repository} onChange={(e) => set("git_repository", e.target.value)} />
            </Field>
            <Field label={t("Branch")}>
              <Input value={f.git_branch} onChange={(e) => set("git_branch", e.target.value)} />
            </Field>
            <Field label={t("Root directory")} hint={t("For monorepos, e.g. /apps/web")}>
              <Input value={f.base_directory} onChange={(e) => set("base_directory", e.target.value)} />
            </Field>
            <Field label={t("Pinned commit")} hint={t("Optional: always deploy this SHA.")}>
              <Input value={f.git_commit} onChange={(e) => set("git_commit", e.target.value)} placeholder="HEAD" />
            </Field>
            <Field label={t("Access token")} hint={app.has_git_token ? t("A token is stored. Type a new one to replace it.") : t("Required for private repositories.")}>
              <div className="flex gap-2">
                <div className="flex-1">
                  <PasswordInput
                    value={token ?? ""}
                    placeholder={app.has_git_token && token !== "" ? t("•••••••• (stored)") : "ghp_… / glpat-…"}
                    onChange={(e) => setToken(e.target.value || null)}
                  />
                </div>
                {app.has_git_token && (
                  <Button type="button" variant="ghost" onClick={() => setToken("")} disabled={token === ""}>
                    {token === "" ? t("Will be removed") : t("Remove")}
                  </Button>
                )}
              </div>
            </Field>
          </Section>
        ) : (
          <Section title={t("Image")}>
            <Field label={t("Docker image")} className="sm:col-span-2">
              <Input value={f.docker_image} onChange={(e) => set("docker_image", e.target.value)} />
            </Field>
          </Section>
        )}
        {f.source_type === "git" && (
          <div className="border-b border-border px-5 py-6">
            <h4 className="mb-4 text-[13px] font-semibold">{t("Build & runtime")}</h4>
            <BuildSettings value={f} hidePort onChange={(patch) => setF((x) => ({ ...x, ...patch }))} />
          </div>
        )}
        <ServersSection f={f} set={set} serverIds={serverIds} setServerIds={setServerIds} app={app} />
        <Section title={t("Network")} description={t("Ports of the container. Domains, HTTPS and redirects are managed in the Domains tab.")}>
          <Field label={t("Container port")} hint={t("Default internal port for domains.")}>
            <Input type="number" value={f.port} onChange={(e) => set("port", Number(e.target.value))} />
          </Field>
          <Field label={t("Host port mappings")} hint={t("e.g. 8080:80 (disables zero-downtime)")}>
            <Input value={f.ports_mapping} onChange={(e) => set("ports_mapping", e.target.value)} />
          </Field>
        </Section>
        <Section title={t("Health check & hooks")} description={t("New versions only receive traffic after passing the health check.")}>
          <Field label={t("Health check path")} hint={t("HTTP GET must return 2xx. Empty = container running.")}>
            <Input value={f.health_check_path} onChange={(e) => set("health_check_path", e.target.value)} placeholder="/health" />
          </Field>
          <Field label={t("Timeout (seconds)")}>
            <Input type="number" value={f.health_check_timeout} onChange={(e) => set("health_check_timeout", Number(e.target.value))} />
          </Field>
          <Field label={t("Pre-deploy command")} hint={t("Runs in a one-off container of the new image (e.g. migrations).")}>
            <Input value={f.pre_deploy_command} onChange={(e) => set("pre_deploy_command", e.target.value)} placeholder="npm run migrate" />
          </Field>
          <Field label={t("Post-deploy command")} hint={t("Runs inside the new container.")}>
            <Input value={f.post_deploy_command} onChange={(e) => set("post_deploy_command", e.target.value)} />
          </Field>
        </Section>
        <Section title={t("Resources & scaling")}>
          <Field label={t("CPU limit")} hint={t("e.g. 0.5, 2")}>
            <Input value={f.cpu_limit} onChange={(e) => set("cpu_limit", e.target.value)} placeholder={t("unlimited")} />
          </Field>
          <Field label={t("Memory limit")} hint={t("e.g. 512m, 2g")}>
            <Input value={f.memory_limit} onChange={(e) => set("memory_limit", e.target.value)} placeholder={t("unlimited")} />
          </Field>
          <Field label={t("Replicas")} hint={t("Load balanced by Traefik.")}>
            <Input type="number" min={1} max={20} value={f.replicas} onChange={(e) => set("replicas", Number(e.target.value))} />
          </Field>
        </Section>
        <Section title={t("Advanced")}>
          <Field label={t("Custom labels")} hint={t("One key=value per line (e.g. extra Traefik middlewares).")} className="sm:col-span-2">
            <Textarea rows={3} value={f.custom_labels} onChange={(e) => set("custom_labels", e.target.value)} />
          </Field>
          <Field label={t("Custom docker run options")} hint={t("Appended to docker run, e.g. --cap-add SYS_PTRACE -v data:/data")} className="sm:col-span-2">
            <Input value={f.custom_docker_options} onChange={(e) => set("custom_docker_options", e.target.value)} />
          </Field>
        </Section>
      </Card>
      {app.access.can_write && (
        <div className="sticky bottom-4 z-10 flex justify-end">
          <Button variant="primary" icon={<Save className="size-4" />} disabled={!dirty} loading={save.isPending} onClick={() => save.mutate()} className="shadow-xl">
            {t("Save configuration")}
          </Button>
        </div>
      )}
      {app.access.can_manage && (
      <Card title={t("Danger zone")} className="border-danger/30">
        <div className="flex flex-wrap items-center justify-between gap-4 p-5">
          <p className="text-sm text-muted">{t("Delete this application, its containers, images and deployment history.")}</p>
          <Button
            variant="danger"
            icon={<Trash2 className="size-4" />}
            loading={del.isPending}
            onClick={async () => {
              if (await confirm({ title: t("Delete application"), description: t("This cannot be undone."), danger: true, confirmText: t("Delete"), typeToConfirm: app.name })) del.mutate();
            }}
          >
            {t("Delete application")}
          </Button>
        </div>
      </Card>
      )}
    </div>
  );
}

function ServersSection({
  f,
  set,
  serverIds,
  setServerIds,
  app,
}: {
  f: ApplicationDetail;
  set: <K extends keyof ApplicationDetail>(k: K, v: ApplicationDetail[K]) => void;
  serverIds: number[];
  setServerIds: (v: number[]) => void;
  app: ApplicationDetail;
}) {
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers") });
  const regs = useRegistries();
  const list = servers.data ?? [];
  const multi = serverIds.length > 0;
  const builds = f.source_type === "git" && f.build_pack !== "compose";
  const reg = regs.data?.registries.find((r) => r.id === f.registry_id);
  const defaultRepo = reg ? `${reg.host}/${[reg.namespace, app.name.toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^[-._]+|[-._]+$/g, "") || "app"].filter(Boolean).join("/")}` : "";
  return (
    <Section
      title={t("Servers & registry")}
      description={t("Deploy the same build to several servers with a rolling update. Point each domain's DNS (or your load balancer) at all of them.")}
    >
      <Field label={t("Primary server")} hint={app.status !== "stopped" ? t("Stop the application to move it.") : t("Runs previews and the pre-deploy command.")}>
        <Select value={f.server_id} disabled={app.status !== "stopped"} onChange={(e) => set("server_id", Number(e.target.value))}>
          {list.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
      </Field>
      {builds && (
        <Field label={t("Build on")} hint={t("A dedicated build server keeps builds off production machines.")}>
          <Select value={f.build_server_id || 0} onChange={(e) => set("build_server_id", Number(e.target.value))}>
            <option value={0}>{t("Primary server")}</option>
            {list
              .filter((s) => s.id !== f.server_id)
              .map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
          </Select>
        </Field>
      )}
      <Field label={t("Additional servers")} className="sm:col-span-2" hint={list.length <= 1 ? t("Add more servers in Servers to deploy this application to several machines.") : undefined}>
        <div className="grid gap-2 sm:grid-cols-2">
          {list
            .filter((s) => s.id !== f.server_id)
            .map((s) => {
              const on = serverIds.includes(s.id);
              return (
                <label key={s.id} className={`flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2 text-[13px] transition-colors ${on ? "border-border-strong bg-surface-2" : "border-border hover:bg-surface-2/60"}`}>
                  <input type="checkbox" className="size-4 accent-[var(--color-fg)]" checked={on} onChange={(e) => setServerIds(e.target.checked ? [...serverIds, s.id].sort((a, b) => a - b) : serverIds.filter((x) => x !== s.id))} />
                  <StatusDot status={s.status === "reachable" ? "running" : "exited"} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{s.name}</span>
                    <span className="block truncate font-mono text-2xs text-muted">{s.host}</span>
                  </span>
                </label>
              );
            })}
        </div>
        {extraRemoved(app, serverIds) && <p className="mt-2 text-xs text-warning">{t("Servers you uncheck have this application removed from them when you save.")}</p>}
      </Field>
      {(builds || f.source_type === "image" || f.build_pack === "compose") && (
        <>
          <Field
            label={t("Container registry")}
            hint={
              builds
                ? multi || f.build_server_id
                  ? reg
                    ? t("Built images are pushed here; every server pulls them.")
                    : t("Without a registry the image is streamed from the build server to each server over SSH.")
                  : t("Optional: push every build so it can be pulled anywhere.")
                : t("Credentials used to pull private images.")
            }
          >
            <Select value={f.registry_id || 0} onChange={(e) => set("registry_id", Number(e.target.value))}>
              <option value={0}>{builds ? t("None — keep images on the servers") : t("None — public image")}</option>
              {regs.data?.registries.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </Select>
            {!regs.data?.registries.length && (
              <Link to="/settings?tab=registries" className="mt-1.5 inline-block text-xs text-muted underline-offset-2 hover:text-fg hover:underline">
                {t("Connect a registry →")}
              </Link>
            )}
          </Field>
          {builds && reg && (
            <Field label={t("Image name")} hint={t("Default: {repo}", { repo: defaultRepo })}>
              <Input value={f.registry_image} onChange={(e) => set("registry_image", e.target.value)} placeholder={defaultRepo.slice(reg.host.length + 1)} className="font-mono text-xs" />
            </Field>
          )}
        </>
      )}
    </Section>
  );
}

function extraRemoved(app: ApplicationDetail, ids: number[]) {
  return (app.servers ?? []).some((s) => s.id !== app.server_id && !ids.includes(s.id));
}

function DeploymentsList({ appId }: { appId: number }) {
  const { data, isLoading } = useQuery({ queryKey: ["app-deployments", appId], queryFn: () => api.get<Deployment[]>(`/applications/${appId}/deployments?limit=100`) });
  if (isLoading) return <Loading />;
  return (
    <Card>
      {data!.length === 0 ? (
        <div className="p-5">
          <Empty icon={<Rocket />} title={t("No deployments yet")} />
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
          {c.server_name ? `${c.server_name} · ` : ""}
          {c.name} ({c.state}){c.labels["portdock.pr"] && c.labels["portdock.pr"] !== "0" ? ` · PR #${c.labels["portdock.pr"]}` : ""}
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
  if (!data?.length) return <Empty icon={<ScrollText />} title={t("No containers")} description={t("Deploy the application to see runtime logs.")} />;
  const current = name || data.find((c) => c.state === "running")?.name || data[0].name;
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <ContainerPicker containers={data} value={current} onChange={setName} />
        <Button size="icon" variant="ghost" onClick={() => setNonce((n) => n + 1)} title={t("Reconnect")}>
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
  if (!running.length) return <Empty icon={<SquareTerminal />} title={t("No running containers")} description={t("Start or deploy the application to open a shell.")} />;
  const current = name || running[0].name;
  return (
    <div className="space-y-4">
      <ContainerPicker containers={running} value={current} onChange={setName} />
      <Terminal key={current} path={`/servers/${running.find((c) => c.name === current)?.server_id || app.server_id}/containers/${encodeURIComponent(current)}/terminal`} />
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
      toast.success(t("Preview settings saved"));
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
      <Card title={t("Pull request previews")} description={t("Every PR targeting the production branch gets its own deployment and URL. Closed PRs are cleaned up automatically.")}>
        <div className="space-y-4 p-5">
          <Switch checked={f.preview_enabled} onChange={(v) => setF({ ...f, preview_enabled: v })} label={t("Enable preview deployments")} description={t("Requires the GitHub/GitLab/Gitea webhook with pull request events.")} />
          <Field label={t("Preview domain template")} hint={t("Use {token} for the PR number. Empty = <app>-pr-N.<server wildcard domain>.", { token: "{{pr}}" })}>
            <Input value={f.preview_domain} onChange={(e) => setF({ ...f, preview_domain: e.target.value })} placeholder="pr-{{pr}}.preview.example.com" />
          </Field>
          <div className="flex justify-end">
            <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        </div>
      </Card>
      <Card title={t("Active previews")}>
        {previews.isLoading ? (
          <Loading />
        ) : !previews.data?.length ? (
          <div className="p-5">
            <Empty icon={<GitPullRequest />} title={t("No previews")} description={t("Open a pull request to create one.")} />
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
                    if (await confirm({ title: t("Remove preview #{pr}?", { pr: p.pr_number }), danger: true, confirmText: t("Remove") })) del.mutate(p.pr_number);
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
      toast.success(t("Watch paths saved"));
    },
  });
  const regen = useMutation({
    mutationFn: () => api.post<ApplicationDetail>(`/applications/${app.id}/webhook-secret`),
    onSuccess: (a) => {
      qc.setQueryData(["application", app.id], a);
      toast.success(t("Secret regenerated — update your webhooks"));
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
      - name: Trigger PortDock deployment
        run: curl -fsS -X POST "\${{ secrets.PORTDOCK_DEPLOY_HOOK }}&commit=\${{ github.sha }}"`;

  return (
    <div className="space-y-6">
      <Card title={t("Automatic deployments")}>
        <div className="space-y-5 p-5">
          <Switch checked={app.auto_deploy} onChange={(v) => toggle.mutate(v)} label={t("Deploy on push")} description={t("Pushes to {branch} trigger a new deployment. Newer pushes supersede queued builds.", { branch: app.git_branch })} />
          <Field label={t("Watch paths")} hint={t("Only deploy when changed files match these globs (one per line, ** supported, ! to exclude). Empty = always.")}>
            <Textarea rows={3} value={watch} onChange={(e) => setWatch(e.target.value)} placeholder={"apps/web/**\npackages/ui/**\n!**/*.md"} />
          </Field>
          {watch !== app.watch_paths && (
            <div className="flex justify-end">
              <Button size="sm" variant="primary" onClick={() => saveWatch.mutate()} loading={saveWatch.isPending}>
                {t("Save watch paths")}
              </Button>
            </div>
          )}
        </div>
      </Card>
      {app.github_app_id ? (
        <Card title="GitHub App" description={t("This application receives events through the connected GitHub App — no repository webhook is needed.")}>
          <div className="grid gap-3 p-5 text-[13px] text-muted sm:grid-cols-3">
            <div className="rounded-lg border border-border bg-surface-2/40 p-3">
              <div className="font-medium text-fg">{t("Push to {branch}", { branch: app.git_branch })}</div>
              <div className="mt-0.5 text-xs">{app.auto_deploy ? t("Deploys the new commit automatically.") : t("Automatic deploys are off.")}</div>
            </div>
            <div className="rounded-lg border border-border bg-surface-2/40 p-3">
              <div className="font-medium text-fg">{t("Pull requests")}</div>
              <div className="mt-0.5 text-xs">{app.preview_enabled ? t("Each PR gets a preview deployment.") : t("Enable previews in the Previews tab.")}</div>
            </div>
            <div className="rounded-lg border border-border bg-surface-2/40 p-3">
              <div className="font-medium text-fg">{t("Commit status")}</div>
              <div className="mt-0.5 text-xs">{t("Shows as “portdock/deploy” on commits and pull requests.")}</div>
            </div>
          </div>
        </Card>
      ) : app.azure_connection_id ? (
        <AzureHookCard app={app} />
      ) : null}
      <Card
        title={app.github_app_id || app.azure_connection_id ? t("Other Git providers") : t("Git provider webhooks")}
        description={t("Add one of these URLs as a webhook in your repository (content type: application/json). Select push and pull request events.")}
        actions={
          <Button
            size="sm"
            variant="ghost"
            icon={<RefreshCw className="size-3.5" />}
            onClick={async () => {
              if (await confirm({ title: t("Regenerate webhook secret?"), description: t("Existing webhooks and deploy hooks stop working until updated.") })) regen.mutate();
            }}
          >
            {t("Regenerate secret")}
          </Button>
        }
      >
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <CopyField label="GitHub" value={app.webhooks.github} />
          <CopyField label="GitLab" value={app.webhooks.gitlab} />
          <CopyField label="Gitea / Forgejo" value={app.webhooks.gitea} />
          <CopyField label={t("Webhook secret (GitHub/Gitea) · Secret token (GitLab)")} value={app.webhook_secret} secret />
        </div>
      </Card>
      <Card title={t("Deploy hook")} description={t("Trigger deployments from any CI pipeline (GitHub Actions, GitLab CI, Jenkins…) after your tests pass. Optional query params: commit, branch, force=true.")}>
        <div className="space-y-4 p-5">
          <CopyField value={app.webhooks.deploy} secret />
          <div>
            <div className="mb-1.5 text-xs font-medium uppercase tracking-wide text-muted">{t("GitHub Actions example")}</div>
            <pre className="overflow-x-auto rounded-lg border border-border bg-log p-4 font-mono text-xs text-zinc-300">{ghAction}</pre>
          </div>
        </div>
      </Card>
    </div>
  );
}
