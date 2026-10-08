import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Database, FolderOpen, HardDrive, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, AppVolume, VolumesResponse, VolumeUsage } from "../lib/types";
import { t } from "../lib/i18n";
import { Badge, Button, Card, Empty, Field, Input, Loading, Menu, MenuItem, Modal, SegmentedControl, Switch, Table, useConfirm } from "./ui";
import { useUser } from "./Layout";

type VolumeForm = { id?: number; name: string; kind: "volume" | "bind"; host_path: string; mount_path: string; read_only: boolean };

/** Docker volume holding a volume's production data (see store.VolumeName). */
const dockerName = (app: ApplicationDetail, name: string) => `pd-${app.uuid}_${name}`;

export function useVolumes(appId: number) {
  return useQuery({ queryKey: ["volumes", appId], queryFn: () => api.get<VolumesResponse>(`/applications/${appId}/volumes`) });
}

export function VolumesTab({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const me = useUser();
  const isAdmin = me.role === "owner" || me.role === "admin";
  const { data, isLoading } = useVolumes(app.id);
  const usage = useQuery({ queryKey: ["volume-usage", app.id], queryFn: () => api.get<VolumeUsage[]>(`/applications/${app.id}/volumes/usage`), enabled: !!app.current_image });
  const [form, setForm] = useState<VolumeForm | null>(null);
  const canWrite = app.access.can_write;
  const canManage = app.access.can_manage;

  const onResult = (r: VolumesResponse) => {
    qc.setQueryData(["volumes", app.id], r);
    qc.invalidateQueries({ queryKey: ["volume-usage", app.id] });
  };
  const applyHint = (r: VolumesResponse) => (r.requires_deploy ? t("Redeploy to apply the change.") : t("It is mounted on the first deployment."));

  const save = useMutation({
    mutationFn: (f: VolumeForm) => {
      const body = { name: f.name, mount_path: f.mount_path, host_path: f.kind === "bind" ? f.host_path : "", read_only: f.read_only };
      return f.id ? api.patch<VolumesResponse>(`/volumes/${f.id}`, body) : api.post<VolumesResponse>(`/applications/${app.id}/volumes`, body);
    },
    onSuccess: (r, f) => {
      onResult(r);
      setForm(null);
      toast.success(f.id ? t("Volume updated") : t("Volume added"), { description: applyHint(r) });
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: ({ id, data }: { id: number; data: boolean }) => api.del<VolumesResponse>(`/volumes/${id}?data=${data}`),
    onSuccess: (r, v) => {
      onResult(r);
      toast.success(v.data ? t("Volume and its data deleted") : t("Volume removed"), { description: v.data ? undefined : applyHint(r) });
    },
    onError: (e) => toast.error(e.message),
  });
  const purge = useMutation({
    mutationFn: (name: string) => api.del(`/applications/${app.id}/volume-data/${encodeURIComponent(name)}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["volume-usage", app.id] });
      toast.success(t("Data deleted"));
    },
    onError: (e) => toast.error(e.message),
  });

  if (app.build_pack === "compose") {
    return (
      <Card>
        <div className="p-4">
          <Empty compact icon={<HardDrive />} title={t("Volumes come from the compose file")} description={t("Docker Compose applications declare their volumes in docker-compose.yml; they are kept across deployments.")} />
        </div>
      </Card>
    );
  }
  if (isLoading) return <Loading />;
  const volumes = data!.volumes;
  const servers = usage.data ?? [];
  const serversWith = (name: string) => servers.filter((s) => s.volumes.includes(name));
  const configured = new Set(volumes.filter((v) => !v.host_path).map((v) => v.name));
  const leftovers = [...new Set(servers.flatMap((s) => s.volumes))].filter((n) => !configured.has(n)).sort();
  const openNew = () => setForm({ name: "", kind: "volume", host_path: "", mount_path: "", read_only: false });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold tracking-[-0.01em]">{t("Persistent storage")}</h2>
          <p className="text-[13px] text-subtle">{t("Files written to these paths survive redeploys, restarts and rollbacks.")}</p>
        </div>
        {canWrite && (
          <Button variant="primary" icon={<Plus />} onClick={openNew}>
            {t("Add volume")}
          </Button>
        )}
      </div>

      <Card>
        {volumes.length === 0 ? (
          <div className="p-4">
            <Empty
              compact
              icon={<HardDrive />}
              title={t("No persistent storage")}
              description={t("Without a volume, everything the application writes is lost on the next deployment. Add one for uploads, SQLite databases or caches.")}
              action={
                canWrite && (
                  <Button variant="primary" icon={<Plus />} onClick={openNew}>
                    {t("Add volume")}
                  </Button>
                )
              }
            />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Name")}</th>
                <th>{t("Mount path")}</th>
                <th>{t("Source")}</th>
                <th>{t("Access")}</th>
                <th>{t("On servers")}</th>
                {canWrite && <th className="!text-right">{t("Actions")}</th>}
              </tr>
            </thead>
            <tbody>
              {volumes.map((v) => (
                <tr key={v.id}>
                  <td>
                    <div className="flex items-center gap-2 font-medium">
                      {v.host_path ? <FolderOpen className="size-4 shrink-0 text-faint" /> : <HardDrive className="size-4 shrink-0 text-faint" />}
                      {v.name}
                    </div>
                  </td>
                  <td className="font-mono text-[13px]">{v.mount_path}</td>
                  <td>
                    {v.host_path ? (
                      <span className="flex flex-col">
                        <span className="text-muted">{t("Server directory")}</span>
                        <span className="font-mono text-xs text-subtle">{v.host_path}</span>
                      </span>
                    ) : (
                      <span className="flex flex-col">
                        <span className="text-muted">{t("Docker volume")}</span>
                        <span className="font-mono text-xs text-subtle">{dockerName(app, v.name)}</span>
                      </span>
                    )}
                  </td>
                  <td>{v.read_only ? <Badge tone="info">{t("Read-only")}</Badge> : <Badge>{t("Read-write")}</Badge>}</td>
                  <td>
                    <ServerPresence volume={v} servers={servers} present={serversWith(v.name)} loading={usage.isLoading} deployed={!!app.current_image} />
                  </td>
                  {canWrite && (
                    <td>
                      <div className="flex justify-end gap-0.5">
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          title={t("Edit")}
                          onClick={() => setForm({ id: v.id, name: v.name, kind: v.host_path ? "bind" : "volume", host_path: v.host_path, mount_path: v.mount_path, read_only: v.read_only })}
                        >
                          <Pencil />
                        </Button>
                        <Menu
                          trigger={
                            <Button size="icon-sm" variant="ghost" title={t("Remove")} loading={remove.isPending && remove.variables?.id === v.id}>
                              <Trash2 className="text-danger" />
                            </Button>
                          }
                        >
                          <MenuItem
                            icon={<Trash2 />}
                            onClick={async () => {
                              if (
                                await confirm({
                                  title: t("Remove {name} from the application?", { name: v.name }),
                                  description: v.host_path ? t("The directory stays on the server; it is no longer mounted after the next deployment.") : t("The data stays on the servers; the volume is no longer mounted after the next deployment."),
                                  confirmText: t("Remove volume"),
                                })
                              )
                                remove.mutate({ id: v.id, data: false });
                            }}
                          >
                            {t("Remove, keep data")}
                          </MenuItem>
                          {!v.host_path && canManage && (
                            <MenuItem
                              danger
                              icon={<Database />}
                              onClick={async () => {
                                if (
                                  await confirm({
                                    title: t("Delete volume {name} and ALL its data?", { name: v.name }),
                                    description: t("The Docker volume is deleted on every server, including the copies of pull request previews. This cannot be undone."),
                                    danger: true,
                                    confirmText: t("Delete everything"),
                                    typeToConfirm: v.name,
                                  })
                                )
                                  remove.mutate({ id: v.id, data: true });
                              }}
                            >
                              {t("Delete with data")}
                            </MenuItem>
                          )}
                        </Menu>
                      </div>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {volumes.length > 0 && (
        <ul className="list-disc space-y-1 pl-5 text-[13px] text-muted">
          <li>{t("Each server of the application keeps its own copy of a Docker volume; data is not synchronized between servers.")}</li>
          <li>{t("Replicas on a server, and the old and new version during a deployment, use the same volume at the same time.")}</li>
          {app.source_type === "git" && <li>{t("Pull request previews get their own empty volumes; server directories are not mounted in previews.")}</li>}
        </ul>
      )}

      {leftovers.length > 0 && (
        <Card title={t("Leftover data")} description={t("Docker volumes of this application that are no longer mounted. Their data is kept until you delete it.")}>
          <Table>
            <tbody>
              {leftovers.map((n) => (
                <tr key={n}>
                  <td className="font-mono text-[13px]">{dockerName(app, n)}</td>
                  <td className="text-muted">{serversWith(n).map((s) => s.server_name).join(", ")}</td>
                  {canManage && (
                    <td className="text-right">
                      <Button
                        size="sm"
                        variant="danger"
                        icon={<Trash2 className="size-3.5" />}
                        loading={purge.isPending && purge.variables === n}
                        onClick={async () => {
                          if (await confirm({ title: t("Delete the data of {name}?", { name: n }), description: t("The Docker volume is deleted on every server. This cannot be undone."), danger: true, confirmText: t("Delete data"), typeToConfirm: n }))
                            purge.mutate(n);
                        }}
                      >
                        {t("Delete data")}
                      </Button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}

      <VolumeModal form={form} onChange={setForm} onSave={() => form && save.mutate(form)} saving={save.isPending} isAdmin={isAdmin} />
    </div>
  );
}

function ServerPresence({ volume, servers, present, loading, deployed }: { volume: AppVolume; servers: VolumeUsage[]; present: VolumeUsage[]; loading: boolean; deployed: boolean }) {
  if (volume.host_path) return <span className="text-muted">{t("Mounted from each server")}</span>;
  if (!deployed) return <span className="text-subtle">{t("Created on the first deployment")}</span>;
  if (loading) return <span className="text-subtle">…</span>;
  if (present.length === 0) return <span className="text-subtle">{t("Created on the next deployment")}</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {servers.map((s) =>
        s.error ? (
          <Badge key={s.server_id} tone="warning">
            <span title={s.error}>{s.server_name}</span>
          </Badge>
        ) : (
          <Badge key={s.server_id} tone={present.includes(s) ? "success" : "neutral"}>
            {s.server_name}
          </Badge>
        ),
      )}
    </div>
  );
}

function VolumeModal({ form, onChange, onSave, saving, isAdmin }: { form: VolumeForm | null; onChange: (f: VolumeForm | null) => void; onSave: () => void; saving: boolean; isAdmin: boolean }) {
  const set = (patch: Partial<VolumeForm>) => form && onChange({ ...form, ...patch });
  // Members keep a server directory an administrator configured, but cannot choose one.
  const bindLocked = !isAdmin && form?.kind !== "bind";
  const renameLocked = !!form?.id && form.kind === "volume";
  const valid = !!form && !!form.name.trim() && form.mount_path.startsWith("/") && (form.kind === "volume" || form.host_path.startsWith("/"));
  return (
    <Modal
      open={!!form}
      onClose={() => onChange(null)}
      title={form?.id ? t("Edit volume") : t("Add volume")}
      description={t("Changes apply on the next deployment.")}
      footer={
        <>
          <Button onClick={() => onChange(null)}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!valid} onClick={onSave}>
            {form?.id ? t("Save changes") : t("Add volume")}
          </Button>
        </>
      }
    >
      {form && (
        <form
          className="space-y-5"
          onSubmit={(e) => {
            e.preventDefault();
            if (valid) onSave();
          }}
        >
          {!form.id && (
            <Field label={t("Source")}>
              <SegmentedControl
                value={form.kind}
                onChange={(kind) => !(kind === "bind" && bindLocked) && set({ kind })}
                options={[
                  { value: "volume", label: t("Docker volume") },
                  { value: "bind", label: <span title={bindLocked ? t("Only administrators can mount directories of the server") : undefined} className={bindLocked ? "opacity-50" : undefined}>{t("Server directory")}</span> },
                ]}
              />
            </Field>
          )}
          <Field
            label={t("Name")}
            required
            hint={renameLocked ? t("The data is stored under this name, so it cannot be changed.") : t("Lowercase letters, digits, '.' and '-', e.g. data or uploads.")}
          >
            <Input autoFocus={!form.id} value={form.name} disabled={renameLocked} onChange={(e) => set({ name: e.target.value })} placeholder="data" />
          </Field>
          {form.kind === "bind" && (
            <Field label={t("Server directory")} required hint={t("Absolute path on every server of the application. System directories and PortDock's data cannot be mounted.")}>
              <Input value={form.host_path} disabled={!isAdmin} onChange={(e) => set({ host_path: e.target.value })} placeholder="/srv/media" className="font-mono" />
            </Field>
          )}
          <Field label={t("Mount path")} required hint={t("Absolute path inside the container where the application reads and writes the data.")}>
            <Input value={form.mount_path} onChange={(e) => set({ mount_path: e.target.value })} placeholder="/app/data" className="font-mono" />
          </Field>
          <div className="rounded-lg border border-border bg-surface-2/40 p-4">
            <Switch checked={form.read_only} onChange={(v) => set({ read_only: v })} label={t("Read-only")} description={t("The application can read the files but not change them.")} />
          </div>
        </form>
      )}
    </Modal>
  );
}
