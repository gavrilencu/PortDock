import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Lock } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, BuildPack, BuildSuggestion, ComposeService, GitHubRepo } from "../lib/types";
import { Button, Card, Field, Input, PasswordInput, SegmentedControl, Switch } from "../components/ui";
import { BranchSelect, GitHubMark, RepoPicker, useGitHubApps } from "../components/github";
import { AzureBranchSelect, AzureMark, AzureRepoPicker, ConnectAzureLink, useAzureConnections, type AzureSelection } from "../components/azure";
import { BuildSettings, applySuggestion } from "../components/BuildSettings";
import { t, tx } from "../lib/i18n";
import { NewDeployKey, isSSHRepo } from "../components/gitssh";

/** Creates an application from GitHub, Azure DevOps, any Git URL, or a Docker image. */
export function AppForm({ kind, envId, serverId, onCreated }: { kind: "git" | "image" | "compose"; envId: number; serverId: number; onCreated: (a: ApplicationDetail) => void }) {
  const ghApps = useGitHubApps();
  const hasGitHub = (ghApps.data?.length ?? 0) > 0;
  const azConns = useAzureConnections();
  const hasAzure = (azConns.data?.length ?? 0) > 0;
  const [picked, setSource] = useState<"github" | "azure" | "url" | null>(null);
  // Until one is picked, the first connected provider is the source.
  const source = picked === "github" && hasGitHub ? "github" : picked === "azure" && hasAzure ? "azure" : picked === "url" ? "url" : hasGitHub ? "github" : hasAzure ? "azure" : "url";
  const [repo, setRepo] = useState<GitHubRepo | null>(null);
  const [azRepo, setAzRepo] = useState<AzureSelection | null>(null);
  const [picking, setPicking] = useState(true);
  const [touchedPack, setTouchedPack] = useState(false);
  const [privateRepo, setPrivateRepo] = useState(false);
  const [newKey, setNewKey] = useState<{ app: ApplicationDetail; publicKey: string; deploy: boolean } | null>(null);
  const [f, setF] = useState({
    name: "",
    git_repository: "",
    git_branch: "main",
    git_token: "",
    build_pack: (kind === "compose" ? "compose" : kind === "image" ? "image" : "auto") as BuildPack,
    docker_image: "",
    base_directory: "/",
    dockerfile_path: "Dockerfile",
    compose_path: "docker-compose.yml",
    compose_service: "",
    install_command: "",
    build_command: "",
    start_command: "",
    publish_directory: "dist",
    port: kind === "image" ? 80 : 3000,
    domains: "",
    auto_deploy: true,
    deploy: true,
  });
  const set = (patch: Partial<typeof f>) => setF((x) => ({ ...x, ...patch }));
  const useGH = kind !== "image" && source === "github";
  const useAZ = kind !== "image" && source === "azure";
  // Domains per compose service (frontend, backend…), keyed by service name.
  const [svcDomains, setSvcDomains] = useState<Record<string, { domain: string; port: string }>>({});

  // Suggest the deployment type from the selected repository and branch.
  const detect = useQuery({
    queryKey: useAZ
      ? ["azure-detect", azRepo?.connection_id, azRepo?.id, f.git_branch, f.base_directory]
      : ["github-detect", repo?.github_app_id, repo?.installation_id, repo?.full_name, f.git_branch, f.base_directory],
    queryFn: () =>
      api.get<BuildSuggestion>(
        useAZ
          ? `/azure/detect?connection=${azRepo!.connection_id}&project=${encodeURIComponent(azRepo!.project.id)}&repo=${encodeURIComponent(azRepo!.id)}&ref=${encodeURIComponent(f.git_branch)}&base=${encodeURIComponent(f.base_directory)}`
          : `/github/detect?app=${repo!.github_app_id}&installation=${repo!.installation_id}&repo=${encodeURIComponent(repo!.full_name)}&ref=${encodeURIComponent(f.git_branch)}&base=${encodeURIComponent(f.base_directory)}`,
      ),
    enabled: ((useGH && !!repo) || (useAZ && !!azRepo)) && !!f.git_branch,
    retry: false,
  });
  useEffect(() => {
    if (detect.data && !touchedPack && kind === "git") setF((x) => ({ ...x, ...applySuggestion(detect.data) }));
  }, [detect.data, touchedPack, kind]);

  const repoName = useGH ? (repo?.name ?? "") : useAZ ? (azRepo?.name ?? "") : (f.git_repository.replace(/\.git$/, "").split("/").pop() ?? "");
  const composeServices = (useGH || useAZ) && f.build_pack === "compose" ? (detect.data?.compose_services ?? []) : [];

  const create = useMutation({
    mutationFn: async () => {
      const { deploy, ...rest } = f;
      const body: Record<string, unknown> = {
        ...rest,
        name: f.name || (kind === "image" ? f.docker_image.split("/").pop()?.split(":")[0] : repoName) || "app",
        environment_id: envId,
        server_id: serverId,
        source_type: kind === "image" ? "image" : "git",
      };
      if (composeServices.length > 0) {
        body.domains = "";
        body.domain_routes = composeServices
          .filter((s) => svcDomains[s.name]?.domain.trim())
          .map((s) => ({ domain: svcDomains[s.name].domain.trim(), service: s.name, port: Number(svcDomains[s.name].port) || 0 }));
      }
      if (useGH) {
        if (!repo) throw new Error(t("Select a repository"));
        body.github_app_id = repo.github_app_id;
        body.github_installation_id = repo.installation_id;
        body.git_full_name = repo.full_name;
        delete body.git_token;
      } else if (useAZ) {
        if (!azRepo) throw new Error(t("Select a repository"));
        body.azure_connection_id = azRepo.connection_id;
        body.azure_project_id = azRepo.project.id;
        body.azure_repo_id = azRepo.id;
        body.git_repository = azRepo.remote_url;
        delete body.git_token;
      } else if (!privateRepo || isSSHRepo(f.git_repository)) {
        delete body.git_token;
      }
      const app = await api.post<ApplicationDetail>("/applications", body);
      if (kind === "git" && !useGH && !useAZ && isSSHRepo(f.git_repository)) {
        // The deploy key exists only now: it goes into the repository before the first deployment.
        const key = await api.post<{ public_key: string }>(`/applications/${app.id}/deploy-key`, {});
        return { app, publicKey: key.public_key, deploy: !!deploy };
      }
      if (deploy) await api.post(`/applications/${app.id}/deploy`);
      return { app, publicKey: "", deploy: !!deploy };
    },
    onSuccess: ({ app: a, publicKey, deploy }) => {
      if (publicKey) {
        toast.success(t("{name} created", { name: a.name }));
        setNewKey({ app: a, publicKey, deploy });
        return;
      }
      toast.success(deploy ? t("{name} created — first deployment queued", { name: a.name }) : t("{name} created", { name: a.name }));
      onCreated(a);
    },
    onError: (e) => toast.error(e.message),
  });

  if (kind === "image")
    return (
      <Card title={t("Docker image")} description={t("Deploy any public image, or a private one the server can pull.")}>
        <form
          className="grid gap-5 p-5 md:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <Field label={t("Image")} required hint={t("e.g. nginx:alpine, ghcr.io/acme/api:1.4.2")} className="md:col-span-2">
            <Input required value={f.docker_image} onChange={(e) => set({ docker_image: e.target.value })} placeholder="ghcr.io/acme/app:latest" />
          </Field>
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="my-app" />
          </Field>
          <Field label={t("Container port")}>
            <Input type="number" value={f.port} onChange={(e) => set({ port: Number(e.target.value) })} />
          </Field>
          <Field label={t("Domains")} hint={t("Comma separated. TLS certificates are issued automatically.")} className="md:col-span-2">
            <Input value={f.domains} onChange={(e) => set({ domains: e.target.value })} placeholder="app.example.com" />
          </Field>
          <div className="flex items-center justify-between gap-4 border-t border-border pt-5 md:col-span-2">
            <Switch checked={f.deploy} onChange={(v) => set({ deploy: v })} label={t("Deploy immediately")} />
            <Button type="submit" variant="primary" loading={create.isPending}>
              {t("Create application")}
            </Button>
          </div>
        </form>
      </Card>
    );

  const ready = useGH ? repo && !picking : useAZ ? azRepo && !picking : true;

  return (
    <div className="space-y-6">
      {newKey && <NewDeployKey app={newKey.app} publicKey={newKey.publicKey} deploy={newKey.deploy} onDone={() => onCreated(newKey.app)} />}
      <Card
        title={t("Source")}
        description={
          useGH ? t("Pick a repository from your connected GitHub accounts.") : useAZ ? t("Pick a repository from your connected Azure DevOps organizations.") : t("Clone from any Git host, over HTTPS or SSH.")
        }
        actions={
          (hasGitHub || hasAzure) && (
            <SegmentedControl
              value={source}
              onChange={(v) => {
                setSource(v);
                setPicking(true);
                setTouchedPack(false);
              }}
              options={[
                ...(hasGitHub
                  ? [
                      {
                        value: "github" as const,
                        label: (
                          <span className="flex items-center gap-1.5">
                            <GitHubMark className="size-3.5" />
                            GitHub
                          </span>
                        ),
                      },
                    ]
                  : []),
                ...(hasAzure
                  ? [
                      {
                        value: "azure" as const,
                        label: (
                          <span className="flex items-center gap-1.5">
                            <AzureMark className="size-3.5 text-[#0078d4]" />
                            Azure DevOps
                          </span>
                        ),
                      },
                    ]
                  : []),
                { value: "url" as const, label: t("Git URL") },
              ]}
            />
          )
        }
      >
        {useGH ? (
          repo && !picking ? (
            <div className="grid gap-4 p-5 md:grid-cols-3">
              <div className="flex items-center gap-3 rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 md:col-span-3">
                <img src={repo.owner_avatar} alt="" className="size-8 rounded-full border border-border" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5 text-[13px] font-medium">
                    {repo.full_name}
                    {repo.private && <Lock className="size-3 text-faint" />}
                  </div>
                  <div className="truncate text-xs text-subtle">{repo.description || t("No description")}</div>
                </div>
                <Button size="sm" onClick={() => setPicking(true)}>
                  {t("Change")}
                </Button>
              </div>
              <Field label={t("Branch")} hint={t("Commits to this branch are deployed.")}>
                <BranchSelect appId={repo.github_app_id} installationId={repo.installation_id} repo={repo.full_name} value={f.git_branch} defaultBranch={repo.default_branch} onChange={(b) => set({ git_branch: b })} />
              </Field>
              <Field label={t("Root directory")} hint={t("For monorepos, e.g. /apps/web")}>
                <Input value={f.base_directory} onChange={(e) => set({ base_directory: e.target.value || "/" })} />
              </Field>
              <div className="flex items-end pb-1">
                <Switch checked={f.auto_deploy} onChange={(v) => set({ auto_deploy: v })} label={t("Deploy on push")} description={t("Build every new commit on this branch.")} />
              </div>
            </div>
          ) : (
            <RepoPicker
              selected={repo}
              onSelect={(r) => {
                setRepo(r);
                setPicking(false);
                setTouchedPack(false);
                set({ git_branch: r.default_branch, name: f.name || r.name });
              }}
            />
          )
        ) : useAZ ? (
          azRepo && !picking ? (
            <div className="grid gap-4 p-5 md:grid-cols-3">
              <div className="flex items-center gap-3 rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 md:col-span-3">
                <span className="flex size-8 items-center justify-center rounded-lg border border-border bg-surface-2 text-[#0078d4]">
                  <AzureMark />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="text-[13px] font-medium">
                    {azRepo.project.name} / {azRepo.name}
                  </div>
                  <div className="truncate text-xs text-subtle">{azRepo.remote_url}</div>
                </div>
                <Button size="sm" onClick={() => setPicking(true)}>
                  {t("Change")}
                </Button>
              </div>
              <Field label={t("Branch")} hint={t("Commits to this branch are deployed.")}>
                <AzureBranchSelect connection={azRepo.connection_id} project={azRepo.project.id} repo={azRepo.id} value={f.git_branch} defaultBranch={azRepo.default_branch} onChange={(b) => set({ git_branch: b })} />
              </Field>
              <Field label={t("Root directory")} hint={t("For monorepos, e.g. /apps/web")}>
                <Input value={f.base_directory} onChange={(e) => set({ base_directory: e.target.value || "/" })} />
              </Field>
              <div className="flex items-end pb-1">
                <Switch checked={f.auto_deploy} onChange={(v) => set({ auto_deploy: v })} label={t("Deploy on push")} description={t("Build every new commit on this branch.")} />
              </div>
            </div>
          ) : (
            <AzureRepoPicker
              connections={azConns.data ?? []}
              selected={azRepo}
              onSelect={(r) => {
                setAzRepo(r);
                setPicking(false);
                setTouchedPack(false);
                set({ git_branch: r.default_branch || "main", name: f.name || r.name });
              }}
            />
          )
        ) : (
          <div className="grid gap-4 p-5 md:grid-cols-2">
            <Field label={t("Repository URL")} required className="md:col-span-2">
              <Input required value={f.git_repository} onChange={(e) => set({ git_repository: e.target.value })} placeholder="https://gitlab.com/acme/web.git · git@gitlab.corp:team/web.git" />
            </Field>
            <Field label={t("Branch")}>
              <Input value={f.git_branch} onChange={(e) => set({ git_branch: e.target.value })} />
            </Field>
            <Field label={t("Root directory")}>
              <Input value={f.base_directory} onChange={(e) => set({ base_directory: e.target.value || "/" })} />
            </Field>
            <div className="space-y-3 md:col-span-2">
              {isSSHRepo(f.git_repository) ? (
                <p className="rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 text-xs text-muted">
                  {t("Cloned over SSH: PortDock generates a deploy key for this application and shows it after you create it, to add to the repository.")}
                </p>
              ) : (
                <>
                  <Switch checked={privateRepo} onChange={setPrivateRepo} label={t("Private repository")} description={t("Authenticate with a personal access or deploy token.")} />
                  {privateRepo && <PasswordInput value={f.git_token} onChange={(e) => set({ git_token: e.target.value })} placeholder="glpat-… / ghp_…" />}
                </>
              )}
            </div>
            {!hasGitHub && !hasAzure && (
              <div className="rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 text-xs text-muted md:col-span-2">
                {tx("Tip: {github} or {azure} to browse repositories and deploy automatically on every push.", {
                  github: (
                    <Link to="/settings?tab=git" className="font-medium text-fg underline">
                      {t("connect GitHub")}
                    </Link>
                  ),
                  azure: <ConnectAzureLink />,
                })}
              </div>
            )}
          </div>
        )}
      </Card>

      {ready && (
        <>
          <Card title={t("Build & runtime")} description={detect.isFetching ? t("Analyzing repository…") : t("How the application is built and started.")}>
            <div className="p-5">
              <BuildSettings
                value={f}
                suggestion={useGH || useAZ ? detect.data : null}
                onChange={(patch) => {
                  if (patch.build_pack) setTouchedPack(true);
                  set(patch);
                }}
              />
            </div>
          </Card>
          <Card title={t("Application")}>
            <form
              className="grid gap-4 p-5 md:grid-cols-2"
              onSubmit={(e) => {
                e.preventDefault();
                create.mutate();
              }}
            >
              <Field label={t("Name")} hint={t("Defaults to the repository name.")}>
                <Input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder={repoName || "my-app"} />
              </Field>
              {composeServices.length > 0 ? (
                <ServiceDomains services={composeServices} value={svcDomains} onChange={setSvcDomains} />
              ) : (
                <Field label={t("Domains")} hint={t("Comma separated. Leave empty to use the server's wildcard domain.")}>
                  <Input value={f.domains} onChange={(e) => set({ domains: e.target.value })} placeholder="app.example.com" />
                </Field>
              )}
              <div className="flex items-center justify-between gap-4 border-t border-border pt-5 md:col-span-2">
                <Switch checked={f.deploy} onChange={(v) => set({ deploy: v })} label={t("Deploy immediately")} />
                <Button type="submit" variant="primary" loading={create.isPending} disabled={f.build_pack === "shell" && !f.start_command}>
                  {f.deploy ? t("Create & deploy") : t("Create application")}
                </Button>
              </div>
            </form>
          </Card>
        </>
      )}
    </div>
  );
}

/** One domain (and optional port) per service of the repository's compose file. */
function ServiceDomains({
  services,
  value,
  onChange,
}: {
  services: ComposeService[];
  value: Record<string, { domain: string; port: string }>;
  onChange: (v: Record<string, { domain: string; port: string }>) => void;
}) {
  const set = (name: string, patch: Partial<{ domain: string; port: string }>) =>
    onChange({ ...value, [name]: { ...(value[name] ?? { domain: "", port: "" }), ...patch } });
  return (
    <div className="space-y-2 md:col-span-2">
      <div>
        <div className="text-[13px] font-medium">{t("Domains per service")}</div>
        <p className="mt-0.5 text-xs text-subtle">
          {t("Give each public service of docker-compose.yml its own domain, e.g. the frontend on app.example.com and the backend on api.example.com. Leave databases and workers empty.")}
        </p>
      </div>
      <div className="divide-y divide-border rounded-lg border border-border">
        {services.map((s) => (
          <div key={s.name} className="grid items-center gap-2 px-3 py-2.5 sm:grid-cols-[10rem_1fr_7rem]">
            <span className="truncate font-mono text-[13px]" title={s.name}>
              {s.name}
            </span>
            <Input value={value[s.name]?.domain ?? ""} onChange={(e) => set(s.name, { domain: e.target.value })} placeholder={`${s.name}.example.com`} aria-label={t("Domain of {service}", { service: s.name })} />
            <Input
              type="number"
              value={value[s.name]?.port ?? ""}
              onChange={(e) => set(s.name, { port: e.target.value })}
              placeholder={s.port ? String(s.port) : t("Port")}
              aria-label={t("Port of {service}", { service: s.name })}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
