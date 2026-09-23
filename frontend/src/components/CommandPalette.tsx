import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  Box,
  Boxes,
  CornerDownLeft,
  Database,
  FolderKanban,
  LayoutDashboard,
  LogOut,
  Monitor,
  Moon,
  Plus,
  Rocket,
  Search,
  Server as ServerIcon,
  Settings,
  Sun,
  Users,
} from "lucide-react";
import { api } from "../lib/api";
import type { Application, Database as DB, Project, Server, Service } from "../lib/types";
import type { ThemeMode } from "../lib/theme";
import { cn } from "../lib/utils";
import { Kbd, StatusDot } from "./ui";
import { t } from "../lib/i18n";

interface Item {
  id: string;
  group: string;
  label: string;
  hint?: string;
  icon: ReactNode;
  status?: string;
  keywords?: string;
  run: () => void;
}

const PaletteContext = createContext<() => void>(() => {});
export const useCommandPalette = () => useContext(PaletteContext);

export function CommandPaletteProvider({ children, setTheme, onLogout }: { children: ReactNode; setTheme: (m: ThemeMode) => void; onLogout: () => void }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <PaletteContext.Provider value={() => setOpen(true)}>
      {children}
      {open && <Palette onClose={() => setOpen(false)} setTheme={setTheme} onLogout={onLogout} />}
    </PaletteContext.Provider>
  );
}

function Palette({ onClose, setTheme, onLogout }: { onClose: () => void; setTheme: (m: ThemeMode) => void; onLogout: () => void }) {
  const nav = useNavigate();
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
  const apps = useQuery({ queryKey: ["applications"], queryFn: () => api.get<Application[]>("/applications") });
  const dbs = useQuery({ queryKey: ["databases"], queryFn: () => api.get<DB[]>("/databases") });
  const svcs = useQuery({ queryKey: ["services"], queryFn: () => api.get<Service[]>("/services") });
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers") });

  const go = (to: string) => () => {
    nav(to);
    onClose();
  };

  const items = useMemo<Item[]>(() => {
    const all: Item[] = [
      { id: "n-dash", group: t("Navigation"), label: t("Overview"), icon: <LayoutDashboard />, run: go("/") },
      { id: "n-proj", group: t("Navigation"), label: t("Projects"), icon: <FolderKanban />, run: go("/projects") },
      { id: "n-dep", group: t("Navigation"), label: t("Deployments"), icon: <Rocket />, run: go("/deployments") },
      { id: "n-srv", group: t("Navigation"), label: t("Servers"), icon: <ServerIcon />, run: go("/servers") },
      { id: "n-team", group: t("Navigation"), label: t("Team"), icon: <Users />, run: go("/settings?tab=users") },
      { id: "n-set", group: t("Navigation"), label: t("Settings"), icon: <Settings />, run: go("/settings") },
      { id: "a-proj", group: "Actions", label: t("Create project"), icon: <Plus />, keywords: "new add", run: go("/projects?new=1") },
      { id: "a-srv", group: "Actions", label: t("Connect a server"), icon: <Plus />, keywords: "new add ssh", run: go("/servers?new=1") },
      { id: "a-light", group: "Actions", label: t("Switch to light theme"), icon: <Sun />, keywords: "appearance", run: () => (setTheme("light"), onClose()) },
      { id: "a-dark", group: "Actions", label: t("Switch to dark theme"), icon: <Moon />, keywords: "appearance", run: () => (setTheme("dark"), onClose()) },
      { id: "a-sys", group: "Actions", label: t("Use system theme"), icon: <Monitor />, keywords: "appearance", run: () => (setTheme("system"), onClose()) },
      { id: "a-out", group: "Actions", label: t("Sign out"), icon: <LogOut />, keywords: "logout", run: () => (onClose(), onLogout()) },
    ];
    for (const p of projects.data ?? []) all.push({ id: `p${p.id}`, group: t("Projects"), label: p.name, hint: p.description, icon: <FolderKanban />, run: go(`/projects/${p.id}`) });
    for (const a of apps.data ?? [])
      all.push({ id: `a${a.id}`, group: t("Applications"), label: a.name, hint: a.domains.split(",")[0] || a.git_repository.replace(/^https?:\/\//, "") || a.docker_image, icon: <Box />, status: a.status, keywords: a.domains, run: go(`/applications/${a.id}`) });
    for (const d of dbs.data ?? []) all.push({ id: `d${d.id}`, group: t("Databases"), label: d.name, hint: d.image, icon: <Database />, status: d.status, run: go(`/databases/${d.id}`) });
    for (const s of svcs.data ?? []) all.push({ id: `s${s.id}`, group: t("Services"), label: s.name, hint: s.domain || s.template, icon: <Boxes />, status: s.status, run: go(`/services/${s.id}`) });
    for (const s of servers.data ?? []) all.push({ id: `v${s.id}`, group: t("Servers"), label: s.name, hint: s.is_local ? t("local") : s.host, icon: <ServerIcon />, status: s.status, run: go(`/servers/${s.id}`) });
    const needle = q.trim().toLowerCase();
    if (!needle) return all.filter((i) => i.group !== "Actions" || i.id === "a-proj" || i.id === "a-srv").slice(0, 40);
    return all
      .map((i) => {
        const hay = `${i.label} ${i.hint ?? ""} ${i.keywords ?? ""} ${i.group}`.toLowerCase();
        const score = i.label.toLowerCase().startsWith(needle) ? 3 : i.label.toLowerCase().includes(needle) ? 2 : hay.includes(needle) ? 1 : 0;
        return { i, score };
      })
      .filter((x) => x.score > 0)
      .sort((a, b) => b.score - a.score)
      .map((x) => x.i)
      .slice(0, 50);
  }, [q, projects.data, apps.data, dbs.data, svcs.data, servers.data]);

  useEffect(() => setActive(0), [q]);
  useEffect(() => {
    listRef.current?.querySelector(`[data-idx="${active}"]`)?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, items.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      items[active]?.run();
    } else if (e.key === "Escape") {
      onClose();
    }
  };

  let lastGroup = "";
  return createPortal(
    <div className="animate-fade fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-[12vh] backdrop-blur-[2px] dark:bg-black/60" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="animate-pop w-full max-w-[640px] overflow-hidden rounded-xl border border-border bg-surface shadow-pop" onKeyDown={onKey}>
        <div className="flex h-12 items-center gap-3 border-b border-border px-4">
          <Search className="size-4 text-subtle" />
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("Search projects, applications, servers or run a command…")}
            className="h-full flex-1 bg-transparent text-sm placeholder:text-faint focus:outline-none"
          />
          <Kbd>Esc</Kbd>
        </div>
        <div ref={listRef} className="max-h-[420px] overflow-y-auto p-1.5">
          {items.length === 0 && <div className="px-3 py-10 text-center text-[13px] text-subtle">{t("No results for “{q}”", { q })}</div>}
          {items.map((it, idx) => {
            const header = it.group !== lastGroup;
            lastGroup = it.group;
            return (
              <div key={it.id}>
                {header && <div className="px-2.5 pb-1 pt-2.5 text-2xs font-medium uppercase tracking-wider text-faint">{it.group === "Actions" ? t("Actions") : it.group}</div>}
                <button
                  data-idx={idx}
                  onMouseMove={() => setActive(idx)}
                  onClick={it.run}
                  className={cn("flex h-10 w-full items-center gap-3 rounded-md px-2.5 text-left text-[13px] [&_svg]:size-4", idx === active ? "bg-surface-2 text-fg" : "text-muted")}
                >
                  <span className={cn(idx === active ? "text-fg" : "text-faint")}>{it.icon}</span>
                  <span className="truncate font-medium text-fg">{it.label}</span>
                  {it.hint && <span className="truncate text-xs text-faint">{it.hint}</span>}
                  <span className="ml-auto flex items-center gap-2">
                    {it.status && <StatusDot status={it.status} />}
                    {idx === active && <ArrowRight className="text-subtle" />}
                  </span>
                </button>
              </div>
            );
          })}
        </div>
        <div className="flex h-10 items-center gap-4 border-t border-border bg-surface-2/50 px-4 text-2xs text-subtle">
          <span className="flex items-center gap-1.5">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> {t("navigate")}
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>
              <CornerDownLeft className="size-3" />
            </Kbd>{" "}
            {t("open")}
          </span>
          <span className="ml-auto">{t("PortDock command menu")}</span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
