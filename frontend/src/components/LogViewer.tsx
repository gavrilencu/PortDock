import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDownToLine, Download, Search, WrapText } from "lucide-react";
import { cn, stripAnsi } from "../lib/utils";
import { StatusDot } from "./ui";
import { t } from "../lib/i18n";

function lineClass(l: string): string {
  if (l.includes("━━━")) return "text-accent font-semibold pt-2";
  if (l.includes("❌") || /\b(error|fatal|failed|panic)\b/i.test(l)) return "text-red-400";
  if (l.includes("⚠") || /\bwarn(ing)?\b/i.test(l)) return "text-amber-400";
  if (l.includes("✅") || l.includes("🌐")) return "text-emerald-400";
  if (/^\d\d:\d\d:\d\d \$ /.test(l)) return "text-sky-300/80";
  return "text-zinc-300";
}

export function LogViewer({
  lines,
  live,
  className,
  emptyText = t("Waiting for output…"),
  filename = "logs.txt",
}: {
  lines: string[];
  live?: boolean;
  className?: string;
  emptyText?: string;
  filename?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);
  const [wrap, setWrap] = useState(true);
  const [query, setQuery] = useState("");

  const shown = useMemo(() => {
    const clean = lines.map(stripAnsi);
    if (!query) return clean;
    const q = query.toLowerCase();
    return clean.filter((l) => l.toLowerCase().includes(q));
  }, [lines, query]);

  useEffect(() => {
    if (follow && ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [shown, follow]);

  const onScroll = () => {
    const el = ref.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (atBottom !== follow) setFollow(atBottom);
  };

  const download = () => {
    const blob = new Blob([lines.map(stripAnsi).join("\n")], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <div className={cn("flex flex-col overflow-hidden rounded-xl border border-border bg-log", className)}>
      <div className="flex items-center gap-2 border-b border-white/5 px-3 py-2">
        {live !== undefined && (
          <span className="flex items-center gap-1.5 text-xs text-zinc-400">
            <StatusDot status={live ? "deploying" : "stopped"} />
            {live ? t("Live") : t("Ended")}
          </span>
        )}
        <div className="ml-auto flex items-center gap-1">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-zinc-500" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("Filter")}
              className="h-7 w-40 rounded-md border border-white/10 bg-white/5 pl-7 pr-2 text-xs text-zinc-200 placeholder:text-zinc-500 focus:outline-none focus:border-accent"
            />
          </div>
          <button title={t("Wrap lines")} onClick={() => setWrap(!wrap)} className={cn("rounded-md p-1.5 hover:bg-white/10", wrap ? "text-zinc-200" : "text-zinc-500")}>
            <WrapText className="size-3.5" />
          </button>
          <button title={t("Follow")} onClick={() => setFollow(!follow)} className={cn("rounded-md p-1.5 hover:bg-white/10", follow ? "text-accent" : "text-zinc-500")}>
            <ArrowDownToLine className="size-3.5" />
          </button>
          <button title={t("Download")} onClick={download} className="rounded-md p-1.5 text-zinc-400 hover:bg-white/10 hover:text-zinc-200">
            <Download className="size-3.5" />
          </button>
        </div>
      </div>
      <div ref={ref} onScroll={onScroll} className="min-h-0 flex-1 overflow-auto px-4 py-3 font-mono text-[12.5px] leading-[1.6]">
        {shown.length === 0 ? (
          <div className="text-zinc-500">{emptyText}</div>
        ) : (
          shown.map((l, i) => (
            <div key={i} className={cn(lineClass(l), wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre")}>
              {l}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
