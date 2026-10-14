import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, CheckCircle2, Download, Info, XCircle } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { t } from "../lib/i18n";
import { Badge, Button, Card, Field, Input, PasswordInput, Select, Switch } from "./ui";
import { ResourceIcon, useServers } from "./shared";

// Settings → Import: applications, databases and services from Coolify,
// Heroku or Dokku.

type Source = "coolify" | "heroku" | "dokku";

interface PlanVar {
  key: string;
  value: string;
  is_build: boolean;
  is_preview: boolean;
  is_secret: boolean;
}

interface PlanApp {
  ref: string;
  name: string;
  project: string;
  environment: string;
  source_type: string;
  git_repository?: string;
  git_branch?: string;
  docker_image?: string;
  build_pack: string;
  port: number;
  domains: string[];
  replicas: number;
  env: PlanVar[];
  volumes: { name: string; path: string }[];
  jobs: { name: string; schedule: string; command: string }[];
  notes: string[];
}

interface PlanDatabase {
  ref: string;
  name: string;
  project: string;
  environment: string;
  engine: string;
  image?: string;
  notes: string[];
}

interface PlanService {
  ref: string;
  name: string;
  project: string;
  environment: string;
  domain?: string;
  env: PlanVar[];
  notes: string[];
}

interface Plan {
  source: Source;
  apps: PlanApp[];
  databases: PlanDatabase[];
  services: PlanService[];
  warnings: string[];
}

interface Result {
  kind: "app" | "database" | "service";
  ref: string;
  name: string;
  id?: number;
  error?: string;
  notes?: string[];
}

const sources: { id: Source; name: string; mark: string; color: string; hint: string }[] = [
  { id: "coolify", name: "Coolify", mark: "CO", color: "bg-[#6b16ed] text-white", hint: "Projects, applications, databases and services, through Coolify's API (v4)." },
  { id: "heroku", name: "Heroku", mark: "HK", color: "bg-[#430098] text-white", hint: "Applications with their config vars, domains, dynos and Postgres / Redis add-ons." },
  { id: "dokku", name: "Dokku", mark: "DK", color: "bg-[#2f3439] text-white", hint: "Applications of a Dokku server PortDock manages, from the images Dokku built." },
];

const key = (kind: string, ref: string) => `${kind}:${ref}`;

function Notes({ notes }: { notes: string[] }) {
  if (!notes.length) return null;
  return (
    <ul className="mt-1.5 space-y-0.5 text-2xs text-muted">
      {notes.map((n) => (
        <li key={n} className="flex gap-1.5">
          <Info className="mt-0.5 size-3 shrink-0" />
          {n}
        </li>
      ))}
    </ul>
  );
}

export function ImportSettings() {
  const servers = useServers().data ?? [];
  const [source, setSource] = useState<Source | null>(null);
  const [cred, setCred] = useState({ url: "", token: "", server_id: 0 });
  const [plan, setPlan] = useState<Plan | null>(null);
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [target, setTarget] = useState(0);
  const [deploy, setDeploy] = useState(false);
  const [project, setProject] = useState("");
  const [results, setResults] = useState<Result[] | null>(null);
  const src = sources.find((s) => s.id === source);
  const body = () => ({ source, url: cred.url, token: cred.token, server_id: cred.server_id || servers[0]?.id });

  const read = useMutation({
    mutationFn: () => api.post<Plan>("/migrations/plan", body()),
    onSuccess: (p) => {
      setPlan(p);
      setChosen(new Set([...p.apps.map((a) => key("app", a.ref)), ...p.databases.map((d) => key("database", d.ref)), ...p.services.map((s) => key("service", s.ref))]));
      setTarget(servers[0]?.id ?? 0);
    },
    onError: (e) => toast.error(e.message),
  });
  const run = useMutation({
    mutationFn: () =>
      api.post<{ results: Result[] }>("/migrations/apply", {
        ...body(),
        target_server_id: target,
        deploy,
        items: [...chosen].map((k) => {
          const [kind, ...rest] = k.split(":");
          return { kind, ref: rest.join(":"), project: project.trim() };
        }),
      }),
    onSuccess: (r) => {
      setResults(r.results);
      const failed = r.results.filter((x) => x.error).length;
      if (failed) toast.error(t("{n} item could not be imported|{n} items could not be imported", { n: failed }));
      else toast.success(t("Import finished"));
    },
    onError: (e) => toast.error(e.message),
  });

  const toggle = (k: string) => {
    const next = new Set(chosen);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    setChosen(next);
  };
  const total = useMemo(() => (plan ? plan.apps.length + plan.databases.length + plan.services.length : 0), [plan]);
  const reset = () => (setPlan(null), setResults(null));

  if (results) {
    const href = (r: Result) => (r.kind === "app" ? `/applications/${r.id}` : r.kind === "database" ? `/databases/${r.id}` : `/services/${r.id}`);
    return (
      <Card
        title={t("Import results")}
        description={deploy ? t("The imported applications are deploying.") : t("Nothing was deployed: check each resource, then deploy it.")}
        actions={
          <Button size="sm" onClick={() => (setResults(null), setPlan(null), setSource(null))}>
            {t("Import more")}
          </Button>
        }
      >
        <div className="divide-y divide-border">
          {results.map((r) => (
            <div key={key(r.kind, r.ref)} className="flex items-start gap-3 px-5 py-3">
              {r.error ? <XCircle className="mt-0.5 size-4 shrink-0 text-danger" /> : <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" />}
              <div className="min-w-0 flex-1 text-[13px]">
                <div className="flex items-center gap-2">
                  <ResourceIcon kind={r.kind === "app" ? "application" : r.kind} className="size-3.5 text-muted" />
                  {r.id && !r.error ? (
                    <Link to={href(r)} className="font-medium underline-offset-2 hover:underline">
                      {r.name}
                    </Link>
                  ) : (
                    <span className="font-medium">{r.name || r.ref}</span>
                  )}
                </div>
                {r.error && <div className="mt-1 text-xs text-danger">{r.error}</div>}
                <Notes notes={r.notes ?? []} />
              </div>
            </div>
          ))}
        </div>
      </Card>
    );
  }

  if (plan) {
    const row = (kind: "app" | "database" | "service", ref: string, name: string, place: string, detail: React.ReactNode, notes: string[]) => (
      <label key={key(kind, ref)} className="flex cursor-pointer items-start gap-3 px-5 py-3 hover:bg-surface-2/40">
        <input type="checkbox" className="mt-1" checked={chosen.has(key(kind, ref))} onChange={() => toggle(key(kind, ref))} />
        <div className="min-w-0 flex-1 text-[13px]">
          <div className="flex flex-wrap items-center gap-2">
            <ResourceIcon kind={kind === "app" ? "application" : kind} className="size-3.5 text-muted" />
            <span className="font-medium">{name}</span>
            <span className="text-2xs text-faint">{project.trim() ? `${project.trim()} / ${place.split(" / ")[1]}` : place}</span>
          </div>
          <div className="mt-0.5 text-xs text-muted">{detail}</div>
          <Notes notes={notes} />
        </div>
      </label>
    );
    return (
      <div className="space-y-6">
        <Card
          title={t("What {source} has", { source: src!.name })}
          description={t("Choose what to create in PortDock. Applications keep their source, build, port, domains, variables, volumes and jobs; databases are created empty, of the same engine; nothing is copied from the volumes.")}
          actions={
            <Button size="sm" variant="ghost" icon={<ArrowLeft />} onClick={reset}>
              {t("Back")}
            </Button>
          }
        >
          {plan.warnings.length > 0 && (
            <div className="space-y-1 border-b border-border bg-warning-soft/40 px-5 py-3 text-xs">
              {plan.warnings.map((w) => (
                <div key={w} className="flex gap-2">
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />
                  {w}
                </div>
              ))}
            </div>
          )}
          {total === 0 ? (
            <p className="p-5 text-sm text-muted">{t("Nothing to import.")}</p>
          ) : (
            <div className="divide-y divide-border">
              {plan.apps.map((a) =>
                row(
                  "app",
                  a.ref,
                  a.name,
                  `${a.project} / ${a.environment}`,
                  <>
                    {a.source_type === "image" ? a.docker_image : `${a.git_repository}${a.git_branch ? ` @ ${a.git_branch}` : ""}`} · {a.build_pack} · {t("port {port}", { port: a.port })}
                    {a.replicas > 1 && ` · ×${a.replicas}`}
                    {a.domains.length > 0 && ` · ${a.domains.join(", ")}`}
                    {` · ${t("{n} variable|{n} variables", { n: a.env.length })}`}
                    {a.volumes.length > 0 && ` · ${t("{n} volume|{n} volumes", { n: a.volumes.length })}`}
                    {a.jobs.length > 0 && ` · ${t("{n} job|{n} jobs", { n: a.jobs.length })}`}
                  </>,
                  a.notes,
                ),
              )}
              {plan.databases.map((d) => row("database", d.ref, d.name, `${d.project} / ${d.environment}`, <>{d.engine}{d.image && ` · ${d.image}`}</>, d.notes))}
              {plan.services.map((s) => row("service", s.ref, s.name, `${s.project} / ${s.environment}`, <>{s.domain || t("no domain")} · {t("{n} variable|{n} variables", { n: s.env.length })}</>, s.notes))}
            </div>
          )}
        </Card>
        {total > 0 && (
          <Card title={t("Import")}>
            <div className="grid gap-4 p-5 md:grid-cols-2">
              <Field label={t("Server")} hint={t("Where the imported resources run.")}>
                <Select value={String(target)} onChange={(e) => setTarget(Number(e.target.value))}>
                  {servers.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label={t("Project")} hint={t("Empty: the project of each resource in the source, created when missing.")}>
                <Input value={project} onChange={(e) => setProject(e.target.value)} placeholder={t("as in the source")} />
              </Field>
              <div className="md:col-span-2">
                <Switch checked={deploy} onChange={setDeploy} label={t("Deploy after importing")} description={t("Off, everything is created stopped so you can check the variables and domains first.")} />
              </div>
              <div className="flex justify-end border-t border-border pt-4 md:col-span-2">
                <Button variant="primary" icon={<Download />} loading={run.isPending} disabled={chosen.size === 0 || !target} onClick={() => run.mutate()}>
                  {t("Import {n} item|Import {n} items", { n: chosen.size })}
                </Button>
              </div>
            </div>
          </Card>
        )}
      </div>
    );
  }

  return (
    <Card
      title={t("Import from another platform")}
      description={t("Bring the applications, databases and services of Coolify, Heroku or Dokku into PortDock. You see the plan before anything is created.")}
    >
      <div className="space-y-5 p-5">
        <div className="grid gap-2 sm:grid-cols-3">
          {sources.map((s) => (
            <button
              key={s.id}
              onClick={() => setSource(s.id)}
              className={`flex items-start gap-3 rounded-lg border p-3 text-left transition-colors ${source === s.id ? "border-accent bg-accent/5" : "border-border bg-surface hover:border-border-strong hover:bg-surface-2"}`}
            >
              <span className={`flex size-8 shrink-0 items-center justify-center rounded-md text-[10px] font-semibold ${s.color}`}>{s.mark}</span>
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{s.name}</div>
                <div className="text-2xs text-muted">{t(s.hint)}</div>
              </div>
            </button>
          ))}
        </div>
        {source === "coolify" && (
          <div className="grid gap-4 md:grid-cols-2">
            <Field label={t("Coolify address")}>
              <Input value={cred.url} onChange={(e) => setCred({ ...cred, url: e.target.value })} placeholder="https://coolify.example.com" className="font-mono text-xs" />
            </Field>
            <Field label={t("API token")} hint={t("Keys & Tokens → API tokens in Coolify, with read access (and access to sensitive data, for the variables).")}>
              <PasswordInput value={cred.token} onChange={(e) => setCred({ ...cred, token: e.target.value })} autoComplete="new-password" />
            </Field>
          </div>
        )}
        {source === "heroku" && (
          <Field label={t("Heroku API key")} hint={t("heroku authorizations:create, or Account settings → API key. Imported applications keep it (encrypted) to build from Heroku Git until you point them at their own repository.")}>
            <PasswordInput value={cred.token} onChange={(e) => setCred({ ...cred, token: e.target.value })} autoComplete="new-password" className="max-w-md" />
          </Field>
        )}
        {source === "dokku" && (
          <Field label={t("Dokku server")} hint={t("Add the Dokku server in Servers first; PortDock reads it with the dokku command over SSH.")}>
            <Select value={String(cred.server_id || servers[0]?.id || "")} onChange={(e) => setCred({ ...cred, server_id: Number(e.target.value) })} className="max-w-md">
              {servers.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} ({s.host})
                </option>
              ))}
            </Select>
          </Field>
        )}
        {source && (
          <div className="flex items-center justify-between gap-3 border-t border-border pt-4">
            <p className="text-xs text-muted">{t("Credentials are only used to read the source; nothing changes there.")}</p>
            <Button variant="primary" loading={read.isPending} onClick={() => read.mutate()}>
              {t("Read {source}", { source: src!.name })}
            </Button>
          </div>
        )}
        {!source && (
          <p className="flex items-center gap-2 text-xs text-muted">
            <Badge>{t("Tip")}</Badge>
            {t("Templates of Coolify's one-click services can also be added as a service catalog (Service catalogs).")}
          </p>
        )}
      </div>
    </Card>
  );
}
