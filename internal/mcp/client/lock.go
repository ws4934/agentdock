package client

import (
	"context"
	"sync"
)

// 零值可用的可取消互斥锁。排队不会创建等待 goroutine，也不会持有全局注册锁。
type contextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *contextMutex) init() { m.once.Do(func() { m.token = make(chan struct{}, 1) }) }
func (m *contextMutex) LockContext(ctx context.Context) error {
	m.init()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *contextMutex) Lock()   { _ = m.LockContext(context.Background()) }
func (m *contextMutex) Unlock() { <-m.token }
func (m *contextMutex) TryLock() bool {
	m.init()
	select {
	case m.token <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *serverState) lifetime() context.Context {
	s.lifeOnce.Do(func() { s.life, s.stop = context.WithCancel(context.Background()) })
	return s.life
}
func (s *serverState) retire() { _ = s.lifetime(); s.stop() }
