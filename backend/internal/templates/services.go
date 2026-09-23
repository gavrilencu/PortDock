package templates

import (
	"regexp"
	"sort"
	"strings"

	"github.com/shipyard/shipyard/internal/secrets"
)

// ServiceTemplate is a docker-compose based one-click service.
// Placeholders rendered at creation time:
//
//	${SHIPYARD_FQDN}            the service domain (host only)
//	${SHIPYARD_URL}             https://<domain>
//	${SHIPYARD_PASSWORD_<NAME>} random 24-char password (stable per NAME)
//	${SHIPYARD_USER_<NAME>}     random 12-char username
//	${SHIPYARD_SECRET_<NAME>}   random 64-char hex secret
//
// The top-level `x-shipyard` key tells Shipyard which service/port to expose.
type ServiceTemplate struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Website     string `json:"website"`
	Compose     string `json:"compose"`
}

var placeholder = regexp.MustCompile(`\$\{SHIPYARD_(FQDN|URL|PASSWORD|USER|SECRET)(?:_([A-Z0-9_]+))?\}`)

// Render substitutes placeholders in a compose file.
func Render(compose, domain string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	generated := map[string]string{}
	return placeholder.ReplaceAllStringFunc(compose, func(m string) string {
		parts := placeholder.FindStringSubmatch(m)
		kind, name := parts[1], parts[2]
		switch kind {
		case "FQDN":
			return host
		case "URL":
			if host == "" {
				return "http://localhost"
			}
			return "https://" + host
		}
		key := kind + "_" + name
		if v, ok := generated[key]; ok {
			return v
		}
		var v string
		switch kind {
		case "PASSWORD":
			v = secrets.RandomPassword(24)
		case "USER":
			v = strings.ToLower(secrets.RandomPassword(12))
		case "SECRET":
			v = secrets.RandomToken(32)
		}
		generated[key] = v
		return v
	})
}

func ServiceList() []ServiceTemplate {
	out := make([]ServiceTemplate, 0, len(Services))
	for _, s := range Services {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var Services = map[string]ServiceTemplate{
	"litellm": {
		Slug: "litellm", Name: "LiteLLM", Category: "AI", Website: "https://www.litellm.ai",
		Description: "OpenAI-compatible LLM gateway with admin UI, backed by PostgreSQL. Log in as admin with UI_PASSWORD (see Compose tab).",
		Compose: `x-shipyard:
  service: litellm
  port: 4000
services:
  litellm:
    image: ghcr.io/berriai/litellm:main-latest
    restart: unless-stopped
    environment:
      DATABASE_URL: postgresql://litellm:${SHIPYARD_PASSWORD_DB}@postgres:5432/litellm
      LITELLM_MASTER_KEY: sk-${SHIPYARD_SECRET_MASTER}
      LITELLM_SALT_KEY: sk-${SHIPYARD_SECRET_SALT}
      STORE_MODEL_IN_DB: "True"
      UI_USERNAME: admin
      UI_PASSWORD: ${SHIPYARD_PASSWORD_UI}
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: litellm
      POSTGRES_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      POSTGRES_DB: litellm
    volumes:
      - pg-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U litellm -d litellm"]
      interval: 5s
      retries: 10
volumes:
  pg-data:
`,
	},
	"wordpress": {
		Slug: "wordpress", Name: "WordPress", Category: "CMS", Website: "https://wordpress.org",
		Description: "The world's most popular CMS, with MariaDB.",
		Compose: `x-shipyard:
  service: wordpress
  port: 80
services:
  wordpress:
    image: wordpress:6-php8.3-apache
    restart: unless-stopped
    environment:
      WORDPRESS_DB_HOST: db
      WORDPRESS_DB_USER: wordpress
      WORDPRESS_DB_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      WORDPRESS_DB_NAME: wordpress
    volumes:
      - wp-data:/var/www/html
    depends_on:
      db:
        condition: service_healthy
  db:
    image: mariadb:11
    restart: unless-stopped
    environment:
      MARIADB_DATABASE: wordpress
      MARIADB_USER: wordpress
      MARIADB_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      MARIADB_ROOT_PASSWORD: ${SHIPYARD_PASSWORD_ROOT}
    volumes:
      - db-data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      retries: 10
volumes:
  wp-data:
  db-data:
`,
	},
	"uptime-kuma": {
		Slug: "uptime-kuma", Name: "Uptime Kuma", Category: "Monitoring", Website: "https://uptime.kuma.pet",
		Description: "Self-hosted uptime monitoring with status pages.",
		Compose: `x-shipyard:
  service: uptime-kuma
  port: 3001
services:
  uptime-kuma:
    image: louislam/uptime-kuma:2
    restart: unless-stopped
    volumes:
      - data:/app/data
volumes:
  data:
`,
	},
	"n8n": {
		Slug: "n8n", Name: "n8n", Category: "Automation", Website: "https://n8n.io",
		Description: "Workflow automation with 400+ integrations, backed by PostgreSQL.",
		Compose: `x-shipyard:
  service: n8n
  port: 5678
services:
  n8n:
    image: n8nio/n8n:latest
    restart: unless-stopped
    environment:
      N8N_HOST: ${SHIPYARD_FQDN}
      N8N_PROTOCOL: https
      WEBHOOK_URL: ${SHIPYARD_URL}/
      N8N_ENCRYPTION_KEY: ${SHIPYARD_SECRET_KEY}
      DB_TYPE: postgresdb
      DB_POSTGRESDB_HOST: postgres
      DB_POSTGRESDB_DATABASE: n8n
      DB_POSTGRESDB_USER: n8n
      DB_POSTGRESDB_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      GENERIC_TIMEZONE: UTC
    volumes:
      - n8n-data:/home/node/.n8n
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: n8n
      POSTGRES_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      POSTGRES_DB: n8n
    volumes:
      - pg-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U n8n -d n8n"]
      interval: 5s
      retries: 10
volumes:
  n8n-data:
  pg-data:
`,
	},
	"gitea": {
		Slug: "gitea", Name: "Gitea", Category: "Development", Website: "https://gitea.com",
		Description: "Lightweight self-hosted Git service with Actions CI.",
		Compose: `x-shipyard:
  service: gitea
  port: 3000
services:
  gitea:
    image: gitea/gitea:1
    restart: unless-stopped
    environment:
      GITEA__server__ROOT_URL: ${SHIPYARD_URL}/
      GITEA__server__DOMAIN: ${SHIPYARD_FQDN}
      GITEA__security__SECRET_KEY: ${SHIPYARD_SECRET_KEY}
      GITEA__database__DB_TYPE: sqlite3
    volumes:
      - data:/data
    ports:
      - "2222:22"
volumes:
  data:
`,
	},
	"minio": {
		Slug: "minio", Name: "MinIO", Category: "Storage", Website: "https://min.io",
		Description: "S3-compatible object storage (console exposed on the domain, API on :9000).",
		Compose: `x-shipyard:
  service: minio
  port: 9001
services:
  minio:
    image: quay.io/minio/minio:latest
    restart: unless-stopped
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: ${SHIPYARD_USER_ADMIN}
      MINIO_ROOT_PASSWORD: ${SHIPYARD_PASSWORD_ADMIN}
      MINIO_BROWSER_REDIRECT_URL: ${SHIPYARD_URL}
    volumes:
      - data:/data
    ports:
      - "9000:9000"
volumes:
  data:
`,
	},
	"umami": {
		Slug: "umami", Name: "Umami", Category: "Analytics", Website: "https://umami.is",
		Description: "Privacy-focused, GDPR compliant web analytics.",
		Compose: `x-shipyard:
  service: umami
  port: 3000
services:
  umami:
    image: ghcr.io/umami-software/umami:postgresql-latest
    restart: unless-stopped
    environment:
      DATABASE_URL: postgresql://umami:${SHIPYARD_PASSWORD_DB}@postgres:5432/umami
      APP_SECRET: ${SHIPYARD_SECRET_APP}
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: umami
      POSTGRES_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      POSTGRES_DB: umami
    volumes:
      - pg-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U umami -d umami"]
      interval: 5s
      retries: 10
volumes:
  pg-data:
`,
	},
	"grafana": {
		Slug: "grafana", Name: "Grafana", Category: "Monitoring", Website: "https://grafana.com",
		Description: "Dashboards and observability for metrics, logs and traces.",
		Compose: `x-shipyard:
  service: grafana
  port: 3000
services:
  grafana:
    image: grafana/grafana:latest
    restart: unless-stopped
    environment:
      GF_SERVER_ROOT_URL: ${SHIPYARD_URL}
      GF_SECURITY_ADMIN_USER: admin
      GF_SECURITY_ADMIN_PASSWORD: ${SHIPYARD_PASSWORD_ADMIN}
    volumes:
      - data:/var/lib/grafana
volumes:
  data:
`,
	},
	"vaultwarden": {
		Slug: "vaultwarden", Name: "Vaultwarden", Category: "Security", Website: "https://github.com/dani-garcia/vaultwarden",
		Description: "Bitwarden-compatible password manager server.",
		Compose: `x-shipyard:
  service: vaultwarden
  port: 80
services:
  vaultwarden:
    image: vaultwarden/server:latest
    restart: unless-stopped
    environment:
      DOMAIN: ${SHIPYARD_URL}
      ADMIN_TOKEN: ${SHIPYARD_SECRET_ADMIN}
      SIGNUPS_ALLOWED: "true"
    volumes:
      - data:/data
volumes:
  data:
`,
	},
	"ghost": {
		Slug: "ghost", Name: "Ghost", Category: "CMS", Website: "https://ghost.org",
		Description: "Professional publishing platform with MySQL.",
		Compose: `x-shipyard:
  service: ghost
  port: 2368
services:
  ghost:
    image: ghost:6-alpine
    restart: unless-stopped
    environment:
      url: ${SHIPYARD_URL}
      database__client: mysql
      database__connection__host: mysql
      database__connection__user: ghost
      database__connection__password: ${SHIPYARD_PASSWORD_DB}
      database__connection__database: ghost
    volumes:
      - content:/var/lib/ghost/content
    depends_on:
      mysql:
        condition: service_healthy
  mysql:
    image: mysql:8.4
    restart: unless-stopped
    environment:
      MYSQL_ROOT_PASSWORD: ${SHIPYARD_PASSWORD_ROOT}
      MYSQL_DATABASE: ghost
      MYSQL_USER: ghost
      MYSQL_PASSWORD: ${SHIPYARD_PASSWORD_DB}
    volumes:
      - mysql:/var/lib/mysql
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1"]
      interval: 10s
      retries: 10
volumes:
  content:
  mysql:
`,
	},
	"metabase": {
		Slug: "metabase", Name: "Metabase", Category: "Analytics", Website: "https://metabase.com",
		Description: "Business intelligence and dashboards for your data.",
		Compose: `x-shipyard:
  service: metabase
  port: 3000
services:
  metabase:
    image: metabase/metabase:latest
    restart: unless-stopped
    environment:
      MB_DB_TYPE: postgres
      MB_DB_DBNAME: metabase
      MB_DB_PORT: "5432"
      MB_DB_USER: metabase
      MB_DB_PASS: ${SHIPYARD_PASSWORD_DB}
      MB_DB_HOST: postgres
      MB_SITE_URL: ${SHIPYARD_URL}
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: metabase
      POSTGRES_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      POSTGRES_DB: metabase
    volumes:
      - pg-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U metabase -d metabase"]
      interval: 5s
      retries: 10
volumes:
  pg-data:
`,
	},
	"pgadmin": {
		Slug: "pgadmin", Name: "pgAdmin", Category: "Database", Website: "https://pgadmin.org",
		Description: "Web administration tool for PostgreSQL.",
		Compose: `x-shipyard:
  service: pgadmin
  port: 80
services:
  pgadmin:
    image: dpage/pgadmin4:latest
    restart: unless-stopped
    environment:
      PGADMIN_DEFAULT_EMAIL: admin@example.com
      PGADMIN_DEFAULT_PASSWORD: ${SHIPYARD_PASSWORD_ADMIN}
    volumes:
      - data:/var/lib/pgadmin
volumes:
  data:
`,
	},
	"nocodb": {
		Slug: "nocodb", Name: "NocoDB", Category: "Productivity", Website: "https://nocodb.com",
		Description: "Open-source Airtable alternative on top of PostgreSQL.",
		Compose: `x-shipyard:
  service: nocodb
  port: 8080
services:
  nocodb:
    image: nocodb/nocodb:latest
    restart: unless-stopped
    environment:
      NC_DB: pg://postgres:5432?u=nocodb&p=${SHIPYARD_PASSWORD_DB}&d=nocodb
      NC_PUBLIC_URL: ${SHIPYARD_URL}
      NC_AUTH_JWT_SECRET: ${SHIPYARD_SECRET_JWT}
    volumes:
      - data:/usr/app/data
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:18-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: nocodb
      POSTGRES_PASSWORD: ${SHIPYARD_PASSWORD_DB}
      POSTGRES_DB: nocodb
    volumes:
      - pg-data:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U nocodb -d nocodb"]
      interval: 5s
      retries: 10
volumes:
  data:
  pg-data:
`,
	},
}
