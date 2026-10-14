import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, BookOpen, ExternalLink, FileUp, Info, Library, Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ServiceTemplate, TemplateCatalog, TemplateIssue } from "../lib/types";
import { t } from "../lib/i18n";
import { timeAgo } from "../lib/utils";
import { Badge, Button, Card, Empty, Field, Input, Loading, Modal, Select, Switch, Table, Textarea, useConfirm } from "./ui";

// The service template picker of New resource → Service, and the catalogs
// administrators add (Settings → Service catalogs).

export const COMMUNITY_CATALOG = "https://raw.githubusercontent.com/gavrilencu/PortDock/main/backend/internal/templates/services/catalog.json";
export const COOLIFY_CATALOG = "https://raw.githubusercontent.com/coollabsio/coolify/v4.x/templates/service-templates.json";

/** TemplateLogo shows a template's logo, or its initials. */
export function TemplateLogo({ tpl, size = "size-9" }: { tpl: Pick<ServiceTemplate, "name" | "logo">; size?: string }) {
  const [broken, setBroken] = useState(false);
  if (tpl.logo && !broken)
    return <img src={tpl.logo} alt="" loading="lazy" referrerPolicy="no-referrer" onError={() => setBroken(true)} className={`${size} shrink-0 rounded-md bg-surface-2 object-contain p-1`} />;
  const initials = tpl.name.replace(/[^A-Za-z0-9 ]/g, "").split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]).join("").toUpperCase() || "?";
  return <span className={`${size} flex shrink-0 items-center justify-center rounded-md bg-surface-2 text-xs font-semibold text-muted`}>{initials}</span>;
}

/** TemplatePicker lists the templates, with search and filters by category and catalog. */
export function TemplatePicker({ onPick }: { onPick: (tpl: ServiceTemplate) => void }) {
  const tpls = useQuery({ queryKey: ["service-templates"], queryFn: () => api.get<ServiceTemplate[]>("/service-templates") });
  const [q, setQ] = useState("");
  const [category, setCategory] = useState("");
  const [source, setSource] = useState("");
  const all = tpls.data ?? [];
  const categories = useMemo(() => [...new Set(all.map((x) => x.category))].sort(), [all]);
  const sources = useMemo(() => [...new Set(all.map((x) => x.source ?? ""))], [all]);
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return all.filter(
      (x) =>
        (!category || x.category === category) &&
        (source === "" || (x.source ?? "-") === source) &&
        (!needle || `${x.name} ${x.category} ${x.description} ${(x.tags ?? []).join(" ")}`.toLowerCase().includes(needle)),
    );
  }, [all, q, category, source]);
  if (tpls.isLoading) return <Loading />;
  const shown = list.slice(0, 120);
  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="relative w-full max-w-sm">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" />
          <Input className="pl-9" placeholder={t("Search {n} services…", { n: all.length })} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Select className="w-56" value={category} onChange={(e) => setCategory(e.target.value)}>
          <option value="">{t("All categories")}</option>
          {categories.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </Select>
        {sources.length > 1 && (
          <Select className="w-48" value={source} onChange={(e) => setSource(e.target.value)}>
            <option value="">{t("All catalogs")}</option>
            <option value="-">{t("Built in")}</option>
            {sources
              .filter(Boolean)
              .map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
          </Select>
        )}
      </div>
      {shown.length === 0 ? (
        <Empty compact icon={<Search />} title={t("No service matches")} description={t("Try another word, or write a custom compose file.")} />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {shown.map((x) => (
            <button key={x.key} onClick={() => onPick(x)} className="flex flex-col items-stretch rounded-lg border border-border bg-surface p-4 text-left transition-colors hover:border-border-strong hover:bg-surface-2/50">
              <div className="flex items-start gap-3">
                <TemplateLogo tpl={x} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-medium">{x.name}</span>
                    {x.host_access && (
                      <span title={t("Needs host access")}>
                        <AlertTriangle className="size-3.5 text-warning" />
                      </span>
                    )}
                  </div>
                  <div className="mt-0.5 flex flex-wrap gap-1">
                    <Badge>{x.category}</Badge>
                    {x.source && <Badge tone="info">{x.source}</Badge>}
                  </div>
                </div>
              </div>
              <p className="mt-2 line-clamp-3 text-sm text-muted">{x.description}</p>
            </button>
          ))}
        </div>
      )}
      {list.length > shown.length && <p className="mt-3 text-xs text-muted">{t("{n} more: refine the search.", { n: list.length - shown.length })}</p>}
    </div>
  );
}

/** useTemplateDetail loads a template's compose file and what the linter says. */
export function useTemplateDetail(key?: string) {
  return useQuery({
    queryKey: ["service-template", key],
    queryFn: () => api.get<{ template: ServiceTemplate; issues: TemplateIssue[] }>(`/service-templates/${key}`),
    enabled: !!key,
  });
}

/** TemplateNotes shows what to know before deploying a template. */
export function TemplateNotes({ tpl, issues }: { tpl: ServiceTemplate; issues: TemplateIssue[] }) {
  const errors = issues.filter((i) => i.error && !i.text.startsWith("needs host access"));
  const notes = tpl.notes ?? [];
  if (!tpl.source && !tpl.host_access && !(tpl.required ?? []).length && !notes.length && !errors.length && tpl.service) return null;
  return (
    <div className="space-y-2 rounded-lg border border-border bg-surface-2/40 p-3 text-xs md:col-span-2">
      {tpl.source && (
        <p className="flex items-start gap-2 text-muted">
          <Library className="mt-0.5 size-3.5 shrink-0" />
          {t("From the {source} catalog: review the compose file before deploying it.", { source: tpl.source })}
        </p>
      )}
      {!tpl.service && (
        <p className="flex items-start gap-2 text-muted">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          {t("No public address: the service runs without a domain.")}
        </p>
      )}
      {tpl.host_access && (
        <p className="flex items-start gap-2 text-warning">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
          {t("The compose file needs host access (server paths, privileged mode, devices…), which only an administrator can allow. A template that mounts the Docker socket cannot be deployed.")}
        </p>
      )}
      {(tpl.required ?? []).length > 0 && (
        <p className="flex items-start gap-2 text-muted">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          {t("Set these variables in the service's Environment tab before deploying it: {names}. The service is created without starting.", { names: tpl.required!.join(", ") })}
        </p>
      )}
      {[...notes, ...errors.map((e) => e.text)].map((n) => (
        <p key={n} className="flex items-start gap-2 text-muted">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          {n}
        </p>
      ))}
    </div>
  );
}

// ---- Settings → Service catalogs ----

type CatalogForm = { id?: number; name: string; url: string; content: string; format: TemplateCatalog["format"]; mode: "url" | "upload" };

function CatalogModal({ initial, onClose }: { initial: CatalogForm | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<CatalogForm>({ name: "", url: "", content: "", format: "auto", mode: "url" });
  useEffect(() => {
    if (initial) setF(initial);
  }, [initial]);
  const editing = !!f.id;
  const save = useMutation({
    mutationFn: () => {
      const body = { name: f.name, format: f.format, url: f.mode === "url" ? f.url : "", content: f.mode === "upload" ? f.content : "" };
      return editing ? api.patch<TemplateCatalog>(`/template-catalogs/${f.id}`, body) : api.post<TemplateCatalog>("/template-catalogs", body);
    },
    onSuccess: (c) => {
      qc.invalidateQueries({ queryKey: ["template-catalogs"] });
      qc.invalidateQueries({ queryKey: ["service-templates"] });
      toast.success(t("{n} template from {name}|{n} templates from {name}", { n: c.count, name: c.name }), c.skipped.length ? { description: t("{n} skipped", { n: c.skipped.length }) } : undefined);
      onClose();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="lg"
      title={editing ? t("Edit {name}", { name: f.name }) : t("Add a service catalog")}
      description={t("A catalog brings more one-click services: the PortDock community catalog, Coolify's templates (converted automatically) or your own, from a URL or a file.")}
      footer={
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          {editing ? t("Save changes") : t("Add catalog")}
        </Button>
      }
    >
      <div className="grid gap-4 md:grid-cols-2">
        {!editing && (
          <div className="flex flex-wrap gap-2 md:col-span-2">
            <Button size="sm" icon={<BookOpen />} onClick={() => setF({ ...f, name: "community", url: COMMUNITY_CATALOG, format: "portdock", mode: "url" })}>
              {t("PortDock community catalog")}
            </Button>
            <Button size="sm" icon={<Library />} onClick={() => setF({ ...f, name: "coolify", url: COOLIFY_CATALOG, format: "coolify", mode: "url" })}>
              {t("Coolify templates")}
            </Button>
          </div>
        )}
        <Field label={t("Name")} hint={t("Prefixes the templates' keys, e.g. community/n8n.")}>
          <Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-") })} placeholder="community" className="font-mono text-xs" />
        </Field>
        <Field label={t("Format")}>
          <Select value={f.format} onChange={(e) => setF({ ...f, format: e.target.value as CatalogForm["format"] })}>
            <option value="auto">{t("Detect")}</option>
            <option value="portdock">PortDock</option>
            <option value="coolify">Coolify (service-templates.json)</option>
          </Select>
        </Field>
        <Field label={t("Source")} className="md:col-span-2">
          <Select value={f.mode} onChange={(e) => setF({ ...f, mode: e.target.value as CatalogForm["mode"] })} className="w-56">
            <option value="url">{t("A URL, refreshed daily")}</option>
            <option value="upload">{t("An uploaded file")}</option>
          </Select>
        </Field>
        {f.mode === "url" ? (
          <Field label="URL" hint={t("Downloaded through the corporate proxy when one is set; an internal mirror (Nexus, Artifactory, Git server) works for networks without internet.")} className="md:col-span-2">
            <Input value={f.url} onChange={(e) => setF({ ...f, url: e.target.value })} placeholder="https://…/catalog.json" className="font-mono text-xs" />
          </Field>
        ) : (
          <Field label={t("Catalog file")} hint={t("A catalog (pdctl template bundle), a single template, or Coolify's service-templates.json.")} className="md:col-span-2">
            <div className="space-y-2">
              <label className="inline-flex cursor-pointer items-center gap-2 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-surface-2">
                <FileUp className="size-3.5" />
                {t("Choose a file")}
                <input
                  type="file"
                  accept=".json,.yml,.yaml"
                  className="hidden"
                  onChange={async (e) => {
                    const file = e.target.files?.[0];
                    if (file) setF({ ...f, content: await file.text(), name: f.name || file.name.replace(/\.[^.]+$/, "").toLowerCase().replace(/[^a-z0-9-]/g, "-") });
                  }}
                />
              </label>
              <Textarea value={f.content} onChange={(e) => setF({ ...f, content: e.target.value })} rows={5} className="font-mono text-2xs" placeholder='{"version": 1, "templates": [...]}' />
            </div>
          </Field>
        )}
        <p className="text-xs text-muted md:col-span-2">
          {t("Templates are compose files written by others: they deploy like a custom compose file, without host access unless an administrator allows it, and the Docker socket is never mounted. Review a template before deploying it.")}
        </p>
      </div>
    </Modal>
  );
}

export function CatalogsSettings() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [edit, setEdit] = useState<CatalogForm | null>(null);
  const [skippedOf, setSkippedOf] = useState<TemplateCatalog | null>(null);
  const cats = useQuery({ queryKey: ["template-catalogs"], queryFn: () => api.get<TemplateCatalog[]>("/template-catalogs") });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["template-catalogs"] });
    qc.invalidateQueries({ queryKey: ["service-templates"] });
  };
  const refresh = useMutation({
    mutationFn: (id: number) => api.post<TemplateCatalog>(`/template-catalogs/${id}/refresh`),
    onSuccess: (c) => {
      invalidate();
      toast.success(t("{n} template from {name}|{n} templates from {name}", { n: c.count, name: c.name }));
    },
    onError: (e) => {
      invalidate();
      toast.error(e.message);
    },
  });
  const toggle = useMutation({
    mutationFn: (c: TemplateCatalog) => api.patch(`/template-catalogs/${c.id}`, { enabled: !c.enabled }),
    onSuccess: invalidate,
    onError: (e) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/template-catalogs/${id}`),
    onSuccess: () => {
      invalidate();
      toast.success(t("Catalog removed"));
    },
    onError: (e) => toast.error(e.message),
  });
  if (cats.isLoading) return <Loading />;
  const list = cats.data ?? [];
  const add = () => setEdit({ name: "", url: "", content: "", format: "auto", mode: "url" });
  return (
    <div className="space-y-6">
      <Card
        title={t("Service catalogs")}
        description={t("More one-click services on top of the built-in ones. Catalogs from a URL are refreshed every day, so new templates arrive without updating PortDock.")}
        actions={
          list.length > 0 && (
            <Button size="sm" variant="primary" icon={<Plus />} onClick={add}>
              {t("Add catalog")}
            </Button>
          )
        }
      >
        {list.length === 0 ? (
          <div className="p-5">
            <Empty
              compact
              icon={<Library />}
              title={t("No catalogs")}
              description={t("Add the PortDock community catalog, Coolify's 300+ templates or a catalog of your own.")}
              action={
                <Button variant="primary" icon={<Plus />} onClick={add}>
                  {t("Add catalog")}
                </Button>
              }
            />
          </div>
        ) : (
          <Table>
            <thead>
              <tr>
                <th>{t("Catalog")}</th>
                <th>{t("Templates")}</th>
                <th>{t("Updated")}</th>
                <th>{t("Enabled")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((c) => (
                <tr key={c.id}>
                  <td>
                    <div className="font-mono text-[13px] font-medium">{c.name}</div>
                    <div className="max-w-md truncate font-mono text-2xs text-muted">{c.upload ? t("uploaded file") : c.url}</div>
                  </td>
                  <td className="text-xs">
                    <span className="tabular-nums">{c.count}</span>
                    {c.skipped.length > 0 && (
                      <button className="ml-2 text-muted underline-offset-2 hover:text-fg hover:underline" onClick={() => setSkippedOf(c)}>
                        {t("{n} skipped", { n: c.skipped.length })}
                      </button>
                    )}
                  </td>
                  <td className="text-xs">
                    {c.fetch_error ? (
                      <Badge tone="danger" className="max-w-56">
                        <span className="truncate" title={c.fetch_error}>
                          {c.fetch_error}
                        </span>
                      </Badge>
                    ) : (
                      <span className="text-muted">{c.fetched_at ? timeAgo(c.fetched_at) : "—"}</span>
                    )}
                  </td>
                  <td>
                    <Switch checked={c.enabled} onChange={() => toggle.mutate(c)} />
                  </td>
                  <td className="text-right">
                    <div className="flex justify-end gap-1">
                      {!c.upload && (
                        <Button size="sm" variant="ghost" icon={<RefreshCw />} loading={refresh.isPending && refresh.variables === c.id} onClick={() => refresh.mutate(c.id)} aria-label={t("Refresh")} />
                      )}
                      {!c.upload && (
                        <a href={c.url} target="_blank" rel="noreferrer" className="inline-flex size-8 items-center justify-center rounded-md text-muted hover:bg-surface-2 hover:text-fg" aria-label={t("Open")}>
                          <ExternalLink className="size-4" />
                        </a>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={<Trash2 className="text-danger" />}
                        aria-label={t("Delete")}
                        onClick={async () => {
                          if (await confirm({ title: t("Remove {name}?", { name: c.name }), description: t("Its templates leave the picker. Services already created from them keep running."), danger: true, confirmText: t("Remove") })) del.mutate(c.id);
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
      <Card title={t("Write a template")}>
        <div className="space-y-2 p-5 text-xs text-muted">
          <p>{t("A template is a docker-compose file whose x-portdock block describes it and names the public service. Placeholders such as ${PORTDOCK_PASSWORD_DB} or ${PORTDOCK_URL} are filled in when a service is created.")}</p>
          <pre className="overflow-x-auto rounded-lg border border-border bg-surface-2/60 p-3 font-mono text-2xs leading-relaxed text-fg">{`x-portdock:
  name: Uptime Kuma
  description: Self-hosted uptime monitoring with status pages.
  category: Monitoring & Observability
  website: https://uptime.kuma.pet
  tags: [monitoring, uptime]
  service: uptime-kuma
  port: 3001
services:
  uptime-kuma:
    image: louislam/uptime-kuma:2
    restart: unless-stopped
    volumes:
      - data:/app/data
volumes:
  data:`}</pre>
          <p>{t("Check it with pdctl template lint, bundle a directory into a catalog with pdctl template bundle, and propose it to the community catalog with a pull request (backend/internal/templates/services).")}</p>
        </div>
      </Card>
      <CatalogModal initial={edit} onClose={() => setEdit(null)} />
      <Modal open={!!skippedOf} onClose={() => setSkippedOf(null)} size="lg" title={t("Skipped templates of {name}", { name: skippedOf?.name ?? "" })}>
        <div className="max-h-96 space-y-1.5 overflow-y-auto text-xs">
          {(skippedOf?.skipped ?? []).map((s) => (
            <div key={s.slug}>
              <span className="font-mono font-medium">{s.slug}</span> <span className="text-muted">{s.reason}</span>
            </div>
          ))}
        </div>
      </Modal>
    </div>
  );
}
