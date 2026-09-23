// Package audit streams audit-log entries to external systems (SIEM): a signed
// JSON webhook and/or RFC 5424 syslog over UDP or TCP.
package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

// Entry is the exported form of an audit event.
type Entry struct {
	ID        int64  `json:"id"`
	Time      string `json:"time"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	UserID    int64  `json:"user_id"`
	UserEmail string `json:"user_email,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	IP        string `json:"ip,omitempty"`
	Instance  string `json:"instance,omitempty"`
}

// Config is read from settings.
type Config struct {
	WebhookURL    string
	WebhookSecret string
	Syslog        string // udp://host:514 or tcp://host:601
}

func (c Config) Enabled() bool { return c.WebhookURL != "" || c.Syslog != "" }

// Streamer forwards entries asynchronously; it never blocks the caller.
type Streamer struct {
	db     *store.DB
	box    *secrets.Box
	ch     chan store.Activity
	client *http.Client

	mu       sync.Mutex
	cfg      Config
	cfgAt    time.Time
	dropped  int
	lastErr  string
	lastSent time.Time
}

func New(db *store.DB, box *secrets.Box) *Streamer {
	return &Streamer{db: db, box: box, ch: make(chan store.Activity, 2000), client: &http.Client{Timeout: 10 * time.Second}}
}

// Push queues an entry (drops it if the buffer is full).
func (s *Streamer) Push(a store.Activity) {
	select {
	case s.ch <- a:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	}
}

// Invalidate reloads the configuration on the next entry.
func (s *Streamer) Invalidate() {
	s.mu.Lock()
	s.cfgAt = time.Time{}
	s.mu.Unlock()
}

// Status reports the last delivery error and drop count for the settings page.
func (s *Streamer) Status() (lastErr string, lastSent time.Time, dropped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr, s.lastSent, s.dropped
}

func (s *Streamer) config(ctx context.Context) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.cfgAt) < 30*time.Second {
		return s.cfg
	}
	s.cfg = Config{
		WebhookURL:    s.db.Setting(ctx, "audit_webhook_url"),
		WebhookSecret: s.box.MustDecrypt(s.db.Setting(ctx, "audit_webhook_secret")),
		Syslog:        s.db.Setting(ctx, "audit_syslog"),
	}
	s.cfgAt = time.Now()
	return s.cfg
}

func (s *Streamer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-s.ch:
			cfg := s.config(ctx)
			if !cfg.Enabled() {
				continue
			}
			err := s.Deliver(ctx, cfg, s.Enrich(ctx, a))
			s.mu.Lock()
			if err != nil {
				s.lastErr = time.Now().UTC().Format(time.RFC3339) + ": " + err.Error()
				slog.Warn("audit stream", "err", err)
			} else {
				s.lastErr, s.lastSent = "", time.Now()
			}
			s.mu.Unlock()
		}
	}
}

// Enrich adds user details to an activity row.
func (s *Streamer) Enrich(ctx context.Context, a store.Activity) Entry {
	e := Entry{ID: a.ID, Time: a.CreatedAt, Action: a.Action, Target: a.Target, UserID: a.UserID, IP: a.IP}
	if a.UserID != 0 {
		_ = s.db.QueryRowContext(ctx, "SELECT email, name FROM users WHERE id = ?", a.UserID).Scan(&e.UserEmail, &e.UserName)
	}
	e.Instance = s.db.Setting(ctx, "instance_name")
	return e
}

// Deliver sends one entry to every configured destination.
func (s *Streamer) Deliver(ctx context.Context, cfg Config, e Entry) error {
	var errs []string
	if cfg.WebhookURL != "" {
		if err := s.webhook(ctx, cfg, e); err != nil {
			errs = append(errs, "webhook: "+err.Error())
		}
	}
	if cfg.Syslog != "" {
		if err := sendSyslog(cfg.Syslog, e); err != nil {
			errs = append(errs, "syslog: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Sign returns the X-PortDock-Signature header value for body.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (s *Streamer) webhook(ctx context.Context, cfg Config, e Entry) error {
	body, _ := json.Marshal(e)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PortDock-Audit/1")
	if cfg.WebhookSecret != "" {
		req.Header.Set("X-PortDock-Signature", Sign(cfg.WebhookSecret, body))
		req.Header.Set("X-Shipyard-Signature", Sign(cfg.WebhookSecret, body)) // pre-rename receivers
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

// FormatSyslog renders an RFC 5424 message (facility 13 "log audit", severity 6 "informational").
func FormatSyslog(e Entry, hostname string) string {
	body, _ := json.Marshal(e)
	ts := e.Time
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("<110>1 %s %s portdock - %s - %s", ts, hostname, sanitizeMsgID(e.Action), body)
}

func sanitizeMsgID(s string) string {
	if s == "" {
		return "-"
	}
	var b strings.Builder
	for _, r := range s {
		if r > 32 && r < 127 && b.Len() < 32 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseSyslogTarget validates udp://host:port or tcp://host:port.
func ParseSyslogTarget(raw string) (network, addr string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "udp" && u.Scheme != "tcp") || u.Host == "" {
		return "", "", fmt.Errorf("syslog target must look like udp://siem.example.com:514 or tcp://siem.example.com:601")
	}
	host := u.Host
	if u.Port() == "" {
		port := "514"
		if u.Scheme == "tcp" {
			port = "601"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	return u.Scheme, host, nil
}

func sendSyslog(target string, e Entry) error {
	network, addr, err := ParseSyslogTarget(target)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	msg := FormatSyslog(e, host)
	conn, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if network == "tcp" {
		msg = fmt.Sprintf("%d %s", len(msg), msg) // RFC 6587 octet counting
	}
	_, err = conn.Write([]byte(msg))
	return err
}
