import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Copy, Eye, EyeOff, LoaderCircle, X } from "lucide-react";
import { toast } from "sonner";
import { cn, copy } from "../lib/utils";

// ---------- Button ----------

type Variant = "primary" | "secondary" | "ghost" | "danger" | "outline" | "accent";
type Size = "xs" | "sm" | "md" | "lg" | "icon" | "icon-sm";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
  trailing?: ReactNode;
}

const variants: Record<Variant, string> = {
  primary: "bg-inverse text-inverse-fg shadow-xs hover:opacity-90 active:opacity-100",
  accent: "bg-accent text-accent-fg shadow-xs hover:brightness-110",
  secondary: "bg-surface text-fg border border-border shadow-xs hover:bg-surface-2 hover:border-border-strong",
  outline: "border border-border text-fg hover:bg-surface-2",
  ghost: "text-muted hover:text-fg hover:bg-surface-2",
  danger: "bg-surface text-danger border border-border shadow-xs hover:bg-danger-soft hover:border-danger/40",
};

const sizes: Record<Size, string> = {
  xs: "h-6 px-2 text-xs gap-1 rounded-md [&_svg]:size-3",
  sm: "h-7 px-2.5 text-[13px] gap-1.5 rounded-md [&_svg]:size-3.5",
  md: "h-8 px-3 text-[13px] gap-1.5 rounded-md [&_svg]:size-4",
  lg: "h-10 px-4 text-sm gap-2 rounded-lg [&_svg]:size-4",
  icon: "size-8 justify-center rounded-md [&_svg]:size-4",
  "icon-sm": "size-7 justify-center rounded-md [&_svg]:size-3.5",
};

export function Button({ variant = "secondary", size = "md", loading, icon, trailing, className, children, disabled, type = "button", ...props }: ButtonProps) {
  return (
    <button
      type={type}
      className={cn(
        "inline-flex shrink-0 items-center font-medium whitespace-nowrap select-none transition-[background,border,color,opacity,box-shadow] duration-150",
        "focus-visible:ring-[3px] focus-visible:ring-[var(--ring)] disabled:pointer-events-none disabled:opacity-50",
        variants[variant],
        sizes[size],
        className,
      )}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? <LoaderCircle className="animate-spin" /> : icon}
      {children}
      {trailing}
    </button>
  );
}

// ---------- Form controls ----------

const control =
  "w-full rounded-md border border-border bg-surface px-2.5 text-[13px] text-fg shadow-xs placeholder:text-faint transition-[border,box-shadow] " +
  "hover:border-border-strong focus:outline-none focus:border-accent focus:ring-[3px] focus:ring-[var(--ring)] disabled:cursor-not-allowed disabled:bg-surface-2 disabled:text-subtle";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn(control, "h-8", className)} {...props} />;
}

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn(control, "min-h-24 py-2 font-mono text-[12.5px] leading-relaxed", className)} spellCheck={false} {...props} />;
}

export function Select({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <div className={cn("relative w-full", className)}>
      <select className={cn(control, "h-8 appearance-none pr-8")} {...props}>
        {children}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-subtle" />
    </div>
  );
}

export function PasswordInput(props: InputHTMLAttributes<HTMLInputElement>) {
  const [show, setShow] = useState(false);
  return (
    <div className="relative">
      <Input {...props} type={show ? "text" : "password"} className={cn("pr-9", props.className)} />
      <button type="button" tabIndex={-1} onClick={() => setShow(!show)} className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-fg">
        {show ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
      </button>
    </div>
  );
}

export function Field({ label, hint, children, className, required }: { label: ReactNode; hint?: ReactNode; children: ReactNode; className?: string; required?: boolean }) {
  return (
    <label className={cn("block", className)}>
      <span className="mb-1.5 block text-[13px] font-medium text-fg">
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </span>
      {children}
      {hint && <span className="mt-1.5 block text-xs leading-relaxed text-subtle">{hint}</span>}
    </label>
  );
}

export function Switch({ checked, onChange, label, description, disabled }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; description?: ReactNode; disabled?: boolean }) {
  return (
    <label className={cn("flex cursor-pointer items-start gap-3 select-none", disabled && "cursor-not-allowed opacity-60")}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cn(
          "relative mt-0.5 h-[18px] w-8 shrink-0 rounded-full border transition-colors focus-visible:ring-[3px] focus-visible:ring-[var(--ring)]",
          checked ? "border-transparent bg-inverse" : "border-border-strong bg-surface-3",
        )}
      >
        <span className={cn("absolute top-[1px] left-[1px] size-3.5 rounded-full shadow-sm transition-transform", checked ? "translate-x-[14px] bg-inverse-fg" : "bg-white")} />
      </button>
      {(label || description) && (
        <span className="min-w-0">
          {label && <span className="block text-[13px] font-medium leading-5">{label}</span>}
          {description && <span className="block text-xs leading-relaxed text-subtle">{description}</span>}
        </span>
      )}
    </label>
  );
}

export function SegmentedControl<T extends string>({ value, onChange, options, className }: { value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode }[]; className?: string }) {
  return (
    <div className={cn("inline-flex h-8 items-center rounded-md border border-border bg-surface-2 p-0.5", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            "h-full rounded-[5px] px-2.5 text-xs font-medium capitalize transition-colors",
            value === o.value ? "bg-surface text-fg shadow-xs" : "text-subtle hover:text-fg",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

// ---------- Surfaces ----------

export function Card({ className, children, title, actions, description, padded }: { className?: string; children?: ReactNode; title?: ReactNode; actions?: ReactNode; description?: ReactNode; padded?: boolean }) {
  return (
    <section className={cn("overflow-hidden rounded-xl border border-border bg-surface shadow-card", className)}>
      {(title || actions) && (
        <header className="flex min-h-12 items-center justify-between gap-4 border-b border-border px-4 py-3">
          <div className="min-w-0">
            {title && <h3 className="text-sm font-semibold leading-5 tracking-[-0.005em]">{title}</h3>}
            {description && <p className="mt-0.5 text-xs leading-relaxed text-subtle">{description}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      {padded ? <div className="p-4">{children}</div> : children}
    </section>
  );
}

/** Two-column settings row: title & description on the left, controls on the right. */
export function SettingsRow({ title, description, children }: { title: ReactNode; description?: ReactNode; children: ReactNode }) {
  return (
    <div className="grid gap-4 border-b border-border px-5 py-5 last:border-0 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] lg:gap-10">
      <div>
        <h4 className="text-[13px] font-semibold">{title}</h4>
        {description && <p className="mt-1 text-xs leading-relaxed text-subtle">{description}</p>}
      </div>
      <div className="grid gap-4 sm:grid-cols-2">{children}</div>
    </div>
  );
}

const HeaderSlotContext = createContext<HTMLElement | null>(null);
export const HeaderSlotProvider = HeaderSlotContext.Provider;

/** Renders breadcrumbs into the top bar when available. */
export function Breadcrumbs({ children }: { children: ReactNode }) {
  const slot = useContext(HeaderSlotContext);
  if (!slot) return null;
  return createPortal(<div className="flex min-w-0 items-center gap-1.5 truncate text-[13px] text-subtle [&_a:hover]:text-fg [&_a]:transition-colors">{children}</div>, slot);
}

export function PageHeader({ title, subtitle, actions, icon, breadcrumbs, meta }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; icon?: ReactNode; breadcrumbs?: ReactNode; meta?: ReactNode }) {
  return (
    <div className="mb-6">
      {breadcrumbs && <Breadcrumbs>{breadcrumbs}</Breadcrumbs>}
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
        <div className="flex min-w-0 items-start gap-3.5">
          {icon && <div className="mt-0.5 flex size-10 shrink-0 items-center justify-center rounded-lg border border-border bg-surface text-fg shadow-xs [&_svg]:size-[18px]">{icon}</div>}
          <div className="min-w-0">
            <h1 className="truncate text-[22px] font-semibold leading-8 tracking-[-0.02em]">{title}</h1>
            {subtitle && <div className="mt-0.5 text-[13px] text-subtle">{subtitle}</div>}
            {meta && <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-subtle">{meta}</div>}
          </div>
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}

export function Empty({ icon, title, description, action, compact }: { icon?: ReactNode; title: ReactNode; description?: ReactNode; action?: ReactNode; compact?: boolean }) {
  return (
    <div className={cn("relative flex flex-col items-center justify-center overflow-hidden rounded-xl border border-dashed border-border-strong/70 px-6 text-center", compact ? "py-10" : "py-16")}>
      <div className="bg-grid pointer-events-none absolute inset-0 opacity-60 [mask-image:radial-gradient(ellipse_at_center,black,transparent_70%)]" />
      {icon && <div className="relative mb-4 flex size-11 items-center justify-center rounded-xl border border-border bg-surface text-muted shadow-card [&_svg]:size-5">{icon}</div>}
      <p className="relative text-sm font-semibold">{title}</p>
      {description && <p className="relative mt-1.5 max-w-sm text-[13px] leading-relaxed text-subtle">{description}</p>}
      {action && <div className="relative mt-5">{action}</div>}
    </div>
  );
}

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle className={cn("size-4 animate-spin text-subtle", className)} />;
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("skeleton h-4", className)} />;
}

export function Loading({ rows = 3 }: { rows?: number }) {
  return (
    <div className="space-y-3 py-2" aria-busy>
      <Skeleton className="h-7 w-56" />
      <Skeleton className="h-4 w-80" />
      <div className="grid gap-3 pt-4">
        {Array.from({ length: rows }).map((_, i) => (
          <Skeleton key={i} className="h-16 w-full rounded-xl" />
        ))}
      </div>
    </div>
  );
}

export function ErrorBox({ error, title = "Something went wrong" }: { error: unknown; title?: string }) {
  return (
    <div className="flex gap-3 rounded-lg border border-danger/25 bg-danger-soft px-4 py-3 text-[13px]">
      <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-danger" />
      <div className="min-w-0">
        <div className="font-medium text-danger">{title}</div>
        <div className="mt-0.5 break-words text-muted">{error instanceof Error ? error.message : String(error)}</div>
      </div>
    </div>
  );
}

// ---------- Status ----------

type Tone = "success" | "info" | "warning" | "danger" | "neutral";

const statusTone: Record<string, Tone> = {
  running: "success",
  success: "success",
  reachable: "success",
  healthy: "success",
  deploying: "info",
  starting: "info",
  queued: "warning",
  degraded: "warning",
  restarting: "warning",
  paused: "warning",
  failed: "danger",
  unreachable: "danger",
  exited: "danger",
  dead: "danger",
  cancelled: "neutral",
  stopped: "neutral",
  created: "neutral",
  missing: "neutral",
  unknown: "neutral",
  disabled: "neutral",
};

const toneText: Record<Tone, string> = {
  success: "text-success",
  info: "text-info",
  warning: "text-warning",
  danger: "text-danger",
  neutral: "text-faint",
};

const statusLabel: Record<string, string> = {
  success: "Ready",
  running: "Running",
  deploying: "Building",
  queued: "Queued",
  failed: "Failed",
  cancelled: "Canceled",
  stopped: "Stopped",
  reachable: "Online",
  unreachable: "Offline",
  exited: "Exited",
  degraded: "Degraded",
};

export function StatusDot({ status, className }: { status: string; className?: string }) {
  const tone = statusTone[status] ?? "neutral";
  const live = status === "deploying" || status === "queued" || status === "starting";
  return <span className={cn("relative inline-flex size-2 shrink-0 rounded-full bg-current", toneText[tone], live && "pulse-dot", className)} />;
}

export function StatusBadge({ status, className, label }: { status: string; className?: string; label?: string }) {
  return (
    <span className={cn("inline-flex h-6 items-center gap-1.5 rounded-md border border-border bg-surface px-2 text-xs font-medium text-fg shadow-xs", className)}>
      <StatusDot status={status} />
      {label ?? statusLabel[status] ?? status.charAt(0).toUpperCase() + status.slice(1)}
    </span>
  );
}

export function Badge({ children, className, tone = "neutral" }: { children: ReactNode; className?: string; tone?: "neutral" | "accent" | "success" | "warning" | "danger" | "info" }) {
  const tones = {
    neutral: "bg-surface-2 text-muted border-border",
    accent: "bg-accent-soft text-accent border-accent/20",
    success: "bg-success-soft text-success border-success/20",
    warning: "bg-warning-soft text-warning border-warning/20",
    danger: "bg-danger-soft text-danger border-danger/20",
    info: "bg-info-soft text-info border-info/20",
  };
  return <span className={cn("inline-flex h-5 items-center gap-1 whitespace-nowrap rounded border px-1.5 text-2xs font-medium [&_svg]:size-3", tones[tone], className)}>{children}</span>;
}

export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return <kbd className={cn("inline-flex h-5 min-w-5 items-center justify-center rounded border border-border bg-surface-2 px-1 font-sans text-2xs font-medium text-subtle", className)}>{children}</kbd>;
}

// ---------- Tabs ----------

export function Tabs<T extends string>({ tabs, value, onChange, className }: { tabs: { id: T; label: ReactNode; icon?: ReactNode; hidden?: boolean; count?: number }[]; value: T; onChange: (id: T) => void; className?: string }) {
  return (
    <div className={cn("mb-6 flex gap-5 overflow-x-auto border-b border-border", className)}>
      {tabs
        .filter((t) => !t.hidden)
        .map((t) => (
          <button
            key={t.id}
            onClick={() => onChange(t.id)}
            className={cn(
              "relative -mb-px flex h-10 items-center gap-2 whitespace-nowrap border-b-2 text-[13px] font-medium transition-colors [&_svg]:size-4",
              value === t.id ? "border-fg text-fg" : "border-transparent text-subtle hover:text-fg",
            )}
          >
            {t.icon && <span className={cn(value === t.id ? "text-fg" : "text-faint")}>{t.icon}</span>}
            {t.label}
            {t.count !== undefined && <span className="rounded bg-surface-2 px-1.5 text-2xs text-subtle">{t.count}</span>}
          </button>
        ))}
    </div>
  );
}

// ---------- Overlays ----------

export function Modal({ open, onClose, title, description, children, footer, size = "md" }: { open: boolean; onClose: () => void; title: ReactNode; description?: ReactNode; children: ReactNode; footer?: ReactNode; size?: "sm" | "md" | "lg" | "xl" }) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [open, onClose]);
  if (!open) return null;
  const widths = { sm: "max-w-md", md: "max-w-lg", lg: "max-w-2xl", xl: "max-w-5xl" };
  return createPortal(
    <div className="animate-fade fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 pt-[8vh] backdrop-blur-[2px] dark:bg-black/60" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div role="dialog" aria-modal className={cn("animate-pop w-full overflow-hidden rounded-xl border border-border bg-surface shadow-pop", widths[size])}>
        <div className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
          <div className="min-w-0">
            <h2 className="text-[15px] font-semibold tracking-[-0.01em]">{title}</h2>
            {description && <p className="mt-1 text-[13px] leading-relaxed text-subtle">{description}</p>}
          </div>
          <button onClick={onClose} className="-mr-1 flex size-7 items-center justify-center rounded-md text-subtle hover:bg-surface-2 hover:text-fg">
            <X className="size-4" />
          </button>
        </div>
        <div className="px-5 py-5">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-border bg-surface-2/50 px-5 py-3">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

/** Minimal dropdown menu anchored to its trigger. */
export function Menu({ trigger, children, align = "end", side = "bottom", className }: { trigger: ReactNode; children: ReactNode; align?: "start" | "end"; side?: "bottom" | "top"; className?: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div ref={ref} className={cn("relative", className)}>
      <div onClick={() => setOpen(!open)}>{trigger}</div>
      {open && (
        <div
          onClick={() => setOpen(false)}
          className={cn(
            "animate-pop absolute z-40 min-w-52 rounded-lg border border-border bg-surface p-1 shadow-pop",
            align === "end" ? "right-0" : "left-0",
            side === "bottom" ? "top-full mt-1.5" : "bottom-full mb-1.5",
          )}
        >
          {children}
        </div>
      )}
    </div>
  );
}

export function MenuItem({ icon, children, onClick, danger, shortcut, active }: { icon?: ReactNode; children: ReactNode; onClick?: () => void; danger?: boolean; shortcut?: ReactNode; active?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex h-8 w-full items-center gap-2.5 rounded-md px-2 text-left text-[13px] transition-colors [&_svg]:size-4",
        danger ? "text-danger hover:bg-danger-soft" : "text-fg hover:bg-surface-2",
      )}
    >
      {icon && <span className={cn(!danger && "text-subtle")}>{icon}</span>}
      <span className="flex-1">{children}</span>
      {active && <Check className="text-fg" />}
      {shortcut}
    </button>
  );
}

export function MenuSeparator() {
  return <div className="my-1 h-px bg-border" />;
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <div className="px-2 pb-1 pt-1.5 text-2xs font-medium uppercase tracking-wider text-faint">{children}</div>;
}

// ---------- Confirm dialog (promise based) ----------

interface ConfirmOptions {
  title: string;
  description?: ReactNode;
  confirmText?: string;
  danger?: boolean;
  /** Require typing this text to confirm. */
  typeToConfirm?: string;
}

const ConfirmContext = createContext<(o: ConfirmOptions) => Promise<boolean>>(async () => false);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<(ConfirmOptions & { resolve: (v: boolean) => void }) | null>(null);
  const [typed, setTyped] = useState("");
  const confirm = (o: ConfirmOptions) =>
    new Promise<boolean>((resolve) => {
      setTyped("");
      setState({ ...o, resolve });
    });
  const close = (v: boolean) => {
    state?.resolve(v);
    setState(null);
  };
  const blocked = !!state?.typeToConfirm && typed !== state.typeToConfirm;
  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <Modal
        open={!!state}
        onClose={() => close(false)}
        title={state?.title}
        size="sm"
        footer={
          <>
            <Button variant="secondary" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button variant={state?.danger ? "danger" : "primary"} className={state?.danger ? "border-danger/40 bg-danger text-white hover:bg-danger hover:opacity-90" : ""} disabled={blocked} onClick={() => close(true)}>
              {state?.confirmText ?? "Confirm"}
            </Button>
          </>
        }
      >
        <form
          className="space-y-4 text-[13px] leading-relaxed text-muted"
          onSubmit={(e) => {
            e.preventDefault();
            if (!blocked) close(true);
          }}
        >
          <div>{state?.description ?? "Are you sure you want to continue?"}</div>
          {state?.typeToConfirm && (
            <Field label={<span className="font-normal text-muted">Type <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-xs text-fg">{state.typeToConfirm}</code> to confirm</span>}>
              <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus />
            </Field>
          )}
        </form>
      </Modal>
    </ConfirmContext.Provider>
  );
}

export const useConfirm = () => useContext(ConfirmContext);

// ---------- Data display ----------

export function CopyButton({ value, className }: { value: string; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className={cn("flex size-7 shrink-0 items-center justify-center rounded-md text-subtle transition-colors hover:bg-surface-2 hover:text-fg", className)}
      onClick={async (e) => {
        e.preventDefault();
        e.stopPropagation();
        if (await copy(value)) {
          setDone(true);
          toast.success("Copied to clipboard");
          setTimeout(() => setDone(false), 1500);
        }
      }}
      title="Copy"
    >
      {done ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
    </button>
  );
}

export function CopyField({ value, secret, label }: { value: string; secret?: boolean; label?: ReactNode }) {
  const [show, setShow] = useState(!secret);
  return (
    <div className="min-w-0">
      {label && <div className="mb-1.5 text-[13px] font-medium">{label}</div>}
      <div className="flex h-8 items-center gap-0.5 rounded-md border border-border bg-surface-2/60 pl-2.5 pr-0.5">
        <code className="min-w-0 flex-1 truncate font-mono text-xs">{show ? value || "—" : "•".repeat(Math.min(value.length || 12, 28))}</code>
        {secret && (
          <button type="button" className="flex size-7 items-center justify-center rounded-md text-subtle hover:bg-surface-2 hover:text-fg" onClick={() => setShow(!show)}>
            {show ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
          </button>
        )}
        {value && <CopyButton value={value} />}
      </div>
    </div>
  );
}

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("overflow-x-auto", className)}>
      <table className="tabular w-full text-[13px] [&_tbody_tr]:border-t [&_tbody_tr]:border-border [&_tbody_tr]:transition-colors [&_tbody_tr:hover]:bg-surface-2/50 [&_td]:h-12 [&_td]:px-4 [&_td]:py-2 [&_th]:h-9 [&_th]:bg-surface-2/60 [&_th]:px-4 [&_th]:text-left [&_th]:text-2xs [&_th]:font-medium [&_th]:uppercase [&_th]:tracking-wider [&_th]:text-subtle">
        {children}
      </table>
    </div>
  );
}

export function DescriptionList({ items, className }: { items: [ReactNode, ReactNode][]; className?: string }) {
  return (
    <dl className={cn("divide-y divide-border", className)}>
      {items.map(([k, v], i) => (
        <div key={i} className="flex min-h-10 items-center justify-between gap-4 px-4 py-2 text-[13px]">
          <dt className="shrink-0 text-subtle">{k}</dt>
          <dd className="min-w-0 truncate text-right font-medium">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Stat({ label, value, sub, icon, tone, trend }: { label: ReactNode; value: ReactNode; sub?: ReactNode; icon?: ReactNode; tone?: string; trend?: ReactNode }) {
  return (
    <div className="rounded-xl border border-border bg-surface p-4 shadow-card">
      <div className="flex items-center justify-between text-[13px] font-medium text-subtle">
        {label}
        {icon && <span className={cn("text-faint [&_svg]:size-4", tone)}>{icon}</span>}
      </div>
      <div className="tabular mt-2 flex items-baseline gap-2 text-[26px] font-semibold leading-8 tracking-[-0.02em]">
        {value}
        {trend}
      </div>
      {sub && <div className="mt-1 text-xs text-subtle">{sub}</div>}
    </div>
  );
}

export function Meter({ value, max, className }: { value: number; max: number; className?: string }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const tone = pct > 90 ? "bg-danger" : pct > 75 ? "bg-warning" : "bg-fg";
  return (
    <div className={cn("h-1 w-full overflow-hidden rounded-full bg-surface-3", className)}>
      <div className={cn("h-full rounded-full transition-all", tone)} style={{ width: `${pct}%` }} />
    </div>
  );
}

export function Avatar({ name, className }: { name: string; className?: string }) {
  const initials = name
    .split(/[\s@.]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase())
    .join("");
  return (
    <span className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-zinc-600 to-zinc-800 text-2xs font-semibold text-white ring-1 ring-black/5 dark:from-zinc-500 dark:to-zinc-700", className)}>
      {initials || "?"}
    </span>
  );
}
