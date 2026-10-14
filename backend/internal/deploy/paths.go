package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/portdock/portdock/internal/executor"
)

// ensureInside refuses paths that lead outside root on the server once
// symbolic links are resolved: a repository can hold a link to / or to
// PortDock's data directory, and a build context, a Dockerfile or a bind mount
// through it would hand those over. A path that does not exist yet is judged
// by its closest existing parent.
func ensureInside(ctx context.Context, x executor.Executor, root string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "r=$(readlink -f %s) || exit 1; for p in", q(root))
	for _, p := range paths {
		b.WriteString(" " + q(p))
	}
	b.WriteString(`; do t="$p"; while [ ! -e "$t" ] && [ ! -L "$t" ] && [ "$t" != / ]; do t=$(dirname "$t"); done; ` +
		`t=$(readlink -f "$t"); case "$t" in "$r"|"$r"/*) ;; *) echo "$p";; esac; done`)
	out, err := executor.Output(ctx, x, b.String())
	if err != nil {
		return fmt.Errorf("check paths: %w", err)
	}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return fmt.Errorf("%s leads outside the repository through a symbolic link", strings.TrimPrefix(l, root+"/"))
		}
	}
	return nil
}

// checkRepoPaths runs after the checkout, before PortDock writes or builds
// anything in it: the build context, Dockerfile, compose file and publish
// directory must stay inside the repository.
func (j *job) checkRepoPaths(ctx context.Context) error {
	paths := []string{j.ctxDir}
	for _, rels := range [][]string{
		{j.app.BaseDirectory, j.app.DockerfilePath},
		{j.app.BaseDirectory, j.app.ComposePath},
		{j.app.BaseDirectory, j.app.PublishDirectory},
	} {
		if p, err := subPath(j.srcDir, rels...); err == nil && p != j.srcDir {
			paths = append(paths, p)
		}
	}
	return ensureInside(ctx, j.x, j.srcDir, paths...)
}
