// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	// Addr is the HTTP listen address, e.g. ":8000".
	Addr string
	// DataDir holds the SQLite database, secret key and local build/backup data.
	// When Shipyard runs in Docker this directory must be bind-mounted at the
	// same path on the host so that `docker run -v` paths resolve correctly.
	DataDir string
	// PublicURL is the external URL of this Shipyard instance (used for webhook URLs).
	PublicURL string
	// SecretKey overrides the generated encryption key (base64, 32 bytes).
	SecretKey string
	// DevMode enables permissive CORS for the Vite dev server.
	DevMode bool
	// ProxyNetwork is the Docker network shared by Traefik and all managed workloads.
	ProxyNetwork string
	// MaxConcurrentBuilds limits parallel deployments across all servers.
	MaxConcurrentBuilds int
}

func Load() Config {
	c := Config{
		Addr:                env("SHIPYARD_ADDR", ":8000"),
		DataDir:             env("SHIPYARD_DATA_DIR", "/data/shipyard"),
		PublicURL:           env("SHIPYARD_PUBLIC_URL", "http://localhost:8000"),
		SecretKey:           os.Getenv("SHIPYARD_SECRET_KEY"),
		DevMode:             os.Getenv("SHIPYARD_DEV") == "1",
		ProxyNetwork:        env("SHIPYARD_NETWORK", "shipyard"),
		MaxConcurrentBuilds: envInt("SHIPYARD_MAX_BUILDS", 4),
	}
	c.DataDir = filepath.ToSlash(c.DataDir)
	return c
}

func (c Config) DBPath() string { return filepath.Join(c.DataDir, "shipyard.db") }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}
