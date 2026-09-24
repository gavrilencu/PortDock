import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, Pause, Play, RotateCw, ScrollText, Square, SquareTerminal, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Container } from "../lib/types";
import { useLogStream } from "../lib/sse";
import { Badge, Button, Menu, MenuItem, MenuSeparator, Modal, StatusDot, Table, useConfirm } from "./ui";
import { LogViewer } from "./LogViewer";
import { Terminal } from "./Terminal";
import { t, tk } from "../lib/i18n";
import { cn } from "../lib/utils";

const actionDone: Record<string, string> = {
  start: tk("{name}: started"),
  stop: tk("{name}: stopped"),
  restart: tk("{name}: restarted"),
  pause: tk("{name}: paused"),
  unpause: tk("{name}: resumed"),
  remove: tk("{name}: removed"),
};

export function ContainerLogs({ url, className = "h-[520px]" }: { url: string; className?: string }) {
  const stream = useLogStream(url);
  return <LogViewer lines={stream.lines} live={stream.connected} className={className} emptyText={stream.error ?? t("No log output yet…")} />;
}

const resourcePath: Record<string, string> = { application: "applications", database: "databases", service: "services" };

export function ContainerTable({ serverId, containers, compact = false, showServer = false }: { serverId: number; containers: Container[]; compact?: boolean; showServer?: boolean }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [logs, setLogs] = useState<Container | null>(null);
  const [term, setTerm] = useState<Container | null>(null);

  const action = useMutation({
    mutationFn: ({ c, a }: { c: Container; a: string }) => api.post(`/servers/${c.server_id || serverId}/containers/${encodeURIComponent(c.id)}/${a}`),
    onSuccess: (_, { a, c }) => {
      toast.success(t(actionDone[a] ?? tk("{name}: done"), { name: c.name }));
      qc.invalidateQueries({ queryKey: ["containers"] });
      qc.invalidateQueries({ queryKey: ["fleet"] });
    },
    onError: (e) => toast.error(e.message),
  });

  const remove = async (c: Container) => {
    if (await confirm({ title: t("Remove container {name}?", { name: c.name }), description: t("The container is force-removed. Volumes are kept."), danger: true, confirmText: t("Remove") }))
      action.mutate({ c, a: "remove" });
  };

  if (containers.length === 0)
    return <div className="px-4 py-10 text-center text-[13px] text-subtle">{t("No containers yet. They appear here after the first deployment.")}</div>;
  const withStats = containers.some((c) => c.stats);

  return (
    <>
      <Table>
        <thead>
          <tr>
            {showServer && <th>{t("Server")}</th>}
            <th>{t("Name")}</th>
            {!compact && <th>{t("Image")}</th>}
            <th>{t("Status")}</th>
            {withStats && <th className="!text-right">CPU</th>}
            {withStats && <th className="!text-right">{t("Memory")}</th>}
            {!compact && !showServer && <th>{t("Ports")}</th>}
            <th className="text-right">{t("Actions")}</th>
          </tr>
        </thead>
        <tbody>
          {containers.map((c) => {
            const running = c.state === "running";
            const kind = c.labels["portdock.kind"];
            const pr = c.labels["portdock.pr"];
            return (
              <tr key={`${c.server_id ?? serverId}-${c.id}`}>
                {showServer && (
                  <td className="whitespace-nowrap">
                    <Link to={`/servers/${c.server_id}`} className="text-[13px] font-medium hover:underline">
                      {c.server_name}
                    </Link>
                  </td>
                )}
                <td>
                  <div className="flex items-center gap-2">
                    <StatusDot status={c.state} />
                    <span className="font-medium">{c.name}</span>
                    {kind && <Badge tone="accent">{kind}</Badge>}
                    {pr && pr !== "0" && <Badge tone="info">PR #{pr}</Badge>}
                    {c.server_name && !showServer && <Badge>{c.server_name}</Badge>}
                  </div>
                  {c.resource && (
                    <Link to={`/${resourcePath[c.resource.kind]}/${c.resource.id}`} className="mt-0.5 block truncate text-xs text-subtle hover:text-fg">
                      {c.resource.project} / {c.resource.environment} / <span className="font-medium">{c.resource.name}</span>
                    </Link>
                  )}
                </td>
                {!compact && (
                  <td className="max-w-64 font-mono text-xs text-muted">
                    <div className="truncate" title={c.image}>
                      {c.image}
                    </div>
                    {showServer && c.ports && (
                      <div className="truncate text-2xs text-faint" title={c.ports}>
                        {c.ports}
                      </div>
                    )}
                  </td>
                )}
                <td className={cn("text-xs text-muted", showServer && "whitespace-nowrap")}>{c.status}</td>
                {withStats && <td className="whitespace-nowrap text-right font-mono text-xs tabular-nums">{c.stats?.cpu ?? "—"}</td>}
                {withStats && (
                  <td className="whitespace-nowrap text-right font-mono text-xs tabular-nums" title={c.stats?.mem_usage}>
                    {c.stats ? `${c.stats.mem_usage.split("/")[0].trim()} · ${c.stats.mem}` : "—"}
                  </td>
                )}
                {!compact && !showServer && <td className="max-w-56 truncate font-mono text-xs text-muted">{c.ports || "—"}</td>}
                <td>
                  <div className="flex justify-end gap-0.5">
                    <Button size="icon" variant="ghost" title={t("Logs")} onClick={() => setLogs(c)}>
                      <ScrollText className="size-4" />
                    </Button>
                    {running && (
                      <Button size="icon" variant="ghost" title={t("Terminal")} onClick={() => setTerm(c)}>
                        <SquareTerminal className="size-4" />
                      </Button>
                    )}
                    {running ? (
                      <Button size="icon" variant="ghost" title={t("Restart")} onClick={() => action.mutate({ c, a: "restart" })}>
                        <RotateCw className="size-4" />
                      </Button>
                    ) : (
                      <Button size="icon" variant="ghost" title={c.state === "paused" ? t("Resume") : t("Start")} onClick={() => action.mutate({ c, a: c.state === "paused" ? "unpause" : "start" })}>
                        <Play className="size-4" />
                      </Button>
                    )}
                    {showServer ? (
                      <Menu
                        trigger={
                          <Button size="icon" variant="ghost" aria-label={t("More actions")}>
                            <MoreHorizontal className="size-4" />
                          </Button>
                        }
                      >
                        {running && (
                          <>
                            <MenuItem icon={<Pause />} onClick={() => action.mutate({ c, a: "pause" })}>
                              {t("Pause")}
                            </MenuItem>
                            <MenuItem icon={<Square />} onClick={() => action.mutate({ c, a: "stop" })}>
                              {t("Stop")}
                            </MenuItem>
                            <MenuSeparator />
                          </>
                        )}
                        <MenuItem icon={<Trash2 />} danger onClick={() => remove(c)}>
                          {t("Remove")}
                        </MenuItem>
                      </Menu>
                    ) : (
                      <>
                        {running && (
                          <>
                            <Button size="icon" variant="ghost" title={t("Pause")} onClick={() => action.mutate({ c, a: "pause" })}>
                              <Pause className="size-4" />
                            </Button>
                            <Button size="icon" variant="ghost" title={t("Stop")} onClick={() => action.mutate({ c, a: "stop" })}>
                              <Square className="size-4" />
                            </Button>
                          </>
                        )}
                        <Button size="icon" variant="ghost" title={t("Remove")} onClick={() => remove(c)}>
                          <Trash2 className="size-4 text-danger" />
                        </Button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </Table>
      <Modal open={!!logs} onClose={() => setLogs(null)} title={`${t("Logs")} · ${logs?.name}`} size="xl">
        {logs && <ContainerLogs url={`/servers/${logs.server_id || serverId}/containers/${encodeURIComponent(logs.id)}/logs?tail=500`} />}
      </Modal>
      <Modal open={!!term} onClose={() => setTerm(null)} title={`${t("Terminal")} · ${term?.name}`} size="xl">
        {term && <Terminal path={`/servers/${term.server_id || serverId}/containers/${encodeURIComponent(term.id)}/terminal`} />}
      </Modal>
    </>
  );
}
