package store

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
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
	return s.Locker.Lock(ctx, filepath.Join(s.Root, "tasks", taskID, ".lock"))
}

func (s ScopedLocks) Projects(ctx context.Context) (Lock, error) {
	return s.Locker.Lock(ctx, filepath.Join(s.Root, "projects", ".lock"))
}

func (s ScopedLocks) Clients(ctx context.Context) (Lock, error) {
	return s.Locker.Lock(ctx, filepath.Join(s.Root, "clients", ".lock"))
}
