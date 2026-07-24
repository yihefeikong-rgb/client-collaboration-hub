package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type fakeLock struct{ unlocked bool }

func (l *fakeLock) Unlock() error { l.unlocked = true; return nil }

type fakeLocker struct {
	paths []string
	err   error
}

func (l *fakeLocker) Lock(_ context.Context, path string) (Lock, error) {
	l.paths = append(l.paths, path)
	if l.err != nil {
		return nil, l.err
	}
	return &fakeLock{}, nil
}

func TestScopedLocksUseNarrowPaths(t *testing.T) {
	fake := &fakeLocker{}
	locks := ScopedLocks{Root: "root", Locker: fake}
	for _, lock := range []func(context.Context) (Lock, error){
		func(ctx context.Context) (Lock, error) { return locks.Task(ctx, "T-0001") },
		locks.Projects,
		locks.Clients,
	} {
		if _, err := lock(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		filepath.Join("root", "tasks", "T-0001", ".lock"),
		filepath.Join("root", "projects", ".lock"),
		filepath.Join("root", "clients", ".lock"),
	}
	for i := range want {
		if fake.paths[i] != want[i] {
			t.Fatalf("path[%d] = %q, want %q", i, fake.paths[i], want[i])
		}
	}
}

func TestScopedLocksReturnLockerError(t *testing.T) {
	want := errors.New("locked")
	locks := ScopedLocks{Root: "root", Locker: &fakeLocker{err: want}}
	if _, err := locks.Task(context.Background(), "T-0001"); !errors.Is(err, want) {
		t.Fatalf("Task() error = %v, want %v", err, want)
	}
}
