//go:build !windows

package executor

import (
	"context"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type localSession struct {
	f   *os.File
	cmd *exec.Cmd
}

func (s *localSession) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *localSession) Write(p []byte) (int, error) { return s.f.Write(p) }
func (s *localSession) Close() error {
	_ = s.f.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}
func (s *localSession) Resize(rows, cols int) error {
	return pty.Setsize(s.f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}
func (s *localSession) Wait() error { return s.cmd.Wait() }

func (Local) PTY(ctx context.Context, cmd string, rows, cols int) (Session, error) {
	sh, flag := shell()
	c := exec.CommandContext(ctx, sh, flag, cmd)
	c.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(c, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	return &localSession{f: f, cmd: c}, nil
}
