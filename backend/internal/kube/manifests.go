package kube

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Object is a Kubernetes manifest.
type Object map[string]any

func (o Object) name() string {
	meta, _ := o["metadata"].(Object)
	name, _ := meta["name"].(string)
	return name
}

// AppName is the name of the Deployment, Service and Ingress of a resource.
// Resource UUIDs are lowercase hex, and the prefix makes the name start with a
// letter, as Service names must.
func AppName(uuid string) string { return "pd-" + strings.ToLower(uuid) }

// EnvSecretName is the Secret holding the variables of a resource.
func EnvSecretName(uuid string) string { return AppName(uuid) + "-env" }

// ClaimName is the PersistentVolumeClaim of one volume of a resource.
func ClaimName(uuid, volume string) string { return AppName(uuid) + "-" + volume }

// PullSecretName is the Secret with the credentials of a registry.
func PullSecretName(registryID int64) string { return fmt.Sprintf("pd-registry-%d", registryID) }

// Selector renders labels as a label selector, in a stable order.
func Selector(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + labels[k]
	}
	return strings.Join(parts, ",")
}

func meta(name string, labels map[string]string) Object {
	m := Object{"name": name}
	if len(labels) > 0 {
		m["labels"] = labels
	}
	return m
}

// Workload describes the pods of one application.
type Workload struct {
	Name string
	// Selector identifies the pods and never changes for a resource; Labels are
	// set on every object and pod, and must include Selector.
	Selector map[string]string
	Labels   map[string]string
	// PodLabels are added to the pods only (the deployment they belong to):
	// a change replaces the pods.
	PodLabels map[string]string
	Image     string
	// PullAlways pulls the image on every start (tags that move, like "latest").
	PullAlways bool
	PullSecret string
	Replicas   int
	// Command replaces the image's entrypoint, Args its arguments.
	Command []string
	Args    []string
	// Ports the container listens on; the first is the one probed.
	Ports []int
	// EnvSecret is the Secret whose keys become environment variables.
	EnvSecret string
	// CPU and Memory are limits, as Kubernetes quantities ("" for none).
	CPU    string
	Memory string
	// HealthPath is probed over HTTP on the first port; without it the port is
	// probed with a TCP connection.
	HealthPath string
	// StartupSeconds is how long the application may take to answer.
	StartupSeconds int
	Mounts         []Mount
}

// Mount is a PersistentVolumeClaim mounted in the container.
type Mount struct {
	Claim    string
	Path     string
	ReadOnly bool
}

// escapeExpansion keeps Kubernetes from expanding "$(VAR)" in a command: the
// shell (or the program) must see what the user typed.
func escapeExpansion(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$(", "$$(")
	}
	return out
}

func (w Workload) podLabels() map[string]string {
	out := make(map[string]string, len(w.Labels)+len(w.PodLabels))
	for k, v := range w.Labels {
		out[k] = v
	}
	for k, v := range w.PodLabels {
		out[k] = v
	}
	return out
}

func (w Workload) podSpec(probes bool) Object {
	c := Object{"name": "app", "image": w.Image, "imagePullPolicy": "IfNotPresent"}
	if w.PullAlways {
		c["imagePullPolicy"] = "Always"
	}
	if cmd := escapeExpansion(w.Command); cmd != nil {
		c["command"] = cmd
	}
	if args := escapeExpansion(w.Args); args != nil {
		c["args"] = args
	}
	if len(w.Ports) > 0 {
		ports := make([]Object, len(w.Ports))
		for i, p := range w.Ports {
			ports[i] = Object{"containerPort": p, "protocol": "TCP"}
		}
		c["ports"] = ports
	}
	if w.EnvSecret != "" {
		c["envFrom"] = []Object{{"secretRef": Object{"name": w.EnvSecret}}}
	}
	limits := Object{}
	if w.CPU != "" {
		limits["cpu"] = w.CPU
	}
	if w.Memory != "" {
		limits["memory"] = w.Memory
	}
	if len(limits) > 0 {
		c["resources"] = Object{"limits": limits}
	}
	if probes && len(w.Ports) > 0 {
		check := Object{"tcpSocket": Object{"port": w.Ports[0]}}
		if w.HealthPath != "" {
			p := w.HealthPath
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			check = Object{"httpGet": Object{"path": p, "port": w.Ports[0]}}
		}
		probe := func(period, failures int) Object {
			o := Object{"periodSeconds": period, "timeoutSeconds": 5, "failureThreshold": failures}
			for k, v := range check {
				o[k] = v
			}
			return o
		}
		// The startup probe gives a slow application its time; afterwards the
		// readiness probe takes a pod that stops answering out of the Service.
		c["startupProbe"] = probe(2, max(w.StartupSeconds, 10)/2)
		c["readinessProbe"] = probe(5, 3)
	}
	spec := Object{
		"containers": []Object{c},
		// Service links would add a <NAME>_PORT variable for every Service of
		// the namespace (REDIS_PORT=tcp://…), which breaks applications reading
		// those names; and applications do not need the cluster's API.
		"enableServiceLinks":           false,
		"automountServiceAccountToken": false,
	}
	if w.PullSecret != "" {
		spec["imagePullSecrets"] = []Object{{"name": w.PullSecret}}
	}
	if len(w.Mounts) > 0 {
		vols, mounts := make([]Object, len(w.Mounts)), make([]Object, len(w.Mounts))
		for i, m := range w.Mounts {
			name := "v" + strconv.Itoa(i)
			vols[i] = Object{"name": name, "persistentVolumeClaim": Object{"claimName": m.Claim}}
			mounts[i] = Object{"name": name, "mountPath": m.Path, "readOnly": m.ReadOnly}
		}
		spec["volumes"] = vols
		c["volumeMounts"] = mounts
	}
	return spec
}

// Deployment renders the Deployment of a workload. New pods replace the old
// ones only once they are ready, so a version that does not start leaves the
// previous one serving. With volumes the old pods stop first: a volume that
// one node mounts cannot follow a new pod to another node.
func Deployment(w Workload) Object {
	strategy := Object{"type": "RollingUpdate", "rollingUpdate": Object{"maxSurge": 1, "maxUnavailable": 0}}
	if len(w.Mounts) > 0 {
		strategy = Object{"type": "Recreate"}
	}
	return Object{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": meta(w.Name, w.Labels),
		"spec": Object{
			"replicas":             max(w.Replicas, 0),
			"revisionHistoryLimit": 5,
			"selector":             Object{"matchLabels": w.Selector},
			"strategy":             strategy,
			"template":             Object{"metadata": Object{"labels": w.podLabels()}, "spec": w.podSpec(true)},
		},
	}
}

// Job renders a Job running the workload's command once, without retries.
func Job(name string, w Workload, timeout time.Duration) Object {
	spec := w.podSpec(false)
	spec["restartPolicy"] = "Never"
	return Object{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": meta(name, w.Labels),
		"spec": Object{
			"backoffLimit":            0,
			"activeDeadlineSeconds":   max(int(timeout.Seconds()), 1),
			"ttlSecondsAfterFinished": 600,
			"template":                Object{"metadata": Object{"labels": w.podLabels()}, "spec": spec},
		},
	}
}

// Service renders a ClusterIP Service in front of the pods matching selector,
// one port per container port.
func Service(name string, labels, selector map[string]string, ports []int) Object {
	sp := make([]Object, len(ports))
	for i, p := range ports {
		sp[i] = Object{"name": "p" + strconv.Itoa(p), "port": p, "targetPort": p, "protocol": "TCP"}
	}
	return Object{
		"apiVersion": "v1", "kind": "Service",
		"metadata": meta(name, labels),
		"spec":     Object{"type": "ClusterIP", "selector": selector, "ports": sp},
	}
}

// Secret renders an Opaque Secret.
func Secret(name string, labels, data map[string]string) Object {
	enc := make(map[string]string, len(data))
	for k, v := range data {
		enc[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return Object{"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": meta(name, labels), "data": enc}
}

// PullSecret renders the Secret pods use to pull images from a registry.
func PullSecret(name string, labels map[string]string, host, user, password string) Object {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	cfg, _ := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"username": user, "password": password, "auth": auth}}})
	return Object{
		"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/dockerconfigjson",
		"metadata": meta(name, labels),
		"data":     map[string]string{".dockerconfigjson": base64.StdEncoding.EncodeToString(cfg)},
	}
}

// Claim renders a PersistentVolumeClaim (storageClass "" uses the cluster's default).
func Claim(name string, labels map[string]string, size, storageClass string) Object {
	spec := Object{"accessModes": []string{"ReadWriteOnce"}, "resources": Object{"requests": Object{"storage": size}}}
	if storageClass != "" {
		spec["storageClassName"] = storageClass
	}
	return Object{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": meta(name, labels), "spec": spec}
}

// Route sends a host (and path prefix) to a port of a Service.
type Route struct {
	Host    string
	Path    string
	Service string
	Port    int
	TLS     bool
}

// IngressOptions are the settings of the cluster an Ingress is rendered for.
type IngressOptions struct {
	// Class is the ingress class ("" uses the cluster's default).
	Class string
	// Issuer is the cert-manager ClusterIssuer that issues the certificates;
	// without one, HTTPS hosts use the ingress controller's default certificate.
	Issuer string
	// Middlewares are the Traefik middlewares of the Ingress's routers
	// ("<namespace>-<name>@kubernetescrd"), such as the HTTPS redirect.
	Middlewares string
}

// TLSSecretName is the Secret cert-manager stores the certificate of host in.
func TLSSecretName(host string) string {
	return "pd-tls-" + slug(strings.Replace(host, "*", "wildcard", 1), 55)
}

// slug reduces s to a DNS label of at most n characters; a shortened name
// ends with a hash of the whole, so two long names never collide.
func slug(s string, n int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > n {
		sum := sha256.Sum256([]byte(s))
		out = strings.TrimRight(out[:n-9], "-") + "-" + hex.EncodeToString(sum[:4])
	}
	return out
}

// Ingress renders the Ingress routing hosts to Services.
func Ingress(name string, labels map[string]string, routes []Route, o IngressOptions) Object {
	byHost := map[string][]Object{}
	var hosts, tlsHosts []string
	seenTLS := map[string]bool{}
	for _, r := range routes {
		p := r.Path
		if p == "" {
			p = "/"
		}
		if _, ok := byHost[r.Host]; !ok {
			hosts = append(hosts, r.Host)
		}
		byHost[r.Host] = append(byHost[r.Host], Object{
			"path": p, "pathType": "Prefix",
			"backend": Object{"service": Object{"name": r.Service, "port": Object{"number": r.Port}}},
		})
		if r.TLS && !seenTLS[r.Host] {
			seenTLS[r.Host] = true
			tlsHosts = append(tlsHosts, r.Host)
		}
	}
	rules := make([]Object, len(hosts))
	for i, h := range hosts {
		rules[i] = Object{"host": h, "http": Object{"paths": byHost[h]}}
	}
	spec := Object{"rules": rules}
	if o.Class != "" {
		spec["ingressClassName"] = o.Class
	}
	m := meta(name, labels)
	annotations := map[string]string{}
	if len(tlsHosts) > 0 {
		tls := make([]Object, len(tlsHosts))
		for i, h := range tlsHosts {
			tls[i] = Object{"hosts": []string{h}}
			if o.Issuer != "" {
				tls[i]["secretName"] = TLSSecretName(h)
			}
		}
		spec["tls"] = tls
		if o.Issuer != "" {
			annotations["cert-manager.io/cluster-issuer"] = o.Issuer
		}
	}
	if o.Middlewares != "" {
		annotations["traefik.ingress.kubernetes.io/router.middlewares"] = o.Middlewares
	}
	if len(annotations) > 0 {
		m["annotations"] = annotations
	}
	return Object{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": m, "spec": spec}
}

// MemoryQuantity converts a Docker memory limit (512m, 1g, 1.5g, 1073741824)
// to a Kubernetes quantity in bytes. Docker's units are binary: 1m is 1 MiB.
func MemoryQuantity(limit string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(limit))
	if s == "" {
		return "", nil
	}
	mult := float64(1)
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30},
		{"ki", 1 << 10}, {"mi", 1 << 20}, {"gi", 1 << 30}, {"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"b", 1}} {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = n, u.mult
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return "", fmt.Errorf("invalid memory limit %q (examples: 512m, 1g)", limit)
	}
	return strconv.FormatInt(int64(v*mult), 10), nil
}

// CPUQuantity converts a Docker CPU limit (0.5, 2) to a Kubernetes quantity (500m, 2000m).
func CPUQuantity(limit string) (string, error) {
	s := strings.TrimSpace(limit)
	if s == "" {
		return "", nil
	}
	if m, ok := strings.CutSuffix(s, "m"); ok {
		if n, err := strconv.Atoi(m); err == nil && n > 0 {
			return s, nil
		}
		return "", fmt.Errorf("invalid CPU limit %q (examples: 0.5, 2)", limit)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return "", fmt.Errorf("invalid CPU limit %q (examples: 0.5, 2)", limit)
	}
	return strconv.Itoa(int(v*1000+0.5)) + "m", nil
}
