import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bird, Check, Undo2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, Deployment } from "../lib/types";
import { t } from "../lib/i18n";
import { Badge, Button, Field, Input, Select } from "./ui";

// Canary deployments: the strategy in the application's settings and the live
// panel of a deployment.

interface CanaryInfo {
  percent: number;
  started_at: string;
  ends_at: string;
  requests: number;
  errors: number;
  error_rate: number;
  stable_requests: number;
  stable_errors: number;
  stable_rate: number;
  max_errors: number;
  decision?: string;
}

/** CanaryStrategy edits the canary settings of an application. */
export function CanaryStrategy({ f, set }: { f: ApplicationDetail; set: <K extends keyof ApplicationDetail>(k: K, v: ApplicationDetail[K]) => void }) {
  const on = (f.canary_percent ?? 0) > 0;
  return (
    <>
      <Field label={t("Strategy")} hint={t("Canary: the new version first gets part of the traffic and is rolled back automatically if it answers with more server errors than allowed.")}>
        <Select value={String(f.canary_percent ?? 0)} onChange={(e) => set("canary_percent", Number(e.target.value))}>
          <option value="0">{t("All at once (after the health check)")}</option>
          {[5, 10, 25, 50].map((p) => (
            <option key={p} value={p}>
              {t("Canary: {n}% of the traffic first", { n: p })}
            </option>
          ))}
        </Select>
      </Field>
      {on && (
        <>
          <Field label={t("Observation (minutes)")} hint={t("Then all the traffic moves to the new version.")}>
            <Input type="number" min={1} max={120} value={f.canary_minutes ?? 10} onChange={(e) => set("canary_minutes", Number(e.target.value))} />
          </Field>
          <Field label={t("Error threshold (%)")} hint={t("Share of 5xx responses of the new version that rolls it back (when also clearly above the current version's).")}>
            <Input type="number" min={0.1} max={100} step={0.1} value={f.canary_max_errors ?? 5} onChange={(e) => set("canary_max_errors", Number(e.target.value))} />
          </Field>
        </>
      )}
    </>
  );
}

function remaining(ends: string) {
  const s = Math.max(0, Math.round((new Date(ends).getTime() - Date.now()) / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** CanaryPanel shows a deployment's canary and lets people decide early. */
export function CanaryPanel({ deployment, canDeploy }: { deployment: Deployment; canDeploy: boolean }) {
  const qc = useQueryClient();
  const observing = deployment.canary_status === "observing" && deployment.status === "running";
  // Live numbers while the canary runs.
  useQuery({
    queryKey: ["deployment", deployment.id],
    queryFn: () => api.get<Deployment>(`/deployments/${deployment.id}`),
    refetchInterval: observing ? 5000 : false,
    enabled: observing,
  });
  const [, tick] = useState(0);
  useEffect(() => {
    if (!observing) return;
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [observing]);
  const decide = useMutation({
    mutationFn: (action: "promote" | "rollback") => api.post(`/deployments/${deployment.id}/canary`, { action }),
    onSuccess: (_, action) => {
      toast.success(action === "promote" ? t("Promoting the new version") : t("Rolling the new version back"));
      qc.invalidateQueries({ queryKey: ["deployment", deployment.id] });
    },
    onError: (e) => toast.error(e.message),
  });
  if (!deployment.canary_status || !deployment.canary_info) return null;
  let info: CanaryInfo;
  try {
    info = JSON.parse(deployment.canary_info);
  } catch {
    return null;
  }
  const tone = deployment.canary_status === "rolled_back" ? "danger" : deployment.canary_status === "promoted" ? "success" : "info";
  const label = deployment.canary_status === "rolled_back" ? t("Rolled back") : deployment.canary_status === "promoted" ? t("Promoted") : t("Observing");
  return (
    <div className="mb-4 rounded-xl border border-border bg-surface p-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2">
          <Bird className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
            {t("Canary: {n}% of the traffic", { n: info.percent })}
            <Badge tone={tone}>{label}</Badge>
            {observing && <span className="text-xs font-normal text-muted">{t("{time} left", { time: remaining(info.ends_at) })}</span>}
          </div>
          <div className="mt-1 text-xs text-muted">
            {t("New version: {requests} responses, {rate}% server errors · current version: {stable}% · threshold {max}%", {
              requests: info.requests,
              rate: info.error_rate.toFixed(1),
              stable: info.stable_rate.toFixed(1),
              max: info.max_errors,
            })}
          </div>
          {info.decision && <div className={`mt-1 text-xs ${deployment.canary_status === "rolled_back" ? "text-danger" : "text-muted"}`}>{info.decision}</div>}
        </div>
        {observing && canDeploy && (
          <div className="flex gap-2">
            <Button size="sm" icon={<Undo2 className="size-3.5" />} loading={decide.isPending && decide.variables === "rollback"} onClick={() => decide.mutate("rollback")}>
              {t("Roll back")}
            </Button>
            <Button size="sm" variant="primary" icon={<Check className="size-3.5" />} loading={decide.isPending && decide.variables === "promote"} onClick={() => decide.mutate("promote")}>
              {t("Promote now")}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
