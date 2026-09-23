import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

export interface LogStream {
  lines: string[];
  status: string | null;
  connected: boolean;
  error: string | null;
  clear: () => void;
}

const MAX_LINES = 20000;

/**
 * Subscribes to an SSE endpoint that emits `snapshot` (full text), `log`
 * (single line), `status` and `error` events. Pass null to disconnect.
 */
export function useLogStream(url: string | null): LogStream {
  const [lines, setLines] = useState<string[]>([]);
  const [status, setStatus] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const buffer = useRef<string[]>([]);

  useEffect(() => {
    setLines([]);
    setStatus(null);
    setError(null);
    buffer.current = [];
    if (!url) return;

    const es = new EventSource(`/api${url}`, { withCredentials: true });
    let pending: string[] = [];
    let raf = 0;
    const flush = () => {
      raf = 0;
      if (!pending.length) return;
      const next = buffer.current.concat(pending);
      pending = [];
      buffer.current = next.length > MAX_LINES ? next.slice(-MAX_LINES) : next;
      setLines(buffer.current);
    };
    const push = (l: string[]) => {
      pending.push(...l);
      if (!raf) raf = requestAnimationFrame(flush);
    };

    es.onopen = () => setConnected(true);
    es.addEventListener("snapshot", (e) => {
      const text = JSON.parse((e as MessageEvent).data) as string;
      buffer.current = text ? text.replace(/\n$/, "").split("\n") : [];
      pending = [];
      setLines(buffer.current);
    });
    es.addEventListener("log", (e) => push([JSON.parse((e as MessageEvent).data) as string]));
    es.addEventListener("status", (e) => {
      setStatus(JSON.parse((e as MessageEvent).data) as string);
      flush();
      es.close();
      setConnected(false);
    });
    es.addEventListener("failure", (e) => {
      const data = (e as MessageEvent).data;
      if (data) setError(JSON.parse(data) as string);
    });
    es.onerror = () => setConnected(false);
    return () => {
      if (raf) cancelAnimationFrame(raf);
      es.close();
    };
  }, [url]);

  return {
    lines,
    status,
    connected,
    error,
    clear: () => {
      buffer.current = [];
      setLines([]);
    },
  };
}

/** Listens to /api/events and invalidates matching queries so the UI stays live. */
export function useLiveEvents() {
  const qc = useQueryClient();
  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout>;
    const connect = () => {
      es = new EventSource("/api/events", { withCredentials: true });
      es.addEventListener("event", (e) => {
        const { kind, id } = JSON.parse((e as MessageEvent).data) as { kind: string; id: number; status: string };
        qc.invalidateQueries({ queryKey: [kind, id] });
        qc.invalidateQueries({ queryKey: ["env-resources"] });
        qc.invalidateQueries({ queryKey: ["dashboard"] });
        if (kind === "deployment") {
          qc.invalidateQueries({ queryKey: ["deployments"] });
          qc.invalidateQueries({ queryKey: ["app-deployments"] });
          qc.invalidateQueries({ queryKey: ["application"] });
          qc.invalidateQueries({ queryKey: ["previews"] });
        }
        if (kind === "server") qc.invalidateQueries({ queryKey: ["servers"] });
        if (kind === "backup") qc.invalidateQueries({ queryKey: ["backup-runs"] });
        if (kind === "application" || kind === "service" || kind === "database") {
          qc.invalidateQueries({ queryKey: ["containers"] });
        }
      });
      es.onerror = () => {
        es?.close();
        retry = setTimeout(connect, 3000);
      };
    };
    connect();
    return () => {
      clearTimeout(retry);
      es?.close();
    };
  }, [qc]);
}
