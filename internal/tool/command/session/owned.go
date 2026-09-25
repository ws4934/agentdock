package session

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

func ShellCommand(ctx context.Context, command string) *exec.Cmd { return shellCommand(ctx, command) }

var ErrProcessUndrained = errors.New("process tree did not drain")

// RunOwned reuses the normal process-group / Windows Job Object owner for a
// command inside a detached supervisor. The caller owns the supervisor lifetime.
func RunOwned(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	runner, err := startStandardRunner(cmd, stdout, stderr)
	if err != nil {
		return -1, err
	}
	_ = runner.Stdin().Close()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() { code, err := runner.Wait(); done <- result{code, err} }()
	select {
	case r := <-done:
		return r.code, r.err
	case <-ctx.Done():
		killErr := runner.Kill()
		select {
		case r := <-done:
			return r.code, errors.Join(ctx.Err(), killErr, r.err)
		case <-time.After(5 * time.Second):
			return -1, errors.Join(ctx.Err(), killErr, ErrProcessUndrained)
		}
	}
}
