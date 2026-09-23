import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { api } from "../lib/api";
import { formatBytes } from "../lib/utils";
import { Card, Empty, SegmentedControl, Skeleton } from "./ui";

export interface MetricPoint {
  t: number;
  cpu: number;
  cpu_max: number;
  mem: number;
  mem_total: number;
  disk: number;
  disk_total: number;
  rx: number;
  tx: number;
  load: number;
  restarts: number;
  up: number;
}

export type MetricRange = "1h" | "6h" | "24h" | "7d" | "30d";
export const ranges: { value: MetricRange; label: string }[] = [
  { value: "1h", label: "1H" },
  { value: "6h", label: "6H" },
  { value: "24h", label: "24H" },
  { value: "7d", label: "7D" },
  { value: "30d", label: "30D" },
];

export type Series = { key: string; label: string; color: string; values: (number | null)[]; dashed?: boolean };

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    if (!ref.current) return;
    const ro = new ResizeObserver(([e]) => setW(e.contentRect.width));
    ro.observe(ref.current);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function timeLabel(t: number, span: number) {
  const d = new Date(t * 1000);
  if (span > 2 * 86400) return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** Dependency-free SVG line/area chart with crosshair tooltip. */
export function LineChart({
  times,
  series,
  format,
  max,
  height = 160,
  threshold,
}: {
  times: number[];
  series: Series[];
  format: (v: number) => string;
  max?: number;
  height?: number;
  threshold?: number;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const gid = useId().replace(/:/g, "");
  const padL = 44,
    padR = 8,
    padT = 8,
    padB = 20;
  const w = Math.max(0, width - padL - padR);
  const h = height - padT - padB;
  const peak = Math.max(0, ...series.flatMap((s) => s.values.filter((v): v is number => v != null)), threshold ?? 0);
  const yMax = max ?? niceMax(peak * 1.1);
  const t0 = times[0] ?? 0;
  const t1 = times[times.length - 1] ?? 1;
  const span = Math.max(1, t1 - t0);
  const x = (t: number) => padL + ((t - t0) / span) * w;
  const y = (v: number) => padT + h - (Math.min(v, yMax) / yMax) * h;

  const paths = useMemo(
    () =>
      series.map((s) => {
        let d = "";
        let area = "";
        let open = false;
        let startX = 0;
        s.values.forEach((v, i) => {
          if (v == null) {
            if (open) area += `L${x(times[i - 1])},${padT + h}L${startX},${padT + h}Z`;
            open = false;
            return;
          }
          const px = x(times[i]),
            py = y(v);
          if (!open) {
            d += `M${px},${py}`;
            area += `M${px},${padT + h}L${px},${py}`;
            startX = px;
            open = true;
          } else {
            d += `L${px},${py}`;
            area += `L${px},${py}`;
          }
        });
        if (open) area += `L${x(times[times.length - 1])},${padT + h}L${startX},${padT + h}Z`;
        return { d, area };
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, times, width, yMax],
  );

  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => f * yMax);
  const xTicks = width > 0 ? Math.max(2, Math.min(6, Math.floor(w / 110))) : 0;

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    if (!times.length) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const t = t0 + ((e.clientX - rect.left - padL) / Math.max(1, w)) * span;
    let best = 0;
    for (let i = 1; i < times.length; i++) if (Math.abs(times[i] - t) < Math.abs(times[best] - t)) best = i;
    setHover(best);
  };

  return (
    <div ref={ref} className="relative w-full" style={{ height }}>
      {width > 0 && (
        <svg width={width} height={height} onMouseMove={onMove} onMouseLeave={() => setHover(null)} className="block select-none">
          <defs>
            {series.map((s, i) => (
              <linearGradient key={s.key} id={`${gid}-${i}`} x1="0" x2="0" y1="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.22} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          {ticks.map((v, i) => (
            <g key={i}>
              <line x1={padL} x2={padL + w} y1={y(v)} y2={y(v)} stroke="var(--chart-grid)" strokeDasharray={i === 0 ? undefined : "3 3"} />
              <text x={padL - 6} y={y(v)} dy="0.32em" textAnchor="end" className="fill-[var(--fg-faint)] text-[10px] tabular-nums">
                {format(v)}
              </text>
            </g>
          ))}
          {Array.from({ length: xTicks }, (_, i) => {
            const t = t0 + (span * (i + 0.5)) / xTicks;
            return (
              <text key={i} x={x(t)} y={height - 4} textAnchor="middle" className="fill-[var(--fg-faint)] text-[10px]">
                {timeLabel(t, span)}
              </text>
            );
          })}
          {threshold != null && threshold < yMax && (
            <line x1={padL} x2={padL + w} y1={y(threshold)} y2={y(threshold)} stroke="var(--chart-critical)" strokeDasharray="4 3" strokeOpacity={0.7} />
          )}
          {paths.map((p, i) =>
            series[i].dashed ? null : <path key={`a${i}`} d={p.area} fill={`url(#${gid}-${i})`} />,
          )}
          {paths.map((p, i) => (
            <path key={`l${i}`} d={p.d} fill="none" stroke={series[i].color} strokeWidth={1.6} strokeDasharray={series[i].dashed ? "4 3" : undefined} strokeLinejoin="round" />
          ))}
          {hover != null && (
            <g>
              <line x1={x(times[hover])} x2={x(times[hover])} y1={padT} y2={padT + h} stroke="var(--border-strong)" />
              {series.map((s) =>
                s.values[hover] != null ? <circle key={s.key} cx={x(times[hover])} cy={y(s.values[hover]!)} r={3} fill={s.color} stroke="var(--surface)" strokeWidth={1.5} /> : null,
              )}
            </g>
          )}
        </svg>
      )}
      {hover != null && width > 0 && (
        <div
          className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs shadow-pop"
          style={x(times[hover]) > width / 2 ? { right: width - x(times[hover]) + 10 } : { left: x(times[hover]) + 10 }}
        >
          <div className="mb-1 text-2xs text-subtle">{new Date(times[hover] * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</div>
          {series.map((s) => (
            <div key={s.key} className="flex items-center gap-2">
              <span className="size-2 rounded-full" style={{ background: s.color }} />
              <span className="flex-1 text-muted">{s.label}</span>
              <span className="font-medium tabular-nums">{s.values[hover] == null ? "—" : format(s.values[hover]!)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const pct = (v: number) => `${v >= 10 || v === 0 ? v.toFixed(0) : v.toFixed(1)}%`;
const bytes = (v: number) => (v === 0 ? "0" : formatBytes(v));
const rate = (v: number) => (v === 0 ? "0" : `${formatBytes(v)}/s`);

function ChartCard({ title, value, children }: { title: string; value?: string; children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border bg-surface p-4 shadow-card">
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <div className="text-[13px] font-medium text-muted">{title}</div>
        {value && <div className="text-lg font-semibold tabular-nums tracking-[-0.02em]">{value}</div>}
      </div>
      {children}
    </div>
  );
}

/** Metrics dashboard for a server or resource. */
export function MetricsPanel({ scope, id, title = "Metrics" }: { scope: "server" | "application" | "database" | "service"; id: number; title?: string }) {
  const [range, setRange] = useState<MetricRange>(() => {
    try {
      return (localStorage.getItem("portdock.metrics.range") as MetricRange) || "6h";
    } catch {
      return "6h";
    }
  });
  const pick = (r: MetricRange) => {
    setRange(r);
    try {
      localStorage.setItem("portdock.metrics.range", r);
    } catch {
      /* ignore */
    }
  };
  const q = useQuery({
    queryKey: ["metrics", scope, id, range],
    queryFn: () => api.get<{ points: MetricPoint[]; bucket: number }>(`/metrics/${scope}/${id}?range=${range}`),
    refetchInterval: 60_000,
  });
  const pts = q.data?.points ?? [];
  const bucket = q.data?.bucket ?? 60;

  // Insert gaps where samples are missing so lines don't bridge outages.
  const filled = useMemo(() => {
    const out: (MetricPoint | null)[] = [];
    pts.forEach((p, i) => {
      if (i > 0 && p.t - pts[i - 1].t > bucket * 2.5) out.push(null);
      out.push(p);
    });
    return out;
  }, [pts, bucket]);
  const times = useMemo(() => {
    const ts: number[] = [];
    filled.forEach((p, i) => ts.push(p ? p.t : (filled[i - 1]!.t + bucket)));
    return ts;
  }, [filled, bucket]);
  const col = (f: (p: MetricPoint) => number) => filled.map((p) => (p ? f(p) : null));
  const last = pts[pts.length - 1];
  const isServer = scope === "server";

  const memPct = (p: MetricPoint) => (p.mem_total > 0 ? (p.mem / p.mem_total) * 100 : 0);
  const memTotal = last?.mem_total ?? 0;
  const hourly = range === "7d" || range === "30d";

  return (
    <Card
      title={title}
      description={isServer ? "Sampled every minute. CPU and memory are for the whole machine." : "Sampled every minute, summed across replicas and servers. CPU: 100% = one core."}
      actions={<SegmentedControl value={range} onChange={pick} options={ranges} />}
    >
      <div className="p-4">
        {q.isLoading ? (
          <div className="grid gap-4 md:grid-cols-2">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-[210px] rounded-xl" />
            ))}
          </div>
        ) : pts.length === 0 ? (
          <Empty compact icon={<Activity />} title="No data yet" description="Metrics appear a minute after the resource starts running." />
        ) : (
          <div className="grid gap-4 md:grid-cols-2">
            <ChartCard title="CPU" value={last ? pct(last.cpu) : undefined}>
              <LineChart
                times={times}
                format={pct}
                max={isServer ? 100 : undefined}
                series={[
                  { key: "cpu", label: hourly ? "Average" : "CPU", color: "var(--chart-1)", values: col((p) => p.cpu) },
                  ...(hourly ? [{ key: "max", label: "Peak", color: "var(--chart-3)", values: col((p) => p.cpu_max), dashed: true }] : []),
                ]}
              />
            </ChartCard>
            <ChartCard title="Memory" value={last ? `${bytes(last.mem)}${memTotal ? ` / ${bytes(memTotal)}` : ""}` : undefined}>
              {memTotal > 0 ? (
                <LineChart times={times} format={pct} max={100} series={[{ key: "mem", label: "Used", color: "var(--chart-2)", values: col(memPct) }]} />
              ) : (
                <LineChart times={times} format={bytes} series={[{ key: "mem", label: "Used", color: "var(--chart-2)", values: col((p) => p.mem) }]} />
              )}
            </ChartCard>
            <ChartCard title="Network" value={last ? `↓ ${rate(last.rx)}  ↑ ${rate(last.tx)}` : undefined}>
              <LineChart
                times={times}
                format={rate}
                series={[
                  { key: "rx", label: "Inbound", color: "var(--chart-1)", values: col((p) => p.rx) },
                  { key: "tx", label: "Outbound", color: "var(--chart-3)", values: col((p) => p.tx) },
                ]}
              />
            </ChartCard>
            {isServer ? (
              <ChartCard title="Disk" value={last && last.disk_total ? `${bytes(last.disk)} / ${bytes(last.disk_total)}` : undefined}>
                <LineChart
                  times={times}
                  format={pct}
                  max={100}
                  threshold={85}
                  series={[{ key: "disk", label: "Used", color: "var(--chart-3)", values: col((p) => (p.disk_total ? (p.disk / p.disk_total) * 100 : 0)) }]}
                />
              </ChartCard>
            ) : (
              <ChartCard title="Availability & restarts" value={last ? (last.up >= 1 ? "Healthy" : "Degraded") : undefined}>
                <LineChart
                  times={times}
                  format={(v) => (v >= 1 || v === 0 ? v.toFixed(0) : v.toFixed(1))}
                  series={[
                    { key: "up", label: "Healthy (1 = all running)", color: "var(--chart-2)", values: col((p) => p.up) },
                    { key: "restarts", label: "Restarts", color: "var(--chart-critical)", values: col((p) => p.restarts) },
                  ]}
                />
              </ChartCard>
            )}
            {isServer && (
              <ChartCard title="Load average (1 min)" value={last ? last.load.toFixed(2) : undefined}>
                <LineChart times={times} format={(v) => v.toFixed(1)} series={[{ key: "load", label: "Load", color: "var(--chart-1)", values: col((p) => p.load) }]} />
              </ChartCard>
            )}
          </div>
        )}
      </div>
    </Card>
  );
}
