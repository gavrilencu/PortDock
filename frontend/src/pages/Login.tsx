import { useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, GitBranch, Lock, ShieldCheck, Workflow } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type { LoginProviders } from "../lib/types";
import { ProviderMark } from "../components/sso";
import { LogoMark, useAuthStatus } from "../components/Layout";
import { Button, ErrorBox, Field, Input, PasswordInput } from "../components/ui";
import { LanguageSelect } from "../components/LanguagePicker";
import { Credits } from "../components/credits";
import { t, tk, tx } from "../lib/i18n";

const highlights = [
  { icon: GitBranch, title: tk("Git-driven delivery"), text: tk("Push-to-deploy, pull request previews and one-click rollbacks.") },
  { icon: Workflow, title: tk("Zero-downtime releases"), text: tk("Health-checked rollouts across every server you connect.") },
  { icon: ShieldCheck, title: tk("Secure by default"), text: tk("Encrypted secrets, automatic TLS, role-based access and audit log.") },
];

export function LoginPage() {
  const { data, isLoading } = useAuthStatus();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const providers = useQuery({ queryKey: ["login-providers"], queryFn: () => api.get<LoginProviders>("/auth/providers"), staleTime: 60_000 });
  const [form, setForm] = useState({ name: "", email: "", password: "" });
  const [mfa, setMfa] = useState<{ token: string; code: string; recovery: boolean } | null>(null);
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<unknown>(params.get("sso_error") ? new Error(params.get("sso_error")!) : null);
  const [busy, setBusy] = useState(false);

  if (isLoading) return null;
  const next = params.get("next") || "/";
  if (data?.user) return <Navigate to={next} replace />;
  const setup = data?.setup_required;
  const ssoList = setup ? [] : (providers.data?.providers ?? []);
  const ldapList = setup ? [] : (providers.data?.ldap ?? []);
  const passwordForm = setup || providers.data?.password_login !== false || showPassword || ssoList.length === 0 || ldapList.length > 0;

  const done = async () => {
    await qc.invalidateQueries({ queryKey: ["auth-status"] });
    location.href = next;
  };
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (mfa) {
        await api.post("/auth/login/mfa", { mfa_token: mfa.token, code: mfa.code });
        await done();
        return;
      }
      const res = await api.post<{ mfa_required?: boolean; mfa_token?: string }>(setup ? "/auth/register" : "/auth/login", form);
      if (res.mfa_required && res.mfa_token) {
        setMfa({ token: res.mfa_token, code: "", recovery: false });
        return;
      }
      await done();
    } catch (err) {
      setError(err);
      if (mfa && err instanceof ApiError && (err.status === 429 || /expired/.test(err.message))) setMfa(null);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid min-h-full bg-surface lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)]">
      <div className="flex flex-col px-6 py-8 sm:px-12">
        <div className="flex items-center gap-2.5">
          <LogoMark />
          <span className="text-[15px] font-semibold tracking-[-0.02em]">PortDock</span>
          <LanguageSelect className="ml-auto" />
        </div>
        <div className="flex flex-1 items-center justify-center py-12">
          {mfa ? (
            <form onSubmit={submit} className="w-full max-w-[360px]">
              <span className="flex size-10 items-center justify-center rounded-lg border border-border bg-surface-2">
                <ShieldCheck className="size-5" />
              </span>
              <h1 className="mt-5 text-2xl font-semibold tracking-[-0.025em]">{t("Two-factor authentication")}</h1>
              <p className="mt-2 text-[13px] leading-relaxed text-subtle">
                {mfa.recovery ? t("Enter one of the recovery codes you saved when you set up two-factor authentication.") : t("Open your authenticator app and enter the 6-digit code for PortDock.")}
              </p>
              <div className="mt-8 space-y-4">
                <Field label={mfa.recovery ? t("Recovery code") : t("Authentication code")}>
                  {mfa.recovery ? (
                    <Input className="h-11 font-mono tracking-wider" value={mfa.code} onChange={(e) => setMfa({ ...mfa, code: e.target.value })} placeholder="xxxxx-xxxxx" autoFocus autoComplete="off" />
                  ) : (
                    <Input
                      className="h-11 text-center font-mono text-lg tracking-[0.4em]"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      maxLength={6}
                      placeholder="000000"
                      autoFocus
                      value={mfa.code}
                      onChange={(e) => setMfa({ ...mfa, code: e.target.value.replace(/\D/g, "") })}
                    />
                  )}
                </Field>
                {error ? <ErrorBox error={error} title={t("Verification failed")} /> : null}
                <Button type="submit" variant="primary" size="lg" className="w-full justify-center" loading={busy} disabled={mfa.recovery ? mfa.code.length < 10 : mfa.code.length !== 6}>
                  {t("Verify")}
                </Button>
              </div>
              <div className="mt-6 flex items-center justify-between text-xs">
                <button
                  type="button"
                  className="text-muted underline-offset-2 hover:text-fg hover:underline"
                  onClick={() => {
                    setMfa({ ...mfa, code: "", recovery: !mfa.recovery });
                    setError(null);
                  }}
                >
                  {mfa.recovery ? t("Use authenticator app") : t("Use a recovery code")}
                </button>
                <button
                  type="button"
                  className="text-muted underline-offset-2 hover:text-fg hover:underline"
                  onClick={() => {
                    setMfa(null);
                    setError(null);
                  }}
                >
                  {t("Back to sign in")}
                </button>
              </div>
            </form>
          ) : (
            <form onSubmit={submit} className="w-full max-w-[360px]">
              <h1 className="text-2xl font-semibold tracking-[-0.025em]">{setup ? t("Set up your instance") : t("Sign in")}</h1>
              <p className="mt-2 text-[13px] leading-relaxed text-subtle">
                {setup ? t("Create the owner account. You can invite your team afterwards.") : t("Welcome back. Sign in to access your workspace.")}
              </p>
              {ssoList.length > 0 && (
                <div className="mt-8 space-y-2">
                  {ssoList.map((p) => (
                    <a
                      key={p.slug}
                      href={`/api/auth/sso/${p.slug}/start?next=${encodeURIComponent(next)}`}
                      className="flex h-10 w-full items-center justify-center gap-2.5 rounded-lg border border-border bg-surface text-[13px] font-medium shadow-xs transition-colors hover:border-border-strong hover:bg-surface-2"
                    >
                      <ProviderMark kind={p.kind} name={p.name} className="size-5 text-[9px]" />
                      {t("Continue with {name}", { name: p.name })}
                    </a>
                  ))}
                </div>
              )}
              {ssoList.length > 0 && passwordForm && (
                <div className="my-6 flex items-center gap-3 text-2xs font-medium uppercase tracking-wider text-faint">
                  <span className="h-px flex-1 bg-border" /> {t("or")} <span className="h-px flex-1 bg-border" />
                </div>
              )}
              {passwordForm ? (
                <div className={ssoList.length > 0 ? "space-y-4" : "mt-8 space-y-4"}>
                  {setup && (
                    <Field label={t("Full name")}>
                      <Input className="h-10" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Jane Cooper" autoFocus autoComplete="name" />
                    </Field>
                  )}
                  <Field label={ldapList.length ? t("Email or username") : t("Work email")} hint={ldapList.length && !setup ? t("Use your {directory} account or a PortDock account.", { directory: ldapList.map((d) => d.name).join(" / ") }) : undefined}>
                    <Input className="h-10" type={ldapList.length ? "text" : "email"} value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder={ldapList.length ? t("jdoe or jane@company.com") : "jane@company.com"} autoFocus={!setup && ssoList.length === 0} required autoComplete="username" />
                  </Field>
                  <Field label={t("Password")} hint={setup ? t("Use at least 8 characters.") : undefined}>
                    <PasswordInput className="h-10" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required minLength={setup ? 8 : undefined} autoComplete={setup ? "new-password" : "current-password"} />
                  </Field>
                  {error ? <ErrorBox error={error} title={setup ? t("Could not create account") : t("Sign-in failed")} /> : null}
                  <Button type="submit" variant={ssoList.length > 0 ? "secondary" : "primary"} size="lg" className="w-full justify-center" loading={busy} trailing={!busy && <ArrowRight />}>
                    {setup ? t("Create owner account") : ssoList.length > 0 ? t("Continue with email") : t("Continue")}
                  </Button>
                </div>
              ) : (
                <div className="mt-4 space-y-4">
                  {error ? <ErrorBox error={error} title={t("Sign-in failed")} /> : null}
                  <button type="button" onClick={() => setShowPassword(true)} className="text-xs text-muted underline-offset-2 hover:text-fg hover:underline">
                    {t("Owner sign-in with password")}
                  </button>
                </div>
              )}
              {!setup && passwordForm && (
                <p className="mt-6 text-xs leading-relaxed text-subtle">
                  {tx("Forgot your password? Ask an administrator, or reset it on the server with {command}.", {
                    command: <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[11px]">portdock -reset-password</code>,
                  })}
                </p>
              )}
            </form>
          )}
        </div>
        <div className="flex items-center justify-between text-xs text-faint">
          <span className="flex items-center gap-1.5">
            <Lock className="size-3" /> {t("Self-hosted · your data stays on your infrastructure")}
          </span>
          <span>v{data?.version}</span>
        </div>
        <Credits className="mt-2" />
      </div>

      <div className="relative hidden overflow-hidden border-l border-border bg-canvas lg:block">
        <div className="bg-grid absolute inset-0 opacity-70 [mask-image:linear-gradient(to_bottom,black,transparent_85%)]" />
        <div className="relative flex h-full flex-col justify-center px-16 py-12">
          <div className="max-w-[520px]">
            <div className="inline-flex h-6 items-center gap-2 rounded-full border border-border bg-surface px-2.5 text-xs font-medium text-muted shadow-xs">
              <span className="size-1.5 rounded-full bg-success" /> {t("Platform engineering, simplified")}
            </div>
            <h2 className="mt-5 text-[34px] font-semibold leading-[1.15] tracking-[-0.03em]">
              {t("Ship software on your own infrastructure — with the polish of a managed cloud.")}
            </h2>
            <div className="mt-10 space-y-3">
              {highlights.map((h) => (
                <div key={h.title} className="flex gap-4 rounded-xl border border-border bg-surface p-4 shadow-card">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2">
                    <h.icon className="size-4" />
                  </span>
                  <div>
                    <div className="text-[13px] font-semibold">{t(h.title)}</div>
                    <div className="mt-0.5 text-[13px] leading-relaxed text-subtle">{t(h.text)}</div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
