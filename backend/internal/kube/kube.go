// Package kube drives a Kubernetes cluster with kubectl, the way package
// docker drives a Docker host with the docker CLI.
//
// A cluster is one of PortDock's servers. Cluster implements
// executor.Executor, so it travels through the same pool as local and SSH
// servers, and docker.Backend, so everything that lists containers, reads
// their logs or checks their state works on pods without knowing about
// Kubernetes. Shell commands are refused: code that has not been ported to
// Kubernetes fails with ErrNotDocker instead of running `docker …` somewhere
// it was not meant to.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
)

// ErrNotDocker is returned for operations that only exist on Docker servers.
var ErrNotDocker = errors.New("this operation is not available on a Kubernetes cluster yet")

// runner executes kubectl with args; tests replace it.
type runner func(ctx context.Context, in io.Reader, out, errw io.Writer, args ...string) error

// Cluster is one namespace of a Kubernetes cluster, reached with kubectl.
type Cluster struct {
	// Namespace holds everything PortDock creates on the cluster.
	Namespace string
	// kubeconfig is the file kubectl reads: the server's own, or one made of
	// the service account when PortDock runs in a pod of the cluster.
	kubeconfig string
	run        runner
}

// New returns the cluster described by a kubeconfig file, limited to namespace.
func New(kubeconfigFile, namespace string) *Cluster {
	return &Cluster{Namespace: namespace, kubeconfig: kubeconfigFile, run: kubectl}
}

// NewWithRunner returns a cluster whose kubectl is run. Tests of the packages
// that deploy to a cluster script it with this.
func NewWithRunner(namespace string, run func(ctx context.Context, in io.Reader, out, errw io.Writer, args ...string) error) *Cluster {
	return &Cluster{Namespace: namespace, run: run}
}

func kubectl(ctx context.Context, in io.Reader, out, errw io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errw
	// A cancelled context (a closed log stream) kills kubectl; without a delay
	// Wait could block on pipes held by a child.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("kubectl is not installed where PortDock runs")
	}
	return err
}

// args prefixes a with the options selecting the cluster and the namespace.
func (c *Cluster) args(a ...string) []string {
	var base []string
	if c.kubeconfig != "" {
		base = append(base, "--kubeconfig", c.kubeconfig)
	}
	if c.Namespace != "" {
		base = append(base, "--namespace", c.Namespace)
	}
	return append(base, a...)
}

// output runs kubectl and returns its standard output; the error carries what
// kubectl wrote to standard error.
func (c *Cluster) output(ctx context.Context, in io.Reader, a ...string) (string, error) {
	var out, errb bytes.Buffer
	if err := c.run(ctx, in, &out, &errb, c.args(a...)...); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		if msg == "" {
			return out.String(), err
		}
		return out.String(), fmt.Errorf("%s", cleanError(msg))
	}
	return out.String(), nil
}

// cleanError drops kubectl's "Error from server (Reason): " prefix noise but
// keeps the reason, which callers match on (NotFound, Forbidden).
func cleanError(msg string) string {
	return strings.TrimPrefix(strings.TrimPrefix(msg, "error: "), "Error: ")
}

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "(NotFound)") || strings.Contains(err.Error(), "not found"))
}

func isForbidden(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "(Forbidden)") || strings.Contains(err.Error(), "forbidden"))
}

// ---- executor.Executor ----

// Run refuses shell commands: a cluster has no shell, and running them where
// PortDock runs would aim `docker …` at the wrong machine.
func (c *Cluster) Run(context.Context, string, executor.RunOpts) error { return ErrNotDocker }

// PTY refuses a shell on the cluster itself; Terminal opens one in a pod.
func (c *Cluster) PTY(context.Context, string, int, int) (executor.Session, error) {
	return nil, ErrNotDocker
}

func (c *Cluster) Close() error { return nil }

// ---- the cluster ----

// Version is the Kubernetes version of the API server (e.g. v1.31.2).
func (c *Cluster) Version(ctx context.Context) (string, error) {
	out, err := c.output(ctx, nil, "get", "--raw", "/version", "--request-timeout=15s")
	if err != nil {
		return "", err
	}
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil || v.GitVersion == "" {
		return "", errors.New("the cluster did not report its version")
	}
	return v.GitVersion, nil
}

// EngineVersion implements docker.Backend.
func (c *Cluster) EngineVersion(ctx context.Context) (string, error) { return c.Version(ctx) }

// EnsureNamespace creates the namespace when it does not exist. Credentials
// limited to the namespace cannot read it: that is taken as "it exists", and
// CanDeploy tells whether they can work in it.
func (c *Cluster) EnsureNamespace(ctx context.Context) error {
	if c.Namespace == "" {
		return errors.New("no namespace configured")
	}
	_, err := c.output(ctx, nil, "get", "namespace", c.Namespace, "-o", "name", "--request-timeout=15s")
	switch {
	case err == nil, isForbidden(err):
		return nil
	case !isNotFound(err):
		return err
	}
	if _, err := c.output(ctx, nil, "create", "namespace", c.Namespace, "--request-timeout=15s"); err != nil {
		return fmt.Errorf("the namespace %q does not exist and could not be created: %w", c.Namespace, err)
	}
	return nil
}

// CanDeploy checks that the credentials may create Deployments in the namespace.
func (c *Cluster) CanDeploy(ctx context.Context) error {
	out, err := c.output(ctx, nil, "auth", "can-i", "create", "deployments.apps", "--request-timeout=15s")
	if strings.TrimSpace(out) == "yes" {
		return nil
	}
	if err != nil && strings.TrimSpace(out) != "no" {
		return err
	}
	return fmt.Errorf("these credentials cannot create Deployments in the namespace %q", c.Namespace)
}

// IngressClasses lists the ingress classes of the cluster, the default one first.
// Credentials limited to a namespace cannot list them: that is not an error.
func (c *Cluster) IngressClasses(ctx context.Context) []string {
	out, err := c.output(ctx, nil, "get", "ingressclasses", "-o", "json", "--request-timeout=15s")
	if err != nil {
		return nil
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return nil
	}
	var names []string
	for _, it := range list.Items {
		if it.Metadata.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
			names = append([]string{it.Metadata.Name}, names...)
		} else {
			names = append(names, it.Metadata.Name)
		}
	}
	return names
}

// HasResource reports whether the cluster serves an API resource
// (e.g. "clusterissuers.cert-manager.io").
func (c *Cluster) HasResource(ctx context.Context, resource string) bool {
	name, group, _ := strings.Cut(resource, ".")
	out, err := c.output(ctx, nil, "api-resources", "--api-group="+group, "-o", "name", "--request-timeout=15s")
	if err != nil {
		return false
	}
	for _, line := range strings.Fields(out) {
		if line == resource || line == name {
			return true
		}
	}
	return false
}

// ---- objects ----

// Apply creates or updates objects (server-side apply: PortDock owns the
// fields it sets, so a field it stops setting is removed).
func (c *Cluster) Apply(ctx context.Context, objects ...Object) error {
	if len(objects) == 0 {
		return nil
	}
	list := Object{"apiVersion": "v1", "kind": "List", "items": objects}
	body, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = c.output(ctx, bytes.NewReader(body), "apply", "--server-side", "--force-conflicts", "--field-manager=portdock", "-f", "-")
	return err
}

// Delete removes the objects of the given kinds matching a label selector.
func (c *Cluster) Delete(ctx context.Context, kinds []string, selector string) error {
	if selector == "" {
		return errors.New("refusing to delete without a selector")
	}
	_, err := c.output(ctx, nil, "delete", strings.Join(kinds, ","), "-l", selector, "--ignore-not-found", "--wait=false")
	return err
}

// DeleteNamed removes one object by name (no error when it does not exist).
func (c *Cluster) DeleteNamed(ctx context.Context, kind, name string) error {
	_, err := c.output(ctx, nil, "delete", kind, name, "--ignore-not-found", "--wait=false")
	return err
}

// Label returns the value of a label of an object, and whether the object exists.
func (c *Cluster) Label(ctx context.Context, kind, name, label string) (value string, exists bool, err error) {
	out, err := c.output(ctx, nil, "get", kind, name, "-o", "json", "--request-timeout=15s")
	if isNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var obj struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		return "", true, err
	}
	return obj.Metadata.Labels[label], true, nil
}

// Names lists the names of the objects of a kind matching a label selector.
func (c *Cluster) Names(ctx context.Context, kind, selector string) ([]string, error) {
	out, err := c.output(ctx, nil, "get", kind, "-l", selector, "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}", "--request-timeout=15s")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// ---- deployments ----

// RolloutStatus waits until every pod of a Deployment runs the new version
// and is ready, writing progress to w.
func (c *Cluster) RolloutStatus(ctx context.Context, name string, timeout time.Duration, w io.Writer) error {
	var errb bytes.Buffer
	err := c.run(ctx, nil, w, io.MultiWriter(w, &errb), c.args("rollout", "status", "deployment/"+name, fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())))...)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return fmt.Errorf("%s", cleanError(lastLine(msg)))
		}
	}
	return err
}

// fatalWaiting are the reasons a pod will not start without a change: waiting
// for the rollout's timeout would only delay the error.
var fatalWaiting = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true, "InvalidImageName": true,
	"CreateContainerConfigError": true, "CreateContainerError": true, "RunContainerError": true,
}

// ErrNoAnswer marks a pod of the new version that was stopped, or stopped by
// itself, before it ever answered its health check: most often because the
// application listens on another port than the one probed.
var ErrNoAnswer = errors.New("it never answered its health check")

// startFailure tells why a pod of a new version will not become ready without
// a change, or returns nil while it may still. A container restarted before
// it was ever ready already had its whole startup time: the next runs end the
// same way, so there is no point waiting through the back-off of each.
func (p pod) startFailure() error {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready || cs.RestartCount == 0 {
			continue
		}
		last := cs.LastState.Terminated
		switch {
		case last == nil:
		case last.ExitCode == 0 || last.ExitCode == 137 || last.ExitCode == 143:
			// Exit 0 or a signal: stopped by the kubelet after the startup probe
			// failed (or the application exited by itself).
			return fmt.Errorf("pod %s was restarted %d time(s): %w (last run ended with exit code %d)", p.Metadata.Name, cs.RestartCount, ErrNoAnswer, last.ExitCode)
		default:
			reason := last.Reason
			if reason == "" {
				reason = "Error"
			}
			return fmt.Errorf("pod %s cannot start: its container exits with code %d (%s), restarted %d time(s)", p.Metadata.Name, last.ExitCode, reason, cs.RestartCount)
		}
	}
	if reason, msg := p.waiting(); fatalWaiting[reason] {
		err := fmt.Errorf("pod %s cannot start: %s", p.Metadata.Name, reason)
		if msg != "" {
			err = fmt.Errorf("%w (%s)", err, msg)
		}
		return err
	}
	return nil
}

// Rollout waits for the rollout of a Deployment like RolloutStatus, and stops
// early when a pod matching podSelector (the pods of the new version) cannot
// start: an image that cannot be pulled, a container that keeps crashing, or
// one restarted before it ever answered its health check (startFailure).
func (c *Cluster) Rollout(ctx context.Context, name, podSelector string, timeout time.Duration, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failed := make(chan error, 1)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			pods, err := c.pods(ctx, podSelector)
			if err != nil {
				continue
			}
			for _, p := range pods {
				if err := p.startFailure(); err != nil {
					failed <- err
					cancel()
					return
				}
			}
		}
	}()
	err := c.RolloutStatus(ctx, name, timeout, w)
	select {
	case ferr := <-failed:
		return ferr
	default:
		return err
	}
}

// PodTemplate is what the pods of a Deployment run with.
type PodTemplate struct {
	Image      string
	EnvSecret  string
	PullSecret string
	Mounts     []Mount
}

// Template reads the image, variables and volumes of a Deployment's pods, so a
// job can run in a pod like them.
func (c *Cluster) Template(ctx context.Context, name string) (PodTemplate, error) {
	var t PodTemplate
	out, err := c.output(ctx, nil, "get", "deployment", name, "-o", "json", "--request-timeout=15s")
	if err != nil {
		return t, err
	}
	var d struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Image   string `json:"image"`
						EnvFrom []struct {
							SecretRef struct {
								Name string `json:"name"`
							} `json:"secretRef"`
						} `json:"envFrom"`
						VolumeMounts []struct {
							Name      string `json:"name"`
							MountPath string `json:"mountPath"`
							ReadOnly  bool   `json:"readOnly"`
						} `json:"volumeMounts"`
					} `json:"containers"`
					ImagePullSecrets []struct {
						Name string `json:"name"`
					} `json:"imagePullSecrets"`
					Volumes []struct {
						Name  string `json:"name"`
						Claim struct {
							ClaimName string `json:"claimName"`
						} `json:"persistentVolumeClaim"`
					} `json:"volumes"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return t, err
	}
	spec := d.Spec.Template.Spec
	if len(spec.Containers) == 0 {
		return t, errors.New("the deployment has no container")
	}
	ct := spec.Containers[0]
	t.Image = ct.Image
	for _, ef := range ct.EnvFrom {
		if ef.SecretRef.Name != "" {
			t.EnvSecret = ef.SecretRef.Name
		}
	}
	if len(spec.ImagePullSecrets) > 0 {
		t.PullSecret = spec.ImagePullSecrets[0].Name
	}
	claims := map[string]string{}
	for _, v := range spec.Volumes {
		claims[v.Name] = v.Claim.ClaimName
	}
	for _, m := range ct.VolumeMounts {
		if claim := claims[m.Name]; claim != "" {
			t.Mounts = append(t.Mounts, Mount{Claim: claim, Path: m.MountPath, ReadOnly: m.ReadOnly})
		}
	}
	return t, nil
}

// LabelValues lists the value of a label on the objects of a kind matching a selector.
func (c *Cluster) LabelValues(ctx context.Context, kind, selector, label string) ([]string, error) {
	out, err := c.output(ctx, nil, "get", kind, "-l", selector, "-o", "json", "--request-timeout=15s")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	var values []string
	for _, it := range list.Items {
		if v := it.Metadata.Labels[label]; v != "" {
			values = append(values, v)
		}
	}
	return values, nil
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// RolloutUndo returns a Deployment to its previous version.
func (c *Cluster) RolloutUndo(ctx context.Context, name string) error {
	_, err := c.output(ctx, nil, "rollout", "undo", "deployment/"+name)
	return err
}

// Restart replaces the pods of a Deployment one by one.
func (c *Cluster) Restart(ctx context.Context, name string) error {
	_, err := c.output(ctx, nil, "rollout", "restart", "deployment/"+name)
	return err
}

// Scale sets the number of pods of a Deployment.
func (c *Cluster) Scale(ctx context.Context, name string, replicas int) error {
	_, err := c.output(ctx, nil, "scale", "deployment/"+name, fmt.Sprintf("--replicas=%d", replicas))
	return err
}

// DeploymentExists reports whether the Deployment exists.
func (c *Cluster) DeploymentExists(ctx context.Context, name string) (bool, error) {
	_, err := c.output(ctx, nil, "get", "deployment", name, "-o", "name", "--request-timeout=15s")
	if isNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// Exec runs a command in a pod of the Deployment, writing its output to w.
func (c *Cluster) Exec(ctx context.Context, deployment string, w io.Writer, command ...string) error {
	a := append([]string{"exec", "deployment/" + deployment, "--"}, command...)
	return c.run(ctx, nil, w, w, c.args(a...)...)
}

// ---- jobs ----

// RunJob runs a Job to completion and writes its output to w. The Job is
// removed afterwards, whatever the result.
func (c *Cluster) RunJob(ctx context.Context, job Object, timeout time.Duration, w io.Writer) error {
	name := job.name()
	if name == "" {
		return errors.New("the job has no name")
	}
	_ = c.DeleteNamed(ctx, "job", name) // a leftover of an interrupted run
	if err := c.Apply(ctx, job); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = c.output(cleanup, nil, "delete", "job", name, "--ignore-not-found", "--wait=false", "--cascade=background")
	}()
	logs := func() {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = c.run(lctx, nil, w, w, c.args("logs", "job/"+name, "--tail=500")...)
	}
	deadline := time.Now().Add(timeout)
	for {
		out, err := c.output(ctx, nil, "get", "job", name, "-o", "json", "--request-timeout=15s")
		if err != nil {
			return err
		}
		var j struct {
			Status struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			} `json:"status"`
		}
		_ = json.Unmarshal([]byte(out), &j)
		switch {
		case j.Status.Succeeded > 0:
			logs()
			return nil
		case j.Status.Failed > 0:
			logs()
			return errors.New("the command exited with an error")
		case time.Now().After(deadline):
			logs()
			return fmt.Errorf("the command did not finish within %s: %w", timeout, context.DeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			logs()
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ---- pods as containers (docker.Backend) ----

type pod struct {
	Metadata struct {
		Name              string            `json:"name"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp string            `json:"creationTimestamp"`
		DeletionTimestamp string            `json:"deletionTimestamp"`
	} `json:"metadata"`
	Spec struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
			Ports []struct {
				ContainerPort int    `json:"containerPort"`
				Protocol      string `json:"protocol"`
			} `json:"ports"`
			Env []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"env"`
			Command        []string        `json:"command"`
			Args           []string        `json:"args"`
			ReadinessProbe json.RawMessage `json:"readinessProbe"`
			Resources      json.RawMessage `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase      string `json:"phase"`
		PodIP      string `json:"podIP"`
		Reason     string `json:"reason"`
		StartTime  string `json:"startTime"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
			State        struct {
				Waiting *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"waiting"`
				Terminated *struct {
					Reason   string `json:"reason"`
					ExitCode int    `json:"exitCode"`
				} `json:"terminated"`
			} `json:"state"`
			// LastState is how the previous run of a restarted container ended.
			LastState struct {
				Terminated *struct {
					Reason   string `json:"reason"`
					ExitCode int    `json:"exitCode"`
				} `json:"terminated"`
			} `json:"lastState"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

// waiting is the reason the pod's containers are not running ("" when they are).
func (p pod) waiting() (reason, message string) {
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			return w.Reason, w.Message
		}
	}
	return "", ""
}

func (p pod) ready() (ready, total int) {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return ready, len(p.Spec.Containers)
}

func (p pod) restarts() (n int) {
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}

// state maps the pod to the container states the rest of PortDock knows
// (running, created, restarting, exited) and a line describing it.
func (p pod) state() (state, status string) {
	reason, _ := p.waiting()
	ready, total := p.ready()
	switch {
	case reason == "CrashLoopBackOff":
		state, status = "restarting", "CrashLoopBackOff"
	case p.Status.Phase == "Running":
		state, status = "running", fmt.Sprintf("Running (%d/%d ready)", ready, total)
	case p.Status.Phase == "Pending":
		state, status = "created", "Pending"
		if reason != "" {
			status += ": " + reason
		}
	case p.Status.Phase == "Succeeded":
		state, status = "exited", "Completed"
	default:
		state, status = "exited", firstNonEmpty(p.Status.Reason, p.Status.Phase, "Unknown")
	}
	if n := p.restarts(); n > 0 {
		status += fmt.Sprintf(", %d restart(s)", n)
	}
	return state, status
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (p pod) container() docker.Container {
	state, status := p.state()
	c := docker.Container{
		ID: p.Metadata.Name, Name: p.Metadata.Name, State: state, Status: status,
		Networks: p.Status.PodIP, CreatedAt: p.Metadata.CreationTimestamp, Labels: p.Metadata.Labels,
	}
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	var ports []string
	for i, ct := range p.Spec.Containers {
		if i == 0 {
			c.Image = ct.Image
		}
		for _, pt := range ct.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", pt.ContainerPort, strings.ToLower(firstNonEmpty(pt.Protocol, "TCP"))))
		}
	}
	c.Ports = strings.Join(ports, ", ")
	return c
}

// selector turns docker `--filter` values into a label selector. Only label
// filters exist on both sides; anything else is refused instead of ignored,
// because a filter that silently matches everything would widen whatever the
// caller does next.
func selector(filters []string) (string, error) {
	var sel []string
	for _, f := range filters {
		v, ok := strings.CutPrefix(f, "label=")
		if !ok || v == "" {
			return "", fmt.Errorf("filter %q is not supported on Kubernetes", f)
		}
		sel = append(sel, v)
	}
	return strings.Join(sel, ","), nil
}

func (c *Cluster) pods(ctx context.Context, sel string) ([]pod, error) {
	a := []string{"get", "pods", "-o", "json", "--request-timeout=30s"}
	if sel != "" {
		a = append(a, "-l", sel)
	}
	out, err := c.output(ctx, nil, a...)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []pod `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("read the pods: %w", err)
	}
	return list.Items, nil
}

// Containers implements docker.Backend: the pods of the namespace, as
// containers. Pods being deleted are left out; without all, so are those not running.
func (c *Cluster) Containers(ctx context.Context, all bool, filters ...string) ([]docker.Container, error) {
	sel, err := selector(filters)
	if err != nil {
		return nil, err
	}
	pods, err := c.pods(ctx, sel)
	if err != nil {
		return nil, err
	}
	out := make([]docker.Container, 0, len(pods))
	for _, p := range pods {
		// A pod being deleted is on its way out, and one that completed (a
		// finished job) is what `docker run --rm` leaves behind: nothing.
		if p.Metadata.DeletionTimestamp != "" || p.Status.Phase == "Succeeded" {
			continue
		}
		ct := p.container()
		if !all && ct.State != "running" {
			continue
		}
		out = append(out, ct)
	}
	return out, nil
}

// ImageDigest is the image a running pod matching the selector pulled, pinned
// to its digest (registry/name@sha256:…); "" when it cannot be told.
func (c *Cluster) ImageDigest(ctx context.Context, sel string) string {
	out, err := c.output(ctx, nil, "get", "pods", "-l", sel, "-o", "jsonpath={range .items[*]}{.status.containerStatuses[0].imageID}{\"\\n\"}{end}", "--request-timeout=15s")
	if err != nil {
		return ""
	}
	for _, id := range strings.Fields(out) {
		id = strings.TrimPrefix(id, "docker-pullable://")
		if name, digest, ok := strings.Cut(id, "@"); ok && name != "" && strings.HasPrefix(digest, "sha256:") {
			return id
		}
	}
	return ""
}

func (c *Cluster) pod(ctx context.Context, name string) (pod, json.RawMessage, error) {
	var p pod
	if err := checkName(name); err != nil {
		return p, nil, err
	}
	out, err := c.output(ctx, nil, "get", "pod", name, "-o", "json", "--request-timeout=15s")
	if err != nil {
		return p, nil, err
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return p, nil, err
	}
	return p, json.RawMessage(out), nil
}

// checkName refuses names kubectl could read as an option or another resource.
func checkName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "/ \t\n") {
		return fmt.Errorf("invalid pod name %q", name)
	}
	return nil
}

// ContainerInspect implements docker.Backend. The document has the shape of
// `docker inspect` for the fields PortDock reads (labels, image, environment,
// state), plus the pod itself under "Pod". Variables that come from a Secret
// are not in the pod and so are not listed.
func (c *Cluster) ContainerInspect(ctx context.Context, name string) (json.RawMessage, error) {
	p, raw, err := c.pod(ctx, name)
	if err != nil {
		return nil, err
	}
	return json.Marshal([]map[string]any{p.inspect(raw)})
}

// InspectContainers returns the inspect documents of every pod matching the
// label filters, from one request (the topology map reads them all at once).
func (c *Cluster) InspectContainers(ctx context.Context, filters ...string) (json.RawMessage, error) {
	sel, err := selector(filters)
	if err != nil {
		return nil, err
	}
	a := []string{"get", "pods", "-o", "json", "--request-timeout=30s"}
	if sel != "" {
		a = append(a, "-l", sel)
	}
	out, err := c.output(ctx, nil, a...)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("read the pods: %w", err)
	}
	docs := []map[string]any{}
	for _, raw := range list.Items {
		var p pod
		if json.Unmarshal(raw, &p) != nil || p.Metadata.DeletionTimestamp != "" || p.Status.Phase == "Succeeded" {
			continue
		}
		docs = append(docs, p.inspect(raw))
	}
	return json.Marshal(docs)
}

// inspect renders the pod as a `docker inspect` document; raw is the pod itself.
func (p pod) inspect(raw json.RawMessage) map[string]any {
	state, status := p.state()
	env := []string{}
	var image string
	var cmd, args []string
	if len(p.Spec.Containers) > 0 {
		ct := p.Spec.Containers[0]
		image, cmd, args = ct.Image, ct.Command, ct.Args
		for _, e := range ct.Env {
			env = append(env, e.Name+"="+e.Value)
		}
	}
	labels := p.Metadata.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	st := map[string]any{
		"Status": state, "Running": state == "running", "Restarting": state == "restarting",
		"StartedAt": p.Status.StartTime, "Description": status,
	}
	if _, msg := p.waiting(); msg != "" {
		st["Error"] = msg
	}
	for _, cs := range p.Status.ContainerStatuses {
		if t := cs.State.Terminated; t != nil {
			st["ExitCode"], st["OOMKilled"] = t.ExitCode, t.Reason == "OOMKilled"
		}
	}
	if len(p.Spec.Containers) > 0 && len(p.Spec.Containers[0].ReadinessProbe) > 0 && state == "running" {
		health := "starting"
		if ready, total := p.ready(); total > 0 && ready == total {
			health = "healthy"
		}
		st["Health"] = map[string]any{"Status": health}
	}
	return map[string]any{
		"Id":           p.Metadata.Name,
		"Name":         "/" + p.Metadata.Name,
		"Created":      p.Metadata.CreationTimestamp,
		"Path":         strings.Join(cmd, " "),
		"Args":         args,
		"RestartCount": p.restarts(),
		"State":        st,
		"Config":       map[string]any{"Image": image, "Labels": labels, "Env": env, "Cmd": args, "Entrypoint": cmd},
		"NetworkSettings": map[string]any{
			"IPAddress": p.Status.PodIP,
			"Networks":  map[string]any{"pod": map[string]any{"IPAddress": p.Status.PodIP}},
		},
		"Node": p.Spec.NodeName,
		"Pod":  raw,
	}
}

// ContainerState implements docker.Backend: the state of a pod, and its
// readiness as health ("" without a readiness probe).
func (c *Cluster) ContainerState(ctx context.Context, name string) (state, health string, err error) {
	p, _, err := c.pod(ctx, name)
	if err != nil {
		return "", "", err
	}
	state, _ = p.state()
	if len(p.Spec.Containers) > 0 && len(p.Spec.Containers[0].ReadinessProbe) > 0 && state == "running" {
		health = "starting"
		if ready, total := p.ready(); total > 0 && ready == total {
			health = "healthy"
		}
	}
	return state, health, nil
}

// ContainerAction implements docker.Backend. A pod belongs to its Deployment,
// which replaces it at once: restarting it is deleting it, and it cannot be
// stopped or started on its own.
func (c *Cluster) ContainerAction(ctx context.Context, action, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	switch action {
	case "restart", "kill", "remove", "rm":
		_, err := c.output(ctx, nil, "delete", "pod", name, "--ignore-not-found", "--wait=false")
		return err
	case "start", "stop", "pause", "unpause":
		return errors.New("a pod cannot be " + map[string]string{"start": "started", "stop": "stopped", "pause": "paused", "unpause": "resumed"}[action] +
			" on its own: stop, start or scale its application instead")
	}
	return fmt.Errorf("unknown action %q", action)
}

// ContainerLogs implements docker.Backend.
func (c *Cluster) ContainerLogs(ctx context.Context, name string, tail int, follow, timestamps bool, w io.Writer) error {
	if err := checkName(name); err != nil {
		return err
	}
	a := []string{"logs", name, fmt.Sprintf("--tail=%d", tail)}
	if follow {
		a = append(a, "-f")
	}
	if timestamps {
		a = append(a, "--timestamps")
	}
	err := c.run(ctx, nil, w, w, c.args(a...)...)
	if ctx.Err() != nil {
		return nil // the viewer left
	}
	return err
}

// ContainerStats implements docker.Backend with the metrics API. A cluster
// without metrics-server has no usage figures: that is an empty list, not an error.
func (c *Cluster) ContainerStats(ctx context.Context, names ...string) ([]docker.Stat, error) {
	out, err := c.output(ctx, nil, "top", "pods", "--no-headers", "--request-timeout=15s")
	if err != nil {
		// No metrics-server, or pods so new that it has no sample of them yet.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "metrics api not available") || strings.Contains(msg, "metrics.k8s.io") ||
			strings.Contains(msg, "metrics not available yet") || isForbidden(err) {
			return []docker.Stat{}, nil
		}
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	stats := []docker.Stat{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || (len(want) > 0 && !want[f[0]]) {
			continue
		}
		stats = append(stats, docker.Stat{
			Container: f[0], Name: f[0],
			CPUPerc:  fmt.Sprintf("%.2f%%", milliCPU(f[1])/10),
			MemUsage: memDocker(f[2]) + " / 0B", MemPerc: "0.00%", NetIO: "0B / 0B", BlockIO: "0B / 0B", PIDs: "0",
		})
	}
	return stats, nil
}

// milliCPU reads kubectl's CPU figures: "250m" or "2" (cores).
func milliCPU(s string) float64 {
	var v float64
	if m, ok := strings.CutSuffix(s, "m"); ok {
		_, _ = fmt.Sscanf(m, "%g", &v)
		return v
	}
	if n, ok := strings.CutSuffix(s, "n"); ok {
		_, _ = fmt.Sscanf(n, "%g", &v)
		return v / 1e6
	}
	_, _ = fmt.Sscanf(s, "%g", &v)
	return v * 1000
}

// memDocker rewrites kubectl's memory figures ("64Mi") the way docker stats
// prints them ("64MiB"), which the rest of PortDock parses.
func memDocker(s string) string {
	for _, u := range []string{"Ki", "Mi", "Gi", "Ti"} {
		if strings.HasSuffix(s, u) {
			return s + "B"
		}
	}
	return s + "B"
}

// HostMetrics implements docker.Backend with what a cluster has in place of
// a host: the capacity of its nodes (when the credentials may list them) and
// the pods of the namespace.
func (c *Cluster) HostMetrics(ctx context.Context) (docker.Metrics, error) {
	var m docker.Metrics
	pods, err := c.pods(ctx, "")
	if err != nil {
		return m, err
	}
	for _, p := range pods {
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		m.Containers++
		if st, _ := p.state(); st == "running" {
			m.RunningCont++
		}
	}
	m.OS = "Kubernetes"
	if v, err := c.Version(ctx); err == nil {
		m.Kernel = v
	}
	out, err := c.output(ctx, nil, "get", "nodes", "-o", "json", "--request-timeout=15s")
	if err != nil {
		return m, nil // credentials limited to the namespace
	}
	var nodes struct {
		Items []struct {
			Status struct {
				Capacity map[string]string `json:"capacity"`
				NodeInfo struct {
					OSImage string `json:"osImage"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &nodes) != nil {
		return m, nil
	}
	for _, n := range nodes.Items {
		m.CPUs += int(milliCPU(n.Status.Capacity["cpu"]) / 1000)
		m.MemTotal += quantityBytes(n.Status.Capacity["memory"])
	}
	if len(nodes.Items) > 0 {
		m.OS = fmt.Sprintf("Kubernetes, %d node(s)", len(nodes.Items))
	}
	return m, nil
}

// quantityBytes reads a Kubernetes memory quantity (e.g. 16384Ki, 8Gi, 1000000).
func quantityBytes(s string) int64 {
	units := []struct {
		suffix string
		mult   float64
	}{{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			var v float64
			_, _ = fmt.Sscanf(n, "%g", &v)
			return int64(v * u.mult)
		}
	}
	var v float64
	_, _ = fmt.Sscanf(s, "%g", &v)
	return int64(v)
}

// Terminal implements docker.Backend: an interactive shell in a pod.
func (c *Cluster) Terminal(ctx context.Context, name string, rows, cols int) (executor.Session, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	a := c.args("exec", "-it", name, "--", "sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi")
	quoted := make([]string, 0, len(a)+1)
	quoted = append(quoted, "kubectl")
	for _, s := range a {
		quoted = append(quoted, executor.Quote(s))
	}
	return executor.Local{}.PTY(ctx, "exec "+strings.Join(quoted, " "), rows, cols)
}

// PodProblems describes the pods matching a selector that are not ready, with
// the end of their logs: what a failed rollout shows in the deployment log.
func (c *Cluster) PodProblems(ctx context.Context, sel string, w io.Writer) {
	pods, err := c.pods(ctx, sel)
	if err != nil {
		return
	}
	for _, p := range pods {
		if p.Metadata.DeletionTimestamp != "" {
			continue
		}
		ready, total := p.ready()
		if p.Status.Phase == "Running" && ready == total && total > 0 {
			continue
		}
		_, status := p.state()
		fmt.Fprintf(w, "Pod %s: %s\n", p.Metadata.Name, status)
		if _, msg := p.waiting(); msg != "" {
			fmt.Fprintf(w, "  %s\n", msg)
		}
		if out, err := c.output(ctx, nil, "get", "events", "--field-selector", "involvedObject.name="+p.Metadata.Name+",type=Warning",
			"-o", "jsonpath={range .items[*]}{.reason}: {.message}{\"\\n\"}{end}", "--request-timeout=15s"); err == nil {
			for _, line := range lastLines(out, 5) {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
		fmt.Fprintf(w, "Pod logs (%s):\n", p.Metadata.Name)
		_ = c.run(ctx, nil, w, w, c.args("logs", p.Metadata.Name, "--tail=50")...)
		return // one pod is enough to see what is wrong
	}
}

func lastLines(s string, n int) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
