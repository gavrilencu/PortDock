package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testSSHServer is a tiny in-process SSH server: it runs "exec" requests by
// echoing the command, allows at most `limit` sessions at once (like OpenSSH's
// MaxSessions) and refuses the channel above that.
type testSSHServer struct {
	ln     net.Listener
	limit  int32
	active atomic.Int32
	conns  atomic.Int32
	mu     sync.Mutex
	open   []net.Conn
}

func newTestSSHServer(t *testing.T, limit int32) (*testSSHServer, string, string) {
	t.Helper()
	hostPriv, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, _ := ssh.ParsePrivateKey([]byte(hostPriv))
	clientPriv, clientPub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	allowed, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(clientPub))
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) == string(allowed.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("unknown key")
	}}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &testSSHServer{ln: ln, limit: limit}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.mu.Lock()
			srv.open = append(srv.open, conn)
			srv.mu.Unlock()
			go srv.serve(conn, cfg)
		}
	}()
	return srv, clientPriv, ln.Addr().String()
}

func (s *testSSHServer) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	s.conns.Add(1)
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		if s.active.Load() >= s.limit {
			nc.Reject(ssh.ResourceShortage, "open failed: too many sessions")
			continue
		}
		s.active.Add(1)
		ch, requests, err := nc.Accept()
		if err != nil {
			s.active.Add(-1)
			continue
		}
		go func() {
			defer s.active.Add(-1)
			defer ch.Close()
			for req := range requests {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				req.Reply(true, nil)
				cmd := string(req.Payload[4:])
				time.Sleep(150 * time.Millisecond) // keep the session busy so the limit matters
				fmt.Fprintf(ch, "ran %s\n", cmd)
				ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
				return
			}
		}()
	}
}

// dropAll closes every accepted TCP connection, like a server that rebooted.
func (s *testSSHServer) dropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.open {
		c.Close()
	}
	s.open = nil
}

func newTestSSH(t *testing.T, limit int32) (*SSH, *testSSHServer) {
	srv, priv, addr := newTestSSHServer(t, limit)
	host, port, _ := net.SplitHostPort(addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	x := NewSSH(SSHTarget{ServerID: 1, Host: host, Port: p, User: "root", PrivateKey: priv}, nil)
	t.Cleanup(func() { x.Close() })
	return x, srv
}

func TestSSHSessionLimitDoesNotResetConnection(t *testing.T) {
	x, srv := newTestSSH(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out strings.Builder
			if err := x.Run(ctx, fmt.Sprintf("job-%d", i), RunOpts{Stdout: &out}); err != nil {
				errs <- fmt.Errorf("job-%d: %w", i, err)
			} else if !strings.Contains(out.String(), fmt.Sprintf("ran job-%d", i)) {
				errs <- fmt.Errorf("job-%d: output %q", i, out.String())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// Every command ran over the one connection; refused channels were retried, not re-dialed.
	if x.dials != 1 || srv.conns.Load() != 1 {
		t.Fatalf("dials = %d, server connections = %d; want 1", x.dials, srv.conns.Load())
	}
}

func TestSSHReconnectsAfterTransportLoss(t *testing.T) {
	x, srv := newTestSSH(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := x.Run(ctx, "one", RunOpts{}); err != nil {
		t.Fatal(err)
	}
	srv.dropAll()
	time.Sleep(100 * time.Millisecond)
	var out strings.Builder
	if err := x.Run(ctx, "two", RunOpts{Stdout: &out}); err != nil || !strings.Contains(out.String(), "ran two") {
		t.Fatalf("after transport loss: %v %q", err, out.String())
	}
	if x.dials != 2 {
		t.Fatalf("dials = %d, want 2", x.dials)
	}
}

func TestSSHSessionOpenHonoursContext(t *testing.T) {
	x, _ := newTestSSH(t, 0) // every channel is refused
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := x.Run(ctx, "never", RunOpts{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %s to give up", time.Since(start))
	}
	if len(x.sem) != 0 {
		t.Fatalf("session slot leaked: %d", len(x.sem))
	}
}
