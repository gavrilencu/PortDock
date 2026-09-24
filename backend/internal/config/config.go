// Package config loads runtime configuration from environment variables.
//
// Variables are named PORTDOCK_*; the SHIPYARD_* names used before the
// product was renamed are still honoured so existing installations keep working.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	// Addr is the HTTP listen address, e.g. ":8000".
	Addr string
	// DataDir holds the SQLite database, secret key and local build/backup data.
	// When PortDock runs in Docker this directory must be bind-mounted at the
	// same path on the host so that `docker run -v` paths resolve correctly.
	DataDir string
	// PublicURL is the external URL of this PortDock instance (used for webhook URLs).
	PublicURL string
	// SecretKey overrides the generated encryption key (base64, 32 bytes).
	SecretKey string
	// DevMode enables permissive CORS for the Vite dev server.
	DevMode bool
	// TrustedProxies lists the proxies whose X-Forwarded-For is believed
	// (comma-separated IPs or CIDR ranges, "none", or "" for private ranges).
	TrustedProxies string
	// ProxyNetwork is the Docker network shared by Traefik and all managed workloads.
	ProxyNetwork string
	// NetworkFromEnv reports whether ProxyNetwork was set explicitly.
	NetworkFromEnv bool
	// MaxConcurrentBuilds limits parallel deployments across all servers.
	MaxConcurrentBuilds int
}

const (
	DefaultDataDir = "/data/portdock"
	LegacyDataDir  = "/data/shipyard"
	// LegacyNetwork is the proxy network of installations created before the rename.
	LegacyNetwork = "shipyard"
)

func Load() Config {
	c := Config{
		Addr:                Env("ADDR", ":8000"),
		DataDir:             Env("DATA_DIR", ""),
		PublicURL:           Env("PUBLIC_URL", "http://localhost:8000"),
		SecretKey:           Env("SECRET_KEY", ""),
		DevMode:             Env("DEV", "") == "1",
		TrustedProxies:      Env("TRUSTED_PROXIES", ""),
		ProxyNetwork:        Env("NETWORK", ""),
		MaxConcurrentBuilds: envInt("MAX_BUILDS", 4),
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir
		// Installations from before the rename keep their data where it is.
		if !exists(DefaultDataDir) && exists(LegacyDataDir) {
			c.DataDir = LegacyDataDir
		}
	}
	c.NetworkFromEnv = c.ProxyNetwork != ""
	if c.ProxyNetwork == "" {
		c.ProxyNetwork = "portdock"
	}
	c.DataDir = filepath.ToSlash(c.DataDir)
	return c
}

func (c Config) DBPath() string { return filepath.Join(c.DataDir, "portdock.db") }

// MigrateLegacyFiles renames the database of a pre-rename installation
// (shipyard.db and its WAL/SHM files) before it is opened.
func (c Config) MigrateLegacyFiles() error {
	newDB := c.DBPath()
	oldDB := filepath.Join(c.DataDir, "shipyard.db")
	if exists(newDB) || !exists(oldDB) {
		return nil
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if exists(oldDB + suffix) {
			if err := os.Rename(oldDB+suffix, newDB+suffix); err != nil {
				return err
			}
		}
	}
	return nil
}

// Env reads PORTDOCK_<name>, falling back to the legacy SHIPYARD_<name>.
func Env(name, def string) string {
	for _, prefix := range []string{"PORTDOCK_", "SHIPYARD_"} {
		if v := os.Getenv(prefix + name); v != "" {
			return v
		}
	}
	return def
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(Env(name, "")); err == nil && v > 0 {
		return v
	}
	return def
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// IsLegacyName reports identifiers from before the rename (used in messages).
func IsLegacyName(s string) bool { return strings.Contains(strings.ToLower(s), "shipyard") }
