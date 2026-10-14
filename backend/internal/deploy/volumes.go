package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/docker"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// Persistent storage of applications. Docker volumes are created by PortDock
// with labels naming the application, the volume and the pull request, so they
// can be listed and removed without parsing names. Each server of an
// application keeps its own copy; replicas on a server, and the old and new
// version during a rollout, share it.

// CheckHostPath refuses server paths that must not be mounted into an
// application: those docker.CheckBindSource refuses and PortDock's own data
// directory (database, encryption key, build checkouts).
func (e *Engine) CheckHostPath(p string) error {
	if err := docker.CheckBindSource(p); err != nil {
		return err
	}
	data := strings.TrimRight(e.cfg.DataDir, "/")
	if data != "" && (p == data || strings.HasPrefix(p, data+"/") || strings.HasPrefix(data, p+"/")) {
		return fmt.Errorf("%s holds PortDock's data and cannot be mounted into a container", p)
	}
	return nil
}

// volumeArgs creates the Docker volumes of the deployment and returns the -v
// options mounting them. Previews get their own empty volumes and no server
// directories, so that a pull request cannot change production data.
func (j *job) volumeArgs(ctx context.Context) (string, error) {
	vols, err := j.e.db.Volumes(ctx, j.app.ID)
	if err != nil {
		return "", fmt.Errorf("load volumes: %w", err)
	}
	var args, create strings.Builder
	for _, v := range vols {
		ro := ""
		if v.ReadOnly {
			ro = ":ro"
		}
		if v.HostPath != "" {
			if j.d.PRNumber > 0 {
				j.logf("Preview: server directory %s is not mounted at %s.", v.HostPath, v.MountPath)
				continue
			}
			if err := j.e.CheckHostPath(v.HostPath); err != nil {
				return "", fmt.Errorf("volume %s: %w", v.Name, err)
			}
			args.WriteString(" -v " + q(v.HostPath+":"+v.MountPath+ro))
			continue
		}
		// An existing volume is reused only if PortDock created it for this
		// volume: one made beforehand under the same name (with driver options
		// mounting a path of the server, say) is refused.
		name := store.VolumeName(j.app.UUID, v.Name, j.d.PRNumber)
		owner := fmt.Sprintf("%s/%s/%d", j.app.UUID, v.Name, j.d.PRNumber)
		format := fmt.Sprintf(`{{index .Labels %q}}/{{index .Labels %q}}/{{index .Labels %q}}`, docker.LabelResource, docker.LabelVolume, docker.LabelPR)
		fmt.Fprintf(&create, `if l=$(docker volume inspect -f %[3]s %[1]s 2>/dev/null); then [ "$l" = %[4]s ] || { echo "volume %[5]s exists but was not created by PortDock for this application" >&2; exit 1; }; `+
			`else docker volume create%[2]s %[1]s >/dev/null || exit 1; fi; `,
			q(name), volumeLabels(j.app.UUID, v.Name, j.d.PRNumber), q(format), q(owner), name)
		args.WriteString(" -v " + q(name+":"+v.MountPath+ro))
	}
	if create.Len() > 0 {
		if _, err := executor.Output(ctx, j.x, create.String()); err != nil {
			return "", fmt.Errorf("create volumes: %w", err)
		}
	}
	if len(vols) > 0 {
		j.logf("Persistent storage: %d volume(s).", len(vols))
	}
	return args.String(), nil
}

func volumeLabels(appUUID, name string, pr int) string {
	var b strings.Builder
	for _, l := range []string{docker.LabelManaged + "=true", docker.LabelKind + "=application", docker.LabelResource + "=" + appUUID, docker.LabelVolume + "=" + name, docker.LabelPR + "=" + strconv.Itoa(pr)} {
		b.WriteString(" --label " + q(l))
	}
	return b.String()
}

// volumesCmd lists the Docker volumes of an application matching the filters.
func volumesCmd(appUUID string, filters ...string) string {
	cmd := "docker volume ls -q --filter " + q("label="+docker.LabelResource+"="+appUUID)
	for _, f := range filters {
		cmd += " --filter " + q("label="+f)
	}
	return cmd
}

// VolumeUsage reports, per server, the Docker volumes of app that exist.
type VolumeUsage struct {
	ServerID   int64    `json:"server_id"`
	ServerName string   `json:"server_name"`
	Volumes    []string `json:"volumes"` // names of the production volumes present on the server
	Error      string   `json:"error,omitempty"`
}

func (e *Engine) VolumeUsage(ctx context.Context, app store.Application) []VolumeUsage {
	out := []VolumeUsage{}
	for _, id := range e.AppServerIDs(ctx, app) {
		u := VolumeUsage{ServerID: id, Volumes: []string{}}
		x, srv, err := e.pool.ForID(ctx, id)
		u.ServerName = srv.Name
		if u.ServerName == "" {
			u.ServerName = fmt.Sprintf("server #%d", id)
		}
		if err == nil {
			var res string
			res, err = executor.Output(ctx, x, "docker volume ls --format '{{.Label \""+docker.LabelVolume+"\"}}' --filter "+q("label="+docker.LabelResource+"="+app.UUID)+" --filter "+q("label="+docker.LabelPR+"=0"))
			u.Volumes = append(u.Volumes, strings.Fields(res)...)
		}
		if err != nil {
			u.Error = err.Error()
		}
		out = append(out, u)
	}
	return out
}

// RemoveVolumeData deletes the Docker volumes (production and previews) of
// one volume of app on all its servers. A volume still mounted by a container
// is left in place and reported.
func (e *Engine) RemoveVolumeData(ctx context.Context, app store.Application, name string) error {
	return e.eachTarget(ctx, app, func(t target) error {
		_, err := executor.Output(ctx, t.x, volumesCmd(app.UUID, docker.LabelVolume+"="+name)+" | xargs -r docker volume rm")
		if err != nil && strings.Contains(err.Error(), "in use") {
			return errors.New("the volume is still used by a container; redeploy the application without it, then delete the data")
		}
		return err
	})
}

// removeAppVolumes deletes every Docker volume of app on one server; its
// containers must be gone already.
func removeAppVolumes(ctx context.Context, x executor.Executor, appUUID string, filters ...string) error {
	return x.Run(ctx, volumesCmd(appUUID, filters...)+" | xargs -r docker volume rm -f >/dev/null 2>&1; true", executor.RunOpts{})
}

// logf writes to the deployment log, if any (scheduled jobs reuse the
// container setup of a deployment without having one).
func (j *job) logf(format string, args ...any) {
	if j.log != nil {
		j.log.Printf(format, args...)
	}
}
