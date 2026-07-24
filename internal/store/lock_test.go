package store

import (
	"context"
	"errors"
	"os"
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
		filepath.Join("root", ".runtime", "locks", "tasks", "T-0001.lock"),
		filepath.Join("root", ".runtime", "locks", "projects.lock"),
		filepath.Join("root", ".runtime", "locks", "clients.lock"),
	}
	for i := range want {
		if fake.paths[i] != want[i] {
			t.Fatalf("path[%d] = %q, want %q", i, fake.paths[i], want[i])
		}
	}
}

func TestScopedLocksRejectTaskPathTraversal(t *testing.T) {
	fake := &fakeLocker{}
	locks := ScopedLocks{Root: "root", Locker: fake}
	for _, taskID := range []string{"", "..", "../projects", "T/0001", `T\0001`, `C:\temp`} {
		if _, err := locks.Task(context.Background(), taskID); err == nil {
			t.Fatalf("Task(%q) error = nil", taskID)
		}
	}
	if len(fake.paths) != 0 {
		t.Fatalf("invalid task IDs reached locker: %#v", fake.paths)
	}
}

func TestScopedLocksReturnLockerError(t *testing.T) {
	want := errors.New("locked")
	locks := ScopedLocks{Root: "root", Locker: &fakeLocker{err: want}}
	if _, err := locks.Task(context.Background(), "T-0001"); !errors.Is(err, want) {
		t.Fatalf("Task() error = %v, want %v", err, want)
	}
}

func TestFlockLockerCreatesOnlyRuntimeLockDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".runtime", "locks", "tasks", "T-0001.lock")
	lock, err := (FlockLocker{}).Lock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks")); !os.IsNotExist(err) {
		t.Fatalf("task data directory should not be created, stat error = %v", err)
	}
}
