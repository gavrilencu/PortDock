package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/updater"
)

// PortDock in a pod updates itself with a Job of its namespace (see
// updater.LaunchKube). kubectl and serviceAccountNamespace are replaced in tests.
var (
	kubectl                 updater.KubeRunner = updater.Kubectl
	serviceAccountNamespace                    = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	lookPath                                   = exec.LookPath
)

// kubeNamespace is the namespace of PortDock's pod.
func kubeNamespace() string {
	if b, err := os.ReadFile(serviceAccountNamespace); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	return "portdock"
}

// podName is the name of PortDock's pod (its hostname).
func podName() string {
	if v := os.Getenv("POD_NAME"); v != "" {
		return v
	}
	h, _ := os.Hostname()
	return h
}

// kubeUpdateSupport reports whether PortDock can update itself in its
// cluster, and why not. The answer is kept for a few minutes: it costs a few
// calls to the cluster's API.
func (s *Server) kubeUpdateSupport(ctx context.Context) (updater.KubeSelf, bool, string) {
	c := s.updates
	c.mu.Lock()
	if !c.kubeCheckedAt.IsZero() && time.Since(c.kubeCheckedAt) < 5*time.Minute {
		self, ok, reason := c.kubeSelf, c.kubeOK, c.kubeReason
		c.mu.Unlock()
		return self, ok, reason
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	ns := kubeNamespace()
	manual := fmt.Sprintf("Meanwhile, update with: kubectl -n %s set image statefulset/portdock portdock=<image>:<version>", ns)
	self, err := updater.KubeSelf{}, error(nil)
	if _, lerr := lookPath("kubectl"); lerr != nil {
		err = fmt.Errorf("kubectl is not in this image")
	} else if self, err = updater.ReadKubeSelf(ctx, kubectl, ns, podName()); err == nil {
		err = updater.CanSelfUpdate(ctx, kubectl, ns)
	}
	ok, reason := err == nil, ""
	if err != nil {
		reason = "PortDock runs in Kubernetes and cannot update itself yet (" + err.Error() + "). Run the Kubernetes installer again, or apply deploy/kubernetes/portdock.yaml again, to give it the right. " + manual
	}
	c.mu.Lock()
	c.kubeCheckedAt, c.kubeSelf, c.kubeOK, c.kubeReason = time.Now(), self, ok, reason
	c.mu.Unlock()
	return self, ok, reason
}

// launchKubeUpdate starts the update of a PortDock running in a pod.
func (s *Server) launchKubeUpdate(ctx context.Context, plan updater.Plan) error {
	self, ok, reason := s.kubeUpdateSupport(ctx)
	if !ok {
		return fmt.Errorf("%s", reason)
	}
	_, err := updater.LaunchKube(ctx, kubectl, self, plan)
	return err
}

// finishKubeUpdate records, once PortDock has restarted, how an update of
// the cluster ended, and tells about a rollback (a success is reported by
// UpdateWatch like on Docker).
func (s *Server) finishKubeUpdate(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, changed := updater.FinishKube(ctx, kubectl, s.cfg.DataDir, Version, kubeNamespace())
	if !changed || st.Status != "rolled_back" {
		return
	}
	slog.Warn("update rolled back", "to", st.To, "err", st.Error)
	s.db.LogActivity(ctx, 0, "update.rolled_back", st.To+" → "+Version)
	s.notify.Send(notify.Event{Name: "update.failed", Title: "PortDock update to " + st.To + " was rolled back", Message: st.Error})
}
