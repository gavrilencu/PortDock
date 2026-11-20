#!/usr/bin/env bash
# PortDock on Kubernetes, on one fresh Linux server, in one command (as root):
#
#   curl -fsSL https://github.com/gavrilencu/PortDock/releases/latest/download/install-kubernetes.sh | sudo bash
#
# It asks for the domain of the dashboard, then installs and sets up:
#   - k3s (Kubernetes, with Traefik as the ingress controller), cert-manager and
#     a Let's Encrypt issuer, so every domain gets its HTTPS certificate;
#   - Docker, where the images of applications from Git are built;
#   - a private container registry on the same domain (https://<domain>/v2/),
#     protected by a generated password;
#   - PortDock on https://<domain>, with its servers ready: "test" and "prod"
#     (the namespaces portdock-test and portdock-prod, which push to and pull
#     from the registry) and "build" (Docker on this server);
# and prints the link to create the owner account, protected by a setup token.
#
# Without a terminal (or to skip the question): PORTDOCK_DOMAIN=panel.example.com.
# Optional: PORTDOCK_EMAIL (Let's Encrypt account), PORTDOCK_VERSION (vX.Y.Z).
# Run it again to update PortDock or repair the setup: passwords and data stay.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

VERSION="${PORTDOCK_VERSION:-}"
REPO="${PORTDOCK_REPO:-gavrilencu/PortDock}"
CERT_MANAGER_VERSION="v1.21.3"
REGISTRY_IMAGE="registry:3.1.2"
NS=portdock
ENVIRONMENTS="test=portdock-test,prod=portdock-prod"
DATA_DIR=/data/portdock

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
step() { printf '\n\033[1;34m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m! %s\033[0m\n' "$*" >&2; }
die() { printf '\033[31mError: %s\033[0m\n' "$*" >&2; exit 1; }
# rand N: N random bytes as hex.
rand() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }
# wait_for SECONDS DESCRIPTION COMMAND...: runs COMMAND every 3 s until it succeeds.
wait_for() {
  local limit=$1 what=$2 start=$SECONDS
  shift 2
  until "$@" >/dev/null 2>&1; do
    [ $((SECONDS - start)) -lt "$limit" ] || return 1
    sleep 3
  done
  echo "    $what: ok"
}
# has_tty: a terminal to ask questions on (stdin is the script under curl | bash).
has_tty() { (exec </dev/tty) 2>/dev/null; }
ask() { # ask PROMPT DEFAULT: reads an answer from the terminal
  local answer=""
  has_tty || { echo "$2"; return; }
  read -rp "$1" answer </dev/tty || true
  echo "${answer:-$2}"
}

# Everything runs from main, called on the last line: under curl | bash the
# whole script is read before anything runs, so no command can swallow the
# rest of it from stdin, and a cut download runs nothing.
main() {
# ---------------------------------------------------------------- checks
[ "$(id -u)" -eq 0 ] || die "run as root: curl -fsSL … | sudo bash"
[ "$(uname -s)" = Linux ] || die "PortDock on Kubernetes needs a Linux server"
command -v systemctl >/dev/null || die "systemd is required (k3s runs as a systemd service)"
case "$(uname -m)" in x86_64 | amd64 | aarch64 | arm64) ;; *) die "unsupported architecture $(uname -m): amd64 or arm64 only" ;; esac
if ! command -v curl >/dev/null; then
  (apt-get update -qq && apt-get install -y -qq curl) >/dev/null 2>&1 || (dnf install -y curl || yum install -y curl) >/dev/null 2>&1 || die "install curl first"
fi
mem_mb=$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo)
[ "$mem_mb" -ge 1800 ] || warn "this server has ${mem_mb} MB of memory; 2 GB or more is recommended"
if command -v docker >/dev/null && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx portdock; then
  die "PortDock already runs here with Docker (container \"portdock\"). Use a fresh server for PortDock on Kubernetes."
fi

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
have_k3s() { command -v k3s >/dev/null && [ -f "$KUBECONFIG" ]; }
if have_k3s && kubectl -n "$NS" get statefulset portdock -o jsonpath='{.spec.volumeClaimTemplates[0].metadata.name}' 2>/dev/null | grep -q .; then
  die "PortDock was installed here with deploy/kubernetes/portdock.yaml (its data on a volume claim). This installer keeps the data in $DATA_DIR: use a fresh server, or delete the namespace $NS first (that deletes PortDock's data)."
fi

# ---------------------------------------------------------------- domain
DOMAIN="${PORTDOCK_DOMAIN:-}"
previous=""
have_k3s && previous=$(kubectl -n "$NS" get ingress portdock -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || true)
if [ -z "$DOMAIN" ]; then
  has_tty || [ -n "$previous" ] || die "no terminal to ask the domain: run with PORTDOCK_DOMAIN=panel.example.com"
  echo
  bold "PortDock on Kubernetes"
  echo "The dashboard is served on a domain of yours, with an HTTPS certificate from Let's Encrypt."
  echo "Its DNS record (A) must point to this server."
  if [ -n "$previous" ]; then
    DOMAIN=$(ask "Domain of the dashboard [$previous]: " "$previous")
  else
    DOMAIN=$(ask "Domain of the dashboard (e.g. panel.example.com): " "")
  fi
fi
DOMAIN=$(printf '%s' "$DOMAIN" | tr '[:upper:]' '[:lower:]' | sed -E 's#^[a-z]+://##; s#[/:].*$##; s#\.$##')
printf '%s' "$DOMAIN" | grep -Eq '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$' || die "\"$DOMAIN\" is not a domain name (e.g. panel.example.com)"
if [ -n "$previous" ] && [ "$previous" != "$DOMAIN" ]; then
  warn "The dashboard moves from $previous to $DOMAIN. The registry follows it: in PortDock, change the host of"
  warn "\"Registry of the cluster\" to $DOMAIN in Settings → Container registries, and the instance URL in Settings."
fi

PUBLIC_IP=$(curl -fsS4 --max-time 8 https://api.ipify.org 2>/dev/null || curl -fsS4 --max-time 8 https://ifconfig.me 2>/dev/null || true)
RESOLVED=$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)
if [ -n "$PUBLIC_IP" ] && ! printf ' %s ' "$RESOLVED" | grep -q " $PUBLIC_IP "; then
  warn "$DOMAIN resolves to: ${RESOLVED:-nothing}; this server's address is $PUBLIC_IP."
  warn "Create the DNS record \"$DOMAIN  A  $PUBLIC_IP\": the certificate is issued once it points here."
  if has_tty; then
    case "$(ask "Continue anyway? [y/N] " n)" in y | Y | yes | da) ;; *) die "stopped: fix the DNS record and run the installer again" ;; esac
  fi
fi

# Versions: the release this script comes from (set in the release asset), else the latest one.
if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL --max-time 10 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1 || true)
  VERSION=${VERSION:-latest}
fi
IMAGE="ghcr.io/$(printf '%s' "$REPO" | tr '[:upper:]' '[:lower:]'):$VERSION"
echo
bold "Installing PortDock $VERSION on https://$DOMAIN"

# ---------------------------------------------------------------- Docker
step "Docker (builds the images of applications from Git)"
if ! command -v docker >/dev/null; then
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker info --format '    Docker {{.ServerVersion}}' || die "Docker does not answer"

# ---------------------------------------------------------------- k3s
step "Kubernetes (k3s)"
if ! have_k3s; then
  port_used() { ss -Hltn "sport = :$1" 2>/dev/null | grep -q .; }
  for p in 80 443; do port_used "$p" && die "port $p is in use: the cluster's ingress controller needs ports 80 and 443"; done
  curl -sfL https://get.k3s.io | INSTALL_K3S_CHANNEL=stable sh -s - --write-kubeconfig-mode 600 >/dev/null
fi
wait_for 300 "node ready" sh -c 'kubectl get nodes --no-headers | grep -q " Ready"' || die "the Kubernetes node did not become ready (journalctl -u k3s)"
wait_for 300 "Traefik deployed" kubectl -n kube-system get deploy traefik || die "Traefik was not deployed by k3s (kubectl -n kube-system get pods)"
kubectl -n kube-system rollout status deploy/traefik --timeout=300s >/dev/null || die "Traefik did not start"
echo "    $(kubectl version 2>/dev/null | sed -n 's/^Server Version: /Kubernetes /p')"
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow 80/tcp >/dev/null && ufw allow 443/tcp >/dev/null
  ufw allow from 10.42.0.0/16 >/dev/null && ufw allow from 10.43.0.0/16 >/dev/null # pods and services (k3s)
  echo "    firewall (ufw): 80 and 443 open"
fi

# ---------------------------------------------------------------- cert-manager
step "HTTPS certificates (cert-manager $CERT_MANAGER_VERSION, Let's Encrypt)"
kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/$CERT_MANAGER_VERSION/cert-manager.yaml" >/dev/null
kubectl -n cert-manager wait --for=condition=Available deploy --all --timeout=300s >/dev/null || die "cert-manager did not start"
email_line=""
[ -n "${PORTDOCK_EMAIL:-}" ] && email_line="    email: ${PORTDOCK_EMAIL}"
issuer() {
  kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
$email_line
    privateKeySecretRef:
      name: letsencrypt-account
    solvers:
    - http01:
        ingress:
          ingressClassName: traefik
EOF
}
# The webhook of cert-manager may take a few more seconds to accept objects.
wait_for 120 "issuer letsencrypt" issuer || die "the ClusterIssuer could not be created (kubectl -n cert-manager get pods)"

# ---------------------------------------------------------------- secrets
step "Namespaces and secrets"
namespaces="$NS $(printf '%s' "$ENVIRONMENTS" | tr ',' '\n' | cut -d= -f2 | tr '\n' ' ')"
for n in $namespaces; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
secret_value() { kubectl -n "$NS" get secret "$1" -o "jsonpath={.data.$2}" 2>/dev/null | base64 -d 2>/dev/null || true; }
SETUP_TOKEN=$(secret_value portdock-setup token)
SETUP_TOKEN=${SETUP_TOKEN:-$(rand 24)}
REGISTRY_USER=portdock
REGISTRY_PASSWORD=$(secret_value portdock-registry password)
REGISTRY_PASSWORD=${REGISTRY_PASSWORD:-$(rand 24)}
REGISTRY_HTTP_SECRET=$(secret_value portdock-registry http-secret)
REGISTRY_HTTP_SECRET=${REGISTRY_HTTP_SECRET:-$(rand 24)}
HTPASSWD=$(secret_value portdock-registry htpasswd)
if [ -z "$HTPASSWD" ]; then
  if ! command -v htpasswd >/dev/null && command -v apt-get >/dev/null; then
    (apt-get update -qq && apt-get install -y -qq apache2-utils) >/dev/null 2>&1 || true
  fi
  if command -v htpasswd >/dev/null; then
    HTPASSWD=$(htpasswd -Bbn "$REGISTRY_USER" "$REGISTRY_PASSWORD")
  else
    HTPASSWD=$(docker run --rm --entrypoint htpasswd httpd:2.4-alpine -Bbn "$REGISTRY_USER" "$REGISTRY_PASSWORD")
  fi
fi
HTPASSWD=$(printf '%s' "$HTPASSWD" | head -1)
kubectl -n "$NS" create secret generic portdock-setup --from-literal=token="$SETUP_TOKEN" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NS" create secret generic portdock-registry \
  --from-literal=username="$REGISTRY_USER" --from-literal=password="$REGISTRY_PASSWORD" \
  --from-literal=htpasswd="$HTPASSWD" --from-literal=http-secret="$REGISTRY_HTTP_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "    setup token and registry password: ready"

# ---------------------------------------------------------------- registry
step "Container registry (https://$DOMAIN/v2/)"
kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: registry-data
  namespace: $NS
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 50Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: registry
  namespace: $NS
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: registry
  template:
    metadata:
      labels:
        app: registry
    spec:
      containers:
      - name: registry
        image: $REGISTRY_IMAGE
        ports:
        - containerPort: 5000
        env:
        - name: REGISTRY_AUTH
          value: htpasswd
        - name: REGISTRY_AUTH_HTPASSWD_REALM
          value: PortDock
        - name: REGISTRY_AUTH_HTPASSWD_PATH
          value: /auth/htpasswd
        - name: REGISTRY_STORAGE_DELETE_ENABLED
          value: "true"
        # Behind Traefik, which ends TLS: upload locations stay relative.
        - name: REGISTRY_HTTP_RELATIVEURLS
          value: "true"
        - name: REGISTRY_HTTP_SECRET
          valueFrom:
            secretKeyRef:
              name: portdock-registry
              key: http-secret
        readinessProbe:
          tcpSocket:
            port: 5000
        volumeMounts:
        - name: data
          mountPath: /var/lib/registry
        - name: auth
          mountPath: /auth
          readOnly: true
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: registry-data
      - name: auth
        secret:
          secretName: portdock-registry
          items:
          - key: htpasswd
            path: htpasswd
---
apiVersion: v1
kind: Service
metadata:
  name: registry
  namespace: $NS
spec:
  selector:
    app: registry
  ports:
  - name: http
    port: 5000
    targetPort: 5000
EOF
echo "    registry: deployed"

# ---------------------------------------------------------------- PortDock
step "PortDock $VERSION"
mkdir -p "$DATA_DIR"
{
  cat <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: portdock
  namespace: $NS
---
# Read-only, cluster-wide: the capacity of the nodes and the ingress classes.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: portdock-reader
rules:
- apiGroups: [""]
  resources: ["nodes", "namespaces"]
  verbs: ["get", "list"]
- apiGroups: ["networking.k8s.io"]
  resources: ["ingressclasses"]
  verbs: ["get", "list"]
- apiGroups: ["cert-manager.io"]
  resources: ["clusterissuers"]
  verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: portdock-reader-portdock
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: portdock-reader
subjects:
- kind: ServiceAccount
  name: portdock
  namespace: $NS
EOF
  # Self-update from the dashboard: a Job of PortDock's image points the
  # StatefulSet at the new version and goes back when it does not start.
  cat <<EOF
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: portdock-self-update
  namespace: $NS
rules:
- apiGroups: ["apps"]
  resources: ["statefulsets"]
  verbs: ["get", "list", "watch", "patch", "update"]
- apiGroups: ["apps"]
  resources: ["controllerrevisions"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list", "watch", "delete"]
- apiGroups: [""]
  resources: ["pods/log"]
  verbs: ["get"]
- apiGroups: ["batch"]
  resources: ["jobs"]
  verbs: ["get", "list", "watch", "create", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: portdock-self-update
  namespace: $NS
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: portdock-self-update
subjects:
- kind: ServiceAccount
  name: portdock
  namespace: $NS
EOF
  # In the namespaces of the applications, besides "admin": the Traefik
  # middleware that sends HTTP to HTTPS, and asking cert-manager to renew a
  # certificate (Domains tab), with the state of its ACME challenge.
  cat <<EOF
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: portdock-ingress
rules:
- apiGroups: ["traefik.io"]
  resources: ["middlewares"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["cert-manager.io"]
  resources: ["certificates", "certificates/status"]
  verbs: ["get", "list", "watch", "patch", "update"]
- apiGroups: ["acme.cert-manager.io"]
  resources: ["challenges", "orders"]
  verbs: ["get", "list", "watch"]
EOF
  # PortDock administers the namespaces of the environments, and nothing else.
  for n in $(printf '%s' "$ENVIRONMENTS" | tr ',' '\n' | cut -d= -f2); do
    cat <<EOF
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: portdock
  namespace: $n
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: admin
subjects:
- kind: ServiceAccount
  name: portdock
  namespace: $NS
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: portdock-ingress
  namespace: $n
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: portdock-ingress
subjects:
- kind: ServiceAccount
  name: portdock
  namespace: $NS
EOF
  done
  cat <<EOF
---
apiVersion: v1
kind: Service
metadata:
  name: portdock
  namespace: $NS
spec:
  selector:
    app: portdock
  ports:
  - name: http
    port: 8000
    targetPort: 8000
---
# One replica: PortDock keeps its state in a SQLite file. Its data is in
# $DATA_DIR on this server, at the same path as in the pod, because the
# Docker daemon of the server (its socket is mounted) builds the images.
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: portdock
  namespace: $NS
spec:
  serviceName: portdock
  replicas: 1
  selector:
    matchLabels:
      app: portdock
  template:
    metadata:
      labels:
        app: portdock
    spec:
      serviceAccountName: portdock
      containers:
      - name: portdock
        image: $IMAGE
        ports:
        - containerPort: 8000
        env:
        - name: PORTDOCK_PUBLIC_URL
          value: https://$DOMAIN
        - name: PORTDOCK_KUBE_SERVERS
          value: "$ENVIRONMENTS"
        - name: PORTDOCK_KUBE_CLUSTER_ISSUER
          value: letsencrypt
        - name: PORTDOCK_REGISTRY_HOST
          value: $DOMAIN
        - name: PORTDOCK_REGISTRY_USERNAME
          value: $REGISTRY_USER
        - name: PORTDOCK_REGISTRY_PASSWORD
          valueFrom:
            secretKeyRef:
              name: portdock-registry
              key: password
        - name: PORTDOCK_SETUP_TOKEN
          valueFrom:
            secretKeyRef:
              name: portdock-setup
              key: token
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8000
          periodSeconds: 5
        livenessProbe:
          httpGet:
            path: /api/health
            port: 8000
          initialDelaySeconds: 30
          periodSeconds: 20
        resources:
          requests:
            cpu: 100m
            memory: 256Mi
        volumeMounts:
        - name: data
          mountPath: $DATA_DIR
        - name: docker
          mountPath: /var/run/docker.sock
      volumes:
      - name: data
        hostPath:
          path: $DATA_DIR
          type: DirectoryOrCreate
      - name: docker
        hostPath:
          path: /var/run/docker.sock
          type: Socket
EOF
} | kubectl apply -f - >/dev/null
echo "    image: $IMAGE"

# ---------------------------------------------------------------- domain
step "Dashboard on https://$DOMAIN"
middleware=""
if kubectl apply -f - >/dev/null 2>&1 <<EOF
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: https-redirect
  namespace: $NS
spec:
  redirectScheme:
    scheme: https
    permanent: true
EOF
then
  middleware="    traefik.ingress.kubernetes.io/router.middlewares: $NS-https-redirect@kubernetescrd"
fi
kubectl apply -f - >/dev/null <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: portdock
  namespace: $NS
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
$middleware
spec:
  ingressClassName: traefik
  rules:
  - host: $DOMAIN
    http:
      paths:
      - path: /v2
        pathType: Prefix
        backend:
          service:
            name: registry
            port:
              number: 5000
      - path: /
        pathType: Prefix
        backend:
          service:
            name: portdock
            port:
              number: 8000
  tls:
  - hosts: [$DOMAIN]
    secretName: portdock-tls
EOF

kubectl -n "$NS" rollout status deploy/registry --timeout=300s >/dev/null || warn "the registry is not ready yet (kubectl -n $NS get pods)"
kubectl -n "$NS" rollout status statefulset/portdock --timeout=300s >/dev/null || die "PortDock did not start (kubectl -n $NS logs statefulset/portdock)"
echo "    PortDock: running"
if wait_for 300 "certificate issued" sh -c "kubectl -n $NS get certificate portdock-tls -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' | grep -qx True"; then
  # This server reaches its own domain: images are pushed and pulled there.
  # Where it cannot (some clouds do not route a server to its own public
  # address), the domain is pointed at the node in /etc/hosts.
  if ! curl -fsS -o /dev/null --max-time 10 -u "$REGISTRY_USER:$REGISTRY_PASSWORD" "https://$DOMAIN/v2/"; then
    node_ip=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
    sed -i "/# portdock-registry$/d" /etc/hosts
    echo "$node_ip $DOMAIN # portdock-registry" >>/etc/hosts
    curl -fsS -o /dev/null --max-time 10 -u "$REGISTRY_USER:$REGISTRY_PASSWORD" "https://$DOMAIN/v2/" ||
      warn "the registry does not answer on https://$DOMAIN/v2/ from this server: builds will fail to push (kubectl -n $NS logs deploy/registry)"
  fi
  echo "    registry: answers on https://$DOMAIN/v2/"
else
  warn "no certificate for $DOMAIN yet. Check that its DNS record points to ${PUBLIC_IP:-this server} and that ports 80 and 443 are open,"
  warn "then watch: kubectl -n $NS get certificate. It is issued as soon as Let's Encrypt reaches the server."
fi

# ---------------------------------------------------------------- done
echo
bold "PortDock is ready."
echo
echo "  Open this link to create your owner account:"
echo
bold "    https://$DOMAIN/login#setup=$SETUP_TOKEN"
echo
echo "  The link holds the setup token: until the owner account exists, nobody can claim the dashboard without it."
echo "  Show it again: kubectl -n $NS get secret portdock-setup -o jsonpath='{.data.token}' | base64 -d"
echo
echo "  Ready in PortDock once you sign in:"
echo "    servers   test (namespace portdock-test) and prod (namespace portdock-prod), with HTTPS certificates from Let's Encrypt"
echo "              build (Docker on this server): builds the images of applications from Git"
echo "    registry  $DOMAIN (user $REGISTRY_USER): images are pushed there and the cluster pulls them"
echo
echo "  Then: Settings → Git to connect GitHub, and New resource in a project (environment \"production\" → server prod)."
echo "  Applications get their domains in their Domains tab: point each one's DNS record to ${PUBLIC_IP:-this server}."
echo "  Update PortDock: run this installer again (PORTDOCK_VERSION=vX.Y.Z for a given version)."
}

main "$@"
