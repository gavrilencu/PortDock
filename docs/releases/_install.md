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

## Update an existing installation

In PortDock, click **Update available** at the bottom of the sidebar, or go to **Settings → Updates**. A backup is taken first, and the previous version comes back automatically if the new one doesn't start. Your applications keep running during the update.
