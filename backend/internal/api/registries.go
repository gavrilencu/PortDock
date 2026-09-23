package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/registry"
	"github.com/portdock/portdock/internal/store"
)

type registryOut struct {
	store.Registry
	PasswordSet bool `json:"password_set"`
	Apps        int  `json:"apps"`
}

func (s *Server) registryOut(ctx context.Context, reg store.Registry) registryOut {
	var n int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM applications WHERE registry_id = ?", reg.ID).Scan(&n)
	return registryOut{Registry: reg, PasswordSet: reg.Password != "", Apps: n}
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) error {
	regs, err := store.Select[store.Registry](r.Context(), s.db, "SELECT * FROM registries ORDER BY name")
	if err != nil {
		return err
	}
	out := make([]registryOut, 0, len(regs))
	for _, reg := range regs {
		out = append(out, s.registryOut(r.Context(), reg))
	}
	return ok(w, map[string]any{"registries": out, "kinds": registry.Kinds})
}

type registryIn struct {
	Name      *string `json:"name"`
	Kind      *string `json:"kind"`
	Host      *string `json:"host"`
	Username  *string `json:"username"`
	Password  *string `json:"password"`
	Namespace *string `json:"namespace"`
}

func (s *Server) applyRegistry(reg *store.Registry, in registryIn) error {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&reg.Name, in.Name)
	set(&reg.Kind, in.Kind)
	set(&reg.Host, in.Host)
	set(&reg.Username, in.Username)
	set(&reg.Namespace, in.Namespace)
	if in.Password != nil && *in.Password != "" {
		reg.Password = s.box.Encrypt(strings.TrimSpace(*in.Password))
	}
	kind, ok := registry.KindOf(reg.Kind)
	if !ok {
		return badRequest("unknown registry type %q", reg.Kind)
	}
	if reg.Host == "" {
		reg.Host = kind.Host
	}
	host, err := registry.NormalizeHost(reg.Host)
	if err != nil {
		return badRequest("%v", err)
	}
	reg.Host = host
	if reg.Kind == "ecr" {
		if _, err := registry.ECRRegion(host); err != nil {
			return badRequest("%v", err)
		}
	}
	reg.Namespace = strings.Trim(strings.ToLower(reg.Namespace), "/")
	if reg.Namespace == "" && (reg.Kind == "dockerhub" || reg.Kind == "ghcr" || reg.Kind == "quay") {
		reg.Namespace = strings.ToLower(reg.Username)
	}
	if reg.Namespace != "" && !registryImageRe.MatchString(reg.Namespace) {
		return badRequest("namespace may contain lowercase letters, digits, '.', '_', '-' and '/'")
	}
	if reg.Name == "" {
		reg.Name = kind.Name
		if reg.Namespace != "" {
			reg.Name += " · " + reg.Namespace
		}
	}
	return nil
}

func (s *Server) createRegistry(w http.ResponseWriter, r *http.Request) error {
	var in registryIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	reg := store.Registry{CreatedAt: store.Now()}
	if err := s.applyRegistry(&reg, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "registries", &reg); err != nil {
		return err
	}
	s.activity(r, "registry.create", reg.Name)
	return ok(w, s.registryOut(r.Context(), reg))
}

func (s *Server) loadRegistry(r *http.Request) (store.Registry, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return store.Registry{}, err
	}
	return store.ByID[store.Registry](r.Context(), s.db, "registries", id)
}

func (s *Server) updateRegistry(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry(r)
	if err != nil {
		return err
	}
	var in registryIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.applyRegistry(&reg, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "registries", &reg); err != nil {
		return err
	}
	s.activity(r, "registry.update", reg.Name)
	return ok(w, s.registryOut(r.Context(), reg))
}

func (s *Server) deleteRegistry(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry(r)
	if err != nil {
		return err
	}
	if out := s.registryOut(r.Context(), reg); out.Apps > 0 {
		return badRequest("%d application(s) use this registry; select another registry for them first", out.Apps)
	}
	if err := store.Delete(r.Context(), s.db, "registries", reg.ID); err != nil {
		return err
	}
	s.activity(r, "registry.delete", reg.Name)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) pingRegistry(ctx context.Context, reg store.Registry) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := s.engine.TestRegistry(ctx, reg); err != nil {
		return badRequest("%v", err)
	}
	return nil
}

func (s *Server) testRegistry(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.loadRegistry(r)
	if err != nil {
		return err
	}
	if err := s.pingRegistry(r.Context(), reg); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

// testRegistryDraft checks credentials before saving; an id reuses the stored secret.
func (s *Server) testRegistryDraft(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		registryIn
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	reg := store.Registry{}
	if in.ID != 0 {
		var err error
		if reg, err = store.ByID[store.Registry](r.Context(), s.db, "registries", in.ID); err != nil {
			return err
		}
	}
	if err := s.applyRegistry(&reg, in.registryIn); err != nil {
		return err
	}
	if err := s.pingRegistry(r.Context(), reg); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}
