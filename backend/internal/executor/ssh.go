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

// maxSessions bounds the sessions open at once on one connection: OpenSSH
// refuses further channels above its MaxSessions (10 by default), and the
// monitor, topology, log followers and a build easily reach that together.
const maxSessions = 8

// SSH runs commands on one server over a single multiplexed connection that
// is dialed on demand and re-dialed only when the transport itself is gone.
type SSH struct {
	target SSHTarget
	store  HostKeyStore
	mu     sync.Mutex
	client *ssh.Client
	sem    chan struct{}
	dials  int // for tests
}

func NewSSH(t SSHTarget, store HostKeyStore) *SSH {
	return &SSH{target: t, store: store, sem: make(chan struct{}, maxSessions)}
}

func (s *SSH) dial(ctx context.Context) (*ssh.Client, error) {
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
	// TCP keepalives notice a server that vanished without closing the connection.
	d := net.Dialer{Timeout: cfg.Timeout, KeepAlive: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := ssh.NewClient(c, chans, reqs)
	s.dials++
	go s.keepAlive(client)
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

// keepAlive pings the server every 30 s; a ping that fails or gets no answer
// within 20 s (a half-open connection) drops the connection so that the next
// command dials again instead of hanging.
func (s *SSH) keepAlive(c *ssh.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		done := make(chan error, 1)
		go func() {
			_, _, err := c.SendRequest("keepalive@portdock", true, nil)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				continue
			}
		case <-time.After(20 * time.Second):
		}
		s.forget(c)
		return
	}
}

func (s *SSH) get(ctx context.Context) (*ssh.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	c, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}

// forget closes a connection that turned out dead. Only the connection that
// failed is dropped: a replacement dialed meanwhile stays.
func (s *SSH) forget(c *ssh.Client) {
	s.mu.Lock()
	if s.client == c {
		s.client = nil
	}
	s.mu.Unlock()
	_ = c.Close()
}

// openSession opens a channel, giving up when the context ends (the library
// itself has no timeout for a channel open).
func openSession(ctx context.Context, c *ssh.Client) (*ssh.Session, error) {
	type result struct {
		sess *ssh.Session
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		sess, err := c.NewSession()
		ch <- result{sess, err}
	}()
	select {
	case r := <-ch:
		return r.sess, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.sess != nil {
				r.sess.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// newSession returns a session and the function that releases its slot.
// A channel the server refuses (too many sessions) is retried on the same
// connection; only a broken transport leads to a new connection, so a
// running build or terminal is never cut by an unrelated failure.
func (s *SSH) newSession(ctx context.Context) (*ssh.Session, func(), error) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() { <-s.sem }
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		var c *ssh.Client
		if c, err = s.get(ctx); err != nil {
			break
		}
		var sess *ssh.Session
		if sess, err = openSession(ctx, c); err == nil {
			return sess, release, nil
		}
		if ctx.Err() != nil {
			break
		}
		var refused *ssh.OpenChannelError
		if errors.As(err, &refused) {
			// The transport works; the server is at its session limit. Wait a moment.
			select {
			case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
			case <-ctx.Done():
				err = ctx.Err()
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}
		s.forget(c)
	}
	release()
	return nil, nil, err
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
	sess, release, err := s.newSession(ctx)
	if err != nil {
		return err
	}
	defer release()
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
	sess, release, err := s.newSession(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (Session, error) {
		sess.Close()
		release()
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		return fail(err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		return fail(err)
	}
	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw
	if err := sess.Start(cmd); err != nil {
		return fail(err)
	}
	go func() {
		_ = sess.Wait()
		_ = pw.Close()
		release() // the slot is free once the shell has ended
	}()
	return &sshSession{sess: sess, stdin: stdin, out: pr}, nil
}

func (s *SSH) Close() error {
	s.mu.Lock()
	c := s.client
	s.client = nil
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
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
