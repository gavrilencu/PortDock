<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="frontend/public/logo-on-dark.png" />
  <img src="frontend/public/logo-on-light.png" alt="PortDock" width="283" height="122" />
</picture>


**Your own Heroku, on your own servers.**
Deploy applications, databases and services from Git or Docker — with HTTPS, rollbacks, monitoring and an AI assistant — on any Linux server you control.

[![Release](https://img.shields.io/github/v/release/gavrilencu/PortDock?label=release)](https://github.com/gavrilencu/PortDock/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Image](https://img.shields.io/badge/image-ghcr.io%2Fgavrilencu%2Fportdock-2496ED?logo=docker&logoColor=white)](https://github.com/gavrilencu/PortDock/pkgs/container/portdock)
[![Stars](https://img.shields.io/github/stars/gavrilencu/PortDock?style=social)](https://github.com/gavrilencu/PortDock/stargazers)

[Quick start](#quick-start) · [Features](#features) · [Screenshots](#screenshots) · [Documentation](#documentation) · [Română](docs/README.ro.md)

<img src="docs/images/versions.png" alt="PortDock — versions of an application with drag-and-drop rollback" width="100%" />

</div>

---

## Why PortDock

- **Everything in one place.** Git deployments, Docker Compose stacks, managed databases, one-click services, domains with automatic HTTPS, metrics, alerts and backups — in a single binary with a modern web interface.
- **Roll back in seconds.** Every production deployment is kept as a version. Drag an older version onto **Live** and it is back — no rebuild, same environment variables.
- **Monorepos done right.** A frontend and a backend in one `docker-compose.yml` deploy together, each service with its own domain and its own variables.
- **Built for teams.** Project roles, protected environments, deployment approvals, SSO (OIDC), LDAP / Active Directory, two-factor authentication and a full audit log.
- **An AI assistant built in.** Ask why a container or deployment fails in the corner of every page: it reads your logs, containers and deployments with your own permissions, proposes fixes you confirm, and works with any model on OpenRouter or a local server.
- **Free and open source.** No plans, no license keys, no limits on servers or users. Apache-2.0.

## Quick start

On a Linux server (Ubuntu or Debian recommended), as root. Ports **80** and **443** must be free for the HTTPS proxy.

**Option 1 — installer** (installs Docker if it is missing):

```bash
git clone https://github.com/gavrilencu/PortDock.git portdock && cd portdock
sudo bash install.sh                               # dashboard on http://<server-ip>:8000
sudo bash install.sh https://portdock.example.com  # or with a domain that points to the server
```

If port 8000 is taken, the installer picks the next free port and prints the address (or set `PORTDOCK_PORT=9000`).

**Option 2 — prebuilt image** (Docker already installed):

```bash
docker run -d --name portdock --restart unless-stopped \
  -p 8000:8000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /data/portdock:/data/portdock \
  -e PORTDOCK_PUBLIC_URL=http://<server-ip>:8000 \
  ghcr.io/gavrilencu/portdock:latest
```

Keep the data directory mounted at the same path as on the host (`/data/portdock`): builds and backups are bind-mounted into helper containers by absolute path.

**Then:** open the dashboard, create the owner account, and start the proxy from **Servers → localhost → Run setup**. Every [release](https://github.com/gavrilencu/PortDock/releases) lists the exact commands for that version.

## Features

<table>
<tr>
<td width="50%" valign="top">

**🚀 Deployments**
- From GitHub (GitHub App), Azure DevOps, GitLab, Gitea / Forgejo, Bitbucket or any Git URL; Docker images; Docker Compose
- Build packs: Dockerfile, Nixpacks, Node.js, Bun, Python, Go, static sites, shell scripts — auto-detected
- Push-to-deploy, pull request previews with their own domain, deploy hooks for any CI
- Deployment status on commits and pull requests (GitHub, Azure DevOps)
- Zero-downtime rollouts: traffic moves only after the health check passes; optional canary deployments with automatic rollback
- Pre- and post-deploy commands, replicas, CPU / memory limits, watch paths for monorepos
- Scheduled jobs (cron) per application, with run history, output, timeouts and failure alerts
- Persistent storage, access protection (single sign-on, password, IP allow list, rate limit)
- Configuration as code: `portdock.yaml` applied with the `pdctl` CLI, a Terraform provider and an OpenAPI 3.1 description of the API
- Several servers per application, dedicated build servers, private registries

</td>
<td width="50%" valign="top">

**🕘 Versions**
- Every production deployment becomes a version (v1, v2…)
- The live version and the last 5 are kept for 15 days
- Roll back by drag and drop or in two clicks — no rebuild, current variables
- Docker Compose services each keep their own images

**🌐 Domains & HTTPS**
- Automatic Let's Encrypt certificates, renewal and expiry watchdog
- Wildcards, DNS-01 (Cloudflare, Route 53, Azure, Hetzner…), uploaded certificates
- www redirects, per-domain ports and paths, a domain per compose service
- Changes apply in seconds, without a redeploy

</td>
</tr>
<tr>
<td valign="top">

**🗄️ Databases & services**
- PostgreSQL, MySQL, MariaDB, Microsoft SQL Server, MongoDB, Redis, Valkey, ClickHouse
- Online backups for every engine, following each one's documentation; manual, scheduled or all at once, queued per database, downloadable
- Restore in one click, after an automatic safety backup; retention, upload to S3 (AWS, R2, B2, MinIO)
- Access from outside by domain or public IP
- SQL console in the browser (PostgreSQL, MySQL, MariaDB, SQL Server, ClickHouse): tables, results, CSV export; read-only for members
- 31 one-click services (Keycloak, Nextcloud, n8n, Gitea, Grafana, Jenkins, Open WebUI…) and a catalog of 127 Docker images
- Service catalogs: the community catalog, Coolify's 350+ templates (converted automatically) or your own, from a URL or a file
- Import from Coolify, Heroku and Dokku: applications with their source, build, domains, variables, volumes and jobs, databases and services

**⚙️ Configuration**
- Encrypted environment variables (AES-256-GCM), build-time and runtime, per compose service
- Secrets read from HashiCorp Vault or Azure Key Vault when a deployment starts, never stored in PortDock
- Variables from `docker-compose.yml` shown and overridable; apply with a restart
- Persistent storage per application: Docker volumes kept across deployments (separate ones for previews), read-only mounts, server directories for administrators
- Registries: Docker Hub, GHCR, GitLab, Amazon ECR, Harbor, Quay, any OCI registry
- Corporate networks: HTTP(S) proxy for PortDock, builds and containers (NO_PROXY with domains and CIDRs), Docker Hub mirror (Nexus, Artifactory, Harbor) applied to each server's daemon, npm / PyPI / Go / Maven mirrors for builds

</td>
<td valign="top">

**🖥️ Servers & containers**
- The local server and remote servers over SSH (keys encrypted, host key pinning)
- One-click server setup: Docker, network and the Traefik proxy
- Web terminal, live logs, start / stop / restart, images, volumes, networks, prune
- One view of the containers, images and volumes of every server, with CPU / memory and the resource each belongs to
- Live topology map with crash-loop detection, in the style of Argo CD

**📈 Monitoring & alerts**
- CPU, memory, disk, network and load history for servers and every resource
- Alert rules with duration and severity; notifications by email, Slack, Microsoft Teams, Discord, Telegram or signed webhook
- Uptime monitors (status code, keyword, response time, TLS expiry) with incidents and a public status page
- Prometheus endpoint, log drains to Loki, Elasticsearch, Graylog, Splunk, syslog or HTTP, and OpenTelemetry settings injected into applications

</td>
</tr>
<tr>
<td valign="top">

**🔐 Teams & security**
- Owner / admin / member, plus viewer / developer / maintainer roles per project
- Protected environments and deployment approvals (four-eyes principle)
- Two-factor authentication with recovery codes, organization-wide 2FA policy
- Single sign-on (OIDC: Entra ID, Okta, Google, Keycloak, Authentik…) and LDAP / Active Directory
- Audit log with CSV / JSON export and SIEM streaming (webhook or syslog), API tokens
- Compliance report for auditors: access, approvals, scans, backups and their test restores, incidents and the audit log of a period, as printable HTML or a ZIP with CSV files and checksums
- Vulnerability scanning of images with Trivy, optionally blocking deployments on critical or high findings
- Static analysis of the source with Semgrep (SAST) and dynamic scans of the running app with OWASP ZAP (DAST), on every deploy or on demand
- Downloadable reports for every scan (HTML, CSV, JSON, SARIF, ZAP) and a ZIP with all the reports of a deployment

</td>
<td valign="top">

**✨ Platform**
- One-click updates from GitHub releases, with a backup and automatic rollback
- AI assistant with any model on OpenRouter or a local server (Ollama, vLLM…): reads logs, containers and deployments with the user's permissions, proposes actions that run after confirmation, knows the documentation; per-user history, access control
- MCP server: AI agents (Claude Code, VS Code, Cursor…) list, deploy, follow, roll back and read logs with an API token
- Interface in English, Romanian and Russian; light and dark theme
- Command palette (Ctrl K), real-time UI, full-width responsive layout

</td>
</tr>
</table>

## Screenshots

| | |
|---|---|
| <img src="docs/images/dashboard.png" alt="Dashboard" /> **Dashboard** — every project, deployment and server at a glance | <img src="docs/images/application.png" alt="Application" /> **Application** — deployments, containers, domains and details |
| <img src="docs/images/topology.png" alt="Topology" /> **Topology** — domains, resources, servers and containers, live | <img src="docs/images/assistant.png" alt="AI assistant" /> **AI assistant** — help with deployments, right where you work |
| <img src="docs/images/domains.png" alt="Domains" /> **Domains** — HTTPS, redirects and DNS checks per domain | <img src="docs/images/about.png" alt="About" /> **About** — everything PortDock can do |

## PortDock vs. a hosted PaaS

| | Hosted PaaS (Heroku, Vercel, Render…) | PortDock |
|---|---|---|
| Where your apps and data live | The provider's cloud | Your own servers, any provider or on-premises |
| Cost | Per app, per seat, per GB | Free — you pay only for your servers |
| Limits | Plan limits on apps, builds, bandwidth | None |
| Databases | Paid add-ons | Included, with backups to your own S3 |
| Single sign-on, LDAP, audit log | Usually enterprise plans | Included |
| Vendor lock-in | Provider-specific configuration | Standard Docker and Docker Compose |

If you know Coolify, Dokploy or CapRover, PortDock will feel familiar — with versions and drag-and-drop rollback, a domain and variables per compose service, deployment approvals, SSO / LDAP and an AI assistant included.

## Documentation

<details>
<summary><b>Remote servers</b></summary>

1. **Servers → Add server** and generate an SSH key.
2. Add the public key to `~/.ssh/authorized_keys` on the server (the `root` user is recommended).
3. Press **Run setup** on the new server: it installs Docker, the `portdock` network and the Traefik proxy.
4. Optional: set a *wildcard domain* (`*.apps.example.com` → server IP) for automatic domains and previews.

</details>

<details>
<summary><b>Domains and certificates</b></summary>

- Each application has a **Domains** tab: HTTP → HTTPS redirect, www redirect, internal port per domain, search indexing (`X-Robots-Tag`), DNS status and certificate status. Changes reach the proxy within seconds, without a redeploy.
- **Check all DNS** compares A / AAAA records with the server IP and detects the Cloudflare proxy; **DNS records** lists exactly what to create.
- Let's Encrypt certificates are issued and renewed automatically. A watchdog checks them every 6 hours and forces a renewal when fewer than 14 days are left, with notifications (`certificate.expiring`, `certificate.renewed`, `certificate.failed`).
- **DNS-01** for wildcard and internal domains: Cloudflare, Route 53, Azure DNS, Google Cloud DNS, DigitalOcean, Hetzner, OVH, GoDaddy, RFC 2136 or any provider supported by lego. A custom ACME server (step-ca) or the Let's Encrypt staging environment can be used too.
- **Uploaded certificates** (corporate CA, EV / OV) are validated and distributed to every proxy, with reminders before they expire.
- **Settings → General** sets the dashboard URL (PortDock serves itself on that domain with a certificate), time zone and public IPs.
- **Access protection** (Domains tab), applied by the proxy on every domain and preview of the application, without a redeploy:
  - **Single sign-on with PortDock**: visitors sign in to PortDock (with the company's OIDC or LDAP when configured) before reaching the application. Choose who may enter (people with access to the project, or every PortDock user) and optionally limit it to some addresses or `@domain`s. The application receives `X-Forwarded-User`, `X-Forwarded-Email` and `X-Forwarded-Name`; `/_portdock/logout` signs out. Ideal for admin panels, Grafana or internal tools.
  - **Password** (HTTP basic authentication) with one or more users.
  - **Allowed addresses** (IP or CIDR, e.g. the office VPN), reading `X-Forwarded-For` behind a CDN or load balancer if needed.
  - **Rate limit** per visitor address, with a burst; excess requests get `429`.

</details>

<details>
<summary><b>Docker Compose: a domain and variables per service</b></summary>

- When the repository comes from GitHub, PortDock reads `docker-compose.yml` and offers a domain field for every service at creation — for example `app.example.com` → `frontend` and `api.example.com` → `backend`. The port is taken from `ports` / `expose`.
- Later, the **Domains** tab has a *Service* selector; changes apply without a redeploy.
- The **Environment** tab has variables for the whole application (available as `${VAR}` in the compose file) and variables per service, which override the values in `docker-compose.yml`. **Restart with new variables** applies them without a rebuild.
- Prefer `expose:` over `ports:` so containers don't bind ports on the host — traffic comes through the proxy.

</details>

<details>
<summary><b>Shared variables and comparing environments</b></summary>

Variables can be set once for many applications and services:

| Scope | Where |
|---|---|
| Organization | **Settings → Shared variables** (administrators) |
| Project, all environments | project page → **Variables** |
| One environment | project page → **Variables** → *&lt;environment&gt; only* |
| Application or service | its **Environment** tab |

The narrowest scope wins, so an application's own variable overrides its environment's, which overrides the project's, which overrides the organization's. Shared variables can be secret, build-time or preview-only like any other. Secrets are masked in deployment logs.

An application's Environment tab lists the shared variables it receives, marking the overridden ones. **Compare with another environment** shows, key by key, how its effective variables differ from the same application in another environment (staging and production, for example) and where each value comes from. Selected variables can be copied over to it. Secret values are compared without being shown.

</details>

<details>
<summary><b>Secret managers: HashiCorp Vault and Azure Key Vault</b></summary>

A variable can read its value from a secret manager instead of storing it. PortDock reads the value when a deployment, a scheduled job or a service starts, passes it to the container and masks it in the logs. A new value in the vault is used from the next deployment (or **Restart with new variables**).

**Settings → Secret managers** (administrators) connects a store:

| | HashiCorp Vault (or OpenBao) | Azure Key Vault |
|---|---|---|
| Sign-in | a token (a periodic one does not expire) or AppRole | an app registration (tenant, client ID, client secret) or the managed identity of an Azure VM |
| Secrets | a KV v2 or v1 engine (`secret` by default), Vault Enterprise namespaces | secrets by name, optionally `name/version`; a JSON secret can be read key by key |
| Permissions | a policy with `read` on the paths | the *Key Vault Secrets User* role (or a *Get* access policy) |

Both take an internal CA certificate and go through the corporate proxy. A store can be limited to some projects and, for Vault, to a path prefix, so a team can only reference its own secrets; credentials are stored encrypted and never shown again. **Test connection** signs in and can read a path, listing its keys but never the values.

Then, in an application's **Environment** tab (or in shared variables), choose **Secret manager** when adding a variable and pick the store, the path and the key: `vault-prod: shop/db#password`. **Check** lists the keys of the path. In `portdock.yaml` and on the command line:

```yaml
    secret_refs:
      DB_PASSWORD: vault-prod:shop/db#password
      API_KEY: keyvault-prod:payments-api-key
```

```sh
pdctl env set shop/web DB_PASSWORD --from vault-prod:shop/db#password
```

With Terraform, `portdock_env_var` takes `secret_ref = "vault-prod:shop/db#password"` instead of `value`.

If a secret cannot be read (a policy that refuses it, a deleted secret, the vault unreachable), the deployment stops before anything changes and its log says why: `variable DB_PASSWORD: vault-prod: permission denied on secret/shop/db`. The running version keeps running. A store that variables use cannot be deleted, and the compliance report lists the stores.

</details>

<details>
<summary><b>Canary deployments</b></summary>

In an application's **Configuration → Deployment strategy**, a canary sends part of the traffic (5, 10, 25 or 50 %) to the new version first, through a Traefik weighted service, while the current version serves the rest.

- During the observation time (10 minutes by default), PortDock reads the proxy's request counters. It rolls the new version back automatically when:
  - more than the threshold (5 % by default) of its responses are server errors, and clearly more than the current version's, so a shared outage doesn't count; or
  - one of its containers stops or restarts.
- After the observation time, all the traffic moves to the new version.
- The deployment page shows the live numbers, with **Promote now** and **Roll back** buttons.
- A rolled-back canary fails the deployment with the reason, the current version serves everything again, and the `deployment.failed` notification says why.
- Canaries apply to production deployments of applications on one server; rollbacks to a previous version go straight to it.

The `deployment.success` notification includes how long the deployment took and the application's address.

</details>

<details>
<summary><b>Versions and rollbacks</b></summary>

- Every successful production deployment is saved as a numbered version. Single-container applications tag their image; compose applications tag the image of every built service and keep the compose file.
- The live version plus the last 5 are kept for 15 days; older ones are removed automatically.
- In the **Versions** tab, drag a version onto **Live**, press **Deploy** or **Roll back to vN**. Nothing is rebuilt, the current variables are used, and the old version stops only once the new one is healthy. Approvals apply when the environment requires them.

</details>

<details>
<summary><b>Database backups</b></summary>

- **Back up now**, a **schedule** (cron, keeps its last N files, optional S3 upload) or **Settings → Backup storage → Back up all now**.
- Backups and restores of a database run one at a time: new requests wait as **Pending**; at most two jobs run per server.
- Files are stored on the database's server in `/data/portdock/backups/<database>/`; the Backups tab shows the path and downloads them.
- **Restore** first takes a safety backup of the current data (kept as *Before restore*), then replaces the data; if the safety backup fails, nothing is restored.

| Engine | Backup | Restore |
|---|---|---|
| PostgreSQL | `pg_dump` custom format | `pg_restore --clean --if-exists` in a single transaction |
| MySQL / MariaDB | `mysqldump` / `mariadb-dump --single-transaction` with routines, events and triggers | `mysql` / `mariadb` |
| Microsoft SQL Server | copy-only full backup `WITH CHECKSUM`, checked with `RESTORE VERIFYONLY` | `RESTORE … WITH REPLACE` (sessions closed with `SINGLE_USER`, reopened afterwards) |
| MongoDB | `mongodump --archive --gzip` | `mongorestore --drop` |
| ClickHouse | table schemas and data in Native format | tables recreated and reloaded, then views |
| Redis / Valkey | `BGSAVE` (doesn't block clients) → RDB file | download only |

SQL Server runs the free **Express** edition (`MSSQL_PID`); it needs about 2 GB of memory.

**Test restores** prove that a backup can be restored, without touching the database:

1. **Test restore** on a backup, or a schedule (weekly, daily or any cron) for the latest backup of a backup schedule, starts a temporary container of the database's image. The container has no network, no published port and none of the database's volumes.
2. The backup is restored into it. Redis and Valkey start directly on the RDB file.
3. PortDock counts the tables (collections for MongoDB, keys for Redis and Valkey), then removes the container.

The result shows on the backup, for example *Restored in 12s: 48 tables*. A failure sends the `backup.verify_failed` notification, and Prometheus gets `portdock_backup_last_verify_success` and `portdock_backup_last_verify_timestamp_seconds`.

**Point-in-time recovery (PostgreSQL)** restores a database as it was at any second of the recovery window, for example just before a bad migration. Turn it on in the Backups tab; the database restarts once.

How it works, with PostgreSQL's own tools:

- **WAL archiving**: PostgreSQL archives every WAL segment, gzipped, to `/data/portdock/backups/<database>/pitr/wal` on its server. With `archive_timeout` at 60 s, a restore misses at most the last minute.
- **Base backups**: `pg_basebackup` runs on a schedule (daily by default) and the last N are kept. WAL older than the oldest one kept is removed with `pg_archivecleanup`.
- **Copy to S3** (optional): the archive and base backups are mirrored to the bucket every 5 minutes.

What a restore to a point in time does:

1. Archives the latest changes.
2. Stops the database.
3. Moves its data aside, inside the data volume.
4. Unpacks the newest base backup taken before that time.
5. Starts PostgreSQL, which replays the archive up to that time and is promoted.

If the recovery fails, the previous data is put back automatically. The replaced data stays until the next restore. Restores can follow each other, including to a time before an earlier restore: PortDock picks the right timeline.

WAL archiving failures are notified, and Prometheus gets `portdock_pitr_archive_ok`, `portdock_pitr_last_archived_timestamp_seconds` and `portdock_pitr_last_base_backup_timestamp_seconds`.

**Replicas and failover (PostgreSQL)**, in the Connection tab:

- **Add replica** creates a hot standby, cloned with `pg_basebackup` through a replication slot so the primary keeps the WAL the replica still needs. The replica follows the primary in real time and answers read-only queries at its own URL.
- The replica can run on the same server or on another one. On another server it reaches the primary through the primary's public port, so allow that server in the firewall. A replica can also publish its own port.
- Lag (bytes and seconds) is measured every minute and shown with the status (*Streaming*, *Lagging*, *Disconnected*). A replica that stops streaming sends a notification, and Prometheus gets `portdock_replica_streaming`, `portdock_replica_lag_bytes` and `portdock_replica_lag_seconds`.
- **Promote** is a failover:
  1. The primary is stopped first (fencing).
  2. The replica is promoted.
  3. The database moves onto the replica's server and data volume under its own name and network aliases, so applications keep their connection URL. The previous primary's data stays in its volume. Other replicas have to be created again.
- **Automatic failover** (off by default) promotes the most up-to-date replica on another server once the primary's server has been unreachable for a set time (120 s by default). An unreachable primary cannot be stopped, so when its server answers again PortDock stops that former primary right away.

</details>

<details>
<summary><b>Scheduled jobs</b></summary>

The **Jobs** tab of an application runs commands on a schedule: nightly reports, clean-ups, synchronizations.

- The schedule is a cron expression (or `@hourly`, `@daily`, `@every 30m`) in the instance time zone; the next run is shown.
- A job runs on the primary server in a **new container of the application's current image**, with its variables and persistent storage (like the pre-deploy command); for Docker Compose applications, inside a running container of the chosen service.
- Each run keeps its status, exit code, duration and output (the last 64 KB), for the last 50 runs. A run stops after its timeout, and a run never starts while the previous one is still running.
- **Run now** starts a run by hand (deploy permission); a failed or timed-out run sends a `job.failed` notification.

</details>

<details>
<summary><b>Backup and restore of PortDock itself</b></summary>

PortDock keeps its whole configuration (accounts, projects, applications, domains, variables, connections) in one SQLite file, with the secrets in it encrypted by `secret.key`. **Settings → Backup storage → Instance backup** backs up both:

- On a **schedule** (cron, in the instance timezone) or with **Back up now**; the last N backups are kept, on the server (`/data/portdock/backups/instance/`) and, with **Upload to S3**, in the bucket under `portdock/instance/`.
- Each backup is a consistent snapshot (`VACUUM INTO`) of the database plus the key, in a `.tar.gz` encrypted with the **backup passphrase** in the [age](https://age-encryption.org) format. Keep the passphrase outside PortDock: it is the only thing needed to restore, and without it nobody can read a backup. A failed backup sends a `backup.failed` notification.
- **Restore on a new server:** install PortDock, open it and choose **Restore it from a backup** on the first-run page, then upload the file and type the passphrase. PortDock restarts with the old data; sign in with the old accounts. From the command line instead:

  ```bash
  docker run --rm -v /data/portdock:/data/portdock -v $PWD:/in:ro -e PORTDOCK_RESTORE_PASSPHRASE='…' \
    ghcr.io/gavrilencu/portdock:latest -restore /in/portdock-20261012-030000.tar.gz.age
  ```

- **Roll back the running instance:** the owner can restore any backup from the list (or copy one from S3 first). The data it replaces is kept in `/data/portdock/restore/replaced-*`.
- By hand, without PortDock: `age -d -o backup.tar.gz portdock-….tar.gz.age && tar -xzf backup.tar.gz` gives `portdock.db`, `secret.key` and `manifest.json`.
- If the old instance used `PORTDOCK_SECRET_KEY` instead of `secret.key`, the restore checks that the new one uses the same value (or none).

</details>

<details>
<summary><b>GitHub integration</b></summary>

1. **Settings → Git sources → Connect GitHub.** PortDock creates a private GitHub App with minimal permissions (Contents, Metadata and Pull requests read; Commit statuses write), optionally under an organization or on GitHub Enterprise Server. **Use existing app** takes an App ID, private key and webhook secret.
2. Install the app on your account or organization and choose the repositories.
3. **New resource → Git repository → GitHub**: pick the repository and branch; the build pack is detected automatically.
4. Every push deploys; pull requests get previews if enabled. The status appears on the commit as `portdock/deploy`.

GitHub needs a public HTTPS `PORTDOCK_PUBLIC_URL` to deliver webhooks.

</details>

<details>
<summary><b>Git over SSH (GitLab, Bitbucket, Gitea on premises)</b></summary>

Repositories can be cloned over SSH: `git@gitlab.corp:team/app.git` or `ssh://git@bitbucket.corp:7999/proj/app.git`.

- **Deploy key**: each application gets its own Ed25519 key. It is generated when you create the application, or later in its settings, where you can also import your own key. Add the public key to the repository as a read-only deploy key. The private key is stored encrypted. It is written next to the checkout, outside the build context, only during the clone, and removed afterwards.
- **Host keys**: the Git server's key is checked on every clone (`StrictHostKeyChecking=yes` with PortDock's own `known_hosts`).
  - An unknown host is trusted on its first clone, and its fingerprints are written to the deployment log.
  - **Settings → Git sources → SSH host keys** lists them. There you can read a host's keys, compare the fingerprints and trust them, or paste `known_hosts` lines.
  - **Only trust hosts listed here** refuses unknown hosts.
  - A changed host key fails the deployment.
- Submodules over SSH use the same key. HTTPS with a token and the GitHub / Azure DevOps integrations work as before.

</details>

<details>
<summary><b>Azure DevOps integration</b></summary>

1. In Azure DevOps create a **personal access token** (*User settings → Personal access tokens*) for the organization with the scopes **Code: Read, Status** and **Service Hooks: Read, query & manage**. To add service hooks, the token's user must be allowed to edit subscriptions in the project (project administrators are).
2. **Settings → Git sources → Connect Azure DevOps**: enter the organization (`contoso`, `https://dev.azure.com/contoso`, `https://contoso.visualstudio.com`, or the collection URL of Azure DevOps Server 2020+) and the token. PortDock checks it by listing the repositories and stores it encrypted.
3. **New resource → Git repository → Azure DevOps**: pick the repository and branch; the build pack is detected from the repository, like for GitHub.
4. PortDock clones with the token and adds **service hooks** (*Code pushed*, *Pull request created*, *Pull request updated* → Web Hooks) to the repository, authenticated by a secret HTTP header. Every push to the branch deploys; watch paths are checked against the files of the pushed commits. The hooks are replaced when the webhook secret is regenerated and removed when the application is deleted.
5. With previews enabled, every active pull request targeting the branch gets a **preview deployment** with its own domain, redeployed on new commits (not on votes or title changes) and removed when the pull request is completed or abandoned.
6. Each deployment is reported as **`portdock/deploy`** on the commit, and as **`portdock/preview`** on the pull request, linking to the deployment in PortDock.

*CI/CD & Webhooks* shows the state of the hooks, recreates them, and gives the URL and header to add it by hand if the token can't. Tokens expire: replace them from *Settings → Git sources* without touching the applications. Azure DevOps needs a public HTTPS `PORTDOCK_PUBLIC_URL` to deliver events.

</details>

<details>
<summary><b>Corporate proxy and Nexus</b></summary>

*Settings → Network & proxy*:

- **Corporate proxy** — HTTP / HTTPS proxy, optional user and password (stored encrypted), and `NO_PROXY` (names, `.domains`, IPs, CIDR ranges). Choose where it is used:
  - **PortDock itself**: Git providers, update checks, notifications, the AI assistant, uptime checks.
  - **Builds**: git clone, `docker build` (as predefined build arguments, so it is not stored in the image), Nixpacks, Docker Compose builds, the Trivy database.
  - **Containers**: `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` in every application and service. Each application can follow the setting, always use the proxy or never use it (*Configuration → Network*).
  - *Test* checks a URL through the settings being edited, before saving them.
- **Docker Hub mirror** — a pull-through registry (a Nexus *docker (proxy)* repository, Artifactory, Harbor). PortDock merges it into `/etc/docker/daemon.json` on each server (other keys are kept, `insecure-registries` for plain HTTP) and reloads Docker without stopping containers. The daemon's own proxy is written as a systemd drop-in and needs a restart of Docker, offered with a confirmation.
- **Package mirrors** — npm, PyPI, Go proxy and any other build variable (Maven, NuGet…). The Node.js, Bun, Python and Go build packs use them automatically; in your own Dockerfile declare `ARG NPM_CONFIG_REGISTRY` (etc.). A mirror of the Trivy database can be set for servers without internet.

Private images hosted in Nexus are added in *Container registries* like any registry.

</details>

<details>
<summary><b>CI/CD</b></summary>

**Push-to-deploy:** in the application, *CI/CD & Webhooks* shows the GitHub / GitLab / Gitea webhook URL and secret (events: push and pull request).

**Tests before deploying** (GitHub Actions) — turn off *Deploy on push* and trigger the deployment after the tests:

```yaml
- run: npm ci && npm test
- run: curl -fsS -X POST "${{ secrets.PORTDOCK_DEPLOY_HOOK }}&commit=${{ github.sha }}"
```

**API:** `curl -X POST $URL/api/applications/<id>/deploy -H "Authorization: Bearer pd_…"`

**API tokens** (*Settings → API tokens*) act with the rights of their account, limited three ways:

| Scope | Allows |
|---|---|
| `read` | `GET` requests: resources, logs, deployments |
| `deploy` | read, plus deploy, redeploy and roll back (the usual choice for CI) |
| `write` | deploy, plus configuration, variables and domains; no deletions, members or approvals |
| `full` | everything the account may do, including the organization for administrators |

A token can also be limited to some projects (the others are invisible to it) and given an expiry date. A limited token is never an administrator, and no token can manage accounts or create other tokens.

**Service accounts** (*Settings → Team*) are accounts for pipelines: they have no password and cannot sign in, an administrator creates their tokens, they get project access like people, and their actions appear under their name in the audit log.

</details>

<details>
<summary><b>One-click services and service catalogs</b></summary>

**New resource → One-click service** lists 31 built-in services: Keycloak, Nextcloud, n8n, Gitea, Grafana, Uptime Kuma, Jenkins, Open WebUI with Ollama, Directus, Wiki.js, BookStack, Matomo, Metabase, Vaultwarden, MinIO, Mailpit and more. Passwords and secrets are generated for each service, and the domain is filled in.

**Settings → Service catalogs** (administrators) adds more:

- the **PortDock community catalog**, downloaded every day from this repository, so templates merged on `main` arrive without an update;
- **Coolify's templates**: `https://raw.githubusercontent.com/coollabsio/coolify/v4.x/templates/service-templates.json` is converted on the fly (its magic variables, inline files, relative bind mounts), which brings 350+ services;
- **your own catalog**, from a URL (an internal Git server, Nexus or Artifactory for networks without internet) or an uploaded file.

The picker searches all of them, by name, category, tag or catalog. A template from a catalog deploys like a custom compose file: without host access unless an administrator allows it, and never with the Docker socket. Templates that need variables first (`${SMTP_HOST:?}`) are created without starting, on their Environment tab.

A template is a compose file with an `x-portdock` block; see [backend/internal/templates/services](backend/internal/templates/services/) to write one. `pdctl template lint` checks it, `pdctl template bundle DIR` makes a catalog of a directory, and `pdctl template convert` converts a Coolify template.

</details>

<details>
<summary><b>Import from Coolify, Heroku and Dokku</b></summary>

**Settings → Import** (administrators) reads another platform, shows what it would create, and creates the items you tick:

| Source | Credentials | What comes over |
|---|---|---|
| Coolify (v4) | its address and an API token (read, with access to sensitive data for the variables) | projects and environments; applications from Git (Nixpacks, Dockerfile, Compose, static) or an image, with port, domains, health check, limits, pre/post-deploy commands, variables, persistent storage and scheduled tasks; databases; one-click services (their compose converted, variables set by hand kept) |
| Heroku | an API key | applications with their config vars, custom domains and web dynos (replicas), building from Heroku Git with the key until you point them at your repository; Postgres, MySQL and Redis add-ons |
| Dokku | the server, added to PortDock over SSH | applications from the image Dokku built on that server, with their variables, domains, port, scale and storage; datastores of the postgres, mysql, mariadb, mongo and redis plugins |

Everything is created stopped unless you choose *Deploy after importing*, in the source's project and environment or in one you name. Databases are created empty, of the same engine: restore a dump of the old one into them. Volumes start empty. Each item lists what did not carry over (other process types, a password protection, files mounted from the server…). Nothing changes on the source.

</details>

<details>
<summary><b>Configuration as code: pdctl, portdock.yaml, Terraform, OpenAPI</b></summary>

**pdctl** is the command-line client. It is in the image (`docker exec portdock pdctl …`) and attached to every release for Linux, macOS and Windows.

```sh
pdctl login --url https://portdock.example.com --token pd_…   # or PORTDOCK_URL / PORTDOCK_TOKEN
pdctl deploy shop/web --wait              # exits with an error if the deployment fails
pdctl env set shop/web LOG_LEVEL=debug
pdctl run shop/web nightly-report --wait  # run a scheduled job now
pdctl export shop/web -o portdock.yaml    # the file of an existing application
pdctl apply -f portdock.yaml --dry-run
```

**portdock.yaml** declares the applications of one project and environment, so the configuration lives in the repository and is reviewed in pull requests:

```yaml
version: 1
project: shop
environment: production   # the default
server: localhost         # default server of the applications
prune: false              # true: remove the domains, plain variables, volumes (keeping their data) and jobs the file no longer lists
applications:
  - name: web
    git: {repository: https://github.com/acme/shop, branch: main}
    build: {pack: dockerfile}
    port: 3000
    replicas: 2
    resources: {cpu: "0.5", memory: 512m}
    health_check: {path: /health}
    domains:
      - domain: https://shop.example.com
    env:
      LOG_LEVEL: info
    secrets: [DATABASE_URL]   # values set in PortDock, never in the file
    volumes:
      - {name: uploads, path: /app/uploads}
    jobs:
      - {name: nightly-report, schedule: "0 3 * * *", command: node scripts/report.js}
    protection:
      auth: sso
```

- `pdctl apply` (or `POST /api/gitops/apply`) creates what is missing and changes only what differs; `--dry-run` lists the changes without making them. A second apply of the same file reports *Nothing to change*.
- It acts with the rights of the token: the `write` scope, or `full` when the file creates an environment.
- Secret values never go in the file: list their names under `secrets` and set them in PortDock (or with `pdctl env set APP KEY=VALUE --secret`); apply stops while one is missing. Unknown fields are refused.
- In CI: `pdctl apply -f portdock.yaml && pdctl deploy shop/web --wait`.

**Terraform / OpenTofu:** [terraform-provider-portdock](terraform-provider-portdock/) manages projects, environments, applications, domains and variables, with drift detection and `terraform import`. Every release attaches it, packaged for a filesystem mirror.

**OpenAPI:** `GET /api/openapi.json` describes every endpoint in OpenAPI 3.1, with the token scope each one needs (`x-token-scope`). **Settings → API tokens → API reference** shows it in the browser.

</details>

<details>
<summary><b>AI agents (MCP)</b></summary>

PortDock is a [Model Context Protocol](https://modelcontextprotocol.io) server, so an AI agent can work with your applications. The endpoint is `POST /api/mcp` (Streamable HTTP, protocol 2025-06-18) and takes an API token. **Settings → API tokens → AI agents** shows the configuration for each client.

```sh
# Claude Code
claude mcp add --transport http portdock https://portdock.example.com/api/mcp \
  --header "Authorization: Bearer pd_…"
```

Clients that start a local command (Claude Desktop, Cursor, Windsurf) use `pdctl mcp`, which passes the messages on standard input and output to PortDock:

```json
{ "mcpServers": { "portdock": { "command": "pdctl", "args": ["mcp"],
  "env": { "PORTDOCK_URL": "https://portdock.example.com", "PORTDOCK_TOKEN": "pd_…" } } } }
```

| Tool | What it does | Scope |
|---|---|---|
| `list_projects`, `list_applications`, `get_application` | projects, environments, applications with their status, domains and last deployments | read |
| `list_deployments`, `get_deployment` | deployments of an application; status and end of the log of one | read |
| `application_logs` | the last lines of the running container | read |
| `list_databases`, `list_servers`, `list_alerts` | databases, servers, alerts | read |
| `deploy_application` | deploys the branch or a commit; with `wait`, returns when it finishes | deploy |
| `rollback_deployment` | redeploys the image of an earlier deployment | deploy |
| `restart_application`, `backup_database` | restarts the containers; takes a backup now | write |

Every tool calls the API with the token, so its scope and projects apply to each call: give an agent a `read` token to look around, a `deploy` token to let it ship. Protected environments still wait for a maintainer's approval. Only API tokens are accepted (not browser sessions), and every action an agent takes is in the audit log (`mcp.tool`).

</details>

<details>
<summary><b>Observability: Prometheus, log drains, OpenTelemetry</b></summary>

PortDock connects to the monitoring stack you already run (**Settings → Observability**).

**Prometheus:** `GET /api/metrics` serves the latest values in the Prometheus text format. Scrape it with an API token of a service account (`read` scope):

```yaml
scrape_configs:
  - job_name: portdock
    scheme: https
    metrics_path: /api/metrics
    authorization:
      credentials: pd_…
    static_configs:
      - targets: ["portdock.example.com"]
```

It exports:

- servers: `portdock_server_up`, CPU, memory, disk, load and network;
- applications, databases and services, labelled `kind`, `project`, `environment`, `name` and `server`: CPU, memory and its limit, network, restarts, running containers and status;
- deployments: count by status, last result, duration and last success time;
- jobs: last run and its result;
- database backups: last result and last success time;
- TLS certificates: expiry of each domain;
- uptime monitors: up, response time and certificate expiry;
- firing alerts by severity, for administrators.

A token limited to some projects exports only those. An OpenTelemetry Collector reads the same endpoint with its `prometheus` receiver.

**Log drains** ship the logs of every container PortDock runs (applications, databases, services, jobs, and optionally the proxy) to:

| Destination | Address | Notes |
|---|---|---|
| Grafana Loki | `http://loki:3100` | labels `project`, `environment`, `app`, `kind`, `server`, `stream`; basic auth or bearer token, tenant |
| Elasticsearch / OpenSearch | `https://es:9200` | bulk API, index `portdock-%Y.%m.%d` by default; user/password or API key |
| Graylog (GELF) | `udp://graylog:12201`, `tcp://…`, `tls://…` | fields `_project`, `_environment`, `_app`… |
| Splunk | `https://splunk:8088` | HTTP Event Collector token, index and source type |
| Syslog | `udp://`, `tcp://` or `tls://siem:6514` | RFC 5424; the application is the APP-NAME, project, environment and server go in structured data |
| HTTP | any URL | newline-delimited JSON, optional headers |

- Each line carries the project, environment, application, kind, server, container and deployment.
- A destination can be limited to some projects, and includes the proxy's logs only on request.
- PortDock runs an agent ([Vector](https://vector.dev), `timberio/vector:0.58.0-alpine`) on every server while a destination is enabled. It reads the logs from the Docker socket, like the proxy does.
- Configuration changes are checked by Vector before they replace the running configuration, then applied without a restart.
- Each destination has a 256 MB disk buffer on the server for when it is unreachable.
- The settings page shows what each destination received and any delivery errors. **Send a test line** writes one line through every agent.
- Credentials are stored encrypted in PortDock. On the servers they sit in `<data dir>/logs/config` with mode 0600.

Searching past logs is done in Grafana, Kibana, Graylog or Splunk; PortDock keeps showing the live logs of each container.

**OpenTelemetry:** with an OTLP endpoint set (and optional headers, stored encrypted), each deployment gives the containers the standard SDK variables:

- `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` and `OTEL_EXPORTER_OTLP_HEADERS`;
- `OTEL_SERVICE_NAME`: the application, or `<application>-<service>` for compose services;
- `OTEL_RESOURCE_ATTRIBUTES`: `service.namespace` (project), `deployment.environment.name`, `service.version` (commit) and `portdock.application`.

Applications instrumented with an OpenTelemetry SDK send traces, metrics and logs to the collector without configuration. Variables an application sets itself are kept. An application with its own `OTEL_EXPORTER_OTLP_ENDPOINT`, or with `OTEL_SDK_DISABLED`, gets nothing, so the organization's headers never reach another collector.

</details>

<details>
<summary><b>Compliance report</b></summary>

**Settings → Compliance** (administrators) exports the evidence auditors ask for, for a period (last 30 days, last quarter, last year or any dates) and for every project or one:

- security settings (required 2FA, password sign-in, SSO and LDAP, SIEM streaming, update signature check);
- accounts with their role, 2FA, sign-in source and last sign-in; API tokens with scope, projects and expiry;
- protected environments and approval rules; deployments with who requested and who approved them;
- vulnerability, SAST and DAST scans with their findings;
- backups per database with failures, last success, test restores and point-in-time recovery;
- availability incidents, alerts and the audit log (organization-wide reports only).

**Open the report** shows a printable page (print or save as PDF). **Download ZIP** holds the same report as HTML and JSON, every table as CSV (protected against formula injection) and a `SHA256SUMS` file to check the copy later. Every export is written to the audit log. The API is `GET /api/compliance/report?from=2026-07-01&to=2026-09-30&project=3&format=html|zip|json`.

</details>

<details>
<summary><b>Single sign-on and LDAP / Active Directory</b></summary>

**SSO (OpenID Connect):** **Settings → Authentication → Add provider**, copy the *Redirect URI* (`<dashboard URL>/api/auth/sso/<slug>/callback`) into the app at your identity provider, then fill in the issuer URL, client ID and secret. PKCE, ID token signatures, nonce, audience and expiry are verified. Accounts can be created on first sign-in, restricted to email domains, and admin rights mapped from groups. Password sign-in can be turned off (the owner keeps a break-glass account). Existing accounts are linked by e-mail only for members with a verified e-mail; owners and administrators connect a provider themselves from **Settings → Profile**, and administrators link local accounts to an LDAP directory from **Settings → Team**.

- Entra ID: `https://login.microsoftonline.com/<tenant-id>/v2.0` (add the optional `groups` claim for admin mapping)
- Okta: `https://<org>.okta.com` with the `groups` scope
- Keycloak: `https://<host>/realms/<realm>`

**LDAP / Active Directory:** presets for Active Directory, OpenLDAP and FreeIPA; LDAPS or StartTLS with an optional custom CA; a service account finds the user and the password is checked with a bind; disabled AD accounts are refused and nested groups resolved; required groups and admin groups can be set. **Test** shows the email, groups and resulting role of a user.

</details>

<details>
<summary><b>AI assistant</b></summary>

**Settings → AI assistant** turns on a chat assistant in the bottom-right corner:

- any model on [OpenRouter](https://openrouter.ai), picked from the full catalog (search, context, price per 1M tokens, free-models filter) or typed by id, e.g. `anthropic/claude-sonnet-4.5`;
- the API key is stored encrypted; **Test connection** checks the key and the model;
- access for all users, administrators only or selected users; optional instructions for the system prompt;
- each user has a private history; replies stream in with Markdown, code blocks, Stop and copy.

**What it can see and do.** The assistant has tools that read the instance with the user's own permissions (project roles, visibility and token limits apply exactly as in the interface): projects, applications, deployments and their logs, the containers of every server with their logs, inspect data and live resource usage, databases and their backups, services, servers, domains, jobs, versions, alerts, uptime monitors and the audit log. Ask *Why did the last deployment fail?* or *Which containers have problems?* and it reads the evidence before answering; the chat shows each step and keeps it with the reply. Values that look like credentials in logs are masked before they reach the model. It also searches PortDock's documentation and troubleshooting playbooks, embedded in the binary, and knows which page you are looking at.

It can propose a few **actions**: deploy, roll back, restart or start an application, container, database or service, back up a database, run a job. A proposal runs only when the user presses **Confirm** in the chat, with the user's own rights, and is written to the audit log; it expires after 15 minutes. Nothing can be deleted, removed or stopped through the assistant, and no variable, domain, setting, user or token can be changed: the API refuses such requests even if a model tried. Administrators can turn the actions, or the tools altogether, off in **Settings → AI assistant → Access to the platform**. The tools need a model with tool calling; a model without it answers without the tools.

Messages are sent to OpenRouter and the provider of the selected model — don't paste secrets. `PORTDOCK_OPENROUTER_URL` points the assistant at an OpenRouter-compatible gateway.

**Local models (offline).** Choose **Your own server (OpenAI-compatible)** to keep every message in your network. Any server with the OpenAI API works: Ollama, vLLM, LiteLLM, LocalAI or llama.cpp. The API key is optional, the model list comes from the server, and **Test connection** sends a short prompt. For example, with Ollama on the PortDock network:

```sh
docker run -d --name ollama --network portdock -v ollama:/root/.ollama ollama/ollama
docker exec ollama ollama pull qwen2.5:7b
# Settings → AI assistant: server address http://ollama:11434/v1, model qwen2.5:7b
```

| Server | Address | Notes |
|---|---|---|
| Ollama | `http://ollama:11434/v1` | `ollama pull <model>` first |
| vLLM | `http://vllm:8000/v1` | the key is the `--api-key` you set, if any |
| LiteLLM | `http://litellm:4000/v1` | in front of Azure OpenAI, Bedrock or local models; the key is a LiteLLM key |
| llama.cpp | `http://llama:8080/v1` | `llama-server --alias <model>` names the model |

The address must be reachable from the PortDock container: the same Docker network, or a host name or IP of your network. Larger models (7B and up) give much better answers than tiny ones.

</details>

<details>
<summary><b>Updates</b></summary>

Pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`: tests, the multi-arch image `ghcr.io/<owner>/<repo>:vX.Y.Z` and the GitHub release, whose description comes from `docs/releases/vX.Y.Z.md` plus install instructions. Every build knows its repository, so installations find updates without configuration. PortDock checks for a new release whenever it is opened and shows **Update available** in the sidebar.

On update, PortDock refuses to start while deployments are running, downloads the image, backs up the database and encryption key to `/data/portdock/backups/portdock/`, then an updater container recreates PortDock with the same configuration and waits for its health check — rolling back automatically if the new version doesn't start. Applications keep running throughout. Automatic updates between 02:00 and 05:00 can be enabled for stable releases.

Release images are signed by the release workflow (Sigstore keyless) and carry an SBOM and a provenance attestation. Before installing one, PortDock checks the signature with `cosign verify` (only `.github/workflows/release.yml` of the update repository, for a version tag) and installs exactly the verified digest; an unsigned or foreign image is refused and nothing changes. Networks without access to Sigstore can set **Settings → Updates → Image signature** to *warn*. See [SECURITY.md](SECURITY.md) for the CI checks and how to verify an image by hand.

</details>

<details>
<summary><b>Recovery from the command line</b></summary>

```bash
docker exec portdock portdock -reset-password admin@example.com   # new password for a user
docker exec portdock portdock -disable-2fa user@example.com       # turn off two-factor authentication
```

</details>

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORTDOCK_ADDR` | `:8000` | HTTP listen address |
| `PORTDOCK_PORT` | `8000` | Host port used by `install.sh` / `docker-compose.yml` |
| `PORTDOCK_DATA_DIR` | `/data/portdock` | Database, encryption key, builds and backups. In Docker, mount it at the same path as on the host. |
| `PORTDOCK_PUBLIC_URL` | `http://localhost:8000` | External URL (webhooks); `https://` enables secure cookies |
| `PORTDOCK_SECRET_KEY` | generated in `secret.key` | AES key for secrets (base64, 32 bytes, or a passphrase). **Back it up.** |
| `PORTDOCK_MAX_BUILDS` | `4` | Parallel deployments |
| `PORTDOCK_NETWORK` | `portdock` | Docker network shared with Traefik |
| `PORTDOCK_OPENROUTER_URL` | `https://openrouter.ai/api/v1` | API used by the AI assistant |
| `PORTDOCK_TRUSTED_PROXIES` | private ranges | Proxies whose `X-Forwarded-For` is trusted for client addresses (sign-in limits, audit log): IPs or CIDR ranges, or `none` |
| `PORTDOCK_DEV` / `PORTDOCK_DEBUG` | – | CORS for development / debug logs |

Behind Docker Hub rate limits, build through a mirror: `BASE_REGISTRY=mirror.gcr.io/library docker compose build`.

## Development

```bash
make dev-api   # backend on :8000, data in ./.data
make dev-ui    # Vite on :5173, proxying /api to :8000
make test      # go vet, go test and the TypeScript check
make build     # bin/portdock with the UI embedded
```

The backend runs Docker commands through a shell; on Windows use Git Bash or WSL with Docker Desktop, or run everything with `docker compose up --build`. New interface texts go through `t("…")` with translations in `frontend/src/locales/`; `npm run i18n:check` lists missing ones.

### Stack

| Layer | Technologies |
|---|---|
| Backend | Go, `net/http`, SQLite (pure-Go driver, WAL), `x/crypto/ssh`, WebSocket, SSE |
| Frontend | React 19, Vite, TypeScript, Tailwind CSS 4, TanStack Query, React Router, xterm.js |
| Runtime | Docker and Docker Compose, Traefik v3 (reverse proxy, Let's Encrypt, HTTP/3) |

### Architecture

```
backend/
  cmd/portdock          entrypoint: HTTP server, deploy workers, monitor, scheduler
  internal/api          REST, SSE (logs, events, chat), WebSocket (terminal), webhooks, embedded UI
  internal/deploy       queue and pipeline (clone → build → health check → rollout), compose,
                        versions, databases, backups, services, certificates, routes
  internal/executor     local / SSH command execution (pool, PTY, host key pinning)
  internal/docker       Docker CLI wrapper
  internal/proxy        Traefik: proxy container, file-provider routes, TLS
  internal/assistant    OpenRouter client for the AI assistant
  internal/github       GitHub App client and build pack detection
  internal/azuredevops  Azure DevOps REST client (repositories, files, service hooks)
  internal/updater      self-update from GitHub releases
  internal/store        SQLite schema, migrations and mapper
  internal/{auth,secrets,notify,scheduler,monitor,ldapauth,sso,registry,templates,…}
frontend/src
  pages/ components/ lib/ locales/
```

Every server operation goes through an `Executor` (local or SSH) and the Docker CLI, so local and remote servers behave the same. Routing is written as Traefik file-provider configuration, which is why domain changes apply instantly.

### Known limitations

- Databases run on a single server (PostgreSQL can add replicas on other servers) and one-click services too. Applications can run on several servers; balance traffic between them with DNS or an external load balancer.
- Pull request previews run on the primary server only.
- No Docker Swarm or Kubernetes.
- Point-in-time recovery is available for PostgreSQL; the other engines are backed up with logical dumps (test-restored automatically if you schedule it).

## Contributing

Issues and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). To report a security problem privately, see [SECURITY.md](SECURITY.md).

If PortDock is useful to you, a ⭐ on GitHub helps others find it.

## License

PortDock is free and open source under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE).

Developed by **Gavrilencu Grigore**.
