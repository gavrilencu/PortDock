# Terraform provider for PortDock

Manage PortDock projects, environments, applications, domains and variables with Terraform or OpenTofu.

| Resource / data source | Manages |
|---|---|
| `portdock_project` | A project. `production_environment_id` is the `production` environment every project starts with. |
| `portdock_environment` | Another environment of a project (`staging`, `preview`…), with `protected` and `require_approval`. |
| `portdock_application` | An application from a Docker image (`docker_image`) or a Git repository (`git_repository`), with port, replicas, limits and health check. Importable by id. |
| `portdock_domain` | A domain routed to an application (`https://…` or `http://…`, optional path and port). |
| `portdock_env_var` | A runtime variable: a `value` (sensitive; `secret` hides it in the interface), or a `secret_ref` read from HashiCorp Vault or Azure Key Vault when a deployment starts (`store:path#key`). |
| `data.portdock_server` | A server by name, for `server_id`. |

Terraform changes the configuration. Deployments still come from git pushes, the interface, `pdctl deploy` or the API: after changing variables, redeploy the application.

## Install

Each PortDock release attaches `terraform-provider-portdock_<version>_<os>_<arch>.zip` and a `SHA256SUMS` file. Put the zip in a filesystem mirror, keeping its name:

```
~/.terraform.d/plugins/registry.terraform.io/portdock/portdock/terraform-provider-portdock_1.14.0_linux_amd64.zip
```

On Windows the folder is `%APPDATA%\terraform.d\plugins\…`. Terraform finds it there by itself; with an explicit `provider_installation` block in `~/.terraformrc`, add:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/home/me/.terraform.d/plugins"
    include = ["registry.terraform.io/portdock/portdock"]
  }
  direct {
    exclude = ["registry.terraform.io/portdock/portdock"]
  }
}
```

`terraform init` then installs it. To build it yourself: `go build -o terraform-provider-portdock .` and point `dev_overrides` at the folder.

## Token

Create a service account in **Settings → Team** and give it a token:

- scope `write` is enough for applications, domains and variables in existing projects;
- scope `full` is needed to create, change or delete projects and environments.

Limit the token to some projects when Terraform manages only those (it cannot create new projects then).

## Example

```hcl
terraform {
  required_providers {
    portdock = { source = "portdock/portdock", version = "~> 1.14" }
  }
}

provider "portdock" {
  url = "https://portdock.example.com" # or PORTDOCK_URL
  # token from PORTDOCK_TOKEN
}

data "portdock_server" "main" { name = "localhost" }

resource "portdock_project" "shop" {
  name        = "shop"
  description = "managed by terraform"
}

resource "portdock_environment" "staging" {
  project_id       = portdock_project.shop.id
  name             = "staging"
  require_approval = false
}

resource "portdock_application" "web" {
  environment_id    = portdock_environment.staging.id
  server_id         = data.portdock_server.main.id
  name              = "web"
  git_repository    = "https://github.com/acme/shop"
  git_branch        = "main"
  build_pack        = "dockerfile"
  port              = 3000
  replicas          = 2
  memory_limit      = "512m"
  health_check_path = "/health"
}

resource "portdock_domain" "web" {
  application_id = portdock_application.web.id
  domain         = "https://staging.shop.example.com"
}

resource "portdock_env_var" "database_url" {
  application_id = portdock_application.web.id
  key            = "DATABASE_URL"
  value          = var.database_url
  secret         = true
}

# Read from a secret manager of Settings → Secret managers; PortDock never stores the value.
resource "portdock_env_var" "db_password" {
  application_id = portdock_application.web.id
  key            = "DB_PASSWORD"
  secret_ref     = "vault-prod:shop/db#password"
}
```

An application created in the interface is adopted with `terraform import portdock_application.web <id>` (the id is in its address, `/applications/<id>`); the same works for `portdock_project`.

## Behaviour

- Changes made outside Terraform show in the next `terraform plan` and are reverted by `apply`; a deleted resource is created again.
- Changing `environment_id` moves the application; changing `server_id` requires the application to be stopped.
- Changing a domain's address, a variable's key or a parent id replaces the resource.
- Optional attributes that are not set keep the value PortDock chose (the defaults or a change made in the interface).
