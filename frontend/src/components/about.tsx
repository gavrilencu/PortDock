import type { ReactNode } from "react";
import {
  Activity,
  ArrowUpCircle,
  CheckCircle2,
  Database,
  ExternalLink,
  Globe,
  History,
  KeyRound,
  Rocket,
  Server,
  Settings2,
  ShieldCheck,
} from "lucide-react";
import { t, tk } from "../lib/i18n";
import { DEVELOPER } from "./credits";
import { LogoMark, useAuthStatus } from "./Layout";
import { useUpdateInfo } from "./updates";
import { Badge, Card } from "./ui";

// Everything PortDock can do, in a few words per line.
const features: { icon: ReactNode; title: string; items: string[] }[] = [
  {
    icon: <Rocket />,
    title: tk("Deployments"),
    items: [
      tk("From GitHub, Azure DevOps, GitLab, Gitea or any Git URL, Docker images and Docker Compose"),
      tk("Build packs: Dockerfile, Nixpacks, Node.js, Bun, Python, Go, static sites, shell scripts"),
      tk("Push-to-deploy, pull request previews, zero-downtime rollouts with health checks"),
      tk("Several servers per application, dedicated build servers"),
    ],
  },
  {
    icon: <History />,
    title: tk("Versions"),
    items: [
      tk("The live version and the last 5 are kept for 15 days"),
      tk("Roll back by drag and drop, without a rebuild"),
      tk("Each Docker Compose service keeps its own images"),
    ],
  },
  {
    icon: <Globe />,
    title: tk("Domains and HTTPS"),
    items: [
      tk("Custom domains with automatic Let's Encrypt certificates"),
      tk("Wildcards, DNS challenge, your own certificates, www redirects"),
      tk("A domain per Docker Compose service, applied without a redeploy"),
      tk("Automatic renewal and expiry warnings"),
    ],
  },
  {
    icon: <Database />,
    title: tk("Databases and services"),
    items: [
      tk("PostgreSQL, MySQL, MariaDB, Microsoft SQL Server, MongoDB, Redis, Valkey, ClickHouse"),
      tk("Scheduled backups, locally or to S3"),
      tk("Access from outside by domain"),
      tk("SQL console in the browser, read-only for members"),
      tk("13 one-click services and a catalog of 127 Docker images"),
    ],
  },
  {
    icon: <KeyRound />,
    title: tk("Configuration"),
    items: [
      tk("Encrypted environment variables, also per compose service"),
      tk("Apply changes with a restart, without a rebuild"),
      tk("Private registries: Docker Hub, GHCR, GitLab, ECR, Harbor, Quay"),
    ],
  },
  {
    icon: <Server />,
    title: tk("Servers"),
    items: [
      tk("The local server and remote servers over SSH"),
      tk("Web terminal, live logs, container actions"),
      tk("Containers, images and volumes of every server in one view"),
      tk("Corporate proxy and Nexus mirrors for images and packages"),
      tk("Live topology map with crash-loop detection"),
    ],
  },
  {
    icon: <Activity />,
    title: tk("Monitoring"),
    items: [
      tk("CPU, memory, disk and network metrics with history"),
      tk("Alerts by email, Slack, Microsoft Teams, Discord, Telegram or webhook"),
      tk("Uptime monitors and a public status page"),
    ],
  },
  {
    icon: <ShieldCheck />,
    title: tk("Teams and security"),
    items: [
      tk("Roles per organization and project, protected environments, deployment approvals"),
      tk("Two-factor authentication, single sign-on (OIDC), LDAP and Active Directory"),
      tk("Audit log with export and SIEM streaming, API tokens"),
      tk("Vulnerability scanning of images with Trivy"),
      tk("Static (SAST) and dynamic (DAST) security testing with Semgrep and OWASP ZAP"),
      tk("Downloadable security reports for every build and deploy"),
    ],
  },
  {
    icon: <Settings2 />,
    title: tk("Platform"),
    items: [
      tk("One-click updates from GitHub releases, with a backup and automatic rollback"),
      tk("AI assistant with any model on OpenRouter"),
      tk("English, Romanian and Russian; light and dark theme; command palette (Ctrl K)"),
    ],
  },
];

export function AboutPage() {
  const status = useAuthStatus().data;
  const update = useUpdateInfo().data;
  const version = status?.version ?? "";
  const repo = status?.repo ? `https://github.com/${status.repo}` : "";
  return (
    <div className="space-y-6">
      <Card>
        <div className="flex flex-wrap items-start gap-4 p-5">
          <LogoMark className="size-12 rounded-xl" />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-lg font-semibold tracking-[-0.01em]">PortDock</h2>
              {version && <Badge>{version}</Badge>}
              {update?.update_available && update.latest ? (
                <Badge tone="info">
                  <ArrowUpCircle /> {t("Update available: {version}", { version: update.latest.tag })}
                </Badge>
              ) : update?.latest ? (
                <Badge tone="success">
                  <CheckCircle2 /> {t("Up to date")}
                </Badge>
              ) : null}
              <Badge tone="accent">{t("Free and open source")}</Badge>
              <Badge>Apache-2.0</Badge>
            </div>
            <p className="mt-2 max-w-3xl text-[13px] leading-relaxed text-muted">
              {t("A self-hosted platform for deploying and running applications, databases and services on your own servers — like Heroku or Vercel, on your infrastructure.")}
            </p>
            <p className="mt-1.5 text-[13px] text-subtle">
              {t("Developed by {name}", { name: DEVELOPER })} · © {new Date().getFullYear()}
            </p>
            {repo && (
              <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
                <a href={repo} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 font-medium hover:underline">
                  GitHub <ExternalLink className="size-3.5 text-faint" />
                </a>
                <a href={`${repo}/releases`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 font-medium hover:underline">
                  {t("Releases")} <ExternalLink className="size-3.5 text-faint" />
                </a>
                <a href={`${repo}/issues`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 font-medium hover:underline">
                  {t("Report a problem")} <ExternalLink className="size-3.5 text-faint" />
                </a>
              </div>
            )}
          </div>
        </div>
      </Card>

      <div>
        <h3 className="mb-3 text-sm font-semibold">{t("What PortDock can do")}</h3>
        <div className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
          {features.map((f) => (
            <Card key={f.title}>
              <div className="p-4">
                <div className="mb-2.5 flex items-center gap-2 text-[13px] font-semibold [&_svg]:size-4 [&_svg]:text-accent">
                  {f.icon}
                  {t(f.title)}
                </div>
                <ul className="space-y-1.5 text-[13px] leading-snug text-muted">
                  {f.items.map((item) => (
                    <li key={item} className="flex gap-2">
                      <span className="mt-[7px] size-1 shrink-0 rounded-full bg-faint" />
                      {t(item)}
                    </li>
                  ))}
                </ul>
              </div>
            </Card>
          ))}
        </div>
      </div>

    </div>
  );
}
