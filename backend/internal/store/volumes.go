package store

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// AppVolume is persistent storage mounted into an application's containers.
// Without HostPath it is a Docker volume that PortDock creates on every server
// of the application; with HostPath it bind-mounts that directory of the server.
type AppVolume struct {
	ID            int64  `db:"id" json:"id"`
	ApplicationID int64  `db:"application_id" json:"application_id"`
	Name          string `db:"name" json:"name"`
	MountPath     string `db:"mount_path" json:"mount_path"`
	HostPath      string `db:"host_path" json:"host_path"`
	ReadOnly      bool   `db:"read_only" json:"read_only"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

func (db *DB) Volumes(ctx context.Context, appID int64) ([]AppVolume, error) {
	return Select[AppVolume](ctx, db, "SELECT * FROM app_volumes WHERE application_id = ? ORDER BY mount_path", appID)
}

// VolumeName is the Docker volume holding v's data for the production
// deployment (pr == 0) or a pull request preview, which gets its own empty
// volume. The application UUID comes first so that the volume can be traced
// back to its application. Names cannot contain "_", so neither the UUID (hex)
// nor a production volume (e.g. "data-pr1") can be mistaken for a preview's.
func VolumeName(appUUID, name string, pr int) string {
	n := "pd-" + appUUID + "_" + name
	if pr > 0 {
		n += fmt.Sprintf("_pr%d", pr)
	}
	return n
}

var volumeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)

// NormalizeVolume checks and cleans the fields of v.
func NormalizeVolume(v *AppVolume) error {
	v.Name = strings.ToLower(strings.TrimSpace(v.Name))
	if !volumeNameRe.MatchString(v.Name) {
		return fmt.Errorf("the name may contain lowercase letters, digits, '.' and '-' (e.g. data, uploads)")
	}
	mp, err := cleanAbs(v.MountPath)
	if err != nil {
		return fmt.Errorf("mount path: %w", err)
	}
	v.MountPath = mp
	if strings.TrimSpace(v.HostPath) != "" {
		hp, err := cleanAbs(v.HostPath)
		if err != nil {
			return fmt.Errorf("server path: %w", err)
		}
		v.HostPath = hp
	} else {
		v.HostPath = ""
	}
	return nil
}

// cleanAbs accepts an absolute path other than "/" that docker -v can carry
// (no ':' or ',').
func cleanAbs(p string) (string, error) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%q must be an absolute path, e.g. /data", p)
	}
	if strings.ContainsAny(p, ":,\n\r\x00") {
		return "", fmt.Errorf("%q cannot contain ':' or ','", p)
	}
	p = path.Clean(p)
	if p == "/" {
		return "", fmt.Errorf("the root directory cannot be used")
	}
	return p, nil
}
