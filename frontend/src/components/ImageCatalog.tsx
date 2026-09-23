import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowLeft, Container, Database, ExternalLink, Eye, EyeOff, HardDrive, Lock, Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail, CatalogImage, ImageCatalog as Catalog } from "../lib/types";
import { cn, randomHex, randomPassword, slugify } from "../lib/utils";
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Skeleton, Switch } from "./ui";
import { t, tk, tx } from "../lib/i18n";

// Deterministic, restrained tint per image so the grid is scannable without logos.
const tints = ["bg-zinc-500", "bg-slate-500", "bg-stone-500", "bg-neutral-500", "bg-indigo-500", "bg-sky-600", "bg-emerald-600", "bg-amber-600", "bg-rose-500", "bg-violet-500"];
function tintFor(id: string) {
  let h = 0;
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return tints[h % tints.length];
}

export function ImageMonogram({ name, id, className }: { name: string; id: string; className?: string }) {
  const letters = name.replace(/[^A-Za-z0-9 ]/g, " ").split(/\s+/).filter(Boolean);
  const text = (letters.length > 1 ? letters[0][0] + letters[1][0] : name.slice(0, 2)).toUpperCase();
  return <span className={cn("flex size-10 shrink-0 items-center justify-center rounded-lg text-[13px] font-semibold tracking-tight text-white shadow-xs", tintFor(id), className)}>{text}</span>;
}

const CUSTOM: CatalogImage = { id: "custom", name: tk("Custom image"), image: "", category: "", description: "", port: 80 };

/** Browse the curated catalog of popular images, grouped by category. */
export function ImageCatalogBrowser({ onSelect }: { onSelect: (img: CatalogImage) => void }) {
  const { data, isLoading, error } = useQuery({ queryKey: ["image-catalog"], queryFn: () => api.get<Catalog>("/image-catalog"), staleTime: Infinity });
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("");
  const counts = useMemo(() => {
    const m: Record<string, number> = {};
    for (const i of data?.images ?? []) m[i.category] = (m[i.category] ?? 0) + 1;
    return m;
  }, [data]);
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (data?.images ?? []).filter((i) => (!cat || i.category === cat) && (!needle || `${i.name} ${i.image} ${i.description} ${i.category}`.toLowerCase().includes(needle)));
  }, [data, q, cat]);

  if (error) return <ErrorBox error={error} />;

  return (
    <Card>
      <div className="grid lg:grid-cols-[220px_minmax(0,1fr)]">
        <aside className="border-b border-border p-3 lg:border-b-0 lg:border-r">
          <div className="mb-2 px-2 text-2xs font-medium uppercase tracking-wider text-faint">{t("Categories")}</div>
          <nav className="flex gap-1 overflow-x-auto lg:flex-col lg:overflow-visible">
            {[{ id: "", label: t("All images"), n: data?.images.length ?? 0 }, ...(data?.categories ?? []).map((c) => ({ id: c, label: c, n: counts[c] ?? 0 }))].map((c) => (
              <button
                key={c.id || "all"}
                onClick={() => setCat(c.id)}
                className={cn(
                  "flex h-8 shrink-0 items-center justify-between gap-3 whitespace-nowrap rounded-md px-2 text-left text-[13px] transition-colors",
                  cat === c.id ? "bg-surface-2 font-medium text-fg" : "text-muted hover:bg-surface-2/60 hover:text-fg",
                )}
              >
                {c.label}
                <span className="text-2xs tabular-nums text-faint">{c.n}</span>
              </button>
            ))}
          </nav>
        </aside>
        <div className="min-w-0">
          <div className="flex items-center gap-3 border-b border-border p-3">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
              <Input autoFocus className="pl-8" placeholder={t("Search {count} images — e.g. postgres, grafana, ollama…", { count: data?.images.length ?? "" })} value={q} onChange={(e) => (setQ(e.target.value), e.target.value && setCat(""))} />
            </div>
            <Button icon={<Plus />} onClick={() => onSelect(CUSTOM)}>
              {t("Custom image")}
            </Button>
          </div>
          <div className="max-h-[640px] overflow-y-auto p-3">
            {isLoading ? (
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {Array.from({ length: 9 }).map((_, i) => (
                  <Skeleton key={i} className="h-24 rounded-lg" />
                ))}
              </div>
            ) : list.length === 0 ? (
              <Empty compact icon={<Container />} title={t("No images found")} description={t("Try another search, or deploy any image from a registry.")} action={<Button onClick={() => onSelect({ ...CUSTOM, image: q })}>{t("Use “{query}” as a custom image", { query: q })}</Button>} />
            ) : (
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {list.map((i) => (
                  <button
                    key={i.id}
                    onClick={() => onSelect(i)}
                    className="group flex flex-col items-start gap-3 rounded-lg border border-border bg-surface p-3.5 text-left transition-[border,box-shadow] hover:border-border-strong hover:shadow-card"
                  >
                    <div className="flex w-full items-start gap-3">
                      <ImageMonogram name={i.name} id={i.id} />
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5 text-[13px] font-semibold">
                          <span className="truncate">{i.name}</span>
                          {i.managed && (
                            <Badge tone="accent">
                              <Database />
                              {t("Managed")}
                            </Badge>
                          )}
                        </div>
                        <div className="truncate font-mono text-2xs text-subtle">{i.image}</div>
                      </div>
                    </div>
                    <p className="line-clamp-2 text-xs leading-relaxed text-muted">{i.description}</p>
                    {!cat && <span className="text-2xs text-faint">{i.category}</span>}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </Card>
  );
}

interface EnvRow {
  key: string;
  value: string;
  secret: boolean;
  required?: boolean;
  hint?: string;
}

function fillSecrets(v: string, pw: string, secret: string) {
  return v.replaceAll("${PASSWORD}", pw).replaceAll("${SECRET}", secret);
}

/** Configure and deploy a catalog (or custom) image. */
export function ImageConfigure({ image, envId, serverId, onBack, onCreated, onManaged }: { image: CatalogImage; envId: number; serverId: number; onBack: () => void; onCreated: (a: ApplicationDetail) => void; onManaged?: (engine: string) => void }) {
  // One password/secret per preset, reused wherever the template references it.
  const [password] = useState(() => randomPassword(24));
  const [secret] = useState(() => randomHex(32));
  const [f, setF] = useState(() => ({
    name: image.id === "custom" ? "" : slugify(image.name),
    docker_image: image.image,
    port: image.port,
    command: fillSecrets(image.command ?? "", password, secret),
    options: image.options ?? "",
    domains: "",
    https: true,
    deploy: true,
  }));
  const [env, setEnv] = useState<EnvRow[]>(() => (image.env ?? []).map((e) => ({ key: e.key, value: fillSecrets(e.value, password, secret), secret: !!e.secret, required: e.required, hint: e.hint })));
  const [volumes, setVolumes] = useState<string[]>(image.volumes ?? []);
  const [reveal, setReveal] = useState<Record<number, boolean>>({});
  const set = (patch: Partial<typeof f>) => setF((x) => ({ ...x, ...patch }));
  const missing = env.filter((e) => e.required && !e.value.trim()).map((e) => e.key);

  const create = useMutation({
    mutationFn: async () => {
      const name = f.name || slugify(f.docker_image.split("/").pop()?.split(":")[0] ?? "app");
      const host = f.domains.split(",")[0]?.trim().replace(/^https?:\/\//, "").split("/")[0] ?? "";
      const url = host ? `${f.https ? "https" : "http"}://${host}` : "";
      const suffix = randomHex(3);
      const volumeOpts = volumes.filter((v) => v.trim()).map((v, i) => `-v sy-${name}-${suffix}-${i}:${v.trim()}`);
      const app = await api.post<ApplicationDetail>("/applications", {
        name,
        environment_id: envId,
        server_id: serverId,
        source_type: "image",
        build_pack: "image",
        docker_image: f.docker_image.trim(),
        port: f.port,
        start_command: f.command.trim(),
        custom_docker_options: [...volumeOpts, f.options.trim()].filter(Boolean).join(" "),
        domains: f.domains,
        https: f.https,
      });
      for (const e of env.filter((e) => e.key.trim())) {
        await api.post(`/applications/${app.id}/envs`, {
          key: e.key.trim(),
          value: e.value.replaceAll("${URL}", url).replaceAll("${FQDN}", host),
          is_secret: e.secret,
        });
      }
      if (f.deploy) await api.post(`/applications/${app.id}/deploy`);
      return app;
    },
    onSuccess: (a) => {
      toast.success(f.deploy ? t("{name} created — pulling {image}", { name: a.name, image: f.docker_image }) : t("{name} created", { name: a.name }));
      onCreated(a);
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <div className="space-y-6">
      <button onClick={onBack} className="flex items-center gap-1.5 text-[13px] text-subtle hover:text-fg">
        <ArrowLeft className="size-4" /> {t("Back to catalog")}
      </button>

      <Card>
        <div className="flex flex-wrap items-start gap-4 p-5">
          {image.id === "custom" ? (
            <span className="flex size-10 items-center justify-center rounded-lg border border-border bg-surface-2">
              <Container className="size-5" />
            </span>
          ) : (
            <ImageMonogram name={image.name} id={image.id} />
          )}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 text-[15px] font-semibold">
              {image.id === "custom" ? t(image.name) : image.name}
              {image.category && <Badge>{image.category}</Badge>}
            </div>
            <p className="mt-0.5 text-[13px] text-subtle">{image.description || t("Deploy any image from Docker Hub, GHCR, Quay or a private registry.")}</p>
          </div>
          {image.website && (
            <a href={image.website} target="_blank" rel="noreferrer">
              <Button size="sm" variant="ghost" trailing={<ExternalLink />}>
                {t("Website")}
              </Button>
            </a>
          )}
        </div>
        {image.managed && onManaged && (
          <div className="flex flex-wrap items-center gap-3 border-t border-border bg-accent-soft/60 px-5 py-3 text-[13px]">
            <Database className="size-4 text-accent" />
            <span className="flex-1 text-muted">
              {tx("PortDock can run {name} as a {managed} with scheduled backups, restore and connection strings.", { name: image.name, managed: <span className="font-medium text-fg">{t("managed database")}</span> })}
            </span>
            <Button size="sm" variant="accent" onClick={() => onManaged(image.managed!)}>
              {t("Create managed database")}
            </Button>
          </div>
        )}
      </Card>

      <Card title={t("Container")} description={t("Image, networking and startup.")}>
        <div className="grid gap-4 p-5 md:grid-cols-2">
          <Field label={t("Image")} required hint={t("Registry path and tag, e.g. ghcr.io/acme/api:1.4.2")} className="md:col-span-2">
            <Input className="font-mono text-xs" value={f.docker_image} onChange={(e) => set({ docker_image: e.target.value })} placeholder="nginx:alpine" autoFocus={image.id === "custom"} />
          </Field>
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => set({ name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-") })} placeholder="my-service" />
          </Field>
          <Field label={t("Container port")} hint={t("Port the image listens on; receives the domain traffic.")}>
            <Input type="number" value={f.port} onChange={(e) => set({ port: Number(e.target.value) })} />
          </Field>
          <Field label={t("Command / arguments")} hint={t("Passed to the image entrypoint, like compose `command:`. Leave empty for the image default.")} className="md:col-span-2">
            <Input className="font-mono text-xs" value={f.command} onChange={(e) => set({ command: e.target.value })} placeholder={t("(image default)")} />
          </Field>
          <Field label={t("Domains")} hint={t("Optional. Comma separated; TLS certificates are issued automatically.")}>
            <Input value={f.domains} onChange={(e) => set({ domains: e.target.value })} placeholder="app.example.com" />
          </Field>
          <div className="flex items-end pb-1">
            <Switch checked={f.https} onChange={(v) => set({ https: v })} label="HTTPS" description={t("Redirect HTTP to HTTPS.")} />
          </div>
        </div>
      </Card>

      <Card
        title={t("Environment variables")}
        description={t("Passwords and secrets were generated for you. They are encrypted at rest.")}
        actions={
          <Button size="sm" icon={<Plus />} onClick={() => setEnv([...env, { key: "", value: "", secret: false }])}>
            {t("Add variable")}
          </Button>
        }
      >
        {env.length === 0 ? (
          <p className="px-5 py-6 text-[13px] text-subtle">{t("This image needs no configuration. Add variables if you want to customize it.")}</p>
        ) : (
          <div className="divide-y divide-border">
            {env.map((e, i) => (
              <div key={i} className="grid items-start gap-3 px-5 py-3 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto]">
                <Input className="font-mono text-xs" value={e.key} onChange={(ev) => setEnv(env.map((x, j) => (j === i ? { ...x, key: ev.target.value } : x)))} placeholder="KEY" />
                <div>
                  <Input
                    className={cn("font-mono text-xs", e.required && !e.value && "border-warning")}
                    type={e.secret && !reveal[i] ? "password" : "text"}
                    value={e.value}
                    onChange={(ev) => setEnv(env.map((x, j) => (j === i ? { ...x, value: ev.target.value } : x)))}
                    placeholder={e.hint ?? t("value")}
                  />
                  {e.hint && <div className="mt-1 text-2xs text-subtle">{e.hint}</div>}
                </div>
                <div className="flex items-center gap-1">
                  {e.required && <Badge tone="warning">{t("required")}</Badge>}
                  {e.secret && <Lock className="size-3.5 text-faint" />}
                  <Button size="icon-sm" variant="ghost" title={reveal[i] ? t("Hide") : t("Show")} onClick={() => setReveal({ ...reveal, [i]: !reveal[i] })}>
                    {e.secret && !reveal[i] ? <Eye /> : <EyeOff />}
                  </Button>
                  <Button size="icon-sm" variant="ghost" onClick={() => setEnv(env.filter((_, j) => j !== i))}>
                    <Trash2 className="text-danger" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
        {env.some((e) => e.value.includes("${URL}") || e.value.includes("${FQDN}")) && (
          <div className="border-t border-border px-5 py-2.5 text-xs text-subtle">
            {tx("{url} and {fqdn} are replaced with the first domain above.", { url: <code className="text-fg">${"{URL}"}</code>, fqdn: <code className="text-fg">${"{FQDN}"}</code> })}
          </div>
        )}
      </Card>

      <Card
        title={t("Persistent storage")}
        description={t("Each path becomes a Docker volume that survives redeploys.")}
        actions={
          <Button size="sm" icon={<HardDrive />} onClick={() => setVolumes([...volumes, ""])}>
            {t("Add volume")}
          </Button>
        }
      >
        {volumes.length === 0 ? (
          <p className="px-5 py-6 text-[13px] text-subtle">{t("No persistent data. The container is stateless.")}</p>
        ) : (
          <div className="divide-y divide-border">
            {volumes.map((v, i) => (
              <div key={i} className="flex items-center gap-3 px-5 py-3">
                <HardDrive className="size-4 shrink-0 text-faint" />
                <Input className="font-mono text-xs" value={v} onChange={(e) => setVolumes(volumes.map((x, j) => (j === i ? e.target.value : x)))} placeholder="/data" />
                <Button size="icon-sm" variant="ghost" onClick={() => setVolumes(volumes.filter((_, j) => j !== i))}>
                  <Trash2 className="text-danger" />
                </Button>
              </div>
            ))}
          </div>
        )}
        <div className="border-t border-border p-5">
          <Field label={t("Additional docker run options")} hint={t("Advanced, e.g. {example}", { example: "-v /var/run/docker.sock:/var/run/docker.sock --cap-add NET_ADMIN" })}>
            <Input className="font-mono text-xs" value={f.options} onChange={(e) => set({ options: e.target.value })} />
          </Field>
        </div>
      </Card>

      <div className="flex flex-wrap items-center justify-between gap-4 rounded-xl border border-border bg-surface px-5 py-4 shadow-card">
        <Switch checked={f.deploy} onChange={(v) => set({ deploy: v })} label={t("Deploy immediately")} description={missing.length ? t("Fill in {fields} first.", { fields: missing.join(", ") }) : undefined} />
        <Button variant="primary" size="lg" loading={create.isPending} disabled={!f.docker_image.trim() || (f.deploy && missing.length > 0)} onClick={() => create.mutate()}>
          {f.deploy ? t("Create & deploy") : t("Create application")}
        </Button>
      </div>
    </div>
  );
}
