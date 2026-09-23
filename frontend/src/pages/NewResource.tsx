import { useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowLeft, Boxes, Container, Database as DatabaseIcon, FileCode, GitBranch, Search } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { DatabaseDetail, Engine, Project, ServiceDetail, ServiceTemplate } from "../lib/types";
import { Badge, Button, Card, Field, Input, Loading, PageHeader, PasswordInput, Select, Textarea } from "../components/ui";
import { useServers } from "../components/shared";
import { AppForm } from "./AppForm";
import { ImageCatalogBrowser, ImageConfigure } from "../components/ImageCatalog";
import type { CatalogImage } from "../lib/types";
import { cn } from "../lib/utils";
import { t, tk } from "../lib/i18n";

type Kind = "git" | "image" | "compose" | "database" | "service";

const kinds: { id: Kind; title: string; text: string; icon: ReactNode }[] = [
  { id: "git", title: tk("Git repository"), text: tk("Import from GitHub or any Git URL. Deploy with Docker, Compose, Node, Python, Go, a shell script and more."), icon: <GitBranch /> },
  { id: "image", title: tk("Docker image"), text: tk("Browse 120+ popular images — databases, CMS, AI, monitoring, DevOps — or bring your own."), icon: <Container /> },
  { id: "compose", title: "Docker Compose", text: tk("Deploy a multi-container stack from a compose file in Git."), icon: <FileCode /> },
  { id: "database", title: tk("Database"), text: "PostgreSQL, MySQL, MariaDB, MongoDB, Redis, Valkey, ClickHouse.", icon: <DatabaseIcon /> },
  { id: "service", title: tk("One-click service"), text: tk("WordPress, n8n, Gitea, MinIO, Grafana, Umami and more."), icon: <Boxes /> },
];

export function NewResourcePage() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const nav = useNavigate();
  const project = useQuery({ queryKey: ["project", Number(id)], queryFn: () => api.get<Project>(`/projects/${id}`) });
  const servers = useServers();
  const envId = Number(params.get("env")) || project.data?.environments[0]?.id;
  const [kind, setKind] = useState<Kind | null>(null);
  const [catalogImage, setCatalogImage] = useState<CatalogImage | null>(null);
  const [engine, setEngine] = useState<string | undefined>();
  const [serverId, setServerId] = useState<number | null>(null);
  const server = serverId ?? servers.data?.[0]?.id ?? 0;

  if (project.isLoading || servers.isLoading) return <Loading />;
  const p = project.data!;

  return (
    <>
      <PageHeader
        title={t("New resource")}
        subtitle={t("in {project} / {environment}", { project: p.name, environment: p.environments.find((e) => e.id === envId)?.name ?? "" })}
        breadcrumbs={
          <>
            <Link to="/projects" className="hover:text-fg">{t("Projects")}</Link> / <Link to={`/projects/${p.id}?env=${envId}`} className="hover:text-fg">{p.name}</Link> / <span className="text-fg">{t("New")}</span>
          </>
        }
      />
      {!kind ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {kinds.map((k) => (
            <button key={k.id} onClick={() => setKind(k.id)} className="group flex flex-col items-start rounded-xl border border-border bg-surface p-5 text-left shadow-card transition-[border,box-shadow] hover:border-border-strong hover:shadow-pop">
              <div className="flex size-10 items-center justify-center rounded-lg border border-border bg-surface-2 text-fg [&>svg]:size-[18px]">{k.icon}</div>
              <div className="mt-4 font-semibold">{t(k.title)}</div>
              <p className="mt-1 text-sm text-muted">{t(k.text)}</p>
            </button>
          ))}
        </div>
      ) : (
        <div className="space-y-6">
          <button onClick={() => (setKind(null), setCatalogImage(null))} className="flex items-center gap-1.5 text-sm text-muted hover:text-fg">
            <ArrowLeft className="size-4" /> {t("Choose another type")}
          </button>
          <Card title={t("Destination server")} description={t("Where the resource will run.")}>
            <div className="p-5">
              <Select value={server} onChange={(e) => setServerId(Number(e.target.value))} className="max-w-md">
                {servers.data!.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name} {s.is_local ? t("(this machine)") : `(${s.host})`} — {s.status}
                  </option>
                ))}
              </Select>
            </div>
          </Card>
          {(kind === "git" || kind === "compose") && <AppForm kind={kind} envId={envId!} serverId={server} onCreated={(a) => nav(`/applications/${a.id}`)} />}
          {kind === "image" &&
            (catalogImage ? (
              <ImageConfigure
                key={catalogImage.id}
                image={catalogImage}
                envId={envId!}
                serverId={server}
                onBack={() => setCatalogImage(null)}
                onCreated={(a) => nav(`/applications/${a.id}`)}
                onManaged={(e) => {
                  setEngine(e);
                  setCatalogImage(null);
                  setKind("database");
                }}
              />
            ) : (
              <ImageCatalogBrowser onSelect={setCatalogImage} />
            ))}
          {kind === "database" && <DatabaseForm envId={envId!} serverId={server} initialEngine={engine} onCreated={(d) => nav(`/databases/${d.id}`)} />}
          {kind === "service" && <ServiceForm envId={envId!} serverId={server} onCreated={(s) => nav(`/services/${s.id}`)} />}
        </div>
      )}
    </>
  );
}

function DatabaseForm({ envId, serverId, onCreated, initialEngine }: { envId: number; serverId: number; onCreated: (d: DatabaseDetail) => void; initialEngine?: string }) {
  const engines = useQuery({ queryKey: ["engines"], queryFn: () => api.get<Engine[]>("/database-engines") });
  const [engine, setEngine] = useState(initialEngine ?? "postgres");
  const [f, setF] = useState({ name: "", image: "", username: "", database_name: "", password: "", public_port: 0 });
  const eng = engines.data?.find((e) => e.id === engine);
  const create = useMutation({
    mutationFn: () => api.post<DatabaseDetail>("/databases", { ...f, engine, environment_id: envId, server_id: serverId, name: f.name || engine }),
    onSuccess: (d) => {
      toast.success(t("{name} is starting", { name: d.name }));
      onCreated(d);
    },
    onError: (e) => toast.error(e.message),
  });
  if (engines.isLoading) return <Loading />;
  return (
    <Card title={t("Database")}>
      <div className="space-y-5 p-5">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
          {engines.data!.map((e) => (
            <button key={e.id} onClick={() => setEngine(e.id)} className={cn("rounded-lg border p-3 text-center transition-colors", engine === e.id ? "border-fg bg-surface-2 ring-1 ring-fg" : "border-border hover:border-border-strong")}>
              <DatabaseIcon className="mx-auto size-5 text-muted" />
              <div className="mt-2 text-sm font-medium">{e.name}</div>
            </button>
          ))}
        </div>
        <form
          className="grid gap-5 md:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <Field label={t("Name")}>
            <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder={engine} />
          </Field>
          <Field label={t("Image")} hint={t("Default: {image}", { image: eng?.default_image ?? "" })}>
            <Input value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} placeholder={eng?.default_image} />
          </Field>
          {eng?.has_user && (
            <Field label={t("Username")} hint={t("Leave empty for the default.")}>
              <Input value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} />
            </Field>
          )}
          {eng?.has_database && (
            <Field label={t("Database name")} hint={t("Derived from the name when empty.")}>
              <Input value={f.database_name} onChange={(e) => setF({ ...f, database_name: e.target.value })} />
            </Field>
          )}
          <Field label={t("Password")} hint={t("A strong password is generated when empty.")}>
            <PasswordInput value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} />
          </Field>
          <Field label={t("Public port")} hint={t("Expose on the host (0 = internal network only).")}>
            <Input type="number" value={f.public_port} onChange={(e) => setF({ ...f, public_port: Number(e.target.value) })} />
          </Field>
          <div className="md:col-span-2 flex justify-end border-t border-border pt-5">
            <Button type="submit" variant="primary" loading={create.isPending}>
              {t("Create {name}", { name: eng?.name ?? "" })}
            </Button>
          </div>
        </form>
      </div>
    </Card>
  );
}

function ServiceForm({ envId, serverId, onCreated }: { envId: number; serverId: number; onCreated: (s: ServiceDetail) => void }) {
  const tpls = useQuery({ queryKey: ["service-templates"], queryFn: () => api.get<ServiceTemplate[]>("/service-templates") });
  const [q, setQ] = useState("");
  const [tpl, setTpl] = useState<ServiceTemplate | null>(null);
  const [custom, setCustom] = useState(false);
  const [f, setF] = useState({ name: "", domain: "", compose: "" });
  const list = useMemo(() => (tpls.data ?? []).filter((t) => `${t.name} ${t.category} ${t.description}`.toLowerCase().includes(q.toLowerCase())), [tpls.data, q]);

  const create = useMutation({
    mutationFn: () =>
      api.post<ServiceDetail>("/services", {
        environment_id: envId,
        server_id: serverId,
        template: custom ? "custom" : tpl!.slug,
        name: f.name || (custom ? "stack" : tpl!.name),
        domain: f.domain,
        compose: custom ? f.compose : undefined,
      }),
    onSuccess: (s) => {
      toast.success(t("{name} is deploying", { name: s.name }));
      onCreated(s);
    },
    onError: (e) => toast.error(e.message),
  });

  if (tpls.isLoading) return <Loading />;
  if (!tpl && !custom)
    return (
      <Card
        title={t("Choose a service")}
        actions={
          <Button size="sm" onClick={() => setCustom(true)} icon={<FileCode className="size-3.5" />}>
            {t("Custom compose")}
          </Button>
        }
      >
        <div className="p-5">
          <div className="relative mb-4 max-w-sm">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" />
            <Input className="pl-9" placeholder={t("Search services…")} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {list.map((t) => (
              <button key={t.slug} onClick={() => setTpl(t)} className="flex flex-col items-stretch rounded-lg border border-border bg-surface p-4 text-left transition-colors hover:border-border-strong hover:bg-surface-2/50">
                <div className="flex items-center justify-between">
                  <span className="font-medium">{t.name}</span>
                  <Badge>{t.category}</Badge>
                </div>
                <p className="mt-1.5 text-sm text-muted">{t.description}</p>
              </button>
            ))}
          </div>
        </div>
      </Card>
    );

  return (
    <Card
      title={custom ? t("Custom Docker Compose") : tpl!.name}
      description={custom ? t("Add {code} to choose which service receives the domain.", { code: "`x-portdock: {service, port}`" }) : tpl!.description}
      actions={
        <Button size="sm" variant="ghost" onClick={() => (setTpl(null), setCustom(false))}>
          {t("Change")}
        </Button>
      }
    >
      <form
        className="grid gap-5 p-5 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <Field label={t("Name")}>
          <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder={custom ? "stack" : tpl!.name} />
        </Field>
        <Field label={t("Domain")} hint={t("Credentials and URLs in the template are generated for this domain.")}>
          <Input value={f.domain} onChange={(e) => setF({ ...f, domain: e.target.value })} placeholder="service.example.com" />
        </Field>
        <div className="md:col-span-2">
          <Field label="docker-compose.yml">
            {custom ? (
              <Textarea rows={16} value={f.compose} onChange={(e) => setF({ ...f, compose: e.target.value })} placeholder={"x-portdock:\n  service: web\n  port: 80\nservices:\n  web:\n    image: nginx:alpine"} />
            ) : (
              <Textarea rows={14} value={tpl!.compose} readOnly className="opacity-80" />
            )}
          </Field>
        </div>
        <div className="md:col-span-2 flex justify-end border-t border-border pt-5">
          <Button type="submit" variant="primary" loading={create.isPending}>
            {t("Deploy service")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
