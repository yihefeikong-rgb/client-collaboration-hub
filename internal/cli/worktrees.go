package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

// taskWorktree 记录并行 agent 对任务工作区的认领。这是运行时协调事实
// （与 wake-state 同级），不进入任务审计账本；审计仍由事件与 Evidence 负责。
type taskWorktree struct {
	TaskID    string    `json:"task_id"`
	ClaimedBy string    `json:"claimed_by"`
	Worktree  string    `json:"worktree"`
	ClaimedAt time.Time `json:"claimed_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type worktreeRegistry struct {
	root   string
	locker store.Locker
}

func newWorktreeRegistry(root string) *worktreeRegistry {
	return &worktreeRegistry{root: root, locker: store.FlockLocker{}}
}

func (r *worktreeRegistry) path() string {
	return filepath.Join(r.root, "collaboration", ".runtime", "worktrees.json")
}

func (r *worktreeRegistry) lockPath() string {
	return filepath.Join(r.root, "collaboration", ".runtime", "locks", "worktrees.lock")
}

func (r *worktreeRegistry) read() (map[string]taskWorktree, error) {
	data, err := os.ReadFile(r.path())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]taskWorktree{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := map[string]taskWorktree{}
	if len(data) == 0 {
		return records, nil
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("decode worktree registry: %w", err)
	}
	return records, nil
}

func (r *worktreeRegistry) write(records map[string]taskWorktree) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path()), 0o700); err != nil {
		return err
	}
	temp := r.path() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, r.path())
}

// Claim 登记或更新一个任务工作区。同一任务只能有一个认领者；其他客户端
// 认领同一任务会被拒绝，避免并行 agent 同时修改同一目录。
func (r *worktreeRegistry) Claim(ctx context.Context, taskID, actor, worktree string, now time.Time) (taskWorktree, error) {
	if !protocol.IsValidID(taskID) || !protocol.IsValidID(actor) {
		return taskWorktree{}, fmt.Errorf("invalid task or actor id")
	}
	if !filepath.IsAbs(worktree) {
		return taskWorktree{}, fmt.Errorf("worktree must be an absolute path")
	}
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		return taskWorktree{}, fmt.Errorf("worktree directory is not available: %s", worktree)
	}
	lock, err := r.locker.Lock(ctx, r.lockPath())
	if err != nil {
		return taskWorktree{}, err
	}
	defer lock.Unlock()
	records, err := r.read()
	if err != nil {
		return taskWorktree{}, err
	}
	if existing, ok := records[taskID]; ok && existing.ClaimedBy != actor {
		return taskWorktree{}, fmt.Errorf("task %s is already claimed by %s", taskID, existing.ClaimedBy)
	}
	record := taskWorktree{TaskID: taskID, ClaimedBy: actor, Worktree: worktree, ClaimedAt: now.UTC(), UpdatedAt: now.UTC()}
	if existing, ok := records[taskID]; ok {
		record.ClaimedAt = existing.ClaimedAt
	}
	records[taskID] = record
	if err := r.write(records); err != nil {
		return taskWorktree{}, err
	}
	return record, nil
}

func (r *worktreeRegistry) Get(ctx context.Context, taskID string) (taskWorktree, bool, error) {
	records, err := r.read()
	if err != nil {
		return taskWorktree{}, false, err
	}
	record, ok := records[taskID]
	return record, ok, nil
}

func (r *worktreeRegistry) List(ctx context.Context) ([]taskWorktree, error) {
	records, err := r.read()
	if err != nil {
		return nil, err
	}
	result := make([]taskWorktree, 0, len(records))
	for _, record := range records {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

// Release 释放认领。只有当前认领者或空 actor（人工清理）可以释放。
func (r *worktreeRegistry) Release(ctx context.Context, taskID, actor string) error {
	if !protocol.IsValidID(taskID) {
		return fmt.Errorf("invalid task id")
	}
	lock, err := r.locker.Lock(ctx, r.lockPath())
	if err != nil {
		return err
	}
	defer lock.Unlock()
	records, err := r.read()
	if err != nil {
		return err
	}
	record, ok := records[taskID]
	if !ok {
		return nil
	}
	if actor != "" && record.ClaimedBy != actor {
		return fmt.Errorf("task %s is claimed by %s, not %s", taskID, record.ClaimedBy, actor)
	}
	delete(records, taskID)
	return r.write(records)
}
