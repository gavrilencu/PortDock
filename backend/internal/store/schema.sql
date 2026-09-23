-- Shipyard schema v1. All columns are NOT NULL with defaults so they scan into plain Go types.

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
  key         TEXT NOT NULL,
  value       TEXT NOT NULL DEFAULT '',  -- encrypted
  is_build    INTEGER NOT NULL DEFAULT 0,
  is_preview  INTEGER NOT NULL DEFAULT 0,
  is_secret   INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT '',
  UNIQUE(owner_type, owner_id, key, is_preview)
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
