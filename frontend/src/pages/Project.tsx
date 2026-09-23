import { useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, FolderKanban, Lock, MoreHorizontal, Pencil, Plus, Search, Shield, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { EnvironmentResources, Project } from "../lib/types";
import { Button, Empty, ErrorBox, Field, Input, Loading, Menu, MenuItem, MenuSeparator, Modal, PageHeader, SegmentedControl, StatusBadge, Table, Tabs, useConfirm } from "../components/ui";
import { ResourceIcon } from "../components/shared";
import { ProtectedBadge, RoleBadge } from "../components/access";
import { EnvironmentSettingsModal, MembersModal } from "../components/projectAccess";
import { domainURL, splitList, timeAgo } from "../lib/utils";
import { t, tk } from "../lib/i18n";

type Kind = "application" | "database" | "service";

const kindLabel: Record<Kind, string> = { application: tk("Application"), database: tk("Database"), service: tk("Service") };

export function ProjectPage() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const project = useQuery({ queryKey: ["project", Number(id)], queryFn: () => api.get<Project>(`/projects/${id}`) });
  const envs = project.data?.environments ?? [];
  const envId = Number(params.get("env")) || envs[0]?.id;
  const resources = useQuery({
    queryKey: ["env-resources", envId],
    queryFn: () => api.get<EnvironmentResources>(`/environments/${envId}/resources`),
    enabled: !!envId,
  });
  const [newEnv, setNewEnv] = useState<string | null>(null);
  const [edit, setEdit] = useState<{ name: string; description: string } | null>(null);
  const [kind, setKind] = useState<"all" | Kind>("all");
  const [membersOpen, setMembersOpen] = useState(false);
  const [envSettings, setEnvSettings] = useState<number | null>(null);
  const [q, setQ] = useState("");

  const createEnv = useMutation({
    mutationFn: (name: string) => api.post<{ id: number }>(`/projects/${id}/environments`, { name }),
    onSuccess: (e) => {
      qc.invalidateQueries({ queryKey: ["project", Number(id)] });
      setNewEnv(null);
      setParams({ env: String(e.id) });
    },
    onError: (e) => toast.error(e.message),
  });
  const deleteEnv = useMutation({
    mutationFn: (eid: number) => api.del(`/environments/${eid}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["project", Number(id)] });
      setParams({});
    },
    onError: (e) => toast.error(e.message),
  });
  const updateProject = useMutation({
    mutationFn: () => api.patch(`/projects/${id}`, { ...edit, default_role: project.data?.default_role }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["project", Number(id)] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      setEdit(null);
      toast.success(t("Project updated"));
    },
    onError: (e) => toast.error(e.message),
  });
  const deleteProject = useMutation({
    mutationFn: () => api.del(`/projects/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      nav("/projects");
    },
    onError: (e) => toast.error(e.message),
  });

  const r = resources.data;
  const items = useMemo(() => {
    if (!r) return [];
    const all = [
      ...r.applications.map((a) => ({ kind: "application" as Kind, id: a.id, name: a.name, status: a.status, source: a.source_type === "image" ? a.docker_image : a.git_repository.replace(/^https?:\/\//, ""), detail: a.source_type === "image" ? t("Docker image") : `${a.build_pack} · ${a.git_branch}`, domains: splitList(a.domains), updated: a.updated_at, to: `/applications/${a.id}` })),
      ...r.databases.map((d) => ({ kind: "database" as Kind, id: d.id, name: d.name, status: d.status, source: d.image, detail: d.engine, domains: [] as string[], updated: d.created_at, to: `/databases/${d.id}` })),
      ...r.services.map((s) => ({ kind: "service" as Kind, id: s.id, name: s.name, status: s.status, source: s.template === "custom" ? "Docker Compose" : s.template, detail: t("Service stack"), domains: splitList(s.domain), updated: s.created_at, to: `/services/${s.id}` })),
    ];
    return all.filter((i) => (kind === "all" || i.kind === kind) && `${i.name} ${i.source} ${i.domains.join(" ")}`.toLowerCase().includes(q.toLowerCase()));
  }, [r, kind, q]);

  if (project.isLoading) return <Loading />;
  if (project.error) return <ErrorBox error={project.error} />;
  const p = project.data!;
  const env = envs.find((e) => e.id === envId);
  const total = r ? r.applications.length + r.databases.length + r.services.length : 0;
  const canManage = p.role === "admin" || p.role === "maintainer";
  const canWrite = canManage || (p.role === "developer" && !env?.protected);

  return (
    <>
      <PageHeader
        icon={<FolderKanban />}
        title={p.name}
        subtitle={p.description || t("No description")}
        meta={<RoleBadge role={p.role} />}
        breadcrumbs={
          <>
            <Link to="/projects">{t("Projects")}</Link>
            <span className="text-faint">/</span>
            <span className="text-fg">{p.name}</span>
          </>
        }
        actions={
          <>
            {canManage && (
              <Button icon={<Users />} onClick={() => setMembersOpen(true)}>
                {t("Access")}
              </Button>
            )}
            {canManage && (
            <Menu
              trigger={
                <Button size="icon" aria-label={t("Project actions")}>
                  <MoreHorizontal />
                </Button>
              }
            >
              <MenuItem icon={<Pencil />} onClick={() => setEdit({ name: p.name, description: p.description })}>
                {t("Edit project")}
              </MenuItem>
              <MenuItem icon={<Shield />} onClick={() => setEnvSettings(envId)}>
                {t("Protect “{name}”", { name: env?.name ?? "" })}
              </MenuItem>
              <MenuItem icon={<Plus />} onClick={() => setNewEnv("")}>
                {t("New environment")}
              </MenuItem>
              {envs.length > 1 && (
                <MenuItem
                  icon={<Trash2 />}
                  danger
                  onClick={async () => {
                    if (await confirm({ title: t("Delete environment “{name}”?", { name: env?.name ?? "" }), description: t("The environment must be empty."), danger: true, confirmText: t("Delete environment") })) deleteEnv.mutate(envId);
                  }}
                >
                  {t("Delete environment")}
                </MenuItem>
              )}
              <MenuSeparator />
              <MenuItem
                icon={<Trash2 />}
                danger
                onClick={async () => {
                  if (await confirm({ title: t("Delete project “{name}”?", { name: p.name }), description: t("All environments must be empty. This cannot be undone."), danger: true, confirmText: t("Delete project"), typeToConfirm: p.name })) deleteProject.mutate();
                }}
              >
                {t("Delete project")}
              </MenuItem>
            </Menu>
            )}
            {canWrite && (
              <Link to={`/projects/${p.id}/new?env=${envId}`}>
                <Button variant="primary" icon={<Plus />}>
                  {t("Add resource")}
                </Button>
              </Link>
            )}
          </>
        }
      />

      <Tabs
        value={String(envId)}
        onChange={(v) => (v === "new" ? setNewEnv("") : setParams({ env: v }))}
        tabs={[
          ...envs.map((e) => ({ id: String(e.id), label: e.protected ? <span className="flex items-center gap-1.5">{e.name}<Lock className="size-3 text-warning" /></span> : e.name })),
          ...(canManage ? [{ id: "new", label: <span className="flex items-center gap-1 text-faint"><Plus className="size-3.5" />{t("Environment")}</span> }] : []),
        ]}
      />
      {env?.protected && (
        <div className="mb-4 flex flex-wrap items-center gap-2 text-xs text-muted">
          <ProtectedBadge approvals={env.require_approval} />
          {env.require_approval ? t("Every deployment needs approval from a maintainer other than the requester.") : t("Only maintainers change this environment; developer deployments need approval.")}
          {canManage && (
            <button className="font-medium text-fg underline-offset-2 hover:underline" onClick={() => setEnvSettings(env.id)}>
              {t("Settings")}
            </button>
          )}
        </div>
      )}

      {resources.isLoading ? (
        <Loading />
      ) : total === 0 ? (
        <Empty
          icon={<ResourceIcon kind="application" />}
          title={env?.name ? t("{name} is empty", { name: env.name }) : t("This environment is empty")}
          description={t("Deploy an application from Git or a Docker image, provision a database, or launch a service from the catalog.")}
          action={
            canWrite && (
              <Link to={`/projects/${p.id}/new?env=${envId}`}>
                <Button variant="primary" icon={<Plus />}>
                  {t("Add resource")}
                </Button>
              </Link>
            )
          }
        />
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <div className="relative w-full max-w-xs">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
              <Input className="pl-8" placeholder={t("Filter resources…")} value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
            <SegmentedControl
              value={kind}
              onChange={setKind}
              options={[
                { value: "all", label: t("All {count}", { count: total }) },
                { value: "application", label: t("Apps {count}", { count: r!.applications.length }) },
                { value: "database", label: t("Databases {count}", { count: r!.databases.length }) },
                { value: "service", label: t("Services {count}", { count: r!.services.length }) },
              ]}
            />
          </div>
          <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
            <Table>
              <thead>
                <tr>
                  <th>{t("Resource")}</th>
                  <th className="w-32">{t("Status")}</th>
                  <th>{t("Source")}</th>
                  <th>{t("Domains")}</th>
                  <th className="w-28 !text-right">{t("Updated")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((it) => (
                  <tr key={`${it.kind}-${it.id}`} className="cursor-pointer" onClick={() => nav(it.to)}>
                    <td>
                      <div className="flex items-center gap-3">
                        <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-border bg-surface-2 text-muted">
                          <ResourceIcon kind={it.kind} />
                        </span>
                        <div className="min-w-0">
                          <div className="truncate font-medium">{it.name}</div>
                          <div className="truncate text-xs text-subtle">
                            {t(kindLabel[it.kind])} · {it.detail}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td>
                      <StatusBadge status={it.status} />
                    </td>
                    <td className="max-w-64">
                      <span className="block truncate font-mono text-xs text-muted">{it.source}</span>
                    </td>
                    <td className="max-w-64">
                      {it.domains.length ? (
                        <a href={domainURL(it.domains[0])} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="inline-flex max-w-full items-center gap-1 truncate text-muted hover:text-fg">
                          <span className="truncate">{it.domains[0].replace(/^https?:\/\//, "")}</span>
                          <ExternalLink className="size-3 shrink-0 text-faint" />
                          {it.domains.length > 1 && <span className="text-xs text-faint">+{it.domains.length - 1}</span>}
                        </a>
                      ) : (
                        <span className="text-faint">—</span>
                      )}
                    </td>
                    <td className="text-right text-subtle">{timeAgo(it.updated)}</td>
                  </tr>
                ))}
              </tbody>
            </Table>
            {items.length === 0 && <div className="border-t border-border px-4 py-10 text-center text-[13px] text-subtle">{t("No resources match your filters.")}</div>}
          </div>
        </>
      )}

      <Modal
        open={newEnv !== null}
        onClose={() => setNewEnv(null)}
        title={t("New environment")}
        description={t("Environments isolate resources and configuration, e.g. staging or qa.")}
        size="sm"
        footer={
          <>
            <Button onClick={() => setNewEnv(null)}>{t("Cancel")}</Button>
            <Button variant="primary" loading={createEnv.isPending} disabled={!newEnv} onClick={() => createEnv.mutate(newEnv!)}>
              {t("Create environment")}
            </Button>
          </>
        }
      >
        <Field label={t("Environment name")} required>
          <Input autoFocus value={newEnv ?? ""} onChange={(e) => setNewEnv(e.target.value)} placeholder="staging" />
        </Field>
      </Modal>
      <MembersModal projectId={p.id} open={membersOpen} onClose={() => setMembersOpen(false)} />
      <EnvironmentSettingsModal env={envs.find((e) => e.id === envSettings) ?? null} onClose={() => setEnvSettings(null)} />
      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        title={t("Edit project")}
        footer={
          <>
            <Button onClick={() => setEdit(null)}>{t("Cancel")}</Button>
            <Button variant="primary" loading={updateProject.isPending} onClick={() => updateProject.mutate()}>
              {t("Save changes")}
            </Button>
          </>
        }
      >
        {edit && (
          <div className="space-y-4">
            <Field label={t("Project name")} required>
              <Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </Field>
            <Field label={t("Description")}>
              <Input value={edit.description} onChange={(e) => setEdit({ ...edit, description: e.target.value })} />
            </Field>
          </div>
        )}
      </Modal>
    </>
  );
}
