import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, Network, Pencil, Plus, PlugZap, Trash2, UserSearch, Users } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { t } from "../lib/i18n";
import { UpgradeNotice, useLicense } from "./access";
import { Badge, Button, Card, Empty, Field, Input, Loading, Modal, PasswordInput, Select, Switch, Table, Textarea, useConfirm } from "./ui";

type Directory = {
  id: number;
  name: string;
  slug: string;
  kind: string;
  host: string;
  port: number;
  security: "ldaps" | "starttls" | "none";
  skip_verify: boolean;
  ca_cert: string;
  bind_dn: string;
  bind_password_set: boolean;
  base_dn: string;
  user_filter: string;
  email_attr: string;
  name_attr: string;
  groups_attr: string;
  required_groups: string;
  admin_groups: string;
  nested_groups: boolean;
  auto_register: boolean;
  default_role: "member" | "admin";
  enabled: boolean;
  users: number;
};
type Preset = { kind: string; name: string; port: number; security: string; user_filter: string; email_attr: string; name_attr: string; groups_attr: string; nested_groups: boolean; bind_hint: string; base_hint: string };
type Form = Partial<Directory> & { bind_password?: string };
type TestResult = { message: string; user?: { identity: { dn: string; username: string; email: string; name: string; groups: string[] }; role: string; allowed: boolean; password_checked: boolean } };

const marks: Record<string, string> = { ad: "AD", openldap: "LD", freeipa: "IPA", generic: "LDAP" };

function DirectoryModal({ initial, presets, onClose }: { initial: Form | null; presets: Preset[]; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<Form>({});
  const [test, setTest] = useState<TestResult | null>(null);
  const [probe, setProbe] = useState({ username: "", password: "" });
  useEffect(() => {
    setF(initial ?? {});
    setTest(null);
    setProbe({ username: "", password: "" });
  }, [initial]);
  const preset = presets.find((p) => p.kind === f.kind);
  const upd = (patch: Form) => {
    setF({ ...f, ...patch });
    setTest(null);
  };
  const choose = (p: Preset) =>
    setF({ kind: p.kind, name: p.kind === "generic" ? "" : p.name, port: p.port, security: p.security as Directory["security"], user_filter: p.user_filter, email_attr: p.email_attr, name_attr: p.name_attr, groups_attr: p.groups_attr, nested_groups: p.nested_groups, auto_register: true, default_role: "member", enabled: true });

  const runTest = useMutation({
    mutationFn: () => api.post<TestResult>("/ldap/test", { ...f, username: probe.username, password: probe.password }),
    onSuccess: setTest,
    onError: (e) => {
      setTest(null);
      toast.error(e.message);
    },
  });
  const save = useMutation({
    mutationFn: () => (f.id ? api.patch(`/ldap/directories/${f.id}`, f) : api.post("/ldap/directories", f)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ldap"] });
      qc.invalidateQueries({ queryKey: ["login-providers"] });
      toast.success(f.id ? t("Directory updated") : t("Directory added — users can sign in with their directory account"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="xl"
      title={f.id ? t("Edit {name}", { name: f.name ?? "" }) : preset ? t("Add {name}", { name: preset.name }) : t("Add LDAP directory")}
      description={preset ? t("Users sign in with their directory username (or email) and password. A service account looks the user up; their own password is verified with a bind.") : t("Choose your directory type.")}
      footer={
        preset && (
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
            {f.id ? t("Save changes") : t("Add directory")}
          </Button>
        )
      }
    >
      {!preset ? (
        <div className="grid gap-2 sm:grid-cols-2">
          {presets.map((p) => (
            <button key={p.kind} onClick={() => choose(p)} className="flex items-center gap-3 rounded-lg border border-border bg-surface p-3 text-left transition-colors hover:border-border-strong hover:bg-surface-2">
              <span className="flex size-8 items-center justify-center rounded-md bg-inverse text-[10px] font-semibold text-inverse-fg">{marks[p.kind]}</span>
              <span className="text-[13px] font-medium">{p.name}</span>
            </button>
          ))}
        </div>
      ) : (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
          <div className="space-y-5">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label={t("Display name")} hint={t("Shown on the sign-in page.")}>
                <Input value={f.name ?? ""} onChange={(e) => upd({ name: e.target.value })} placeholder="Corporate AD" />
              </Field>
              <Switch checked={f.enabled !== false} onChange={(v) => upd({ enabled: v })} label={t("Enabled")} description={t("Allow sign-in with this directory.")} />
            </div>
            <div className="rounded-lg border border-border">
              <div className="border-b border-border px-4 py-2.5 text-[13px] font-semibold">{t("Connection")}</div>
              <div className="grid gap-4 p-4 md:grid-cols-[minmax(0,1fr)_110px_150px]">
                <Field label={t("Host")} hint={f.kind === "ad" ? t("A domain controller or the domain name (DNS round-robin).") : undefined}>
                  <Input value={f.host ?? ""} onChange={(e) => upd({ host: e.target.value })} placeholder="dc01.corp.example" className="font-mono text-xs" />
                </Field>
                <Field label={t("Port")}>
                  <Input type="number" value={f.port ?? ""} onChange={(e) => upd({ port: Number(e.target.value) })} />
                </Field>
                <Field label={t("Security")}>
                  <Select value={f.security} onChange={(e) => upd({ security: e.target.value as Directory["security"], port: e.target.value === "ldaps" ? 636 : 389 })}>
                    <option value="ldaps">LDAPS (TLS)</option>
                    <option value="starttls">StartTLS</option>
                    <option value="none">{t("None (insecure)")}</option>
                  </Select>
                </Field>
                {f.security !== "none" && (
                  <>
                    <Field label={t("CA certificate (PEM)")} hint={t("For directories signed by an internal CA (e.g. AD Certificate Services).")} className="md:col-span-2">
                      <Textarea rows={3} className="font-mono text-2xs" value={f.ca_cert ?? ""} onChange={(e) => upd({ ca_cert: e.target.value })} placeholder="-----BEGIN CERTIFICATE-----" />
                    </Field>
                    <div className="pt-6">
                      <Switch checked={!!f.skip_verify} onChange={(v) => upd({ skip_verify: v })} label={t("Skip verification")} description={t("Lab use only.")} />
                    </div>
                  </>
                )}
                {f.security === "none" && <p className="text-xs text-warning md:col-span-3">{t("Passwords would travel unencrypted. Use LDAPS or StartTLS in production.")}</p>}
              </div>
            </div>
            <div className="rounded-lg border border-border">
              <div className="border-b border-border px-4 py-2.5 text-[13px] font-semibold">{t("Search")}</div>
              <div className="grid gap-4 p-4 md:grid-cols-2">
                <Field label={t("Service account (bind DN)")} hint={preset.bind_hint || t("Leave empty for anonymous search.")}>
                  <Input value={f.bind_dn ?? ""} onChange={(e) => upd({ bind_dn: e.target.value })} className="font-mono text-xs" />
                </Field>
                <Field label={t("Service account password")} hint={f.id && f.bind_password_set ? t("Leave empty to keep the stored password.") : t("Stored encrypted.")}>
                  <PasswordInput value={f.bind_password ?? ""} onChange={(e) => upd({ bind_password: e.target.value })} placeholder={f.id && f.bind_password_set ? "••••••••" : ""} />
                </Field>
                <Field label={t("Base DN")} hint={preset.base_hint} className="md:col-span-2">
                  <Input value={f.base_dn ?? ""} onChange={(e) => upd({ base_dn: e.target.value })} placeholder={preset.base_hint} className="font-mono text-xs" />
                </Field>
                <Field label={t("User filter")} hint={t("{username} is replaced (escaped) with what the user types.")} className="md:col-span-2">
                  <Input value={f.user_filter ?? ""} onChange={(e) => upd({ user_filter: e.target.value })} className="font-mono text-xs" />
                </Field>
                <div className="grid grid-cols-3 gap-3 md:col-span-2">
                  <Field label={t("Email attribute")}>
                    <Input value={f.email_attr ?? ""} onChange={(e) => upd({ email_attr: e.target.value })} className="font-mono text-xs" />
                  </Field>
                  <Field label={t("Name attribute")}>
                    <Input value={f.name_attr ?? ""} onChange={(e) => upd({ name_attr: e.target.value })} className="font-mono text-xs" />
                  </Field>
                  <Field label={t("Groups attribute")}>
                    <Input value={f.groups_attr ?? ""} onChange={(e) => upd({ groups_attr: e.target.value })} className="font-mono text-xs" />
                  </Field>
                </div>
              </div>
            </div>
            <div className="rounded-lg border border-border">
              <div className="border-b border-border px-4 py-2.5 text-[13px] font-semibold">{t("Access & roles")}</div>
              <div className="grid gap-4 p-4 md:grid-cols-2">
                <Field label={t("Required groups")} hint={t("Only members may sign in. CN or full DN, comma-separated. Empty = any account.")}>
                  <Input value={f.required_groups ?? ""} onChange={(e) => upd({ required_groups: e.target.value })} placeholder="PortDock-Users" />
                </Field>
                <Field label={t("Admin groups")} hint={t("Members become admins; synced at every sign-in.")}>
                  <Input value={f.admin_groups ?? ""} onChange={(e) => upd({ admin_groups: e.target.value })} placeholder="PortDock-Admins, Domain Admins" />
                </Field>
                <Switch checked={f.auto_register !== false} onChange={(v) => upd({ auto_register: v })} label={t("Create accounts automatically")} description={t("On first sign-in. Otherwise only invited emails can sign in.")} />
                {f.kind === "ad" && <Switch checked={f.nested_groups !== false} onChange={(v) => upd({ nested_groups: v })} label={t("Nested groups")} description={t("Resolve membership through nested AD groups.")} />}
                <Field label={t("Default role")}>
                  <Select value={f.default_role ?? "member"} onChange={(e) => upd({ default_role: e.target.value as "member" | "admin" })}>
                    <option value="member">{t("Member")}</option>
                    <option value="admin">{t("Admin")}</option>
                  </Select>
                </Field>
              </div>
            </div>
          </div>

          <div className="space-y-3 lg:sticky lg:top-0 lg:self-start">
            <div className="rounded-lg border border-border bg-surface-2/40 p-4">
              <div className="mb-3 text-[13px] font-semibold">{t("Test")}</div>
              <div className="space-y-3">
                <Field label={t("Username (optional)")}>
                  <Input value={probe.username} onChange={(e) => setProbe({ ...probe, username: e.target.value })} placeholder="jdoe" autoComplete="off" />
                </Field>
                <Field label={t("Password (optional)")} hint={t("With a password the full sign-in is verified.")}>
                  <PasswordInput value={probe.password} onChange={(e) => setProbe({ ...probe, password: e.target.value })} autoComplete="new-password" />
                </Field>
                <Button className="w-full justify-center" icon={probe.username ? <UserSearch /> : <PlugZap />} loading={runTest.isPending} onClick={() => runTest.mutate()}>
                  {probe.username ? t("Test user") : t("Test connection")}
                </Button>
              </div>
              {test && (
                <div className="mt-4 space-y-2 text-xs">
                  <div className="flex gap-1.5 text-success">
                    <CircleCheck className="mt-0.5 size-3.5 shrink-0" /> {test.message}
                  </div>
                  {test.user && (
                    <div className="space-y-1.5 rounded-md border border-border bg-surface p-2.5">
                      <div className="font-medium">
                        {test.user.identity.name} <span className="text-muted">({test.user.identity.username})</span>
                      </div>
                      <div className="text-muted">{test.user.identity.email || <span className="text-danger">{t("no email attribute")}</span>}</div>
                      <div className="break-all font-mono text-2xs text-faint">{test.user.identity.dn}</div>
                      <div className="flex flex-wrap gap-1">
                        <Badge tone={test.user.allowed ? "success" : "danger"}>{test.user.allowed ? t("Allowed") : t("Not in required groups")}</Badge>
                        <Badge tone="accent">{test.user.role === "admin" ? t("Admin") : test.user.role === "member" ? t("Member") : test.user.role}</Badge>
                        {test.user.password_checked && <Badge tone="success">{t("Password OK")}</Badge>}
                      </div>
                      <div className="text-muted">{t("{n} group|{n} groups", { n: test.user.identity.groups.length })}</div>
                      <div className="max-h-28 space-y-0.5 overflow-y-auto font-mono text-2xs text-faint">
                        {test.user.identity.groups.map((g) => (
                          <div key={g} className="truncate" title={g}>
                            {g}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </Modal>
  );
}

export function LDAPCard() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { has } = useLicense();
  const q = useQuery({ queryKey: ["ldap"], queryFn: () => api.get<{ directories: Directory[]; presets: Preset[] }>("/ldap/directories") });
  const [edit, setEdit] = useState<Form | null>(null);
  const toggle = useMutation({
    mutationFn: (d: Directory) => api.patch(`/ldap/directories/${d.id}`, { enabled: !d.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ldap"] }),
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/ldap/directories/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ldap"] }),
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  const { directories, presets } = q.data!;
  return (
    <Card
      title="LDAP & Active Directory"
      description={t("Sign in with directory accounts. Local accounts keep working as a fallback; 2FA policies still apply.")}
      actions={
        directories.length > 0 && (
          <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
            {t("Add directory")}
          </Button>
        )
      }
    >
      {!has("ldap") && (
        <div className="border-b border-border p-4">
          <UpgradeNotice feature="ldap">{t("LDAP and Active Directory sign-in are part of the")}</UpgradeNotice>
        </div>
      )}
      {directories.length === 0 ? (
        <div className="p-5">
          <Empty
            compact
            icon={<Network />}
            title={t("No directories")}
            description={t("Connect Microsoft Active Directory, OpenLDAP or FreeIPA.")}
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
                {t("Add directory")}
              </Button>
            }
          />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("Directory")}</th>
              <th>{t("Security")}</th>
              <th>{t("Users")}</th>
              <th>{t("Enabled")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {directories.map((d) => (
              <tr key={d.id}>
                <td>
                  <div className="flex items-center gap-3">
                    <span className="flex size-8 items-center justify-center rounded-md bg-inverse text-[10px] font-semibold text-inverse-fg">{marks[d.kind] ?? "LDAP"}</span>
                    <div className="min-w-0">
                      <div className="font-medium">{d.name}</div>
                      <div className="truncate font-mono text-2xs text-muted">
                        {d.host}:{d.port} · {d.base_dn}
                      </div>
                    </div>
                  </div>
                </td>
                <td>
                  <Badge tone={d.security === "none" ? "danger" : d.skip_verify ? "warning" : "success"}>{d.security === "none" ? t("Unencrypted") : d.security === "ldaps" ? "LDAPS" : "StartTLS"}</Badge>
                </td>
                <td>
                  <span className="inline-flex items-center gap-1 text-xs text-muted">
                    <Users className="size-3.5" /> {d.users}
                  </span>
                </td>
                <td>
                  <Switch checked={d.enabled} onChange={() => toggle.mutate(d)} />
                </td>
                <td className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button size="icon-sm" variant="ghost" title={t("Edit")} onClick={() => setEdit({ ...d, bind_password: "" })}>
                      <Pencil />
                    </Button>
                    <Button size="icon-sm" variant="ghost" title={t("Remove")} onClick={async () => (await confirm({ title: t("Remove {name}?", { name: d.name }), description: t("Directory users can no longer sign in. Their PortDock accounts are kept."), danger: true, confirmText: t("Remove") })) && del.mutate(d.id)}>
                      <Trash2 className="text-danger" />
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <DirectoryModal initial={edit} presets={presets} onClose={() => setEdit(null)} />
    </Card>
  );
}
