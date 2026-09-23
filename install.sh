#!/usr/bin/env bash
# PortDock installer: installs Docker if needed and starts PortDock with docker compose.
# Usage (as root, from the repository root):  sudo bash install.sh [public-url]
set -euo pipefail

PUBLIC_URL="${1:-}"
if [ "$(id -u)" -ne 0 ]; then echo "Please run as root (sudo ./install.sh)"; exit 1; fi

if ! command -v docker >/dev/null 2>&1; then
  echo "==> Installing Docker"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker 2>/dev/null || true
fi

mkdir -p /data/portdock
cd "$(dirname "$0")"

port_busy() { ss -Hltn "sport = :$1" 2>/dev/null | grep -q . || docker ps --format '{{.Ports}}' | grep -q ":$1->"; }
# Dashboard port: PORTDOCK_PORT, else 8000, else the next free port.
# A re-install keeps the port of the running PortDock container.
PORT="${PORTDOCK_PORT:-}"
if [ -z "$PORT" ]; then
  PORT=$(docker port portdock 8000/tcp 2>/dev/null | head -1 | sed 's/.*://')
fi
if [ -z "$PORT" ]; then
  PORT=8000
  while port_busy "$PORT"; do PORT=$((PORT + 1)); done
  if [ "$PORT" != 8000 ]; then echo "==> Port 8000 is in use, using port ${PORT}"; fi
fi

if [ -z "$PUBLIC_URL" ]; then
  IP=$(curl -fsS4 https://ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')
  PUBLIC_URL="http://${IP}:${PORT}"
fi

# Version and update source come from the checkout, so in-app updates work out of the box.
VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
REPO=$(git config --get remote.origin.url 2>/dev/null | sed -E 's#(\.git)?$##; s#^.*github\.com[:/]##')

echo "==> Building and starting PortDock ${VERSION} (${PUBLIC_URL})"
PORTDOCK_PORT="$PORT" PORTDOCK_PUBLIC_URL="$PUBLIC_URL" PORTDOCK_VERSION="$VERSION" PORTDOCK_UPDATE_REPO="$REPO"   docker compose up -d --build

echo
echo "PortDock is starting at ${PUBLIC_URL}"
echo "Open it in your browser and create the admin account."
echo "Dashboard port: ${PORT} (open it in the firewall if needed)."
echo "Ports 80/443 must be free for the Traefik proxy (automatic HTTPS)."
