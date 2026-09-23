package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/shipyard/shipyard/internal/docker"
	"github.com/shipyard/shipyard/internal/executor"
	"github.com/shipyard/shipyard/internal/store"
	"github.com/shipyard/shipyard/internal/stream"
)

type dockerContainer = docker.Container

// sse wraps a ResponseWriter for Server-Sent Events.
type sse struct {
	w  http.ResponseWriter
	rc *http.ResponseController
	mu sync.Mutex
}

func newSSE(w http.ResponseWriter) *sse {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	s := &sse{w: w, rc: http.NewResponseController(w)}
	_ = s.rc.SetWriteDeadline(time.Time{})
	s.flush()
	return s
}

func (s *sse) flush() { _ = s.rc.Flush() }

func (s *sse) send(event string, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	s.flush()
	return nil
}

func (s *sse) ping() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprint(s.w, ": ping\n\n"); err != nil {
		return err
	}
	s.flush()
	return nil
}

// sseTopic relays hub messages of topic until the client disconnects.
func (s *Server) sseTopic(w http.ResponseWriter, r *http.Request, topic, initial string) error {
	ch, unsub := s.hub.Subscribe(topic)
	defer unsub()
	out := newSSE(w)
	if initial != "" {
		_ = out.send("snapshot", initial)
	}
	return pump(r.Context(), out, ch, nil)
}

// pump forwards messages; stop (optional) ends the stream after a matching message.
func pump(ctx context.Context, out *sse, ch <-chan stream.Message, stop func(stream.Message) bool) error {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			if err := out.ping(); err != nil {
				return nil
			}
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			if err := out.send(m.Type, m.Data); err != nil {
				return nil
			}
			if stop != nil && stop(m) {
				return nil
			}
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	return s.sseTopic(w, r, "events", "")
}

// sseDeployment sends the logs so far, then live lines until the deployment finishes.
func (s *Server) sseDeployment(w http.ResponseWriter, r *http.Request, d store.Deployment, done bool) error {
	out := newSSE(w)
	if done {
		_ = out.send("snapshot", d.Logs)
		_ = out.send("status", d.Status)
		return nil
	}
	snap, ch, unsub, live := s.engine.AttachLogs(d.ID)
	defer unsub()
	if !live {
		// Not running yet (queued) or finished in between: re-check the database.
		if cur, err := store.ByID[store.Deployment](r.Context(), s.db, "deployments", d.ID); err == nil && cur.Status != "queued" && cur.Status != "running" {
			_ = out.send("snapshot", cur.Logs)
			_ = out.send("status", cur.Status)
			return nil
		}
	}
	_ = out.send("snapshot", snap)
	return pump(r.Context(), out, ch, func(m stream.Message) bool { return m.Type == "status" })
}

// lineWriter turns a byte stream into SSE "log" events.
type lineWriter struct {
	out  *sse
	part []byte
}

func (l *lineWriter) Write(p []byte) (int, error) {
	data := append(l.part, p...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if err := l.out.send("log", string(bytes.TrimRight(data[:i], "\r"))); err != nil {
			return 0, err
		}
		data = data[i+1:]
	}
	l.part = append(l.part[:0], data...)
	return len(p), nil
}

func (s *Server) sseContainerLogs(w http.ResponseWriter, r *http.Request, x executor.Executor, name string) error {
	tail := 200
	if t, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && t >= 0 && t <= 10000 {
		tail = t
	}
	follow := r.URL.Query().Get("follow") != "false"
	timestamps := r.URL.Query().Get("timestamps") == "true"
	out := newSSE(w)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if out.ping() != nil {
					cancel()
					return
				}
			}
		}
	}()
	lw := &lineWriter{out: out}
	err := docker.Logs(ctx, x, name, tail, follow, timestamps, lw)
	if len(lw.part) > 0 {
		_ = out.send("log", string(lw.part))
	}
	if err != nil && ctx.Err() == nil {
		_ = out.send("failure", err.Error())
	}
	_ = out.send("status", "ended")
	return nil
}

// ---- terminals ----

type termMsg struct {
	Type string `json:"type"` // input | resize
	Data string `json:"data"`
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request, x executor.Executor, cmd string) error {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: s.cfg.DevMode})
	if err != nil {
		return nil // Accept already wrote the error response
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	rows, cols := 30, 120
	if v, err := strconv.Atoi(r.URL.Query().Get("rows")); err == nil && v > 0 {
		rows = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("cols")); err == nil && v > 0 {
		cols = v
	}
	sess, err := x.PTY(ctx, cmd, rows, cols)
	if err != nil {
		_ = c.Write(ctx, websocket.MessageText, []byte("\r\n\x1b[31mCould not open terminal: "+err.Error()+"\x1b[0m\r\n"))
		c.Close(websocket.StatusInternalError, "pty failed")
		return nil
	}
	defer sess.Close()

	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = c.Write(ctx, websocket.MessageText, []byte("\r\n\x1b[90m[session closed]\x1b[0m\r\n"))
				c.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return nil
		}
		var m termMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "input":
			if _, err := sess.Write([]byte(m.Data)); err != nil {
				return nil
			}
		case "resize":
			if m.Rows > 0 && m.Cols > 0 {
				_ = sess.Resize(m.Rows, m.Cols)
			}
		}
	}
}

func (s *Server) containerTerminal(w http.ResponseWriter, r *http.Request) error {
	x, srv, err := s.serverExec(r)
	if err != nil {
		return err
	}
	cid := r.PathValue("cid")
	if err := s.authorizeContainer(r, x, cid, permWrite); err != nil {
		return err
	}
	s.activity(r, "terminal.container", srv.Name+"/"+cid)
	cmd := "docker exec -it " + executor.Quote(cid) + " sh -c 'if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi'"
	return s.terminal(w, r, x, cmd)
}

func (s *Server) serverTerminal(w http.ResponseWriter, r *http.Request) error {
	x, srv, err := s.serverExec(r)
	if err != nil {
		return err
	}
	s.activity(r, "terminal.server", srv.Name)
	return s.terminal(w, r, x, `cd ~ 2>/dev/null; exec "${SHELL:-/bin/sh}" -l`)
}
