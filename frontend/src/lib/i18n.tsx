// Interface translations.
//
// The English text is the translation key: t("Deploy now") renders "Deploy now" in
// English and looks it up in the Romanian/Russian dictionaries otherwise, falling
// back to English when a translation is missing. Dictionaries live in
// src/locales/*.ts; every file exports `ro` and `ru` maps and is picked up
// automatically.
//
//   t("Deleted {name}", { name })          placeholders
//   t("{n} server|{n} servers", { n })      plural forms separated by "|":
//                                           en one|other, ro one|few|other, ru one|few|many
//
// t() reads the current language at call time, so call it while rendering (never at
// module load). Changing the language remounts the app below <LocaleRoot>.
import { Fragment, useSyncExternalStore, type ReactNode } from "react";

export type Locale = "en" | "ro" | "ru";
type Dict = Record<string, string>;

export const LOCALES: { id: Locale; label: string }[] = [
  { id: "en", label: "English" },
  { id: "ro", label: "Română" },
  { id: "ru", label: "Русский" },
];

const KEY = "portdock.locale";
const PLURAL_ORDER: Record<Locale, string[]> = {
  en: ["one", "other"],
  ro: ["one", "few", "other"],
  ru: ["one", "few", "many"],
};

const dicts: Record<Locale, Dict> = { en: {}, ro: {}, ru: {} };
for (const m of Object.values(import.meta.glob<{ ro?: Dict; ru?: Dict }>("../locales/*.ts", { eager: true }))) {
  Object.assign(dicts.ro, m.ro);
  Object.assign(dicts.ru, m.ru);
}

export const isLocale = (v: unknown): v is Locale => v === "en" || v === "ro" || v === "ru";

function stored(): Locale {
  try {
    const v = localStorage.getItem(KEY);
    if (isLocale(v)) return v;
  } catch {
    /* storage unavailable */
  }
  return "en";
}

let current: Locale = stored();
const listeners = new Set<() => void>();
document.documentElement.lang = current;

export function getLocale(): Locale {
  return current;
}

export function setLocale(l: Locale) {
  try {
    localStorage.setItem(KEY, l);
  } catch {
    /* storage unavailable */
  }
  if (l === current) return;
  current = l;
  document.documentElement.lang = l;
  listeners.forEach((f) => f());
}

export function useLocale(): Locale {
  return useSyncExternalStore(
    (f) => {
      listeners.add(f);
      return () => listeners.delete(f);
    },
    () => current,
  );
}

function pickPlural(s: string, n: number): string {
  const forms = s.split("|");
  const order = PLURAL_ORDER[current];
  let i = order.indexOf(new Intl.PluralRules(current).select(n));
  if (i < 0) i = order.length - 1;
  return forms[Math.min(i, forms.length - 1)];
}

export function t(text: string, vars?: Record<string, string | number>): string {
  let s = (current !== "en" && dicts[current][text]) || text;
  if (vars && typeof vars.n === "number" && s.includes("|")) s = pickPlural(s, vars.n);
  if (vars) s = s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
  return s;
}

/**
 * Marks a text for translation without translating it, for module-level constants
 * (navigation items, option lists). Translate it while rendering with t(item.label).
 */
export const tk = (text: string) => text;

/**
 * Like t(), with React elements as placeholder values:
 *   tx("Click {action} to start the proxy.", { action: <b>{t("Run setup")}</b> })
 */
export function tx(text: string, parts: Record<string, ReactNode>): ReactNode {
  const s = (current !== "en" && dicts[current][text]) || text;
  return s.split(/(\{\w+\})/).map((seg, i) => {
    const m = /^\{(\w+)\}$/.exec(seg);
    return m && m[1] in parts ? <Fragment key={i}>{parts[m[1]]}</Fragment> : seg;
  });
}

/** Translates only when a translation exists; used for messages coming from the API. */
export function tMaybe(text: string): string {
  return (current !== "en" && dicts[current][text]) || text;
}

/** BCP 47 tag for Intl/Date formatting; English keeps the browser's own format. */
export function intlLocale(): string | undefined {
  return current === "en" ? undefined : current === "ro" ? "ro-RO" : "ru-RU";
}

/** Re-renders the whole interface when the language changes. */
export function LocaleRoot({ children }: { children: ReactNode }) {
  const locale = useLocale();
  return <Fragment key={locale}>{children}</Fragment>;
}
