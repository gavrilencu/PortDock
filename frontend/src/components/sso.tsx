import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, ExternalLink, Pencil, Plus, Save, ShieldCheck, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { t } from "../lib/i18n";
import type { Settings, SSOPreset, SSOProvider } from "../lib/types";
import { UpgradeNotice, useLicense } from "./access";
import { LDAPCard } from "./ldap";
import { Button, Card, CopyField, Empty, Field, Input, Loading, Modal, PasswordInput, Select, Switch, Table, useConfirm } from "./ui";

const kindColors: Record<string, string> = {
  entra: "bg-[#0078d4] text-white",
  google: "bg-white text-[#4285f4] border border-border",
  okta: "bg-[#00297a] text-white",
  keycloak: "bg-[#4d4d4d] text-white",
  authentik: "bg-[#fd4b2d] text-white",
  gitlab: "bg-[#fc6d26] text-white",
  oidc: "bg-inverse text-inverse-fg",
};

export function ProviderMark({ kind, name, className = "size-8 text-xs" }: { kind: string; name: string; className?: string }) {
  const letters = kind === "entra" ? "MS" : name.replace(/[^A-Za-z0-9]/g, "").slice(0, 2).toUpperCase() || "ID";
  return <span className={`flex shrink-0 items-center justify-center rounded-md font-semibold ${kindColors[kind] ?? kindColors.oidc} ${className}`}>{letters}</span>;
}

type Form = Partial<SSOProvider> & { client_secret?: string };

function PolicyCard() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => api.get<Settings>("/settings") });
  const [f, setF] = useState({ require_2fa: "off", password_login: "true" });
  useEffect(() => {
    if (settings.data) setF({ require_2fa: settings.data.require_2fa || "off", password_login: settings.data.password_login || "true" });
  }, [settings.data]);
  const save = useMutation({
    mutationFn: () => api.patch<Settings>("/settings", f),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      qc.invalidateQueries({ queryKey: ["auth-status"] });
      toast.success(t("Security policy saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (settings.isLoading) return <Loading rows={2} />;
  const dirty = f.require_2fa !== (settings.data!.require_2fa || "off") || f.password_login !== (settings.data!.password_login || "true");
  return (
    <Card
      title={t("Sign-in policy")}
      description={t("Applies to every member of this instance.")}
      actions={
        <Button size="sm" variant="primary" icon={<Save />} disabled={!dirty} loading={save.isPending} onClick={() => save.mutate()}>
          {t("Save")}
        </Button>
      }
    >
      <div className="grid gap-5 p-5 md:grid-cols-2">
        <Field label={t("Require two-factor authentication")} hint={t("Users without 2FA must enroll at their next sign-in. Single sign-on sessions rely on the identity provider's MFA.")}>
          <Select value={f.require_2fa} onChange={(e) => setF({ ...f, require_2fa: e.target.value })}>
            <option value="off">{t("Optional")}</option>
            <option value="admins">{t("Required for owners and admins")}</option>
            <option value="all">{t("Required for everyone")}</option>
          </Select>
        </Field>
        <div className="pt-1">
          <Switch
            checked={f.password_login !== "false"}
            onChange={(v) => setF({ ...f, password_login: v ? "true" : "false" })}
            label={t("Allow password sign-in")}
            description={t("Turn off to enforce single sign-on. The owner can always use their password as a break-glass account.")}
          />
        </div>
      </div>
    </Card>
  );
}

function ProviderModal({ initial, presets, redirectBase, onClose }: { initial: Form | null; presets: SSOPreset[]; redirectBase: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<Form>({});
  const [issuerOK, setIssuerOK] = useState<string | null>(null);
  useEffect(() => {
    setF(initial ?? {});
    setIssuerOK(null);
  }, [initial]);
  const editing = !!f.id;
  const preset = presets.find((p) => p.kind === f.kind);

  const save = useMutation({
    mutationFn: () => (editing ? api.patch<SSOProvider>(`/sso/providers/${f.id}`, f) : api.post<SSOProvider>("/sso/providers", f)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sso-providers"] });
      toast.success(editing ? t("Provider updated") : t("Provider added — users can now sign in with it"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  const discover = useMutation({
    mutationFn: () => api.post<{ issuer: string; authorization_endpoint: string }>("/sso/discover", { issuer: f.issuer }),
    onSuccess: (m) => setIssuerOK(m.authorization_endpoint),
    onError: (e) => {
      setIssuerOK(null);
      toast.error(e.message);
    },
  });

  const choose = (p: SSOPreset) =>
    setF({ kind: p.kind, name: p.kind === "oidc" ? "" : p.name, issuer: p.issuer, scopes: p.scopes, groups_claim: p.groups_claim, enabled: true, trust_email: true, default_role: "member" });

  const slug = f.slug || (f.name ?? "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  const redirect = redirectBase.replace("{slug}", slug || "<slug>");

  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="lg"
      title={editing ? t("Edit {name}", { name: f.name ?? "" }) : f.kind ? (preset ? t("Add {name}", { name: preset.name }) : t("Add provider")) : t("Add identity provider")}
      description={f.kind ? preset?.docs : t("Choose your identity provider. Any OpenID Connect provider is supported.")}
      footer={
        f.kind && (
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
            {editing ? t("Save changes") : t("Add provider")}
          </Button>
        )
      }
    >
      {!f.kind ? (
        <div className="grid gap-2 sm:grid-cols-2">
          {presets.map((p) => (
            <button key={p.kind} onClick={() => choose(p)} className="flex items-center gap-3 rounded-lg border border-border bg-surface p-3 text-left transition-colors hover:border-border-strong hover:bg-surface-2">
              <ProviderMark kind={p.kind} name={p.name} />
              <span className="text-[13px] font-medium">{p.name}</span>
            </button>
          ))}
        </div>
      ) : (
        <div className="space-y-5">
          <div className="grid gap-4 md:grid-cols-2">
            <Field label={t("Display name")} hint={t("Shown on the sign-in button.")}>
              <Input value={f.name ?? ""} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="Contoso SSO" autoFocus />
            </Field>
            <Field label={t("Slug")} hint={t("Part of the redirect URI. Can't contain spaces.")}>
              <Input value={f.slug ?? ""} onChange={(e) => setF({ ...f, slug: e.target.value })} placeholder={slug || "contoso"} className="font-mono text-xs" />
            </Field>
          </div>
          <Field label={t("Redirect URI")} hint={t("Register this exact URL with your identity provider.")}>
            <CopyField value={redirect} />
          </Field>
          <Field
            label={t("Issuer URL")}
            hint={
              issuerOK ? (
                <span className="flex items-center gap-1 text-success">
                  <CircleCheck className="size-3.5" /> {t("Discovery document found")}
                </span>
              ) : (
                preset?.issuer.includes("{") && t("Replace the placeholders in {issuer}.", { issuer: preset.issuer })
              )
            }
          >
            <div className="flex gap-2">
              <Input value={f.issuer ?? ""} onChange={(e) => (setF({ ...f, issuer: e.target.value }), setIssuerOK(null))} placeholder="https://login.microsoftonline.com/<tenant-id>/v2.0" className="font-mono text-xs" />
              <Button loading={discover.isPending} disabled={!f.issuer || f.issuer.includes("{")} onClick={() => discover.mutate()}>
                {t("Test")}
              </Button>
            </div>
          </Field>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label={t("Client ID")}>
              <Input value={f.client_id ?? ""} onChange={(e) => setF({ ...f, client_id: e.target.value })} className="font-mono text-xs" />
            </Field>
            <Field label={t("Client secret")} hint={editing && f.client_secret_set ? t("Leave empty to keep the current secret.") : t("Stored encrypted.")}>
              <PasswordInput value={f.client_secret ?? ""} onChange={(e) => setF({ ...f, client_secret: e.target.value })} placeholder={editing && f.client_secret_set ? "••••••••" : ""} />
            </Field>
          </div>

          <div className="rounded-lg border border-border">
            <div className="border-b border-border px-4 py-2.5 text-[13px] font-semibold">{t("Provisioning & access")}</div>
            <div className="grid gap-4 p-4 md:grid-cols-2">
              <Switch checked={!!f.auto_register} onChange={(v) => setF({ ...f, auto_register: v })} label={t("Create accounts automatically")} description={t("Just-in-time provisioning on first sign-in. Otherwise only invited users can sign in.")} />
              <Field label={t("Default role")}>
                <Select value={f.default_role ?? "member"} onChange={(e) => setF({ ...f, default_role: e.target.value as "admin" | "member" })}>
                  <option value="member">{t("Member")}</option>
                  <option value="admin">{t("Admin")}</option>
                </Select>
              </Field>
              <Field label={t("Allowed email domains")} hint={t("Comma-separated. Empty allows any domain.")}>
                <Input value={f.allowed_domains ?? ""} onChange={(e) => setF({ ...f, allowed_domains: e.target.value })} placeholder="contoso.com, contoso.eu" />
              </Field>
              <Switch checked={f.trust_email !== false} onChange={(v) => setF({ ...f, trust_email: v })} label={t("Link existing accounts by email")} description={t("Used when the provider doesn't send email_verified (e.g. Entra ID).")} />
            </div>
          </div>

          <div className="rounded-lg border border-border">
            <div className="border-b border-border px-4 py-2.5 text-[13px] font-semibold">{t("Role mapping")}</div>
            <div className="grid gap-4 p-4 md:grid-cols-2">
              <Field label={t("Admin groups")} hint={t("Members of these groups become admins; everyone else gets the default role. Synced on every sign-in. Empty disables mapping.")}>
                <Input value={f.admin_groups ?? ""} onChange={(e) => setF({ ...f, admin_groups: e.target.value })} placeholder="platform-admins, 5f1c…-group-object-id" />
              </Field>
              <Field label={t("Groups claim")} hint={t("Name of the claim that lists the user's groups.")}>
                <Input value={f.groups_claim ?? ""} onChange={(e) => setF({ ...f, groups_claim: e.target.value })} placeholder="groups" className="font-mono text-xs" />
              </Field>
              <Field label={t("Scopes")} className="md:col-span-2">
                <Input value={f.scopes ?? ""} onChange={(e) => setF({ ...f, scopes: e.target.value })} className="font-mono text-xs" />
              </Field>
            </div>
          </div>
        </div>
      )}
    </Modal>
  );
}

function ProvidersCard() {
  const { has } = useLicense();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useQuery({
    queryKey: ["sso-providers"],
    queryFn: () => api.get<{ providers: SSOProvider[]; presets: SSOPreset[]; redirect_base: string }>("/sso/providers"),
  });
  const [edit, setEdit] = useState<Form | null>(null);
  const toggle = useMutation({
    mutationFn: (p: SSOProvider) => api.patch(`/sso/providers/${p.id}`, { enabled: !p.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sso-providers"] }),
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/sso/providers/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sso-providers"] });
      toast.success(t("Provider removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  const { providers, presets, redirect_base } = q.data!;
  return (
    <Card
      title={t("Single sign-on")}
      description={t("Let your team sign in with your company identity provider over OpenID Connect.")}
      actions={
        providers.length > 0 && (
          <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
            {t("Add provider")}
          </Button>
        )
      }
    >
      {!has("sso") && (
        <div className="border-b border-border p-4">
          <UpgradeNotice feature="sso">{t("Single sign-on is part of the")}</UpgradeNotice>
        </div>
      )}
      {providers.length === 0 ? (
        <div className="p-5">
          <Empty
            compact
            icon={<ShieldCheck />}
            title={t("No identity providers")}
            description={t("Connect Microsoft Entra ID, Okta, Google Workspace, Keycloak or any OpenID Connect provider.")}
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
                {t("Add provider")}
              </Button>
            }
          />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("Provider")}</th>
              <th>{t("Provisioning")}</th>
              <th>{t("Users")}</th>
              <th>{t("Status")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {providers.map((p) => (
              <tr key={p.id}>
                <td>
                  <div className="flex items-center gap-3">
                    <ProviderMark kind={p.kind} name={p.name} />
                    <div className="min-w-0">
                      <div className="font-medium">{p.name}</div>
                      <a href={p.issuer} target="_blank" rel="noreferrer" className="flex items-center gap-1 truncate font-mono text-2xs text-muted hover:text-fg">
                        {p.issuer} <ExternalLink className="size-3" />
                      </a>
                    </div>
                  </div>
                </td>
                <td className="text-xs text-muted">
                  {p.auto_register ? t("Automatic · {role}", { role: p.default_role === "admin" ? t("admin") : t("member") }) : t("Invited users only")}
                  {p.admin_groups && <div>{t("Admin groups mapped")}</div>}
                  {p.allowed_domains && <div className="truncate">{p.allowed_domains}</div>}
                </td>
                <td>
                  <span className="inline-flex items-center gap-1 text-xs text-muted">
                    <Users className="size-3.5" /> {p.users}
                  </span>
                </td>
                <td>
                  <Switch checked={p.enabled} onChange={() => toggle.mutate(p)} />
                </td>
                <td className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button size="icon-sm" variant="ghost" onClick={() => setEdit({ ...p, client_secret: "" })} title={t("Edit")}>
                      <Pencil />
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title={t("Remove")}
                      onClick={async () =>
                        (await confirm({
                          title: t("Remove {name}?", { name: p.name }),
                          description: t("Users who only sign in through this provider will no longer be able to sign in. Their accounts are kept."),
                          danger: true,
                          confirmText: t("Remove"),
                        })) && del.mutate(p.id)
                      }
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
      <ProviderModal initial={edit} presets={presets} redirectBase={redirect_base} onClose={() => setEdit(null)} />
    </Card>
  );
}

export function AuthenticationSettings() {
  return (
    <div className="space-y-6">
      <PolicyCard />
      <ProvidersCard />
      <LDAPCard />
    </div>
  );
}
