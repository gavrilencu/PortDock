// Settings → AI assistant (admins): the OpenRouter key and model, who may use
// the assistant and extra instructions for its system prompt.
import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ExternalLink, PlugZap, RotateCcw, Save, Search, ShieldAlert, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../lib/api";
import { intlLocale, t, tk, tx } from "../../lib/i18n";
import type { ChatModel, ChatSettings, User } from "../../lib/types";
import { cn } from "../../lib/utils";
import { Avatar, Badge, Button, Card, ErrorBox, Field, Input, Loading, PasswordInput, SegmentedControl, Skeleton, Spinner, Switch, Textarea, useConfirm } from "../ui";
import { useChatStatus } from "./ChatWidget";

type Access = ChatSettings["access"];

interface Form {
  enabled: boolean;
  model: string;
  access: Access;
  user_ids: number[];
  instructions: string;
  /** A new key to save; empty keeps the saved one. */
  api_key: string;
}

interface TestResult {
  ok: boolean;
  label: string;
  usage: number;
  limit: number | null;
  reply: string;
}

const ACCESS: { value: Access; label: string; hint: string }[] = [
  { value: "all", label: tk("All users"), hint: tk("Every signed-in user of this instance can use the assistant.") },
  { value: "admins", label: tk("Admins only"), hint: tk("Only owners and admins can use the assistant.") },
  { value: "selected", label: tk("Selected users"), hint: tk("Only the users checked below can use the assistant.") },
];

/** Rows rendered in the model list at most (the catalog has hundreds). */
const LIST_LIMIT = 100;

const formFrom = (s: ChatSettings): Form => ({
  enabled: !!s.enabled,
  model: s.model ?? "",
  access: s.access ?? "all",
  user_ids: s.user_ids ?? [],
  instructions: s.instructions ?? "",
  api_key: "",
});

function sameIds(a: number[], b: number[]): boolean {
  if (a.length !== b.length) return false;
  const x = [...a].sort((p, q) => p - q);
  const y = [...b].sort((p, q) => p - q);
  return x.every((v, i) => v === y[i]);
}

function clip(s: string, n: number): string {
  const v = s.replace(/\s+/g, " ").trim();
  return v.length > n ? `${v.slice(0, n - 1)}…` : v;
}

function usd(n: number): string {
  return n > 0 && n < 0.005 ? "<$0.01" : `$${n.toFixed(2)}`;
}

/** USD per 1M tokens from a per-token decimal string (NaN when unknown). */
function perMillion(price: string | undefined): number {
  if (price === undefined || price === null || price === "") return NaN;
  const n = Number(price);
  return Number.isFinite(n) ? n * 1e6 : NaN;
}

function price(m: ChatModel): { free: boolean; perMillion: boolean; text: string } {
  const input = perMillion(m.prompt_price);
  const output = perMillion(m.completion_price);
  if (input === 0 && output === 0) return { free: true, perMillion: false, text: t("free") };
  if (!(input >= 0 && output >= 0)) return { free: false, perMillion: false, text: t("variable price") };
  return { free: false, perMillion: true, text: t("in {in} · out {out}", { in: usd(input), out: usd(output) }) };
}

const isFree = (m: ChatModel) => perMillion(m.prompt_price) === 0 && perMillion(m.completion_price) === 0;

function contextLabel(n: number): string {
  if (!n || n < 0) return "";
  if (n >= 1_000_000) return `${(n / 1_000_000).toLocaleString(intlLocale(), { maximumFractionDigits: 1 })}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return String(n);
}

/** Models whose id or name contain every typed word; exact and prefix matches first. */
function filterModels(all: ChatModel[], query: string, freeOnly: boolean): ChatModel[] {
  const list = freeOnly ? all.filter(isFree) : all;
  const needle = query.trim().toLowerCase();
  if (!needle) return list;
  const words = needle.split(/\s+/);
  const ranked: { m: ChatModel; score: number }[] = [];
  for (const m of list) {
    const id = m.id.toLowerCase();
    const name = (m.name ?? "").toLowerCase();
    const hay = `${id} ${name}`;
    if (!words.every((w) => hay.includes(w))) continue;
    const score = id === needle ? 3 : id.startsWith(needle) || name.startsWith(needle) || (id.split("/")[1] ?? "").startsWith(needle) ? 2 : 1;
    ranked.push({ m, score });
  }
  return ranked.sort((a, b) => b.score - a.score).map((x) => x.m);
}

function usageText(r: TestResult): string {
  const used = usd(Number(r.usage) || 0);
  return r.limit === null || r.limit === undefined ? t("Credits used: {used}, no limit", { used }) : t("Credits used: {used} of {limit}", { used, limit: usd(Number(r.limit)) });
}

export function AssistantSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useQuery({ queryKey: ["chat-settings"], queryFn: () => api.get<ChatSettings>("/chat/settings") });
  const widgetShown = useChatStatus().data?.allowed ?? false;
  const [f, setF] = useState<Form | null>(null);
  useEffect(() => {
    if (q.data && !f) setF(formFrom(q.data));
  }, [q.data, f]);
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<User[]>("/users"), enabled: f?.access === "selected" });

  const save = useMutation({
    mutationFn: (form: Form) => {
      // Drop selections of users that no longer exist.
      const known = users.data ? new Set(users.data.map((u) => u.id)) : null;
      return api.put<ChatSettings>("/chat/settings", {
        enabled: form.enabled,
        model: form.model.trim(),
        access: form.access,
        user_ids: known ? form.user_ids.filter((id) => known.has(id)) : form.user_ids,
        instructions: form.instructions,
        ...(form.api_key.trim() ? { api_key: form.api_key.trim() } : {}),
      });
    },
    onSuccess: (s) => {
      qc.setQueryData(["chat-settings"], s);
      setF(formFrom(s));
      qc.invalidateQueries({ queryKey: ["chat-status"] });
      toast.success(t("Assistant settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  const clearKey = useMutation({
    mutationFn: () => api.put<ChatSettings>("/chat/settings", { clear_api_key: true }),
    onSuccess: (s) => {
      qc.setQueryData(["chat-settings"], s);
      setF((x) => x && { ...x, api_key: "" });
      qc.invalidateQueries({ queryKey: ["chat-status"] });
      toast.success(t("API key removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: (form: Form) =>
      api.post<TestResult>("/chat/test", {
        ...(form.api_key.trim() ? { api_key: form.api_key.trim() } : {}),
        ...(form.model.trim() ? { model: form.model.trim() } : {}),
      }),
    onSuccess: (r) => toast.success(t("Connected — key {label}, model replied: {reply}", { label: r.label || "—", reply: clip(r.reply ?? "", 160) }), { description: usageText(r) }),
    onError: (e) => toast.error(e.message),
  });

  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} title={t("The assistant settings could not be loaded")} />;
  if (!f || !q.data) return <Loading />;
  const s = q.data;
  const dirty =
    !!f.api_key.trim() ||
    f.enabled !== !!s.enabled ||
    f.model.trim() !== (s.model ?? "") ||
    f.access !== (s.access ?? "all") ||
    f.instructions !== (s.instructions ?? "") ||
    !sameIds(f.user_ids, s.user_ids ?? []);
  const accessHint = ACCESS.find((a) => a.value === f.access)?.hint;
  const canTest = !!f.api_key.trim() || s.api_key_set;

  const removeKey = async () => {
    const ok = await confirm({
      title: t("Remove the OpenRouter API key?"),
      description: t("The assistant stops answering until a new key is saved."),
      danger: true,
      confirmText: t("Remove key"),
    });
    if (ok) clearKey.mutate();
  };

  return (
    <div className="space-y-6">
      <Card
        title={t("AI assistant")}
        description={t("Answers questions about PortDock, deployments, Docker and configuration with a model of your choice on OpenRouter. It only sees what users write in the chat.")}
        actions={s.enabled ? s.api_key_set ? <Badge tone="success">{t("Enabled")}</Badge> : <Badge tone="warning">{t("Not configured")}</Badge> : <Badge>{t("Disabled")}</Badge>}
      >
        <div className="space-y-3 p-5">
          <Switch
            checked={f.enabled}
            onChange={(enabled) => setF({ ...f, enabled })}
            label={t("Enable the AI assistant")}
            description={t("Shows a chat assistant in the bottom-right corner for the users you choose.")}
          />
          {f.enabled && !s.api_key_set && !f.api_key.trim() && <p className="pl-11 text-xs text-warning">{t("Add an OpenRouter API key below — the assistant stays hidden until one is saved.")}</p>}
        </div>
        <div className="flex gap-2.5 border-t border-border bg-info-soft px-5 py-3 text-xs leading-relaxed text-info">
          <ShieldAlert className="mt-px size-4 shrink-0" />
          <span>{t("Messages are sent to OpenRouter and the provider of the selected model. Ask users not to paste secrets.")}</span>
        </div>
      </Card>

      <Card
        className="overflow-visible"
        title="OpenRouter"
        description={t("The assistant uses your OpenRouter account, so any model offered there can be used.")}
        actions={
          <Button size="sm" icon={<PlugZap />} loading={test.isPending} disabled={!canTest} onClick={() => test.mutate(f)}>
            {t("Test connection")}
          </Button>
        }
      >
        <div className="grid gap-5 p-5 md:grid-cols-2">
          <Field
            label={
              <span className="flex flex-wrap items-center gap-2">
                {t("API key")}
                {s.api_key_set && !f.api_key && (
                  <Badge tone="success">
                    <Check />
                    {t("A key is saved")}
                  </Badge>
                )}
              </span>
            }
            hint={
              <>
                {s.api_key_set && <span>{t("Type a new key to replace the saved one.")} </span>}
                {tx("Get a key at {link}", {
                  link: (
                    <a href="https://openrouter.ai/keys" target="_blank" rel="noreferrer" className="inline-flex items-center gap-0.5 font-medium text-fg underline underline-offset-2">
                      openrouter.ai/keys
                      <ExternalLink className="size-3" />
                    </a>
                  ),
                })}
              </>
            }
          >
            <div className="flex gap-2">
              <div className="min-w-0 flex-1">
                <PasswordInput value={f.api_key} onChange={(e) => setF({ ...f, api_key: e.target.value })} placeholder="sk-or-v1-…" autoComplete="off" spellCheck={false} className="font-mono text-xs" />
              </div>
              {s.api_key_set && (
                <Button variant="danger" icon={<Trash2 />} loading={clearKey.isPending} onClick={removeKey}>
                  {t("Remove key")}
                </Button>
              )}
            </div>
          </Field>
          <div>
            <label htmlFor="assistant-model" className="mb-1.5 block text-[13px] font-medium text-fg">
              {t("Model")}
            </label>
            <ModelPicker id="assistant-model" value={f.model} onChange={(model) => setF({ ...f, model })} />
          </div>
        </div>
      </Card>

      <Card title={t("Access")} description={t("Choose who can use the assistant.")}>
        <div className="space-y-4 p-5">
          <div className="space-y-2">
            <SegmentedControl
              className="max-w-full overflow-x-auto [&_button]:shrink-0 [&_button]:normal-case"
              value={f.access}
              onChange={(access) => setF({ ...f, access })}
              options={ACCESS.map((a) => ({ value: a.value, label: t(a.label) }))}
            />
            {accessHint && <p className="text-xs leading-relaxed text-subtle">{t(accessHint)}</p>}
          </div>
          {f.access === "selected" && (
            <UserPicker list={users.data} loading={users.isLoading} error={users.error} selected={f.user_ids} onChange={(user_ids) => setF({ ...f, user_ids })} />
          )}
        </div>
      </Card>

      <Card title={t("Instructions")}>
        <div className="p-5">
          <Field
            label={
              <>
                {t("Custom instructions")} <span className="font-normal text-subtle">· {t("Optional")}</span>
              </>
            }
            hint={t("Added to the assistant's system prompt — for example your team's conventions or the tone to use.")}
          >
            <Textarea
              rows={5}
              value={f.instructions}
              onChange={(e) => setF({ ...f, instructions: e.target.value })}
              placeholder={t("For example: We deploy from the main branch. Keep answers short.")}
              spellCheck
              className="font-sans text-[13px]"
            />
          </Field>
        </div>
      </Card>

      {/* Kept clear of the floating assistant button in the same corner. */}
      <div className={cn("sticky bottom-4 z-10 flex justify-end", widgetShown && "pr-14")}>
        <Button variant="primary" icon={<Save className="size-4" />} disabled={!dirty} loading={save.isPending} onClick={() => save.mutate(f)} className="shadow-pop">
          {t("Save settings")}
        </Button>
      </div>
    </div>
  );
}

/** Model id input with a searchable list of the OpenRouter catalog; any id can be typed. */
function ModelPicker({ id, value, onChange }: { id: string; value: string; onChange: (v: string) => void }) {
  const models = useQuery({ queryKey: ["chat-models"], queryFn: () => api.get<{ models: ChatModel[] }>("/chat/models"), staleTime: 10 * 60_000, retry: false });
  const all = useMemo(() => models.data?.models ?? [], [models.data]);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [freeOnly, setFreeOnly] = useState(false);
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const matches = useMemo(() => filterModels(all, query, freeOnly), [all, query, freeOnly]);
  const shown = matches.slice(0, LIST_LIMIT);
  const typed = value.trim();
  const current = typed ? all.find((m) => m.id === typed) : undefined;
  // Variants such as ":online" or ":nitro" aren't listed separately.
  const known = !!current || (typed.includes(":") && all.some((m) => m.id === typed.replace(/:[^:/]*$/, "")));

  useEffect(() => {
    if (open) listRef.current?.querySelector(`[data-idx="${active}"]`)?.scrollIntoView({ block: "nearest" });
  }, [open, active]);

  const show = () => {
    if (open) return;
    setQuery("");
    setOpen(true);
    setActive(Math.max(0, filterModels(all, "", freeOnly).slice(0, LIST_LIMIT).findIndex((m) => m.id === typed)));
  };
  const pick = (m: ChatModel) => {
    onChange(m.id);
    setQuery("");
    setOpen(false);
  };
  const onKeyDown = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (!open) show();
      else setActive((a) => Math.min(a + 1, Math.max(shown.length - 1, 0)));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      if (open && shown[active]) {
        e.preventDefault();
        pick(shown[active]);
      }
    } else if (e.key === "Escape" && open) {
      e.preventDefault();
      e.stopPropagation();
      setOpen(false);
    }
  };

  let hint: ReactNode;
  if (current) {
    const p = price(current);
    const ctx = contextLabel(current.context_length);
    hint = (
      <>
        <span className="font-medium text-muted">{current.name || current.id}</span>
        {ctx && ` · ${t("{size} context", { size: ctx })}`}
        {` · ${p.text}`}
        {p.perMillion && ` ${t("per 1M tokens")}`}
      </>
    );
  } else if (models.error) hint = t("The model list is unavailable right now. You can still type any OpenRouter model id.");
  else if (typed && models.data && !known) hint = <span className="text-warning">{t("This id is not in the OpenRouter catalog — check it before saving.")}</span>;
  else hint = t("Type any OpenRouter model id or pick one from the list.");

  return (
    <div>
      <div className="relative">
        <Input
          id={id}
          role="combobox"
          aria-expanded={open}
          aria-controls={`${id}-list`}
          aria-autocomplete="list"
          aria-activedescendant={open && shown[active] ? `${id}-${active}` : undefined}
          autoComplete="off"
          spellCheck={false}
          value={value}
          placeholder="anthropic/claude-sonnet-4.5"
          className="pr-9 font-mono text-xs"
          onChange={(e) => {
            onChange(e.target.value);
            setQuery(e.target.value);
            setOpen(true);
            setActive(0);
          }}
          onFocus={show}
          onClick={show}
          onBlur={() => setOpen(false)}
          onKeyDown={onKeyDown}
        />
        <button
          type="button"
          tabIndex={-1}
          aria-label={t("Show models")}
          onMouseDown={(e) => {
            e.preventDefault();
            if (open) setOpen(false);
            else {
              document.getElementById(id)?.focus();
              show();
            }
          }}
          className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-fg"
        >
          <ChevronDown className={cn("size-3.5 transition-transform", open && "rotate-180")} />
        </button>
        {open && (
          // mousedown is cancelled so the input keeps focus while the list is used.
          <div className="animate-pop absolute inset-x-0 top-full z-30 mt-1.5 overflow-hidden rounded-lg border border-border bg-surface shadow-pop" onMouseDown={(e) => e.preventDefault()}>
            <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 border-b border-border px-3 py-2">
              <span className="text-2xs text-subtle">{models.data ? `${t("{n} model|{n} models", { n: matches.length })} · ${t("prices per 1M tokens")}` : ""}</span>
              {/* A plain button, not the <label>-based Switch: a label click would move focus and close the list. */}
              <button
                type="button"
                role="switch"
                aria-checked={freeOnly}
                onClick={() => {
                  setFreeOnly(!freeOnly);
                  setActive(0);
                }}
                className="flex items-center gap-2 text-xs font-medium text-muted transition-colors hover:text-fg"
              >
                <span className={cn("relative h-4 w-7 shrink-0 rounded-full border transition-colors", freeOnly ? "border-transparent bg-inverse" : "border-border-strong bg-surface-3")}>
                  <span className={cn("absolute left-px top-px size-3 rounded-full shadow-sm transition-transform", freeOnly ? "translate-x-3 bg-inverse-fg" : "bg-white")} />
                </span>
                {t("Free models only")}
              </button>
            </div>
            <div ref={listRef} id={`${id}-list`} role="listbox" className="max-h-72 overflow-y-auto p-1">
              {models.isLoading ? (
                <div className="flex items-center gap-2 px-2.5 py-3 text-[13px] text-subtle">
                  <Spinner className="size-3.5" />
                  {t("Loading models…")}
                </div>
              ) : models.error ? (
                <div className="space-y-2 px-2.5 py-3 text-[13px]">
                  <div className="text-danger">{t("The model list could not be loaded: {error}", { error: models.error.message })}</div>
                  <Button size="xs" icon={<RotateCcw />} onClick={() => models.refetch()}>
                    {t("Try again")}
                  </Button>
                </div>
              ) : shown.length === 0 ? (
                <div className="px-2.5 py-3 text-[13px] text-subtle">{query.trim() ? t("No models match “{q}”. You can still use the id you typed.", { q: query.trim() }) : t("No models found.")}</div>
              ) : (
                shown.map((m, idx) => {
                  const p = price(m);
                  const ctx = contextLabel(m.context_length);
                  const selected = m.id === typed;
                  return (
                    <div
                      key={m.id}
                      id={`${id}-${idx}`}
                      role="option"
                      aria-selected={selected}
                      data-idx={idx}
                      onMouseMove={() => idx !== active && setActive(idx)}
                      onClick={() => pick(m)}
                      title={m.description ? clip(m.description, 300) : undefined}
                      className={cn("flex cursor-pointer items-center gap-3 rounded-md px-2.5 py-1.5", idx === active && "bg-surface-2")}
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5">
                          <span className="truncate text-[13px] font-medium text-fg">{m.name || m.id}</span>
                          {selected && <Check className="size-3.5 shrink-0 text-accent" />}
                        </div>
                        <div className="truncate font-mono text-2xs text-subtle">{m.id}</div>
                      </div>
                      <div className="shrink-0 text-right text-2xs leading-4 text-subtle">
                        {ctx && <div>{t("{size} context", { size: ctx })}</div>}
                        {p.free ? (
                          <Badge tone="success" className="mt-0.5">
                            {p.text}
                          </Badge>
                        ) : (
                          <div className="tabular">{p.text}</div>
                        )}
                      </div>
                    </div>
                  );
                })
              )}
            </div>
            {matches.length > LIST_LIMIT && (
              <div className="border-t border-border px-3 py-1.5 text-2xs text-subtle">{t("Showing the first {shown} of {n} — type to narrow the list.", { shown: LIST_LIMIT, n: matches.length })}</div>
            )}
          </div>
        )}
      </div>
      <div className="mt-1.5 text-xs leading-relaxed text-subtle">{hint}</div>
    </div>
  );
}

function UserPicker({ list, loading, error, selected, onChange }: { list?: User[]; loading: boolean; error: unknown; selected: number[]; onChange: (ids: number[]) => void }) {
  const [q, setQ] = useState("");
  const chosen = new Set(selected);
  const needle = q.trim().toLowerCase();
  const shown = (list ?? []).filter((u) => !needle || `${u.name} ${u.email}`.toLowerCase().includes(needle));
  const count = list ? list.filter((u) => chosen.has(u.id)).length : selected.length;
  const allShown = shown.length > 0 && shown.every((u) => chosen.has(u.id));
  const setChecked = (id: number, on: boolean) => onChange(on ? (chosen.has(id) ? selected : [...selected, id]) : selected.filter((x) => x !== id));
  const toggleShown = () => {
    const ids = new Set(shown.map((u) => u.id));
    onChange(allShown ? selected.filter((id) => !ids.has(id)) : [...selected, ...shown.filter((u) => !chosen.has(u.id)).map((u) => u.id)]);
  };
  return (
    <div className="space-y-2">
      <div className="overflow-hidden rounded-lg border border-border">
        <div className="flex flex-wrap items-center gap-2 border-b border-border bg-surface-2/40 px-3 py-2">
          <div className="relative min-w-40 flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("Search users…")} aria-label={t("Search users…")} className="pl-8" />
          </div>
          <span className="text-xs text-subtle">{t("{n} user selected|{n} users selected", { n: count })}</span>
          <Button size="xs" variant="ghost" disabled={shown.length === 0} onClick={toggleShown}>
            {allShown ? t("Clear selection") : t("Select all")}
          </Button>
        </div>
        <div className="max-h-72 divide-y divide-border overflow-y-auto">
          {loading ? (
            <div className="space-y-2 p-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-9 w-full" />
              ))}
            </div>
          ) : error ? (
            <div className="p-3">
              <ErrorBox error={error} />
            </div>
          ) : shown.length === 0 ? (
            <div className="px-3 py-6 text-center text-[13px] text-subtle">{needle ? t("No users match “{q}”.", { q: q.trim() }) : t("No users yet.")}</div>
          ) : (
            shown.map((u) => (
              <label key={u.id} className="flex cursor-pointer items-center gap-3 px-3 py-2 transition-colors hover:bg-surface-2/50">
                <input type="checkbox" className="size-4 shrink-0 accent-[var(--color-fg)]" checked={chosen.has(u.id)} onChange={(e) => setChecked(u.id, e.target.checked)} />
                <Avatar name={u.name || u.email} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-medium">{u.name || u.email}</span>
                  <span className="block truncate text-xs text-muted">{u.email}</span>
                </span>
                {u.role !== "member" && <Badge tone="accent">{u.role === "owner" ? t("Owner") : t("Admin")}</Badge>}
                {u.disabled && <Badge tone="danger">{t("Disabled")}</Badge>}
              </label>
            ))
          )}
        </div>
      </div>
      {count === 0 && !loading && <p className="text-xs text-warning">{t("Nobody can use the assistant until you select at least one user.")}</p>}
    </div>
  );
}
