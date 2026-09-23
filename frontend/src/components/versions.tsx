import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, Clock, GitCommitHorizontal, GripVertical, History, Loader2, Rocket, Undo2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, AppVersion, VersionsResponse } from "../lib/types";
import { cn, shortSha, timeAgo } from "../lib/utils";
import { intlLocale, t } from "../lib/i18n";
import { Badge, Button, Card, Empty, Loading, useConfirm } from "./ui";

const DRAG_TYPE = "application/x-portdock-version";

/**
 * Versions of an application, Kargo style: the Live stage on the left, the saved
 * versions on the right. Drag a version onto Live (or press Deploy) to put it
 * live again with the current variables, without a build.
 */
export function VersionsBoard({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [over, setOver] = useState(false);
  const q = useQuery({
    queryKey: ["versions", app.id],
    queryFn: () => api.get<VersionsResponse>(`/applications/${app.id}/versions`),
    refetchInterval: (query) => (query.state.data?.pending ? 3000 : 20_000),
  });
  const promote = useMutation({
    mutationFn: (v: AppVersion) => api.post(`/applications/${app.id}/versions/${v.id}/deploy`),
    onSuccess: (_, v) => {
      qc.invalidateQueries({ queryKey: ["versions", app.id] });
      qc.invalidateQueries({ queryKey: ["deployments"] });
      toast.success(t("Deploying {tag}", { tag: v.tag }), {
        description: app.access.deploy_needs_approval ? t("It waits for a maintainer's approval.") : t("It replaces the live version as soon as it is healthy."),
      });
    },
    onError: (e) => toast.error(e.message),
  });

  if (q.isLoading) return <Loading />;
  const data = q.data!;
  const versions = data.versions;
  const live = versions.find((v) => v.live);
  const previous = versions.find((v) => !v.live && (!live || v.number < live.number));
  const pending = data.pending && data.pending.version_id ? versions.find((v) => v.id === data.pending!.version_id) : undefined;
  const canDeploy = app.access.can_deploy;

  const ask = async (v: AppVersion) => {
    if (!canDeploy || v.live || promote.isPending) return;
    const ok = await confirm({
      title: t("Deploy {tag}?", { tag: v.tag }),
      description: live
        ? t("{tag} replaces {live} once it is healthy. The current environment variables are kept and nothing is rebuilt.", { tag: v.tag, live: live.tag })
        : t("{tag} is started with the current environment variables; nothing is rebuilt.", { tag: v.tag }),
      confirmText: t("Deploy {tag}", { tag: v.tag }),
    });
    if (ok) promote.mutate(v);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold tracking-[-0.01em]">{t("Versions")}</h2>
          <p className="max-w-3xl text-[13px] text-subtle">
            {t("Every successful production deployment is saved as a version. The live one and the last {kept} are kept for {days} days. Drag a version onto Live — or press Deploy — to switch; environment variables stay as they are.", {
              kept: data.kept,
              days: data.max_age_days,
            })}
          </p>
        </div>
        {previous && canDeploy && (
          <Button icon={<Undo2 />} loading={promote.isPending && promote.variables?.id === previous.id} onClick={() => ask(previous)}>
            {t("Roll back to {tag}", { tag: previous.tag })}
          </Button>
        )}
      </div>

      {versions.length === 0 ? (
        <Card>
          <div className="p-4">
            <Empty compact icon={<History />} title={t("No versions yet")} description={t("The next successful production deployment is saved as v1.")} />
          </div>
        </Card>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[minmax(320px,380px)_1fr]">
          {/* Live stage (drop target) */}
          <section
            onDragOver={(e) => {
              if (canDeploy && e.dataTransfer.types.includes(DRAG_TYPE)) {
                e.preventDefault();
                setOver(true);
              }
            }}
            onDragLeave={() => setOver(false)}
            onDrop={(e) => {
              setOver(false);
              const id = Number(e.dataTransfer.getData(DRAG_TYPE));
              const v = versions.find((x) => x.id === id);
              if (v) ask(v);
            }}
            className={cn(
              "flex flex-col rounded-xl border-2 border-dashed bg-surface p-4 shadow-card transition-colors lg:sticky lg:top-4 lg:self-start",
              over ? "border-accent bg-accent-soft" : "border-border",
            )}
          >
            <div className="mb-3 flex items-center gap-2">
              <span className="relative flex size-2.5">
                <span className="absolute inset-0 animate-ping rounded-full bg-success opacity-40" />
                <span className="relative size-2.5 rounded-full bg-success" />
              </span>
              <span className="text-sm font-semibold">{t("Live")}</span>
              <span className="text-xs text-subtle">· {app.name}</span>
            </div>
            {live ? <VersionBody v={live} big /> : <p className="text-[13px] text-muted">{t("No saved version is live. Deploy one to start.")}</p>}
            {data.pending && (
              <Link
                to={`/applications/${app.id}/deployments/${data.pending.deployment_id}`}
                className="mt-4 flex items-center gap-2 rounded-lg border border-info/25 bg-info-soft px-3 py-2 text-[13px] text-info hover:underline"
              >
                <Loader2 className="size-4 animate-spin" />
                {pending
                  ? data.pending.status === "pending_approval"
                    ? t("{tag} is waiting for approval", { tag: pending.tag })
                    : t("Switching to {tag}…", { tag: pending.tag })
                  : t("A deployment is in progress…")}
              </Link>
            )}
            <div className={cn("mt-4 rounded-lg border border-dashed px-3 py-4 text-center text-xs", over ? "border-accent text-accent" : "border-border text-faint")}>
              {canDeploy ? t("Drop a version here to deploy it") : t("You can't deploy this application")}
            </div>
          </section>

          {/* Saved versions */}
          <div className="space-y-2">
            {versions.map((v) => (
              <div
                key={v.id}
                draggable={canDeploy && !v.live}
                onDragStart={(e) => {
                  e.dataTransfer.setData(DRAG_TYPE, String(v.id));
                  e.dataTransfer.effectAllowed = "move";
                }}
                className={cn(
                  "group flex items-start gap-3 rounded-xl border bg-surface p-3.5 shadow-xs transition-shadow",
                  v.live ? "border-success/40" : "border-border",
                  canDeploy && !v.live && "cursor-grab hover:shadow-card active:cursor-grabbing",
                  pending?.id === v.id && "ring-2 ring-info/40",
                )}
              >
                {canDeploy && !v.live ? <GripVertical className="mt-1 size-4 shrink-0 text-faint group-hover:text-muted" /> : <span className="w-4 shrink-0" />}
                <div className="min-w-0 flex-1">
                  <VersionBody v={v} />
                </div>
                {v.live ? (
                  <Badge tone="success">{t("Live")}</Badge>
                ) : (
                  canDeploy && (
                    <Button size="sm" icon={<Rocket />} loading={promote.isPending && promote.variables?.id === v.id} onClick={() => ask(v)}>
                      {t("Deploy")}
                    </Button>
                  )
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function VersionBody({ v, big = false }: { v: AppVersion; big?: boolean }) {
  const services = Object.entries(v.services ?? {});
  const daysLeft = v.expires_at ? Math.max(0, Math.ceil((new Date(v.expires_at).getTime() - Date.now()) / 86_400_000)) : null;
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className={cn("rounded-md bg-inverse px-2 font-mono font-semibold text-inverse-fg", big ? "py-1 text-base" : "py-0.5 text-[13px]")}>{v.tag}</span>
        <span className="truncate text-[13px] font-medium">{v.commit_message || t("No commit message")}</span>
      </div>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-subtle">
        {v.commit_sha && (
          <span className="inline-flex items-center gap-1 font-mono">
            <GitCommitHorizontal className="size-3.5" />
            {shortSha(v.commit_sha)}
            {v.branch && <span className="font-sans">· {v.branch}</span>}
          </span>
        )}
        <span className="inline-flex items-center gap-1" title={new Date(v.created_at).toLocaleString(intlLocale())}>
          <Clock className="size-3.5" />
          {t("created {time}", { time: timeAgo(v.created_at) })}
        </span>
        {daysLeft !== null && <span>{t("kept {n} more day|kept {n} more days", { n: daysLeft })}</span>}
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {services.length > 0 ? (
          services.map(([name, image]) => (
            <span key={name} className="inline-flex items-center gap-1 rounded border border-border bg-surface-2/60 px-1.5 py-0.5 font-mono text-2xs text-muted" title={image}>
              <Box className="size-3" />
              {name}
              <span className="text-faint">:{image.split(":").pop()}</span>
            </span>
          ))
        ) : v.image && !v.image.startsWith("compose@") ? (
          <span className="inline-flex items-center gap-1 rounded border border-border bg-surface-2/60 px-1.5 py-0.5 font-mono text-2xs text-muted" title={v.image}>
            <Box className="size-3" />
            {v.image}
          </span>
        ) : null}
      </div>
    </div>
  );
}
