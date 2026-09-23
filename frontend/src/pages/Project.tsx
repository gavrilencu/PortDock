import { useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, FolderKanban, MoreHorizontal, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { EnvironmentResources, Project } from "../lib/types";
import { Button, Empty, ErrorBox, Field, Input, Loading, Menu, MenuItem, MenuSeparator, Modal, PageHeader, SegmentedControl, StatusBadge, Table, Tabs, useConfirm } from "../components/ui";
import { ResourceIcon } from "../components/shared";
import { domainURL, splitList, timeAgo } from "../lib/utils";

type Kind = "application" | "database" | "service";

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
    mutationFn: () => api.patch(`/projects/${id}`, edit),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["project", Number(id)] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      setEdit(null);
      toast.success("Project updated");
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
      ...r.applications.map((a) => ({ kind: "application" as Kind, id: a.id, name: a.name, status: a.status, source: a.source_type === "image" ? a.docker_image : a.git_repository.replace(/^https?:\/\//, ""), detail: a.source_type === "image" ? "Docker image" : `${a.build_pack} · ${a.git_branch}`, domains: splitList(a.domains), updated: a.updated_at, to: `/applications/${a.id}` })),
      ...r.databases.map((d) => ({ kind: "database" as Kind, id: d.id, name: d.name, status: d.status, source: d.image, detail: d.engine, domains: [] as string[], updated: d.created_at, to: `/databases/${d.id}` })),
      ...r.services.map((s) => ({ kind: "service" as Kind, id: s.id, name: s.name, status: s.status, source: s.template === "custom" ? "Docker Compose" : s.template, detail: "Service stack", domains: splitList(s.domain), updated: s.created_at, to: `/services/${s.id}` })),
    ];
    return all.filter((i) => (kind === "all" || i.kind === kind) && `${i.name} ${i.source} ${i.domains.join(" ")}`.toLowerCase().includes(q.toLowerCase()));
  }, [r, kind, q]);

  if (project.isLoading) return <Loading />;
  if (project.error) return <ErrorBox error={project.error} />;
  const p = project.data!;
  const env = envs.find((e) => e.id === envId);
  const total = r ? r.applications.length + r.databases.length + r.services.length : 0;

  return (
    <>
      <PageHeader
        icon={<FolderKanban />}
        title={p.name}
        subtitle={p.description || "No description"}
        breadcrumbs={
          <>
            <Link to="/projects">Projects</Link>
            <span className="text-faint">/</span>
            <span className="text-fg">{p.name}</span>
          </>
        }
        actions={
          <>
            <Menu
              trigger={
                <Button size="icon" aria-label="Project actions">
                  <MoreHorizontal />
                </Button>
              }
            >
              <MenuItem icon={<Pencil />} onClick={() => setEdit({ name: p.name, description: p.description })}>
                Edit project
              </MenuItem>
              <MenuItem icon={<Plus />} onClick={() => setNewEnv("")}>
                New environment
              </MenuItem>
              {envs.length > 1 && (
                <MenuItem
                  icon={<Trash2 />}
                  danger
                  onClick={async () => {
                    if (await confirm({ title: `Delete environment “${env?.name}”?`, description: "The environment must be empty.", danger: true, confirmText: "Delete environment" })) deleteEnv.mutate(envId);
                  }}
                >
                  Delete environment
                </MenuItem>
              )}
              <MenuSeparator />
              <MenuItem
                icon={<Trash2 />}
                danger
                onClick={async () => {
                  if (await confirm({ title: `Delete project “${p.name}”?`, description: "All environments must be empty. This cannot be undone.", danger: true, confirmText: "Delete project", typeToConfirm: p.name })) deleteProject.mutate();
                }}
              >
                Delete project
              </MenuItem>
            </Menu>
            <Link to={`/projects/${p.id}/new?env=${envId}`}>
              <Button variant="primary" icon={<Plus />}>
                Add resource
              </Button>
            </Link>
          </>
        }
      />

      <Tabs
        value={String(envId)}
        onChange={(v) => (v === "new" ? setNewEnv("") : setParams({ env: v }))}
        tabs={[...envs.map((e) => ({ id: String(e.id), label: e.name })), { id: "new", label: <span className="flex items-center gap-1 text-faint"><Plus className="size-3.5" />Environment</span> }]}
      />

      {resources.isLoading ? (
        <Loading />
      ) : total === 0 ? (
        <Empty
          icon={<ResourceIcon kind="application" />}
          title={`${env?.name ?? "This environment"} is empty`}
          description="Deploy an application from Git or a Docker image, provision a database, or launch a service from the catalog."
          action={
            <Link to={`/projects/${p.id}/new?env=${envId}`}>
              <Button variant="primary" icon={<Plus />}>
                Add resource
              </Button>
            </Link>
          }
        />
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <div className="relative w-full max-w-xs">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
              <Input className="pl-8" placeholder="Filter resources…" value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
            <SegmentedControl
              value={kind}
              onChange={setKind}
              options={[
                { value: "all", label: `All ${total}` },
                { value: "application", label: `Apps ${r!.applications.length}` },
                { value: "database", label: `Databases ${r!.databases.length}` },
                { value: "service", label: `Services ${r!.services.length}` },
              ]}
            />
          </div>
          <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
            <Table>
              <thead>
                <tr>
                  <th>Resource</th>
                  <th className="w-32">Status</th>
                  <th>Source</th>
                  <th>Domains</th>
                  <th className="w-28 !text-right">Updated</th>
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
                          <div className="truncate text-xs capitalize text-subtle">
                            {it.kind} · {it.detail}
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
            {items.length === 0 && <div className="border-t border-border px-4 py-10 text-center text-[13px] text-subtle">No resources match your filters.</div>}
          </div>
        </>
      )}

      <Modal
        open={newEnv !== null}
        onClose={() => setNewEnv(null)}
        title="New environment"
        description="Environments isolate resources and configuration, e.g. staging or qa."
        size="sm"
        footer={
          <>
            <Button onClick={() => setNewEnv(null)}>Cancel</Button>
            <Button variant="primary" loading={createEnv.isPending} disabled={!newEnv} onClick={() => createEnv.mutate(newEnv!)}>
              Create environment
            </Button>
          </>
        }
      >
        <Field label="Environment name" required>
          <Input autoFocus value={newEnv ?? ""} onChange={(e) => setNewEnv(e.target.value)} placeholder="staging" />
        </Field>
      </Modal>
      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        title="Edit project"
        footer={
          <>
            <Button onClick={() => setEdit(null)}>Cancel</Button>
            <Button variant="primary" loading={updateProject.isPending} onClick={() => updateProject.mutate()}>
              Save changes
            </Button>
          </>
        }
      >
        {edit && (
          <div className="space-y-4">
            <Field label="Project name" required>
              <Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </Field>
            <Field label="Description">
              <Input value={edit.description} onChange={(e) => setEdit({ ...edit, description: e.target.value })} />
            </Field>
          </div>
        )}
      </Modal>
    </>
  );
}
