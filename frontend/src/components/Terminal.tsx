import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { RotateCw } from "lucide-react";
import { wsURL } from "../lib/api";
import { Button, StatusDot } from "./ui";
import { t } from "../lib/i18n";

/** Interactive terminal over WebSocket (docker exec or SSH shell). */
export function Terminal({ path, className = "h-[520px]" }: { path: string; className?: string }) {
  const host = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<"connecting" | "open" | "closed">("connecting");
  const [session, setSession] = useState(0);

  useEffect(() => {
    if (!host.current) return;
    const term = new XTerm({
      cursorBlink: true,
      fontFamily: '"JetBrains Mono", "Cascadia Code", Menlo, Consolas, monospace',
      fontSize: 13,
      theme: { background: "#08080b", foreground: "#e4e4e7", cursor: "#7c6dff", selectionBackground: "#7c6dff55" },
      scrollback: 5000,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current);
    fit.fit();

    setState("connecting");
    const ws = new WebSocket(`${wsURL(path)}?rows=${term.rows}&cols=${term.cols}`);
    ws.binaryType = "arraybuffer";
    ws.onopen = () => {
      setState("open");
      term.focus();
    };
    ws.onmessage = (e) => {
      if (typeof e.data === "string") term.write(e.data);
      else term.write(new Uint8Array(e.data as ArrayBuffer));
    };
    ws.onclose = () => setState("closed");
    const send = (m: object) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify(m));
    const input = term.onData((data) => send({ type: "input", data }));
    const resize = term.onResize(({ rows, cols }) => send({ type: "resize", rows, cols }));
    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* element hidden */
      }
    });
    ro.observe(host.current);
    return () => {
      ro.disconnect();
      input.dispose();
      resize.dispose();
      ws.close();
      term.dispose();
    };
  }, [path, session]);

  return (
    <div className="overflow-hidden rounded-xl border border-border bg-log">
      <div className="flex items-center justify-between border-b border-white/5 px-3 py-2 text-xs text-zinc-400">
        <span className="flex items-center gap-2">
          <StatusDot status={state === "open" ? "running" : state === "connecting" ? "starting" : "stopped"} />
          {state === "open" ? t("Connected") : state === "connecting" ? t("Connecting…") : t("Disconnected")}
        </span>
        <Button size="sm" variant="ghost" icon={<RotateCw className="size-3.5" />} onClick={() => setSession((s) => s + 1)}>
          {t("Reconnect")}
        </Button>
      </div>
      <div ref={host} className={className} />
    </div>
  );
}
