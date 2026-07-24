package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/huangxinyang/client-collaboration-hub/internal/protocol"
)

type Lock interface {
	Unlock() error
}

type Locker interface {
	Lock(context.Context, string) (Lock, error)
}

type FlockLocker struct {
	RetryDelay time.Duration
}

func (l FlockLocker) Lock(ctx context.Context, path string) (Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	delay := l.RetryDelay
	if delay == 0 {
		delay = 10 * time.Millisecond
	}
	fileLock := flock.New(path)
	locked, err := fileLock.TryLockContext(ctx, delay)
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("lock %q was not acquired", path)
	}
	return fileLock, nil
}

type ScopedLocks struct {
	Root   string
	Locker Locker
}

func (s ScopedLocks) Task(ctx context.Context, taskID string) (Lock, error) {
	if !protocol.IsValidID(taskID) {
		return nil, fmt.Errorf("invalid task lock id %q", taskID)
	}
	return s.Locker.Lock(ctx, filepath.Join(s.Root, ".runtime", "locks", "tasks", taskID+".lock"))
}

func (s ScopedLocks) Projects(ctx context.Context) (Lock, error) {
	return s.Locker.Lock(ctx, filepath.Join(s.Root, ".runtime", "locks", "projects.lock"))
}

func (s ScopedLocks) Clients(ctx context.Context) (Lock, error) {
	return s.Locker.Lock(ctx, filepath.Join(s.Root, ".runtime", "locks", "clients.lock"))
}
