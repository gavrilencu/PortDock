package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
)

func (s *Server) userCount(r *http.Request) int {
	var n int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&n)
	return n
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) error {
	// repo is the GitHub repository of this build (shown on the About page).
	res := map[string]any{"setup_required": s.userCount(r) == 0, "version": Version, "repo": s.updateRepo(r.Context())}
	if u, sess, err := s.auth.AuthenticateSession(r); err == nil {
		res["user"] = toUserOut(u)
		res["mfa_setup_required"] = s.needsMFASetup(r.Context(), u, sess)
	}
	return ok(w, res)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func validateCredentials(c *credentials) error {
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if _, err := mail.ParseAddress(c.Email); err != nil {
		return badRequest("invalid email address")
	}
	if len(c.Password) < 8 {
		return badRequest("password must be at least 8 characters")
	}
	return nil
}

// register creates the first (owner) account. Later accounts are created by admins.
func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	if s.userCount(r) > 0 {
		return errStatus(403, "registration is closed; ask an administrator to invite you")
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	if err := validateCredentials(&c); err != nil {
		return err
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		return err
	}
	u := store.User{Email: c.Email, Name: strings.TrimSpace(c.Name), PasswordHash: hash, Role: "owner", CreatedAt: store.Now()}
	if u.Name == "" {
		u.Name = strings.Split(c.Email, "@")[0]
	}
	if _, err := store.Insert(r.Context(), s.db, "users", &u); err != nil {
		return err
	}
	if err := s.ensureLocalServer(r); err != nil {
		return err
	}
	if err := s.auth.CreateSession(r.Context(), w, r, u.ID, "password"); err != nil {
		return err
	}
	return ok(w, toUserOut(u))
}

// ensureLocalServer registers the machine running PortDock as the default server.
func (s *Server) ensureLocalServer(r *http.Request) error {
	var n int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM servers WHERE is_local = 1").Scan(&n)
	if n > 0 {
		return nil
	}
	srv := store.Server{Name: "localhost", Description: "The server running PortDock", Host: "localhost", Port: 22,
		User: "root", IsLocal: true, Status: "unknown", ProxyEnabled: true, ProxyStatus: "unknown", CreatedAt: store.Now()}
	if _, err := store.Insert(r.Context(), s.db, "servers", &srv); err != nil {
		return err
	}
	go func() { _, _ = s.engine.ValidateServer(context.Background(), srv) }()
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	s.auth.DestroySession(r.Context(), w, r)
	return ok(w, map[string]bool{"ok": true})
}

// supportedLocales are the interface languages; "" means the default (English).
var supportedLocales = map[string]bool{"": true, "en": true, "ro": true, "ru": true}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	return ok(w, toUserOut(auth.UserFrom(r.Context())))
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	var in struct {
		Name            *string `json:"name"`
		Email           *string `json:"email"`
		CurrentPassword string  `json:"current_password"`
		NewPassword     string  `json:"new_password"`
		Locale          *string `json:"locale"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Locale != nil {
		if !supportedLocales[*in.Locale] {
			return badRequest("unsupported language")
		}
		u.Locale = *in.Locale
	}
	if in.Name != nil {
		u.Name = strings.TrimSpace(*in.Name)
	}
	if in.Email != nil {
		c := credentials{Email: *in.Email, Password: "placeholder"}
		if err := validateCredentials(&c); err != nil {
			return err
		}
		u.Email = c.Email
	}
	passwordChanged := in.NewPassword != ""
	if passwordChanged {
		if u.HasPassword() && !auth.CheckPassword(u.PasswordHash, in.CurrentPassword) {
			return badRequest("current password is incorrect")
		}
		if len(in.NewPassword) < 8 {
			return badRequest("password must be at least 8 characters")
		}
		hash, err := auth.HashPassword(in.NewPassword)
		if err != nil {
			return err
		}
		u.PasswordHash = hash
	}
	if err := store.Update(r.Context(), s.db, "users", &u); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a user with this email already exists")
		}
		return err
	}
	if passwordChanged {
		_ = s.auth.RevokeSessions(r.Context(), u.ID, auth.SessionFrom(r.Context()).TokenHash)
		s.activity(r, "password.change", u.Email)
	}
	return ok(w, toUserOut(u))
}

// ---- users (admin) ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := store.Select[store.User](r.Context(), s.db, "SELECT * FROM users ORDER BY id")
	if err != nil {
		return err
	}
	out := make([]userOut, 0, len(users))
	for _, u := range users {
		out = append(out, toUserOut(u))
	}
	return ok(w, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		credentials
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := validateCredentials(&in.credentials); err != nil {
		return err
	}
	if in.Role != "admin" {
		in.Role = "member"
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	u := store.User{Email: in.Email, Name: in.Name, PasswordHash: hash, Role: in.Role, CreatedAt: store.Now()}
	if _, err := store.Insert(r.Context(), s.db, "users", &u); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return badRequest("a user with this email already exists")
		}
		return err
	}
	s.activity(r, "user.create", u.Email)
	return ok(w, toUserOut(u))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u, err := store.ByID[store.User](r.Context(), s.db, "users", id)
	if err != nil {
		return err
	}
	var in struct {
		Name     *string `json:"name"`
		Role     *string `json:"role"`
		Password string  `json:"password"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Name != nil {
		u.Name = *in.Name
	}
	if in.Role != nil && u.Role != "owner" {
		if *in.Role != "admin" && *in.Role != "member" {
			return badRequest("role must be admin or member")
		}
		u.Role = *in.Role
	}
	if in.Password != "" {
		if len(in.Password) < 8 {
			return badRequest("password must be at least 8 characters")
		}
		if u.PasswordHash, err = auth.HashPassword(in.Password); err != nil {
			return err
		}
	}
	revoke := in.Password != ""
	if in.Disabled != nil && *in.Disabled != u.Disabled {
		if u.Role == "owner" || u.ID == auth.UserFrom(r.Context()).ID {
			return badRequest("you cannot disable this account")
		}
		u.Disabled = *in.Disabled
		revoke = revoke || u.Disabled
		action := "user.enable"
		if u.Disabled {
			action = "user.disable"
		}
		s.activity(r, action, u.Email)
	}
	if err := store.Update(r.Context(), s.db, "users", &u); err != nil {
		return err
	}
	if revoke {
		_ = s.auth.RevokeSessions(r.Context(), u.ID, "")
	}
	return ok(w, toUserOut(u))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u, err := store.ByID[store.User](r.Context(), s.db, "users", id)
	if err != nil {
		return err
	}
	if u.Role == "owner" {
		return badRequest("the owner account cannot be deleted")
	}
	if err := store.Delete(r.Context(), s.db, "users", id); err != nil {
		return err
	}
	s.activity(r, "user.delete", u.Email)
	return ok(w, map[string]bool{"ok": true})
}

// ---- API tokens ----

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) error {
	ts, err := store.Select[store.APIToken](r.Context(), s.db, "SELECT * FROM api_tokens WHERE user_id = ? ORDER BY id DESC", auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	return ok(w, ts)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Name) == "" {
		return badRequest("name is required")
	}
	plain, t, err := s.auth.CreateToken(r.Context(), auth.UserFrom(r.Context()).ID, in.Name)
	if err != nil {
		return err
	}
	s.activity(r, "token.create", in.Name)
	return ok(w, map[string]any{"token": plain, "api_token": t})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(r.Context(), "DELETE FROM api_tokens WHERE id = ? AND user_id = ?", id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

// ---- instance settings ----

var settingKeys = []string{
	"instance_name", "instance_url", "instance_https", "instance_timezone", "public_ipv4", "public_ipv6",
	"acme_email", "auto_cleanup", "proxy_dashboard", "require_2fa", "password_login",
	"s3_endpoint", "s3_bucket", "s3_region", "s3_access_key", "s3_secret_key",
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	res := map[string]any{}
	for _, k := range settingKeys {
		v := s.db.Setting(r.Context(), k)
		if k == "s3_secret_key" {
			res["s3_secret_key_set"] = v != ""
			continue
		}
		res[k] = v
	}
	res["public_url"] = s.publicURL(r.Context())
	res["env_public_url"] = s.cfg.PublicURL
	res["instance_route_error"] = s.db.Setting(r.Context(), "instance_route_error")
	res["version"] = Version
	return ok(w, res)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var in map[string]string
	if err := readJSON(r, &in); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, k := range settingKeys {
		allowed[k] = true
	}
	if err := validateInstanceSettings(in); err != nil {
		return err
	}
	prevURL, prevHTTPS, prevTZ := s.db.Setting(r.Context(), "instance_url"), s.db.Setting(r.Context(), "instance_https"), s.db.Setting(r.Context(), "instance_timezone")
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		if k == "s3_secret_key" {
			if v == "" {
				continue
			}
			v = s.box.Encrypt(v)
		}
		if err := s.db.SetSetting(r.Context(), k, strings.TrimSpace(v)); err != nil {
			return err
		}
	}
	s.activity(r, "settings.update", "")
	if s.db.Setting(r.Context(), "instance_timezone") != prevTZ {
		s.sched.Reload(r.Context())
	}
	if s.db.Setting(r.Context(), "instance_url") != prevURL || s.db.Setting(r.Context(), "instance_https") != prevHTTPS {
		if err := s.engine.ApplyDashboardRoute(r.Context()); err != nil {
			w.Header().Set("X-PortDock-Warning", err.Error())
			_ = s.db.SetSetting(r.Context(), "instance_route_error", err.Error())
		} else {
			_ = s.db.SetSetting(r.Context(), "instance_route_error", "")
		}
	}
	return s.getSettings(w, r)
}

func validateInstanceSettings(in map[string]string) error {
	if v, ok := in["instance_url"]; ok && strings.TrimSpace(v) != "" {
		u, err := url.Parse(strings.TrimSpace(v))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
			return badRequest("instance URL must look like https://panel.example.com")
		}
		in["instance_url"] = u.Scheme + "://" + u.Host
	}
	if v, ok := in["require_2fa"]; ok && v != "" && v != "off" && v != "admins" && v != "all" {
		return badRequest("require_2fa must be off, admins or all")
	}
	if v, ok := in["password_login"]; ok && v != "" && v != "true" && v != "false" {
		return badRequest("password_login must be true or false")
	}
	if v, ok := in["instance_timezone"]; ok && v != "" {
		if _, err := time.LoadLocation(v); err != nil {
			return badRequest("unknown timezone %q", v)
		}
	}
	for _, k := range []string{"public_ipv4", "public_ipv6"} {
		if v, ok := in[k]; ok && strings.TrimSpace(v) != "" {
			ip := net.ParseIP(strings.TrimSpace(v))
			if ip == nil || (k == "public_ipv4") != (ip.To4() != nil) {
				return badRequest("%s is not a valid %s address", v, map[string]string{"public_ipv4": "IPv4", "public_ipv6": "IPv6"}[k])
			}
		}
	}
	return nil
}

// ---- notifications ----

type notificationDTO struct {
	store.NotificationChannel
	Config map[string]string `json:"config"`
}

func (s *Server) notificationOut(c store.NotificationChannel) notificationDTO {
	cfg := map[string]string{}
	_ = json.Unmarshal([]byte(s.box.MustDecrypt(c.Config)), &cfg)
	for k := range cfg {
		if strings.Contains(k, "token") || strings.Contains(k, "password") || strings.Contains(k, "secret") {
			if cfg[k] != "" {
				cfg[k] = "********"
			}
		}
	}
	return notificationDTO{NotificationChannel: c, Config: cfg}
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) error {
	cs, err := store.Select[store.NotificationChannel](r.Context(), s.db, "SELECT * FROM notification_channels ORDER BY id")
	if err != nil {
		return err
	}
	out := make([]notificationDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.notificationOut(c))
	}
	return ok(w, out)
}

type notificationIn struct {
	Name    *string           `json:"name"`
	Type    *string           `json:"type"`
	Events  *string           `json:"events"`
	Enabled *bool             `json:"enabled"`
	Config  map[string]string `json:"config"`
}

func (s *Server) applyNotification(c *store.NotificationChannel, in notificationIn) error {
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.Type != nil {
		switch *in.Type {
		case "discord", "slack", "teams", "telegram", "webhook", "email":
			c.Type = *in.Type
		default:
			return badRequest("unsupported type")
		}
	}
	if in.Events != nil {
		c.Events = *in.Events
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Config != nil {
		prev := map[string]string{}
		_ = json.Unmarshal([]byte(s.box.MustDecrypt(c.Config)), &prev)
		for k, v := range in.Config {
			if v == "********" {
				continue // unchanged masked secret
			}
			prev[k] = v
		}
		b, _ := json.Marshal(prev)
		c.Config = s.box.Encrypt(string(b))
	}
	if c.Name == "" || c.Type == "" {
		return badRequest("name and type are required")
	}
	return nil
}

func (s *Server) createNotification(w http.ResponseWriter, r *http.Request) error {
	var in notificationIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	c := store.NotificationChannel{Enabled: true, CreatedAt: store.Now(),
		Events: "deployment.failed,deployment.success,backup.failed,server.unreachable,server.reachable,alert.firing,alert.resolved,deployment.approval_required,certificate.expiring,certificate.failed,certificate.renewed,update.available,update.success,update.failed"}
	if err := s.applyNotification(&c, in); err != nil {
		return err
	}
	if _, err := store.Insert(r.Context(), s.db, "notification_channels", &c); err != nil {
		return err
	}
	return ok(w, s.notificationOut(c))
}

func (s *Server) updateNotification(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.NotificationChannel](r.Context(), s.db, "notification_channels", id)
	if err != nil {
		return err
	}
	var in notificationIn
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.applyNotification(&c, in); err != nil {
		return err
	}
	if err := store.Update(r.Context(), s.db, "notification_channels", &c); err != nil {
		return err
	}
	return ok(w, s.notificationOut(c))
}

func (s *Server) deleteNotification(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "notification_channels", id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := store.ByID[store.NotificationChannel](r.Context(), s.db, "notification_channels", id)
	if err != nil {
		return err
	}
	if err := s.notify.SendTo(r.Context(), c, notify.Event{
		Name: "test", Title: "PortDock test notification", Message: "If you can read this, notifications work! 🎉", Success: true,
	}); err != nil {
		return badRequest("sending failed: %v", err)
	}
	return ok(w, map[string]bool{"ok": true})
}

// ---- dashboard / activity ----

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	count := func(q string) int {
		var n int
		_ = s.db.QueryRowContext(ctx, q).Scan(&n)
		return n
	}
	servers, err := store.Select[store.Server](ctx, s.db, "SELECT * FROM servers ORDER BY id")
	if err != nil {
		return err
	}
	type recent struct {
		store.Deployment
		ApplicationName string `db:"application_name" json:"application_name"`
		EnvironmentID   int64  `db:"environment_id" json:"environment_id"`
	}
	deps, err := store.Select[recent](ctx, s.db, `SELECT d.id, d.uuid, d.application_id, d.status, d.trigger, d.commit_sha, d.commit_message,
		d.branch, d.pr_number, d.image, d.started_at, d.finished_at, d.created_at, a.name AS application_name, a.environment_id AS environment_id
		FROM deployments d JOIN applications a ON a.id = d.application_id ORDER BY d.id DESC LIMIT 200`)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	if u := auth.UserFrom(ctx); !u.IsAdmin() {
		// Members: everything is counted over the projects they can see.
		visible := s.envVisibility(ctx)
		deps = filterBy(deps, func(d recent) bool { return visible(d.EnvironmentID) })
		type res struct {
			Env    int64  `db:"environment_id"`
			Status string `db:"status"`
		}
		for _, t := range []string{"applications", "databases", "services"} {
			rows, _ := store.Select[res](ctx, s.db, "SELECT environment_id, status FROM "+t)
			for _, x := range rows {
				if visible(x.Env) {
					counts[t]++
					if x.Status == "running" {
						counts["running"]++
					}
				}
			}
		}
		counts["projects"] = len(s.visibleProjectIDs(ctx))
		today := time.Now().UTC().Format("2006-01-02")
		for _, d := range deps {
			if d.CreatedAt >= today {
				counts["deployments_today"]++
				if d.Status == "failed" {
					counts["failed_today"]++
				}
			}
		}
	}
	if len(deps) > 10 {
		deps = deps[:10]
	}
	all := map[string]int{
		"projects":          count("SELECT COUNT(*) FROM projects"),
		"applications":      count("SELECT COUNT(*) FROM applications"),
		"databases":         count("SELECT COUNT(*) FROM databases"),
		"services":          count("SELECT COUNT(*) FROM services"),
		"running":           count("SELECT (SELECT COUNT(*) FROM applications WHERE status = 'running') + (SELECT COUNT(*) FROM databases WHERE status = 'running') + (SELECT COUNT(*) FROM services WHERE status = 'running')"),
		"deployments_today": count("SELECT COUNT(*) FROM deployments WHERE created_at >= date('now')"),
		"failed_today":      count("SELECT COUNT(*) FROM deployments WHERE status = 'failed' AND created_at >= date('now')"),
	}
	if !auth.UserFrom(ctx).IsAdmin() {
		for _, k := range []string{"projects", "applications", "databases", "services", "running", "deployments_today", "failed_today"} {
			all[k] = counts[k]
		}
	}
	all["servers"] = len(servers)
	return ok(w, map[string]any{
		"counts":             all,
		"servers":            servers,
		"recent_deployments": deps,
	})
}
