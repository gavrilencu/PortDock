import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Clock, GitCommitHorizontal, GitPullRequest, Rocket, ShieldCheck, Undo2, X } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Deployment } from "../lib/types";
import { useLogStream } from "../lib/sse";
import { Badge, Button, Empty, ErrorBox, Input, Loading, PageHeader, SegmentedControl, StatusBadge, useConfirm } from "../components/ui";
import { LogViewer } from "../components/LogViewer";
import { DeploymentsTable } from "../components/shared";
import { useApp } from "./Application";
import { duration, shortSha, timeAgo } from "../lib/utils";
import { t } from "../lib/i18n";

export function DeploymentPage() {
  const params = useParams();
  const appId = Number(params.id);
  const depId = Number(params.depId);
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const app = useApp(appId);
  const dep = useQuery({ queryKey: ["deployment", depId], queryFn: () => api.get<Deployment>(`/deployments/${depId}`) });
  // Pending/rejected deployments have no build output yet: attach once approved.
  const waiting = dep.data?.status === "pending_approval" || dep.data?.status === "rejected";
  const stream = useLogStream(dep.data && !waiting ? `/deployments/${depId}/stream` : null);
  const [, tick] = useState(0);

  const finished = stream.status ?? (dep.data && !["queued", "running", "pending_approval"].includes(dep.data.status) ? dep.data.status : null);
  const [comment, setComment] = useState("");
  const review = useMutation({
    mutationFn: (approve: boolean) => api.post<Deployment>(`/deployments/${depId}/${approve ? "approve" : "reject"}`, { comment }),
    onSuccess: (d) => {
      qc.invalidateQueries({ queryKey: ["deployment", depId] });
      qc.invalidateQueries({ queryKey: ["deployments"] });
      toast.success(d.status === "queued" ? t("Approved — the deployment is starting") : t("Deployment rejected"));
    },
    onError: (e) => toast.error(e.message),
  });
  useEffect(() => {
    if (stream.status) qc.invalidateQueries({ queryKey: ["deployment", depId] });
  }, [stream.status, depId, qc]);
  useEffect(() => {
    if (finished) return;
    const timer = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(timer);
  }, [finished]);

  const cancel = useMutation({
    mutationFn: () => api.post(`/deployments/${depId}/cancel`),
    onSuccess: () => toast.success(t("Cancellation requested")),
    onError: (e) => toast.error(e.message),
  });
  const rollback = useMutation({
    mutationFn: () => api.post<Deployment>(`/deployments/${depId}/rollback`),
    onSuccess: (d) => nav(`/applications/${appId}/deployments/${d.id}`),
    onError: (e) => toast.error(e.message),
  });
  const redeploy = useMutation({
    mutationFn: () => api.post<Deployment>(`/applications/${appId}/deploy`, { commit_sha: dep.data?.commit_sha }),
    onSuccess: (d) => nav(`/applications/${appId}/deployments/${d.id}`),
    onError: (e) => toast.error(e.message),
  });

  if (dep.isLoading || app.isLoading) return <Loading />;
  if (dep.error) return <ErrorBox error={dep.error} />;
  const d = dep.data!;
  const a = app.data!;
  const status = stream.status ?? d.status;
  const active = status === "queued" || status === "running";
  const pending = status === "pending_approval";
  const isCurrent = a.current_image === d.image && d.pr_number === 0;

  return (
    <>
      <PageHeader
        icon={<Rocket className="size-5" />}
        title={
          <span className="flex items-center gap-3">
            {t("Deployment #{id}", { id: d.id })} <StatusBadge status={status} />
            {isCurrent && status === "success" && <Badge tone="success">{t("current")}</Badge>}
          </span>
        }
        subtitle={
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
            {d.commit_sha && (
              <span className="flex items-center gap-1 font-mono">
                <GitCommitHorizontal className="size-3.5" />
                {shortSha(d.commit_sha)}
              </span>
            )}
            {d.commit_message && <span className="truncate">{d.commit_message}</span>}
            {d.pr_number > 0 && (
              <Badge tone="info">
                <GitPullRequest className="size-3" /> PR #{d.pr_number}
              </Badge>
            )}
            <span className="flex items-center gap-1">
              <Clock className="size-3.5" /> {timeAgo(d.created_at)} · {d.started_at ? duration(d.started_at, d.finished_at || undefined) : t("queued")}
            </span>
            <span>{({ manual: t("Triggered by a user"), webhook: t("Triggered by a Git push"), api: t("Triggered by the API"), rollback: t("Triggered by a rollback") } as Record<string, string>)[d.trigger] ?? t("Triggered by {trigger}", { trigger: d.trigger })}</span>
          </span>
        }
        breadcrumbs={
          <>
            <Link to={`/projects/${a.project.id}`} className="hover:text-fg">{a.project.name}</Link> / <Link to={`/applications/${a.id}`} className="hover:text-fg">{a.name}</Link> /{" "}
            <Link to={`/applications/${a.id}?tab=deployments`} className="hover:text-fg">{t("Deployments")}</Link> / <span className="text-fg">#{d.id}</span>
          </>
        }
        actions={
          <>
            {(active || pending) && a.access.can_deploy && (
              <Button variant="danger" icon={<Ban className="size-4" />} loading={cancel.isPending} onClick={() => cancel.mutate()}>
                {pending ? t("Withdraw request") : t("Cancel")}
              </Button>
            )}
            {!active && !pending && a.access.can_deploy && d.image && !isCurrent && status === "success" && (
              <Button
                icon={<Undo2 className="size-4" />}
                loading={rollback.isPending}
                onClick={async () => {
                  if (await confirm({ title: t("Roll back to #{id}?", { id: d.id }), description: t("Redeploys image {image} without rebuilding.", { image: d.image }), confirmText: t("Roll back") })) rollback.mutate();
                }}
              >
                {t("Roll back to this")}
              </Button>
            )}
            {!active && !pending && a.access.can_deploy && (
              <Button variant="primary" icon={<Rocket className="size-4" />} loading={redeploy.isPending} onClick={() => redeploy.mutate()}>
                {a.access.deploy_needs_approval ? t("Request redeploy") : t("Redeploy")} {d.commit_sha ? shortSha(d.commit_sha) : ""}
              </Button>
            )}
          </>
        }
      />
      {(pending || status === "rejected" || d.reviewed_by > 0) && (
        <div className={`mb-4 rounded-xl border p-4 ${pending ? "border-warning/40 bg-warning-soft" : status === "rejected" ? "border-danger/30 bg-danger-soft" : "border-border bg-surface"}`}>
          <div className="flex flex-wrap items-start gap-3">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-surface">
              <ShieldCheck className="size-4" />
            </span>
            <div className="min-w-0 flex-1 text-[13px]">
              <div className="font-semibold">
                {pending ? t("Waiting for approval") : status === "rejected" ? t("Rejected by {name}", { name: d.reviewed_by_name ?? "" }) : t("Approved by {name}", { name: d.reviewed_by_name ?? "" })}
              </div>
              <div className="mt-0.5 text-muted">
                {t("Requested by {name}", { name: d.requested_by_name || t("automation (Git push or API)") })} · {timeAgo(d.created_at)}
                {d.reviewed_at && ` · ${t("reviewed {time}", { time: timeAgo(d.reviewed_at) })}`}
              </div>
              {d.review_comment && <div className="mt-1.5 rounded-md bg-surface px-2.5 py-1.5 text-fg">“{d.review_comment}”</div>}
              {pending && !d.can_review && <div className="mt-1.5 text-xs text-muted">{t("A project maintainer must approve this deployment in {env}.", { env: a.environment.name })}</div>}
            </div>
            {pending && d.can_review && (
              <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
                <Input className="h-8 w-full sm:w-64" placeholder={t("Comment (optional)")} value={comment} onChange={(e) => setComment(e.target.value)} />
                <Button icon={<X />} loading={review.isPending && review.variables === false} onClick={() => review.mutate(false)}>
                  {t("Reject")}
                </Button>
                <Button
                  variant="primary"
                  icon={<Check />}
                  disabled={!d.can_self_approve}
                  title={d.can_self_approve ? undefined : t("You requested this deployment — another maintainer must approve it")}
                  loading={review.isPending && review.variables === true}
                  onClick={() => review.mutate(true)}
                >
                  {t("Approve & deploy")}
                </Button>
              </div>
            )}
          </div>
          {pending && d.can_review && !d.can_self_approve && <div className="mt-2 pl-12 text-xs text-muted">{t("You requested this deployment, so another maintainer must approve it (four-eyes principle).")}</div>}
        </div>
      )}
      <LogViewer lines={stream.lines} live={active} className="h-[calc(100vh-240px)] min-h-[420px]" filename={`deployment-${d.id}.log`} emptyText={pending ? t("The build starts once the deployment is approved.") : status === "rejected" ? t("This deployment was rejected and never ran.") : status === "queued" ? t("Waiting for a build slot…") : active ? t("Waiting for output…") : t("No build output was recorded for this deployment.")} />
    </>
  );
}

export function DeploymentsPage() {
  const [status, setStatus] = useState("");
  const { data, isLoading } = useQuery({ queryKey: ["deployments", status], queryFn: () => api.get<Deployment[]>(`/deployments${status ? `?status=${status}` : ""}`), refetchInterval: 15_000 });
  return (
    <>
      <PageHeader title={t("Deployments")} subtitle={t("Build and release history across every application.")} />
      <div className="mb-4 flex items-center gap-3">
        <SegmentedControl
          value={status}
          onChange={setStatus}
          options={[
            { value: "", label: t("All") },
            { value: "running", label: t("Building") },
            { value: "queued", label: t("Queued") },
            { value: "pending_approval", label: t("Awaiting approval") },
            { value: "success", label: t("Ready") },
            { value: "failed", label: t("Failed") },
          ]}
        />
        {data && <span className="text-xs text-subtle">{t("{n} deployment|{n} deployments", { n: data.length })}</span>}
      </div>
      {isLoading ? (
        <Loading />
      ) : data!.length === 0 ? (
        <Empty icon={<Rocket />} title={t("No deployments")} description={status ? t("No deployments match this filter.") : t("Deployments appear here once an application is shipped.")} />
      ) : (
        <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
          <DeploymentsTable deployments={data!} showApp />
        </div>
      )}
    </>
  );
}
