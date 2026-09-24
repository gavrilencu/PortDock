import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderKanban, LayoutGrid, List, Plus, Search } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { Project } from "../lib/types";
import { Badge, Button, Empty, ErrorBox, Field, Input, Loading, Modal, PageHeader, SegmentedControl, Table } from "../components/ui";
import { timeAgo } from "../lib/utils";
import { t } from "../lib/i18n";

function useViewMode(key: string) {
  const [mode, setMode] = useState<"grid" | "list">(() => {
    try {
      return localStorage.getItem(key) === "list" ? "list" : "grid";
    } catch {
      return "grid";
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(key, mode);
    } catch {
      /* ignore */
    }
  }, [key, mode]);
  return [mode, setMode] as const;
}

export function ProjectsPage() {
  const qc = useQueryClient();
  const nav = useNavigate();
  const [params, setParams] = useSearchParams();
  const { data, isLoading, error } = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
  const [q, setQ] = useState("");
  const [mode, setMode] = useViewMode("portdock.projects.view");
  const open = params.get("new") === "1";
  const setOpen = (v: boolean) => setParams(v ? { new: "1" } : {});
  const [form, setForm] = useState({ name: "", description: "" });

  const create = useMutation({
    mutationFn: () => api.post<Project>("/projects", form),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      toast.success(t("Project “{name}” created", { name: p.name }));
      nav(`/projects/${p.id}`);
    },
    onError: (e) => toast.error(e.message),
  });

  const list = useMemo(() => (data ?? []).filter((p) => `${p.name} ${p.description}`.toLowerCase().includes(q.toLowerCase())), [data, q]);

  return (
    <>
      <PageHeader
        title={t("Projects")}
        subtitle={t("Group applications, databases and services into environments.")}
        actions={
          <Button variant="primary" icon={<Plus />} onClick={() => setOpen(true)}>
            {t("New project")}
          </Button>
        }
      />
      {isLoading ? (
        <Loading />
      ) : error ? (
        <ErrorBox error={error} />
      ) : data!.length === 0 ? (
        <Empty
          icon={<FolderKanban />}
          title={t("Create your first project")}
          description={t("A project holds the resources of one product or team, separated into environments like production and staging.")}
          action={
            <Button variant="primary" icon={<Plus />} onClick={() => setOpen(true)}>
              {t("New project")}
            </Button>
          }
        />
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <div className="relative w-full max-w-xs">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
              <Input className="pl-8" placeholder={t("Filter projects…")} value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
            <span className="text-xs text-subtle">
              {t("{shown} of {total}", { shown: list.length, total: data!.length })}
            </span>
            <SegmentedControl
              className="ml-auto"
              value={mode}
              onChange={setMode}
              options={[
                { value: "grid", label: <LayoutGrid className="size-3.5" /> },
                { value: "list", label: <List className="size-3.5" /> },
              ]}
            />
          </div>
          {list.length === 0 ? (
            <Empty compact title={t("No matching projects")} description={t("Nothing matches “{query}”.", { query: q })} />
          ) : mode === "grid" ? (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {list.map((p) => (
                <Link key={p.id} to={`/projects/${p.id}`} className="group flex flex-col rounded-xl border border-border bg-surface p-5 shadow-card transition-[border,box-shadow] hover:border-border-strong hover:shadow-pop">
                  <div className="flex items-start gap-3">
                    <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-surface-2 text-sm font-semibold uppercase text-muted">{p.name.slice(0, 2)}</div>
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-semibold tracking-[-0.01em]">{p.name}</div>
                      <p className="mt-0.5 line-clamp-2 min-h-9 text-[13px] leading-relaxed text-subtle">{p.description || t("No description")}</p>
                    </div>
                  </div>
                  <div className="mt-4 flex flex-wrap gap-1.5">
                    {(p.environments ?? []).map((e) => (
                      <Badge key={e.id} tone={e.name === "production" ? "accent" : "neutral"}>
                        {e.name}
                      </Badge>
                    ))}
                  </div>
                  <div className="mt-4 flex items-center justify-between border-t border-border pt-3 text-xs text-subtle">
                    <span>
                      {t("{n} resource|{n} resources", { n: p.resources })}
                    </span>
                    <span>{t("Created {time}", { time: timeAgo(p.created_at) })}</span>
                  </div>
                </Link>
              ))}
            </div>
          ) : (
            <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
              <Table>
                <thead>
                  <tr>
                    <th>{t("Project")}</th>
                    <th>{t("Environments")}</th>
                    <th className="!text-right">{t("Resources")}</th>
                    <th className="!text-right">{t("Created")}</th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((p) => (
                    <tr key={p.id} className="cursor-pointer" onClick={() => nav(`/projects/${p.id}`)}>
                      <td>
                        <div className="font-medium">{p.name}</div>
                        <div className="max-w-md truncate text-xs text-subtle">{p.description || t("No description")}</div>
                      </td>
                      <td>
                        <div className="flex flex-wrap gap-1.5">
                          {(p.environments ?? []).map((e) => (
                            <Badge key={e.id}>{e.name}</Badge>
                          ))}
                        </div>
                      </td>
                      <td className="text-right">{p.resources}</td>
                      <td className="text-right text-subtle">{timeAgo(p.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          )}
        </>
      )}
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={t("Create project")}
        description={t("A production environment is created automatically. You can add staging or preview environments later.")}
        footer={
          <>
            <Button onClick={() => setOpen(false)}>{t("Cancel")}</Button>
            <Button variant="primary" loading={create.isPending} disabled={!form.name} onClick={() => create.mutate()}>
              {t("Create project")}
            </Button>
          </>
        }
      >
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (form.name) create.mutate();
          }}
        >
          <Field label={t("Project name")} required>
            <Input autoFocus value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t("e.g. Payments Platform")} />
          </Field>
          <Field label={t("Description")} hint={t("Optional. Helps teammates understand what this project contains.")}>
            <Input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} placeholder={t("Checkout, billing and ledger services")} />
          </Field>
        </form>
      </Modal>
    </>
  );
}
