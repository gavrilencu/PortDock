---

## Install on a server

On a Linux server (Ubuntu or Debian recommended), as root. Ports **80** and **443** must be free for the HTTPS proxy.

**Option 1: installer** (installs Docker if it is missing and builds this version):

```bash
git clone --branch {{VERSION}} https://github.com/{{REPO}}.git portdock
cd portdock
sudo bash install.sh                               # dashboard on http://<server-ip>:8000
sudo bash install.sh https://portdock.example.com  # or with a domain that points to the server
```

If port 8000 is taken, the installer uses the next free port and prints the address.

**Option 2: prebuilt image** (Docker already installed):

```bash
docker run -d --name portdock --restart unless-stopped \
  -p 8000:8000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /data/portdock:/data/portdock \
  -e PORTDOCK_PUBLIC_URL=http://<server-ip>:8000 \
  {{IMAGE}}:{{VERSION}}
```

Keep the data directory mounted at the same path as on the host (`/data/portdock`). To use another port, change it on the left of `-p`, for example `-p 8001:8000`.

Then open the dashboard, create the owner account, and start the proxy from **Servers → localhost → Run setup**.

**Option 3: Kubernetes on a fresh server, all in one** (asks for the domain of the dashboard):

```bash
curl -fsSL https://github.com/{{REPO}}/releases/download/{{VERSION}}/install-kubernetes.sh | sudo bash
```

It installs k3s, cert-manager, Docker for the builds and a private registry, then PortDock on `https://<your domain>` with the servers **test** and **prod** ready, and prints the link to create the owner account. Point the domain's DNS record to the server first. Unattended: `curl … | sudo PORTDOCK_DOMAIN=panel.example.com bash`.

**Option 4: in an existing Kubernetes cluster** (no Docker; the cluster is the server):

```bash
curl -fsSL https://raw.githubusercontent.com/{{REPO}}/{{VERSION}}/deploy/kubernetes/portdock.yaml \
  | sed 's#portdock:latest#portdock:{{VERSION}}#' | kubectl apply -f -
kubectl -n portdock rollout status statefulset/portdock
kubectl -n portdock port-forward svc/portdock 8000:8000   # then open http://localhost:8000
```

To deploy to a cluster from a PortDock that runs on a server, run `curl -fsSL https://raw.githubusercontent.com/{{REPO}}/{{VERSION}}/deploy/kubernetes/connect.sh | bash` where `kubectl` reaches the cluster and paste the kubeconfig it prints in **Servers → Connect server → Kubernetes cluster**.

## Update an existing installation

In PortDock, click **Update available** at the bottom of the sidebar, or go to **Settings → Updates**. A backup is taken first, and the previous version comes back automatically if the new one doesn't start. Your applications keep running during the update.

## Verify the image

The image is signed by this repository's release workflow (Sigstore keyless) and carries an SBOM and a provenance attestation; the SBOM is also attached to this release. PortDock checks the signature itself before updating. To check it by hand:

```bash
cosign verify {{IMAGE}}:{{VERSION}} \
  --certificate-identity-regexp '^https://github.com/{{REPO}}/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Image digest: `{{DIGEST}}`
