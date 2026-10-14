import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, KeyRound, Plus, Power, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { APIToken, Project, ServiceAccount, TokenScope } from "../lib/types";
import { timeAgo } from "../lib/utils";
import { t, tk } from "../lib/i18n";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Loading, Modal, Select, Table, useConfirm } from "./ui";

export const scopeInfo: Record<TokenScope, { label: string; hint: string; tone: "neutral" | "info" | "warning" | "danger" }> = {
  read: { label: tk("Read"), hint: tk("View resources, logs and deployments."), tone: "neutral" },
  deploy: { label: tk("Deploy"), hint: tk("Read, plus deploy, redeploy and roll back. The usual choice for CI."), tone: "info" },
  write: { label: tk("Write"), hint: tk("Deploy, plus change configuration, variables and domains. No deletions."), tone: "warning" },
  full: { label: tk("Full"), hint: tk("Everything the account may do, including deleting resources and, for administrators, the organization."), tone: "danger" },
};

const expiries = [
  { days: 30, label: tk("30 days") },
  { days: 90, label: tk("90 days") },
  { days: 365, label: tk("1 year") },
  { days: 0, label: tk("Never") },
];

type TokenForm = { name: string; scope: TokenScope; project_ids: number[]; expires_in_days: number };

/** Form creating a token for the current user (path /tokens) or a service account. */
export function CreateToken({ path, onCreated }: { path: string; onCreated: () => void }) {
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
  const [f, setF] = useState<TokenForm>({ name: "", scope: "deploy", project_ids: [], expires_in_days: 90 });
  const [allProjects, setAllProjects] = useState(true);
  const [created, setCreated] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.post<{ token: string }>(path, { ...f, project_ids: allProjects ? [] : f.project_ids }),
    onSuccess: (r) => {
      setCreated(r.token);
      setF({ ...f, name: "" });
      onCreated();
    },
    onError: (e) => toast.error(e.message),
  });
  const toggle = (id: number) => setF({ ...f, project_ids: f.project_ids.includes(id) ? f.project_ids.filter((x) => x !== id) : [...f.project_ids, id] });
  const valid = f.name.trim() !== "" && (allProjects || f.project_ids.length > 0);
  return (
    <div className="space-y-4">
      <form
        className="grid gap-4 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid) create.mutate();
        }}
      >
        <Field label={t("Name")}>
          <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder={t("Token name, e.g. github-actions")} />
        </Field>
        <Field label={t("Expires")}>
          <Select value={f.expires_in_days} onChange={(e) => setF({ ...f, expires_in_days: Number(e.target.value) })}>
            {expiries.map((x) => (
              <option key={x.days} value={x.days}>
                {t(x.label)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Scope")} hint={t(scopeInfo[f.scope].hint)} className="md:col-span-2">
          <Select value={f.scope} onChange={(e) => setF({ ...f, scope: e.target.value as TokenScope })}>
            {(Object.keys(scopeInfo) as TokenScope[]).map((s) => (
              <option key={s} value={s}>
                {t(scopeInfo[s].label)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Projects")} className="md:col-span-2" hint={allProjects ? t("Every project the account can access, now and later.") : t("Other projects stay invisible to this token.")}>
          <div className="space-y-2">
            <Select value={allProjects ? "all" : "some"} onChange={(e) => setAllProjects(e.target.value === "all")}>
              <option value="all">{t("All projects")}</option>
              <option value="some">{t("Only selected projects")}</option>
            </Select>
            {!allProjects && (
              <div className="flex flex-wrap gap-2">
                {(projects.data ?? []).map((p) => (
                  <label key={p.id} className="flex cursor-pointer items-center gap-2 rounded-md border border-border px-2.5 py-1.5 text-[13px] hover:bg-surface-2">
                    <input type="checkbox" checked={f.project_ids.includes(p.id)} onChange={() => toggle(p.id)} />
                    {p.name}
                  </label>
                ))}
              </div>
            )}
          </div>
        </Field>
        <div className="flex justify-end md:col-span-2">
          <Button type="submit" variant="primary" icon={<Plus className="size-4" />} loading={create.isPending} disabled={!valid}>
            {t("Create token")}
          </Button>
        </div>
      </form>
      {created && (
        <div className="rounded-lg border border-success/30 bg-success/5 p-4">
          <p className="mb-2 text-sm font-medium text-success">{t("Copy your token now — it won't be shown again.")}</p>
          <CopyField value={created} />
        </div>
      )}
    </div>
  );
}

/** Table of tokens with their limits. */
export function TokenTable({ tokens, onRevoke }: { tokens: APIToken[]; onRevoke: (tok: APIToken) => void }) {
  const confirm = useConfirm();
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
  const projectName = (id: string) => (projects.data ?? []).find((p) => String(p.id) === id)?.name ?? `#${id}`;
  if (tokens.length === 0) return <p className="p-5 text-sm text-muted">{t("No tokens.")}</p>;
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("Name")}</th>
          <th>{t("Scope")}</th>
          <th>{t("Projects")}</th>
          <th>{t("Expires")}</th>
          <th>{t("Last used")}</th>
          <th />
        </tr>
      </thead>
      <tbody>
        {tokens.map((tok) => {
          const scope = scopeInfo[tok.scope] ?? scopeInfo.full;
          const expired = !!tok.expires_at && new Date(tok.expires_at) <= new Date();
          return (
            <tr key={tok.id} className={expired ? "opacity-60" : undefined}>
              <td className="font-medium">{tok.name}</td>
              <td>
                <Badge tone={scope.tone}>{t(scope.label)}</Badge>
              </td>
              <td className="text-xs text-muted">{tok.project_ids ? tok.project_ids.split(",").map(projectName).join(", ") : t("All")}</td>
              <td className="text-xs">{expired ? <Badge tone="danger">{t("Expired")}</Badge> : tok.expires_at ? <span className="text-muted">{new Date(tok.expires_at).toLocaleDateString()}</span> : <span className="text-muted">{t("Never")}</span>}</td>
              <td className="text-xs text-muted">{tok.last_used_at ? timeAgo(tok.last_used_at) : "—"}</td>
              <td className="text-right">
                <Button
                  size="icon-sm"
                  variant="ghost"
                  title={t("Revoke")}
                  aria-label={t("Revoke")}
                  onClick={async () => (await confirm({ title: t("Revoke {name}?", { name: tok.name }), description: t("Scripts using this token stop working immediately."), danger: true, confirmText: t("Revoke") })) && onRevoke(tok)}
                >
                  <Trash2 className="text-danger" />
                </Button>
              </td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}

/** Settings → Team: accounts for automation, which only act through tokens. */
export function ServiceAccountsCard() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const list = useQuery({ queryKey: ["service-accounts"], queryFn: () => api.get<ServiceAccount[]>("/service-accounts") });
  const [creating, setCreating] = useState<{ name: string; role: "member" | "admin" } | null>(null);
  const [managing, setManaging] = useState<number | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["service-accounts"] });
  const create = useMutation({
    mutationFn: () => api.post<ServiceAccount>("/service-accounts", creating),
    onSuccess: (sa) => {
      setCreating(null);
      refresh();
      setManaging(sa.id);
    },
    onError: (e) => toast.error(e.message),
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: number; body: Record<string, unknown> }) => api.patch(`/service-accounts/${id}`, body),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/service-accounts/${id}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const revoke = useMutation({
    mutationFn: ({ sa, tok }: { sa: number; tok: number }) => api.del(`/service-accounts/${sa}/tokens/${tok}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  if (list.isLoading) return <Loading />;
  const accounts = list.data ?? [];
  const current = accounts.find((a) => a.id === managing);
  return (
    <Card
      title={t("Service accounts")}
      description={t("Accounts for CI pipelines and scripts. They cannot sign in; they act only through the API tokens you create for them, and their actions appear under their name in the audit log.")}
      actions={
        <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setCreating({ name: "", role: "member" })}>
          {t("Add service account")}
        </Button>
      }
    >
      {accounts.length === 0 ? (
        <div className="p-4">
          <Empty compact icon={<Bot />} title={t("No service accounts")} description={t("Create one per pipeline (e.g. github-actions) instead of using a person's token.")} />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("Name")}</th>
              <th>{t("Role")}</th>
              <th>{t("Tokens")}</th>
              <th className="!text-right">{t("Actions")}</th>
            </tr>
          </thead>
          <tbody>
            {accounts.map((sa) => (
              <tr key={sa.id} className={sa.disabled ? "opacity-60" : undefined}>
                <td>
                  <span className="flex items-center gap-2 font-medium">
                    <Bot className="size-4 text-faint" />
                    {sa.name}
                    {sa.disabled && <Badge>{t("Disabled")}</Badge>}
                  </span>
                </td>
                <td>
                  <Select className="w-32" value={sa.role} onChange={(e) => update.mutate({ id: sa.id, body: { role: e.target.value } })}>
                    <option value="member">{t("Member")}</option>
                    <option value="admin">{t("Admin")}</option>
                  </Select>
                </td>
                <td className="text-muted">{t("{n} token|{n} tokens", { n: sa.tokens.length })}</td>
                <td>
                  <div className="flex justify-end gap-0.5">
                    <Button size="sm" icon={<KeyRound className="size-3.5" />} onClick={() => setManaging(sa.id)}>
                      {t("Tokens")}
                    </Button>
                    <Button size="icon-sm" variant="ghost" title={sa.disabled ? t("Enable") : t("Disable")} aria-label={sa.disabled ? t("Enable") : t("Disable")} onClick={() => update.mutate({ id: sa.id, body: { disabled: !sa.disabled } })}>
                      <Power />
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title={t("Delete")}
                      aria-label={t("Delete")}
                      onClick={async () => (await confirm({ title: t("Delete {name}?", { name: sa.name }), description: t("Its tokens stop working immediately."), danger: true, confirmText: t("Delete"), typeToConfirm: sa.name })) && del.mutate(sa.id)}
                    >
                      <Trash2 className="text-danger" />
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Modal
        open={!!creating}
        onClose={() => setCreating(null)}
        title={t("Add service account")}
        footer={
          <Button variant="primary" loading={create.isPending} disabled={!creating?.name.trim()} onClick={() => create.mutate()}>
            {t("Create")}
          </Button>
        }
      >
        {creating && (
          <div className="grid gap-4 md:grid-cols-2">
            <Field label={t("Name")}>
              <Input autoFocus value={creating.name} onChange={(e) => setCreating({ ...creating, name: e.target.value })} placeholder="github-actions" />
            </Field>
            <Field label={t("Role")} hint={t("Members get access to projects like people do: the project's default role or an explicit membership.")}>
              <Select value={creating.role} onChange={(e) => setCreating({ ...creating, role: e.target.value as "member" | "admin" })}>
                <option value="member">{t("Member")}</option>
                <option value="admin">{t("Admin")}</option>
              </Select>
            </Field>
          </div>
        )}
      </Modal>
      <Modal open={!!current} onClose={() => setManaging(null)} size="lg" title={t("Tokens of {name}", { name: current?.name ?? "" })}>
        {current && (
          <div className="space-y-5">
            <CreateToken path={`/service-accounts/${current.id}/tokens`} onCreated={refresh} />
            <div className="rounded-lg border border-border">
              <TokenTable tokens={current.tokens} onRevoke={(tok) => revoke.mutate({ sa: current.id, tok: tok.id })} />
            </div>
          </div>
        )}
      </Modal>
    </Card>
  );
}
