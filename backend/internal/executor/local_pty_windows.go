//go:build windows

package executor

import (
	"context"
	"errors"
)

func (Local) PTY(ctx context.Context, cmd string, rows, cols int) (Session, error) {
	return nil, errors.New("interactive terminal on the local server requires Linux; run Shipyard in Docker")
}
