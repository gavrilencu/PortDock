# PortDock service templates

Every `*.yml` file here is a one-click service of PortDock (New resource → One-click service), built into each release. `catalog.json` bundles them. Instances that add the **PortDock community catalog** (Settings → Service catalogs) download it from the `main` branch every day, so a template merged here reaches them without an update.

## Writing a template

A template is a `docker-compose` file. Its top-level `x-portdock` block describes the service and names the container that the domain points to:

```yaml
x-portdock:
  name: Uptime Kuma                         # shown in the picker
  description: Self-hosted uptime monitoring with status pages.
  category: Monitoring & Observability      # one of the categories of ../images.json
  website: https://uptime.kuma.pet          # https
  logo: https://example.com/logo.svg        # optional, https
  tags: [monitoring, uptime, status-page]   # optional, for the search
  service: uptime-kuma                      # the public service…
  port: 3001                                # …and its port in the container (omit both for a service without a domain)
services:
  uptime-kuma:
    image: louislam/uptime-kuma:2
    restart: unless-stopped
    volumes:
      - data:/app/data
volumes:
  data:
```

The file name is the template's slug (`uptime-kuma.yml`): lowercase letters, digits and dashes.

### Placeholders

PortDock fills these in when a service is created. The same placeholder gets the same value everywhere in the file, so a database and its application share a password.

| Placeholder | Value |
|---|---|
| `${PORTDOCK_FQDN}` | the service's domain, e.g. `kuma.example.com` |
| `${PORTDOCK_URL}` | `https://` and the domain |
| `${PORTDOCK_PASSWORD_<NAME>}` | 24 random letters and digits (`PASSWORD64`: 64) |
| `${PORTDOCK_USER_<NAME>}` | 12 random lowercase characters |
| `${PORTDOCK_SECRET_<NAME>}` | 64 random hex characters (also `HEX32`, `HEX64`, `HEX128`) |
| `${PORTDOCK_RANDOM32_<NAME>}` | random letters and digits, 32, 64 or 128 long (`RANDOM64`, `RANDOM128`) |
| `${PORTDOCK_BASE64_<NAME>}` | base64 of 32 random bytes (`BASE64x64`, `BASE64x128`) |

Other variables (`${SMTP_HOST}`, `${TZ:-UTC}`) come from the service's Environment tab. A variable written `${NAME:?}` must be set there before the service can start: the picker says so and creates the service without starting it.

### Rules

- No `container_name`: two copies of the service would collide.
- No host access: no `privileged`, host namespaces, devices, capabilities or bind mounts of server paths. Named volumes keep the data. The Docker socket is never allowed.
- Prefer a pinned image version over `latest`, and a `restart: unless-stopped` policy.
- Published `ports:` are only for protocols the proxy does not carry (SSH, SMTP…).
- Wait for a database with `depends_on: {db: {condition: service_healthy}}` and a `healthcheck` on it.
- Say in the description how to sign in the first time (which variable of the Compose tab holds the password).

## Checking and proposing a template

```sh
pdctl template lint backend/internal/templates/services/my-service.yml
cd backend && PORTDOCK_UPDATE_CATALOG=1 go test ./internal/templates -run CommunityCatalog   # refresh catalog.json
go test ./internal/templates                                                                # what CI runs
PORTDOCK_DOCKER_E2E=1 go test ./internal/deploy -run ServiceTemplatesPassComposeCheck       # with the real Docker Compose
```

Then open a pull request with the template file and the updated `catalog.json`. Please deploy the service once (a custom compose service with the rendered file is enough) and mention what you checked.

## Your own catalog

Teams can keep templates for their internal tools in a repository of their own: put the `*.yml` files in a directory, run `pdctl template bundle DIR -o catalog.json`, publish the file where PortDock can download it (a Git server's raw URL, Nexus, Artifactory) and add its URL in Settings → Service catalogs. A catalog can also be uploaded as a file.

Coolify's templates work as they are: add `https://raw.githubusercontent.com/coollabsio/coolify/v4.x/templates/service-templates.json` as a catalog and PortDock converts its 350+ services, or convert one file with `pdctl template convert`.
