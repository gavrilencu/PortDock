import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, GitCompareArrows } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, Environment, Project } from "../lib/types";
import { t } from "../lib/i18n";
import { EnvEditor } from "./EnvEditor";
import { Badge, Button, Card, Empty, Modal, SegmentedControl, Select, Table } from "./ui";

// Shared variables (organization, project, environment) and the comparison of
// variables between applications.

interface InheritedVar {
  key: string;
  value: string;
  scope: "organization" | "project" | "environment";
  is_secret: boolean;
  is_build: boolean;
  is_preview: boolean;
  overridden: boolean;
  /** The secret manager reference, e.g. "vault-prod: shop/db#password" ("" = a value). */
  ref: string;
}

const scopeLabel: Record<InheritedVar["scope"], string> = { organization: "Organization", project: "Project", environment: "Environment" };

/** InheritedVariables lists the shared variables an application or service receives. */
export function InheritedVariables({ owner, projectId }: { owner: string; projectId?: number }) {
  const q = useQuery({ queryKey: ["envs-inherited", owner], queryFn: () => api.get<InheritedVar[]>(`${owner}/envs/inherited`) });
  const vars = q.data ?? [];
  if (vars.length === 0) return null;
  return (
    <Card
      title={t("Shared variables")}
      description={t("Set for the whole organization, the project or the environment. A variable of the same name set here, or at a narrower scope, takes precedence.")}
      actions={
        projectId ? (
          <Link to={`/projects/${projectId}?variables=1`} className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline">
            {t("Edit in the project")} →
          </Link>
        ) : undefined
      }
    >
      <Table>
        <thead>
          <tr>
            <th>{t("Key")}</th>
            <th>{t("Value")}</th>
            <th>{t("Scope")}</th>
          </tr>
        </thead>
        <tbody>
          {vars.map((v) => (
            <tr key={`${v.scope}-${v.key}-${v.is_preview}`} className={v.overridden ? "opacity-50" : ""}>
              <td className="font-mono text-xs">
                <span className={v.overridden ? "line-through" : ""}>{v.key}</span>
                {v.is_preview && <Badge className="ml-2">{t("previews")}</Badge>}
              </td>
              <td className="max-w-md truncate font-mono text-xs text-muted">{v.ref ? <span title={t("Read from the secret manager when a deployment starts")}>↗ {v.ref}</span> : v.is_secret ? "••••••••" : v.value}</td>
              <td className="text-xs">
                <Badge tone={v.scope === "environment" ? "info" : v.scope === "project" ? "accent" : "neutral"}>{t(scopeLabel[v.scope])}</Badge>
                {v.overridden && <span className="ml-2 text-faint">{t("overridden")}</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

interface EnvDiff {
  key: string;
  status: "same" | "different" | "only_here" | "only_there";
  here: string;
  there: string;
  here_from: string;
  there_from: string;
  secret: boolean;
}

function From({ scope }: { scope: string }) {
  if (!scope || scope === "application") return null;
  return <div className="text-2xs text-faint">{t("from: {scope}", { scope: t(scopeLabel[scope as InheritedVar["scope"]] ?? scope) })}</div>;
}

interface Candidate {
  id: number;
  name: string;
  environment_name?: string;
  environment_id: number;
}

/** CompareVariables compares the variables of an application with another one's and copies them over. */
export function CompareVariables({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const apps = useQuery({ queryKey: ["applications"], queryFn: () => api.get<Candidate[]>("/applications") });
  const project = useQuery({ queryKey: ["project", app.project.id], queryFn: () => api.get<Project>(`/projects/${app.project.id}`) });
  const envs = useMemo(() => project.data?.environments ?? [], [project.data]);
  const envName = (id: number) => envs.find((e: Environment) => e.id === id)?.name ?? "";
  // The same application in the project's other environments first.
  const candidates = useMemo(() => {
    const inProject = (apps.data ?? []).filter((a) => a.id !== app.id && envs.some((e: Environment) => e.id === a.environment_id));
    return inProject.sort((a, b) => Number(b.name === app.name) - Number(a.name === app.name) || a.name.localeCompare(b.name));
  }, [apps.data, app.id, app.name, envs]);
  const [other, setOther] = useState<number | null>(null);
  const target = other ?? candidates[0]?.id ?? null;
  const cmp = useQuery({
    queryKey: ["envs-compare", app.id, target],
    queryFn: () => api.get<{ with: { id: number; name: string }; variables: EnvDiff[] }>(`/applications/${app.id}/envs/compare?with=${target}`),
    enabled: !!target,
  });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState<"changes" | "all">("changes");
  const promote = useMutation({
    mutationFn: () => api.post<{ copied: number }>(`/applications/${app.id}/envs/promote`, { to: String(target), keys: [...selected] }),
    onSuccess: (r) => {
      toast.success(t("{n} variable(s) copied", { n: r.copied }), { description: t("Redeploy the other application to apply them.") });
      setSelected(new Set());
      qc.invalidateQueries({ queryKey: ["envs-compare", app.id, target] });
    },
    onError: (e) => toast.error(e.message),
  });
  if (candidates.length === 0) return null;
  const rows = (cmp.data?.variables ?? []).filter((v) => filter === "all" || v.status !== "same");
  const targetApp = candidates.find((c) => c.id === target);
  const statusTone = { same: "success", different: "warning", only_here: "info", only_there: "neutral" } as const;
  const statusText = { same: t("Same"), different: t("Different"), only_here: t("Only here"), only_there: t("Only there") };
  return (
    <Card
      title={t("Compare with another environment")}
      description={t("Find what differs between, for example, staging and production, and copy variables over.")}
      actions={
        <div className="flex items-center gap-2">
          <Select
            value={String(target ?? "")}
            onChange={(e) => {
              setOther(Number(e.target.value));
              setSelected(new Set());
            }}
          >
            {candidates.map((c) => (
              <option key={c.id} value={c.id}>
                {envName(c.environment_id)} / {c.name}
              </option>
            ))}
          </Select>
        </div>
      }
    >
      <div className="flex items-center justify-between gap-3 px-5 pt-4">
        <SegmentedControl
          value={filter}
          onChange={(v) => setFilter(v as "changes" | "all")}
          options={[
            { value: "changes", label: t("Differences") },
            { value: "all", label: t("All") },
          ]}
        />
        <Button size="sm" variant="primary" icon={<ArrowRight className="size-3.5" />} disabled={selected.size === 0} loading={promote.isPending} onClick={() => promote.mutate()}>
          {t("Copy {n} to {target}", { n: selected.size, target: targetApp ? `${envName(targetApp.environment_id)} / ${targetApp.name}` : "" })}
        </Button>
      </div>
      {rows.length === 0 ? (
        <div className="p-5">
          <Empty compact icon={<GitCompareArrows />} title={t("No differences")} description={t("Both applications have the same variables.")} />
        </div>
      ) : (
        <Table className="mt-3">
          <thead>
            <tr>
              <th className="w-8" />
              <th>{t("Key")}</th>
              <th>{t("Here")}</th>
              <th>{targetApp ? envName(targetApp.environment_id) : t("There")}</th>
              <th>{t("Status")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((v) => (
              <tr key={v.key}>
                <td>
                  {v.status !== "only_there" && v.status !== "same" && (
                    <input
                      type="checkbox"
                      checked={selected.has(v.key)}
                      onChange={(e) => {
                        const next = new Set(selected);
                        if (e.target.checked) next.add(v.key);
                        else next.delete(v.key);
                        setSelected(next);
                      }}
                    />
                  )}
                </td>
                <td className="font-mono text-xs">{v.key}</td>
                <td className="max-w-xs truncate font-mono text-xs text-muted">
                  {v.status === "only_there" ? "—" : v.secret ? "••••••••" : v.here}
                  <From scope={v.here_from} />
                </td>
                <td className="max-w-xs truncate font-mono text-xs text-muted">
                  {v.status === "only_here" ? "—" : v.secret ? "••••••••" : v.there}
                  <From scope={v.there_from} />
                </td>
                <td>
                  <Badge tone={statusTone[v.status]}>{statusText[v.status]}</Badge>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

/** SharedVariablesModal edits the variables of a project and of one of its environments. */
export function SharedVariablesModal({ project, env, open, onClose }: { project: Project; env?: Environment; open: boolean; onClose: () => void }) {
  const [scope, setScope] = useState<"project" | "environment">("project");
  return (
    <Modal open={open} onClose={onClose} size="xl" title={t("Shared variables")} description={t("Every application and service of the project receives them at its next deployment. Variables set on an application take precedence.")}>
      <div className="space-y-4">
        <SegmentedControl
          value={scope}
          onChange={(v) => setScope(v as "project" | "environment")}
          options={[
            { value: "project", label: t("All environments") },
            ...(env ? [{ value: "environment", label: t("{name} only", { name: env.name }) }] : []),
          ]}
        />
        {scope === "project" ? <EnvEditor key="p" owner={`/projects/${project.id}`} supportsBuild supportsPreview /> : env && <EnvEditor key={`e${env.id}`} owner={`/environments/${env.id}`} supportsBuild supportsPreview />}
      </div>
    </Modal>
  );
}

/** OrganizationVariables edits the variables shared by every project (Settings). */
export function OrganizationVariables() {
  return (
    <Card title={t("Organization variables")} description={t("Every application and service of every project receives them; projects, environments and applications can override them. For example a log collector address or the company's proxy settings.")}>
      <div className="p-5">
        <EnvEditor owner="/organization" supportsBuild supportsPreview />
      </div>
    </Card>
  );
}
