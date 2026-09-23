import { useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, GitBranch, Lock, ShieldCheck, Workflow } from "lucide-react";
import { api } from "../lib/api";
import { LogoMark, useAuthStatus } from "../components/Layout";
import { Button, ErrorBox, Field, Input, PasswordInput } from "../components/ui";

const highlights = [
  { icon: GitBranch, title: "Git-driven delivery", text: "Push-to-deploy, pull request previews and one-click rollbacks." },
  { icon: Workflow, title: "Zero-downtime releases", text: "Health-checked rollouts across every server you connect." },
  { icon: ShieldCheck, title: "Secure by default", text: "Encrypted secrets, automatic TLS, role-based access and audit log." },
];

export function LoginPage() {
  const { data, isLoading } = useAuthStatus();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const [form, setForm] = useState({ name: "", email: "", password: "" });
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  if (isLoading) return null;
  const next = params.get("next") || "/";
  if (data?.user) return <Navigate to={next} replace />;
  const setup = data?.setup_required;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post(setup ? "/auth/register" : "/auth/login", form);
      await qc.invalidateQueries({ queryKey: ["auth-status"] });
      location.href = next;
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid min-h-full bg-surface lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)]">
      <div className="flex flex-col px-6 py-8 sm:px-12">
        <div className="flex items-center gap-2.5">
          <LogoMark />
          <span className="text-[15px] font-semibold tracking-[-0.02em]">Shipyard</span>
        </div>
        <div className="flex flex-1 items-center justify-center py-12">
          <form onSubmit={submit} className="w-full max-w-[360px]">
            <h1 className="text-2xl font-semibold tracking-[-0.025em]">{setup ? "Set up your instance" : "Sign in"}</h1>
            <p className="mt-2 text-[13px] leading-relaxed text-subtle">
              {setup ? "Create the owner account. You can invite your team afterwards." : "Welcome back. Enter your credentials to access your workspace."}
            </p>
            <div className="mt-8 space-y-4">
              {setup && (
                <Field label="Full name">
                  <Input className="h-10" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Jane Cooper" autoFocus autoComplete="name" />
                </Field>
              )}
              <Field label="Work email">
                <Input className="h-10" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder="jane@company.com" autoFocus={!setup} required autoComplete="email" />
              </Field>
              <Field label="Password" hint={setup ? "Use at least 8 characters." : undefined}>
                <PasswordInput className="h-10" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required minLength={setup ? 8 : undefined} autoComplete={setup ? "new-password" : "current-password"} />
              </Field>
              {error ? <ErrorBox error={error} title={setup ? "Could not create account" : "Sign-in failed"} /> : null}
              <Button type="submit" variant="primary" size="lg" className="w-full justify-center" loading={busy} trailing={!busy && <ArrowRight />}>
                {setup ? "Create owner account" : "Continue"}
              </Button>
            </div>
            {!setup && <p className="mt-6 text-xs leading-relaxed text-subtle">Forgot your password? Ask an administrator, or reset it on the server with <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[11px]">shipyard -reset-password</code>.</p>}
          </form>
        </div>
        <div className="flex items-center justify-between text-xs text-faint">
          <span className="flex items-center gap-1.5">
            <Lock className="size-3" /> Self-hosted · your data stays on your infrastructure
          </span>
          <span>v{data?.version}</span>
        </div>
      </div>

      <div className="relative hidden overflow-hidden border-l border-border bg-canvas lg:block">
        <div className="bg-grid absolute inset-0 opacity-70 [mask-image:linear-gradient(to_bottom,black,transparent_85%)]" />
        <div className="relative flex h-full flex-col justify-center px-16 py-12">
          <div className="max-w-[520px]">
            <div className="inline-flex h-6 items-center gap-2 rounded-full border border-border bg-surface px-2.5 text-xs font-medium text-muted shadow-xs">
              <span className="size-1.5 rounded-full bg-success" /> Platform engineering, simplified
            </div>
            <h2 className="mt-5 text-[34px] font-semibold leading-[1.15] tracking-[-0.03em]">
              Ship software on your own infrastructure — with the polish of a managed cloud.
            </h2>
            <div className="mt-10 space-y-3">
              {highlights.map((h) => (
                <div key={h.title} className="flex gap-4 rounded-xl border border-border bg-surface p-4 shadow-card">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2">
                    <h.icon className="size-4" />
                  </span>
                  <div>
                    <div className="text-[13px] font-semibold">{h.title}</div>
                    <div className="mt-0.5 text-[13px] leading-relaxed text-subtle">{h.text}</div>
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
