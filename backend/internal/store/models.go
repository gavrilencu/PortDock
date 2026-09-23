package store

import "strings"

type User struct {
	ID           int64  `db:"id" json:"id"`
	Email        string `db:"email" json:"email"`
	Name         string `db:"name" json:"name"`
	PasswordHash string `db:"password_hash" json:"-"`
	Role         string `db:"role" json:"role"`
	CreatedAt    string `db:"created_at" json:"created_at"`
	// TOTPSecret is encrypted; it is set (but not yet enabled) during 2FA enrollment.
	TOTPSecret   string `db:"totp_secret" json:"-"`
	TOTPEnabled  bool   `db:"totp_enabled" json:"totp_enabled"`
	TOTPLastStep int64  `db:"totp_last_step" json:"-"`
	// RecoveryCodes holds comma-separated SHA-256 hashes of unused recovery codes.
	RecoveryCodes string `db:"recovery_codes" json:"-"`
	// AuthSource is "" for local accounts or the slug of the SSO provider that created it.
	AuthSource  string `db:"auth_source" json:"auth_source"`
	Disabled    bool   `db:"disabled" json:"disabled"`
	LastLoginAt string `db:"last_login_at" json:"last_login_at"`
	// Locale is the interface language chosen by the user ("" = default, English).
	Locale string `db:"locale" json:"locale"`
}

func (u User) HasPassword() bool { return u.PasswordHash != "" }

// Session is a browser login. ID is a short prefix of the token hash.
type Session struct {
	TokenHash  string `db:"token_hash" json:"-"`
	UserID     int64  `db:"user_id" json:"user_id"`
	ExpiresAt  string `db:"expires_at" json:"expires_at"`
	CreatedAt  string `db:"created_at" json:"created_at"`
	LastSeenAt string `db:"last_seen_at" json:"last_seen_at"`
	IP         string `db:"ip" json:"ip"`
	UserAgent  string `db:"user_agent" json:"user_agent"`
	// Method is how the session was established: password, password+totp, password+recovery or sso:<slug>.
	Method string `db:"method" json:"method"`
}

// SSOProvider is an OpenID Connect identity provider (Entra ID, Okta, Google, Keycloak…).
type SSOProvider struct {
	ID           int64  `db:"id" json:"id"`
	Slug         string `db:"slug" json:"slug"`
	Name         string `db:"name" json:"name"`
	Kind         string `db:"kind" json:"kind"` // preset: entra | google | okta | keycloak | authentik | gitlab | oidc
	Issuer       string `db:"issuer" json:"issuer"`
	ClientID     string `db:"client_id" json:"client_id"`
	ClientSecret string `db:"client_secret" json:"-"` // encrypted
	Scopes       string `db:"scopes" json:"scopes"`
	Enabled      bool   `db:"enabled" json:"enabled"`
	// AutoRegister creates accounts on first login (just-in-time provisioning).
	AutoRegister bool   `db:"auto_register" json:"auto_register"`
	DefaultRole  string `db:"default_role" json:"default_role"`
	// AllowedDomains restricts logins to these email domains (comma-separated; empty = any).
	AllowedDomains string `db:"allowed_domains" json:"allowed_domains"`
	GroupsClaim    string `db:"groups_claim" json:"groups_claim"`
	// AdminGroups grants the admin role to members of these groups (synced on every login).
	AdminGroups string `db:"admin_groups" json:"admin_groups"`
	// TrustEmail links existing accounts by email when the provider does not send email_verified.
	TrustEmail bool   `db:"trust_email" json:"trust_email"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}

// UserIdentity links a user to a subject at an SSO provider.
type UserIdentity struct {
	ID         int64  `db:"id" json:"id"`
	UserID     int64  `db:"user_id" json:"user_id"`
	ProviderID int64  `db:"provider_id" json:"provider_id"`
	Subject    string `db:"subject" json:"subject"`
	Email      string `db:"email" json:"email"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}

func (u User) IsAdmin() bool { return u.Role == "owner" || u.Role == "admin" }

type APIToken struct {
	ID         int64  `db:"id" json:"id"`
	UserID     int64  `db:"user_id" json:"user_id"`
	Name       string `db:"name" json:"name"`
	TokenHash  string `db:"token_hash" json:"-"`
	LastUsedAt string `db:"last_used_at" json:"last_used_at"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}

type SSHKey struct {
	ID         int64  `db:"id" json:"id"`
	Name       string `db:"name" json:"name"`
	PublicKey  string `db:"public_key" json:"public_key"`
	PrivateKey string `db:"private_key" json:"-"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}

type Server struct {
	ID             int64  `db:"id" json:"id"`
	Name           string `db:"name" json:"name"`
	Description    string `db:"description" json:"description"`
	Host           string `db:"host" json:"host"`
	Port           int    `db:"port" json:"port"`
	User           string `db:"user" json:"user"`
	SSHKeyID       int64  `db:"ssh_key_id" json:"ssh_key_id"`
	IsLocal        bool   `db:"is_local" json:"is_local"`
	Status         string `db:"status" json:"status"`
	StatusMessage  string `db:"status_message" json:"status_message"`
	DockerVersion  string `db:"docker_version" json:"docker_version"`
	ProxyEnabled   bool   `db:"proxy_enabled" json:"proxy_enabled"`
	ProxyStatus    string `db:"proxy_status" json:"proxy_status"`
	WildcardDomain string `db:"wildcard_domain" json:"wildcard_domain"`
	LastCheckAt    string `db:"last_check_at" json:"last_check_at"`
	CreatedAt      string `db:"created_at" json:"created_at"`
}

type Project struct {
	ID          int64  `db:"id" json:"id"`
	Name        string `db:"name" json:"name"`
	Description string `db:"description" json:"description"`
	// DefaultRole applies to members without an explicit project role:
	// none (private project) | viewer | developer | maintainer.
	DefaultRole string `db:"default_role" json:"default_role"`
	CreatedAt   string `db:"created_at" json:"created_at"`
}

type Environment struct {
	ID        int64  `db:"id" json:"id"`
	ProjectID int64  `db:"project_id" json:"project_id"`
	Name      string `db:"name" json:"name"`
	// Protected environments can only be changed by maintainers; developer
	// deployments become approval requests.
	Protected bool `db:"protected" json:"protected"`
	// RequireApproval holds every deployment (including git pushes) until a
	// maintainer other than the requester approves it.
	RequireApproval bool   `db:"require_approval" json:"require_approval"`
	CreatedAt       string `db:"created_at" json:"created_at"`
}

// ProjectMember grants a user a role in one project.
type ProjectMember struct {
	ProjectID int64  `db:"project_id" json:"project_id"`
	UserID    int64  `db:"user_id" json:"user_id"`
	Role      string `db:"role" json:"role"` // viewer | developer | maintainer
	CreatedAt string `db:"created_at" json:"created_at"`
}

type Application struct {
	ID                  int64  `db:"id" json:"id"`
	UUID                string `db:"uuid" json:"uuid"`
	EnvironmentID       int64  `db:"environment_id" json:"environment_id"`
	ServerID            int64  `db:"server_id" json:"server_id"`
	Name                string `db:"name" json:"name"`
	Description         string `db:"description" json:"description"`
	SourceType          string `db:"source_type" json:"source_type"`
	GitRepository       string `db:"git_repository" json:"git_repository"`
	GitBranch           string `db:"git_branch" json:"git_branch"`
	GitCommit           string `db:"git_commit" json:"git_commit"`
	GitToken            string `db:"git_token" json:"-"`
	GitHubAppID         int64  `db:"github_app_id" json:"github_app_id"`
	GitHubInstallation  int64  `db:"github_installation_id" json:"github_installation_id"`
	GitFullName         string `db:"git_full_name" json:"git_full_name"`
	BuildPack           string `db:"build_pack" json:"build_pack"`
	BaseDirectory       string `db:"base_directory" json:"base_directory"`
	DockerfilePath      string `db:"dockerfile_path" json:"dockerfile_path"`
	ComposePath         string `db:"compose_path" json:"compose_path"`
	ComposeService      string `db:"compose_service" json:"compose_service"`
	DockerImage         string `db:"docker_image" json:"docker_image"`
	InstallCommand      string `db:"install_command" json:"install_command"`
	BuildCommand        string `db:"build_command" json:"build_command"`
	StartCommand        string `db:"start_command" json:"start_command"`
	PublishDirectory    string `db:"publish_directory" json:"publish_directory"`
	Port                int    `db:"port" json:"port"`
	PortsMapping        string `db:"ports_mapping" json:"ports_mapping"`
	Domains             string `db:"domains" json:"domains"`
	HTTPS               bool   `db:"https" json:"https"`
	HealthCheckPath     string `db:"health_check_path" json:"health_check_path"`
	HealthCheckTimeout  int    `db:"health_check_timeout" json:"health_check_timeout"`
	PreDeployCommand    string `db:"pre_deploy_command" json:"pre_deploy_command"`
	PostDeployCommand   string `db:"post_deploy_command" json:"post_deploy_command"`
	WatchPaths          string `db:"watch_paths" json:"watch_paths"`
	AutoDeploy          bool   `db:"auto_deploy" json:"auto_deploy"`
	PreviewEnabled      bool   `db:"preview_enabled" json:"preview_enabled"`
	PreviewDomain       string `db:"preview_domain" json:"preview_domain"`
	WebhookSecret       string `db:"webhook_secret" json:"webhook_secret"`
	CPULimit            string `db:"cpu_limit" json:"cpu_limit"`
	MemoryLimit         string `db:"memory_limit" json:"memory_limit"`
	Replicas            int    `db:"replicas" json:"replicas"`
	CustomLabels        string `db:"custom_labels" json:"custom_labels"`
	CustomDockerOptions string `db:"custom_docker_options" json:"custom_docker_options"`
	Status              string `db:"status" json:"status"`
	CurrentImage        string `db:"current_image" json:"current_image"`
	// RegistryID pushes built images to (and pulls private images from) this registry.
	RegistryID int64 `db:"registry_id" json:"registry_id"`
	// RegistryImage overrides the repository path (default: <namespace>/<app name>).
	RegistryImage string `db:"registry_image" json:"registry_image"`
	// BuildServerID builds on a dedicated server (0 = the primary server).
	BuildServerID int64  `db:"build_server_id" json:"build_server_id"`
	CreatedAt     string `db:"created_at" json:"created_at"`
	UpdatedAt     string `db:"updated_at" json:"updated_at"`

	HasGitToken bool `db:"-" json:"has_git_token"`
}

func (a Application) DomainList() []string { return SplitList(a.Domains) }

// Registry is a container registry used to push built images and pull private ones.
type Registry struct {
	ID        int64  `db:"id" json:"id"`
	Name      string `db:"name" json:"name"`
	Kind      string `db:"kind" json:"kind"` // dockerhub | ghcr | gitlab | harbor | quay | ecr | generic
	Host      string `db:"host" json:"host"`
	Username  string `db:"username" json:"username"` // ECR: access key ID
	Password  string `db:"password" json:"-"`        // encrypted token/password; ECR: secret access key
	Namespace string `db:"namespace" json:"namespace"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

type EnvVar struct {
	ID        int64  `db:"id" json:"id"`
	OwnerType string `db:"owner_type" json:"owner_type"`
	OwnerID   int64  `db:"owner_id" json:"owner_id"`
	// Service scopes the variable to one Docker Compose service ("" = the whole application).
	Service   string `db:"service" json:"service"`
	Key       string `db:"key" json:"key"`
	Value     string `db:"value" json:"value"`
	IsBuild   bool   `db:"is_build" json:"is_build"`
	IsPreview bool   `db:"is_preview" json:"is_preview"`
	IsSecret  bool   `db:"is_secret" json:"is_secret"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

type Deployment struct {
	ID            int64  `db:"id" json:"id"`
	UUID          string `db:"uuid" json:"uuid"`
	ApplicationID int64  `db:"application_id" json:"application_id"`
	Status        string `db:"status" json:"status"`
	Trigger       string `db:"trigger" json:"trigger"`
	CommitSHA     string `db:"commit_sha" json:"commit_sha"`
	CommitMessage string `db:"commit_message" json:"commit_message"`
	Branch        string `db:"branch" json:"branch"`
	PRNumber      int    `db:"pr_number" json:"pr_number"`
	Image         string `db:"image" json:"image"`
	RollbackOf    int64  `db:"rollback_of" json:"rollback_of"`
	ForceRebuild  bool   `db:"force_rebuild" json:"force_rebuild"`
	Logs          string `db:"logs" json:"logs,omitempty"`
	StartedAt     string `db:"started_at" json:"started_at"`
	FinishedAt    string `db:"finished_at" json:"finished_at"`
	CreatedAt     string `db:"created_at" json:"created_at"`
	// Approval workflow (status pending_approval → queued | rejected).
	RequestedBy   int64  `db:"requested_by" json:"requested_by"`
	ReviewedBy    int64  `db:"reviewed_by" json:"reviewed_by"`
	ReviewedAt    string `db:"reviewed_at" json:"reviewed_at"`
	ReviewComment string `db:"review_comment" json:"review_comment"`
}

type Database struct {
	ID            int64  `db:"id" json:"id"`
	UUID          string `db:"uuid" json:"uuid"`
	EnvironmentID int64  `db:"environment_id" json:"environment_id"`
	ServerID      int64  `db:"server_id" json:"server_id"`
	Name          string `db:"name" json:"name"`
	Description   string `db:"description" json:"description"`
	Engine        string `db:"engine" json:"engine"`
	Image         string `db:"image" json:"image"`
	Username      string `db:"username" json:"username"`
	Password      string `db:"password" json:"-"`
	DatabaseName  string `db:"database_name" json:"database_name"`
	PublicPort    int    `db:"public_port" json:"public_port"`
	PublicHost    string `db:"public_host" json:"public_host"` // domain external clients connect to; "" = server host/IP
	CPULimit      string `db:"cpu_limit" json:"cpu_limit"`
	MemoryLimit   string `db:"memory_limit" json:"memory_limit"`
	CustomArgs    string `db:"custom_args" json:"custom_args"`
	Status        string `db:"status" json:"status"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

type Service struct {
	ID            int64  `db:"id" json:"id"`
	UUID          string `db:"uuid" json:"uuid"`
	EnvironmentID int64  `db:"environment_id" json:"environment_id"`
	ServerID      int64  `db:"server_id" json:"server_id"`
	Name          string `db:"name" json:"name"`
	Description   string `db:"description" json:"description"`
	Template      string `db:"template" json:"template"`
	Compose       string `db:"compose" json:"compose"`
	Domain        string `db:"domain" json:"domain"`
	Status        string `db:"status" json:"status"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

type Backup struct {
	ID         int64  `db:"id" json:"id"`
	DatabaseID int64  `db:"database_id" json:"database_id"`
	Schedule   string `db:"schedule" json:"schedule"`
	Retention  int    `db:"retention" json:"retention"`
	UploadS3   bool   `db:"upload_s3" json:"upload_s3"`
	Enabled    bool   `db:"enabled" json:"enabled"`
	LastRunAt  string `db:"last_run_at" json:"last_run_at"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}

type BackupRun struct {
	ID        int64  `db:"id" json:"id"`
	BackupID  int64  `db:"backup_id" json:"backup_id"`
	Status    string `db:"status" json:"status"`
	File      string `db:"file" json:"file"`
	Size      int64  `db:"size" json:"size"`
	Message   string `db:"message" json:"message"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

type NotificationChannel struct {
	ID        int64  `db:"id" json:"id"`
	Name      string `db:"name" json:"name"`
	Type      string `db:"type" json:"type"`
	Config    string `db:"config" json:"-"`
	Events    string `db:"events" json:"events"`
	Enabled   bool   `db:"enabled" json:"enabled"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

type Activity struct {
	ID        int64  `db:"id" json:"id"`
	UserID    int64  `db:"user_id" json:"user_id"`
	Action    string `db:"action" json:"action"`
	Target    string `db:"target" json:"target"`
	IP        string `db:"ip" json:"ip"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

// SplitList splits a comma/newline separated list, trimming blanks.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type GitHubApp struct {
	ID            int64  `db:"id" json:"id"`
	Name          string `db:"name" json:"name"`
	AppID         int64  `db:"app_id" json:"app_id"`
	Slug          string `db:"slug" json:"slug"`
	Owner         string `db:"owner" json:"owner"`
	ClientID      string `db:"client_id" json:"client_id"`
	ClientSecret  string `db:"client_secret" json:"-"`
	WebhookSecret string `db:"webhook_secret" json:"-"`
	PrivateKey    string `db:"private_key" json:"-"`
	HTMLURL       string `db:"html_url" json:"html_url"`
	APIURL        string `db:"api_url" json:"api_url"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

type GitHubInstallation struct {
	ID                  int64  `db:"id" json:"id"`
	GitHubAppID         int64  `db:"github_app_id" json:"github_app_id"`
	InstallationID      int64  `db:"installation_id" json:"installation_id"`
	AccountLogin        string `db:"account_login" json:"account_login"`
	AccountType         string `db:"account_type" json:"account_type"`
	AccountAvatar       string `db:"account_avatar" json:"account_avatar"`
	RepositorySelection string `db:"repository_selection" json:"repository_selection"`
	CreatedAt           string `db:"created_at" json:"created_at"`
}

// CustomCertificate is an uploaded certificate (corporate CA, EV, …) served by
// Traefik for every hostname it covers.
type CustomCertificate struct {
	ID        int64  `db:"id" json:"id"`
	Name      string `db:"name" json:"name"`
	CertPEM   string `db:"cert_pem" json:"-"`
	KeyPEM    string `db:"key_pem" json:"-"`       // encrypted
	Domains   string `db:"domains" json:"domains"` // SANs, comma-separated
	Issuer    string `db:"issuer" json:"issuer"`
	NotBefore string `db:"not_before" json:"not_before"`
	NotAfter  string `db:"not_after" json:"not_after"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

// LDAPDirectory is an LDAP / Active Directory server users can sign in with.
type LDAPDirectory struct {
	ID           int64  `db:"id" json:"id"`
	Name         string `db:"name" json:"name"`
	Slug         string `db:"slug" json:"slug"`
	Kind         string `db:"kind" json:"kind"` // ad | openldap | freeipa | generic
	Host         string `db:"host" json:"host"`
	Port         int    `db:"port" json:"port"`
	Security     string `db:"security" json:"security"` // ldaps | starttls | none
	SkipVerify   bool   `db:"skip_verify" json:"skip_verify"`
	CACert       string `db:"ca_cert" json:"ca_cert"`
	BindDN       string `db:"bind_dn" json:"bind_dn"`
	BindPassword string `db:"bind_password" json:"-"` // encrypted
	BaseDN       string `db:"base_dn" json:"base_dn"`
	// UserFilter finds the account; {username} is replaced (escaped) with what the user typed.
	UserFilter string `db:"user_filter" json:"user_filter"`
	EmailAttr  string `db:"email_attr" json:"email_attr"`
	NameAttr   string `db:"name_attr" json:"name_attr"`
	GroupsAttr string `db:"groups_attr" json:"groups_attr"`
	// RequiredGroups (DNs or CNs, comma-separated): only members may sign in.
	RequiredGroups string `db:"required_groups" json:"required_groups"`
	// AdminGroups: members become admins (synced on every sign-in).
	AdminGroups  string `db:"admin_groups" json:"admin_groups"`
	NestedGroups bool   `db:"nested_groups" json:"nested_groups"` // AD: resolve nested membership
	AutoRegister bool   `db:"auto_register" json:"auto_register"`
	DefaultRole  string `db:"default_role" json:"default_role"`
	Enabled      bool   `db:"enabled" json:"enabled"`
	CreatedAt    string `db:"created_at" json:"created_at"`
}

// AlertRule fires when metric exceeds threshold for duration minutes.
type AlertRule struct {
	ID        int64   `db:"id" json:"id"`
	Name      string  `db:"name" json:"name"`
	Scope     string  `db:"scope" json:"scope"`
	TargetID  int64   `db:"target_id" json:"target_id"`
	Metric    string  `db:"metric" json:"metric"`
	Threshold float64 `db:"threshold" json:"threshold"`
	Duration  int     `db:"duration" json:"duration"`
	Severity  string  `db:"severity" json:"severity"`
	Enabled   bool    `db:"enabled" json:"enabled"`
	CreatedAt string  `db:"created_at" json:"created_at"`
}

// AlertEvent is one firing (and later resolved) occurrence of a rule for a target.
type AlertEvent struct {
	ID             int64   `db:"id" json:"id"`
	RuleID         int64   `db:"rule_id" json:"rule_id"`
	Scope          string  `db:"scope" json:"scope"`
	RefID          int64   `db:"ref_id" json:"ref_id"`
	ServerID       int64   `db:"server_id" json:"server_id"`
	TargetName     string  `db:"target_name" json:"target_name"`
	Status         string  `db:"status" json:"status"`
	Value          float64 `db:"value" json:"value"`
	Message        string  `db:"message" json:"message"`
	StartedAt      string  `db:"started_at" json:"started_at"`
	ResolvedAt     string  `db:"resolved_at" json:"resolved_at"`
	AcknowledgedBy int64   `db:"acknowledged_by" json:"acknowledged_by"`
}
