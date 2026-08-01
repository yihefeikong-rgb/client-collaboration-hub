package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var worktreeTestTime = time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)

func TestWorktreeRegistryClaimConflictAndRelease(t *testing.T) {
	root := t.TempDir()
	registry := newWorktreeRegistry(root)
	worktreeA := filepath.Join(root, "wt-cc")
	worktreeB := filepath.Join(root, "wt-re")
	for _, path := range []string{worktreeA, worktreeB} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	record, err := registry.Claim(ctx, "T-0001", "cc-haha", worktreeA, worktreeTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if record.ClaimedBy != "cc-haha" || record.Worktree != worktreeA {
		t.Fatalf("claim record = %+v", record)
	}
	// 同一认领者可以更新工作区。
	later := worktreeTestTime.Add(time.Minute)
	if _, err := registry.Claim(ctx, "T-0001", "cc-haha", worktreeB, later); err != nil {
		t.Fatalf("same claimer update failed: %v", err)
	}
	// 其他客户端认领同一任务必须被拒绝。
	if _, err := registry.Claim(ctx, "T-0001", "reasonix", worktreeB, later); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("conflicting claim error = %v", err)
	}
	// Get 返回当前认领者与工作区。
	got, ok, err := registry.Get(ctx, "T-0001")
	if err != nil || !ok || got.ClaimedBy != "cc-haha" || got.Worktree != worktreeB {
		t.Fatalf("get = %+v, %t, %v", got, ok, err)
	}
	// 非认领者释放被拒绝；认领者释放成功。
	if err := registry.Release(ctx, "T-0001", "reasonix"); err == nil || !strings.Contains(err.Error(), "claimed by") {
		t.Fatalf("wrong release error = %v", err)
	}
	if err := registry.Release(ctx, "T-0001", "cc-haha"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := registry.Get(ctx, "T-0001"); err != nil || ok {
		t.Fatalf("release did not clear record: ok=%t err=%v", ok, err)
	}
	// 不存在的目录不能认领。
	if _, err := registry.Claim(ctx, "T-0002", "cc-haha", filepath.Join(root, "missing"), later); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("missing worktree error = %v", err)
	}
}

func TestTaskClaimCommandAndWatchPrompt(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assignTask(t, app, "T-CLAIM")
	worktree := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := app.Run([]string{"task", "claim", "--task", "T-CLAIM", "--actor", "cc-haha", "--worktree", worktree}); code != ExitOK {
		t.Fatalf("task claim exit = %d", code)
	}
	// 非责任方不能认领。
	if code := app.Run([]string{"task", "claim", "--task", "T-CLAIM", "--actor", "reasonix", "--worktree", worktree}); code == ExitOK {
		t.Fatal("non-responsible claim unexpectedly succeeded")
	}
	// watch 提示词应包含工作区登记。
	snapshot, err := app.Query.Snapshot(context.Background(), "T-CLAIM", 0)
	if err != nil {
		t.Fatal(err)
	}
	registry := newWorktreeRegistry(app.Root)
	prompt := withWorktreePrompt(context.Background(), "base", snapshot, registry)
	if !strings.Contains(prompt, "工作区登记") || !strings.Contains(prompt, worktree) {
		t.Fatalf("prompt does not carry worktree: %s", prompt)
	}
	// 释放后提示词不再包含工作区。
	if code := app.Run([]string{"task", "claim", "--task", "T-CLAIM", "--actor", "cc-haha", "--release"}); code != ExitOK {
		t.Fatalf("task claim release exit = %d", code)
	}
	prompt = withWorktreePrompt(context.Background(), "base", snapshot, registry)
	if strings.Contains(prompt, "工作区登记") {
		t.Fatalf("prompt still carries released worktree: %s", prompt)
	}
}
