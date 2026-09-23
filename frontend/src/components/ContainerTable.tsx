import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Pause, Play, RotateCw, ScrollText, Square, SquareTerminal, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Container } from "../lib/types";
import { useLogStream } from "../lib/sse";
import { Badge, Button, Modal, StatusDot, Table, useConfirm } from "./ui";
import { LogViewer } from "./LogViewer";
import { Terminal } from "./Terminal";
import { t, tk } from "../lib/i18n";

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

export function ContainerTable({ serverId, containers, compact = false }: { serverId: number; containers: Container[]; compact?: boolean }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [logs, setLogs] = useState<Container | null>(null);
  const [term, setTerm] = useState<Container | null>(null);

  const action = useMutation({
    mutationFn: ({ c, a }: { c: Container; a: string }) => api.post(`/servers/${c.server_id || serverId}/containers/${encodeURIComponent(c.id)}/${a}`),
    onSuccess: (_, { a, c }) => {
      toast.success(t(actionDone[a] ?? tk("{name}: done"), { name: c.name }));
      qc.invalidateQueries({ queryKey: ["containers"] });
    },
    onError: (e) => toast.error(e.message),
  });

  if (containers.length === 0)
    return <div className="px-4 py-10 text-center text-[13px] text-subtle">{t("No containers yet. They appear here after the first deployment.")}</div>;

  return (
    <>
      <Table>
        <thead>
          <tr>
            <th>{t("Name")}</th>
            {!compact && <th>{t("Image")}</th>}
            <th>{t("Status")}</th>
            {!compact && <th>{t("Ports")}</th>}
            <th className="text-right">{t("Actions")}</th>
          </tr>
        </thead>
        <tbody>
          {containers.map((c) => {
            const running = c.state === "running";
            const kind = c.labels["portdock.kind"];
            const pr = c.labels["portdock.pr"];
            return (
              <tr key={c.id}>
                <td>
                  <div className="flex items-center gap-2">
                    <StatusDot status={c.state} />
                    <span className="font-medium">{c.name}</span>
                    {kind && <Badge tone="accent">{kind}</Badge>}
                    {pr && pr !== "0" && <Badge tone="info">PR #{pr}</Badge>}
                    {c.server_name && <Badge>{c.server_name}</Badge>}
                  </div>
                </td>
                {!compact && <td className="max-w-64 truncate font-mono text-xs text-muted">{c.image}</td>}
                <td className="text-xs text-muted">{c.status}</td>
                {!compact && <td className="max-w-56 truncate font-mono text-xs text-muted">{c.ports || "—"}</td>}
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
                      <>
                        <Button size="icon" variant="ghost" title={t("Restart")} onClick={() => action.mutate({ c, a: "restart" })}>
                          <RotateCw className="size-4" />
                        </Button>
                        <Button size="icon" variant="ghost" title={t("Pause")} onClick={() => action.mutate({ c, a: "pause" })}>
                          <Pause className="size-4" />
                        </Button>
                        <Button size="icon" variant="ghost" title={t("Stop")} onClick={() => action.mutate({ c, a: "stop" })}>
                          <Square className="size-4" />
                        </Button>
                      </>
                    ) : (
                      <Button size="icon" variant="ghost" title={c.state === "paused" ? t("Resume") : t("Start")} onClick={() => action.mutate({ c, a: c.state === "paused" ? "unpause" : "start" })}>
                        <Play className="size-4" />
                      </Button>
                    )}
                    <Button
                      size="icon"
                      variant="ghost"
                      title={t("Remove")}
                      onClick={async () => {
                        if (await confirm({ title: t("Remove container {name}?", { name: c.name }), description: t("The container is force-removed. Volumes are kept."), danger: true, confirmText: t("Remove") }))
                          action.mutate({ c, a: "remove" });
                      }}
                    >
                      <Trash2 className="size-4 text-danger" />
                    </Button>
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
