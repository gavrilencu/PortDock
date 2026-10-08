package api

import (
	"context"
	"net/http"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/store"
)

// Persistent storage of an application: Docker volumes managed by PortDock
// and, for administrators, directories of the server. Changes apply on the
// next deployment.

type volumeInput struct {
	Name      *string `json:"name"`
	MountPath *string `json:"mount_path"`
	HostPath  *string `json:"host_path"`
	ReadOnly  *bool   `json:"read_only"`
}

type volumesResponse struct {
	Volumes        []store.AppVolume `json:"volumes"`
	RequiresDeploy bool              `json:"requires_deploy"`
}

func (s *Server) volumesOf(ctx context.Context, app store.Application) (volumesResponse, error) {
	vs, err := s.db.Volumes(ctx, app.ID)
	if vs == nil {
		vs = []store.AppVolume{}
	}
	return volumesResponse{Volumes: vs, RequiresDeploy: app.CurrentImage != ""}, err
}

func (s *Server) loadVolume(r *http.Request) (store.AppVolume, store.Application, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.AppVolume{}, store.Application{}, err
	}
	v, err := store.ByID[store.AppVolume](r.Context(), s.db, "app_volumes", id)
	if err != nil {
		return v, store.Application{}, err
	}
	a, err := store.ByID[store.Application](r.Context(), s.db, "applications", v.ApplicationID)
	return v, a, err
}

func (s *Server) listAppVolumes(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	res, err := s.volumesOf(r.Context(), app)
	if err != nil {
		return err
	}
	return ok(w, res)
}

// appVolumeUsage reports which volumes exist on each server of the application.
func (s *Server) appVolumeUsage(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	return ok(w, s.engine.VolumeUsage(r.Context(), app))
}

// checkVolume validates v against the other volumes of app. previousHost is
// the server directory saved before (an administrator's choice stays valid
// when someone else edits the mount path).
func (s *Server) checkVolume(ctx context.Context, app store.Application, v *store.AppVolume, previousHost string) error {
	if app.BuildPack == "compose" {
		return badRequest("Docker Compose applications declare their volumes in the compose file")
	}
	if err := store.NormalizeVolume(v); err != nil {
		return badRequest("%v", err)
	}
	if v.HostPath != "" {
		if v.HostPath != previousHost && !auth.UserFrom(ctx).IsAdmin() {
			return errStatus(403, "only an administrator can mount a directory of the server")
		}
		if err := s.engine.CheckHostPath(v.HostPath); err != nil {
			return badRequest("%v", err)
		}
	}
	others, err := s.db.Volumes(ctx, app.ID)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID == v.ID {
			continue
		}
		if o.Name == v.Name {
			return errStatus(409, "a volume named %q already exists", v.Name)
		}
		if o.MountPath == v.MountPath {
			return errStatus(409, "volume %q is already mounted at %s", o.Name, v.MountPath)
		}
	}
	return nil
}

func applyVolumeInput(v *store.AppVolume, in volumeInput) {
	if in.Name != nil {
		v.Name = *in.Name
	}
	if in.MountPath != nil {
		v.MountPath = *in.MountPath
	}
	if in.HostPath != nil {
		v.HostPath = *in.HostPath
	}
	if in.ReadOnly != nil {
		v.ReadOnly = *in.ReadOnly
	}
}

func (s *Server) createAppVolume(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	var in volumeInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	v := store.AppVolume{ApplicationID: app.ID, CreatedAt: store.Now()}
	applyVolumeInput(&v, in)
	if err := s.checkVolume(r.Context(), app, &v, ""); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "app_volumes", &v); err != nil {
		return err
	}
	s.activity(r, "volume.add", app.Name+": "+v.Name+" → "+v.MountPath)
	res, err := s.volumesOf(r.Context(), app)
	if err != nil {
		return err
	}
	return ok(w, res)
}

func (s *Server) updateAppVolume(w http.ResponseWriter, r *http.Request) error {
	v, app, err := s.loadVolume(r)
	if err != nil {
		return err
	}
	var in volumeInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	before := v
	applyVolumeInput(&v, in)
	if err := s.checkVolume(r.Context(), app, &v, before.HostPath); err != nil {
		return err
	}
	// The data of a Docker volume lives under its name: renaming would mount
	// an empty volume and leave the data behind.
	if v.Name != before.Name && before.HostPath == "" {
		return badRequest("a volume cannot be renamed: its data is stored under the name %q", before.Name)
	}
	if err := store.Update(r.Context(), s.db, "app_volumes", &v); err != nil {
		return err
	}
	s.activity(r, "volume.update", app.Name+": "+v.Name+" → "+v.MountPath)
	res, err := s.volumesOf(r.Context(), app)
	if err != nil {
		return err
	}
	return ok(w, res)
}

// deleteAppVolume unmounts a volume from the next deployments; ?data=true also
// deletes its data on every server, which needs the right to delete the
// application.
func (s *Server) deleteAppVolume(w http.ResponseWriter, r *http.Request) error {
	v, app, err := s.loadVolume(r)
	if err != nil {
		return err
	}
	data := r.URL.Query().Get("data") == "true" && v.HostPath == ""
	if data {
		if !s.resourceAccess(r.Context(), app.EnvironmentID).CanManage {
			return errStatus(403, "deleting the data of a volume requires the right to delete the application")
		}
		if err := s.engine.RemoveVolumeData(r.Context(), app, v.Name); err != nil {
			return errStatus(409, "%v", err)
		}
	}
	if err := store.Delete(r.Context(), s.db, "app_volumes", v.ID); err != nil {
		return err
	}
	action := "volume.remove"
	if data {
		action = "volume.delete_data"
	}
	s.activity(r, action, app.Name+": "+v.Name)
	res, err := s.volumesOf(r.Context(), app)
	if err != nil {
		return err
	}
	return ok(w, res)
}

// deleteAppVolumeData deletes the data left on the servers by a volume that is no
// longer configured (removed while a container still used it).
func (s *Server) deleteAppVolumeData(w http.ResponseWriter, r *http.Request) error {
	app, err := s.loadApp(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	vs, err := s.db.Volumes(r.Context(), app.ID)
	if err != nil {
		return err
	}
	for _, v := range vs {
		if v.Name == name {
			return errStatus(409, "volume %q is still configured; remove it with its data instead", name)
		}
	}
	if err := store.NormalizeVolume(&store.AppVolume{Name: name, MountPath: "/x"}); err != nil {
		return badRequest("%v", err)
	}
	if err := s.engine.RemoveVolumeData(r.Context(), app, name); err != nil {
		return errStatus(409, "%v", err)
	}
	s.activity(r, "volume.delete_data", app.Name+": "+name)
	return ok(w, map[string]bool{"ok": true})
}
