import { createContext, useContext, useState, type ReactNode } from "react";
import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronsUpDown,
  FolderKanban,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu as MenuIcon,
  Monitor,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Rocket,
  Search,
  Server as ServerIcon,
  Settings,
  Sun,
  User as UserIcon,
  Users,
  X,
} from "lucide-react";
import { api } from "../lib/api";
import type { AuthStatus, Server, User } from "../lib/types";
import { useLiveEvents } from "../lib/sse";
import { useTheme, type ThemeMode } from "../lib/theme";
import { cn } from "../lib/utils";
import { CommandPaletteProvider, useCommandPalette } from "./CommandPalette";
import { Avatar, Button, HeaderSlotProvider, Kbd, Menu, MenuItem, MenuLabel, MenuSeparator, Skeleton } from "./ui";

const UserContext = createContext<User | null>(null);
export const useUser = () => useContext(UserContext)!;

export function useAuthStatus() {
  return useQuery({ queryKey: ["auth-status"], queryFn: () => api.get<AuthStatus>("/auth/status"), staleTime: 60_000 });
}

export function LogoMark({ className }: { className?: string }) {
  return (
    <div className={cn("flex size-7 shrink-0 items-center justify-center rounded-lg bg-inverse text-inverse-fg shadow-xs", className)}>
      <svg viewBox="0 0 24 24" className="size-4" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M4 15.5 12 20l8-4.5" />
        <path d="M4 11 12 15.5 20 11" opacity=".55" />
        <path d="M12 4 4 8.5 12 13l8-4.5z" />
      </svg>
    </div>
  );
}

export function Logo({ className }: { className?: string }) {
  return (
    <div className={cn("flex items-center gap-2.5", className)}>
      <LogoMark />
      <span className="text-[15px] font-semibold tracking-[-0.02em]">Shipyard</span>
    </div>
  );
}

const navGroups = [
  {
    label: "Platform",
    items: [
      { to: "/", label: "Overview", icon: LayoutDashboard, end: true },
      { to: "/projects", label: "Projects", icon: FolderKanban },
      { to: "/deployments", label: "Deployments", icon: Rocket },
    ],
  },
  {
    label: "Infrastructure",
    items: [{ to: "/servers", label: "Servers", icon: ServerIcon }],
  },
  {
    label: "Organization",
    items: [
      { to: "/settings?tab=users", label: "Team", icon: Users, match: "tab=users" },
      { to: "/settings", label: "Settings", icon: Settings, exclude: "tab=users" },
    ],
  },
];

function themeIcon(m: ThemeMode) {
  return m === "dark" ? <Moon /> : m === "light" ? <Sun /> : <Monitor />;
}

function Sidebar({ user, collapsed, onToggle, onNavigate, theme, setTheme, onLogout }: { user: User; collapsed: boolean; onToggle?: () => void; onNavigate?: () => void; theme: ThemeMode; setTheme: (m: ThemeMode) => void; onLogout: () => void }) {
  const openPalette = useCommandPalette();
  const location = useLocation();
  return (
    <aside className={cn("flex h-full flex-col border-r border-border bg-surface transition-[width] duration-200", collapsed ? "w-[60px]" : "w-[248px]")}>
      <div className={cn("flex h-14 items-center border-b border-border", collapsed ? "justify-center px-2" : "px-3")}>
        <Link to="/" onClick={onNavigate} className={cn("flex min-w-0 items-center gap-2.5 rounded-md", !collapsed && "flex-1 px-1.5 py-1 hover:bg-surface-2")}>
          <LogoMark />
          {!collapsed && (
            <div className="min-w-0 flex-1 leading-tight">
              <div className="truncate text-[13px] font-semibold tracking-[-0.01em]">Shipyard</div>
              <div className="truncate text-2xs text-subtle">Self-hosted platform</div>
            </div>
          )}
        </Link>
      </div>

      <div className={cn("pt-3", collapsed ? "px-2" : "px-3")}>
        <button
          onClick={openPalette}
          className={cn(
            "flex h-8 w-full items-center gap-2 rounded-md border border-border bg-surface-2/60 text-[13px] text-subtle shadow-xs transition-colors hover:border-border-strong hover:text-fg",
            collapsed ? "justify-center" : "px-2.5",
          )}
          title="Search (Ctrl+K)"
        >
          <Search className="size-3.5" />
          {!collapsed && (
            <>
              <span className="flex-1 text-left">Search…</span>
              <Kbd>Ctrl K</Kbd>
            </>
          )}
        </button>
      </div>

      <nav className={cn("flex-1 overflow-y-auto pb-4", collapsed ? "px-2" : "px-3")}>
        {navGroups.map((g) => (
          <div key={g.label} className="mt-5">
            {!collapsed ? <div className="mb-1 px-2 text-2xs font-medium uppercase tracking-wider text-faint">{g.label}</div> : <div className="mx-2 mb-2 h-px bg-border" />}
            <div className="space-y-px">
              {g.items.map((n) => {
                const it = n as typeof n & { match?: string; exclude?: string; end?: boolean };
                return (
                  <NavLink
                    key={n.to}
                    to={n.to}
                    end={it.end}
                    onClick={onNavigate}
                    title={collapsed ? n.label : undefined}
                    className={({ isActive }) => {
                      let active = isActive;
                      if (it.match) active = location.pathname === "/settings" && location.search.includes(it.match);
                      if (it.exclude && location.search.includes(it.exclude)) active = false;
                      return cn(
                        "group flex h-8 items-center gap-2.5 rounded-md text-[13px] font-medium transition-colors [&_svg]:size-4",
                        collapsed ? "justify-center" : "px-2",
                        active ? "bg-surface-2 text-fg [&_svg]:text-fg" : "text-muted hover:bg-surface-2/70 hover:text-fg [&_svg]:text-faint hover:[&_svg]:text-muted",
                      );
                    }}
                  >
                    <n.icon />
                    {!collapsed && n.label}
                  </NavLink>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      <div className={cn("space-y-1 border-t border-border py-2", collapsed ? "px-2" : "px-3")}>
        {onToggle && (
          <button onClick={onToggle} className={cn("flex h-8 w-full items-center gap-2.5 rounded-md text-[13px] text-muted hover:bg-surface-2 hover:text-fg [&_svg]:size-4 [&_svg]:text-faint", collapsed ? "justify-center" : "px-2")}>
            {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
            {!collapsed && "Collapse sidebar"}
          </button>
        )}
        <Menu
          side="top"
          align="start"
          trigger={
            <button className={cn("flex w-full items-center gap-2.5 rounded-md py-1.5 text-left transition-colors hover:bg-surface-2", collapsed ? "justify-center" : "px-2")}>
              <Avatar name={user.name || user.email} />
              {!collapsed && (
                <>
                  <div className="min-w-0 flex-1 leading-tight">
                    <div className="truncate text-[13px] font-medium">{user.name}</div>
                    <div className="truncate text-2xs text-subtle">{user.email}</div>
                  </div>
                  <ChevronsUpDown className="size-3.5 text-faint" />
                </>
              )}
            </button>
          }
        >
          <div className="px-2 py-1.5">
            <div className="text-[13px] font-medium">{user.name}</div>
            <div className="text-xs text-subtle">
              {user.email} · <span className="capitalize">{user.role}</span>
            </div>
          </div>
          <MenuSeparator />
          <Link to="/settings?tab=profile">
            <MenuItem icon={<UserIcon />}>Profile</MenuItem>
          </Link>
          <Link to="/settings?tab=tokens">
            <MenuItem icon={<KeyRound />}>API tokens</MenuItem>
          </Link>
          <MenuSeparator />
          <MenuLabel>Theme</MenuLabel>
          {(["system", "light", "dark"] as const).map((m) => (
            <MenuItem key={m} icon={themeIcon(m)} active={theme === m} onClick={() => setTheme(m)}>
              <span className="capitalize">{m}</span>
            </MenuItem>
          ))}
          <MenuSeparator />
          <MenuItem icon={<LogOut />} onClick={onLogout}>
            Sign out
          </MenuItem>
        </Menu>
      </div>
    </aside>
  );
}

function SystemStatus() {
  const { data } = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers"), refetchInterval: 60_000 });
  if (!data) return <Skeleton className="h-7 w-40" />;
  const down = data.filter((s) => s.status === "unreachable").length;
  return (
    <Link
      to="/servers"
      className="hidden h-7 items-center gap-2 rounded-full border border-border bg-surface px-3 text-xs font-medium text-muted shadow-xs transition-colors hover:text-fg sm:flex"
    >
      <span className={cn("relative flex size-2 rounded-full", down ? "bg-danger" : "bg-success")}>
        <span className={cn("absolute inset-0 animate-ping rounded-full opacity-40", down ? "bg-danger" : "bg-success")} />
      </span>
      {down ? `${down} server${down > 1 ? "s" : ""} offline` : "All systems operational"}
    </Link>
  );
}

function Topbar({ onMenu, slotRef }: { onMenu: () => void; slotRef: (el: HTMLDivElement | null) => void }) {
  const user = useUser();
  const admin = user.role !== "member";
  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-3 border-b border-border bg-surface/85 px-4 backdrop-blur-md md:px-6">
      <button onClick={onMenu} className="-ml-1 flex size-8 items-center justify-center rounded-md text-muted hover:bg-surface-2 md:hidden">
        <MenuIcon className="size-4" />
      </button>
      <div ref={slotRef} className="flex min-w-0 flex-1 items-center" />
      <SystemStatus />
      <Menu
        trigger={
          <Button variant="primary" size="sm" icon={<Plus />}>
            Create
          </Button>
        }
      >
        <MenuLabel>Create new</MenuLabel>
        <Link to="/projects?new=1">
          <MenuItem icon={<FolderKanban />}>Project</MenuItem>
        </Link>
        {admin && (
          <Link to="/servers?new=1">
            <MenuItem icon={<ServerIcon />}>Server</MenuItem>
          </Link>
        )}
        {admin && (
          <Link to="/settings?tab=users">
            <MenuItem icon={<Users />}>Team member</MenuItem>
          </Link>
        )}
      </Menu>
    </header>
  );
}

export function AppLayout() {
  const { data, isLoading } = useAuthStatus();
  const location = useLocation();
  const qc = useQueryClient();
  const [theme, setTheme] = useTheme();
  const [mobile, setMobile] = useState(false);
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem("shipyard.sidebar") === "collapsed";
    } catch {
      return false;
    }
  });
  useLiveEvents();

  const toggle = () =>
    setCollapsed((c) => {
      try {
        localStorage.setItem("shipyard.sidebar", c ? "expanded" : "collapsed");
      } catch {
        /* ignore */
      }
      return !c;
    });
  const logout = async () => {
    await api.post("/auth/logout");
    qc.clear();
    window.location.href = "/login";
  };

  if (isLoading)
    return (
      <div className="flex h-full items-center justify-center">
        <LogoMark className="animate-pulse" />
      </div>
    );
  if (!data?.user) return <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />;

  return (
    <UserContext.Provider value={data.user}>
      <CommandPaletteProvider setTheme={setTheme} onLogout={logout}>
        <div className="flex h-full">
          <div className="hidden md:block">
            <Sidebar user={data.user} collapsed={collapsed} onToggle={toggle} theme={theme} setTheme={setTheme} onLogout={logout} />
          </div>
          {mobile && (
            <div className="animate-fade fixed inset-0 z-40 flex md:hidden">
              <Sidebar user={data.user} collapsed={false} onNavigate={() => setMobile(false)} theme={theme} setTheme={setTheme} onLogout={logout} />
              <div className="flex-1 bg-black/40" onClick={() => setMobile(false)}>
                <button className="m-3 flex size-8 items-center justify-center rounded-md bg-surface text-muted">
                  <X className="size-4" />
                </button>
              </div>
            </div>
          )}
          <div className="flex min-w-0 flex-1 flex-col">
            <Topbar onMenu={() => setMobile(true)} slotRef={setSlot} />
            <main className="min-h-0 flex-1 overflow-y-auto">
              <HeaderSlotProvider value={slot}>
                <div className="mx-auto w-full max-w-[1320px] px-4 py-7 md:px-8">
                  <Outlet />
                </div>
              </HeaderSlotProvider>
            </main>
          </div>
        </div>
      </CommandPaletteProvider>
    </UserContext.Provider>
  );
}

export function AdminOnly({ children }: { children: ReactNode }) {
  const u = useUser();
  return u.role === "owner" || u.role === "admin" ? <>{children}</> : null;
}
