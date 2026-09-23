import { t } from "../lib/i18n";
import { cn } from "../lib/utils";

export const DEVELOPER = "Gavrilencu Grigore";

/** "© 2026 PortDock · Developed by Gavrilencu Grigore": shown at the bottom of every page. */
export function Credits({ className }: { className?: string }) {
  return (
    <p className={cn("text-xs text-faint", className)}>
      © {new Date().getFullYear()} PortDock · {t("Developed by {name}", { name: DEVELOPER })}
    </p>
  );
}
