import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ArrowUpCircle, Ban, Bell, Bot, Boxes, FileKey2, Cloud, GitBranch, Link2, Link2Off, Network, Eye, Info, EyeOff, KeyRound, LockKeyhole, LogOut, MoreHorizontal, Plus, RefreshCw, Save, Send, Settings2, ShieldCheck, ShieldOff, Terminal as TerminalIcon, Trash2, User as UserIcon, Users } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { APIToken, DNSStatus, LoginProviders, NotificationChannel, Settings, SSHKey, User, UserIdentity } from "../lib/types";
import { DNSBadge } from "../components/Domains";
import { useUser } from "../components/Layout";
import { GitSourcesSettings } from "../components/github";
import { AzureDevOpsSettings } from "../components/azure";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Loading, Menu, MenuItem, MenuSeparator, Modal, PageHeader, PasswordInput, Select, Switch, Table, useConfirm } from "../components/ui";
import { SecuritySettings } from "../components/security";
import { AuthenticationSettings } from "../components/sso";
import { RegistriesSettings } from "../components/registries";
import { AuditLog } from "../components/audit";
import { CertificatesSettings } from "../components/certificates";
import { UpdateSettings } from "../components/updates";
import { AssistantSettings } from "../components/assistant/AssistantSettings";
import { AboutPage } from "../components/about";
import { NetworkSettings } from "../components/network";
import { timeAgo } from "../lib/utils";
import { isLocale, LOCALES, t, tk, useLocale } from "../lib/i18n";
import { useChangeLocale } from "../components/LanguagePicker";

type Tab = "about" | "general" | "profile" | "security" | "auth" | "assistant" | "registries" | "network" | "certificates" | "updates" | "users" | "git" | "keys" | "tokens" | "notifications" | "storage" | "activity";

export function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const user0 = useUser();
  const tab = (params.get("tab") as Tab) || (user0.role !== "member" ? "general" : "profile");
  const user = useUser();
  const admin = user.role !== "member";
  const groups: { label: string; items: { id: Tab; label: string; icon: React.ReactNode; hidden?: boolean }[] }[] = [
    {
      label: t("Account"),
      items: [
        { id: "profile", label: t("Profile"), icon: <UserIcon /> },
        { id: "security", label: t("Sign-in & security"), icon: <ShieldCheck /> },
        { id: "tokens", label: t("API tokens"), icon: <TerminalIcon /> },
      ],
    },
    {
      label: t("Organization"),
      items: [
        { id: "general", label: t("General"), icon: <Settings2 />, hidden: !admin },
        { id: "users", label: t("Team members"), icon: <Users />, hidden: !admin },
        { id: "auth", label: t("Authentication"), icon: <LockKeyhole />, hidden: !admin },
        { id: "assistant", label: t("AI assistant"), icon: <Bot />, hidden: !admin },
        { id: "git", label: t("Git sources"), icon: <GitBranch />, hidden: !admin },
        { id: "network", label: t("Network & proxy"), icon: <Network />, hidden: !admin },
        { id: "registries", label: t("Container registries"), icon: <Boxes />, hidden: !admin },
        { id: "certificates", label: t("Certificates"), icon: <FileKey2 />, hidden: !admin },
        { id: "updates", label: t("Updates"), icon: <ArrowUpCircle />, hidden: !admin },
        { id: "notifications", label: t("Notifications"), icon: <Bell />, hidden: !admin },
        { id: "storage", label: t("Backup storage"), icon: <Cloud />, hidden: !admin },
      ],
    },
    {
      label: t("Security"),
      items: [
        { id: "keys", label: t("SSH keys"), icon: <KeyRound /> },
        { id: "activity", label: t("Audit log"), icon: <Activity /> },
      ],
    },
    {
      label: "PortDock",
      items: [{ id: "about", label: t("About"), icon: <Info /> }],
    },
  ];
  return (
    <>
      <PageHeader title={t("Settings")} subtitle={t("Manage your account, organization and security preferences.")} />
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
          {tab === "assistant" && <AssistantSettings />}
          {tab === "about" && <AboutPage />}
          {tab === "registries" && <RegistriesSettings />}
          {tab === "network" && <NetworkSettings />}
          {tab === "certificates" && <CertificatesSettings />}
          {tab === "updates" && <UpdateSettings />}
          {tab === "general" && <General />}
          {tab === "users" && <Team />}
          {tab === "git" && (
            <div className="space-y-6">
              <GitSourcesSettings />
              <AzureDevOpsSettings />
            </div>
          )}
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
      toast.success(t("Profile updated"));
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="space-y-6">
      <Card title={t("Your profile")}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </Field>
          <Field label={t("Email")}>
            <Input value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} />
          </Field>
          {user.has_password ? (
            <Field label={t("Current password")} hint={t("Required to set a new password. Other sessions are signed out after a change.")}>
              <PasswordInput value={f.current_password} onChange={(e) => setF({ ...f, current_password: e.target.value })} />
            </Field>
          ) : (
            <Field label={t("Password")} hint={t("You sign in with single sign-on. Set a password only if your administrator allows password sign-in.")}>
              <Input value={t("Not set")} disabled />
            </Field>
          )}
          <Field label={t("New password")}>
            <PasswordInput value={f.new_password} onChange={(e) => setF({ ...f, new_password: e.target.value })} />
          </Field>
          <div className="flex justify-end md:col-span-2">
            <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        </div>
      </Card>
      <SingleSignOnCard user={user} />
      <LanguageCard />
    </div>
  );
}

/** The identity providers connected to the account: the only way an owner or administrator gets single sign-on. */
function SingleSignOnCard({ user }: { user: User }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [params, setParams] = useSearchParams();
  const providers = useQuery({ queryKey: ["login-providers"], queryFn: () => api.get<LoginProviders>("/auth/providers"), staleTime: 60_000 });
  const identities = useQuery({ queryKey: ["identities"], queryFn: () => api.get<UserIdentity[]>("/auth/identities") });
  const unlink = useMutation({
    mutationFn: (id: number) => api.del(`/auth/identities/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["identities"] });
      toast.success(t("Single sign-on disconnected"));
    },
    onError: (e) => toast.error(e.message),
  });
  useEffect(() => {
    const linked = params.get("sso_linked");
    const error = params.get("sso_error");
    if (!linked && !error) return;
    if (linked) toast.success(t("{provider} connected to your account", { provider: linked }));
    if (error) toast.error(error);
    params.delete("sso_linked");
    params.delete("sso_error");
    setParams(params, { replace: true });
    qc.invalidateQueries({ queryKey: ["identities"] });
  }, [params, setParams, qc]);
  const list = providers.data?.providers ?? [];
  if (list.length === 0) return null;
  const onlyWay = !user.has_password && !user.auth_source.startsWith("ldap:") && (identities.data?.length ?? 0) <= 1;
  return (
    <Card title={t("Single sign-on")} description={t("Connect an identity provider to sign in to this account with it. Providers are never connected automatically to owner and administrator accounts.")}>
      <div className="divide-y divide-border">
        {list.map((p) => {
          const link = identities.data?.find((i) => i.slug === p.slug);
          return (
            <div key={p.slug} className="flex flex-wrap items-center gap-3 px-5 py-3">
              <div className="min-w-0 flex-1">
                <div className="font-medium">{p.name}</div>
                <div className="text-xs text-muted">{link ? t("Connected as {email}", { email: link.email }) : t("Not connected")}</div>
              </div>
              {link ? (
                <Button
                  size="sm"
                  icon={<Link2Off className="size-3.5" />}
                  disabled={onlyWay}
                  title={onlyWay ? t("This is the only way to sign in to your account; set a password first.") : undefined}
                  loading={unlink.isPending}
                  onClick={async () => (await confirm({ title: t("Disconnect {provider}?", { provider: p.name }), confirmText: t("Disconnect") })) && unlink.mutate(link.id)}
                >
                  {t("Disconnect")}
                </Button>
              ) : (
                <Button size="sm" icon={<Link2 className="size-3.5" />} onClick={() => (location.href = `/api/auth/sso/${p.slug}/start?link=1`)}>
                  {t("Connect")}
                </Button>
              )}
            </div>
          );
        })}
      </div>
    </Card>
  );
}

function LanguageCard() {
  const locale = useLocale();
  const change = useChangeLocale();
  return (
    <Card title={t("Interface language")} description={t("Saved for your account and used on every device.")}>
      <div className="p-5">
        <Field label={t("Language")}>
          <Select className="max-w-xs" value={locale} onChange={(e) => isLocale(e.target.value) && change(e.target.value)}>
            {LOCALES.map((l) => (
              <option key={l.id} value={l.id}>
                {l.label}
              </option>
            ))}
          </Select>
        </Field>
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
      if (s.instance_route_error) toast.warning(t("Settings saved, but the dashboard domain could not be routed"), { description: s.instance_route_error });
      else toast.success(t("Settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const detect = useMutation({
    mutationFn: () => api.post<{ public_ipv4: string; public_ipv6: string }>("/settings/detect-ip"),
    onSuccess: (r) => {
      setF((x) => ({ ...x, public_ipv4: r.public_ipv4 || x.public_ipv4, public_ipv6: r.public_ipv6 || x.public_ipv6 }));
      toast.success(r.public_ipv4 || r.public_ipv6 ? t("Public addresses detected — save to apply") : t("Could not detect a public address"));
    },
    onError: (e) => toast.error(e.message),
  });

  if (isLoading) return <Loading />;
  const dirty = JSON.stringify(f) !== JSON.stringify(data);

  return (
    <div className="space-y-6">
      <Card title={t("General")} description={t("How this PortDock instance is identified and reached.")}>
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field
            label={t("URL")}
            hint={
              <>
                {t("The dashboard is served on this domain by Traefik with a Let's Encrypt certificate. Point its DNS record to this server.")}
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
          <Field label={t("Redirect HTTP to HTTPS")} hint={t("Permanently redirect plain HTTP requests to the dashboard.")}>
            <Select value={f.instance_https === "false" ? "off" : "on"} onChange={(e) => setF({ ...f, instance_https: e.target.value === "on" ? "true" : "false" })}>
              <option value="on">{t("Enabled")}</option>
              <option value="off">{t("Disabled")}</option>
            </Select>
          </Field>
          <Field label={t("Name")} hint={t("Shown in notifications and the browser title.")}>
            <Input value={f.instance_name ?? ""} onChange={(e) => setF({ ...f, instance_name: e.target.value })} placeholder="PortDock" />
          </Field>
          <Field label={t("Instance timezone")} hint={t("Used for scheduled jobs such as database backups.")}>
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
          <div className="border-t border-border bg-warning-soft px-5 py-3 text-xs text-warning">{t("Dashboard routing: {error}", { error: data!.instance_route_error })}</div>
        )}
      </Card>

      <Card
        title={t("Network addresses")}
        description={t("Public addresses of this server, used to verify DNS records and generate the records to create.")}
        actions={
          <Button size="sm" icon={<RefreshCw />} loading={detect.isPending} onClick={() => detect.mutate()}>
            {t("Detect")}
          </Button>
        }
      >
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field label={t("Instance public IPv4")}>
            <div className="relative">
              <Input className="pr-9 font-mono text-xs" type={showV4 ? "text" : "password"} value={f.public_ipv4 ?? ""} onChange={(e) => setF({ ...f, public_ipv4: e.target.value.trim() })} placeholder="203.0.113.10" />
              <button type="button" onClick={() => setShowV4(!showV4)} className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-fg">
                {showV4 ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </button>
            </div>
          </Field>
          <Field label={t("Instance public IPv6")}>
            <div className="relative">
              <Input className="pr-9 font-mono text-xs" type={showV6 ? "text" : "password"} value={f.public_ipv6 ?? ""} onChange={(e) => setF({ ...f, public_ipv6: e.target.value.trim() })} placeholder="2001:db8::1" />
              <button type="button" onClick={() => setShowV6(!showV6)} className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-fg">
                {showV6 ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </button>
            </div>
          </Field>
        </div>
      </Card>

      <Card title={t("Proxy & maintenance")}>
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field label={t("Let's Encrypt email")} hint={t("Receives certificate expiry notices. Recreate proxies after changing.")}>
            <Input value={f.acme_email ?? ""} onChange={(e) => setF({ ...f, acme_email: e.target.value })} placeholder="ops@example.com" />
          </Field>
          <Field label={t("Version")}>
            <Input value={data!.version} disabled />
          </Field>
          <Switch checked={f.auto_cleanup !== "false"} onChange={(v) => setF({ ...f, auto_cleanup: v ? "true" : "false" })} label={t("Automatic cleanup")} description={t("Daily prune of build cache (>72h) and dangling images on all servers.")} />
          <Switch checked={f.proxy_dashboard === "true"} onChange={(v) => setF({ ...f, proxy_dashboard: v ? "true" : "false" })} label={t("Traefik dashboard")} description={t("Expose the Traefik dashboard on 127.0.0.1:8080 of each server.")} />
        </div>
      </Card>

      <div className="sticky bottom-4 z-10 flex justify-end">
        <Button variant="primary" icon={<Save className="size-4" />} disabled={!dirty} loading={save.isPending} onClick={() => save.mutate()} className="shadow-pop">
          {t("Save settings")}
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
  const [linkUser, setLinkUser] = useState<User | null>(null);
  const [linkDir, setLinkDir] = useState("");
  const dirs = useQuery({ queryKey: ["ldap"], queryFn: () => api.get<{ directories: { slug: string; name: string }[] }>("/ldap/directories"), enabled: !!linkUser });
  const create = useMutation({
    mutationFn: () => api.post("/users", invite),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      setInvite(null);
      toast.success(t("User created — share the credentials securely"));
    },
    onError: (e) => toast.error(e.message),
  });
  const update = useMutation({
    mutationFn: ({ id, ...body }: { id: number; role?: string; disabled?: boolean; auth_source?: string }) => api.patch(`/users/${id}`, body),
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
      title={t("Team members")}
      description={t("Admins manage servers, settings and users. Members manage projects and resources.")}
      actions={
        <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setInvite({ name: "", email: "", password: "", role: "member" })}>
          {t("Add member")}
        </Button>
      }
    >
      <Table>
        <thead>
          <tr>
            <th>{t("User")}</th>
            <th>{t("Role")}</th>
            <th>{t("Security")}</th>
            <th>{t("Last sign-in")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {users.data!.map((u) => (
            <tr key={u.id}>
              <td className={u.disabled ? "opacity-60" : undefined}>
                <div className="font-medium">
                  {u.name} {u.id === me.id && <Badge>{t("you")}</Badge>} {u.disabled && <Badge tone="danger">{t("Disabled")}</Badge>}
                </div>
                <div className="text-xs text-muted">{u.email}</div>
              </td>
              <td>
                {u.role === "owner" ? (
                  <Badge tone="accent">{t("Owner")}</Badge>
                ) : (
                  <Select value={u.role} onChange={(e) => update.mutate({ id: u.id, role: e.target.value })} className="h-8 w-32">
                    <option value="admin">{t("admin")}</option>
                    <option value="member">{t("member")}</option>
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
                      <ShieldOff /> {t("No 2FA")}
                    </Badge>
                  )}
                  {u.auth_source && <Badge tone="info">SSO · {u.auth_source}</Badge>}
                </div>
              </td>
              <td className="text-xs text-muted">{u.last_login_at ? timeAgo(u.last_login_at) : t("Never")}</td>
              <td className="text-right">
                {u.id !== me.id && (u.role !== "owner" || me.role === "owner") && (
                  <Menu
                    trigger={
                      <Button size="icon-sm" variant="ghost" aria-label={t("Actions")}>
                        <MoreHorizontal />
                      </Button>
                    }
                  >
                    <MenuItem
                      icon={<LogOut />}
                      onClick={async () =>
                        (await confirm({ title: t("Sign {name} out everywhere?", { name: u.name }), confirmText: t("Sign out") })) && action.mutate({ id: u.id, path: "sessions/revoke", done: t("All sessions signed out") })
                      }
                    >
                      {t("Sign out all sessions")}
                    </MenuItem>
                    {u.totp_enabled && (
                      <MenuItem
                        icon={<ShieldOff />}
                        onClick={async () =>
                          (await confirm({ title: t("Reset 2FA for {email}?", { email: u.email }), description: t("Use this when the user lost their device. They will be signed out and can enroll again."), danger: true, confirmText: t("Reset 2FA") })) &&
                          action.mutate({ id: u.id, path: "2fa/reset", done: t("Two-factor authentication reset") })
                        }
                      >
                        {t("Reset two-factor")}
                      </MenuItem>
                    )}
                    {u.auth_source.startsWith("ldap:") ? (
                      <MenuItem icon={<Link2Off />} onClick={() => update.mutate({ id: u.id, auth_source: "" })}>
                        {t("Unlink from directory")}
                      </MenuItem>
                    ) : (
                      !u.auth_source && (
                        <MenuItem
                          icon={<Link2 />}
                          onClick={() => {
                            setLinkDir("");
                            setLinkUser(u);
                          }}
                        >
                          {t("Link to directory…")}
                        </MenuItem>
                      )
                    )}
                    {u.role !== "owner" && (
                      <>
                        <MenuItem icon={<Ban />} onClick={() => update.mutate({ id: u.id, disabled: !u.disabled })}>
                          {u.disabled ? t("Enable account") : t("Disable account")}
                        </MenuItem>
                        <MenuSeparator />
                        <MenuItem icon={<Trash2 />} danger onClick={async () => (await confirm({ title: t("Remove {name}?", { name: u.email }), danger: true, confirmText: t("Remove") })) && del.mutate(u.id)}>
                          {t("Remove member")}
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
        open={!!linkUser}
        onClose={() => setLinkUser(null)}
        title={t("Link {email} to a directory", { email: linkUser?.email ?? "" })}
        footer={
          <Button
            variant="primary"
            disabled={!linkDir}
            loading={update.isPending}
            onClick={() => {
              if (linkUser) update.mutate({ id: linkUser.id, auth_source: `ldap:${linkDir}` });
              setLinkUser(null);
            }}
          >
            {t("Link")}
          </Button>
        }
      >
        <Field label={t("Directory")} hint={t("The user then signs in with the directory password; the e-mail of the directory account must match.")}>
          <Select value={linkDir} onChange={(e) => setLinkDir(e.target.value)}>
            <option value="">{t("Choose…")}</option>
            {(dirs.data?.directories ?? []).map((d) => (
              <option key={d.slug} value={d.slug}>
                {d.name}
              </option>
            ))}
          </Select>
        </Field>
      </Modal>
      <Modal
        open={!!invite}
        onClose={() => setInvite(null)}
        title={t("Add team member")}
        footer={
          <Button variant="primary" loading={create.isPending} onClick={() => create.mutate()}>
            {t("Create user")}
          </Button>
        }
      >
        {invite && (
          <div className="grid gap-4 md:grid-cols-2">
            <Field label={t("Name")}>
              <Input value={invite.name} onChange={(e) => setInvite({ ...invite, name: e.target.value })} />
            </Field>
            <Field label={t("Email")}>
              <Input type="email" value={invite.email} onChange={(e) => setInvite({ ...invite, email: e.target.value })} />
            </Field>
            <Field label={t("Initial password")} hint={t("At least 8 characters.")}>
              <PasswordInput value={invite.password} onChange={(e) => setInvite({ ...invite, password: e.target.value })} />
            </Field>
            <Field label={t("Role")}>
              <Select value={invite.role} onChange={(e) => setInvite({ ...invite, role: e.target.value })}>
                <option value="member">{t("Member")}</option>
                <option value="admin">{t("Admin")}</option>
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
      title={t("SSH keys")}
      description={t("Private keys are encrypted at rest and never leave PortDock.")}
      actions={
        user.role !== "member" && (
          <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setF({ name: "", private_key: "" })}>
            {t("Add key")}
          </Button>
        )
      }
    >
      {keys.data!.length === 0 ? (
        <div className="p-5">
          <Empty icon={<KeyRound />} title={t("No SSH keys")} description={t("Generate a key to connect remote servers.")} />
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
                <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: t("Delete key {name}?", { name: k.name }), danger: true, confirmText: t("Delete") })) && del.mutate(k.id)}>
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
        title={t("Add SSH key")}
        footer={
          <Button variant="primary" loading={create.isPending} disabled={!f?.name} onClick={() => create.mutate()}>
            {f?.private_key ? t("Import") : t("Generate")}
          </Button>
        }
      >
        {f && (
          <div className="space-y-4">
            <Field label={t("Name")}>
              <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} autoFocus />
            </Field>
            <Field label={t("Private key")} hint={t("Leave empty to generate a new Ed25519 key.")}>
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
      <Card title={t("API tokens")} description={t("Use with Authorization: Bearer <token> to automate PortDock from scripts or CI.")}>
        <form
          className="flex gap-2 border-b border-border p-5"
          onSubmit={(e) => {
            e.preventDefault();
            if (name) create.mutate();
          }}
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("Token name, e.g. github-actions")} className="max-w-sm" />
          <Button type="submit" variant="primary" icon={<Plus className="size-4" />} loading={create.isPending} disabled={!name}>
            {t("Create")}
          </Button>
        </form>
        {created && (
          <div className="border-b border-border bg-success/5 p-5">
            <p className="mb-2 text-sm font-medium text-success">{t("Copy your token now — it won't be shown again.")}</p>
            <CopyField value={created} />
          </div>
        )}
        {tokens.data!.length === 0 ? (
          <p className="p-5 text-sm text-muted">{t("No tokens.")}</p>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Name")}</th>
                <th>{t("Created")}</th>
                <th>{t("Last used")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {tokens.data!.map((tok) => (
                <tr key={tok.id}>
                  <td className="font-medium">{tok.name}</td>
                  <td className="text-xs text-muted">{timeAgo(tok.created_at)}</td>
                  <td className="text-xs text-muted">{timeAgo(tok.last_used_at)}</td>
                  <td className="text-right">
                    <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: t("Revoke {name}?", { name: tok.name }), danger: true, confirmText: t("Revoke") })) && del.mutate(tok.id)}>
                      <Trash2 className="size-4 text-danger" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title={t("Example")}>
        <pre className="overflow-x-auto p-5 font-mono text-xs text-muted">{`curl -X POST ${location.origin}/api/applications/<id>/deploy \\
  -H "Authorization: Bearer sy_…" \\
  -H "Content-Type: application/json" \\
  -d '{"force_rebuild": false}'`}</pre>
      </Card>
    </div>
  );
}

const channelFields: Record<NotificationChannel["type"], { key: string; label: string; secret?: boolean; placeholder?: string; hint?: string }[]> = {
  discord: [{ key: "webhook_url", label: tk("Webhook URL"), secret: true, placeholder: "https://discord.com/api/webhooks/…" }],
  slack: [{ key: "webhook_url", label: tk("Webhook URL"), secret: true, placeholder: "https://hooks.slack.com/services/…" }],
  teams: [
    {
      key: "webhook_url",
      label: tk("Webhook URL"),
      secret: true,
      placeholder: "https://….logic.azure.com/workflows/…",
      hint: tk("In the Teams channel: ⋯ → Workflows → “Post to a channel when a webhook request is received”, then copy the URL. Incoming webhook URLs work too."),
    },
  ],
  telegram: [
    { key: "bot_token", label: tk("Bot token"), secret: true },
    { key: "chat_id", label: tk("Chat ID") },
  ],
  webhook: [
    { key: "url", label: tk("URL"), placeholder: "https://example.com/hooks/portdock" },
    { key: "secret", label: tk("Signing secret (optional)"), secret: true },
  ],
  email: [
    { key: "host", label: tk("SMTP host") },
    { key: "port", label: tk("SMTP port"), placeholder: "587" },
    { key: "username", label: tk("Username") },
    { key: "password", label: tk("Password"), secret: true },
    { key: "from", label: tk("From") },
    { key: "to", label: tk("Recipients (comma separated)") },
  ],
};

const channelTypeLabels: Record<string, string> = { teams: "Microsoft Teams", webhook: tk("webhook"), email: tk("email") };

const allEvents = ["deployment.success", "deployment.failed", "deployment.approval_required", "backup.failed", "server.unreachable", "server.reachable", "alert.firing", "alert.resolved", "certificate.expiring", "certificate.failed", "certificate.renewed", "security.vulnerabilities", "monitor.down", "monitor.up", "monitor.ssl_expiring"];

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
      toast.success(t("Channel saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: (id: number) => api.post(`/notifications/${id}/test`),
    onSuccess: () => toast.success(t("Test notification sent")),
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
      title={t("Notification channels")}
      description={t("Get notified about deployments, backups and server health.")}
      actions={
        <Button size="sm" variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEdit({ type: "discord", name: "", enabled: true, events: allEvents.join(","), config: {} })}>
          {t("Add channel")}
        </Button>
      }
    >
      {list.data!.length === 0 ? (
        <div className="p-5">
          <Empty icon={<Bell />} title={t("No channels")} description={t("Connect Discord, Slack, Telegram, email or a webhook.")} />
        </div>
      ) : (
        <div className="divide-y divide-border">
          {list.data!.map((c) => (
            <div key={c.id} className="flex flex-wrap items-center gap-4 px-5 py-3">
              <Badge>{t(channelTypeLabels[c.type] ?? c.type)}</Badge>
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{c.name}</div>
                <div className="truncate text-xs text-muted">{c.events.split(",").join(" · ")}</div>
              </div>
              {!c.enabled && <Badge>{t("disabled")}</Badge>}
              <Button size="sm" icon={<Send className="size-3.5" />} loading={test.isPending && test.variables === c.id} onClick={() => test.mutate(c.id)}>
                {t("Test")}
              </Button>
              <Button size="icon" variant="ghost" onClick={() => setEdit(c)}>
                <Settings2 className="size-4" />
              </Button>
              <Button size="icon" variant="ghost" onClick={async () => (await confirm({ title: t("Delete {name}?", { name: c.name }), danger: true, confirmText: t("Delete") })) && del.mutate(c.id)}>
                <Trash2 className="size-4 text-danger" />
              </Button>
            </div>
          ))}
        </div>
      )}
      <Modal
        open={!!edit}
        onClose={() => setEdit(null)}
        title={edit?.id ? t("Edit channel") : t("New channel")}
        footer={
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate(edit!)}>
            {t("Save")}
          </Button>
        }
      >
        {edit && (
          <div className="space-y-4">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label={t("Name")}>
                <Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="#deployments" />
              </Field>
              <Field label={t("Type")}>
                <Select value={edit.type} disabled={!!edit.id} onChange={(e) => setEdit({ ...edit, type: e.target.value as NotificationChannel["type"], config: {} })}>
                  {Object.keys(channelFields).map((ty) => (
                    <option key={ty} value={ty}>
                      {t(channelTypeLabels[ty] ?? ty)}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            {channelFields[edit.type!].map((fld) => (
              <Field key={fld.key} label={t(fld.label)} hint={fld.hint ? t(fld.hint) : undefined}>
                {fld.secret ? (
                  <PasswordInput value={edit.config?.[fld.key] ?? ""} placeholder={fld.placeholder} onChange={(e) => setEdit({ ...edit, config: { ...edit.config, [fld.key]: e.target.value } })} />
                ) : (
                  <Input value={edit.config?.[fld.key] ?? ""} placeholder={fld.placeholder} onChange={(e) => setEdit({ ...edit, config: { ...edit.config, [fld.key]: e.target.value } })} />
                )}
              </Field>
            ))}
            <div>
              <div className="mb-2 text-xs font-medium uppercase tracking-wide text-muted">{t("Events")}</div>
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
            <Switch checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label={t("Enabled")} />
          </div>
        )}
      </Modal>
    </Card>
  );
}

/** Queues a manual backup of every database of the instance. */
function BackupAll() {
  const all = useMutation({
    mutationFn: () => api.post<{ queued: number }>("/databases/backup-all"),
    onSuccess: (r) => toast.success(t("{n} database backup queued|{n} database backups queued", { n: r.queued }), { description: t("Follow them in the Backups tab of each database.") }),
    onError: (e) => toast.error(e.message),
  });
  return (
    <Card
      title={t("Back up all databases")}
      description={t("Queues a backup of every database now. Each is stored on its own server and can be downloaded from the database's Backups tab.")}
      actions={
        <Button size="sm" variant="primary" loading={all.isPending} onClick={() => all.mutate()}>
          {t("Back up all now")}
        </Button>
      }
    />
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
      toast.success(t("S3 destination saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (isLoading) return <Loading />;
  return (
    <div className="space-y-6">
    <BackupAll />
    <Card title={t("S3 backup destination")} description={t("AWS S3, Cloudflare R2, Backblaze B2, MinIO or any S3-compatible storage. Enable 'Upload to S3' per backup schedule.")}>
      <div className="grid gap-4 p-5 md:grid-cols-2">
        <Field label={t("Endpoint")} hint={t("Leave empty for AWS S3.")}>
          <Input value={f.s3_endpoint} onChange={(e) => setF({ ...f, s3_endpoint: e.target.value })} placeholder="https://<account>.r2.cloudflarestorage.com" />
        </Field>
        <Field label={t("Bucket")}>
          <Input value={f.s3_bucket} onChange={(e) => setF({ ...f, s3_bucket: e.target.value })} />
        </Field>
        <Field label={t("Region")}>
          <Input value={f.s3_region} onChange={(e) => setF({ ...f, s3_region: e.target.value })} placeholder="us-east-1 / auto" />
        </Field>
        <Field label={t("Access key ID")}>
          <Input value={f.s3_access_key} onChange={(e) => setF({ ...f, s3_access_key: e.target.value })} />
        </Field>
        <Field label={t("Secret access key")} hint={data!.s3_secret_key_set ? t("A secret is stored; enter a new one to replace it.") : undefined}>
          <PasswordInput value={f.s3_secret_key} onChange={(e) => setF({ ...f, s3_secret_key: e.target.value })} placeholder={data!.s3_secret_key_set ? "••••••••" : ""} />
        </Field>
        <div className="flex items-end justify-end">
          <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
            {t("Save")}
          </Button>
        </div>
      </div>
    </Card>
    </div>
  );
}
