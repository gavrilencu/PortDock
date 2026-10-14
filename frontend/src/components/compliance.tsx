import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download, FileText } from "lucide-react";
import { api } from "../lib/api";
import type { Project } from "../lib/types";
import { t } from "../lib/i18n";
import { Button, Card, Field, Input, Select } from "./ui";

// Settings → Compliance: the report auditors ask for, for a period.

const iso = (d: Date) => d.toISOString().slice(0, 10);

function preset(kind: string): { from: string; to: string } {
  const now = new Date();
  const today = iso(now);
  switch (kind) {
    case "quarter": {
      const q = Math.floor(now.getUTCMonth() / 3);
      const start = new Date(Date.UTC(now.getUTCFullYear(), (q - 1) * 3, 1));
      const end = new Date(Date.UTC(now.getUTCFullYear(), q * 3, 0));
      return { from: iso(start), to: iso(end) };
    }
    case "year":
      return { from: `${now.getUTCFullYear() - 1}-01-01`, to: `${now.getUTCFullYear() - 1}-12-31` };
    case "ytd":
      return { from: `${now.getUTCFullYear()}-01-01`, to: today };
    default: {
      const d = new Date(now);
      d.setUTCDate(d.getUTCDate() - 30);
      return { from: iso(d), to: today };
    }
  }
}

export function ComplianceReport() {
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => api.get<Project[]>("/projects") });
  const [period, setPeriod] = useState(preset("30d"));
  const [kind, setKind] = useState("30d");
  const [project, setProject] = useState("0");
  const query = useMemo(() => `from=${period.from}&to=${period.to}${project !== "0" ? `&project=${project}` : ""}`, [period, project]);
  return (
    <Card
      title={t("Compliance report")}
      description={t("The evidence auditors ask for, for a period: accounts and two-factor authentication, API tokens, protected environments, deployments with their approvals, security scans, backups with their test restores, point-in-time recovery, incidents, alerts and the audit log.")}
    >
      <div className="grid gap-4 p-5 md:grid-cols-4">
        <Field label={t("Period")}>
          <Select
            value={kind}
            onChange={(e) => {
              setKind(e.target.value);
              if (e.target.value !== "custom") setPeriod(preset(e.target.value));
            }}
          >
            <option value="30d">{t("Last 30 days")}</option>
            <option value="quarter">{t("Last quarter")}</option>
            <option value="ytd">{t("This year so far")}</option>
            <option value="year">{t("Last year")}</option>
            <option value="custom">{t("Custom")}</option>
          </Select>
        </Field>
        <Field label={t("From")}>
          <Input type="date" value={period.from} onChange={(e) => (setKind("custom"), setPeriod({ ...period, from: e.target.value }))} />
        </Field>
        <Field label={t("To")}>
          <Input type="date" value={period.to} onChange={(e) => (setKind("custom"), setPeriod({ ...period, to: e.target.value }))} />
        </Field>
        <Field label={t("Scope")}>
          <Select value={project} onChange={(e) => setProject(e.target.value)}>
            <option value="0">{t("All projects (with the audit log)")}</option>
            {(projects.data ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </Select>
        </Field>
        <div className="flex flex-wrap items-center gap-2 md:col-span-4">
          <a href={`/api/compliance/report?${query}`} target="_blank" rel="noreferrer">
            <Button variant="primary" icon={<FileText className="size-4" />}>
              {t("Open the report")}
            </Button>
          </a>
          <a href={`/api/compliance/report?${query}&format=zip`} download>
            <Button icon={<Download className="size-4" />}>{t("Download ZIP (HTML, CSV, JSON, checksums)")}</Button>
          </a>
          <span className="text-xs text-muted">{t("The report opens in a new tab: print it or save it as PDF. Every export is written to the audit log.")}</span>
        </div>
      </div>
    </Card>
  );
}
