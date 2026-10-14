import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Save, ShieldCheck, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../lib/api";
import type { ApplicationDetail } from "../lib/types";
import { t } from "../lib/i18n";
import { Button, Card, ErrorBox, Field, Input, Loading, PasswordInput, Select, Textarea } from "./ui";

type Protection = {
  auth: "" | "password" | "sso";
  sso_access: "project" | "any";
  sso_emails: string;
  ip_allow: string;
  ip_depth: number;
  rate_average: number;
  rate_burst: number;
  users: string[];
};

type UserRow = { user: string; password: string; stored: boolean };

/** Domains tab: who may reach the application through the proxy. */
export function ProtectionCard({ app }: { app: ApplicationDetail }) {
  const qc = useQueryClient();
  const key = ["protection", app.id];
  const q = useQuery({ queryKey: key, queryFn: () => api.get<Protection>(`/applications/${app.id}/protection`) });
  const [f, setF] = useState<Omit<Protection, "users">>({ auth: "", sso_access: "project", sso_emails: "", ip_allow: "", ip_depth: 0, rate_average: 0, rate_burst: 0 });
  const [users, setUsers] = useState<UserRow[]>([]);
  useEffect(() => {
    if (!q.data) return;
    const { users: names, ...rest } = q.data;
    setF(rest);
    setUsers((names ?? []).map((u) => ({ user: u, password: "", stored: true })));
  }, [q.data]);
  const save = useMutation({
    mutationFn: () => api.put<{ protection: Protection; routing_warning?: string }>(`/applications/${app.id}/protection`, { ...f, users: users.map((u) => ({ user: u.user, password: u.password })) }),
    onSuccess: (r) => {
      qc.setQueryData(key, r.protection);
      if (r.routing_warning) toast.warning(r.routing_warning);
      else toast.success(t("Access protection saved"), { description: t("The proxy applies it within seconds — no redeploy needed.") });
    },
    onError: (e) => toast.error(e.message),
  });
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const readOnly = !app.access.can_write;
  const setUser = (i: number, patch: Partial<UserRow>) => setUsers(users.map((u, j) => (j === i ? { ...u, ...patch } : u)));

  return (
    <Card
      title={
        <span className="flex items-center gap-2">
          <ShieldCheck className="size-4" />
          {t("Access protection")}
        </span>
      }
      description={t("Restrict who reaches this application, on all its domains and previews. Applied by the proxy before any request reaches the application.")}
    >
      <fieldset disabled={readOnly} className="grid gap-5 p-5 md:grid-cols-2">
        <Field
          label={t("Sign-in")}
          className="md:col-span-2"
          hint={
            f.auth === "sso"
              ? t("Visitors sign in with PortDock (and your single sign-on provider, when configured). The application receives X-Forwarded-User, X-Forwarded-Email and X-Forwarded-Name; /_portdock/logout signs out.")
              : f.auth === "password"
                ? t("Browsers ask for a user name and password (HTTP basic authentication). Use HTTPS.")
                : t("Anyone who knows the address can open the application.")
          }
        >
          <Select value={f.auth} onChange={(e) => setF({ ...f, auth: e.target.value as Protection["auth"] })}>
            <option value="">{t("None")}</option>
            <option value="sso">{t("Single sign-on with PortDock")}</option>
            <option value="password">{t("Password (basic authentication)")}</option>
          </Select>
        </Field>
        {f.auth === "sso" && (
          <>
            <Field label={t("Who may sign in")}>
              <Select value={f.sso_access} onChange={(e) => setF({ ...f, sso_access: e.target.value as Protection["sso_access"] })}>
                <option value="project">{t("People with access to the project")}</option>
                <option value="any">{t("Every PortDock user")}</option>
              </Select>
            </Field>
            <Field label={t("Only these e-mails (optional)")} hint={t("One per line: an address or a whole domain, e.g. @company.com.")}>
              <Textarea rows={3} value={f.sso_emails} onChange={(e) => setF({ ...f, sso_emails: e.target.value })} placeholder={"@company.com\nauditor@partner.com"} />
            </Field>
          </>
        )}
        {f.auth === "password" && (
          <div className="space-y-2 md:col-span-2">
            <div className="text-[13px] font-medium">{t("Users")}</div>
            {users.map((u, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <Input className="w-48" value={u.user} disabled={u.stored} onChange={(e) => setUser(i, { user: e.target.value })} placeholder={t("user name")} />
                <PasswordInput
                  className="w-64"
                  value={u.password}
                  onChange={(e) => setUser(i, { password: e.target.value })}
                  placeholder={u.stored ? t("unchanged") : t("password (8+ characters)")}
                  autoComplete="new-password"
                />
                <Button size="icon-sm" variant="ghost" title={t("Remove")} aria-label={t("Remove")} onClick={() => setUsers(users.filter((_, j) => j !== i))}>
                  <Trash2 className="text-danger" />
                </Button>
              </div>
            ))}
            <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setUsers([...users, { user: "", password: "", stored: false }])}>
              {t("Add user")}
            </Button>
          </div>
        )}
        <Field label={t("Allowed addresses")} hint={t("One IP address or CIDR range per line, e.g. 10.0.0.0/8 for the office VPN. Empty: every address.")}>
          <Textarea rows={3} className="font-mono" value={f.ip_allow} onChange={(e) => setF({ ...f, ip_allow: e.target.value })} placeholder={"10.0.0.0/8\n203.0.113.7"} />
        </Field>
        <Field label={t("Client address")} hint={t("Behind a CDN or load balancer, the visitor's address comes from X-Forwarded-For.")}>
          <Select value={f.ip_depth} onChange={(e) => setF({ ...f, ip_depth: Number(e.target.value) })}>
            <option value={0}>{t("The connection (direct traffic)")}</option>
            <option value={1}>{t("Behind one proxy or CDN")}</option>
            <option value={2}>{t("Behind two proxies")}</option>
          </Select>
        </Field>
        <Field label={t("Rate limit")} hint={t("Requests per second per visitor address; 0 turns it off. Excess requests get 429 Too Many Requests.")}>
          <Input type="number" min={0} value={f.rate_average} onChange={(e) => setF({ ...f, rate_average: Number(e.target.value) })} />
        </Field>
        <Field label={t("Burst")} hint={t("Requests allowed at once above the rate (at least the rate).")}>
          <Input type="number" min={0} value={f.rate_burst} onChange={(e) => setF({ ...f, rate_burst: Number(e.target.value) })} />
        </Field>
        {!readOnly && (
          <div className="flex justify-end md:col-span-2">
            <Button variant="primary" icon={<Save className="size-4" />} loading={save.isPending} onClick={() => save.mutate()}>
              {t("Save")}
            </Button>
          </div>
        )}
      </fieldset>
    </Card>
  );
}
