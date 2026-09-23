import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Deployment } from "../lib/types";
import { SegmentedControl, Table } from "./ui";

interface Day {
  key: string;
  label: string;
  long: string;
  success: number;
  failed: number;
}

/** Builds per-day buckets for the last `days` days (local time). */
export function bucketDeployments(deps: Deployment[], days = 14): Day[] {
  const out: Day[] = [];
  const idx = new Map<string, Day>();
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(today);
    d.setDate(d.getDate() - i);
    const key = d.toDateString();
    const day: Day = {
      key,
      label: d.toLocaleDateString(undefined, { day: "numeric", month: "short" }),
      long: d.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" }),
      success: 0,
      failed: 0,
    };
    out.push(day);
    idx.set(key, day);
  }
  for (const dep of deps) {
    const day = idx.get(new Date(dep.created_at).toDateString());
    if (!day) continue;
    if (dep.status === "success") day.success++;
    else if (dep.status === "failed") day.failed++;
  }
  return out;
}

function niceMax(v: number) {
  if (v <= 4) return 4;
  const pow = 10 ** Math.floor(Math.log10(v));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => v / s <= 4) ?? 10 * pow;
  return Math.ceil(v / step) * step;
}

const H = 200;
const PAD = { top: 12, right: 8, bottom: 26, left: 32 };
const GAP = 2;
const R = 4;

/** Column with only the top corners rounded (square at the baseline). */
function topRounded(x: number, y: number, w: number, h: number, r: number) {
  const rr = Math.min(r, h, w / 2);
  return `M${x},${y + h} V${y + rr} Q${x},${y} ${x + rr},${y} H${x + w - rr} Q${x + w},${y} ${x + w},${y + rr} V${y + h} Z`;
}

export function DeployActivityChart({ deployments }: { deployments: Deployment[] }) {
  const days = useMemo(() => bucketDeployments(deployments), [deployments]);
  const [view, setView] = useState<"chart" | "table">("chart");
  const [hover, setHover] = useState<number | null>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);

  useLayoutEffect(() => {
    if (!wrap.current) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(280, e.contentRect.width)));
    ro.observe(wrap.current);
    return () => ro.disconnect();
  }, [view]);

  const total = days.reduce((a, d) => a + d.success + d.failed, 0);
  const failed = days.reduce((a, d) => a + d.failed, 0);
  const max = niceMax(Math.max(...days.map((d) => d.success + d.failed), 1));
  const ticks = [0, max / 4, max / 2, (3 * max) / 4, max];
  const innerW = width - PAD.left - PAD.right;
  const innerH = H - PAD.top - PAD.bottom;
  const band = innerW / days.length;
  const barW = Math.min(24, band * 0.56);
  const y = (v: number) => PAD.top + innerH - (v / max) * innerH;

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-4 text-xs text-muted">
          <span className="flex items-center gap-1.5">
            <span className="size-2.5 rounded-[3px]" style={{ background: "var(--chart-1)" }} />
            Successful
          </span>
          <span className="flex items-center gap-1.5">
            <span className="size-2.5 rounded-[3px]" style={{ background: "var(--chart-critical)" }} />
            Failed
          </span>
          <span className="text-subtle">
            {total} total · {total ? Math.round(((total - failed) / total) * 100) : 100}% success
          </span>
        </div>
        <SegmentedControl
          value={view}
          onChange={setView}
          options={[
            { value: "chart", label: "Chart" },
            { value: "table", label: "Table" },
          ]}
        />
      </div>

      {view === "table" ? (
        <div className="max-h-[220px] overflow-y-auto rounded-lg border border-border">
          <Table>
            <thead>
              <tr>
                <th>Day</th>
                <th className="!text-right">Successful</th>
                <th className="!text-right">Failed</th>
              </tr>
            </thead>
            <tbody>
              {[...days].reverse().map((d) => (
                <tr key={d.key}>
                  <td>{d.long}</td>
                  <td className="text-right">{d.success}</td>
                  <td className="text-right">{d.failed}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      ) : (
        <div ref={wrap} className="relative" onMouseLeave={() => setHover(null)}>
          <svg width={width} height={H} role="img" aria-label={`Deployments per day over the last 14 days: ${total} total, ${failed} failed`} className="block overflow-visible">
            {ticks.map((t) => (
              <g key={t}>
                <line x1={PAD.left} x2={width - PAD.right} y1={y(t)} y2={y(t)} stroke="var(--chart-grid)" strokeWidth={1} shapeRendering="crispEdges" />
                <text x={PAD.left - 8} y={y(t)} dy="0.32em" textAnchor="end" className="fill-[var(--fg-faint)] text-[11px] tabular-nums">
                  {Number.isInteger(t) ? t : t.toFixed(1)}
                </text>
              </g>
            ))}
            {days.map((d, i) => {
              const cx = PAD.left + band * i + band / 2;
              const x = cx - barW / 2;
              const sH = d.success ? (d.success / max) * innerH : 0;
              const fH = d.failed ? (d.failed / max) * innerH : 0;
              const base = PAD.top + innerH;
              const dim = hover !== null && hover !== i;
              return (
                <g key={d.key} opacity={dim ? 0.45 : 1} style={{ transition: "opacity 120ms" }}>
                  {sH > 0 && <path d={fH > 0 ? `M${x},${base} V${base - sH} H${x + barW} V${base} Z` : topRounded(x, base - sH, barW, sH, R)} fill="var(--chart-1)" />}
                  {fH > 0 && <path d={topRounded(x, base - sH - fH - (sH > 0 ? GAP : 0), barW, fH, R)} fill="var(--chart-critical)" />}
                  {(i % 2 === (days.length - 1) % 2 || days.length <= 7) && (
                    <text x={cx} y={H - 6} textAnchor="middle" className="fill-[var(--fg-faint)] text-[11px]">
                      {d.label}
                    </text>
                  )}
                  <rect x={PAD.left + band * i} y={PAD.top} width={band} height={innerH} fill="transparent" onMouseEnter={() => setHover(i)} />
                </g>
              );
            })}
          </svg>
          {hover !== null && (
            <div
              className="animate-fade pointer-events-none absolute z-10 min-w-40 rounded-lg border border-border bg-surface px-3 py-2 text-xs shadow-pop"
              style={{
                left: Math.min(Math.max(PAD.left + band * hover + band / 2 - 80, 0), width - 170),
                top: Math.max(0, y(days[hover].success + days[hover].failed) - 86),
              }}
            >
              <div className="mb-1.5 font-medium text-fg">{days[hover].long}</div>
              <div className="flex items-center justify-between gap-6 text-muted">
                <span className="flex items-center gap-1.5">
                  <span className="size-2 rounded-sm" style={{ background: "var(--chart-1)" }} />
                  Successful
                </span>
                <span className="tabular-nums text-fg">{days[hover].success}</span>
              </div>
              <div className="mt-1 flex items-center justify-between gap-6 text-muted">
                <span className="flex items-center gap-1.5">
                  <span className="size-2 rounded-sm" style={{ background: "var(--chart-critical)" }} />
                  Failed
                </span>
                <span className="tabular-nums text-fg">{days[hover].failed}</span>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
