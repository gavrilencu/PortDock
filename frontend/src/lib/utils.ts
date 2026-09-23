import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import { intlLocale, t } from "./i18n";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function timeAgo(iso: string): string {
  if (!iso) return t("never");
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 5) return t("just now");
  if (s < 60) return t("{n}s ago", { n: s });
  const m = Math.round(s / 60);
  if (m < 60) return t("{n}m ago", { n: m });
  const h = Math.round(m / 60);
  if (h < 24) return t("{n}h ago", { n: h });
  const d = Math.round(h / 24);
  if (d < 30) return t("{n}d ago", { n: d });
  return new Date(iso).toLocaleDateString(intlLocale());
}

export function duration(start: string, end?: string): string {
  if (!start) return "–";
  const ms = (end ? new Date(end).getTime() : Date.now()) - new Date(start).getTime();
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return t("{s}s", { s });
  return t("{m}m {s}s", { m: Math.floor(s / 60), s: s % 60 });
}

export function formatBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
  return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
}

export function formatUptime(sec: number): string {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return d ? t("{d}d {h}h", { d, h }) : h ? t("{h}h {m}m", { h, m }) : t("{m}m", { m });
}

export function shortSha(sha: string) {
  return sha ? sha.slice(0, 7) : "";
}

export function splitList(s: string): string[] {
  return s
    .split(/[,\n;]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

export function domainURL(d: string, https = true): string {
  if (/^https?:\/\//.test(d)) return d;
  return `${https ? "https" : "http"}://${d}`;
}

// Strip ANSI escape sequences from build output.
const ansi = /\u001b\[[0-9;?]*[ -/]*[@-~]/g;
export function stripAnsi(s: string) {
  return s.replace(ansi, "");
}

export async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/** Cryptographically random alphanumeric string (no ambiguous characters). */
export function randomPassword(length = 24): string {
  const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => alphabet[n % alphabet.length]).join("");
}

/** Random lowercase hex string of `bytes` bytes. */
export function randomHex(bytes = 32): string {
  const buf = new Uint8Array(bytes);
  crypto.getRandomValues(buf);
  return Array.from(buf, (b) => b.toString(16).padStart(2, "0")).join("");
}

export function slugify(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "app";
}
