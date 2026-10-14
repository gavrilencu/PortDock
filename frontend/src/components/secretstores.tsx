import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, KeyRound, Pencil, Plus, PlugZap, Search, Trash2, XCircle } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { EnvVar, Project, SecretStore, SecretStoreName, SecretStoreType } from "../lib/types";
import { t } from "../lib/i18n";
import { timeAgo } from "../lib/utils";
import { Badge, Button, Card, Empty, Field, Input, Loading, Modal, PasswordInput, SegmentedControl, Select, Table, Textarea, useConfirm } from "./ui";

// External secret managers (Settings → Secret managers) and the fields of a
// variable that reads its value from one.

const storeTypes: { type: SecretStoreType; name: string; mark: string; color: string; placeholder: string; hint: string }[] = [
  {
    type: "vault",
    name: "HashiCorp Vault",
    mark: "HV",
    color: "bg-[#000000] text-[#ffec6e]",
    placeholder: "https://vault.example.com:8200",
    hint: "A KV secrets engine of Vault or OpenBao, read with a token or an AppRole.",
  },
  {
    type: "azure_keyvault",
    name: "Azure Key Vault",
    mark: "KV",
    color: "bg-[#0078d4] text-white",
    placeholder: "https://my-vault.vault.azure.net",
    hint: "Secrets of a Key Vault, read with an app registration or the managed identity of the VM.",
  },
];

function StoreMark({ type }: { type: SecretStoreType }) {
  const s = storeTypes.find((x) => x.type === type) ?? storeTypes[0];
  return <span className={`flex size-8 shrink-0 items-center justify-center rounded-md text-[10px] font-semibold ${s.color}`}>{s.mark}</span>;
}

export function useSecretStoreNames() {
  return useQuery({ queryKey: ["secret-store-names"], queryFn: () => api.get<SecretStoreName[]>("/secret-stores/names"), staleTime: 60_000 });
}

/** refText names the secret a variable reads, e.g. "vault-prod: shop/db#password". */
export function refText(stores: SecretStoreName[], v: Pick<EnvVar, "ref_store_id" | "ref_path" | "ref_key">) {
  const name = stores.find((s) => s.id === v.ref_store_id)?.name ?? t("(deleted store)");
  return `${name}: ${v.ref_path}${v.ref_key ? `#${v.ref_key}` : ""}`;
}

type StoreForm = Partial<SecretStore> & { token?: string; secret_id?: string; client_secret?: string; test_path?: string };

type TestResult = { ok: boolean; error?: string; keys?: string[] };

function StoreModal({ initial, onClose }: { initial: StoreForm | null; onClose: () => void }) {
  const qc = useQueryClient();
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects"), enabled: !!initial });
  const [f, setF] = useState<StoreForm>({});
  const [result, setResult] = useState<TestResult | null>(null);
  useEffect(() => {
    setF(initial ?? {});
    setResult(null);
  }, [initial]);
  const kind = storeTypes.find((s) => s.type === f.type);
  const editing = !!f.id;
  const upd = (patch: StoreForm) => {
    setF({ ...f, ...patch });
    setResult(null);
  };
  const vault = f.type === "vault";
  const auth = f.auth || (vault ? "token" : "client_secret");
  const body = () => ({
    name: f.name ?? "",
    type: f.type,
    url: f.url ?? "",
    auth,
    token: f.token ?? "",
    role_id: f.role_id ?? "",
    secret_id: f.secret_id ?? "",
    auth_mount: f.auth_mount ?? "",
    namespace: f.namespace ?? "",
    mount: f.mount ?? "",
    kv_version: f.kv_version || 2,
    tenant_id: f.tenant_id ?? "",
    client_id: f.client_id ?? "",
    client_secret: f.client_secret ?? "",
    authority_host: f.authority_host ?? "",
    ca_cert: f.ca_cert ?? "",
    path_prefix: vault ? (f.path_prefix ?? "") : "",
    project_ids: f.project_ids ?? [],
  });
  const save = useMutation({
    mutationFn: () => (editing ? api.patch(`/secret-stores/${f.id}`, body()) : api.post("/secret-stores", body())),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["secret-stores"] });
      qc.invalidateQueries({ queryKey: ["secret-store-names"] });
      toast.success(editing ? t("Secret manager updated") : t("Secret manager added"));
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: () => api.post<TestResult>("/secret-stores/test", { ...body(), id: f.id ?? 0, test_path: f.test_path ?? "" }),
    onSuccess: setResult,
    onError: (e) => toast.error(e.message),
  });
  const selected = new Set(f.project_ids ?? []);
  const keep = (set?: boolean) => (editing && set ? t("Leave empty to keep the stored secret.") : undefined);
  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="lg"
      title={editing ? t("Edit {name}", { name: f.name ?? "" }) : kind ? t("Add {name}", { name: kind.name }) : t("Add a secret manager")}
      description={kind ? t(kind.hint) : t("Variables can read their value from it when a deployment starts; PortDock never stores the value.")}
      footer={
        kind && (
          <>
            <Button icon={<PlugZap />} loading={test.isPending} onClick={() => test.mutate()}>
              {t("Test connection")}
            </Button>
            <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
              {editing ? t("Save changes") : t("Add secret manager")}
            </Button>
          </>
        )
      }
    >
      {!kind ? (
        <div className="grid gap-2 sm:grid-cols-2">
          {storeTypes.map((s) => (
            <button key={s.type} onClick={() => upd({ type: s.type })} className="flex items-center gap-3 rounded-lg border border-border bg-surface p-3 text-left transition-colors hover:border-border-strong hover:bg-surface-2">
              <StoreMark type={s.type} />
              <div className="min-w-0">
                <div className="text-[13px] font-medium">{s.name}</div>
                <div className="truncate font-mono text-2xs text-muted">{s.placeholder}</div>
              </div>
            </button>
          ))}
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          <Field label={t("Name")} hint={t("Used in references, e.g. vault-prod: shop/db#password.")}>
            <Input value={f.name ?? ""} onChange={(e) => upd({ name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-") })} placeholder={vault ? "vault-prod" : "keyvault-prod"} className="font-mono text-xs" />
          </Field>
          <Field label={vault ? t("Address") : t("Vault URL")}>
            <Input value={f.url ?? ""} onChange={(e) => upd({ url: e.target.value })} placeholder={kind.placeholder} className="font-mono text-xs" />
          </Field>
          <Field label={t("Sign-in")} className="md:col-span-2">
            <SegmentedControl
              value={auth}
              onChange={(v) => upd({ auth: v })}
              className="[&>button]:normal-case"
              options={
                vault
                  ? [
                      { value: "token", label: t("Token") },
                      { value: "approle", label: "AppRole" },
                    ]
                  : [
                      { value: "client_secret", label: t("App registration (client secret)") },
                      { value: "managed_identity", label: t("Managed identity") },
                    ]
              }
            />
          </Field>
          {vault && auth === "token" && (
            <Field label={t("Token")} hint={keep(f.token_set) ?? t("A token whose policy allows reading the secrets; a periodic token does not expire.")} className="md:col-span-2">
              <PasswordInput value={f.token ?? ""} onChange={(e) => upd({ token: e.target.value })} placeholder={editing && f.token_set ? "••••••••" : "hvs.…"} autoComplete="new-password" />
            </Field>
          )}
          {vault && auth === "approle" && (
            <>
              <Field label={t("Role ID")}>
                <Input value={f.role_id ?? ""} onChange={(e) => upd({ role_id: e.target.value })} className="font-mono text-xs" autoComplete="off" />
              </Field>
              <Field label={t("Secret ID")} hint={keep(f.secret_id_set)}>
                <PasswordInput value={f.secret_id ?? ""} onChange={(e) => upd({ secret_id: e.target.value })} placeholder={editing && f.secret_id_set ? "••••••••" : ""} autoComplete="new-password" />
              </Field>
              <Field label={t("AppRole mount")} hint={t("Default: approle")}>
                <Input value={f.auth_mount ?? ""} onChange={(e) => upd({ auth_mount: e.target.value })} placeholder="approle" className="font-mono text-xs" />
              </Field>
            </>
          )}
          {vault && (
            <>
              <Field label={t("Namespace")} hint={t("Vault Enterprise or HCP only.")}>
                <Input value={f.namespace ?? ""} onChange={(e) => upd({ namespace: e.target.value })} placeholder="admin/team-a" className="font-mono text-xs" />
              </Field>
              <Field label={t("KV secrets engine")} hint={t("Its mount path and version.")}>
                <div className="flex gap-2">
                  <Input value={f.mount ?? ""} onChange={(e) => upd({ mount: e.target.value })} placeholder="secret" className="font-mono text-xs" />
                  <Select value={String(f.kv_version || 2)} onChange={(e) => upd({ kv_version: Number(e.target.value) })} className="w-28 shrink-0">
                    <option value="2">KV v2</option>
                    <option value="1">KV v1</option>
                  </Select>
                </div>
              </Field>
              <Field label={t("Path prefix")} hint={t("Variables may only read paths under it, e.g. shop. Empty: any path the token can read.")}>
                <Input value={f.path_prefix ?? ""} onChange={(e) => upd({ path_prefix: e.target.value })} placeholder="shop" className="font-mono text-xs" />
              </Field>
            </>
          )}
          {!vault && auth === "client_secret" && (
            <>
              <Field label={t("Tenant ID")}>
                <Input value={f.tenant_id ?? ""} onChange={(e) => upd({ tenant_id: e.target.value })} placeholder="00000000-0000-0000-0000-000000000000" className="font-mono text-xs" />
              </Field>
              <Field label={t("Client ID")}>
                <Input value={f.client_id ?? ""} onChange={(e) => upd({ client_id: e.target.value })} placeholder="00000000-0000-0000-0000-000000000000" className="font-mono text-xs" />
              </Field>
              <Field label={t("Client secret")} hint={keep(f.client_secret_set)}>
                <PasswordInput value={f.client_secret ?? ""} onChange={(e) => upd({ client_secret: e.target.value })} placeholder={editing && f.client_secret_set ? "••••••••" : ""} autoComplete="new-password" />
              </Field>
              <Field label={t("Authority host")} hint={t("Default: https://login.microsoftonline.com (change it for a sovereign cloud).")}>
                <Input value={f.authority_host ?? ""} onChange={(e) => upd({ authority_host: e.target.value })} placeholder="https://login.microsoftonline.com" className="font-mono text-xs" />
              </Field>
            </>
          )}
          {!vault && auth === "managed_identity" && (
            <Field label={t("Client ID")} hint={t("Only for a user-assigned identity; empty uses the VM's system-assigned identity.")} className="md:col-span-2">
              <Input value={f.client_id ?? ""} onChange={(e) => upd({ client_id: e.target.value })} className="font-mono text-xs" />
            </Field>
          )}
          {!vault && (
            <p className="text-xs text-muted md:col-span-2">
              {t("Give the identity the Key Vault Secrets User role on the vault (or a Get secret access policy). Listing secrets is not needed.")}
            </p>
          )}
          <Field label={t("CA certificate")} hint={t("PEM of an internal CA that signed the server's certificate. Optional.")} className="md:col-span-2">
            <Textarea value={f.ca_cert ?? ""} onChange={(e) => upd({ ca_cert: e.target.value })} className="min-h-14 font-mono text-2xs" placeholder="-----BEGIN CERTIFICATE-----" />
          </Field>
          <Field label={t("Projects")} hint={t("Projects whose variables may use it. None selected: every project and the organization's shared variables.")} className="md:col-span-2">
            <div className="flex flex-wrap gap-1.5">
              {(projects.data ?? []).map((p) => (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => {
                    const next = new Set(selected);
                    if (next.has(p.id)) next.delete(p.id);
                    else next.add(p.id);
                    upd({ project_ids: [...next] });
                  }}
                  className={`rounded-md border px-2 py-1 text-xs transition-colors ${selected.has(p.id) ? "border-accent bg-accent/10 text-fg" : "border-border text-muted hover:text-fg"}`}
                >
                  {p.name}
                </button>
              ))}
            </div>
          </Field>
          <div className="rounded-lg border border-border bg-surface-2/40 p-3 md:col-span-2">
            <Field label={t("Test a secret (optional)")} hint={vault ? t("A path to read with Test connection, e.g. shop/db: its keys are listed, never the values.") : t("A secret name to read with Test connection: its keys are listed, never the value.")}>
              <Input value={f.test_path ?? ""} onChange={(e) => setF({ ...f, test_path: e.target.value })} placeholder={vault ? "shop/db" : "db-password"} className="font-mono text-xs" />
            </Field>
            {result && (
              <div className={`mt-3 flex items-start gap-2 text-xs ${result.ok ? "text-success" : "text-danger"}`}>
                {result.ok ? <CheckCircle2 className="mt-0.5 size-4 shrink-0" /> : <XCircle className="mt-0.5 size-4 shrink-0" />}
                <span>
                  {result.ok ? t("Signed in.") : result.error}
                  {result.ok && result.keys && (result.keys.length ? ` ${t("Keys: {keys}", { keys: result.keys.join(", ") })}` : ` ${t("The secret has one value (no keys).")}`)}
                </span>
              </div>
            )}
          </div>
        </div>
      )}
    </Modal>
  );
}

export function SecretStoresSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [edit, setEdit] = useState<StoreForm | null>(null);
  const stores = useQuery({ queryKey: ["secret-stores"], queryFn: () => api.get<SecretStore[]>("/secret-stores") });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/secret-stores/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["secret-stores"] });
      qc.invalidateQueries({ queryKey: ["secret-store-names"] });
      toast.success(t("Secret manager removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  const test = useMutation({
    mutationFn: (id: number) => api.post<TestResult>("/secret-stores/test", { id }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["secret-stores"] });
      if (r.ok) toast.success(t("Signed in."));
      else toast.error(r.error ?? t("The test failed"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (stores.isLoading) return <Loading />;
  const list = stores.data ?? [];
  return (
    <div className="space-y-6">
      <Card
        title={t("Secret managers")}
        description={t("Variables can read their value from HashiCorp Vault or Azure Key Vault instead of storing it: PortDock reads it when a deployment or job starts and masks it in logs. A new value is used from the next deployment.")}
        actions={
          list.length > 0 && (
            <Button size="sm" variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
              {t("Add secret manager")}
            </Button>
          )
        }
      >
        {list.length === 0 ? (
          <div className="p-5">
            <Empty
              compact
              icon={<KeyRound />}
              title={t("No secret managers")}
              description={t("Keep the secrets in the vault your security team already runs, and reference them from the variables.")}
              action={
                <Button variant="primary" icon={<Plus />} onClick={() => setEdit({})}>
                  {t("Add secret manager")}
                </Button>
              }
            />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Secret manager")}</th>
                <th>{t("Projects")}</th>
                <th>{t("Variables")}</th>
                <th>{t("Last test")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((s) => (
                <tr key={s.id}>
                  <td>
                    <div className="flex items-center gap-3">
                      <StoreMark type={s.type} />
                      <div className="min-w-0">
                        <div className="font-mono text-[13px] font-medium">{s.name}</div>
                        <div className="truncate font-mono text-2xs text-muted">
                          {s.url}
                          {s.path_prefix && ` · ${s.path_prefix}/…`}
                        </div>
                      </div>
                    </div>
                  </td>
                  <td className="text-xs text-muted">{s.project_ids.length === 0 ? t("All") : t("{n} project|{n} projects", { n: s.project_ids.length })}</td>
                  <td className="text-xs tabular-nums text-muted">{s.used_by}</td>
                  <td className="text-xs">
                    {!s.checked_at ? (
                      <span className="text-faint">—</span>
                    ) : s.check_error ? (
                      <Badge tone="danger" className="max-w-56 truncate" >
                        <span title={s.check_error}>{s.check_error}</span>
                      </Badge>
                    ) : (
                      <Badge tone="success">{t("OK {time}", { time: timeAgo(s.checked_at) })}</Badge>
                    )}
                  </td>
                  <td className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button size="sm" variant="ghost" icon={<PlugZap />} loading={test.isPending && test.variables === s.id} onClick={() => test.mutate(s.id)}>
                        {t("Test")}
                      </Button>
                      <Button size="sm" variant="ghost" icon={<Pencil />} onClick={() => setEdit(s)} aria-label={t("Edit")} />
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={<Trash2 className="text-danger" />}
                        aria-label={t("Delete")}
                        onClick={async () => {
                          if (await confirm({ title: t("Remove {name}?", { name: s.name }), description: t("Variables that read from it must be removed first."), danger: true, confirmText: t("Remove") })) del.mutate(s.id);
                        }}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
      <Card title={t("How variables use it")}>
        <div className="space-y-2 p-5 text-xs text-muted">
          <p>{t("In the Environment tab of an application (or in shared variables), choose Secret manager when adding a variable, then the store, the path and the key. Check lists the keys of the path.")}</p>
          <p>{t("In portdock.yaml, list them under secret_refs:")}</p>
          <pre className="overflow-x-auto rounded-lg border border-border bg-surface-2/60 p-3 font-mono text-2xs leading-relaxed text-fg">{`secret_refs:
  DB_PASSWORD: vault-prod:shop/db#password
  API_KEY: keyvault-prod:payments-api-key`}</pre>
        </div>
      </Card>
      <StoreModal initial={edit} onClose={() => setEdit(null)} />
    </div>
  );
}

export type RefDraft = { ref_store_id: number; ref_path: string; ref_key: string };

/** SecretRefFields picks the store, path and key of a variable; Check lists the keys of the path. */
export function SecretRefFields({ owner, stores, value, onChange }: { owner: string; stores: SecretStoreName[]; value: RefDraft; onChange: (v: RefDraft) => void }) {
  const [result, setResult] = useState<TestResult | null>(null);
  const store = stores.find((s) => s.id === value.ref_store_id);
  const vault = store?.type !== "azure_keyvault";
  const check = useMutation({
    mutationFn: () => api.post<TestResult>(`${owner}/envs/check-ref`, value),
    onSuccess: setResult,
    onError: (e) => toast.error(e.message),
  });
  const set = (patch: Partial<RefDraft>) => {
    onChange({ ...value, ...patch });
    setResult(null);
  };
  return (
    <div className="flex min-w-48 flex-1 flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <Select className="w-44 font-mono text-xs" value={String(value.ref_store_id || "")} onChange={(e) => set({ ref_store_id: Number(e.target.value) })}>
          <option value="">{t("Secret manager…")}</option>
          {stores.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
        <Input
          className="min-w-32 flex-1 font-mono text-[13px]"
          placeholder={vault ? (store?.path_prefix ? `${store.path_prefix}/db` : "shop/db") : "db-password"}
          value={value.ref_path}
          onChange={(e) => set({ ref_path: e.target.value })}
        />
        <Input className="w-36 font-mono text-[13px]" placeholder={vault ? t("key") : t("key (optional)")} value={value.ref_key} onChange={(e) => set({ ref_key: e.target.value })} />
        <Button size="sm" icon={<Search className="size-3.5" />} loading={check.isPending} disabled={!value.ref_store_id || !value.ref_path} onClick={() => check.mutate()}>
          {t("Check")}
        </Button>
      </div>
      {result && (
        <div className={`flex flex-wrap items-center gap-1.5 text-xs ${result.ok ? "text-success" : "text-danger"}`}>
          {result.ok ? <CheckCircle2 className="size-3.5" /> : <XCircle className="size-3.5" />}
          <span>{result.ok ? t("Found.") : result.error}</span>
          {(result.keys ?? []).length > 0 && (
            <>
              <span className="text-muted">{t("Keys:")}</span>
              {result.keys!.map((k) => (
                <button key={k} type="button" onClick={() => set({ ref_key: k })} className={`rounded border px-1.5 font-mono text-2xs ${k === value.ref_key ? "border-accent text-fg" : "border-border text-muted hover:text-fg"}`}>
                  {k}
                </button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** RefCheckButton checks the reference of a saved variable. */
export function RefCheckButton({ owner, v }: { owner: string; v: EnvVar }) {
  const check = useMutation({
    mutationFn: () => api.post<TestResult>(`${owner}/envs/check-ref`, { ref_store_id: v.ref_store_id, ref_path: v.ref_path, ref_key: v.ref_key }),
    onSuccess: (r) => (r.ok ? toast.success(t("{key} resolves", { key: v.key })) : toast.error(r.error ?? t("The test failed"))),
    onError: (e) => toast.error(e.message),
  });
  return (
    <Button size="icon" variant="ghost" title={t("Check the reference")} loading={check.isPending} onClick={() => check.mutate()}>
      <Search className="size-4" />
    </Button>
  );
}
