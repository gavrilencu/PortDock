import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, Container as ContainerIcon, Pencil, Plus, PlugZap, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Registry, RegistryKind } from "../lib/types";
import { t } from "../lib/i18n";
import { Button, Card, Empty, Field, Input, Loading, Modal, PasswordInput, Table, useConfirm } from "./ui";

const kindColors: Record<string, string> = {
  dockerhub: "bg-[#1d63ed] text-white",
  ghcr: "bg-[#24292f] text-white",
  gitlab: "bg-[#fc6d26] text-white",
  ecr: "bg-[#ff9900] text-[#232f3e]",
  harbor: "bg-[#60b932] text-white",
  quay: "bg-[#40b4e5] text-white",
  generic: "bg-inverse text-inverse-fg",
};
const kindLetters: Record<string, string> = { dockerhub: "DH", ghcr: "GH", gitlab: "GL", ecr: "ECR", harbor: "HB", quay: "Q", generic: "OCI" };

export function RegistryMark({ kind, className = "size-8 text-[10px]" }: { kind: string; className?: string }) {
  return <span className={`flex shrink-0 items-center justify-center rounded-md font-semibold ${kindColors[kind] ?? kindColors.generic} ${className}`}>{kindLetters[kind] ?? "OCI"}</span>;
}

export function useRegistries() {
  return useQuery({ queryKey: ["registries"], queryFn: () => api.get<{ registries: Registry[]; kinds: RegistryKind[] }>("/registries") });
}

type Form = Partial<Registry> & { password?: string };

function RegistryModal({ initial, kinds, onClose }: { initial: Form | null; kinds: RegistryKind[]; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<Form>({});
  const [tested, setTested] = useState(false);
  useEffect(() => {
    setF(initial ?? {});
    setTested(false);
  }, [initial]);
  const kind = kinds.find((k) => k.kind === f.kind);
  const editing = !!f.id;
  const upd = (patch: Form) => {
    setF({ ...f, ...patch });
    setTested(false);
  };

  const test = useMutation({
    mutationFn: () => api.post("/registries/test", f),
    onSuccess: () => {
      setTested(true);
      toast.success(t("Credentials are valid"));
    },
    onError: (e) => toast.error(e.message),
  });
  const save = useMutation({
    mutationFn: () => (editing ? api.patch(`/registries/${f.id}`, f) : api.post("/registries", f)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["registries"] });
      toast.success(editing ? t("Registry updated") : t("Registry added"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="lg"
      title={editing ? t("Edit {name}", { name: f.name ?? "" }) : kind ? t("Add {name}", { name: kind.name }) : t("Add container registry")}
      description={kind ? kind.hint : t("Built images are pushed here and every server pulls from it — required for fast multi-server deployments.")}
      footer={
        kind && (
          <>
            <Button icon={tested ? <CircleCheck className="text-success" /> : <PlugZap />} loading={test.isPending} onClick={() => test.mutate()}>
              {t("Test connection")}
            </Button>
            <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
              {editing ? t("Save changes") : t("Add registry")}
            </Button>
          </>
        )
      }
    >
      {!kind ? (
        <div className="grid gap-2 sm:grid-cols-2">
          {kinds.map((k) => (
            <button key={k.kind} onClick={() => upd({ kind: k.kind, host: k.host })} className="flex items-center gap-3 rounded-lg border border-border bg-surface p-3 text-left transition-colors hover:border-border-strong hover:bg-surface-2">
              <RegistryMark kind={k.kind} />
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{k.name}</div>
                <div className="truncate font-mono text-2xs text-muted">{k.host || t("custom host")}</div>
              </div>
            </button>
          ))}
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          <Field label={t("Registry host")} hint={kind.kind === "ecr" ? "<account>.dkr.ecr.<region>.amazonaws.com" : t("Without https://")}>
            <Input value={f.host ?? ""} onChange={(e) => upd({ host: e.target.value })} disabled={!!kind.host && kind.kind !== "generic"} className="font-mono text-xs" placeholder="registry.example.com" />
          </Field>
          <Field label={t("Namespace")} hint={t("Prefix for image names: user, organization, project or group/project.")}>
            <Input value={f.namespace ?? ""} onChange={(e) => upd({ namespace: e.target.value })} className="font-mono text-xs" placeholder={kind.kind === "ecr" ? t("optional") : "acme"} />
          </Field>
          <Field label={kind.username_label}>
            <Input value={f.username ?? ""} onChange={(e) => upd({ username: e.target.value })} autoComplete="off" />
          </Field>
          <Field label={kind.password_label} hint={editing && f.password_set ? t("Leave empty to keep the stored secret.") : t("Stored encrypted; passed to docker login over stdin.")}>
            <PasswordInput value={f.password ?? ""} onChange={(e) => upd({ password: e.target.value })} placeholder={editing && f.password_set ? "••••••••" : ""} autoComplete="new-password" />
          </Field>
          <Field label={t("Display name")} className="md:col-span-2">
            <Input value={f.name ?? ""} onChange={(e) => upd({ name: e.target.value })} placeholder={kind.name} />
          </Field>
          <div className="rounded-lg border border-border bg-surface-2/60 px-3 py-2 text-xs text-muted md:col-span-2">
            {t("Images will be pushed as")}{" "}
            <code className="font-mono text-fg">
              {(f.host || "host") + "/" + [f.namespace || (["dockerhub", "ghcr", "quay"].includes(kind.kind) ? (f.username || "").toLowerCase() : ""), "<app-name>"].filter(Boolean).join("/")}:&lt;commit&gt;
            </code>
          </div>
        </div>
      )}
    </Modal>
  );
}

export function RegistriesSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useRegistries();
  const [edit, setEdit] = useState<Form | null>(null);
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/registries/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["registries"] });
      toast.success(t("Registry removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: (id: number) => api.post(`/registries/${id}/test`),
    onSuccess: () => toast.success(t("Connection successful")),
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  const { registries, kinds } = q.data!;
  return (
    <Card
      title={t("Container registries")}
      description={t("Push built images once and pull them on every server. Also used to pull private images.")}
      actions={
        registries.length > 0 && (
          <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
            {t("Add registry")}
          </Button>
        )
      }
    >
      {registries.length === 0 ? (
        <div className="p-5">
          <Empty
            compact
            icon={<ContainerIcon />}
            title={t("No registries")}
            description={t("Connect Docker Hub, GitHub Container Registry, GitLab, Amazon ECR, Harbor, Quay or any OCI registry.")}
            action={
              <Button variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
                {t("Add registry")}
              </Button>
            }
          />
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("Registry")}</th>
              <th>{t("Namespace")}</th>
              <th>{t("Applications")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {registries.map((r) => (
              <tr key={r.id}>
                <td>
                  <div className="flex items-center gap-3">
                    <RegistryMark kind={r.kind} />
                    <div className="min-w-0">
                      <div className="font-medium">{r.name}</div>
                      <div className="truncate font-mono text-2xs text-muted">
                        {r.host} · {r.username || t("anonymous")}
                      </div>
                    </div>
                  </div>
                </td>
                <td className="font-mono text-xs text-muted">{r.namespace || "—"}</td>
                <td className="text-xs text-muted">{r.apps}</td>
                <td className="text-right">
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" loading={test.isPending && test.variables === r.id} onClick={() => test.mutate(r.id)}>
                      {t("Test")}
                    </Button>
                    <Button size="icon-sm" variant="ghost" title={t("Edit")} onClick={() => setEdit({ ...r, password: "" })}>
                      <Pencil />
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title={t("Remove")}
                      onClick={async () => (await confirm({ title: t("Remove {name}?", { name: r.name }), description: t("Images already pushed are not deleted from the registry."), danger: true, confirmText: t("Remove") })) && del.mutate(r.id)}
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
      <RegistryModal initial={edit} kinds={kinds} onClose={() => setEdit(null)} />
    </Card>
  );
}
