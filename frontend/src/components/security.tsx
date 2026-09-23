import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { encode } from "uqr";
import { Check, Download, KeyRound, LaptopMinimal, LogOut, RefreshCw, ShieldCheck, ShieldOff, Smartphone } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { TwoFactorStatus, UserSession } from "../lib/types";
import { timeAgo } from "../lib/utils";
import { useUser } from "./Layout";
import { Badge, Button, Card, CopyButton, Field, Input, Loading, Modal, PasswordInput, Table, useConfirm } from "./ui";

// ---- QR code ----

export function QRCode({ value, size = 176 }: { value: string; size?: number }) {
  const path = useMemo(() => {
    const { data } = encode(value, { ecc: "M", border: 0 });
    let d = "";
    data.forEach((row, y) => row.forEach((on, x) => on && (d += `M${x} ${y}h1v1h-1z`)));
    return { d, n: data.length };
  }, [value]);
  // Always dark-on-white: authenticator apps can't read inverted codes reliably.
  return (
    <div className="inline-flex rounded-lg border border-border bg-white p-3 shadow-xs">
      <svg width={size} height={size} viewBox={`0 0 ${path.n} ${path.n}`} shapeRendering="crispEdges" role="img" aria-label="QR code">
        <path d={path.d} fill="#0a0a0a" />
      </svg>
    </div>
  );
}

// ---- recovery codes ----

export function RecoveryCodes({ codes }: { codes: string[] }) {
  const text = codes.join("\n");
  const download = () => {
    const blob = new Blob([`PortDock recovery codes — ${location.host}\nEach code can be used once.\n\n${text}\n`], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "portdock-recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(a.href);
  };
  return (
    <div>
      <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-lg border border-border bg-surface-2 px-5 py-4 font-mono text-[13px] tracking-wide">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </div>
      <div className="mt-3 flex gap-2">
        <Button size="sm" icon={<Download />} onClick={download}>
          Download
        </Button>
        <CopyButton value={text} />
      </div>
    </div>
  );
}

// ---- enrollment flow ----

type SetupData = { secret: string; otpauth_url: string };

function CodeInput({ value, onChange, autoFocus }: { value: string; onChange: (v: string) => void; autoFocus?: boolean }) {
  return (
    <Input
      className="h-11 text-center font-mono text-lg tracking-[0.4em]"
      inputMode="numeric"
      autoComplete="one-time-code"
      maxLength={6}
      placeholder="000000"
      value={value}
      autoFocus={autoFocus}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
    />
  );
}

/** Three steps: confirm password → scan & verify → save recovery codes. */
export function TwoFactorSetup({ onDone }: { onDone: () => void }) {
  const user = useUser();
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [setup, setSetup] = useState<SetupData | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [saved, setSaved] = useState(false);

  const start = useMutation({
    mutationFn: () => api.post<SetupData>("/auth/2fa/setup", { password }),
    onSuccess: setSetup,
    onError: (e) => toast.error(e.message),
  });
  const enable = useMutation({
    mutationFn: () => api.post<{ recovery_codes: string[] }>("/auth/2fa/enable", { code }),
    onSuccess: (r) => setCodes(r.recovery_codes),
    onError: (e) => toast.error(e.message),
  });
  const finish = () => {
    qc.invalidateQueries({ queryKey: ["auth-status"] });
    qc.invalidateQueries({ queryKey: ["2fa"] });
    qc.invalidateQueries({ queryKey: ["sessions"] });
    toast.success("Two-factor authentication is on");
    onDone();
  };

  if (codes)
    return (
      <div className="space-y-4">
        <div className="flex items-start gap-3 rounded-lg border border-success/30 bg-success-soft px-4 py-3 text-[13px] text-success">
          <Check className="mt-0.5 size-4 shrink-0" /> Two-factor authentication is enabled. Other browsers signed in with your password were signed out.
        </div>
        <div>
          <div className="text-[13px] font-semibold">Save your recovery codes</div>
          <p className="mt-1 text-[13px] text-subtle">If you lose your device, each code signs you in once. They won't be shown again.</p>
        </div>
        <RecoveryCodes codes={codes} />
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} className="size-4 accent-[var(--color-fg)]" />
          I have stored these codes somewhere safe
        </label>
        <div className="flex justify-end">
          <Button variant="primary" disabled={!saved} onClick={finish}>
            Done
          </Button>
        </div>
      </div>
    );

  if (!setup)
    return (
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          start.mutate();
        }}
      >
        <p className="text-[13px] leading-relaxed text-subtle">
          Use an authenticator app such as Microsoft Authenticator, Google Authenticator, 1Password or Bitwarden. Each sign-in will ask for a 6-digit code from the app.
        </p>
        {user.has_password && (
          <Field label="Confirm your password">
            <PasswordInput value={password} onChange={(e) => setPassword(e.target.value)} autoFocus autoComplete="current-password" />
          </Field>
        )}
        <div className="flex justify-end">
          <Button type="submit" variant="primary" loading={start.isPending} disabled={user.has_password && !password}>
            Continue
          </Button>
        </div>
      </form>
    );

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        enable.mutate();
      }}
    >
      <div className="flex flex-col items-center gap-5 sm:flex-row sm:items-start">
        <QRCode value={setup.otpauth_url} />
        <div className="min-w-0 flex-1 space-y-3 text-[13px]">
          <div>
            <div className="font-semibold">1. Scan the QR code</div>
            <p className="mt-0.5 text-subtle">Open your authenticator app and add a new account.</p>
          </div>
          <div>
            <div className="text-subtle">Can't scan? Enter this key manually:</div>
            <div className="mt-1 flex items-center gap-1 rounded-md border border-border bg-surface-2 px-2 py-1">
              <code className="min-w-0 flex-1 break-all font-mono text-xs">{setup.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
              <CopyButton value={setup.secret} />
            </div>
          </div>
        </div>
      </div>
      <Field label="2. Enter the 6-digit code from the app">
        <CodeInput value={code} onChange={setCode} autoFocus />
      </Field>
      <div className="flex justify-end">
        <Button type="submit" variant="primary" loading={enable.isPending} disabled={code.length !== 6}>
          Verify and enable
        </Button>
      </div>
    </form>
  );
}

// ---- settings cards ----

export function TwoFactorCard() {
  const user = useUser();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["2fa"], queryFn: () => api.get<TwoFactorStatus>("/auth/2fa") });
  const [modal, setModal] = useState<"setup" | "disable" | "codes" | null>(null);
  const [form, setForm] = useState({ password: "", code: "" });
  const [newCodes, setNewCodes] = useState<string[] | null>(null);
  const close = () => {
    setModal(null);
    setForm({ password: "", code: "" });
    setNewCodes(null);
  };
  const disable = useMutation({
    mutationFn: () => api.post("/auth/2fa/disable", form),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["2fa"] });
      qc.invalidateQueries({ queryKey: ["auth-status"] });
      toast.success("Two-factor authentication turned off");
      close();
    },
    onError: (e) => toast.error(e.message),
  });
  const regen = useMutation({
    mutationFn: () => api.post<{ recovery_codes: string[] }>("/auth/2fa/recovery-codes", { code: form.code }),
    onSuccess: (r) => {
      setNewCodes(r.recovery_codes);
      qc.invalidateQueries({ queryKey: ["2fa"] });
    },
    onError: (e) => toast.error(e.message),
  });

  if (status.isLoading) return <Loading rows={2} />;
  const s = status.data!;
  const sso = user.auth_source !== "" && !user.has_password;

  return (
    <Card
      title="Two-factor authentication"
      description="Protect your account with a time-based code from an authenticator app, in addition to your password."
    >
      <div className="flex flex-wrap items-center gap-4 p-5">
        <span className={`flex size-10 items-center justify-center rounded-lg border ${s.enabled ? "border-success/30 bg-success-soft text-success" : "border-border bg-surface-2 text-faint"}`}>
          {s.enabled ? <ShieldCheck className="size-5" /> : <ShieldOff className="size-5" />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 text-[13px] font-semibold">
            Authenticator app {s.enabled ? <Badge tone="success">Enabled</Badge> : <Badge>Off</Badge>}
            {s.required && <Badge tone="warning">Required by your organization</Badge>}
          </div>
          <div className="mt-0.5 text-xs text-subtle">
            {s.enabled
              ? `${s.recovery_codes_left} of 10 recovery codes left${s.recovery_codes_left <= 3 ? " — generate new ones soon" : ""}.`
              : sso
                ? "You sign in with single sign-on; multi-factor authentication is handled by your identity provider."
                : "Anyone with your password can currently sign in."}
          </div>
        </div>
        {s.enabled ? (
          <div className="flex gap-2">
            <Button size="sm" icon={<RefreshCw />} onClick={() => setModal("codes")}>
              Recovery codes
            </Button>
            {!s.required && (
              <Button size="sm" variant="danger" onClick={() => setModal("disable")}>
                Turn off
              </Button>
            )}
          </div>
        ) : (
          <Button size="sm" variant="primary" icon={<Smartphone />} onClick={() => setModal("setup")}>
            Set up
          </Button>
        )}
      </div>

      <Modal open={modal === "setup"} onClose={close} title="Set up two-factor authentication" size="md">
        <TwoFactorSetup onDone={close} />
      </Modal>

      <Modal
        open={modal === "disable"}
        onClose={close}
        title="Turn off two-factor authentication?"
        description="Your account will be protected by your password only."
        footer={
          <Button variant="danger" loading={disable.isPending} disabled={!form.code} onClick={() => disable.mutate()}>
            Turn off
          </Button>
        }
      >
        <div className="space-y-4">
          {user.has_password && (
            <Field label="Password">
              <PasswordInput value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} autoComplete="current-password" />
            </Field>
          )}
          <Field label="Authentication or recovery code">
            <Input value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value })} className="font-mono" autoComplete="one-time-code" />
          </Field>
        </div>
      </Modal>

      <Modal
        open={modal === "codes"}
        onClose={close}
        title="Recovery codes"
        description={newCodes ? "Store these somewhere safe. Your previous codes no longer work." : "Generating new codes invalidates the old ones."}
        footer={
          newCodes ? (
            <Button variant="primary" onClick={close}>
              Done
            </Button>
          ) : (
            <Button variant="primary" loading={regen.isPending} disabled={form.code.length !== 6} onClick={() => regen.mutate()}>
              Generate new codes
            </Button>
          )
        }
      >
        {newCodes ? (
          <RecoveryCodes codes={newCodes} />
        ) : (
          <Field label="Current code from your authenticator app">
            <CodeInput value={form.code} onChange={(v) => setForm({ ...form, code: v })} autoFocus />
          </Field>
        )}
      </Modal>
    </Card>
  );
}

/** Best-effort, dependency-free user-agent summary ("Chrome on Windows"). */
export function describeUA(ua: string): string {
  if (!ua) return "Unknown device";
  const browser =
    /Edg\//.test(ua) ? "Edge" : /OPR\//.test(ua) ? "Opera" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : /curl|Go-http|python/i.test(ua) ? "API client" : "Browser";
  const os = /Windows/.test(ua) ? "Windows" : /iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Mac OS X/.test(ua) ? "macOS" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}

export function methodLabel(m: string): { label: string; tone: "neutral" | "success" | "info" | "warning" } {
  if (m.startsWith("sso:")) return { label: `SSO · ${m.slice(4)}`, tone: "info" };
  if (m.startsWith("ldap:")) return { label: `LDAP · ${m.slice(5).replace(/\+.*/, "")}${m.includes("+totp") ? " + 2FA" : ""}`, tone: m.includes("+totp") ? "success" : "info" };
  if (m === "password+totp") return { label: "Password + 2FA", tone: "success" };
  if (m === "password+recovery") return { label: "Recovery code", tone: "warning" };
  return { label: "Password", tone: "neutral" };
}

export function SessionsCard() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: () => api.get<UserSession[]>("/auth/sessions") });
  const revoke = useMutation({
    mutationFn: (id: string) => api.del(`/auth/sessions/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sessions"] });
      toast.success("Session signed out");
    },
    onError: (e) => toast.error(e.message),
  });
  const others = useMutation({
    mutationFn: () => api.post("/auth/sessions/revoke-others"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sessions"] });
      toast.success("Signed out of all other sessions");
    },
    onError: (e) => toast.error(e.message),
  });
  if (sessions.isLoading) return <Loading rows={2} />;
  const list = sessions.data ?? [];
  return (
    <Card
      title="Active sessions"
      description="Browsers currently signed in to your account. Sign out any session you don't recognise."
      actions={
        list.length > 1 && (
          <Button size="sm" icon={<LogOut />} loading={others.isPending} onClick={async () => (await confirm({ title: "Sign out of all other sessions?", confirmText: "Sign out" })) && others.mutate()}>
            Sign out others
          </Button>
        )
      }
    >
      <Table>
        <thead>
          <tr>
            <th>Device</th>
            <th>Sign-in method</th>
            <th>IP address</th>
            <th>Last active</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {list.map((s) => {
            const m = methodLabel(s.method);
            return (
              <tr key={s.id}>
                <td>
                  <div className="flex items-center gap-2.5">
                    <LaptopMinimal className="size-4 text-faint" />
                    <div>
                      <div className="font-medium">
                        {describeUA(s.user_agent)} {s.current && <Badge tone="success">This device</Badge>}
                      </div>
                      <div className="text-xs text-muted">Signed in {timeAgo(s.created_at)}</div>
                    </div>
                  </div>
                </td>
                <td>
                  <Badge tone={m.tone}>{m.label}</Badge>
                </td>
                <td className="font-mono text-xs text-muted">{s.ip || "—"}</td>
                <td className="text-xs text-muted">{s.current ? "Now" : timeAgo(s.last_seen_at)}</td>
                <td className="text-right">
                  {!s.current && (
                    <Button size="sm" variant="ghost" onClick={() => revoke.mutate(s.id)}>
                      Sign out
                    </Button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

export function SecuritySettings() {
  return (
    <div className="space-y-6">
      <TwoFactorCard />
      <SessionsCard />
    </div>
  );
}

// ---- enforced enrollment (organization policy) ----

export function MFAEnforcement({ onLogout }: { onLogout: () => void }) {
  const qc = useQueryClient();
  return (
    <div className="flex min-h-full items-center justify-center bg-canvas px-4 py-12">
      <div className="w-full max-w-[520px] rounded-xl border border-border bg-surface p-7 shadow-card">
        <span className="flex size-10 items-center justify-center rounded-lg border border-border bg-surface-2">
          <KeyRound className="size-5" />
        </span>
        <h1 className="mt-4 text-xl font-semibold tracking-[-0.02em]">Set up two-factor authentication</h1>
        <p className="mt-1.5 text-[13px] leading-relaxed text-subtle">Your organization requires two-factor authentication. Set it up to continue to PortDock.</p>
        <div className="mt-6">
          <TwoFactorSetup onDone={() => qc.invalidateQueries({ queryKey: ["auth-status"] })} />
        </div>
        <div className="mt-6 border-t border-border pt-4 text-right">
          <Button size="sm" variant="ghost" icon={<LogOut />} onClick={onLogout}>
            Sign out
          </Button>
        </div>
      </div>
    </div>
  );
}
