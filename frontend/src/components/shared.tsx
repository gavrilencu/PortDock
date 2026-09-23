import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Box, Boxes, Database as DatabaseIcon, GitBranch, GitPullRequest, Undo2 } from "lucide-react";
import { api } from "../lib/api";
import type { Deployment, Server } from "../lib/types";
import { duration, shortSha, timeAgo } from "../lib/utils";
import { Badge, StatusBadge, Table } from "./ui";

export function useServers() {
  return useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers") });
}

export function ResourceIcon({ kind, className = "size-4" }: { kind: "application" | "database" | "service"; className?: string }) {
  if (kind === "database") return <DatabaseIcon className={className} />;
  if (kind === "service") return <Boxes className={className} />;
  return <Box className={className} />;
}

const triggerLabel: Record<string, string> = { manual: "Manual", webhook: "Git push", api: "API", rollback: "Rollback", schedule: "Schedule" };

function Commit({ d }: { d: Deployment }) {
  return (
    <div className="min-w-0">
      <div className="truncate font-medium text-fg">{d.commit_message || (d.status === "queued" ? "Waiting for a build slot" : d.image ? d.image : "—")}</div>
      <div className="mt-0.5 flex items-center gap-2 text-xs text-subtle">
        {d.commit_sha && <span className="font-mono">{shortSha(d.commit_sha)}</span>}
        {d.pr_number > 0 && (
          <Badge tone="info">
            <GitPullRequest />#{d.pr_number}
          </Badge>
        )}
        {d.rollback_of > 0 && (
          <Badge tone="warning">
            <Undo2 />
            Rollback of #{d.rollback_of}
          </Badge>
        )}
      </div>
    </div>
  );
}

/** Dense deployment history table used across the product. */
export function DeploymentsTable({ deployments, showApp = false }: { deployments: Deployment[]; showApp?: boolean }) {
  const nav = useNavigate();
  return (
    <Table>
      <thead>
        <tr>
          <th className="w-28">Status</th>
          {showApp && <th>Application</th>}
          <th>Commit</th>
          <th className="w-40">Branch</th>
          <th className="w-28">Trigger</th>
          <th className="w-24 !text-right">Duration</th>
          <th className="w-28 !text-right">Created</th>
        </tr>
      </thead>
      <tbody>
        {deployments.map((d) => (
          <tr key={d.id} className="cursor-pointer" onClick={() => nav(`/applications/${d.application_id}/deployments/${d.id}`)}>
            <td>
              <StatusBadge status={d.status === "running" ? "deploying" : d.status} />
            </td>
            {showApp && (
              <td className="max-w-48">
                <Link to={`/applications/${d.application_id}`} onClick={(e) => e.stopPropagation()} className="truncate font-medium hover:underline">
                  {d.application_name}
                </Link>
              </td>
            )}
            <td className="max-w-md">
              <Commit d={d} />
            </td>
            <td>
              {d.branch && (
                <span className="inline-flex max-w-full items-center gap-1.5 truncate text-muted">
                  <GitBranch className="size-3.5 shrink-0 text-faint" />
                  <span className="truncate">{d.branch}</span>
                </span>
              )}
            </td>
            <td className="text-muted">{triggerLabel[d.trigger] ?? d.trigger}</td>
            <td className="text-right text-muted">{d.started_at ? duration(d.started_at, d.finished_at || undefined) : "—"}</td>
            <td className="text-right text-muted" title={new Date(d.created_at).toLocaleString()}>
              {timeAgo(d.created_at)}
            </td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

/** Compact row, kept for places where a table is too wide. */
export function DeploymentRow({ d, showApp = false }: { d: Deployment; showApp?: boolean }) {
  return (
    <Link to={`/applications/${d.application_id}/deployments/${d.id}`} className="flex items-center gap-4 px-4 py-3 transition-colors hover:bg-surface-2/50">
      <StatusBadge status={d.status === "running" ? "deploying" : d.status} className="w-[92px]" />
      <div className="min-w-0 flex-1 text-[13px]">
        {showApp && <div className="truncate text-xs text-subtle">{d.application_name}</div>}
        <Commit d={d} />
      </div>
      <div className="text-right text-xs text-subtle">
        <div>{timeAgo(d.created_at)}</div>
        <div className="tabular">{d.started_at ? duration(d.started_at, d.finished_at || undefined) : ""}</div>
      </div>
    </Link>
  );
}
