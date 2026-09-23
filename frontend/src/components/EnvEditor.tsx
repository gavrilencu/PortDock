import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Code2, Eye, EyeOff, FileCode2, Hammer, KeyRound, Lock, Plus, RotateCw, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, EnvVar } from "../lib/types";
import { Badge, Button, Card, Empty, Input, Loading, SegmentedControl, Switch, Tabs, Textarea, useConfirm } from "./ui";
import { cn } from "../lib/utils";
import { t } from "../lib/i18n";
import { useComposeServices } from "./Domains";

function toDotEnv(vars: EnvVar[]) {
  return vars
    .map((v) => {
      const needsQuote = /[\s#"'\n]/.test(v.value);
      const val = needsQuote ? `"${v.value.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n")}"` : v.value;
      return `${v.key}=${val}`;
    })
    .join("\n");
}

/**
 * Variables of an application, service or database. For docker compose
 * applications each compose service has its own variables (on top of the
 * application-wide ones), and `app` enables applying changes with a restart.
 */
export function EnvEditor({ owner, supportsBuild = false, supportsPreview = false, app }: { owner: string; supportsBuild?: boolean; supportsPreview?: boolean; app?: ApplicationDetail }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const key = ["envs", owner];
  const { data, isLoading } = useQuery({ queryKey: key, queryFn: () => api.get<EnvVar[]>(`${owner}/envs`) });
  const compose = app?.build_pack === "compose";
  const services = useComposeServices(app?.id ?? 0, compose).data?.services ?? [];
  const [service, setService] = useState("");
  const [changed, setChanged] = useState(false);
  const [scope, setScope] = useState<"production" | "preview">("production");
  const [dev, setDev] = useState(false);
  const [bulk, setBulk] = useState("");
  const [draft, setDraft] = useState({ key: "", value: "", is_build: false, is_secret: false });
  const [revealed, setRevealed] = useState<Record<number, boolean>>({});
  const [edits, setEdits] = useState<Record<number, string>>({});

  const isPreview = scope === "preview";
  const vars = (data ?? []).filter((v) => v.is_preview === isPreview && (v.service ?? "") === service);
  const fromCompose = Object.entries(services.find((s) => s.name === service)?.env ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const refresh = () => {
    setChanged(true);
    return qc.invalidateQueries({ queryKey: key });
  };

  const apply = useMutation({
    mutationFn: () => api.post(`/applications/${app!.id}/apply-env`),
    onSuccess: () => {
      setChanged(false);
      qc.invalidateQueries({ queryKey: ["deployments"] });
      qc.invalidateQueries({ queryKey: ["application", app!.id] });
      toast.success(t("Restarting with the new variables"), { description: t("Follow it in Deployments. Nothing is rebuilt.") });
    },
    onError: (e) => toast.error(e.message),
  });

  const add = useMutation({
    mutationFn: () => api.post<EnvVar>(`${owner}/envs`, { ...draft, is_preview: isPreview, service }),
    onSuccess: () => {
      setDraft({ key: "", value: "", is_build: false, is_secret: false });
      refresh();
      toast.success(t("Variable added"));
    },
    onError: (e) => toast.error(e.message),
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: number; body: Partial<EnvVar> }) => api.patch<EnvVar>(`/envs/${id}`, body),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/envs/${id}`),
    onSuccess: refresh,
    onError: (e) => toast.error(e.message),
  });
  const saveBulk = useMutation({
    mutationFn: () => api.put<EnvVar[]>(`${owner}/envs`, { content: bulk, is_preview: isPreview, service }),
    onSuccess: () => {
      refresh();
      setDev(false);
      toast.success(t("Environment saved"));
    },
    onError: (e) => toast.error(e.message),
  });

  if (isLoading) return <Loading />;

  const serviceName = service || app?.compose_service || t("main service");
  return (
    <div className="space-y-4">
      {compose && services.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-[13px] text-muted">{t("Variables of")}</span>
          <SegmentedControl
            value={service}
            onChange={(v) => {
              setService(v);
              setDev(false);
            }}
            options={[{ value: "", label: t("Application") }, ...services.map((s) => ({ value: s.name, label: s.name }))]}
          />
        </div>
      )}
      {app && !isPreview && (
        <div className={cn("flex flex-wrap items-center gap-3 rounded-lg border px-4 py-2.5 text-[13px]", changed ? "border-info/25 bg-info-soft" : "border-border bg-surface-2/40")}>
          <RotateCw className={cn("size-4", changed ? "text-info" : "text-faint")} />
          <span className="flex-1 text-muted">
            {changed ? t("Variables saved. Restart the application to use them now — the next deployment uses them anyway.") : t("Changes are saved right away and used by the next deployment. Restart to apply them now, without a rebuild.")}
          </span>
          <Button size="sm" variant={changed ? "primary" : "secondary"} icon={<RotateCw className="size-3.5" />} loading={apply.isPending} onClick={() => apply.mutate()}>
            {t("Restart with new variables")}
          </Button>
        </div>
      )}
      {supportsPreview && (
        <Tabs
          className="mb-2"
          value={scope}
          onChange={setScope}
          tabs={[
            { id: "production", label: t("Production") },
            { id: "preview", label: t("Preview deployments") },
          ]}
        />
      )}
      <Card
        title={isPreview ? t("Preview variables") : service ? t("Variables of {service}", { service }) : t("Environment variables")}
        description={
          isPreview
            ? t("Override production values for pull request previews.")
            : service
              ? t("Given only to the {service} container. They override the values from docker-compose.yml and can be used as ${VAR} in its build args.", { service })
              : compose
                ? t("Available as ${VAR} in docker-compose.yml and given to the main service ({service}). Encrypted at rest.", { service: serviceName })
                : t("Encrypted at rest. Changes apply on the next deployment.")
        }
        actions={
          dev ? (
            <>
              <Button size="sm" variant="ghost" onClick={() => setDev(false)}>
                {t("Cancel")}
              </Button>
              <Button size="sm" variant="primary" icon={<Save className="size-3.5" />} loading={saveBulk.isPending} onClick={() => saveBulk.mutate()}>
                {t("Save all")}
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              icon={<Code2 className="size-3.5" />}
              onClick={() => {
                setBulk(toDotEnv(vars));
                setDev(true);
              }}
            >
              {t("Developer view")}
            </Button>
          )
        }
      >
        {dev ? (
          <div className="p-5">
            <Textarea value={bulk} onChange={(e) => setBulk(e.target.value)} rows={16} placeholder={"DATABASE_URL=postgres://...\nAPI_KEY=secret"} />
            <p className="mt-2 text-xs text-muted">{isPreview ? t("Paste a .env file. Saving replaces all preview variables; build/secret flags are kept for existing keys.") : t("Paste a .env file. Saving replaces all production variables; build/secret flags are kept for existing keys.")}</p>
          </div>
        ) : (
          <div className="divide-y divide-border">
            {vars.length === 0 && (
              <div className="p-5">
                <Empty icon={<KeyRound />} title={t("No variables yet")} description={t("Add a variable below or paste a .env file in developer view.")} />
              </div>
            )}
            {vars.map((v) => {
              const hidden = v.is_secret && !revealed[v.id];
              const edited = edits[v.id];
              return (
                <div key={v.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
                  <code className="w-56 shrink-0 truncate font-mono text-[13px] font-medium">{v.key}</code>
                  <div className="relative min-w-48 flex-1">
                    <Input
                      className="font-mono text-[13px]"
                      type={hidden ? "password" : "text"}
                      value={edited ?? v.value}
                      onChange={(e) => setEdits({ ...edits, [v.id]: e.target.value })}
                    />
                  </div>
                  <div className="flex items-center gap-1">
                    {v.is_build && <Badge tone="info"><Hammer className="size-3" />{t("build")}</Badge>}
                    {v.is_secret && <Badge tone="warning"><Lock className="size-3" />{t("secret")}</Badge>}
                    {edited !== undefined && edited !== v.value && (
                      <Button
                        size="sm"
                        variant="primary"
                        onClick={() =>
                          update.mutate(
                            { id: v.id, body: { value: edited } },
                            { onSuccess: () => setEdits(({ [v.id]: _, ...rest }) => rest) },
                          )
                        }
                      >
                        {t("Save")}
                      </Button>
                    )}
                    {v.is_secret && (
                      <Button size="icon" variant="ghost" onClick={() => setRevealed({ ...revealed, [v.id]: !revealed[v.id] })}>
                        {hidden ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
                      </Button>
                    )}
                    {supportsBuild && (
                      <Button size="icon" variant="ghost" title={t("Toggle build-time")} onClick={() => update.mutate({ id: v.id, body: { is_build: !v.is_build } })}>
                        <Hammer className={cn("size-4", v.is_build && "text-info")} />
                      </Button>
                    )}
                    <Button size="icon" variant="ghost" title={t("Toggle secret")} onClick={() => update.mutate({ id: v.id, body: { is_secret: !v.is_secret } })}>
                      <Lock className={cn("size-4", v.is_secret && "text-warning")} />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      onClick={async () => {
                        if (await confirm({ title: t("Delete {key}?", { key: v.key }), danger: true, confirmText: t("Delete") })) remove.mutate(v.id);
                      }}
                    >
                      <Trash2 className="size-4 text-danger" />
                    </Button>
                  </div>
                </div>
              );
            })}
            {fromCompose.length > 0 && (
              <div className="bg-surface-2/30 px-5 py-3">
                <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted">
                  <FileCode2 className="size-3.5" />
                  {t("From docker-compose.yml")}
                </div>
                <div className="space-y-1.5">
                  {fromCompose.map(([k, val]) => {
                    const overridden = vars.some((v) => v.key === k);
                    return (
                      <div key={k} className="flex flex-wrap items-center gap-3">
                        <code className={cn("w-56 shrink-0 truncate font-mono text-[13px]", overridden && "text-faint line-through")}>{k}</code>
                        <code className={cn("min-w-48 flex-1 truncate font-mono text-[13px] text-muted", overridden && "text-faint line-through")} title={val}>
                          {val || "—"}
                        </code>
                        {overridden ? (
                          <Badge>{t("overridden")}</Badge>
                        ) : (
                          <Button size="sm" variant="ghost" onClick={() => setDraft({ key: k, value: val, is_build: false, is_secret: false })}>
                            {t("Override")}
                          </Button>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
            <form
              className="flex flex-wrap items-center gap-3 bg-panel-2/40 px-5 py-3"
              onSubmit={(e) => {
                e.preventDefault();
                if (draft.key) add.mutate();
              }}
            >
              <Input className="w-56 font-mono text-[13px]" placeholder="KEY" value={draft.key} onChange={(e) => setDraft({ ...draft, key: e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_") })} />
              <Input className="min-w-48 flex-1 font-mono text-[13px]" placeholder={t("value")} value={draft.value} onChange={(e) => setDraft({ ...draft, value: e.target.value })} />
              {supportsBuild && <Switch checked={draft.is_build} onChange={(is_build) => setDraft({ ...draft, is_build })} label={t("Build")} />}
              <Switch checked={draft.is_secret} onChange={(is_secret) => setDraft({ ...draft, is_secret })} label={t("Secret")} />
              <Button type="submit" variant="primary" icon={<Plus className="size-4" />} loading={add.isPending} disabled={!draft.key}>
                {t("Add")}
              </Button>
            </form>
          </div>
        )}
      </Card>
    </div>
  );
}
