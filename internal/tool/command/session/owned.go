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
	return RunOwnedTracked(ctx, cmd, stdout, stderr, "", nil)
}

// 将本次新进程的身份交给调用方持久化；失败就取消，不重放操作。
func RunOwnedTracked(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer, name string, started func(int) error) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	runner, err := startStandardRunnerNamed(cmd, stdout, stderr, name)
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
	if started != nil {
		if err := started(cmd.Process.Pid); err != nil {
			killErr := runner.Kill()
			select {
			case r := <-done:
				return r.code, errors.Join(err, killErr, r.err)
			case <-time.After(5 * time.Second):
				return -1, errors.Join(err, killErr, ErrProcessUndrained)
			}
		}
	}
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
