package executor

import (
	"context"
	"os"
	"os/exec"
	"runtime"
)

// Local runs commands on the machine (or container) where PortDock runs.
type Local struct{}

func shell() (string, string) {
	if runtime.GOOS == "windows" {
		// Development convenience: Git Bash / WSL provide `bash`.
		return "bash", "-c"
	}
	return "/bin/sh", "-c"
}

func (Local) Run(ctx context.Context, cmd string, opts RunOpts) error {
	sh, flag := shell()
	c := exec.CommandContext(ctx, sh, flag, cmd)
	c.Stdin = opts.Stdin
	c.Stdout = opts.Stdout
	c.Stderr = opts.Stderr
	if c.Stderr == nil {
		c.Stderr = opts.Stdout
	}
	if len(opts.Env) > 0 {
		c.Env = os.Environ()
		for k, v := range opts.Env {
			c.Env = append(c.Env, k+"="+v)
		}
	}
	return c.Run()
}

func (Local) Close() error { return nil }
