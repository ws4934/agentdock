package desktop

import (
	"context"
	"fmt"
	"testing"
)

type completionRaceBackend struct {
	*fakeWindowBackend
	onSample func()
}

func (b *completionRaceBackend) State(ctx context.Context) (State, error) {
	if b.onSample != nil {
		b.onSample()
	}
	// 模拟系统已返回一份有效样本，但取消/停止与该返回同时发生。
	return b.fakeWindowBackend.State(context.Background())
}
func TestWaitCompletionDoesNotHideLateCancellation(t *testing.T) {
	for _, mode := range []string{"caller_cancel", "local_stop"} {
		for _, timeout := range []int{0, 1000} {
			t.Run(fmt.Sprintf("%s/%d", mode, timeout), func(t *testing.T) {
				b := &completionRaceBackend{fakeWindowBackend: windowFixture()}
				s := New(true, b)
				defer s.Close()
				localPoll(t, s, "")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				b.onSample = func() {
					if mode == "caller_cancel" {
						cancel()
					} else {
						localCommand(t, s, "stop")
					}
				}
				result, err := s.Wait(ctx, WaitRequest{PID: 20, WindowID: 9, Condition: "window_exists", TimeoutMS: msPointer(timeout)})
				if err == nil || result != nil {
					t.Fatalf("late cancellation became condition result: result=%v err=%v", result, err)
				}
				if len(b.inputs)+len(b.events) != 0 {
					t.Fatal("wait dispatched input")
				}
			})
		}
	}
}
