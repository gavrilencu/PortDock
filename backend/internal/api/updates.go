package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/updater"
)

// DefaultUpdateRepo is the GitHub repository (owner/name) this build was released
// from. Release builds set it with -ldflags, so every installation finds its
// updates without configuration; Settings → Updates can override it (forks, mirrors).
var DefaultUpdateRepo = ""

type updateCache struct {
	mu        sync.Mutex
	latest    *updater.Release
	err       string
	checkedAt time.Time
	running   bool
}

func (s *Server) updateRepo(ctx context.Context) string {
	if v := s.db.Setting(ctx, "update_repo"); v != "" {
		return v
	}
	if v := config.Env("UPDATE_REPO", ""); v != "" {
		return v
	}
	return DefaultUpdateRepo
}

func (s *Server) updateImage(ctx context.Context, tag string) string {
	img := s.db.Setting(ctx, "update_image")
	if img == "" {
		img = "ghcr.io/" + strings.ToLower(s.updateRepo(ctx))
	}
	return img + ":" + tag
}

// checkUpdate refreshes the latest release (cached for 6 hours unless forced).
func (s *Server) checkUpdate(ctx context.Context, force bool) {
	c := s.updates
	c.mu.Lock()
	fresh := time.Since(c.checkedAt) < 6*time.Hour && !force
	c.mu.Unlock()
	if fresh {
		return
	}
	repo := s.updateRepo(ctx)
	var rel updater.Release
	err := fmt.Errorf("this is a development build without a release source; install a published release to get automatic updates")
	if repo != "" {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		rel, err = updater.LatestRelease(cctx, repo, s.box.MustDecrypt(s.db.Setting(ctx, "update_token")), s.db.Setting(ctx, "update_channel") == "prerelease")
		cancel()
	}
	c.mu.Lock()
	c.checkedAt = time.Now()
	if err != nil {
		c.err, c.latest = err.Error(), nil
	} else {
		c.err, c.latest = "", &rel
	}
	c.mu.Unlock()
	if err == nil && updater.Compare(Version, rel.Tag) < 0 && s.db.Setting(ctx, "update_notified") != rel.Tag {
		_ = s.db.SetSetting(ctx, "update_notified", rel.Tag)
		s.notify.Send(notify.Event{Name: "update.available", Title: "PortDock " + rel.Tag + " is available", Message: "You are running " + Version + ". Update from the sidebar or Settings → Updates.", URL: rel.URL, Success: true})
	}
}

func (s *Server) updateStatus(ctx context.Context, admin bool) map[string]any {
	c := s.updates
	c.mu.Lock()
	latest, errMsg, checked, running := c.latest, c.err, c.checkedAt, c.running
	c.mu.Unlock()
	res := map[string]any{"current": Version, "update_available": latest != nil && updater.Compare(Version, latest.Tag) < 0, "latest": latest}
	if !checked.IsZero() {
		res["checked_at"] = checked.UTC().Format(time.RFC3339)
	}
	if !admin {
		return res
	}
	st := updater.LoadState(s.cfg.DataDir)
	if running && st.Status == "" {
		st.Status = "running"
	}
	if st.Status == "running" {
		if started, err := time.Parse(time.RFC3339, st.StartedAt); err == nil && time.Since(started) > 20*time.Minute {
			st.Status, st.Error = "failed", "the update did not finish within 20 minutes; the current version kept running"
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	supported, reason := true, ""
	d := updater.NewDocker()
	if !d.Available(dctx) {
		supported, reason = false, "PortDock does not run in Docker with the Docker socket mounted."
	} else if _, err := updater.SelfContainer(dctx, d); err != nil {
		supported, reason = false, err.Error()
	}
	res["error"] = errMsg
	res["state"] = st
	res["supported"], res["unsupported_reason"] = supported, reason
	res["settings"] = map[string]any{
		"repo": s.updateRepo(ctx), "image": s.db.Setting(ctx, "update_image"), "default_image": "ghcr.io/" + strings.ToLower(s.updateRepo(ctx)),
		"channel": firstNonEmpty(s.db.Setting(ctx, "update_channel"), "stable"), "auto": s.db.Setting(ctx, "update_auto") == "true",
		"official_repo": DefaultUpdateRepo, "overridden": s.db.Setting(ctx, "update_repo") != "" || s.db.Setting(ctx, "update_image") != "",
		"token_set": s.db.Setting(ctx, "update_token") != "",
	}
	return res
}

func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) error {
	go s.checkUpdate(context.WithoutCancel(r.Context()), false)
	return ok(w, s.updateStatus(r.Context(), s.isAdmin(r)))
}

func (s *Server) isAdmin(r *http.Request) bool {
	u, err := s.auth.Authenticate(r)
	return err == nil && u.IsAdmin()
}

func (s *Server) checkUpdateNow(w http.ResponseWriter, r *http.Request) error {
	s.checkUpdate(r.Context(), true)
	return ok(w, s.updateStatus(r.Context(), true))
}

func (s *Server) updateUpdateSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Repo    *string `json:"repo"`
		Image   *string `json:"image"`
		Token   *string `json:"token"`
		Channel *string `json:"channel"`
		Auto    *bool   `json:"auto"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if in.Repo != nil {
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(*in.Repo), ".git"), "https://github.com/"))
		if v != "" && !updater.ValidRepo(v) {
			return badRequest("repository must look like owner/name")
		}
		_ = s.db.SetSetting(ctx, "update_repo", v)
	}
	if in.Image != nil {
		v := strings.TrimSpace(*in.Image)
		if strings.Contains(v, ":") && !strings.Contains(v[strings.LastIndex(v, "/")+1:], ":") || strings.ContainsAny(v, " @") {
			return badRequest("image must be a repository without tag, e.g. ghcr.io/acme/portdock")
		}
		_ = s.db.SetSetting(ctx, "update_image", strings.TrimSuffix(v, ":latest"))
	}
	if in.Token != nil && *in.Token != "" {
		_ = s.db.SetSetting(ctx, "update_token", s.box.Encrypt(strings.TrimSpace(*in.Token)))
	}
	if in.Channel != nil {
		if *in.Channel != "stable" && *in.Channel != "prerelease" {
			return badRequest("channel must be stable or prerelease")
		}
		_ = s.db.SetSetting(ctx, "update_channel", *in.Channel)
	}
	if in.Auto != nil {
		_ = s.db.SetSetting(ctx, "update_auto", fmt.Sprint(*in.Auto))
	}
	s.activity(r, "update.settings", "")
	s.checkUpdate(ctx, true)
	return ok(w, s.updateStatus(ctx, true))
}

// backupBeforeUpdate writes a consistent copy of the database and the secret key.
func (s *Server) backupBeforeUpdate(tag string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		dir := filepath.Join(s.cfg.DataDir, "backups", "portdock")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		stamp := time.Now().UTC().Format("20060102-150405")
		dbPath := filepath.Join(dir, fmt.Sprintf("portdock-%s-before-%s-%s.db", Version, tag, stamp))
		if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dbPath); err != nil {
			return "", fmt.Errorf("database backup: %w", err)
		}
		if src, err := os.Open(filepath.Join(s.cfg.DataDir, "secret.key")); err == nil {
			defer src.Close()
			if dst, err := os.OpenFile(dbPath+".secret.key", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
				_, _ = io.Copy(dst, src)
				dst.Close()
			}
		}
		// Keep the ten most recent pre-update backups.
		if entries, err := filepath.Glob(filepath.Join(dir, "portdock-*-before-*.db")); err == nil && len(entries) > 10 {
			for _, old := range entries[:len(entries)-10] {
				_ = os.Remove(old)
				_ = os.Remove(old + ".secret.key")
			}
		}
		return dbPath, nil
	}
}

// startUpdate launches the update to tag. It refuses while deployments run
// (they would be interrupted) unless forced.
func (s *Server) startUpdate(ctx context.Context, tag string, force bool, by string) error {
	if _, ok := updater.ParseVersion(tag); !ok {
		return badRequest("invalid version %q", tag)
	}
	var active int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE status IN ('running', 'queued')").Scan(&active)
	if active > 0 && !force {
		return errStatus(409, "%d deployment(s) are running or queued; wait for them to finish (or force the update)", active)
	}
	c := s.updates
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errStatus(409, "an update is already in progress")
	}
	c.running = true
	c.mu.Unlock()
	plan := updater.Plan{DataDir: s.cfg.DataDir, From: Version, To: tag, Image: s.updateImage(ctx, tag),
		RegistryPass: s.box.MustDecrypt(s.db.Setting(ctx, "update_token")), Backup: s.backupBeforeUpdate(tag)}
	s.db.LogActivity(ctx, 0, "update.start", Version+" → "+tag+" ("+by+")")
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := updater.Launch(bg, updater.NewDocker(), plan); err != nil {
			slog.Warn("update failed", "err", err)
			s.notify.Send(notify.Event{Name: "update.failed", Title: "PortDock update to " + tag + " failed", Message: err.Error()})
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
		}
		// On success this process is stopped by the updater container.
	}()
	return nil
}

func (s *Server) runUpdate(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Version string `json:"version"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Version == "" {
		s.checkUpdate(r.Context(), false)
		s.updates.mu.Lock()
		if s.updates.latest != nil {
			in.Version = s.updates.latest.Tag
		}
		s.updates.mu.Unlock()
	}
	if in.Version == "" {
		return badRequest("no release available")
	}
	if err := s.startUpdate(r.Context(), in.Version, in.Force, "manual"); err != nil {
		return err
	}
	s.activity(r, "update.run", in.Version)
	return ok(w, s.updateStatus(r.Context(), true))
}

// UpdateWatch checks for releases periodically and installs stable ones
// automatically between 02:00 and 05:00 (instance time zone) when enabled.
func (s *Server) UpdateWatch(ctx context.Context) {
	// A finished update is reported once the new version is up.
	if st := updater.LoadState(s.cfg.DataDir); st.Status == "success" && st.To == Version && s.db.Setting(ctx, "update_reported") != st.ID {
		_ = s.db.SetSetting(ctx, "update_reported", st.ID)
		s.db.LogActivity(ctx, 0, "update.success", st.From+" → "+st.To)
		s.notify.Send(notify.Event{Name: "update.success", Title: "PortDock updated to " + st.To, Success: true})
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		updater.CleanupUpdaters(ctx, updater.NewDocker())
		s.checkUpdate(ctx, false)
		if s.db.Setting(ctx, "update_auto") == "true" {
			loc, err := time.LoadLocation(firstNonEmpty(s.db.Setting(ctx, "instance_timezone"), "UTC"))
			if err != nil {
				loc = time.UTC
			}
			h := time.Now().In(loc).Hour()
			s.updates.mu.Lock()
			latest := s.updates.latest
			s.updates.mu.Unlock()
			if h >= 2 && h < 5 && latest != nil && !latest.Prerelease && updater.Compare(Version, latest.Tag) < 0 {
				if err := s.startUpdate(ctx, latest.Tag, false, "automatic"); err != nil {
					slog.Info("automatic update postponed", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
