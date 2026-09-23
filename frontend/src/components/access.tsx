import { Lock, ShieldCheck } from "lucide-react";
import type { Access, ProjectRole } from "../lib/types";
import { Badge } from "./ui";
import { t, tk } from "../lib/i18n";

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
