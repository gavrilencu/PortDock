import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ArrowUpCircle, Ban, Bell, Boxes, FileKey2, Sparkles, Cloud, Eye, EyeOff, KeyRound, LockKeyhole, LogOut, MoreHorizontal, Plus, RefreshCw, Save, Send, Settings2, ShieldCheck, ShieldOff, Terminal as TerminalIcon, Trash2, User as UserIcon, Users } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { APIToken, DNSStatus, NotificationChannel, Settings, SSHKey, User } from "../lib/types";
import { DNSBadge } from "../components/Domains";
import { useUser } from "../components/Layout";
import { GitHubMark, GitSourcesSettings } from "../components/github";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Loading, Menu, MenuItem, MenuSeparator, Modal, PageHeader, PasswordInput, Select, Switch, Table, useConfirm } from "../components/ui";
import { SecuritySettings } from "../components/security";
import { AuthenticationSettings } from "../components/sso";
import { RegistriesSettings } from "../components/registries";
import { AuditLog, LicenseSettings } from "../components/auditLicense";
import { CertificatesSettings } from "../components/certificates";
import { UpdateSettings } from "../components/updates";
import { timeAgo } from "../lib/utils";

type Tab = "general" | "profile" | "security" | "auth" | "registries" | "license" | "certificates" | "updates" | "users" | "git" | "keys" | "tokens" | "notifications" | "storage" | "activity";

export function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const user0 = useUser();
  const tab = (params.get("tab") as Tab) || (user0.role !== "member" ? "general" : "profile");
  const user = useUser();
  const admin = user.role !== "member";
  const groups: { label: string; items: { id: Tab; label: string; icon: React.ReactNode; hidden?: boolean }[] }[] = [
    {
      label: "Account",
      items: [
        { id: "profile", label: "Profile", icon: <UserIcon /> },
        { id: "security", label: "Sign-in & security", icon: <ShieldCheck /> },
        { id: "tokens", label: "API tokens", icon: <TerminalIcon /> },
      ],
    },
    {
      label: "Organization",
      items: [
        { id: "general", label: "General", icon: <Settings2 />, hidden: !admin },
        { id: "users", label: "Team members", icon: <Users />, hidden: !admin },
        { id: "auth", label: "Authentication", icon: <LockKeyhole />, hidden: !admin },
        { id: "git", label: "Git sources", icon: <GitHubMark />, hidden: !admin },
        { id: "registries", label: "Container registries", icon: <Boxes />, hidden: !admin },
        { id: "certificates", label: "Certificates", icon: <FileKey2 />, hidden: !admin },
        { id: "license", label: "License", icon: <Sparkles />, hidden: !admin },
        { id: "updates", label: "Updates", icon: <ArrowUpCircle />, hidden: !admin },
        { id: "notifications", label: "Notifications", icon: <Bell />, hidden: !admin },
        { id: "storage", label: "Backup storage", icon: <Cloud />, hidden: !admin },
      ],
    },
    {
      label: "Security",
      items: [
        { id: "keys", label: "SSH keys", icon: <KeyRound /> },
        { id: "activity", label: "Audit log", icon: <Activity /> },
      ],
    },
  ];
  return (
    <>
      <PageHeader title="Settings" subtitle="Manage your account, organization and security preferences." />
      <div className="grid gap-8 lg:grid-cols-[200px_minmax(0,1fr)]">
        <nav className="flex gap-1 overflow-x-auto lg:flex-col lg:gap-5 lg:overflow-visible">
          {groups.map((g) => (
            <div key={g.label} className="flex gap-1 lg:block">
              <div className="mb-1 hidden px-2 text-2xs font-medium uppercase tracking-wider text-faint lg:block">{g.label}</div>
              {g.items
                .filter((i) => !i.hidden)
                .map((i) => (
                  <button
                    key={i.id}
                    onClick={() => setParams({ tab: i.id })}
                    className={`flex h-8 w-full items-center gap-2.5 whitespace-nowrap rounded-md px-2 text-left text-[13px] font-medium transition-colors [&_svg]:size-4 ${
                      tab === i.id ? "bg-surface-2 text-fg [&_svg]:text-fg" : "text-muted hover:bg-surface-2/60 hover:text-fg [&_svg]:text-faint"
                    }`}
                  >
                    {i.icon}
                    {i.label}
                  </button>
                ))}
            </div>
          ))}
        </nav>
        <div className="min-w-0">
          {tab === "profile" && <Profile />}
          {tab === "security" && <SecuritySettings />}
          {tab === "auth" && <AuthenticationSettings />}
          {tab === "registries" && <RegistriesSettings />}
          {tab === "license" && <LicenseSettings />}
          {tab === "certificates" && <CertificatesSettings />}
          {tab === "updates" && <UpdateSettings />}
          {tab === "general" && <General />}
          {tab === "users" && <Team />}
          {tab === "git" && <GitSourcesSettings />}
          {tab === "keys" && <Keys />}
          {tab === "tokens" && <Tokens />}
          {tab === "notifications" && <Notifications />}
          {tab === "storage" && <Storage />}
          {tab === "activity" && <AuditLog />}
        </div>
      </div>
    </>
  );
}

function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => api.get<Settings>("/settings") });
}

function Profile() {
  const user = useUser();
  const qc = useQueryClient();
  const [f, setF] = useState({ name: user.name, email: user.email, current_password: "", new_password: "" });
  const save = useMutation({
    mutationFn: () => api.patch<User>("/auth/me", f),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["auth-status"] });
      setF((x) => ({ ...x, current_password: "", new_password: "" }));
      toast.success("Profile updated");
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Card title="Your profile">
      <div className="grid gap-4 p-5 md:grid-cols-2">
        <Field label="Name">
          <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        <Field label="Email">
          <Input value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} />
        </Field>
        {user.has_password ? (
          <Field label="Current password" hint="Required to set a new password. Other sessions are signed out after a change.">
            <PasswordInput value={f.current_password} onChange={(e) => setF({ ...f, current_password: e.target.value })} />
          </Field>
        ) : (
          <Field label="Password" hint="You sign in with single sign-on. Set a password only if your administrator allows password sign-in.">
            <Input value="Not set" disabled />
          </Field>
        )}
        <Field label="New password">
          <PasswordInput value={f.new_password} onChange={(e) => setF({ ...f, new_password: e.target.value })} />
        </Field>
        <div className="flex justify-end md:col-span-2">
          <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Card>
  );
}

function General() {
  const qc = useQueryClient();
  const { data, isLoading } = useSettings();
  const [f, setF] = useState<Partial<Settings>>({});
  const [showV4, setShowV4] = useState(false);
  const [showV6, setShowV6] = useState(false);
  useEffect(() => data && setF(data), [data]);
  const zones = useMemo(() => {
    try {
      return (Intl as unknown as { supportedValuesOf: (k: string) => string[] }).supportedValuesOf("timeZone");
    } catch {
      return ["UTC"];
    }
  }, []);
  const host = (() => {
    try {
      return f.instance_url ? new URL(f.instance_url).hostname : "";
    } catch {
      return "";
    }
  })();
  const dns = useQuery({
    queryKey: ["dns-check", host, data?.public_ipv4, data?.public_ipv6],
    queryFn: () => api.get<{ status: DNSStatus; detail: string }>(`/dns/check?host=${encodeURIComponent(host)}`),
    enabled: !!host && host === (data?.instance_url ? new URL(data.instance_url).hostname : ""),
    retry: false,
  });

  const save = useMutation({
    mutationFn: () =>
      api.patch<Settings>("/settings", {
        instance_name: f.instance_name,
        instance_url: f.instance_url,
        instance_https: f.instance_https,
        instance_timezone: f.instance_timezone,
        public_ipv4: f.public_ipv4,
        public_ipv6: f.public_ipv6,
        acme_email: f.acme_email,
        auto_cleanup: f.auto_cleanup,
        proxy_dashboard: f.proxy_dashboard,
      }),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      qc.invalidateQueries({ queryKey: ["dns-check"] });
      if (s.instance_route_error) toast.warning("Settings saved, but the dashboard domain could not be routed", { description: s.instance_route_error });
      else toast.success("Settings saved");
    },
    onError: (e) => toast.error(e.message),
  });
  const detect = useMutation({
    mutationFn: () => api.post<{ public_ipv4: string; public_ipv6: string }>("/settings/detect-ip"),
    onSuccess: (r) => {
      setF((x) => ({ ...x, public_ipv4: r.public_ipv4 || x.public_ipv4, public_ipv6: r.public_ipv6 || x.public_ipv6 }));
      toast.success(r.public_ipv4 || r.public_ipv6 ? "Public addresses detected — save to apply" : "Could not detect a public address");
    },
    onError: (e) => toast.error(e.message),
  });

  if (isLoading) return <Loading />;
  const dirty = JSON.stringify(f) !== JSON.stringify(data);

  return (
    <div className="space-y-6">
      <Card title="General" description="How this PortDock instance is identified and reached.">
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field
            label="URL"
            hint={
              <>
                The dashboard is served on this domain by Traefik with a Let's Encrypt certificate. Point its DNS record to this server.
                {dns.data && (
                  <span className="mt-1.5 flex items-center gap-1.5">
                    <DNSBadge d={{ dns_status: dns.data.status, dns_detail: dns.data.detail, dns_checked_at: "" }} />
                    <span className="text-subtle">{dns.data.detail}</span>
                  </span>
                )}
              </>
            }
          >
            <Input value={f.instance_url ?? ""} onChange={(e) => setF({ ...f, instance_url: e.target.value })} placeholder="https://panel.example.com" />
          </Field>
          <Field label="Redirect HTTP to HTTPS" hint="Permanently redirect plain HTTP requests to the dashboard.">
            <Select value={f.instance_https === "false" ? "off" : "on"} onChange={(e) => setF({ ...f, instance_https: e.target.value === "on" ? "true" : "false" })}>
              <option value="on">Enabled</option>
              <option value="off">Disabled</option>
            </Select>
          </Field>
          <Field label="Name" hint="Shown in notifications and the browser title.">
            <Input value={f.instance_name ?? ""} onChange={(e) => setF({ ...f, instance_name: e.target.value })} placeholder="PortDock" />
          </Field>
          <Field label="Instance timezone" hint="Used for scheduled jobs such as database backups.">
            <Select value={f.instance_timezone || "UTC"} onChange={(e) => setF({ ...f, instance_timezone: e.target.value })}>
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        {data!.instance_route_error && (
          <div className="border-t border-border bg-warning-soft px-5 py-3 text-xs text-warning">Dashboard routing: {data!.instance_route_error}</div>
        )}
      </Card>

      <Card
        title="Network addresses"
        description="Public addresses of this server, used to verify DNS records and generate the records to create."
        actions={
          <Button size="sm" icon={<RefreshCw />} loading={detect.isPending} onClick={() => detect.mutate()}>
            Detect
          </Button>
        }
      >
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field label="Instance public IPv4">
            <div className="relative">
              <Input className="pr-9 font-mono text-xs" type={showV4 ? "text" : "password"} value={f.public_ipv4 ?? ""} onChange={(e) => setF({ ...f, public_ipv4: e.target.value.trim() })} placeholder="203.0.113.10" />
              <button type="button" onClick={() => setShowV4(!showV4)} className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-fg">
                {showV4 ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </button>
            </div>
          </Field>
          <Field label="Instance public IPv6">
            <div className="relative">
              <Input className="pr-9 font-mono text-xs" type={showV6 ? "text" : "password"} value={f.public_ipv6 ?? ""} onChange={(e) => setF({ ...f, public_ipv6: e.target.value.trim() })} placeholder="2001:db8::1" />
              <button type="button" onClick={() => setShowV6(!showV6)} className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-fg">
                {showV6 ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </button>
            </div>
          </Field>
        </div>
      </Card>

      <Card title="Proxy & maintenance">
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field label="Let's Encrypt email" hint="Receives certificate expiry notices. Recreate proxies after changing.">
            <Input value={f.acme_email ?? ""} onChange={(e) => setF({ ...f, acme_email: e.target.value })} placeholder="ops@example.com" />
          </Field>
          <Field label="Version">
            <Input value={data!.version} disabled />
          </Field>
          <Switch checked={f.auto_cleanup !== "false"} onChange={(v) => setF({ ...f, auto_cleanup: v ? "true" : "false" })} label="Automatic cleanup" description="Daily prune of build cache (>72h) and dangling images on all servers." />
          <Switch checked={f.proxy_dashboard === "true"} onChange={(v) => setF({ ...f, proxy_dashboard: v ? "true" : "false" })} label="Traefik dashboard" description="Expose the Traefik dashboard on 127.0.0.1:8080 of each server." />
        </div>
      </Card>

      <div className="sticky bottom-4 z-10 flex justify-end">
        <Button variant="primary" icon={<Save className="size-4" />} disabled={!dirty} loading={save.isPending} onClick={() => save.mutate()} className="shadow-pop">
          Save settings
        </Button>
      </div>
    </div>
  );
}

function Team() {
  const qc = useQueryClient();
  const me = useUser();
  const confirm = useConfirm();
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<User[]>("/users") });
  const [invite, setInvite] = useState<{ name: string; email: string; password: string; role: string } | null>(null);
  const create = useMutation({
    mutationFn: () => api.post("/users", invite),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      setInvite(null);
      toast.success("User created — share the credentials securely");
    },
    onError: (e) => toast.error(e.message),
  });
  const update = useMutation({
    mutationFn: ({ id, ...body }: { id: number; role?: string; disabled?: boolean }) => api.patch(`/users/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
    onError: (e) => toast.error(e.message),
  });
  const action = useMutation({
    mutationFn: ({ id, path }: { id: number; path: string; done: string }) => api.post(`/users/${id}/${path}`),
    onSuccess: (_, v) => {
      qc.invalidateQueries({ queryKey: ["users"] });
      toast.success(v.done);
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/users/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
    onError: (e) => toast.error(e.message),
  });
  if (users.isLoading) return <Loading />;
  return (
    <Card
      title="Team members"
      description="Admins manage servers, settings and users. Members manage projects and resources."
      actions={
        <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setInvite({ name: "", email: "", password: "", role: "member" })}>
          Add member
        </Button>
      }
    >
      <Table>
        <thead>
          <tr>
            <th>User</th>
            <th>Role</th>
            <th>Security</th>
            <th>Last sign-in</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {users.data!.map((u) => (
            <tr key={u.id}>
              <td className={u.disabled ? "opacity-60" : undefined}>
                <div className="font-medium">
                  {u.name} {u.id === me.id && <Badge>you</Badge>} {u.disabled && <Badge tone="danger">Disabled</Badge>}
                </div>
                <div className="text-xs text-muted">{u.email}</div>
              </td>
              <td>
                {u.role === "owner" ? (
                  <Badge tone="accent">Owner</Badge>
                ) : (
                  <Select value={u.role} onChange={(e) => update.mutate({ id: u.id, role: e.target.value })} className="h-8 w-32">
                    <option value="admin">admin</option>
                    <option value="member">member</option>
                  </Select>
                )}
              </td>
              <td>
                <div className="flex flex-wrap gap-1">
                  {u.totp_enabled ? (
                    <Badge tone="success">
                      <ShieldCheck /> 2FA
                    </Badge>
                  ) : (
                    <Badge tone={u.auth_source ? "neutral" : "warning"}>
                      <ShieldOff /> No 2FA
                    </Badge>
                  )}
                  {u.auth_source && <Badge tone="info">SSO · {u.auth_source}</Badge>}
                </div>
              </td>
              <td className="text-xs text-muted">{u.last_login_at ? timeAgo(u.last_login_at) : "Never"}</td>
              <td className="text-right">
                {u.id !== me.id && (u.role !== "owner" || me.role === "owner") && (
                  <Menu
                    trigger={
                      <Button size="icon-sm" variant="ghost" aria-label="Actions">
                        <MoreHorizontal />
                      </Button>
                    }
                  >
                    <MenuItem
                      icon={<LogOut />}
                      onClick={async () =>
                        (await confirm({ title: `Sign ${u.name} out everywhere?`, confirmText: "Sign out" })) && action.mutate({ id: u.id, path: "sessions/revoke", done: "All sessions signed out" })
                      }
                    >
                      Sign out all sessions
                    </MenuItem>
                    {u.totp_enabled && (
                      <MenuItem
                        icon={<ShieldOff />}
                        onClick={async () =>
                          (await confirm({ title: `Reset 2FA for ${u.email}?`, description: "Use this when the user lost their device. They will be signed out and can enroll again.", danger: true, confirmText: "Reset 2FA" })) &&
                          action.mutate({ id: u.id, path: "2fa/reset", done: "Two-factor authentication reset" })
                        }
                      >
                        Reset two-factor
                      </MenuItem>
                    )}
                    {u.role !== "owner" && (
                      <>
                        <MenuItem icon={<Ban />} onClick={() => update.mutate({ id: u.id, disabled: !u.disabled })}>
                          {u.disabled ? "Enable account" : "Disable account"}
                        </MenuItem>
                        <MenuSeparator />
                        <MenuItem icon={<Trash2 />} danger onClick={async () => (await confirm({ title: `Remove ${u.email}?`, danger: true, confirmText: "Remove" })) && del.mutate(u.id)}>
                          Remove member
                        </MenuItem>
                      </>
                    )}
                  </Menu>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
      <Modal
        open={!!invite}
        onClose={() => setInvite(null)}
        title="Add team member"
        footer={
          <Button variant="primary" loading={create.isPending} onClick={() => create.mutate()}>
            Create user
          </Button>
        }
      >
        {invite && (
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Name">
              <Input value={invite.name} onChange={(e) => setInvite({ ...invite, name: e.target.value })} />
            </Field>
            <Field label="Email">
              <Input type="email" value={invite.email} onChange={(e) => setInvite({ ...invite, email: e.target.value })} />
            </Field>
            <Field label="Initial password" hint="At least 8 characters.">
              <PasswordInput value={invite.password} onChange={(e) => setInvite({ ...invite, password: e.target.value })} />
            </Field>
            <Field label="Role">
              <Select value={invite.role} onChange={(e) => setInvite({ ...invite, role: e.target.value })}>
                <option value="member">Member</option>
                <option value="admin">Admin</option>
              </Select>
            </Field>
          </div>
        )}
      </Modal>
    </Card>
  );
}

function Keys() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const user = useUser();
  const keys = useQuery({ queryKey: ["ssh-keys"], queryFn: () => api.get<SSHKey[]>("/ssh-keys") });
  const [f, setF] = useState<{ name: string; private_key: string } | null>(null);
  const create = useMutation({
    mutationFn: () => api.post("/ssh-keys", f),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ssh-keys"] });
      setF(null);
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/ssh-keys/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ssh-keys"] }),
    onError: (e) => toast.error(e.message),
  });
  if (keys.isLoading) return <Loading />;
  return (
    <Card
      title="SSH keys"
      description="Private keys are encrypted at rest and never leave PortDock."
      actions={
        user.role !== "member" && (
          <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setF({ name: "", private_key: "" })}>
            Add key
          </Button>
        )
      }
    >
      {keys.data!.length === 0 ? (
        <div className="p-5">
          <Empty icon={<KeyRound />} title="No SSH keys" description="Generate a key to connect remote servers." />
        </div>
      ) : (
        <div className="divide-y divide-border">
          {keys.data!.map((k) => (
            <div key={k.id} className="flex items-center gap-4 px-5 py-3">
              <KeyRound className="size-4 text-muted" />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{k.name}</div>
                <CopyField value={k.public_key} />
              </div>
              {user.role !== "member" && (
                <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: `Delete key ${k.name}?`, danger: true, confirmText: "Delete" })) && del.mutate(k.id)}>
                  <Trash2 className="size-4 text-danger" />
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      <Modal
        open={!!f}
        onClose={() => setF(null)}
        title="Add SSH key"
        footer={
          <Button variant="primary" loading={create.isPending} disabled={!f?.name} onClick={() => create.mutate()}>
            {f?.private_key ? "Import" : "Generate"}
          </Button>
        }
      >
        {f && (
          <div className="space-y-4">
            <Field label="Name">
              <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} autoFocus />
            </Field>
            <Field label="Private key" hint="Leave empty to generate a new Ed25519 key.">
              <textarea
                className="h-32 w-full rounded-lg border border-border bg-bg px-3 py-2 font-mono text-xs focus:border-accent focus:outline-none"
                value={f.private_key}
                onChange={(e) => setF({ ...f, private_key: e.target.value })}
              />
            </Field>
          </div>
        )}
      </Modal>
    </Card>
  );
}

function Tokens() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: () => api.get<APIToken[]>("/tokens") });
  const [name, setName] = useState("");
  const [created, setCreated] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.post<{ token: string }>("/tokens", { name }),
    onSuccess: (r) => {
      setCreated(r.token);
      setName("");
      qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  if (tokens.isLoading) return <Loading />;
  return (
    <div className="space-y-6">
      <Card title="API tokens" description="Use with Authorization: Bearer <token> to automate PortDock from scripts or CI.">
        <form
          className="flex gap-2 border-b border-border p-5"
          onSubmit={(e) => {
            e.preventDefault();
            if (name) create.mutate();
          }}
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Token name, e.g. github-actions" className="max-w-sm" />
          <Button type="submit" variant="primary" icon={<Plus className="size-4" />} loading={create.isPending} disabled={!name}>
            Create
          </Button>
        </form>
        {created && (
          <div className="border-b border-border bg-success/5 p-5">
            <p className="mb-2 text-sm font-medium text-success">Copy your token now — it won't be shown again.</p>
            <CopyField value={created} />
          </div>
        )}
        {tokens.data!.length === 0 ? (
          <p className="p-5 text-sm text-muted">No tokens.</p>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Created</th>
                <th>Last used</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {tokens.data!.map((t) => (
                <tr key={t.id}>
                  <td className="font-medium">{t.name}</td>
                  <td className="text-xs text-muted">{timeAgo(t.created_at)}</td>
                  <td className="text-xs text-muted">{timeAgo(t.last_used_at)}</td>
                  <td className="text-right">
                    <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: `Revoke ${t.name}?`, danger: true, confirmText: "Revoke" })) && del.mutate(t.id)}>
                      <Trash2 className="size-4 text-danger" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title="Example">
        <pre className="overflow-x-auto p-5 font-mono text-xs text-muted">{`curl -X POST ${location.origin}/api/applications/<id>/deploy \\
  -H "Authorization: Bearer sy_…" \\
  -H "Content-Type: application/json" \\
  -d '{"force_rebuild": false}'`}</pre>
      </Card>
    </div>
  );
}

const channelFields: Record<NotificationChannel["type"], { key: string; label: string; secret?: boolean; placeholder?: string }[]> = {
  discord: [{ key: "webhook_url", label: "Webhook URL", secret: true, placeholder: "https://discord.com/api/webhooks/…" }],
  slack: [{ key: "webhook_url", label: "Webhook URL", secret: true, placeholder: "https://hooks.slack.com/services/…" }],
  telegram: [
    { key: "bot_token", label: "Bot token", secret: true },
    { key: "chat_id", label: "Chat ID" },
  ],
  webhook: [
    { key: "url", label: "URL", placeholder: "https://example.com/hooks/portdock" },
    { key: "secret", label: "Signing secret (optional)", secret: true },
  ],
  email: [
    { key: "host", label: "SMTP host" },
    { key: "port", label: "SMTP port", placeholder: "587" },
    { key: "username", label: "Username" },
    { key: "password", label: "Password", secret: true },
    { key: "from", label: "From" },
    { key: "to", label: "Recipients (comma separated)" },
  ],
};

const allEvents = ["deployment.success", "deployment.failed", "deployment.approval_required", "backup.failed", "server.unreachable", "server.reachable", "alert.firing", "alert.resolved", "certificate.expiring", "certificate.failed", "certificate.renewed"];

function Notifications() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const list = useQuery({ queryKey: ["notifications"], queryFn: () => api.get<NotificationChannel[]>("/notifications") });
  const [edit, setEdit] = useState<Partial<NotificationChannel> | null>(null);
  const save = useMutation({
    mutationFn: (c: Partial<NotificationChannel>) => (c.id ? api.patch(`/notifications/${c.id}`, c) : api.post("/notifications", c)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifications"] });
      setEdit(null);
      toast.success("Channel saved");
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: (id: number) => api.post(`/notifications/${id}/test`),
    onSuccess: () => toast.success("Test notification sent"),
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/notifications/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notifications"] }),
  });
  if (list.isLoading) return <Loading />;
  const events = (edit?.events ?? allEvents.join(",")).split(",").filter(Boolean);
  return (
    <Card
      title="Notification channels"
      description="Get notified about deployments, backups and server health."
      actions={
        <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEdit({ type: "discord", name: "", enabled: true, events: allEvents.join(","), config: {} })}>
          Add channel
        </Button>
      }
    >
      {list.data!.length === 0 ? (
        <div className="p-5">
          <Empty icon={<Bell />} title="No channels" description="Connect Discord, Slack, Telegram, email or a webhook." />
        </div>
      ) : (
        <div className="divide-y divide-border">
          {list.data!.map((c) => (
            <div key={c.id} className="flex flex-wrap items-center gap-4 px-5 py-3">
              <Badge>{c.type}</Badge>
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{c.name}</div>
                <div className="truncate text-xs text-muted">{c.events.split(",").join(" · ")}</div>
              </div>
              {!c.enabled && <Badge>disabled</Badge>}
              <Button size="sm" icon={<Send className="size-3.5" />} loading={test.isPending && test.variables === c.id} onClick={() => test.mutate(c.id)}>
                Test
              </Button>
              <Button size="icon" variant="ghost" onClick={() => setEdit(c)}>
                <Settings2 className="size-4" />
              </Button>
              <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: `Delete ${c.name}?`, danger: true, confirmText: "Delete" })) && del.mutate(c.id)}>
                <Trash2 className="size-4 text-danger" />
              </Button>
            </div>
          ))}
        </div>
      )}
      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        title={edit?.id ? "Edit channel" : "New channel"}
        footer={
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate(edit!)}>
            Save
          </Button>
        }
      >
        {edit && (
          <div className="space-y-4">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="Name">
                <Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="#deployments" />
              </Field>
              <Field label="Type">
                <Select value={edit.type} disabled={!!edit.id} onChange={(e) => setEdit({ ...edit, type: e.target.value as NotificationChannel["type"], config: {} })}>
                  {Object.keys(channelFields).map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            {channelFields[edit.type!].map((fld) => (
              <Field key={fld.key} label={fld.label}>
                {fld.secret ? (
                  <PasswordInput value={edit.config?.[fld.key] ?? ""} placeholder={fld.placeholder} onChange={(e) => setEdit({ ...edit, config: { ...edit.config, [fld.key]: e.target.value } })} />
                ) : (
                  <Input value={edit.config?.[fld.key] ?? ""} placeholder={fld.placeholder} onChange={(e) => setEdit({ ...edit, config: { ...edit.config, [fld.key]: e.target.value } })} />
                )}
              </Field>
            ))}
            <div>
              <div className="mb-2 text-xs font-medium uppercase tracking-wide text-muted">Events</div>
              <div className="grid gap-2 sm:grid-cols-2">
                {allEvents.map((ev) => (
                  <Switch
                    key={ev}
                    checked={events.includes(ev)}
                    onChange={(on) => setEdit({ ...edit, events: (on ? [...events, ev] : events.filter((x) => x !== ev)).join(",") })}
                    label={<code className="text-xs">{ev}</code>}
                  />
                ))}
              </div>
            </div>
            <Switch checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Enabled" />
          </div>
        )}
      </Modal>
    </Card>
  );
}

function Storage() {
  const qc = useQueryClient();
  const { data, isLoading } = useSettings();
  const [f, setF] = useState({ s3_endpoint: "", s3_bucket: "", s3_region: "", s3_access_key: "", s3_secret_key: "" });
  useEffect(() => {
    if (data) setF({ s3_endpoint: data.s3_endpoint, s3_bucket: data.s3_bucket, s3_region: data.s3_region, s3_access_key: data.s3_access_key, s3_secret_key: "" });
  }, [data]);
  const save = useMutation({
    mutationFn: () => api.patch<Settings>("/settings", f),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      toast.success("S3 destination saved");
    },
    onError: (e) => toast.error(e.message),
  });
  if (isLoading) return <Loading />;
  return (
    <Card title="S3 backup destination" description="AWS S3, Cloudflare R2, Backblaze B2, MinIO or any S3-compatible storage. Enable 'Upload to S3' per backup schedule.">
      <div className="grid gap-4 p-5 md:grid-cols-2">
        <Field label="Endpoint" hint="Leave empty for AWS S3.">
          <Input value={f.s3_endpoint} onChange={(e) => setF({ ...f, s3_endpoint: e.target.value })} placeholder="https://<account>.r2.cloudflarestorage.com" />
        </Field>
        <Field label="Bucket">
          <Input value={f.s3_bucket} onChange={(e) => setF({ ...f, s3_bucket: e.target.value })} />
        </Field>
        <Field label="Region">
          <Input value={f.s3_region} onChange={(e) => setF({ ...f, s3_region: e.target.value })} placeholder="us-east-1 / auto" />
        </Field>
        <Field label="Access key ID">
          <Input value={f.s3_access_key} onChange={(e) => setF({ ...f, s3_access_key: e.target.value })} />
        </Field>
        <Field label="Secret access key" hint={data!.s3_secret_key_set ? "A secret is stored; enter a new one to replace it." : undefined}>
          <PasswordInput value={f.s3_secret_key} onChange={(e) => setF({ ...f, s3_secret_key: e.target.value })} placeholder={data!.s3_secret_key_set ? "••••••••" : ""} />
        </Field>
        <div className="flex items-end justify-end">
          <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
            Save
          </Button>
        </div>
      </div>
    </Card>
  );
}
