import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Clock, GitCommitHorizontal, GitPullRequest, Rocket, Undo2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Deployment } from "../lib/types";
import { useLogStream } from "../lib/sse";
import { Badge, Button, Empty, ErrorBox, Loading, PageHeader, SegmentedControl, StatusBadge, useConfirm } from "../components/ui";
import { LogViewer } from "../components/LogViewer";
import { DeploymentsTable } from "../components/shared";
import { useApp } from "./Application";
import { duration, shortSha, timeAgo } from "../lib/utils";

export function DeploymentPage() {
  const params = useParams();
  const appId = Number(params.id);
  const depId = Number(params.depId);
  const qc = useQueryClient();
  const nav = useNavigate();
  const confirm = useConfirm();
  const app = useApp(appId);
  const dep = useQuery({ queryKey: ["deployment", depId], queryFn: () => api.get<Deployment>(`/deployments/${depId}`) });
  const stream = useLogStream(`/deployments/${depId}/stream`);
  const [, tick] = useState(0);

  const finished = stream.status ?? (dep.data && !["queued", "running"].includes(dep.data.status) ? dep.data.status : null);
  useEffect(() => {
    if (stream.status) qc.invalidateQueries({ queryKey: ["deployment", depId] });
  }, [stream.status, depId, qc]);
  useEffect(() => {
    if (finished) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [finished]);

  const cancel = useMutation({
    mutationFn: () => api.post(`/deployments/${depId}/cancel`),
    onSuccess: () => toast.success("Cancellation requested"),
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
  const isCurrent = a.current_image === d.image && d.pr_number === 0;

  return (
    <>
      <PageHeader
        icon={<Rocket className="size-5" />}
        title={
          <span className="flex items-center gap-3">
            Deployment #{d.id} <StatusBadge status={status} />
            {isCurrent && status === "success" && <Badge tone="success">current</Badge>}
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
              <Clock className="size-3.5" /> {timeAgo(d.created_at)} · {d.started_at ? duration(d.started_at, d.finished_at || undefined) : "queued"}
            </span>
            <span>Triggered by {({ manual: "a user", webhook: "a Git push", api: "the API", rollback: "a rollback" } as Record<string, string>)[d.trigger] ?? d.trigger}</span>
          </span>
        }
        breadcrumbs={
          <>
            <Link to={`/projects/${a.project.id}`} className="hover:text-fg">{a.project.name}</Link> / <Link to={`/applications/${a.id}`} className="hover:text-fg">{a.name}</Link> /{" "}
            <Link to={`/applications/${a.id}?tab=deployments`} className="hover:text-fg">Deployments</Link> / <span className="text-fg">#{d.id}</span>
          </>
        }
        actions={
          <>
            {active && (
              <Button variant="danger" icon={<Ban className="size-4" />} loading={cancel.isPending} onClick={() => cancel.mutate()}>
                Cancel
              </Button>
            )}
            {!active && d.image && !isCurrent && status === "success" && (
              <Button
                icon={<Undo2 className="size-4" />}
                loading={rollback.isPending}
                onClick={async () => {
                  if (await confirm({ title: `Roll back to #${d.id}?`, description: `Redeploys image ${d.image} without rebuilding.`, confirmText: "Roll back" })) rollback.mutate();
                }}
              >
                Roll back to this
              </Button>
            )}
            {!active && (
              <Button variant="primary" icon={<Rocket className="size-4" />} loading={redeploy.isPending} onClick={() => redeploy.mutate()}>
                Redeploy {d.commit_sha ? shortSha(d.commit_sha) : ""}
              </Button>
            )}
          </>
        }
      />
      <LogViewer lines={stream.lines} live={active} className="h-[calc(100vh-240px)] min-h-[420px]" filename={`deployment-${d.id}.log`} emptyText={status === "queued" ? "Waiting for a build slot…" : active ? "Waiting for output…" : "No build output was recorded for this deployment."} />
    </>
  );
}

export function DeploymentsPage() {
  const [status, setStatus] = useState("");
  const { data, isLoading } = useQuery({ queryKey: ["deployments", status], queryFn: () => api.get<Deployment[]>(`/deployments${status ? `?status=${status}` : ""}`), refetchInterval: 15_000 });
  return (
    <>
      <PageHeader title="Deployments" subtitle="Build and release history across every application." />
      <div className="mb-4 flex items-center gap-3">
        <SegmentedControl
          value={status}
          onChange={setStatus}
          options={[
            { value: "", label: "All" },
            { value: "running", label: "Building" },
            { value: "queued", label: "Queued" },
            { value: "success", label: "Ready" },
            { value: "failed", label: "Failed" },
          ]}
        />
        {data && <span className="text-xs text-subtle">{data.length} deployments</span>}
      </div>
      {isLoading ? (
        <Loading />
      ) : data!.length === 0 ? (
        <Empty icon={<Rocket />} title="No deployments" description={status ? "No deployments match this filter." : "Deployments appear here once an application is shipped."} />
      ) : (
        <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
          <DeploymentsTable deployments={data!} showApp />
        </div>
      )}
    </>
  );
}
