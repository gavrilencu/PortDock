-- PortDock schema v1. All columns are NOT NULL with defaults so they scan into plain Go types.

CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'member', -- owner | admin | member
  created_at    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sso_providers (
  id              INTEGER PRIMARY KEY,
  slug            TEXT NOT NULL UNIQUE,
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL DEFAULT 'oidc',
  issuer          TEXT NOT NULL,
  client_id       TEXT NOT NULL,
  client_secret   TEXT NOT NULL DEFAULT '', -- encrypted
  scopes          TEXT NOT NULL DEFAULT 'openid email profile',
  enabled         INTEGER NOT NULL DEFAULT 1,
  auto_register   INTEGER NOT NULL DEFAULT 0,
  default_role    TEXT NOT NULL DEFAULT 'member',
  allowed_domains TEXT NOT NULL DEFAULT '',
  groups_claim    TEXT NOT NULL DEFAULT 'groups',
  admin_groups    TEXT NOT NULL DEFAULT '',
  trust_email     INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS user_identities (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider_id INTEGER NOT NULL REFERENCES sso_providers(id) ON DELETE CASCADE,
  subject     TEXT NOT NULL,
  email       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT '',
  UNIQUE(provider_id, subject)
);

CREATE TABLE IF NOT EXISTS registries (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  kind       TEXT NOT NULL DEFAULT 'generic',
  host       TEXT NOT NULL,
  username   TEXT NOT NULL DEFAULT '',
  password   TEXT NOT NULL DEFAULT '', -- encrypted
  namespace  TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS custom_certificates (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  cert_pem   TEXT NOT NULL,
  key_pem    TEXT NOT NULL,            -- encrypted
  domains    TEXT NOT NULL DEFAULT '',
  issuer     TEXT NOT NULL DEFAULT '',
  not_before TEXT NOT NULL DEFAULT '',
  not_after  TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS ldap_directories (
  id              INTEGER PRIMARY KEY,
  name            TEXT NOT NULL,
  slug            TEXT NOT NULL UNIQUE,
  kind            TEXT NOT NULL DEFAULT 'ad',
  host            TEXT NOT NULL,
  port            INTEGER NOT NULL DEFAULT 636,
  security        TEXT NOT NULL DEFAULT 'ldaps',
  skip_verify     INTEGER NOT NULL DEFAULT 0,
  ca_cert         TEXT NOT NULL DEFAULT '',
  bind_dn         TEXT NOT NULL DEFAULT '',
  bind_password   TEXT NOT NULL DEFAULT '', -- encrypted
  base_dn         TEXT NOT NULL,
  user_filter     TEXT NOT NULL,
  email_attr      TEXT NOT NULL DEFAULT 'mail',
  name_attr       TEXT NOT NULL DEFAULT 'displayName',
  groups_attr     TEXT NOT NULL DEFAULT 'memberOf',
  required_groups TEXT NOT NULL DEFAULT '',
  admin_groups    TEXT NOT NULL DEFAULT '',
  nested_groups   INTEGER NOT NULL DEFAULT 1,
  auto_register   INTEGER NOT NULL DEFAULT 1,
  default_role    TEXT NOT NULL DEFAULT 'member',
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  last_used_at TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS ssh_keys (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  public_key  TEXT NOT NULL DEFAULT '',
  private_key TEXT NOT NULL DEFAULT '', -- encrypted
  created_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS servers (
  id              INTEGER PRIMARY KEY,
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  host            TEXT NOT NULL DEFAULT '',
  port            INTEGER NOT NULL DEFAULT 22,
  user            TEXT NOT NULL DEFAULT 'root',
  ssh_key_id      INTEGER NOT NULL DEFAULT 0,
  is_local        INTEGER NOT NULL DEFAULT 0,
  status          TEXT NOT NULL DEFAULT 'unknown', -- unknown | reachable | unreachable
  status_message  TEXT NOT NULL DEFAULT '',
  docker_version  TEXT NOT NULL DEFAULT '',
  proxy_enabled   INTEGER NOT NULL DEFAULT 1,
  proxy_status    TEXT NOT NULL DEFAULT 'unknown',
  wildcard_domain TEXT NOT NULL DEFAULT '',
  last_check_at   TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS projects (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS environments (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT '',
  UNIQUE(project_id, name)
);

CREATE TABLE IF NOT EXISTS project_members (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL DEFAULT 'developer', -- viewer | developer | maintainer
  created_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (project_id, user_id)
);

CREATE TABLE IF NOT EXISTS applications (
  id                    INTEGER PRIMARY KEY,
  uuid                  TEXT NOT NULL UNIQUE,
  environment_id        INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  server_id             INTEGER NOT NULL REFERENCES servers(id),
  name                  TEXT NOT NULL,
  description           TEXT NOT NULL DEFAULT '',
  source_type           TEXT NOT NULL DEFAULT 'git',        -- git | image
  git_repository        TEXT NOT NULL DEFAULT '',
  git_branch            TEXT NOT NULL DEFAULT 'main',
  git_commit            TEXT NOT NULL DEFAULT '',           -- pin to a commit (optional)
  git_token             TEXT NOT NULL DEFAULT '',           -- encrypted
  build_pack            TEXT NOT NULL DEFAULT 'dockerfile', -- dockerfile | nixpacks | static | compose | image
  base_directory        TEXT NOT NULL DEFAULT '/',
  dockerfile_path       TEXT NOT NULL DEFAULT 'Dockerfile',
  compose_path          TEXT NOT NULL DEFAULT 'docker-compose.yml',
  compose_service       TEXT NOT NULL DEFAULT '',
  docker_image          TEXT NOT NULL DEFAULT '',
  install_command       TEXT NOT NULL DEFAULT '',
  build_command         TEXT NOT NULL DEFAULT '',
  start_command         TEXT NOT NULL DEFAULT '',
  publish_directory     TEXT NOT NULL DEFAULT 'dist',
  port                  INTEGER NOT NULL DEFAULT 3000,
  ports_mapping         TEXT NOT NULL DEFAULT '',           -- e.g. "8080:80,9000:9000"
  domains               TEXT NOT NULL DEFAULT '',           -- comma separated
  https                 INTEGER NOT NULL DEFAULT 1,
  health_check_path     TEXT NOT NULL DEFAULT '',
  health_check_timeout  INTEGER NOT NULL DEFAULT 60,
  pre_deploy_command    TEXT NOT NULL DEFAULT '',
  post_deploy_command   TEXT NOT NULL DEFAULT '',
  watch_paths           TEXT NOT NULL DEFAULT '',           -- newline separated globs; deploy only when matched
  auto_deploy           INTEGER NOT NULL DEFAULT 1,
  preview_enabled       INTEGER NOT NULL DEFAULT 0,
  preview_domain        TEXT NOT NULL DEFAULT '',           -- e.g. "pr-{{pr}}.preview.example.com"
  webhook_secret        TEXT NOT NULL DEFAULT '',
  cpu_limit             TEXT NOT NULL DEFAULT '',
  memory_limit          TEXT NOT NULL DEFAULT '',
  replicas              INTEGER NOT NULL DEFAULT 1,
  custom_labels         TEXT NOT NULL DEFAULT '',
  custom_docker_options TEXT NOT NULL DEFAULT '',
  status                TEXT NOT NULL DEFAULT 'stopped',   -- stopped | running | deploying | failed | exited
  current_image         TEXT NOT NULL DEFAULT '',
  created_at            TEXT NOT NULL DEFAULT '',
  updated_at            TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS env_vars (
  id          INTEGER PRIMARY KEY,
  owner_type  TEXT NOT NULL,             -- application | service | database
  owner_id    INTEGER NOT NULL,
  service     TEXT NOT NULL DEFAULT '',  -- compose service of an application ('' = all)
  key         TEXT NOT NULL,
  value       TEXT NOT NULL DEFAULT '',  -- encrypted
  is_build    INTEGER NOT NULL DEFAULT 0,
  is_preview  INTEGER NOT NULL DEFAULT 0,
  is_secret   INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT '',
  UNIQUE(owner_type, owner_id, service, key, is_preview)
);

CREATE TABLE IF NOT EXISTS deployments (
  id             INTEGER PRIMARY KEY,
  uuid           TEXT NOT NULL UNIQUE,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  status         TEXT NOT NULL DEFAULT 'queued', -- queued | running | success | failed | cancelled
  trigger        TEXT NOT NULL DEFAULT 'manual', -- manual | webhook | api | rollback | schedule
  commit_sha     TEXT NOT NULL DEFAULT '',
  commit_message TEXT NOT NULL DEFAULT '',
  branch         TEXT NOT NULL DEFAULT '',
  pr_number      INTEGER NOT NULL DEFAULT 0,
  image          TEXT NOT NULL DEFAULT '',
  rollback_of    INTEGER NOT NULL DEFAULT 0,
  force_rebuild  INTEGER NOT NULL DEFAULT 0,
  logs           TEXT NOT NULL DEFAULT '',
  started_at     TEXT NOT NULL DEFAULT '',
  finished_at    TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_deployments_app ON deployments(application_id, id DESC);

-- Versions of an application: the images of each successful production build,
-- kept for rollbacks (the live one plus the latest few, for a limited time).
CREATE TABLE IF NOT EXISTS app_versions (
  id             INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  number         INTEGER NOT NULL,                  -- shown as v<number>
  deployment_id  INTEGER NOT NULL DEFAULT 0,        -- the build that produced it
  commit_sha     TEXT NOT NULL DEFAULT '',
  commit_message TEXT NOT NULL DEFAULT '',
  branch         TEXT NOT NULL DEFAULT '',
  image          TEXT NOT NULL DEFAULT '',          -- single-container applications
  services       TEXT NOT NULL DEFAULT '',          -- compose: JSON {"service": "image"} of built services
  compose_file   TEXT NOT NULL DEFAULT '',          -- compose: the compose file of that build
  status         TEXT NOT NULL DEFAULT 'available', -- available | expired
  created_at     TEXT NOT NULL DEFAULT '',
  UNIQUE(application_id, number)
);

CREATE TABLE IF NOT EXISTS databases (
  id             INTEGER PRIMARY KEY,
  uuid           TEXT NOT NULL UNIQUE,
  environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  server_id      INTEGER NOT NULL REFERENCES servers(id),
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  engine         TEXT NOT NULL,              -- postgres | mysql | mariadb | mongodb | redis | keydb | clickhouse
  image          TEXT NOT NULL,
  username       TEXT NOT NULL DEFAULT '',
  password       TEXT NOT NULL DEFAULT '',   -- encrypted
  database_name  TEXT NOT NULL DEFAULT '',
  public_port    INTEGER NOT NULL DEFAULT 0, -- 0 = not exposed
  cpu_limit      TEXT NOT NULL DEFAULT '',
  memory_limit   TEXT NOT NULL DEFAULT '',
  custom_args    TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'stopped',
  created_at     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS services (
  id             INTEGER PRIMARY KEY,
  uuid           TEXT NOT NULL UNIQUE,
  environment_id INTEGER NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  server_id      INTEGER NOT NULL REFERENCES servers(id),
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  template       TEXT NOT NULL DEFAULT 'custom',
  compose        TEXT NOT NULL DEFAULT '',
  domain         TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'stopped',
  created_at     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS backups (
  id           INTEGER PRIMARY KEY,
  database_id  INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
  schedule     TEXT NOT NULL DEFAULT '0 3 * * *',
  retention    INTEGER NOT NULL DEFAULT 7,
  upload_s3    INTEGER NOT NULL DEFAULT 0,
  enabled      INTEGER NOT NULL DEFAULT 1,
  last_run_at  TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS backup_runs (
  id          INTEGER PRIMARY KEY,
  backup_id   INTEGER NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'running',
  file        TEXT NOT NULL DEFAULT '',
  size        INTEGER NOT NULL DEFAULT 0,
  message     TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS notification_channels (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  type       TEXT NOT NULL,              -- discord | slack | telegram | webhook
  config     TEXT NOT NULL DEFAULT '',   -- encrypted JSON
  events     TEXT NOT NULL DEFAULT 'deployment.failed,deployment.success,backup.failed,server.unreachable',
  enabled    INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS activity (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL DEFAULT 0,
  action     TEXT NOT NULL,
  target     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT ''
);

-- GitHub Apps (created via the manifest flow or registered manually).
CREATE TABLE IF NOT EXISTS github_apps (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL,
  app_id         INTEGER NOT NULL,
  slug           TEXT NOT NULL DEFAULT '',
  owner          TEXT NOT NULL DEFAULT '',
  client_id      TEXT NOT NULL DEFAULT '',
  client_secret  TEXT NOT NULL DEFAULT '',  -- encrypted
  webhook_secret TEXT NOT NULL DEFAULT '',  -- encrypted
  private_key    TEXT NOT NULL DEFAULT '',  -- encrypted PEM
  html_url       TEXT NOT NULL DEFAULT 'https://github.com',
  api_url        TEXT NOT NULL DEFAULT 'https://api.github.com',
  created_at     TEXT NOT NULL DEFAULT '',
  UNIQUE(api_url, app_id)
);

CREATE TABLE IF NOT EXISTS github_installations (
  id              INTEGER PRIMARY KEY,
  github_app_id   INTEGER NOT NULL REFERENCES github_apps(id) ON DELETE CASCADE,
  installation_id INTEGER NOT NULL,
  account_login   TEXT NOT NULL DEFAULT '',
  account_type    TEXT NOT NULL DEFAULT '',
  account_avatar  TEXT NOT NULL DEFAULT '',
  repository_selection TEXT NOT NULL DEFAULT 'all',
  created_at      TEXT NOT NULL DEFAULT '',
  UNIQUE(github_app_id, installation_id)
);

-- Domains routed to an application (one row per host/path).
CREATE TABLE IF NOT EXISTS app_domains (
  id             INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  host           TEXT NOT NULL,
  path           TEXT NOT NULL DEFAULT '',
  port           INTEGER NOT NULL DEFAULT 0,        -- 0 = application port
  https          INTEGER NOT NULL DEFAULT 1,        -- issue a certificate and redirect HTTP -> HTTPS
  www_redirect   TEXT NOT NULL DEFAULT 'none',      -- none | to-www | to-apex
  indexing       INTEGER NOT NULL DEFAULT 1,        -- 0 adds X-Robots-Tag: noindex
  dns_status     TEXT NOT NULL DEFAULT 'unknown',   -- unknown | ok | mismatch | unresolved | proxied
  dns_detail     TEXT NOT NULL DEFAULT '',
  dns_checked_at TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT '',
  UNIQUE(host, path)
);

-- Persistent storage mounted into an application's containers: a Docker volume
-- managed by PortDock (pd-<app uuid>_<name>, one per server) or, when host_path
-- is set, a directory of the server (administrators only).
CREATE TABLE IF NOT EXISTS app_volumes (
  id             INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  mount_path     TEXT NOT NULL,
  host_path      TEXT NOT NULL DEFAULT '',
  read_only      INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT '',
  UNIQUE(application_id, name),
  UNIQUE(application_id, mount_path)
);

-- Additional servers an application is deployed to (besides applications.server_id).
CREATE TABLE IF NOT EXISTS application_servers (
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  server_id      INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  PRIMARY KEY (application_id, server_id)
);

-- Time series. scope: server | application | database | service; ref_id is the
-- resource id (the server id for scope=server). One row per (resource, server) per minute.
CREATE TABLE IF NOT EXISTS metrics (
  ts        INTEGER NOT NULL, -- unix seconds
  scope     TEXT NOT NULL,
  ref_id    INTEGER NOT NULL,
  server_id INTEGER NOT NULL,
  cpu       REAL NOT NULL DEFAULT 0, -- percent (servers: of all cores; containers: 100 = one core)
  mem       REAL NOT NULL DEFAULT 0, -- bytes
  mem_total REAL NOT NULL DEFAULT 0,
  disk      REAL NOT NULL DEFAULT 0,
  disk_total REAL NOT NULL DEFAULT 0,
  rx        REAL NOT NULL DEFAULT 0, -- bytes/s
  tx        REAL NOT NULL DEFAULT 0,
  load      REAL NOT NULL DEFAULT 0,
  restarts  INTEGER NOT NULL DEFAULT 0, -- container restarts since the previous sample
  up        REAL NOT NULL DEFAULT 1     -- 1 when every container is running
);
CREATE INDEX IF NOT EXISTS idx_metrics_ref ON metrics(scope, ref_id, ts);
CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics(ts);

-- Hourly rollups (averages, plus peaks) kept for long ranges.
CREATE TABLE IF NOT EXISTS metrics_hourly (
  ts        INTEGER NOT NULL,
  scope     TEXT NOT NULL,
  ref_id    INTEGER NOT NULL,
  server_id INTEGER NOT NULL,
  cpu       REAL NOT NULL DEFAULT 0,
  cpu_max   REAL NOT NULL DEFAULT 0,
  mem       REAL NOT NULL DEFAULT 0,
  mem_max   REAL NOT NULL DEFAULT 0,
  mem_total REAL NOT NULL DEFAULT 0,
  disk      REAL NOT NULL DEFAULT 0,
  disk_total REAL NOT NULL DEFAULT 0,
  rx        REAL NOT NULL DEFAULT 0,
  tx        REAL NOT NULL DEFAULT 0,
  load      REAL NOT NULL DEFAULT 0,
  restarts  INTEGER NOT NULL DEFAULT 0,
  up        REAL NOT NULL DEFAULT 1,
  PRIMARY KEY (scope, ref_id, server_id, ts)
);

CREATE TABLE IF NOT EXISTS alert_rules (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  scope        TEXT NOT NULL,             -- server | application | database | service
  target_id    INTEGER NOT NULL DEFAULT 0, -- 0 = every resource of the scope
  metric       TEXT NOT NULL,             -- cpu | memory | disk | load | restarts | down
  threshold    REAL NOT NULL DEFAULT 0,
  duration     INTEGER NOT NULL DEFAULT 5, -- minutes the condition must hold
  severity     TEXT NOT NULL DEFAULT 'warning', -- warning | critical
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS alert_events (
  id          INTEGER PRIMARY KEY,
  rule_id     INTEGER NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  scope       TEXT NOT NULL,
  ref_id      INTEGER NOT NULL,
  server_id   INTEGER NOT NULL DEFAULT 0,
  target_name TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'firing', -- firing | resolved
  value       REAL NOT NULL DEFAULT 0,
  message     TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL,
  resolved_at TEXT NOT NULL DEFAULT '',
  acknowledged_by INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_alert_events_open ON alert_events(status, rule_id, scope, ref_id, server_id);

-- AI assistant: each user's conversations with the configured OpenRouter model.
CREATE TABLE IF NOT EXISTS chat_conversations (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_chat_conversations_user ON chat_conversations(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS chat_messages (
  id              INTEGER PRIMARY KEY,
  conversation_id INTEGER NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
  role            TEXT NOT NULL,                 -- user | assistant
  content         TEXT NOT NULL DEFAULT '',
  model           TEXT NOT NULL DEFAULT '',
  error           INTEGER NOT NULL DEFAULT 0,    -- the reply was cut off by an error
  created_at      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_conversation ON chat_messages(conversation_id, id);

-- Azure DevOps organizations (or on-premises collections), reached with a personal access token.
CREATE TABLE IF NOT EXISTS azure_connections (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  url        TEXT NOT NULL,
  token      TEXT NOT NULL DEFAULT '',   -- encrypted
  created_at TEXT NOT NULL DEFAULT ''
);

-- Vulnerability scans (Trivy) of application images.
CREATE TABLE IF NOT EXISTS image_scans (
  id             INTEGER PRIMARY KEY,
  application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  deployment_id  INTEGER NOT NULL DEFAULT 0,
  service        TEXT NOT NULL DEFAULT '',         -- compose service
  image          TEXT NOT NULL DEFAULT '',
  trigger        TEXT NOT NULL DEFAULT 'manual',   -- deploy | manual
  status         TEXT NOT NULL DEFAULT 'running',  -- running | success | failed
  critical       INTEGER NOT NULL DEFAULT 0,
  high           INTEGER NOT NULL DEFAULT 0,
  medium         INTEGER NOT NULL DEFAULT 0,
  low            INTEGER NOT NULL DEFAULT 0,
  unknown        INTEGER NOT NULL DEFAULT 0,
  fixable        INTEGER NOT NULL DEFAULT 0,
  findings       TEXT NOT NULL DEFAULT '',         -- JSON list of vulnerabilities
  error          TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT '',
  finished_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_image_scans_app ON image_scans(application_id, id);

-- Uptime monitors: HTTP(S) checks of a URL, listed on the public status page when public.
CREATE TABLE IF NOT EXISTS monitors (
  id                   INTEGER PRIMARY KEY,
  name                 TEXT NOT NULL,
  url                  TEXT NOT NULL,
  application_id       INTEGER NOT NULL DEFAULT 0,
  interval_seconds     INTEGER NOT NULL DEFAULT 60,
  timeout_seconds      INTEGER NOT NULL DEFAULT 10,
  expected_status      TEXT NOT NULL DEFAULT '200-399',
  keyword              TEXT NOT NULL DEFAULT '',        -- the body must contain it
  ssl_warn_days        INTEGER NOT NULL DEFAULT 14,
  failures_to_alert    INTEGER NOT NULL DEFAULT 2,      -- consecutive failed checks before "down"
  enabled              INTEGER NOT NULL DEFAULT 1,
  public               INTEGER NOT NULL DEFAULT 1,
  status               TEXT NOT NULL DEFAULT 'pending', -- pending | up | down | paused
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_checked_at      TEXT NOT NULL DEFAULT '',
  last_change_at       TEXT NOT NULL DEFAULT '',
  last_response_ms     INTEGER NOT NULL DEFAULT 0,
  last_status_code     INTEGER NOT NULL DEFAULT 0,
  last_error           TEXT NOT NULL DEFAULT '',
  ssl_expires_at       TEXT NOT NULL DEFAULT '',
  ssl_warned_at        TEXT NOT NULL DEFAULT '',
  created_at           TEXT NOT NULL DEFAULT ''
);

-- Raw checks, kept 7 days (response time chart).
CREATE TABLE IF NOT EXISTS monitor_checks (
  id          INTEGER PRIMARY KEY,
  monitor_id  INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  ok          INTEGER NOT NULL DEFAULT 0,
  status_code INTEGER NOT NULL DEFAULT 0,
  response_ms INTEGER NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_monitor_checks ON monitor_checks(monitor_id, created_at);

-- Daily uptime per monitor, kept 90 days (status page bars).
CREATE TABLE IF NOT EXISTS monitor_days (
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  day        TEXT NOT NULL,                       -- YYYY-MM-DD (UTC)
  checks     INTEGER NOT NULL DEFAULT 0,
  failures   INTEGER NOT NULL DEFAULT 0,
  total_ms   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (monitor_id, day)
);

-- Outages: from the check that marked the monitor down to the one that brought it back.
CREATE TABLE IF NOT EXISTS monitor_incidents (
  id          INTEGER PRIMARY KEY,
  monitor_id  INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  error       TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_monitor_incidents ON monitor_incidents(monitor_id, started_at);
