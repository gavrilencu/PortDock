package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The agent runs in a container of PortDock's image on each server
// (portdock wg-agent), in the server's network namespace with NET_ADMIN. It
// creates pd0 in the kernel, applies the configuration PortDock writes next
// to it, masquerades the containers' traffic to the private network, and
// starts the containers bound to the private address that could not start
// before pd0 existed (after a reboot). Stopped, it removes pd0.

// Agent files, in the directory mounted at AgentDir.
const (
	AgentDir    = "/etc/portdock-wg"
	ConfigFile  = "pd0.conf"
	AddressFile = "address" // e.g. 10.88.0.2/24
	// LabelBind marks the containers that publish a port on the private address.
	LabelBind = "portdock.overlay-bind"
)

// RunAgent keeps pd0 configured until ctx ends.
func RunAgent(ctx context.Context, dir string) error {
	applied := ""
	nat := ""
	defer func() {
		cleanup := context.Background()
		if nat != "" {
			_ = run(cleanup, "iptables", "-t", "nat", "-D", "POSTROUTING", "-o", Interface, "!", "-s", nat, "-j", "MASQUERADE")
		}
		_ = run(cleanup, "ip", "link", "del", Interface)
		slog.Info("overlay agent stopped: pd0 removed")
	}()
	for {
		conf, err1 := os.ReadFile(filepath.Join(dir, ConfigFile))
		addr, err2 := os.ReadFile(filepath.Join(dir, AddressFile))
		if err1 == nil && err2 == nil {
			address := strings.TrimSpace(string(addr))
			sum := fmt.Sprintf("%x", sha256.Sum256(append(conf, addr...)))
			if sum != applied || !linkExists(ctx) {
				if err := apply(ctx, dir, address, linkExists(ctx)); err != nil {
					slog.Error("overlay agent", "err", err)
				} else {
					if applied == "" {
						startWaiting(ctx)
					}
					applied = sum
					slog.Info("overlay agent: pd0 configured", "address", address)
				}
			}
			if subnet := subnetOf(address); subnet != "" && subnet != nat {
				if nat != "" {
					_ = run(ctx, "iptables", "-t", "nat", "-D", "POSTROUTING", "-o", Interface, "!", "-s", nat, "-j", "MASQUERADE")
				}
				// Containers leave through pd0 with the server's private address.
				if run(ctx, "iptables", "-t", "nat", "-C", "POSTROUTING", "-o", Interface, "!", "-s", subnet, "-j", "MASQUERADE") != nil {
					if err := run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", Interface, "!", "-s", subnet, "-j", "MASQUERADE"); err != nil {
						slog.Error("overlay agent: masquerade", "err", err)
					}
				}
				nat = subnet
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

func linkExists(ctx context.Context) bool {
	return run(ctx, "ip", "link", "show", Interface) == nil
}

// apply creates pd0 if needed and loads the configuration.
func apply(ctx context.Context, dir, address string, exists bool) error {
	conf := filepath.Join(dir, ConfigFile)
	if !exists {
		if err := run(ctx, "ip", "link", "add", Interface, "type", "wireguard"); err != nil {
			return fmt.Errorf("create %s (the kernel needs WireGuard: Linux 5.6+ or the wireguard module): %w", Interface, err)
		}
		if err := run(ctx, "wg", "setconf", Interface, conf); err != nil {
			return err
		}
	} else if err := run(ctx, "wg", "syncconf", Interface, conf); err != nil {
		return err
	}
	if err := run(ctx, "ip", "address", "replace", address, "dev", Interface); err != nil {
		return err
	}
	return run(ctx, "ip", "link", "set", Interface, "mtu", "1420", "up")
}

// startWaiting brings up the containers bound to the private address once
// pd0 exists: after a reboot Docker either could not start them, or started
// them without their port (the address did not exist yet).
func startWaiting(ctx context.Context) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "--filter", "label="+LabelBind+"=true").Output()
	if err != nil {
		return
	}
	for _, id := range strings.Fields(string(out)) {
		state, _ := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", id).Output()
		switch {
		case strings.TrimSpace(string(state)) != "true":
			if err := run(ctx, "docker", "start", id); err == nil {
				slog.Info("overlay agent: started a container bound to the private address", "container", id)
			}
		default:
			ports, _ := exec.CommandContext(ctx, "docker", "port", id).Output()
			if strings.TrimSpace(string(ports)) == "" {
				if err := run(ctx, "docker", "restart", "-t", "10", id); err == nil {
					slog.Info("overlay agent: restarted a container that missed its private port", "container", id)
				}
			}
		}
	}
}

// subnetOf turns 10.88.0.2/24 into 10.88.0.0/24.
func subnetOf(address string) string {
	p, err := netip.ParsePrefix(address)
	if err != nil {
		return ""
	}
	return p.Masked().String()
}

func run(ctx context.Context, name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
