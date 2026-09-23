// Package executor runs shell commands on managed servers, either locally or
// over SSH. Every Docker/Git operation in Shipyard goes through an Executor,
// which makes local and remote servers behave identically.
package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

type RunOpts struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer // defaults to Stdout when nil
	Env    map[string]string
}

// Session is an interactive PTY session (web terminal).
type Session interface {
	io.ReadWriteCloser
	Resize(rows, cols int) error
	Wait() error
}

type Executor interface {
	Run(ctx context.Context, cmd string, opts RunOpts) error
	PTY(ctx context.Context, cmd string, rows, cols int) (Session, error)
	Close() error
}

// Output runs cmd and returns trimmed stdout. Stderr is included in the error.
func Output(ctx context.Context, e Executor, cmd string) (string, error) {
	var out, errb bytes.Buffer
	err := e.Run(ctx, cmd, RunOpts{Stdout: &out, Stderr: &errb})
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("%w: %s", err, truncate(msg, 2000))
	}
	return strings.TrimSpace(out.String()), nil
}

// WriteFile writes content to path on the target, creating parent directories.
func WriteFile(ctx context.Context, e Executor, path string, content []byte, mode string) error {
	if mode == "" {
		mode = "0644"
	}
	dir := path[:max(strings.LastIndex(path, "/"), 0)]
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && chmod %s %s", Quote(dir), Quote(path), mode, Quote(path))
	var errb bytes.Buffer
	if err := e.Run(ctx, cmd, RunOpts{Stdin: bytes.NewReader(content), Stdout: io.Discard, Stderr: &errb}); err != nil {
		return fmt.Errorf("write %s: %w: %s", path, err, errb.String())
	}
	return nil
}

// Quote returns s as a single-quoted POSIX shell word.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+%", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
