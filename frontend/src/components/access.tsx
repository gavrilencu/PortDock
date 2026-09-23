import type { ReactNode } from "react";
import { Link } from "react-router";
import { Lock, ShieldCheck, Sparkles } from "lucide-react";
import type { Access, ProjectRole } from "../lib/types";
import { useAuthStatus } from "./Layout";
import { Badge } from "./ui";
import { t, tk, tx } from "../lib/i18n";

export const roleLabels: Record<ProjectRole, string> = {
  admin: tk("Admin"),
  maintainer: tk("Maintainer"),
  developer: tk("Developer"),
  viewer: tk("Viewer"),
  none: tk("No access"),
};

export const roleDescriptions: Record<Exclude<ProjectRole, "admin">, string> = {
  maintainer: tk("Everything, including protected environments, members and approvals."),
  developer: tk("Deploy and configure unprotected environments; request deployments to protected ones."),
  viewer: tk("Read-only: resources, logs, deployments and metrics. No secrets."),
  none: tk("Can't see the project."),
};

/** Plan info from /auth/status. */
export function useLicense() {
  const { data } = useAuthStatus();
  const l = data?.license;
  return { license: l, has: (feature: string) => !!l?.features.includes(feature) };
}

export const featurePlan: Record<string, "Pro" | "Enterprise"> = {
  multi_server: "Pro",
  registries: "Pro",
  project_roles: "Pro",
  audit_export: "Pro",
  sso: "Enterprise",
  approvals: "Enterprise",
  audit_streaming: "Enterprise",
  ldap: "Enterprise",
};

/** Inline upsell shown instead of (or next to) a gated control. */
export function UpgradeNotice({ feature, children }: { feature: string; children?: ReactNode }) {
  const { has } = useLicense();
  if (has(feature)) return null;
  return (
    <div className="flex items-start gap-2.5 rounded-lg border border-accent/25 bg-accent-soft px-3 py-2.5 text-[13px]">
      <Sparkles className="mt-0.5 size-4 shrink-0 text-accent" />
      <div className="min-w-0 flex-1 text-muted">
        {tx("{text} {plan} plan.", { text: children ?? t("This feature is part of the"), plan: <span className="font-medium text-fg">{featurePlan[feature]}</span> })}{" "}
        <Link to="/settings?tab=license" className="font-medium text-fg underline underline-offset-2">
          {t("Start a free trial or add a license")}
        </Link>
      </div>
    </div>
  );
}

export function RoleBadge({ role }: { role: ProjectRole }) {
  return <Badge tone={role === "admin" || role === "maintainer" ? "accent" : role === "viewer" ? "neutral" : "info"}>{t(roleLabels[role])}</Badge>;
}

export function ProtectedBadge({ approvals }: { approvals?: boolean }) {
  return (
    <Badge tone="warning">
      {approvals ? <ShieldCheck /> : <Lock />} {approvals ? t("Approvals") : t("Protected")}
    </Badge>
  );
}

/** Banner for resources the user can't change. */
export function AccessNotice({ access }: { access: Access }) {
  if (access.can_write) return null;
  const msg =
    access.role === "viewer"
      ? t("You have read-only access to this project.")
      : access.protected
        ? t("This is a protected environment: only project maintainers can change it. You can still request deployments.")
        : t("Your role does not allow changes here.");
  return (
    <div className="mb-5 flex items-center gap-2.5 rounded-lg border border-border bg-surface-2/60 px-4 py-2.5 text-[13px] text-muted">
      <Lock className="size-4 shrink-0 text-faint" /> {msg}
    </div>
  );
}
