// Command portdock runs the PortDock self-hosted PaaS: API, web UI, deployment
// workers, status monitor and backup scheduler in a single binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // timezones on minimal images and Windows

	"github.com/portdock/portdock/internal/api"
	"github.com/portdock/portdock/internal/audit"
	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/deploy"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/github"
	"github.com/portdock/portdock/internal/monitor"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/scheduler"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/store"
	"github.com/portdock/portdock/internal/uptime"
	"github.com/portdock/portdock/internal/stream"
	"github.com/portdock/portdock/internal/updater"
	"github.com/portdock/portdock/web"
)

func main() {
	resetPassword := flag.String("reset-password", "", "reset the password of the user with this email and exit")
	disable2FA := flag.String("disable-2fa", "", "turn off two-factor authentication for the user with this email and exit")
	selfUpdate := flag.String("self-update", "", "internal: replace the given PortDock container with this image (run by the updater container)")
	flag.Parse()
	if *selfUpdate != "" {
		dataDir := config.Load().DataDir
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := updater.Swap(ctx, updater.NewDocker(), *selfUpdate, dataDir); err != nil {
			fmt.Fprintln(os.Stderr, "update failed:", err)
			os.Exit(1)
		}
		return
	}

	level := slog.LevelInfo
	if config.Env("DEBUG", "") == "1" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if err := run(*resetPassword, *disable2FA); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(resetPassword, disable2FA string) error {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}
	if err := cfg.MigrateLegacyFiles(); err != nil {
		return fmt.Errorf("migrate database file: %w", err)
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	migrateInstallation(db, &cfg)

	if resetPassword != "" {
		return doResetPassword(db, resetPassword)
	}
	if disable2FA != "" {
		res, err := db.ExecContext(context.Background(),
			"UPDATE users SET totp_enabled = 0, totp_secret = '', recovery_codes = '', totp_last_step = 0 WHERE email = ?", strings.ToLower(disable2FA))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("user %s not found", disable2FA)
		}
		db.LogActivity(context.Background(), 0, "2fa.reset", disable2FA+" (command line)")
		fmt.Printf("Two-factor authentication disabled for %s\n", disable2FA)
		return nil
	}

	box, err := secrets.LoadOrCreate(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := stream.NewHub()
	pool := executor.NewPool(db, box)
	notifier := notify.New(db, box)
	gh := github.New(db, box)
	engine := deploy.New(cfg, db, box, pool, hub, notifier, gh)
	sched := scheduler.New(db, engine, pool, notifier)

	engine.Start(ctx)
	sched.Start(ctx)
	mon := monitor.New(db, pool, notifier, hub)
	go mon.Run(ctx)
	auditStream := audit.New(db, box)
	store.OnActivity = auditStream.Push
	go auditStream.Run(ctx)
	engine.RecoverBackups(ctx) // jobs interrupted by the previous run
	engine.RecoverScans(ctx)
	netcfg.Install() // PortDock's own requests follow the corporate proxy setting
	engine.ReloadNetwork(ctx)
	go engine.Monitor(ctx, 30*time.Second)
	go engine.CertificateWatch(ctx, 6*time.Hour)
	go engine.VersionRetention(ctx)
	checker := uptime.New(db, notifier, engine.PublicURL)
	go checker.Run(ctx)
	go func() {
		if err := engine.ApplyDashboardRoute(ctx); err != nil && !errors.Is(err, deploy.ErrNotContainerized) {
			slog.Warn("dashboard domain routing", "err", err)
		}
	}()

	srv := api.New(api.Deps{
		Config: cfg, DB: db, Box: box, Pool: pool, Hub: hub, Engine: engine, Scheduler: sched, Notifier: notifier, GitHub: gh, Monitor: mon, Audit: auditStream, Uptime: checker,
		Auth: auth.New(db, strings.HasPrefix(cfg.PublicURL, "https://")),
		UI:   web.FS(),
	})
	go srv.UpdateWatch(ctx)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	slog.Info("PortDock started", "addr", cfg.Addr, "data", cfg.DataDir, "url", cfg.PublicURL, "version", api.Version)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("PortDock stopped")
	return nil
}

func doResetPassword(db *store.DB, email string) error {
	ctx := context.Background()
	u, err := store.Get[store.User](ctx, db, "SELECT * FROM users WHERE email = ?", strings.ToLower(email))
	if err != nil {
		return fmt.Errorf("user %s not found", email)
	}
	pass := secrets.RandomPassword(16)
	hash, err := auth.HashPassword(pass)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", hash, u.ID); err != nil {
		return err
	}
	_, _ = db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID)
	fmt.Printf("New password for %s: %s\n", u.Email, pass)
	return nil
}

// migrateInstallation keeps installations created before the rename working:
// the proxy network name is kept, and stored paths follow a moved data directory.
func migrateInstallation(db *store.DB, cfg *config.Config) {
	ctx := context.Background()
	if !cfg.NetworkFromEnv {
		net := db.Setting(ctx, "proxy_network")
		if net == "" {
			net = cfg.ProxyNetwork
			if _, err := executor.Output(ctx, executor.Local{}, "docker network inspect "+config.LegacyNetwork+" >/dev/null 2>&1"); err == nil {
				net = config.LegacyNetwork // existing workloads are attached to it
			}
			_ = db.SetSetting(ctx, "proxy_network", net)
		}
		cfg.ProxyNetwork = net
	}
	if prev := db.Setting(ctx, "data_dir"); prev != "" && prev != cfg.DataDir {
		_, _ = db.ExecContext(ctx, "UPDATE backup_runs SET file = replace(file, ?, ?) WHERE file LIKE ?", prev+"/", cfg.DataDir+"/", prev+"/%")
		db.LogActivity(ctx, 0, "system.data_dir_moved", prev+" → "+cfg.DataDir)
	}
	_ = db.SetSetting(ctx, "data_dir", cfg.DataDir)
}
