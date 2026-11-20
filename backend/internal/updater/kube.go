package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Updating a PortDock that runs in Kubernetes (a pod of a StatefulSet): a Job
// of the current, known-good image points the StatefulSet at the new image,
// waits for its pod to become ready, and when it does not, goes back to the
// previous version and deletes the broken pod (a StatefulSet does not replace
// a pod that never became ready by itself). The Job runs on its own, so it
// finishes while PortDock is restarted; the version that starts afterwards
// records the outcome (FinishKube).

// KubeSelf is the pod PortDock runs in.
type KubeSelf struct {
	Namespace      string
	Pod            string
	StatefulSet    string
	Container      string
	Image          string // the image this pod runs; the update Job runs it too
	ServiceAccount string
}

// KubeRunner runs kubectl with args (stdin may be empty) and returns its output.
type KubeRunner func(ctx context.Context, stdin string, args ...string) (string, error)

// Kubectl runs the kubectl of the image, which reaches the cluster with the
// pod's service account.
func Kubectl(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return strings.TrimSpace(out.String()), errors.New(msg)
		}
		return strings.TrimSpace(out.String()), err
	}
	return strings.TrimSpace(out.String()), nil
}

// ReadKubeSelf reads the pod named pod in namespace: its StatefulSet,
// container, image and service account.
func ReadKubeSelf(ctx context.Context, run KubeRunner, namespace, pod string) (KubeSelf, error) {
	out, err := run(ctx, "", "-n", namespace, "get", "pod", pod, "-o", "json")
	if err != nil {
		return KubeSelf{}, fmt.Errorf("read the pod %s: %w", pod, err)
	}
	var p struct {
		Metadata struct {
			OwnerReferences []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
		Spec struct {
			ServiceAccountName string `json:"serviceAccountName"`
			Containers         []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"containers"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return KubeSelf{}, fmt.Errorf("read the pod %s: %w", pod, err)
	}
	self := KubeSelf{Namespace: namespace, Pod: pod, ServiceAccount: p.Spec.ServiceAccountName}
	for _, o := range p.Metadata.OwnerReferences {
		if o.Kind == "StatefulSet" {
			self.StatefulSet = o.Name
		}
	}
	if self.StatefulSet == "" {
		return KubeSelf{}, fmt.Errorf("the pod %s does not belong to a StatefulSet: update PortDock with kubectl", pod)
	}
	if len(p.Spec.Containers) == 0 {
		return KubeSelf{}, fmt.Errorf("the pod %s has no container", pod)
	}
	self.Container, self.Image = p.Spec.Containers[0].Name, p.Spec.Containers[0].Image
	return self, nil
}

// kubeUpdateRights are what the update Job and its follow-up need in
// PortDock's namespace.
var kubeUpdateRights = [][2]string{
	{"create", "jobs.batch"}, {"patch", "statefulsets.apps"}, {"list", "controllerrevisions.apps"}, {"delete", "pods"}, {"get", "pods/log"},
}

// CanSelfUpdate reports what PortDock is missing to update itself in namespace.
func CanSelfUpdate(ctx context.Context, run KubeRunner, namespace string) error {
	var missing []string
	for _, r := range kubeUpdateRights {
		out, _ := run(ctx, "", "-n", namespace, "auth", "can-i", r[0], r[1])
		if strings.TrimSpace(out) != "yes" {
			missing = append(missing, r[0]+" "+r[1])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the service account of PortDock may not %s in the namespace %s", strings.Join(missing, ", "), namespace)
	}
	return nil
}

// kubeUpdateScript is what the update Job runs; NS, STS, CONTAINER, IMAGE and
// POD come from its environment.
const kubeUpdateScript = `echo "Updating statefulset/$STS ($CONTAINER) to $IMAGE"
kubectl -n "$NS" set image "statefulset/$STS" "$CONTAINER=$IMAGE" || exit 1
if kubectl -n "$NS" rollout status "statefulset/$STS" --timeout=300s; then
  echo "PortDock runs $IMAGE"
  exit 0
fi
echo "The new version did not become ready within 5 minutes: going back to the previous one"
kubectl -n "$NS" rollout undo "statefulset/$STS"
kubectl -n "$NS" delete pod "$POD" --wait=false
kubectl -n "$NS" rollout status "statefulset/$STS" --timeout=300s
exit 1
`

// KubeUpdateJob is the Job named name that moves self's StatefulSet to image.
func KubeUpdateJob(self KubeSelf, name, image string) map[string]any {
	env := []map[string]string{
		{"name": "NS", "value": self.Namespace}, {"name": "STS", "value": self.StatefulSet},
		{"name": "CONTAINER", "value": self.Container}, {"name": "IMAGE", "value": image}, {"name": "POD", "value": self.Pod},
	}
	labels := map[string]string{"portdock.managed": "true", "portdock.kind": "updater"}
	return map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   map[string]any{"name": name, "namespace": self.Namespace, "labels": labels},
		"spec": map[string]any{
			"backoffLimit":            0,
			"activeDeadlineSeconds":   1500,
			"ttlSecondsAfterFinished": 7 * 24 * 3600,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"serviceAccountName": self.ServiceAccount,
					"restartPolicy":      "Never",
					"containers": []map[string]any{{
						"name": "update", "image": self.Image, "imagePullPolicy": "IfNotPresent",
						"command": []string{"sh", "-c", kubeUpdateScript}, "env": env,
					}},
				},
			},
		},
	}
}

// LaunchKube verifies the image and backs up the data like Launch, then starts
// the update Job. PortDock is restarted by it within a minute.
func LaunchKube(ctx context.Context, run KubeRunner, self KubeSelf, p Plan) (State, error) {
	now := time.Now()
	st := State{ID: fmt.Sprintf("upd-%d", now.Unix()), Status: "running", From: p.From, To: p.To, Image: p.Image, StartedAt: now.UTC().Format(time.RFC3339)}
	st.Log(p.DataDir, "Updating PortDock %s → %s (Kubernetes, statefulset/%s)", p.From, p.To, self.StatefulSet)
	fail := func(err error) (State, error) {
		st.Log(p.DataDir, "❌ %v", err)
		st.finish(p.DataDir, "failed", err)
		return st, err
	}
	if p.Verify != nil {
		pinned, msg, err := p.Verify(ctx, p.Image)
		if err != nil {
			return fail(err)
		}
		if msg != "" {
			st.Log(p.DataDir, "%s", msg)
		}
		if pinned != "" {
			p.Image, st.Image = pinned, pinned
		}
	}
	if p.Backup != nil {
		path, err := p.Backup(ctx)
		if err != nil {
			return fail(fmt.Errorf("backup failed, nothing was changed: %w", err))
		}
		st.Backup = path
		st.Log(p.DataDir, "Database backed up to %s", path)
	}
	st.Job = "portdock-" + st.ID
	manifest, _ := json.Marshal(KubeUpdateJob(self, st.Job, p.Image))
	if _, err := run(ctx, string(manifest), "-n", self.Namespace, "create", "-f", "-"); err != nil {
		return fail(fmt.Errorf("create the update job: %w", err))
	}
	st.Log(p.DataDir, "Update job %s started: PortDock restarts with the new version within a minute, and goes back to %s by itself if it does not start", st.Job, p.From)
	return st, nil
}

// FinishKube records the outcome of a Kubernetes update once PortDock has
// restarted: success when it runs the target version, rolled back when the
// previous version came back (with the end of the Job's log). It returns the
// state and whether it changed.
func FinishKube(ctx context.Context, run KubeRunner, dataDir, version, namespace string) (State, bool) {
	st := LoadState(dataDir)
	if st.Status != "running" || st.Job == "" {
		return st, false
	}
	if Compare(version, st.To) == 0 {
		st.Log(dataDir, "✅ PortDock %s is running", version)
		st.finish(dataDir, "success", nil)
		return st, true
	}
	if logs, err := run(ctx, "", "-n", namespace, "logs", "job/"+st.Job, "--tail=20"); err == nil {
		for _, line := range strings.Split(logs, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				st.Log(dataDir, "%s", line)
			}
		}
	}
	st.Log(dataDir, "↩ The new version did not start: PortDock %s is back", version)
	st.finish(dataDir, "rolled_back", fmt.Errorf("PortDock %s did not become ready; the previous version %s was restored", st.To, version))
	return st, true
}
