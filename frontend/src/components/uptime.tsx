import { cn } from "../lib/utils";
import { intlLocale, t } from "../lib/i18n";

/** Uptime of one day on the status bars (null: no checks that day). */
export interface UptimeDay {
  day: string;
  uptime: number | null;
}

export function uptimeText(u: number | null | undefined) {
  if (u === null || u === undefined) return "—";
  if (u >= 99.995) return "100%";
  return `${u.toFixed(2)}%`;
}

export function uptimeTone(u: number | null | undefined) {
  if (u === null || u === undefined) return "bg-border";
  if (u >= 99.9) return "bg-success";
  if (u >= 99) return "bg-success/60";
  if (u >= 95) return "bg-warning";
  return "bg-danger";
}

/** 90 thin bars, one per day, colored by uptime. */
export function UptimeBars({ days, className }: { days: UptimeDay[]; className?: string }) {
  return (
    <div className={cn("flex h-8 items-stretch gap-[2px]", className)}>
      {days.map((d) => (
        <div
          key={d.day}
          className={cn("min-w-[2px] flex-1 rounded-[2px] transition-opacity hover:opacity-70", uptimeTone(d.uptime))}
          title={`${new Date(d.day + "T12:00:00Z").toLocaleDateString(intlLocale(), { day: "numeric", month: "short", year: "numeric" })} · ${d.uptime === null ? t("No data") : uptimeText(d.uptime)}`}
        />
      ))}
    </div>
  );
}

/** Response times of recent checks, failed ones in red. */
export function ResponseBars({ checks, className }: { checks: { ok: boolean; response_ms: number; created_at: string }[]; className?: string }) {
  const maxMs = Math.max(100, ...checks.map((c) => c.response_ms));
  return (
    <div className={cn("flex h-8 items-end gap-[2px]", className)}>
      {checks.map((c, i) => (
        <div
          key={i}
          className={cn("min-w-[2px] flex-1 rounded-t-[1px]", c.ok ? "bg-success/70" : "bg-danger")}
          style={{ height: c.ok ? `${Math.max(8, (c.response_ms / maxMs) * 100)}%` : "100%" }}
          title={`${new Date(c.created_at).toLocaleString(intlLocale())} · ${c.ok ? `${c.response_ms} ms` : t("Failed")}`}
        />
      ))}
    </div>
  );
}
