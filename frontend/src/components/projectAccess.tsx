import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, UserPlus } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Environment, ProjectRole } from "../lib/types";
import { roleDescriptions, roleLabels } from "./access";
import { Avatar, Badge, Button, Empty, Loading, Modal, Select, Switch } from "./ui";
import { t } from "../lib/i18n";

type Member = { user_id: number; name: string; email: string; user_role: string; role: ProjectRole };
type Candidate = { id: number; name: string; email: string; role: string };

export function MembersModal({ projectId, open, onClose }: { projectId: number; open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["project-members", projectId],
    queryFn: () => api.get<{ members: Member[]; default_role: ProjectRole }>(`/projects/${projectId}/members`),
    enabled: open,
  });
  const candidates = useQuery({ queryKey: ["member-candidates", projectId], queryFn: () => api.get<Candidate[]>(`/projects/${projectId}/member-candidates`), enabled: open });
  const [members, setMembers] = useState<Member[]>([]);
  const [defaultRole, setDefaultRole] = useState<ProjectRole>("developer");
  const [pick, setPick] = useState(0);
  useEffect(() => {
    if (q.data) {
      setMembers(q.data.members);
      setDefaultRole(q.data.default_role);
    }
  }, [q.data]);

  const save = useMutation({
    mutationFn: async () => {
      await api.put(`/projects/${projectId}/members`, { members: members.map((m) => ({ user_id: m.user_id, role: m.role })) });
      if (defaultRole !== q.data?.default_role) {
        const p = await api.get<{ name: string; description: string }>(`/projects/${projectId}`);
        await api.patch(`/projects/${projectId}`, { name: p.name, description: p.description, default_role: defaultRole });
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["project-members", projectId] });
      qc.invalidateQueries({ queryKey: ["project", projectId] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      toast.success(t("Project access updated"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });

  const available = (candidates.data ?? []).filter((c) => c.role === "member" && !members.some((m) => m.user_id === c.id));
  const admins = (candidates.data ?? []).filter((c) => c.role !== "member");
  const add = () => {
    const c = available.find((x) => x.id === pick);
    if (!c) return;
    setMembers([...members, { user_id: c.id, name: c.name, email: c.email, user_role: c.role, role: "developer" }]);
    setPick(0);
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="lg"
      title={t("Project access")}
      description={t("Owners and admins always have full access. Everyone else gets their role below, or the default role.")}
      footer={
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          {t("Save access")}
        </Button>
      }
    >
      {q.isLoading ? (
        <Loading rows={3} />
      ) : (
        <div className="space-y-5">
          <div className="rounded-lg border border-border p-4">
            <div className="flex flex-wrap items-center gap-3">
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-semibold">{t("Default role for other members")}</div>
                <div className="text-xs text-muted">{t(roleDescriptions[defaultRole as Exclude<ProjectRole, "admin">])}</div>
              </div>
              <Select className="w-44" value={defaultRole} onChange={(e) => setDefaultRole(e.target.value as ProjectRole)}>
                <option value="none">{t("No access (private)")}</option>
                <option value="viewer">{t("Viewer")}</option>
                <option value="developer">{t("Developer")}</option>
                <option value="maintainer">{t("Maintainer")}</option>
              </Select>
            </div>
          </div>

          <div>
            <div className="mb-2 flex items-center gap-2">
              <Select className="flex-1" value={pick} onChange={(e) => setPick(Number(e.target.value))} disabled={available.length === 0}>
                <option value={0}>{available.length ? t("Add a team member…") : t("Every member already has a role")}</option>
                {available.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} — {c.email}
                  </option>
                ))}
              </Select>
              <Button icon={<UserPlus />} disabled={!pick} onClick={add}>
                {t("Add")}
              </Button>
            </div>
            {members.length === 0 ? (
              <Empty compact title={t("No explicit members")} description={t("Everyone uses the default role.")} />
            ) : (
              <div className="divide-y divide-border rounded-lg border border-border">
                {members.map((m) => (
                  <div key={m.user_id} className="flex items-center gap-3 px-3 py-2.5">
                    <Avatar name={m.name} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-[13px] font-medium">{m.name}</div>
                      <div className="truncate text-xs text-muted">{m.email}</div>
                    </div>
                    <Select className="h-8 w-36" value={m.role} onChange={(e) => setMembers(members.map((x) => (x.user_id === m.user_id ? { ...x, role: e.target.value as ProjectRole } : x)))}>
                      <option value="viewer">{t("Viewer")}</option>
                      <option value="developer">{t("Developer")}</option>
                      <option value="maintainer">{t("Maintainer")}</option>
                    </Select>
                    <Button size="icon-sm" variant="ghost" title={t("Remove")} onClick={() => setMembers(members.filter((x) => x.user_id !== m.user_id))}>
                      <Trash2 className="text-danger" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>
          {admins.length > 0 && (
            <div className="text-xs text-muted">
              {t("Full access through their organization role:")}{" "}
              {admins.map((a) => (
                <Badge key={a.id} className="mr-1">
                  {a.name} · {t(roleLabels[a.role === "owner" ? "admin" : (a.role as ProjectRole)] ?? a.role)}
                </Badge>
              ))}
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}

export function EnvironmentSettingsModal({ env, onClose }: { env: Environment | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState({ protected: false, require_approval: false });
  useEffect(() => {
    if (env) setF({ protected: env.protected, require_approval: env.require_approval });
  }, [env]);
  const save = useMutation({
    mutationFn: () => api.patch(`/environments/${env!.id}`, f),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["project", env!.project_id] });
      qc.invalidateQueries({ queryKey: ["application"] });
      toast.success(t("Environment updated"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Modal
      open={!!env}
      onClose={onClose}
      title={t("Environment “{name}”", { name: env?.name ?? "" })}
      description={t("Protection rules for deployments and configuration changes.")}
      footer={
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          {t("Save")}
        </Button>
      }
    >
      <div className="space-y-5">
        <Switch
          checked={f.protected}
          onChange={(v) => setF({ protected: v, require_approval: v ? f.require_approval : false })}
          label={t("Protected environment")}
          description={t("Only maintainers can change configuration, variables and domains. Deployments by developers become approval requests.")}
        />
        <Switch
          checked={f.require_approval}
          onChange={(v) => setF({ protected: v || f.protected, require_approval: v })}
          label={t("Require approval for every deployment")}
          description={t("Including Git pushes and API calls. A maintainer other than the requester must approve (four-eyes principle).")}
        />
      </div>
    </Modal>
  );
}
