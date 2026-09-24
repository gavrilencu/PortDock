import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Plug, RefreshCw, RotateCw, Save, XCircle } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Server, Settings } from "../lib/types";
import { t, tMaybe, tx } from "../lib/i18n";
import { Badge, Button, Card, Field, Input, Loading, PasswordInput, StatusDot, Switch, Table, Textarea, useConfirm } from "./ui";

const keys = [
  "proxy_http", "proxy_https", "proxy_no", "proxy_username", "proxy_password",
  "proxy_portdock", "proxy_builds", "proxy_containers",
  "registry_mirror", "registry_insecure", "npm_registry", "pypi_index", "go_proxy", "build_env", "trivy_db_repository",
] as const;
type Key = (typeof keys)[number];
type Form = Record<Key, string>;

const defaultNoProxy = "localhost,127.0.0.1,::1,.local,.internal,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16";

type NetSettings = Settings & Partial<Record<Key, string>> & { proxy_password_set?: boolean };

/** Settings → Network: the corporate proxy, the Docker Hub mirror and the package mirrors. */
export function NetworkSettings() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<NetSettings>("/settings") });
  const [f, setF] = useState<Form | null>(null);
  useEffect(() => {
    if (settings.data && !f) {
      const init = {} as Form;
      for (const k of keys) init[k] = k === "proxy_password" ? "" : String(settings.data[k] ?? "");
      setF(init);
    }
  }, [settings.data, f]);
  const save = useMutation({
    mutationFn: (v: Form) => api.patch<NetSettings>("/settings", v),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      qc.invalidateQueries({ queryKey: ["docker-network"] });
      setF((x) => (x ? { ...x, proxy_password: "" } : x));
      toast.success(t("Network settings saved"), { description: t("They apply to the next builds and deployments.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const [testURL, setTestURL] = useState("https://registry-1.docker.io/v2/");
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; status?: number; error?: string; via: string; ms: number }>("/network/test", { settings: f, url: testURL }),
    onError: (e) => toast.error(e.message),
  });
  if (settings.isLoading || !f) return <Loading />;
  const set = (p: Partial<Form>) => setF({ ...f, ...p });
  const on = (k: Key) => f[k] === "true";
  const toggle = (k: Key) => (v: boolean) => set({ [k]: v ? "true" : "" } as Partial<Form>);
  const saveBtn = (
    <Button size="sm" variant="primary" icon={<Save className="size-3.5" />} loading={save.isPending} onClick={() => save.mutate(f)}>
      {t("Save")}
    </Button>
  );

  return (
    <div className="space-y-6">
      <Card title={t("Corporate proxy")} description={t("For networks where the internet is only reachable through an HTTP proxy.")} actions={saveBtn}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("HTTP proxy")} hint={t("e.g. http://proxy.corp.local:3128")}>
            <Input value={f.proxy_http} onChange={(e) => set({ proxy_http: e.target.value })} placeholder="http://proxy.corp.local:3128" />
          </Field>
          <Field label={t("HTTPS proxy")} hint={t("Leave empty to use the HTTP proxy for HTTPS too.")}>
            <Input value={f.proxy_https} onChange={(e) => set({ proxy_https: e.target.value })} />
          </Field>
          <Field label={t("User name")} hint={t("Optional, e.g. DOMAIN\\user")}>
            <Input value={f.proxy_username} onChange={(e) => set({ proxy_username: e.target.value })} autoComplete="off" />
          </Field>
          <Field label={t("Password")} hint={settings.data?.proxy_password_set ? t("Saved. Type a new one to replace it.") : undefined}>
            <PasswordInput value={f.proxy_password} onChange={(e) => set({ proxy_password: e.target.value })} autoComplete="new-password" placeholder={settings.data?.proxy_password_set ? "••••••••" : ""} />
          </Field>
          <Field
            label={t("Bypass the proxy for (NO_PROXY)")}
            hint={
              <span>
                {t("Comma separated: host names, domains (.corp.local), IP addresses and ranges (10.0.0.0/8).")}{" "}
                {!f.proxy_no && (
                  <button className="font-medium text-fg underline" onClick={() => set({ proxy_no: defaultNoProxy })}>
                    {t("Use the usual private networks")}
                  </button>
                )}
              </span>
            }
            className="md:col-span-2"
          >
            <Textarea rows={2} value={f.proxy_no} onChange={(e) => set({ proxy_no: e.target.value })} className="font-mono text-xs" placeholder={defaultNoProxy} />
          </Field>
          <div className="space-y-3 md:col-span-2">
            <div className="text-[13px] font-medium">{t("Use the proxy for")}</div>
            <Switch checked={on("proxy_portdock")} onChange={toggle("proxy_portdock")} label={t("PortDock itself")} description={t("GitHub and Azure DevOps, update checks, notifications, the AI assistant and uptime checks.")} />
            <Switch checked={on("proxy_builds")} onChange={toggle("proxy_builds")} label={t("Builds")} description={t("Git clone, docker build (RUN steps), Nixpacks, Docker Compose builds and the vulnerability database. Not stored in the images.")} />
            <Switch checked={on("proxy_containers")} onChange={toggle("proxy_containers")} label={t("Containers")} description={t("HTTP_PROXY, HTTPS_PROXY and NO_PROXY in every application and service; each application can opt out in its configuration.")} />
          </div>
          <div className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-surface-2/40 p-3 md:col-span-2">
            <Field label={t("Test a connection through these settings")} className="min-w-64 flex-1">
              <Input value={testURL} onChange={(e) => setTestURL(e.target.value)} />
            </Field>
            <Button icon={<Plug className="size-4" />} loading={test.isPending} onClick={() => test.mutate()}>
              {t("Test")}
            </Button>
            {test.data && (
              <div className="w-full text-xs">
                {test.data.ok ? (
                  <span className="inline-flex items-center gap-1.5 text-success">
                    <CheckCircle2 className="size-3.5" /> {t("Reached in {ms} ms (HTTP {status}) via {via}", { ms: test.data.ms, status: test.data.status ?? "", via: test.data.via === "direct" ? t("a direct connection") : test.data.via })}
                  </span>
                ) : (
                  <span className="inline-flex items-start gap-1.5 text-danger">
                    <XCircle className="mt-0.5 size-3.5 shrink-0" /> {tMaybe(test.data.error ?? "")}
                  </span>
                )}
              </div>
            )}
          </div>
        </div>
      </Card>

      <Card
        title={t("Docker Hub mirror")}
        description={t("A pull-through registry (Nexus, Artifactory, Harbor) used by the Docker daemon of each server for images of Docker Hub, including the FROM of builds.")}
        actions={saveBtn}
      >
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("Mirror URL")} hint={t("e.g. https://nexus.corp.local:8443 (a Docker proxy repository of Docker Hub)")}>
            <Input value={f.registry_mirror} onChange={(e) => set({ registry_mirror: e.target.value })} placeholder="https://nexus.corp.local:8443" />
          </Field>
          <div className="flex items-end pb-1">
            <Switch checked={on("registry_insecure")} onChange={toggle("registry_insecure")} label={t("Plain HTTP or self-signed certificate")} description={t("Adds the mirror to insecure-registries.")} />
          </div>
          <p className="text-xs text-subtle md:col-span-2">
            {tx("Private images of the mirror (hosted repositories) are added in {registries} like any registry.", {
              registries: <span className="font-medium text-fg">{t("Container registries")}</span>,
            })}
          </p>
        </div>
        <DaemonTable />
      </Card>

      <Card title={t("Package mirrors for builds")} description={t("Build variables that point package managers at your mirrors (Nexus proxy repositories).")} actions={saveBtn}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("npm registry")} hint="NPM_CONFIG_REGISTRY, YARN_NPM_REGISTRY_SERVER, BUN_CONFIG_REGISTRY">
            <Input value={f.npm_registry} onChange={(e) => set({ npm_registry: e.target.value })} placeholder="https://nexus.corp.local/repository/npm-proxy/" />
          </Field>
          <Field label={t("PyPI index")} hint="PIP_INDEX_URL, PIP_TRUSTED_HOST, UV_INDEX_URL">
            <Input value={f.pypi_index} onChange={(e) => set({ pypi_index: e.target.value })} placeholder="https://nexus.corp.local/repository/pypi-proxy/simple" />
          </Field>
          <Field label={t("Go module proxy")} hint="GOPROXY">
            <Input value={f.go_proxy} onChange={(e) => set({ go_proxy: e.target.value })} placeholder="https://nexus.corp.local/repository/go-proxy/,direct" />
          </Field>
          <Field label={t("Trivy database mirror")} hint={t("OCI repository with a copy of ghcr.io/aquasecurity/trivy-db, for servers without internet.")}>
            <Input value={f.trivy_db_repository} onChange={(e) => set({ trivy_db_repository: e.target.value })} placeholder="nexus.corp.local:8443/aquasecurity/trivy-db:2" />
          </Field>
          <Field label={t("Other build variables")} hint={t("KEY=value per line, e.g. for Maven, NuGet or Composer mirrors.")} className="md:col-span-2">
            <Textarea rows={3} value={f.build_env} onChange={(e) => set({ build_env: e.target.value })} className="font-mono text-xs" placeholder={"MAVEN_OPTS=-Dmaven.repo.remote=https://nexus.corp.local/repository/maven-public\nCOMPOSER_MIRROR_PATH_REPOS=1"} />
          </Field>
          <p className="text-xs text-subtle md:col-span-2">
            {tx("The Node.js, Bun, Python and Go build packs use them automatically. In your own Dockerfile, declare them with {arg} before the step that installs packages.", {
              arg: <code className="font-mono text-fg">ARG NPM_CONFIG_REGISTRY</code>,
            })}
          </p>
        </div>
      </Card>
    </div>
  );
}

interface DaemonNetwork {
  mirrors: string[];
  insecure: string[];
  http_proxy: string;
  https_proxy: string;
  systemd: boolean;
  want_mirror: string;
  want_proxy: boolean;
  mirror_ok: boolean;
  proxy_ok: boolean;
  up_to_date: boolean;
}

/** The Docker daemon of each server: what it uses for pulls, and applying the settings. */
function DaemonTable() {
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers") });
  if (servers.isLoading) return <Loading rows={2} />;
  return (
    <div className="border-t border-border">
      <div className="px-5 pt-4 text-[13px] font-medium">{t("Docker daemon of each server")}</div>
      <p className="px-5 pb-2 text-xs text-subtle">
        {t("Save first, then apply to each server. Applying reloads the mirror without stopping containers; the daemon's proxy needs a restart of Docker (containers come back by themselves).")}
      </p>
      <Table>
        <thead>
          <tr>
            <th>{t("Server")}</th>
            <th>{t("Mirror")}</th>
            <th>{t("Daemon proxy")}</th>
            <th className="!text-right">{t("Actions")}</th>
          </tr>
        </thead>
        <tbody>
          {(servers.data ?? []).map((s) => (
            <DaemonRow key={s.id} srv={s} />
          ))}
        </tbody>
      </Table>
    </div>
  );
}

function DaemonRow({ srv }: { srv: Server }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const st = useQuery({ queryKey: ["docker-network", srv.id], queryFn: () => api.get<DaemonNetwork>(`/servers/${srv.id}/docker-network`), retry: false });
  const apply = useMutation({
    mutationFn: (mode: "reload" | "restart") => api.post<{ done: string; warning?: string }>(`/servers/${srv.id}/docker-network`, { mode }),
    onSuccess: (r, mode) => {
      if (r.warning) toast.warning(tMaybe(r.warning), { description: r.done });
      else toast.success(mode === "restart" ? t("Docker is restarting on {name}", { name: srv.name }) : t("Applied on {name}", { name: srv.name }), { description: r.done });
      setTimeout(() => qc.invalidateQueries({ queryKey: ["docker-network", srv.id] }), mode === "restart" ? 8000 : 500);
    },
    onError: (e) => toast.error(e.message),
  });
  const d = st.data;
  return (
    <tr>
      <td className="whitespace-nowrap">
        <div className="flex items-center gap-2 text-[13px] font-medium">
          <StatusDot status={srv.status === "reachable" ? "running" : "failed"} />
          {srv.name}
        </div>
      </td>
      <td className="text-xs">
        {st.isLoading ? "…" : st.error ? <span className="text-danger">{st.error.message}</span> : d ? (
          <div className="flex flex-wrap items-center gap-1.5">
            {d.mirrors.length ? <code className="font-mono">{d.mirrors[0]}</code> : <span className="text-faint">{t("none")}</span>}
            {d.want_mirror || d.mirrors.length ? d.mirror_ok ? <Badge tone="success">{t("up to date")}</Badge> : <Badge tone="warning">{t("not applied")}</Badge> : null}
          </div>
        ) : null}
      </td>
      <td className="text-xs">
        {d && (
          <div className="flex flex-wrap items-center gap-1.5">
            {d.https_proxy || d.http_proxy ? <code className="font-mono">{d.https_proxy || d.http_proxy}</code> : <span className="text-faint">{t("none")}</span>}
            {d.want_proxy && (d.proxy_ok ? <Badge tone="success">{t("up to date")}</Badge> : <Badge tone="warning">{d.systemd ? t("restart Docker") : t("no systemd")}</Badge>)}
          </div>
        )}
      </td>
      <td>
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="ghost" icon={<RefreshCw className="size-3.5" />} loading={apply.isPending && apply.variables === "reload"} disabled={!d} onClick={() => apply.mutate("reload")}>
            {t("Apply")}
          </Button>
          {d?.systemd && (
            <Button
              size="sm"
              variant="ghost"
              icon={<RotateCw className="size-3.5" />}
              loading={apply.isPending && apply.variables === "restart"}
              onClick={async () => {
                if (
                  await confirm({
                    title: t("Restart Docker on {name}?", { name: srv.name }),
                    description: t("Every container of the server stops for a few seconds and starts again. If PortDock runs on this server, the page reconnects by itself."),
                    danger: true,
                    confirmText: t("Restart Docker"),
                  })
                )
                  apply.mutate("restart");
              }}
            >
              {t("Restart Docker")}
            </Button>
          )}
        </div>
      </td>
    </tr>
  );
}
