import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpCircle, Copy, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { DatabaseDetail, Server } from "../lib/types";
import { t, tMaybe } from "../lib/i18n";
import { formatBytes, timeAgo } from "../lib/utils";
import { Badge, Button, Card, CopyField, Empty, Field, Input, Modal, Select, Switch, Table, useConfirm } from "./ui";

// Streaming replicas and failover of a PostgreSQL database (Connection tab).

interface Replica {
  id: number;
  uuid: string;
  server_id: number;
  server_name: string;
  public_port: number;
  status: "creating" | "streaming" | "lagging" | "disconnected" | "failed" | "promoting";
  message: string;
  lag_bytes: number;
  lag_seconds: number;
  checked_at: string;
  internal_url: string;
  public_url: string;
}

interface ReplicasInfo {
  replicas: Replica[];
  supported: boolean;
  failover_auto: boolean;
  failover_after: number;
}

const tone: Record<Replica["status"], "success" | "warning" | "danger" | "info"> = {
  creating: "info",
  streaming: "success",
  lagging: "warning",
  disconnected: "danger",
  failed: "danger",
  promoting: "info",
};
const label: Record<Replica["status"], string> = {
  creating: "Creating",
  streaming: "Streaming",
  lagging: "Lagging",
  disconnected: "Disconnected",
  failed: "Failed",
  promoting: "Promoting",
};

export function Replicas({ db }: { db: DatabaseDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const key = ["replicas", db.id];
  const q = useQuery({
    queryKey: key,
    queryFn: () => api.get<ReplicasInfo>(`/databases/${db.id}/replicas`),
    refetchInterval: (s) => (s.state.data?.replicas.some((r) => r.status === "creating" || r.status === "promoting") ? 3000 : 20_000),
  });
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/servers") });
  const [add, setAdd] = useState<{ server_id: number; public_port: string } | null>(null);
  const [promote, setPromote] = useState<Replica | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: key });
  const create = useMutation({
    mutationFn: (b: { server_id: number; public_port: number }) => api.post(`/databases/${db.id}/replicas`, b),
    onSuccess: () => {
      refresh();
      setAdd(null);
      toast.success(t("Replica being created"), { description: t("The data is copied from the primary, then the replica follows it.") });
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/replicas/${id}`),
    onSuccess: () => {
      refresh();
      toast.success(t("Replica removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const failover = useMutation({
    mutationFn: (b: { auto: boolean; after: number }) => api.put(`/databases/${db.id}/failover`, b),
    onSuccess: () => {
      refresh();
      toast.success(t("Failover settings saved"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (!q.data?.supported) return null;
  const info = q.data;
  return (
    <Card
      title={t("Replicas & failover")}
      description={t("Read-only copies that follow the primary in real time (streaming replication). Send reports and read traffic to a replica, and promote it if the primary is lost.")}
      actions={
        <Button size="sm" icon={<Plus className="size-3.5" />} disabled={db.status !== "running"} onClick={() => setAdd({ server_id: db.server_id, public_port: "" })}>
          {t("Add replica")}
        </Button>
      }
    >
      {info.replicas.length === 0 ? (
        <div className="p-5">
          <Empty compact icon={<Copy />} title={t("No replicas")} description={t("A replica on another server keeps the database available when its server fails.")} />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("Replica")}</th>
              <th>{t("Status")}</th>
              <th>{t("Lag")}</th>
              <th>{t("Read-only URL")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {info.replicas.map((r) => (
              <tr key={r.id}>
                <td className="align-top">
                  <div className="font-medium">{r.server_name}</div>
                  <div className="font-mono text-2xs text-muted">{r.uuid}</div>
                </td>
                <td className="align-top">
                  <Badge tone={tone[r.status]}>{t(label[r.status])}</Badge>
                  {r.message && <p className={`mt-1 max-w-xs text-xs ${r.status === "failed" || r.status === "disconnected" ? "text-danger" : "text-muted"}`}>{tMaybe(r.message)}</p>}
                </td>
                <td className="align-top text-xs tabular-nums text-muted">
                  {r.status === "streaming" || r.status === "lagging" ? `${formatBytes(r.lag_bytes)} · ${r.lag_seconds.toFixed(1)} s` : "—"}
                  {r.checked_at && <div className="text-2xs text-faint">{timeAgo(r.checked_at)}</div>}
                </td>
                <td className="max-w-sm align-top">
                  {r.internal_url && <CopyField value={r.internal_url} secret />}
                  {r.public_url && <div className="mt-1"><CopyField value={r.public_url} secret /></div>}
                </td>
                <td className="align-top">
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" icon={<ArrowUpCircle className="size-3.5" />} disabled={!["streaming", "lagging", "disconnected"].includes(r.status)} onClick={() => setPromote(r)}>
                      {t("Promote")}
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title={t("Remove replica")}
                      disabled={r.status === "promoting"}
                      onClick={async () => {
                        if (await confirm({ title: t("Remove this replica?"), description: t("Its container, its data and its replication slot are removed."), danger: true, confirmText: t("Remove") })) remove.mutate(r.id);
                      }}
                    >
                      <Trash2 className="text-danger" />
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <div className="border-t border-border px-5 py-3">
        <Switch
          checked={info.failover_auto}
          onChange={(v) => failover.mutate({ auto: v, after: info.failover_after || 120 })}
          label={t("Automatic failover")}
          description={t("Promote the most up-to-date replica on another server when the primary's server has been unreachable for {n} seconds. PortDock cannot stop an unreachable primary: use it when that server cannot come back on its own, or keep it off and promote by hand.", { n: info.failover_after || 120 })}
        />
      </div>

      <Modal
        open={!!add}
        onClose={() => setAdd(null)}
        title={t("Add replica")}
        description={t("A replica on another server connects to the primary's public port: enable public access on the primary and allow that server in the firewall.")}
        footer={
          add && (
            <Button variant="primary" loading={create.isPending} onClick={() => create.mutate({ server_id: add.server_id, public_port: Number(add.public_port) || 0 })}>
              {t("Create replica")}
            </Button>
          )
        }
      >
        {add && (
          <div className="space-y-4">
            <Field label={t("Server")}>
              <Select value={add.server_id} onChange={(e) => setAdd({ ...add, server_id: Number(e.target.value) })}>
                {(servers.data ?? []).map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                    {s.id === db.server_id ? ` (${t("same as the primary")})` : ""}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("Public port")} hint={t("Optional: publish the replica for clients outside the server (read-only).")}>
              <Input type="number" value={add.public_port} onChange={(e) => setAdd({ ...add, public_port: e.target.value })} placeholder="15432" />
            </Field>
          </div>
        )}
      </Modal>

      <PromoteModal db={db} replica={promote} onClose={() => setPromote(null)} onDone={refresh} />
    </Card>
  );
}

function PromoteModal({ db, replica, onClose, onDone }: { db: DatabaseDetail; replica: Replica | null; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const [typed, setTyped] = useState("");
  const [force, setForce] = useState(false);
  useEffect(() => {
    setTyped("");
    setForce(false);
  }, [replica]);
  const run = useMutation({
    mutationFn: () => api.post(`/replicas/${replica!.id}/promote`, { force }),
    onSuccess: () => {
      onDone();
      qc.invalidateQueries({ queryKey: ["database", db.id] });
      onClose();
      toast.success(t("Failover done"), { description: t("{name} now runs from the promoted replica; applications keep their connection URL.", { name: db.name }) });
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Modal
      open={!!replica}
      onClose={onClose}
      title={t("Promote this replica?")}
      description={t("The primary is stopped first, then the replica becomes the writable database under the same name, so applications keep their connection URL. The previous primary's data is kept in its volume. Other replicas must be created again.")}
      footer={
        <Button variant="danger" disabled={typed !== db.name} loading={run.isPending} onClick={() => run.mutate()}>
          {t("Promote")}
        </Button>
      }
    >
      {replica && (
        <div className="space-y-4">
          {replica.lag_bytes > 0 && <p className="text-xs text-warning">{t("The replica is {lag} behind: changes not replayed yet are lost.", { lag: formatBytes(replica.lag_bytes) })}</p>}
          <Switch checked={force} onChange={setForce} label={t("Promote even if the primary cannot be stopped")} description={t("Only when the primary's server is down for good: two primaries would accept different writes.")} />
          <Field label={t("Type {name} to confirm", { name: db.name })}>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
          </Field>
        </div>
      )}
    </Modal>
  );
}
