#!/usr/bin/env bash
# Prepares a Kubernetes cluster for PortDock and prints the kubeconfig to paste
# in Servers → Connect server → Kubernetes cluster.
#
# Run it where kubectl already reaches the cluster, as someone who may create
# namespaces and role bindings:
#
#   curl -fsSL https://raw.githubusercontent.com/gavrilencu/PortDock/main/deploy/kubernetes/connect.sh | bash
#   curl -fsSL …/connect.sh | bash -s -- my-namespace      # another namespace than "portdock"
#
# It creates, in the namespace: a service account "portdock" that administers
# that namespace only, and a token for it that does not expire. Cluster-wide
# it only grants reading nodes and ingress classes (skipped if you may not).
# To revoke the access: kubectl delete namespace <namespace>
set -euo pipefail

NS="${1:-portdock}"
command -v kubectl >/dev/null || { echo "kubectl is not installed" >&2; exit 1; }
case "$NS" in
  kube-system|kube-public|kube-node-lease) echo "the namespace $NS belongs to Kubernetes itself" >&2; exit 1 ;;
esac

SERVER="${PORTDOCK_KUBE_SERVER:-$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')}"
[ -n "$SERVER" ] || { echo "kubectl has no cluster selected" >&2; exit 1; }

kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS" >&2

kubectl apply -f - >&2 <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: portdock
  namespace: $NS
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: portdock
  namespace: $NS
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: admin
subjects:
- kind: ServiceAccount
  name: portdock
  namespace: $NS
---
apiVersion: v1
kind: Secret
metadata:
  name: portdock-token
  namespace: $NS
  annotations:
    kubernetes.io/service-account.name: portdock
type: kubernetes.io/service-account-token
EOF

# Optional: lets PortDock show the cluster's capacity and its ingress classes.
kubectl apply -f - >&2 <<EOF || echo "note: could not grant read access to nodes and ingress classes (not needed to deploy)" >&2
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
  name: portdock-reader-$NS
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: portdock-reader
subjects:
- kind: ServiceAccount
  name: portdock
  namespace: $NS
EOF

TOKEN=""
for _ in $(seq 1 30); do
  TOKEN=$(kubectl -n "$NS" get secret portdock-token -o jsonpath='{.data.token}' 2>/dev/null | base64 --decode 2>/dev/null || true)
  [ -n "$TOKEN" ] && break
  sleep 1
done
[ -n "$TOKEN" ] || { echo "the cluster did not issue a token for the service account" >&2; exit 1; }
CA=$(kubectl -n "$NS" get secret portdock-token -o jsonpath='{.data.ca\.crt}')

case "$SERVER" in
  *//127.0.0.1*|*//localhost*|*//0.0.0.0*)
    echo "warning: kubectl reaches the cluster at $SERVER, which PortDock cannot use from another machine." >&2
    echo "         Run again with PORTDOCK_KUBE_SERVER=https://<address of the cluster>:6443" >&2 ;;
esac

echo >&2
echo "Paste what follows in PortDock (Servers → Connect server → Kubernetes cluster), namespace: $NS" >&2
echo >&2
cat <<EOF
apiVersion: v1
kind: Config
current-context: portdock
clusters:
- name: cluster
  cluster:
    server: $SERVER
    certificate-authority-data: $CA
contexts:
- name: portdock
  context:
    cluster: cluster
    user: portdock
    namespace: $NS
users:
- name: portdock
  user:
    token: $TOKEN
EOF
