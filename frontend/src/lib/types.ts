// Types mirror the Go API (backend/internal/store/models.go and api DTOs).

export type Role = "owner" | "admin" | "member";

export interface User {
  id: number;
  email: string;
  name: string;
  role: Role;
  created_at: string;
  totp_enabled: boolean;
  auth_source: string;
  disabled: boolean;
  last_login_at: string;
  has_password: boolean;
  locale: string;
}

export interface AuthStatus {
  setup_required: boolean;
  version: string;
  /** GitHub repository (owner/name) of this build. */
  repo?: string;
  user?: User;
  mfa_setup_required?: boolean;
}

export type ProjectRole = "admin" | "maintainer" | "developer" | "viewer" | "none";

export interface Access {
  role: ProjectRole;
  can_deploy: boolean;
  can_write: boolean;
  can_manage: boolean;
  deploy_needs_approval: boolean;
  protected: boolean;
}

export interface LoginProviders {
  password_login: boolean;
  providers: { slug: string; name: string; kind: string }[];
  ldap?: { slug: string; name: string; kind: string }[];
}

export interface TwoFactorStatus {
  enabled: boolean;
  required: boolean;
  recovery_codes_left: number;
}

export interface UserSession {
  id: string;
  current: boolean;
  ip: string;
  user_agent: string;
  method: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
}

export interface SSOProvider {
  id: number;
  slug: string;
  name: string;
  kind: string;
  issuer: string;
  client_id: string;
  client_secret_set: boolean;
  scopes: string;
  enabled: boolean;
  auto_register: boolean;
  default_role: "admin" | "member";
  allowed_domains: string;
  groups_claim: string;
  admin_groups: string;
  trust_email: boolean;
  redirect_uri: string;
  users: number;
  created_at: string;
}

export interface SSOPreset {
  kind: string;
  name: string;
  issuer: string;
  scopes: string;
  groups_claim: string;
  docs: string;
}

export interface SSHKey {
  id: number;
  name: string;
  public_key: string;
  created_at: string;
}

export interface Server {
  id: number;
  name: string;
  description: string;
  host: string;
  port: number;
  user: string;
  ssh_key_id: number;
  is_local: boolean;
  status: "unknown" | "reachable" | "unreachable";
  status_message: string;
  docker_version: string;
  proxy_enabled: boolean;
  proxy_status: string;
  wildcard_domain: string;
  last_check_at: string;
  created_at: string;
}

export interface Environment {
  id: number;
  project_id: number;
  name: string;
  protected: boolean;
  require_approval: boolean;
  created_at: string;
}

export interface Project {
  id: number;
  name: string;
  description: string;
  default_role: ProjectRole;
  created_at: string;
  environments: Environment[];
  resources: number;
  role: ProjectRole;
}

export type BuildPack = "auto" | "dockerfile" | "compose" | "node" | "bun" | "python" | "go" | "static" | "shell" | "nixpacks" | "image";

export interface Application {
  id: number;
  uuid: string;
  environment_id: number;
  server_id: number;
  name: string;
  description: string;
  source_type: "git" | "image";
  git_repository: string;
  git_branch: string;
  git_commit: string;
  has_git_token: boolean;
  github_app_id: number;
  github_installation_id: number;
  git_full_name: string;
  azure_connection_id: number;
  azure_project_id: string;
  azure_repo_id: string;
  azure_hook_id: string;
  azure_hook_error: string;
  scan_on_deploy: boolean;
  scan_block: "" | "critical" | "high";
  scan_ignore_unfixed: boolean;
  build_pack: BuildPack;
  base_directory: string;
  dockerfile_path: string;
  compose_path: string;
  compose_service: string;
  docker_image: string;
  install_command: string;
  build_command: string;
  start_command: string;
  publish_directory: string;
  port: number;
  ports_mapping: string;
  domains: string;
  https: boolean;
  health_check_path: string;
  health_check_timeout: number;
  pre_deploy_command: string;
  post_deploy_command: string;
  watch_paths: string;
  auto_deploy: boolean;
  preview_enabled: boolean;
  preview_domain: string;
  webhook_secret: string;
  cpu_limit: string;
  memory_limit: string;
  replicas: number;
  custom_labels: string;
  custom_docker_options: string;
  status: string;
  current_image: string;
  created_at: string;
  updated_at: string;
  registry_id: number;
  registry_image: string;
  build_server_id: number;
}

export interface ServerRef {
  id: number;
  name: string;
  host: string;
  status: string;
}

export interface Registry {
  id: number;
  name: string;
  kind: string;
  host: string;
  username: string;
  namespace: string;
  password_set: boolean;
  apps: number;
  created_at: string;
}

export interface RegistryKind {
  kind: string;
  name: string;
  host: string;
  username_label: string;
  password_label: string;
  hint: string;
}

export interface ApplicationDetail extends Application {
  webhooks: { github: string; gitlab: string; gitea: string; azure: string; deploy: string };
  project: { id: number; name: string };
  environment: Environment;
  server_name: string;
  server_host: string;
  servers: ServerRef[];
  registry_name: string;
  registry_repository: string;
  access: Access;
}

export type DeploymentStatus = "queued" | "running" | "success" | "failed" | "cancelled" | "pending_approval" | "rejected";

export interface Deployment {
  id: number;
  uuid: string;
  application_id: number;
  status: DeploymentStatus;
  trigger: string;
  commit_sha: string;
  commit_message: string;
  branch: string;
  pr_number: number;
  image: string;
  rollback_of: number;
  force_rebuild: boolean;
  logs?: string;
  requested_by: number;
  reviewed_by: number;
  reviewed_at: string;
  review_comment: string;
  requested_by_name?: string;
  reviewed_by_name?: string;
  can_review?: boolean;
  can_self_approve?: boolean;
  started_at: string;
  finished_at: string;
  created_at: string;
  application_name?: string;
}

export interface Preview {
  pr_number: number;
  branch: string;
  domains: string[] | null;
  last_deployment: Deployment;
}

export interface EnvVar {
  id: number;
  owner_type: string;
  owner_id: number;
  /** Docker Compose service the variable belongs to ("" = the whole application). */
  service: string;
  key: string;
  value: string;
  is_build: boolean;
  is_preview: boolean;
  is_secret: boolean;
  created_at: string;
}

export interface Engine {
  id: string;
  name: string;
  default_image: string;
  port: number;
  has_user: boolean;
  has_database: boolean;
  backup_ext: string;
}

export interface Database {
  id: number;
  uuid: string;
  environment_id: number;
  server_id: number;
  name: string;
  description: string;
  engine: string;
  image: string;
  username: string;
  database_name: string;
  public_port: number;
  public_host: string;
  cpu_limit: string;
  memory_limit: string;
  custom_args: string;
  status: string;
  created_at: string;
}

export interface DatabaseDetail extends Database {
  password: string;
  internal_url: string;
  public_url: string;
  hostname: string;
  port: number;
  engine_name: string;
  project: { id: number; name: string };
  environment: Environment;
  server_name: string;
  access: Access;
  public_address: string;
  recreated?: boolean;
  dns_status?: DNSStatus;
  dns_detail?: string;
}

export interface Backup {
  id: number;
  database_id: number;
  schedule: string;
  retention: number;
  upload_s3: boolean;
  enabled: boolean;
  last_run_at: string;
  created_at: string;
}

export interface BackupRun {
  id: number;
  backup_id: number;
  kind: "scheduled" | "manual" | "pre-restore";
  status: "pending" | "running" | "success" | "failed";
  /** Path of the backup file on the database's server. */
  file: string;
  size: number;
  message: string;
  created_at: string;
  started_at: string;
  finished_at: string;
  restore_status: "" | "pending" | "running" | "success" | "failed";
  restore_message: string;
  restored_at: string;
}

export interface BackupRunsResponse {
  runs: BackupRun[];
  /** Directory of the backups on the server. */
  directory: string;
  server: string;
  supported: boolean;
  /** Whether PortDock can restore this engine's backups. */
  restore: boolean;
  format: string;
}

export interface ServiceTemplate {
  slug: string;
  name: string;
  description: string;
  category: string;
  website: string;
  compose: string;
}

export interface Service {
  id: number;
  uuid: string;
  environment_id: number;
  server_id: number;
  name: string;
  description: string;
  template: string;
  compose: string;
  domain: string;
  status: string;
  created_at: string;
}

export interface ServiceDetail extends Service {
  project: { id: number; name: string };
  environment: Environment;
  server_name: string;
  access: Access;
}

export interface EnvironmentResources {
  environment: Environment;
  applications: Application[];
  databases: Database[];
  services: Service[];
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  ports: string;
  networks: string;
  created_at: string;
  labels: Record<string, string>;
  server_id?: number;
  server_name?: string;
}

export interface ContainerStat {
  container: string;
  name: string;
  cpu: string;
  mem_usage: string;
  mem: string;
  net_io: string;
  block_io: string;
  pids: string;
}

export interface Metrics {
  cpus: number;
  load1: number;
  load5: number;
  load15: number;
  mem_total: number;
  mem_used: number;
  disk_total: number;
  disk_used: number;
  uptime: number;
  os: string;
  kernel: string;
  containers: number;
  running_containers: number;
}

export interface DockerImage {
  id: string;
  repository: string;
  tag: string;
  size: string;
  created_at: string;
}

export interface Volume {
  name: string;
  driver: string;
  labels: string;
}

export interface DockerNetwork {
  id: string;
  name: string;
  driver: string;
  scope: string;
}

export interface Dashboard {
  counts: Record<
    "projects" | "applications" | "databases" | "services" | "servers" | "running" | "deployments_today" | "failed_today",
    number
  >;
  servers: Server[];
  recent_deployments: Deployment[];
}

export interface APIToken {
  id: number;
  name: string;
  last_used_at: string;
  created_at: string;
}

export interface NotificationChannel {
  id: number;
  name: string;
  type: "discord" | "slack" | "teams" | "telegram" | "webhook" | "email";
  events: string;
  enabled: boolean;
  config: Record<string, string>;
  created_at: string;
}

export interface Settings {
  instance_name: string;
  instance_url: string;
  instance_https: string;
  instance_timezone: string;
  instance_route_error: string;
  public_ipv4: string;
  public_ipv6: string;
  env_public_url: string;
  instance_domain: string;
  acme_email: string;
  auto_cleanup: string;
  proxy_dashboard: string;
  require_2fa: "" | "off" | "admins" | "all";
  password_login: string;
  s3_endpoint: string;
  s3_bucket: string;
  s3_region: string;
  s3_access_key: string;
  s3_secret_key_set: boolean;
  trivy_image?: string;
  status_page_enabled?: string;
  status_page_title?: string;
  status_page_description?: string;
  public_url: string;
  version: string;
}

export interface ActivityEntry {
  id: number;
  user_id: number;
  user_name: string;
  user_email: string;
  action: string;
  target: string;
  ip: string;
  created_at: string;
}

export interface GitHubInstallation {
  id: number;
  github_app_id: number;
  installation_id: number;
  account_login: string;
  account_type: string;
  account_avatar: string;
  repository_selection: string;
  created_at: string;
}

export interface GitHubApp {
  id: number;
  name: string;
  app_id: number;
  slug: string;
  owner: string;
  client_id: string;
  html_url: string;
  api_url: string;
  created_at: string;
  installations: GitHubInstallation[];
  install_url: string;
  settings_url: string;
  applications: number;
  webhook_url: string;
  webhook_configured: boolean;
  public_url: boolean;
}

export interface Monitor {
  id: number;
  name: string;
  url: string;
  application_id: number;
  interval_seconds: number;
  timeout_seconds: number;
  expected_status: string;
  keyword: string;
  ssl_warn_days: number;
  failures_to_alert: number;
  enabled: boolean;
  public: boolean;
  status: "pending" | "up" | "down" | "paused";
  consecutive_failures: number;
  last_checked_at: string;
  last_change_at: string;
  last_response_ms: number;
  last_status_code: number;
  last_error: string;
  ssl_expires_at: string;
  created_at: string;
  uptime_24h: number | null;
  uptime_30d: number | null;
  recent: { ok: boolean; response_ms: number; created_at: string }[];
}

export interface MonitorCheck {
  id: number;
  ok: boolean;
  status_code: number;
  response_ms: number;
  error: string;
  created_at: string;
}

export interface MonitorIncident {
  id: number;
  monitor_id: number;
  error: string;
  started_at: string;
  resolved_at: string;
}

export interface ImageScan {
  id: number;
  application_id: number;
  deployment_id: number;
  service: string;
  image: string;
  trigger: "deploy" | "manual";
  status: "running" | "success" | "failed";
  critical: number;
  high: number;
  medium: number;
  low: number;
  unknown: number;
  fixable: number;
  error: string;
  created_at: string;
  finished_at: string;
}

export interface ScanFinding {
  id: string;
  severity: string;
  package: string;
  installed: string;
  fixed: string;
  title: string;
  url: string;
  target: string;
}

export interface AzureConnection {
  id: number;
  name: string;
  url: string;
  created_at: string;
  applications: number;
}

export interface AzureRepo {
  id: string;
  name: string;
  project: { id: string; name: string };
  default_branch: string;
  remote_url: string;
  web_url: string;
}

export interface AzureHook {
  hook_id: string;
  hooks: number;
  events: number;
  error: string;
  url: string;
  header: string;
}

export interface GitHubRepo {
  github_app_id: number;
  installation_id: number;
  id: number;
  name: string;
  full_name: string;
  owner: string;
  owner_avatar: string;
  private: boolean;
  default_branch: string;
  description: string;
  language: string;
  html_url: string;
  clone_url: string;
  pushed_at: string;
  archived: boolean;
}

export interface GitHubBranch {
  name: string;
  protected: boolean;
  sha: string;
}

export interface BuildSuggestion {
  build_pack: BuildPack;
  port: number;
  reason: string;
  install_command?: string;
  build_command?: string;
  start_command?: string;
  publish_directory?: string;
  compose_path?: string;
  compose_services?: ComposeService[];
  framework?: string;
  files: string[];
}

/** A service of a compose file and the container port it declares (0 = unknown). */
export interface ComposeService {
  name: string;
  port: number;
  /** `environment:` of the service in docker-compose.yml. */
  env?: Record<string, string>;
}

export interface CatalogEnv {
  key: string;
  value: string;
  secret?: boolean;
  required?: boolean;
  hint?: string;
}

export interface CatalogImage {
  id: string;
  name: string;
  image: string;
  category: string;
  description: string;
  port: number;
  command?: string;
  options?: string;
  env?: CatalogEnv[];
  volumes?: string[];
  website?: string;
  managed?: string;
}

export interface ImageCatalog {
  categories: string[];
  images: CatalogImage[];
}

export type WWWRedirect = "none" | "to-www" | "to-apex";
export type DNSStatus = "unknown" | "ok" | "mismatch" | "unresolved" | "proxied" | "local";

export interface AppDomain {
  id: number;
  application_id: number;
  host: string;
  path: string;
  port: number;
  /** Docker Compose service receiving the traffic ("" = the main service). */
  service: string;
  https: boolean;
  www_redirect: WWWRedirect;
  indexing: boolean;
  dns_status: DNSStatus;
  dns_detail: string;
  dns_checked_at: string;
  cert_mode: CertMode | "";
  cert_status: CertStatus;
  cert_issuer: string;
  cert_expires_at: string;
  cert_checked_at: string;
  cert_error: string;
  created_at: string;
}

export type CertMode = "auto" | "dns" | "custom";
export type CertStatus = "" | "valid" | "expiring" | "expired" | "pending" | "missing" | "local" | "error";

export interface DomainsResponse {
  domains: AppDomain[];
  requires_deploy: boolean;
  routing_warning?: string;
  server_addresses: string[];
}

export interface DNSRecord {
  type: "A" | "AAAA";
  name: string;
  value: string;
  for: string;
}

/** A saved, deployable version of an application (v1, v2…). */
export interface AppVersion {
  id: number;
  application_id: number;
  number: number;
  tag: string;
  deployment_id: number;
  commit_sha: string;
  commit_message: string;
  branch: string;
  /** Image of single-container applications ("compose@vN" for compose). */
  image: string;
  /** Compose: image of each built service. */
  services: Record<string, string>;
  status: "available" | "expired";
  created_at: string;
  live: boolean;
  /** When a previous version is removed. */
  expires_at?: string;
}

export interface VersionsResponse {
  versions: AppVersion[];
  live_version_id: number;
  kept: number;
  max_age_days: number;
  pending?: { deployment_id: number; version_id: number; status: string; trigger: string };
}

// ---------- AI assistant ----------

export interface ChatStatus {
  enabled: boolean;
  /** Enabled, configured and this user is permitted. */
  allowed: boolean;
  model: string;
}

export interface ChatSettings {
  enabled: boolean;
  api_key_set: boolean;
  model: string;
  access: "all" | "admins" | "selected";
  user_ids: number[];
  instructions: string;
}

export interface ChatModel {
  id: string;
  name: string;
  context_length: number;
  /** USD per token as a decimal string; "0" = free. */
  prompt_price: string;
  completion_price: string;
  description?: string;
}

export interface ChatConversation {
  id: number;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface ChatMessage {
  id: number;
  conversation_id: number;
  role: "user" | "assistant";
  content: string;
  model: string;
  created_at: string;
  error?: boolean;
}
