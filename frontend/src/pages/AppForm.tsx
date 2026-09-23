import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Lock } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, BuildPack, BuildSuggestion, GitHubRepo } from "../lib/types";
import { Button, Card, Field, Input, PasswordInput, SegmentedControl, Switch } from "../components/ui";
import { BranchSelect, GitHubMark, RepoPicker, useGitHubApps } from "../components/github";
import { BuildSettings, applySuggestion } from "../components/BuildSettings";

/** Creates an application from GitHub, any Git URL, or a Docker image. */
export function AppForm({ kind, envId, serverId, onCreated }: { kind: "git" | "image" | "compose"; envId: number; serverId: number; onCreated: (a: ApplicationDetail) => void }) {
  const ghApps = useGitHubApps();
  const hasGitHub = (ghApps.data?.length ?? 0) > 0;
  const [source, setSource] = useState<"github" | "url">("github");
  const [repo, setRepo] = useState<GitHubRepo | null>(null);
  const [picking, setPicking] = useState(true);
  const [touchedPack, setTouchedPack] = useState(false);
  const [privateRepo, setPrivateRepo] = useState(false);
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
  const useGH = kind !== "image" && source === "github" && hasGitHub;

  // Suggest the deployment type from the selected repository and branch.
  const detect = useQuery({
    queryKey: ["github-detect", repo?.github_app_id, repo?.installation_id, repo?.full_name, f.git_branch, f.base_directory],
    queryFn: () =>
      api.get<BuildSuggestion>(
        `/github/detect?app=${repo!.github_app_id}&installation=${repo!.installation_id}&repo=${encodeURIComponent(repo!.full_name)}&ref=${encodeURIComponent(f.git_branch)}&base=${encodeURIComponent(f.base_directory)}`,
      ),
    enabled: useGH && !!repo && !!f.git_branch,
    retry: false,
  });
  useEffect(() => {
    if (detect.data && !touchedPack && kind === "git") setF((x) => ({ ...x, ...applySuggestion(detect.data) }));
  }, [detect.data, touchedPack, kind]);

  const repoName = useGH ? (repo?.name ?? "") : (f.git_repository.replace(/\.git$/, "").split("/").pop() ?? "");

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
      if (useGH) {
        if (!repo) throw new Error("Select a repository");
        body.github_app_id = repo.github_app_id;
        body.github_installation_id = repo.installation_id;
        body.git_full_name = repo.full_name;
        delete body.git_token;
      } else if (!privateRepo) {
        delete body.git_token;
      }
      const app = await api.post<ApplicationDetail>("/applications", body);
      if (deploy) await api.post(`/applications/${app.id}/deploy`);
      return app;
    },
    onSuccess: (a) => {
      toast.success(f.deploy ? `${a.name} created — first deployment queued` : `${a.name} created`);
      onCreated(a);
    },
    onError: (e) => toast.error(e.message),
  });

  if (kind === "image")
    return (
      <Card title="Docker image" description="Deploy any public image, or a private one the server can pull.">
        <form
          className="grid gap-5 p-5 md:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <Field label="Image" required hint="e.g. nginx:alpine, ghcr.io/acme/api:1.4.2" className="md:col-span-2">
            <Input required value={f.docker_image} onChange={(e) => set({ docker_image: e.target.value })} placeholder="ghcr.io/acme/app:latest" />
          </Field>
          <Field label="Name">
            <Input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="my-app" />
          </Field>
          <Field label="Container port">
            <Input type="number" value={f.port} onChange={(e) => set({ port: Number(e.target.value) })} />
          </Field>
          <Field label="Domains" hint="Comma separated. TLS certificates are issued automatically." className="md:col-span-2">
            <Input value={f.domains} onChange={(e) => set({ domains: e.target.value })} placeholder="app.example.com" />
          </Field>
          <div className="flex items-center justify-between gap-4 border-t border-border pt-5 md:col-span-2">
            <Switch checked={f.deploy} onChange={(v) => set({ deploy: v })} label="Deploy immediately" />
            <Button type="submit" variant="primary" loading={create.isPending}>
              Create application
            </Button>
          </div>
        </form>
      </Card>
    );

  const ready = !useGH || (repo && !picking);

  return (
    <div className="space-y-6">
      <Card
        title="Source"
        description={useGH ? "Pick a repository from your connected GitHub accounts." : "Clone from any Git host over HTTPS."}
        actions={
          hasGitHub && (
            <SegmentedControl
              value={source}
              onChange={setSource}
              options={[
                {
                  value: "github",
                  label: (
                    <span className="flex items-center gap-1.5">
                      <GitHubMark className="size-3.5" />
                      GitHub
                    </span>
                  ),
                },
                { value: "url", label: "Git URL" },
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
                  <div className="truncate text-xs text-subtle">{repo.description || "No description"}</div>
                </div>
                <Button size="sm" onClick={() => setPicking(true)}>
                  Change
                </Button>
              </div>
              <Field label="Branch" hint="Commits to this branch are deployed.">
                <BranchSelect appId={repo.github_app_id} installationId={repo.installation_id} repo={repo.full_name} value={f.git_branch} defaultBranch={repo.default_branch} onChange={(b) => set({ git_branch: b })} />
              </Field>
              <Field label="Root directory" hint="For monorepos, e.g. /apps/web">
                <Input value={f.base_directory} onChange={(e) => set({ base_directory: e.target.value || "/" })} />
              </Field>
              <div className="flex items-end pb-1">
                <Switch checked={f.auto_deploy} onChange={(v) => set({ auto_deploy: v })} label="Deploy on push" description="Build every new commit on this branch." />
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
        ) : (
          <div className="grid gap-4 p-5 md:grid-cols-2">
            <Field label="Repository URL" required className="md:col-span-2">
              <Input required value={f.git_repository} onChange={(e) => set({ git_repository: e.target.value })} placeholder="https://gitlab.com/acme/web.git" />
            </Field>
            <Field label="Branch">
              <Input value={f.git_branch} onChange={(e) => set({ git_branch: e.target.value })} />
            </Field>
            <Field label="Root directory">
              <Input value={f.base_directory} onChange={(e) => set({ base_directory: e.target.value || "/" })} />
            </Field>
            <div className="space-y-3 md:col-span-2">
              <Switch checked={privateRepo} onChange={setPrivateRepo} label="Private repository" description="Authenticate with a personal access or deploy token." />
              {privateRepo && <PasswordInput value={f.git_token} onChange={(e) => set({ git_token: e.target.value })} placeholder="glpat-… / ghp_…" />}
            </div>
            {!hasGitHub && (
              <div className="rounded-lg border border-border bg-surface-2/50 px-3 py-2.5 text-xs text-muted md:col-span-2">
                Tip:{" "}
                <Link to="/settings?tab=git" className="font-medium text-fg underline">
                  connect GitHub
                </Link>{" "}
                to browse repositories and deploy automatically on every push.
              </div>
            )}
          </div>
        )}
      </Card>

      {ready && (
        <>
          <Card title="Build & runtime" description={detect.isFetching ? "Analyzing repository…" : "How the application is built and started."}>
            <div className="p-5">
              <BuildSettings
                value={f}
                suggestion={useGH ? detect.data : null}
                onChange={(patch) => {
                  if (patch.build_pack) setTouchedPack(true);
                  set(patch);
                }}
              />
            </div>
          </Card>
          <Card title="Application">
            <form
              className="grid gap-4 p-5 md:grid-cols-2"
              onSubmit={(e) => {
                e.preventDefault();
                create.mutate();
              }}
            >
              <Field label="Name" hint="Defaults to the repository name.">
                <Input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder={repoName || "my-app"} />
              </Field>
              <Field label="Domains" hint="Comma separated. Leave empty to use the server's wildcard domain.">
                <Input value={f.domains} onChange={(e) => set({ domains: e.target.value })} placeholder="app.example.com" />
              </Field>
              <div className="flex items-center justify-between gap-4 border-t border-border pt-5 md:col-span-2">
                <Switch checked={f.deploy} onChange={(v) => set({ deploy: v })} label="Deploy immediately" />
                <Button type="submit" variant="primary" loading={create.isPending} disabled={f.build_pack === "shell" && !f.start_command}>
                  {f.deploy ? "Create & deploy" : "Create application"}
                </Button>
              </div>
            </form>
          </Card>
        </>
      )}
    </div>
  );
}
