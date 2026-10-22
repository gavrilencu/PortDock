// Floating AI assistant (OpenRouter through the PortDock API) for the signed-in
// users an administrator allowed. Replies stream over Server-Sent Events from
// POST /api/chat/send; the state lives here, so minimizing the panel keeps the
// conversation and a reply that is still streaming.
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowLeft, ArrowUp, Ban, Check, CircleAlert, History, Maximize2, MessageSquare, MessagesSquare, Minimize2, Minus, Plus, RotateCcw, ShieldCheck, Sparkles, Square, SquarePen, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "../../lib/api";
import { intlLocale, t, tk, tMaybe, tx } from "../../lib/i18n";
import type { ChatConversation, ChatMessage, ChatStatus, ChatStep, ChatStepStatus } from "../../lib/types";
import { cn, timeAgo } from "../../lib/utils";
import { Button, Empty, ErrorBox, Skeleton, Spinner, useConfirm } from "../ui";
import { CopyIconButton, Markdown } from "./Markdown";

const STORE = "portdock.assistant.";
const MAX_CHARS = 20_000;
/** About six lines of the composer (20px line height + padding). */
const MAX_INPUT_HEIGHT = 132;

const SUGGESTIONS = [tk("How do I add a custom domain with HTTPS?"), tk("Why could my deployment fail?"), tk("How do I roll back to a previous version?")];
/** When the assistant can read the platform. */
const SUGGESTIONS_TOOLS = [tk("Which containers have problems right now?"), tk("Show me the errors in the logs of my containers"), tk("Why did the last deployment fail?"), tk("How do I add a custom domain with HTTPS?")];

/** How each tool of the assistant is shown, with the arguments it names. */
const TOOL_TITLES: Record<string, [string, string[]]> = {
  search_docs: [tk("Searching the documentation for “{query}”"), ["query"]],
  instance_info: [tk("Reading the instance overview"), []],
  list_projects: [tk("Listing the projects"), []],
  list_applications: [tk("Listing the applications"), []],
  get_application: [tk("Reading application {app}"), ["app"]],
  list_deployments: [tk("Listing the deployments of {app}"), ["app"]],
  get_deployment: [tk("Reading deployment #{deployment_id}"), ["deployment_id"]],
  application_logs: [tk("Reading the logs of application {app}"), ["app"]],
  list_versions: [tk("Listing the versions of {app}"), ["app"]],
  list_domains: [tk("Listing the domains of {app}"), ["app"]],
  list_jobs: [tk("Listing the jobs of {app}"), ["app"]],
  list_databases: [tk("Listing the databases"), []],
  get_database: [tk("Reading database {database}"), ["database"]],
  list_services: [tk("Listing the services"), []],
  list_servers: [tk("Listing the servers"), []],
  server_status: [tk("Reading the status of the servers"), []],
  list_containers: [tk("Listing the containers"), []],
  container_logs: [tk("Reading the logs of container {container}"), ["container"]],
  container_inspect: [tk("Inspecting container {container}"), ["container"]],
  container_stats: [tk("Reading the resource usage of the containers"), []],
  resource_metrics: [tk("Reading the metrics of {kind} #{id}"), ["kind", "id"]],
  list_alerts: [tk("Listing the alerts"), []],
  list_monitors: [tk("Listing the uptime monitors"), []],
  recent_activity: [tk("Reading the recent activity"), []],
  deploy_application: [tk("Deploy application {app}"), ["app"]],
  rollback_deployment: [tk("Roll back to deployment #{deployment_id}"), ["deployment_id"]],
  restart_application: [tk("Restart application {app}"), ["app"]],
  start_application: [tk("Start application {app}"), ["app"]],
  restart_container: [tk("Restart container {container}"), ["container"]],
  start_container: [tk("Start container {container}"), ["container"]],
  restart_database: [tk("Restart database {database}"), ["database"]],
  restart_service: [tk("Restart service {service}"), ["service"]],
  backup_database: [tk("Back up database #{database_id}"), ["database_id"]],
  run_job: [tk("Run job {job} of {app}"), ["job", "app"]],
};

function stepTitle(s: ChatStep): string {
  const def = TOOL_TITLES[s.tool];
  if (!def) return s.title || s.tool;
  const vars: Record<string, string> = {};
  for (const k of def[1]) vars[k] = String(s.args?.[k] ?? "");
  return t(def[0], vars);
}

const STEP_STATUS: Record<ChatStepStatus, string> = {
  running: tk("Working…"),
  done: tk("Done"),
  error: tk("Failed"),
  pending: tk("Waiting for your confirmation"),
  confirmed: tk("Done"),
  failed: tk("Failed"),
  cancelled: tk("Cancelled"),
  expired: tk("Expired — ask the assistant again"),
};

/** A message as shown here: saved messages plus local-only states of this session. */
interface Msg extends ChatMessage {
  /** Why the reply failed, when known in this session. */
  errorText?: string;
  /** The user stopped the reply. */
  stopped?: boolean;
  /** The user message could not be sent (it isn't saved). */
  failed?: boolean;
}

type ConversationDetail = { conversation: ChatConversation; messages: ChatMessage[] };

function readStore(key: string): string | null {
  try {
    return localStorage.getItem(STORE + key);
  } catch {
    return null;
  }
}

function writeStore(key: string, value: string) {
  try {
    localStorage.setItem(STORE + key, value);
  } catch {
    /* storage unavailable */
  }
}

export function useChatStatus() {
  return useQuery({ queryKey: ["chat-status"], queryFn: () => api.get<ChatStatus>("/chat/status"), staleTime: 60_000, retry: false });
}

/** Mounted once for signed-in users; renders nothing unless the assistant is available to them. */
export function ChatWidget() {
  const { data } = useChatStatus();
  if (!data?.allowed) return null;
  return <Assistant model={data.model ?? ""} tools={!!data.tools} actions={!!data.actions} />;
}

// ---------- Streaming ----------

/** Error of a response that was refused before the stream started (JSON {error}). */
async function responseError(res: Response): Promise<Error> {
  if (res.status === 401) {
    if (!location.pathname.startsWith("/login")) location.href = `/login?next=${encodeURIComponent(location.pathname)}`;
    return new ApiError(401, t("authentication required"));
  }
  let message = res.statusText || `HTTP ${res.status}`;
  try {
    const data = JSON.parse(await res.text());
    if (data && typeof data.error === "string" && data.error) message = data.error;
  } catch {
    /* not JSON */
  }
  return new ApiError(res.status, tMaybe(message));
}

/**
 * Reads a text/event-stream body and calls `on` for every event with JSON data.
 * `on` returns true once the reply is complete: reading stops then, without
 * waiting for the server (or a proxy) to close the connection.
 */
async function readEvents(body: ReadableStream<Uint8Array>, on: (event: string, data: unknown) => boolean | void) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let stop = false;
  const dispatch = (block: string) => {
    let event = "message";
    const data: string[] = [];
    for (const line of block.split("\n")) {
      if (!line || line.startsWith(":")) continue; // comments and pings
      const colon = line.indexOf(":");
      const field = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") event = value;
      else if (field === "data") data.push(value);
    }
    if (!data.length) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(data.join("\n"));
    } catch {
      return;
    }
    if (on(event, parsed) === true) stop = true;
  };
  const drain = (final: boolean) => {
    // A "\r" at the end may be the first half of "\r\n": keep it for the next chunk.
    const hold = !final && buf.endsWith("\r");
    let text = (hold ? buf.slice(0, -1) : buf).replace(/\r\n?/g, "\n");
    for (let i = text.indexOf("\n\n"); i >= 0 && !stop; i = text.indexOf("\n\n")) {
      dispatch(text.slice(0, i));
      text = text.slice(i + 2);
    }
    if (final) {
      if (text.trim() && !stop) dispatch(text);
      buf = "";
    } else buf = text + (hold ? "\r" : "");
  };
  try {
    while (!stop) {
      const { value, done } = await reader.read();
      if (done) {
        buf += decoder.decode();
        drain(true);
        break;
      }
      buf += decoder.decode(value, { stream: true });
      drain(false);
    }
    if (stop) await reader.cancel().catch(() => {});
  } finally {
    reader.releaseLock();
  }
}

// ---------- Widget ----------

function Assistant({ model, tools, actions }: { model: string; tools: boolean; actions: boolean }) {
  const qc = useQueryClient();
  const [open, setOpenState] = useState(() => readStore("open") === "1");
  const [expanded, setExpandedState] = useState(() => readStore("expanded") === "1");
  const [activeId, setActiveIdState] = useState(() => {
    const n = Number(readStore("conversation"));
    return Number.isSafeInteger(n) && n > 0 ? n : 0;
  });
  const [view, setView] = useState<"chat" | "history">("chat");
  const [messages, setMessages] = useState<Msg[]>([]);
  /** Conversation whose messages are loaded (0 = a new conversation). */
  const [loadedId, setLoadedId] = useState<number | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  /** Text of the reply being streamed; null while idle. */
  const [stream, setStream] = useState<string | null>(null);
  /** Tools used and actions proposed by the reply being streamed. */
  const [liveSteps, setLiveSteps] = useState<ChatStep[]>([]);
  const [liveNotice, setLiveNotice] = useState<string | null>(null);
  const liveStepsRef = useRef<ChatStep[]>([]);
  const liveNoticeRef = useRef<string | null>(null);
  /** Action being confirmed or cancelled right now. */
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [unread, setUnread] = useState(false);
  const [atBottom, setAtBottom] = useState(true);

  const abortRef = useRef<AbortController | null>(null);
  /** Changes whenever the shown conversation changes, so late stream events are dropped. */
  const sessionRef = useRef(0);
  const streamRef = useRef("");
  const frameRef = useRef(0);
  const tempIdRef = useRef(0);
  const activeRef = useRef(activeId);
  const openRef = useRef(open);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const focusRef = useRef<"input" | "button" | null>(null);

  const streaming = stream !== null;
  const loading = activeId > 0 && loadedId !== activeId;

  const setOpen = (v: boolean) => {
    openRef.current = v;
    setOpenState(v);
    writeStore("open", v ? "1" : "0");
    if (v) {
      setUnread(false);
      stickRef.current = true;
    }
  };
  const setExpanded = (v: boolean) => {
    setExpandedState(v);
    writeStore("expanded", v ? "1" : "0");
  };
  const setActive = (id: number) => {
    activeRef.current = id;
    setActiveIdState(id);
    writeStore("conversation", String(id));
  };

  // Stop a running reply when the widget goes away (sign-out, access removed).
  useEffect(
    () => () => {
      abortRef.current?.abort();
      cancelAnimationFrame(frameRef.current);
    },
    [],
  );

  // Load the active conversation when the panel is open.
  useEffect(() => {
    if (!open || activeId <= 0 || loadedId === activeId) return;
    let cancelled = false;
    setLoadError(null);
    api
      .get<ConversationDetail>(`/chat/conversations/${activeId}`)
      .then((d) => {
        if (cancelled) return;
        stickRef.current = true;
        setMessages(d.messages ?? []);
        setLoadedId(activeId);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 404) {
          // Deleted meanwhile: start over.
          setMessages([]);
          setLoadedId(0);
          setActive(0);
        } else setLoadError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, activeId, loadedId, reloadKey]);

  // Grow the composer with its text, up to about six lines (before the scroll below, which depends on it).
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_INPUT_HEIGHT)}px`;
    el.style.overflowY = el.scrollHeight > MAX_INPUT_HEIGHT ? "auto" : "hidden";
  }, [draft, open, view, expanded]);

  // Keep the newest message in view unless the user scrolled up…
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  });
  // …also when the message area changes size (composer growing, panel resized).
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      if (stickRef.current) el.scrollTop = el.scrollHeight;
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [open, view]);

  useEffect(() => {
    if (focusRef.current === "input" && inputRef.current) {
      inputRef.current.focus();
      focusRef.current = null;
    } else if (focusRef.current === "button" && buttonRef.current) {
      buttonRef.current.focus();
      focusRef.current = null;
    }
  });

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    stickRef.current = near;
    setAtBottom(near);
  };
  const jumpToLatest = () => {
    const el = scrollRef.current;
    if (!el) return;
    stickRef.current = true;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  };

  const upsertConversation = (c: ChatConversation) => qc.setQueryData<ChatConversation[]>(["chat-conversations"], (old) => (old ? [c, ...old.filter((x) => x.id !== c.id)] : old));

  /** Writes the outcome of a proposed action into the message that holds it. */
  const applyStep = (updated: ChatStep) =>
    setMessages((ms) =>
      ms.map((m) => {
        const steps = m.meta?.steps;
        if (!steps?.some((s) => s.action_id === updated.action_id)) return m;
        return { ...m, meta: { ...m.meta, steps: steps.map((s) => (s.action_id === updated.action_id ? { ...s, ...updated } : s)) } };
      }),
    );

  /** Confirms or cancels an action the assistant proposed; it runs on the server with the user's own rights. */
  const decide = useCallback(async (step: ChatStep, verb: "confirm" | "cancel") => {
    if (!step.action_id) return;
    setBusyAction(step.action_id);
    try {
      const res = await api.post<{ status: ChatStepStatus; detail?: string; step?: ChatStep }>(`/chat/actions/${step.action_id}/${verb}`);
      applyStep({ ...step, ...res.step, status: res.status, detail: res.detail ?? res.step?.detail });
      if (verb === "confirm") {
        if (res.status === "confirmed") toast.success(t("Done: {action}", { action: stepTitle(step) }));
        else toast.error(t("Failed: {action}", { action: stepTitle(step) }), { description: res.detail });
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 410) applyStep({ ...step, status: "expired", detail: e.message });
      else toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyAction(null);
    }
  }, []);

  const send = async (raw: string) => {
    const content = raw.trim();
    if (!content || content.length > MAX_CHARS || abortRef.current) return;
    const session = sessionRef.current;
    const alive = () => sessionRef.current === session;
    const conversationId = activeRef.current;
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const userTempId = --tempIdRef.current;
    const now = () => new Date().toISOString();
    const localReply = (extra: Partial<Msg>): Msg => ({
      id: --tempIdRef.current,
      conversation_id: activeRef.current,
      role: "assistant",
      content: streamRef.current,
      model,
      created_at: now(),
      ...(liveStepsRef.current.length || liveNoticeRef.current ? { meta: { steps: liveStepsRef.current, notice: liveNoticeRef.current ?? undefined } } : {}),
      ...extra,
    });

    stickRef.current = true;
    streamRef.current = "";
    liveStepsRef.current = [];
    liveNoticeRef.current = null;
    setLiveSteps([]);
    setLiveNotice(null);
    setStream("");
    setMessages((ms) => [...ms, { id: userTempId, conversation_id: conversationId, role: "user", content, model: "", created_at: now() }]);

    let userSaved = false;
    let settled = false;
    const settle = (m: Msg | null) => {
      settled = true;
      cancelAnimationFrame(frameRef.current);
      frameRef.current = 0;
      if (!alive()) return;
      setStream(null);
      setLiveSteps([]);
      setLiveNotice(null);
      if (m) setMessages((ms) => [...ms, m]);
      if (!openRef.current) setUnread(true);
    };

    try {
      const res = await fetch("/api/chat/send", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        // The page path lets "this application" mean something to the assistant.
        body: JSON.stringify({ conversation_id: conversationId, content, context: { path: location.pathname } }),
        signal: ctrl.signal,
      });
      if (!res.ok) throw await responseError(res);
      if (!res.body) throw new Error(t("The connection was interrupted."));
      await readEvents(res.body, (event, data) => {
        if (settled) return true;
        if (event === "conversation") {
          const c = data as ChatConversation;
          upsertConversation(c);
          if (alive() && activeRef.current !== c.id) {
            setLoadedId(c.id);
            setActive(c.id);
          }
        } else if (event === "user") {
          userSaved = true;
          const saved = data as ChatMessage;
          if (alive()) setMessages((ms) => ms.map((m) => (m.id === userTempId ? saved : m)));
        } else if (event === "delta") {
          const d = data as { text?: string };
          if (!d.text) return;
          streamRef.current += d.text;
          // Render at most once per frame.
          if (!frameRef.current)
            frameRef.current = requestAnimationFrame(() => {
              frameRef.current = 0;
              if (alive() && !settled) setStream(streamRef.current);
            });
        } else if (event === "tool" || event === "action") {
          const step = data as ChatStep;
          const cur = liveStepsRef.current;
          const i = cur.findIndex((s) => s.id === step.id);
          liveStepsRef.current = i < 0 ? [...cur, step] : cur.map((s, j) => (j === i ? step : s));
          if (alive()) setLiveSteps(liveStepsRef.current);
        } else if (event === "notice") {
          const n = data as { text?: string };
          if (n.text) {
            liveNoticeRef.current = n.text;
            if (alive()) setLiveNotice(n.text);
          }
        } else if (event === "done") {
          settle(data as ChatMessage);
          return true;
        } else if (event === "error") {
          const e = data as { error?: string; message?: ChatMessage };
          const errorText = e.error ? tMaybe(e.error) : t("The reply could not be completed.");
          settle(e.message ? { ...e.message, error: true, errorText } : localReply({ error: true, errorText }));
          return true;
        }
      });
      if (!settled) settle(localReply({ error: true, errorText: t("The connection was interrupted.") }));
    } catch (err) {
      const reason = err instanceof TypeError ? t("Could not reach PortDock. Check your connection.") : err instanceof Error ? err.message : String(err);
      if (settled) {
        /* already handled */
      } else if (ctrl.signal.aborted) {
        settle(localReply({ stopped: true })); // the server keeps what was generated
      } else if (!userSaved && !streamRef.current) {
        // Refused before the stream started: the message isn't saved.
        settled = true;
        cancelAnimationFrame(frameRef.current);
        frameRef.current = 0;
        if (alive()) {
          setStream(null);
          setMessages((ms) => ms.map((m) => (m.id === userTempId ? { ...m, failed: true, errorText: reason } : m)));
        }
        if (err instanceof ApiError && err.status === 403) qc.invalidateQueries({ queryKey: ["chat-status"] });
      } else {
        settle(localReply({ error: true, errorText: reason }));
      }
    } finally {
      if (abortRef.current === ctrl) abortRef.current = null;
      qc.invalidateQueries({ queryKey: ["chat-conversations"] });
    }
  };

  const stop = () => abortRef.current?.abort();

  // Until the shown conversation is loaded, a new message would be lost when it arrives.
  const canSend = !streaming && !loading && !loadError;
  const submit = () => {
    if (!canSend || !draft.trim()) return;
    const text = draft;
    setDraft("");
    send(text);
  };

  const onComposerKey = (e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  /** Leaves the shown conversation (drops late events of a running reply). */
  const leave = () => {
    sessionRef.current++;
    abortRef.current?.abort();
    abortRef.current = null;
    cancelAnimationFrame(frameRef.current);
    frameRef.current = 0;
    setStream(null);
    setLoadError(null);
  };

  const newChat = () => {
    setView("chat");
    focusRef.current = "input";
    if (streaming || (activeRef.current === 0 && messages.length === 0)) return;
    leave();
    setMessages([]);
    setLoadedId(0);
    setActive(0);
  };

  const openConversation = (id: number) => {
    setView("chat");
    focusRef.current = "input";
    if (id === activeRef.current || streaming) return;
    leave();
    setMessages([]);
    setLoadedId(null);
    setActive(id);
  };

  const onDeleted = (id: number) => {
    if (id !== activeRef.current) return;
    leave();
    setMessages([]);
    setLoadedId(0);
    setActive(0);
  };

  const retry = (m: Msg) => {
    if (abortRef.current) return;
    if (m.failed) {
      setMessages((ms) => ms.filter((x) => x.id !== m.id));
      send(m.content);
      return;
    }
    const lastUser = [...messages].reverse().find((x) => x.role === "user");
    if (lastUser) send(lastUser.content);
  };
  const retryRef = useRef(retry);
  useLayoutEffect(() => {
    retryRef.current = retry;
  });
  const onRetry = useCallback((m: Msg) => retryRef.current(m), []);

  const minimize = () => {
    focusRef.current = "button";
    setOpen(false);
  };

  if (!open)
    return (
      <button
        ref={buttonRef}
        type="button"
        onClick={() => {
          focusRef.current = "input";
          setOpen(true);
        }}
        title={t("AI assistant")}
        aria-label={t("AI assistant")}
        className="fixed bottom-5 right-5 z-40 flex size-12 items-center justify-center rounded-full bg-inverse text-inverse-fg shadow-pop transition-[scale,opacity] hover:scale-105 hover:opacity-95 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-[var(--ring)] active:scale-95"
      >
        <Sparkles className="size-5" />
        {(streaming || unread) && <span className={cn("absolute right-0 top-0 size-3.5 rounded-full border-2 border-canvas", streaming ? "pulse-dot bg-info text-info" : "bg-accent")} />}
      </button>
    );

  const last = messages[messages.length - 1];
  const nearLimit = draft.length >= MAX_CHARS * 0.9;
  const fmt = (n: number) => n.toLocaleString(intlLocale());

  return (
    <div
      role="dialog"
      aria-label={t("PortDock Assistant")}
      onKeyDown={(e) => {
        if (e.key === "Escape" && !e.defaultPrevented) {
          e.preventDefault();
          minimize();
        }
      }}
      className={cn(
        "animate-pop fixed bottom-5 right-5 z-40 flex max-h-[calc(100vh-6rem)] max-w-[calc(100vw-2.5rem)] origin-bottom-right flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-pop transition-[width,height] duration-200",
        expanded ? "h-[80vh] w-[760px]" : "h-[620px] w-[400px]",
      )}
    >
      <div className="flex h-12 shrink-0 items-center gap-2.5 border-b border-border pl-3.5 pr-2">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-inverse text-inverse-fg">
          <Sparkles className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1 leading-tight">
          <div className="truncate text-[13px] font-semibold tracking-[-0.01em]">{t("PortDock Assistant")}</div>
          {model && (
            <div className="truncate font-mono text-2xs text-subtle" title={model}>
              {model}
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <IconButton title={t("Conversations")} pressed={view === "history"} onClick={() => setView(view === "history" ? "chat" : "history")}>
            <History />
          </IconButton>
          <IconButton title={streaming ? t("Wait for the reply or stop it first") : t("New chat")} disabled={streaming} onClick={newChat}>
            <SquarePen />
          </IconButton>
          <IconButton className="hidden sm:flex" title={expanded ? t("Shrink") : t("Expand")} pressed={expanded} onClick={() => setExpanded(!expanded)}>
            {expanded ? <Minimize2 /> : <Maximize2 />}
          </IconButton>
          <IconButton title={t("Minimize")} onClick={minimize}>
            <Minus />
          </IconButton>
        </div>
      </div>

      {view === "history" ? (
        <Conversations activeId={activeId} busy={streaming} onOpen={openConversation} onNew={newChat} onBack={() => setView("chat")} onDeleted={onDeleted} />
      ) : (
        <>
          <div className="relative min-h-0 flex-1">
            <div ref={scrollRef} onScroll={onScroll} className="h-full overflow-y-auto px-4 py-4">
              {loadError ? (
                <div className="space-y-3">
                  <ErrorBox error={loadError} title={t("The conversation could not be loaded")} />
                  <Button size="sm" icon={<RotateCcw />} onClick={() => setReloadKey((k) => k + 1)}>
                    {t("Try again")}
                  </Button>
                </div>
              ) : loading ? (
                <MessagesSkeleton />
              ) : messages.length === 0 && !streaming ? (
                <Welcome
                  tools={tools}
                  onPick={(q) => {
                    focusRef.current = "input";
                    send(q);
                  }}
                />
              ) : (
                <div className="space-y-4">
                  {messages.map((m) => (
                    <MessageItem key={m.id} m={m} onRetry={!streaming && (m.failed || (m === last && m.error)) ? onRetry : undefined} onDecide={decide} busyAction={busyAction} canDecide={!streaming} />
                  ))}
                  {stream !== null && (
                    <div className="min-w-0">
                      <Steps steps={liveSteps} notice={liveNotice} canDecide={false} busyAction={null} />
                      {stream.trim() ? <Markdown text={stream} /> : <TypingDots />}
                    </div>
                  )}
                </div>
              )}
            </div>
            {!atBottom && !loading && (messages.length > 0 || streaming) && (
              <button
                type="button"
                onClick={jumpToLatest}
                title={t("Scroll to the latest message")}
                aria-label={t("Scroll to the latest message")}
                className="animate-pop absolute bottom-3 left-1/2 flex size-8 -translate-x-1/2 items-center justify-center rounded-full border border-border bg-surface text-muted shadow-pop transition-colors hover:text-fg"
              >
                <ArrowDown className="size-4" />
              </button>
            )}
          </div>

          <div className="shrink-0 border-t border-border px-3 pb-2 pt-3">
            {nearLimit && (
              <div className={cn("tabular mb-1.5 px-1 text-right text-2xs", draft.length >= MAX_CHARS ? "text-danger" : "text-warning")}>
                {fmt(draft.length)} / {fmt(MAX_CHARS)}
              </div>
            )}
            <div className="flex items-end gap-2 rounded-xl border border-border bg-surface p-1.5 pl-3 shadow-xs transition-[border,box-shadow] focus-within:border-accent focus-within:ring-[3px] focus-within:ring-[var(--ring)]">
              <textarea
                ref={inputRef}
                rows={1}
                value={draft}
                maxLength={MAX_CHARS}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={onComposerKey}
                placeholder={t("Ask a question…")}
                aria-label={t("Message to the assistant")}
                className="block min-h-8 flex-1 resize-none bg-transparent py-1.5 text-[13px] leading-5 text-fg placeholder:text-faint focus:outline-none"
              />
              {streaming ? (
                <button
                  type="button"
                  onClick={stop}
                  title={t("Stop")}
                  aria-label={t("Stop")}
                  className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2 text-fg transition-colors hover:border-border-strong hover:bg-surface-3"
                >
                  <Square className="size-3 fill-current" />
                </button>
              ) : (
                <button
                  type="button"
                  onClick={submit}
                  disabled={!canSend || !draft.trim()}
                  title={t("Send")}
                  aria-label={t("Send")}
                  className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-inverse text-inverse-fg transition-opacity hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-[var(--ring)] disabled:cursor-not-allowed disabled:opacity-30"
                >
                  <ArrowUp className="size-4" />
                </button>
              )}
            </div>
            <p className="mt-2 px-1 text-center text-2xs leading-4 text-faint">
              {model ? tx("Replies are generated by {model} via OpenRouter and can be wrong.", { model: <span className="font-mono">{model}</span> }) : t("Replies are generated via OpenRouter and can be wrong.")}
              {tools && (
                <>
                  {" "}
                  {actions
                    ? t("It reads your platform with your permissions; actions run only after you confirm them, and it can never delete anything.")
                    : t("It reads your platform with your permissions and cannot change anything.")}
                </>
              )}
            </p>
          </div>
        </>
      )}
    </div>
  );
}

function IconButton({ title, onClick, pressed, disabled, className, children }: { title: string; onClick: () => void; pressed?: boolean; disabled?: boolean; className?: string; children: ReactNode }) {
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex size-7 items-center justify-center rounded-md text-subtle transition-colors hover:bg-surface-2 hover:text-fg focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-[var(--ring)] disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-subtle [&_svg]:size-4",
        pressed && "bg-surface-2 text-fg",
        className,
      )}
    >
      {children}
    </button>
  );
}

function Welcome({ tools, onPick }: { tools: boolean; onPick: (question: string) => void }) {
  return (
    <div className="flex min-h-full flex-col justify-center py-2">
      <div className="mb-5 text-center">
        <span className="mx-auto mb-3 flex size-11 items-center justify-center rounded-xl border border-border bg-surface-2 text-accent shadow-xs">
          <Sparkles className="size-5" />
        </span>
        <div className="text-sm font-semibold">{t("How can I help?")}</div>
        <p className="mx-auto mt-1 max-w-72 text-[13px] leading-relaxed text-subtle">
          {tools ? t("Ask about your applications, containers, deployments or domains — the assistant can look at them for you.") : t("Ask anything about your applications, deployments, domains or PortDock itself.")}
        </p>
      </div>
      <div className="space-y-2">
        {(tools ? SUGGESTIONS_TOOLS : SUGGESTIONS).map((s) => (
          <button
            key={s}
            type="button"
            onClick={() => onPick(t(s))}
            className="flex w-full items-center gap-2.5 rounded-lg border border-border bg-surface px-3 py-2.5 text-left text-[13px] text-muted shadow-xs transition-colors hover:border-border-strong hover:bg-surface-2 hover:text-fg"
          >
            <MessageSquare className="size-3.5 shrink-0 text-faint" />
            {t(s)}
          </button>
        ))}
      </div>
    </div>
  );
}

function TypingDots() {
  return (
    <div className="flex h-6 items-center gap-1" role="status" aria-label={t("The assistant is typing…")}>
      {[0, 1, 2].map((i) => (
        <span key={i} className="typing-dot size-1.5 rounded-full bg-subtle" style={{ animationDelay: `${i * 160}ms` }} />
      ))}
    </div>
  );
}

function MessagesSkeleton() {
  return (
    <div className="space-y-5" aria-busy>
      <Skeleton className="ml-auto h-9 w-2/3 rounded-2xl" />
      <div className="space-y-2">
        <Skeleton className="h-3.5 w-full" />
        <Skeleton className="h-3.5 w-5/6" />
        <Skeleton className="h-3.5 w-3/5" />
      </div>
      <Skeleton className="ml-auto h-9 w-1/2 rounded-2xl" />
      <div className="space-y-2">
        <Skeleton className="h-3.5 w-full" />
        <Skeleton className="h-3.5 w-2/3" />
      </div>
    </div>
  );
}

const hoverReveal = "opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100";

type Decide = (step: ChatStep, verb: "confirm" | "cancel") => void;

/** The tools a reply used and the actions it proposed, with their outcome. */
function Steps({ steps, notice, canDecide, busyAction, onDecide }: { steps: ChatStep[]; notice?: string | null; canDecide: boolean; busyAction: string | null; onDecide?: Decide }) {
  if (!steps.length && !notice) return null;
  return (
    <div className="mb-2 space-y-1">
      {notice && (
        <div className="flex items-start gap-2 rounded-lg border border-border bg-surface-2/60 px-2.5 py-1.5 text-xs leading-relaxed text-muted">
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="[overflow-wrap:anywhere]">{tMaybe(notice)}</span>
        </div>
      )}
      {steps.map((s) => (
        <StepRow key={s.action_id || s.id} s={s} canDecide={canDecide} busy={!!s.action_id && busyAction === s.action_id} onDecide={onDecide} />
      ))}
    </div>
  );
}

function StepRow({ s, canDecide, busy, onDecide }: { s: ChatStep; canDecide: boolean; busy: boolean; onDecide?: Decide }) {
  const pending = s.status === "pending";
  const bad = s.status === "error" || s.status === "failed";
  const good = s.status === "done" || s.status === "confirmed";
  const tone = bad ? "text-danger" : good ? "text-success" : pending ? "text-info" : "text-subtle";
  const icon = s.status === "running" ? <Spinner className="size-3.5" /> : good ? <Check className="size-3.5" /> : bad ? <CircleAlert className="size-3.5" /> : pending ? <ShieldCheck className="size-3.5" /> : <Ban className="size-3.5" />;
  const detail = s.detail && s.status !== "done" ? (s.detail.length > 300 ? `${s.detail.slice(0, 300)}…` : s.detail) : "";
  return (
    <div className={cn("rounded-lg border px-2.5 py-1.5 text-xs", pending ? "border-info/30 bg-info-soft" : "border-border bg-surface-2/60")}>
      <div className="flex items-start gap-2">
        <span className={cn("mt-0.5 shrink-0", tone)}>{icon}</span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="font-medium text-fg [overflow-wrap:anywhere]">{stepTitle(s)}</span>
            <span className={cn("text-2xs", tone)}>{t(STEP_STATUS[s.status] ?? s.status)}</span>
          </div>
          {detail && <div className="mt-0.5 font-mono text-2xs leading-4 text-muted [overflow-wrap:anywhere]">{tMaybe(detail)}</div>}
          {pending && onDecide && (
            <div className="mt-1.5 flex flex-wrap items-center gap-2">
              <Button size="xs" variant="primary" icon={<Check />} loading={busy} disabled={!canDecide} onClick={() => onDecide(s, "confirm")}>
                {t("Confirm")}
              </Button>
              <Button size="xs" variant="ghost" disabled={busy || !canDecide} onClick={() => onDecide(s, "cancel")}>
                {t("Cancel")}
              </Button>
              {!canDecide && <span className="text-2xs text-subtle">{t("Available once the reply is complete")}</span>}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

const MessageItem = memo(function MessageItem({ m, onRetry, onDecide, busyAction, canDecide }: { m: Msg; onRetry?: (m: Msg) => void; onDecide?: Decide; busyAction?: string | null; canDecide?: boolean }) {
  const time = m.created_at ? new Date(m.created_at).toLocaleTimeString(intlLocale(), { hour: "2-digit", minute: "2-digit" }) : "";
  if (m.role === "user")
    return (
      <div className="group">
        <div className="flex w-full items-end justify-end gap-1">
          <div className={cn("mb-0.5 flex shrink-0 items-center", hoverReveal)}>
            <CopyIconButton value={m.content} />
          </div>
          <div
            className={cn(
              "min-w-0 max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-md bg-inverse px-3.5 py-2 text-[13px] leading-relaxed text-inverse-fg [overflow-wrap:anywhere]",
              m.failed && "opacity-60",
            )}
            title={time || undefined}
          >
            {m.content}
          </div>
        </div>
        {m.failed && (
          <div className="mt-1.5 flex flex-wrap items-center justify-end gap-x-2 gap-y-1 text-right text-xs text-danger">
            <span className="flex items-center gap-1.5 [overflow-wrap:anywhere]">
              <CircleAlert className="size-3.5 shrink-0" />
              {t("Not sent: {error}", { error: m.errorText ?? "" })}
            </span>
            {onRetry && (
              <button type="button" onClick={() => onRetry(m)} className="font-medium underline underline-offset-2 hover:opacity-80">
                {t("Try again")}
              </button>
            )}
          </div>
        )}
      </div>
    );
  return (
    <div className="group min-w-0">
      <Steps steps={m.meta?.steps ?? []} notice={m.meta?.notice} canDecide={canDecide ?? true} busyAction={busyAction ?? null} onDecide={onDecide} />
      {m.content && <Markdown text={m.content} />}
      {m.error && (
        <div className={cn("flex items-start gap-2 rounded-lg border border-danger/25 bg-danger-soft px-3 py-2 text-xs leading-relaxed text-danger", m.content && "mt-2.5")}>
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{m.errorText || t("The reply could not be completed.")}</span>
          {onRetry && (
            <button type="button" onClick={() => onRetry(m)} className="shrink-0 font-medium underline underline-offset-2 hover:opacity-80">
              {t("Try again")}
            </button>
          )}
        </div>
      )}
      {m.stopped && <div className={cn("text-xs italic text-faint", m.content && "mt-1.5")}>{t("Reply stopped")}</div>}
      {m.content && (
        <div className={cn("mt-1 flex h-6 min-w-0 items-center gap-1.5 text-2xs text-faint", hoverReveal)}>
          <CopyIconButton value={m.content} className="-ml-1" />
          {time && <span className="shrink-0">{time}</span>}
          {m.model && <span className="truncate font-mono">{m.model}</span>}
        </div>
      )}
    </div>
  );
});

function Conversations({
  activeId,
  busy,
  onOpen,
  onNew,
  onBack,
  onDeleted,
}: {
  activeId: number;
  busy: boolean;
  onOpen: (id: number) => void;
  onNew: () => void;
  onBack: () => void;
  onDeleted: (id: number) => void;
}) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const list = useQuery({ queryKey: ["chat-conversations"], queryFn: () => api.get<ChatConversation[]>("/chat/conversations") });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/chat/conversations/${id}`),
    onSuccess: (_, id) => {
      qc.setQueryData<ChatConversation[]>(["chat-conversations"], (old) => old?.filter((c) => c.id !== id));
      qc.invalidateQueries({ queryKey: ["chat-conversations"] });
      onDeleted(id);
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = async (c: ChatConversation) => {
    const ok = await confirm({
      title: t("Delete this conversation?"),
      description: t("“{title}” and all its messages will be deleted permanently.", { title: c.title || t("New conversation") }),
      danger: true,
      confirmText: t("Delete"),
    });
    if (ok) del.mutate(c.id);
  };
  const items = list.data ?? [];
  const wait = t("Wait for the reply or stop it first");
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b border-border px-2">
        <button type="button" onClick={onBack} className="flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] font-medium text-muted transition-colors hover:bg-surface-2 hover:text-fg">
          <ArrowLeft className="size-3.5" />
          {t("Back to chat")}
        </button>
        <Button size="xs" icon={<Plus />} disabled={busy} title={busy ? wait : undefined} onClick={onNew}>
          {t("New chat")}
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {list.isLoading ? (
          <div className="space-y-2 p-1">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-11 w-full rounded-lg" />
            ))}
          </div>
        ) : list.error ? (
          <div className="p-1">
            <ErrorBox error={list.error} />
          </div>
        ) : items.length === 0 ? (
          <div className="p-1">
            <Empty compact icon={<MessagesSquare />} title={t("No conversations yet")} description={t("Your conversations with the assistant are listed here.")} />
          </div>
        ) : (
          <div className="space-y-0.5">
            {items.map((c) => {
              const active = c.id === activeId;
              const locked = busy && !active;
              return (
                <div key={c.id} className={cn("group flex items-center gap-1 rounded-lg pr-1 transition-colors", active ? "bg-surface-2" : "hover:bg-surface-2/60")}>
                  <button
                    type="button"
                    onClick={() => onOpen(c.id)}
                    disabled={locked}
                    title={locked ? wait : undefined}
                    aria-current={active || undefined}
                    className="flex min-w-0 flex-1 items-center gap-2.5 rounded-lg px-2.5 py-2 text-left disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <MessageSquare className={cn("size-3.5 shrink-0", active ? "text-fg" : "text-faint")} />
                    <span className="min-w-0 flex-1">
                      <span className={cn("block truncate text-[13px]", active ? "font-semibold" : "font-medium")}>{c.title || t("New conversation")}</span>
                      <span className="block text-2xs text-subtle">{timeAgo(c.updated_at || c.created_at)}</span>
                    </span>
                  </button>
                  {!(busy && active) && (
                    <button
                      type="button"
                      onClick={() => remove(c)}
                      title={t("Delete conversation")}
                      aria-label={t("Delete conversation")}
                      className={cn("flex size-7 shrink-0 items-center justify-center rounded-md text-subtle hover:bg-danger-soft hover:text-danger focus-visible:opacity-100", hoverReveal)}
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
