import { useQueryClient } from "@tanstack/react-query";
import { Languages } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import { isLocale, LOCALES, setLocale, t, useLocale, type Locale } from "../lib/i18n";
import type { AuthStatus } from "../lib/types";
import { cn } from "../lib/utils";

/** Switches the interface language and, when signed in, saves it on the account. */
export function useChangeLocale() {
  const qc = useQueryClient();
  return async (l: Locale) => {
    const signedIn = !!qc.getQueryData<AuthStatus>(["auth-status"])?.user;
    // Update the cached user first so the account sync in AppLayout doesn't switch back.
    qc.setQueryData<AuthStatus>(["auth-status"], (d) => (d?.user ? { ...d, user: { ...d.user, locale: l } } : d));
    setLocale(l);
    if (!signedIn) return;
    try {
      await api.patch("/auth/me", { locale: l });
      toast.success(t("Language updated"));
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
}

/** Compact language switch for pages without the sidebar (sign-in). */
export function LanguageSelect({ className }: { className?: string }) {
  const locale = useLocale();
  const change = useChangeLocale();
  return (
    <label className={cn("relative inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2 text-xs text-muted shadow-xs hover:text-fg", className)}>
      <Languages className="size-3.5" />
      <select aria-label={t("Language")} className="cursor-pointer appearance-none bg-transparent pr-1 outline-none" value={locale} onChange={(e) => isLocale(e.target.value) && change(e.target.value)}>
        {LOCALES.map((l) => (
          <option key={l.id} value={l.id}>
            {l.label}
          </option>
        ))}
      </select>
    </label>
  );
}
