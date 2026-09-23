package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyStore persists known host keys (trust on first use).
type HostKeyStore interface {
	KnownHostKey(serverID int64) string
	SaveHostKey(serverID int64, key string)
}

type SSHTarget struct {
	ServerID   int64
	Host       string
	Port       int
	User       string
	PrivateKey string
}

type SSH struct {
	target SSHTarget
	store  HostKeyStore
	mu     sync.Mutex
	client *ssh.Client
}

func NewSSH(t SSHTarget, store HostKeyStore) *SSH { return &SSH{target: t, store: store} }

func (s *SSH) dial() (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(s.target.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            s.target.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:         15 * time.Second,
		HostKeyCallback: s.hostKeyCallback,
	}
	addr := net.JoinHostPort(s.target.Host, strconv.Itoa(s.target.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	go keepAlive(client)
	return client, nil
}

func (s *SSH) hostKeyCallback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if s.store == nil {
		return nil
	}
	known := s.store.KnownHostKey(s.target.ServerID)
	if known == "" {
		s.store.SaveHostKey(s.target.ServerID, got)
		return nil
	}
	if known != got {
		return fmt.Errorf("host key mismatch for %s (fingerprint %s); if the server was reinstalled, reset its host key in server settings", hostname, ssh.FingerprintSHA256(key))
	}
	return nil
}

func keepAlive(c *ssh.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		if _, _, err := c.SendRequest("keepalive@shipyard", true, nil); err != nil {
			_ = c.Close()
			return
		}
	}
}

func (s *SSH) get() (*ssh.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	c, err := s.dial()
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}

func (s *SSH) reset() {
	s.mu.Lock()
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
	s.mu.Unlock()
}

func (s *SSH) newSession() (*ssh.Session, error) {
	c, err := s.get()
	if err != nil {
		return nil, err
	}
	sess, err := c.NewSession()
	if err != nil {
		// Connection is probably dead; reconnect once.
		s.reset()
		if c, err = s.get(); err != nil {
			return nil, err
		}
		return c.NewSession()
	}
	return sess, nil
}

func envPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("export " + k + "=" + Quote(env[k]) + "; ")
	}
	return b.String()
}

func (s *SSH) Run(ctx context.Context, cmd string, opts RunOpts) error {
	sess, err := s.newSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = opts.Stdin
	sess.Stdout = opts.Stdout
	sess.Stderr = opts.Stderr
	if sess.Stderr == nil {
		sess.Stderr = opts.Stdout
	}
	if sess.Stdout == nil {
		sess.Stdout = io.Discard
		if sess.Stderr == nil {
			sess.Stderr = io.Discard
		}
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Signal(ssh.SIGKILL)
			_ = sess.Close()
		case <-done:
		}
	}()
	err = sess.Run(envPrefix(opts.Env) + cmd)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("exit status %d", exitErr.ExitStatus())
	}
	return err
}

type sshSession struct {
	sess  *ssh.Session
	stdin io.WriteCloser
	out   io.Reader
}

func (s *sshSession) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *sshSession) Write(p []byte) (int, error) { return s.stdin.Write(p) }
func (s *sshSession) Close() error                { return s.sess.Close() }
func (s *sshSession) Resize(rows, cols int) error { return s.sess.WindowChange(rows, cols) }
func (s *sshSession) Wait() error                 { return s.sess.Wait() }

func (s *SSH) PTY(ctx context.Context, cmd string, rows, cols int) (Session, error) {
	sess, err := s.newSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw
	if err := sess.Start(cmd); err != nil {
		sess.Close()
		return nil, err
	}
	go func() {
		_ = sess.Wait()
		_ = pw.Close()
	}()
	return &sshSession{sess: sess, stdin: stdin, out: pr}, nil
}

func (s *SSH) Close() error {
	s.reset()
	return nil
}

// GenerateKeyPair creates an Ed25519 key pair in OpenSSH format.
func GenerateKeyPair() (privPEM, pubAuthorized string, err error) {
	return generateEd25519()
}

// PublicKeyFromPrivate derives the authorized_keys line from a private key.
func PublicKeyFromPrivate(priv string) (string, error) {
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}
