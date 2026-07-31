package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

const (
	defaultWatchInterval = 3 * time.Second
	wakeStateFileName    = "wake-state.json"
)

const (
	ccExecutionPrompt = `协作中枢给你分配了任务。请读取 collab://manual/agent-operating-guide 资源，然后调用 collab_get_next_work（client_id 用 cc-haha）找到你的任务，再调用 collab_get_task 读取任务详情和验收标准。按操作指南自主完成：认领任务、生成交接包、实施、运行真实测试、填写候选响应并调用 collab_submit_candidate 提交。完成后简要报告结果。`
	ccRevisionPrompt  = `协作中枢有任务需要返工。请读取 collab://manual/agent-operating-guide 资源，调用 collab_get_next_work（client_id 用 cc-haha）找到返工任务，读取 REVIEW 反馈和返工要求。按指南重新生成交接包、实施返工并提交候选响应。完成后简要报告结果。`
	codexReviewPrompt = `协作中枢有任务进入 REVIEW 需要你审查。请读取 collab://manual/agent-operating-guide 资源，然后调用 collab_get_next_work（client_id 用 codex）找到待审查任务，读取任务详情和 Evidence。按项目终审模式提交审查候选：生成交接包、填写候选响应（request_changes 或 approve）、调用 collab_submit_candidate 提交。完成后报告审查结论。`
)

// wakeRule maps a task state to the client that should be woken up.
type wakeRule struct {
	Client string
	Prompt string
}

func wakeRuleFor(status protocol.Status, responsible string) *wakeRule {
	switch status {
	case protocol.Assigned:
		if responsible == "cc-haha" {
			return &wakeRule{Client: "cc-haha", Prompt: ccExecutionPrompt}
		}
	case protocol.RevisionRequired:
		if responsible == "cc-haha" {
			return &wakeRule{Client: "cc-haha", Prompt: ccRevisionPrompt}
		}
	case protocol.Review:
		if responsible == "codex" {
			return &wakeRule{Client: "codex", Prompt: codexReviewPrompt}
		}
	}
	return nil
}

type wakeState struct {
	Notified map[string]bool `json:"notified"`
}

// wakeNotifier watches the task journal and launches the responsible client
// headless when a task needs its next action. It never controls a client: it
// only starts the client's own CLI with a fixed prompt, and the client decides
// what to do by reading the hub through MCP.
type wakeNotifier struct {
	app          *App
	interval     time.Duration
	dryRun       bool
	ccCommand    string
	codexCommand string
	taskTimeout  time.Duration
	statePath    string

	mu       sync.Mutex
	notified map[string]bool
	running  map[string]bool
}

func (a *App) watch(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("watch")
	interval := fs.Duration("interval", defaultWatchInterval, "")
	dryRun := fs.Bool("dry-run", false, "")
	once := fs.Bool("once", false, "")
	ccCommand := fs.String("cc-command", "claude", "")
	codexCommand := fs.String("codex-command", "codex", "")
	taskTimeout := fs.Duration("task-timeout", 30*time.Minute, "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	notifier := &wakeNotifier{
		app:          a,
		interval:     *interval,
		dryRun:       *dryRun,
		ccCommand:    *ccCommand,
		codexCommand: *codexCommand,
		taskTimeout:  *taskTimeout,
		statePath:    filepath.Join(a.Root, "collaboration", ".runtime", wakeStateFileName),
		notified:     map[string]bool{},
		running:      map[string]bool{},
	}
	if err := notifier.load(); err != nil {
		return exitCode(err), err
	}
	defer notifier.save()

	watchCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	fmt.Fprintf(a.Stdout, "collab watch started (interval=%s dry_run=%t cc=%s codex=%s)\n", notifier.interval, notifier.dryRun, notifier.ccCommand, notifier.codexCommand)
	notifier.scan(watchCtx)
	if *once {
		fmt.Fprintln(a.Stdout, "collab watch completed one scan")
		return ExitOK, nil
	}
	ticker := time.NewTicker(notifier.interval)
	defer ticker.Stop()
	for {
		select {
		case <-watchCtx.Done():
			fmt.Fprintln(a.Stdout, "collab watch stopped")
			return ExitOK, nil
		case <-ticker.C:
			notifier.scan(watchCtx)
		}
	}
}

func (n *wakeNotifier) scan(ctx context.Context) {
	taskIDs, err := n.app.Journal.ListTaskIDs(ctx)
	if err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] list tasks: %v\n", err)
		return
	}
	for _, taskID := range taskIDs {
		snapshot, err := n.app.Query.Snapshot(ctx, taskID, 0)
		if err != nil {
			fmt.Fprintf(n.app.Stderr, "[watch] read task %s: %v\n", taskID, err)
			continue
		}
		key := fmt.Sprintf("%s|%s|%d", taskID, snapshot.State.Status, snapshot.State.Version)
		n.mu.Lock()
		if n.notified[key] {
			n.mu.Unlock()
			continue
		}
		n.mu.Unlock()
		rule := wakeRuleFor(snapshot.State.Status, snapshot.State.ResponsibleClient)
		if rule == nil {
			n.markNotified(key)
			continue
		}
		n.mu.Lock()
		if n.running[rule.Client] {
			n.mu.Unlock()
			fmt.Fprintf(n.app.Stdout, "[watch] %s: %s busy, will re-check %s later\n", time.Now().UTC().Format(time.RFC3339), rule.Client, taskID)
			continue
		}
		n.running[rule.Client] = true
		n.notified[key] = true
		n.mu.Unlock()
		if n.dryRun {
			fmt.Fprintf(n.app.Stdout, "[watch] %s: WOULD wake %s for %s (%s)\n", time.Now().UTC().Format(time.RFC3339), rule.Client, taskID, snapshot.State.Status)
			n.mu.Lock()
			delete(n.running, rule.Client)
			n.mu.Unlock()
			continue
		}
		go n.wake(ctx, rule, snapshot)
	}
	n.save()
}

func (n *wakeNotifier) wake(ctx context.Context, rule *wakeRule, snapshot store.TaskSnapshot) {
	defer func() {
		n.mu.Lock()
		delete(n.running, rule.Client)
		n.mu.Unlock()
		n.allowRetryIfUnchanged(ctx, snapshot, rule)
	}()
	command, args := n.ccCommand, []string{"-p"}
	if rule.Client == "codex" {
		command, args = n.codexCommand, []string{"exec", "-p"}
	}
	workDir := n.app.Root
	if binding, err := n.app.Bindings.ReadBinding(ctx, DefaultDeviceID(), snapshot.Project.ID); err == nil {
		if info, statErr := os.Stat(binding.LocalPath); statErr == nil && info.IsDir() {
			workDir = binding.LocalPath
		}
	}
	prompt := rule.Prompt + "\n任务：" + snapshot.Task.ID
	if rule.Client == "cc-haha" {
		if err := n.wakeCCHaha(ctx, snapshot, prompt); err != nil {
			fmt.Fprintf(n.app.Stderr, "[watch] %s: CC-HAHA wake failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, err)
		}
		return
	}
	cmd := exec.Command(command, append(args, prompt)...)
	cmd.Dir = workDir
	cmd.Stdout = n.app.Stdout
	cmd.Stderr = n.app.Stderr
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: wake %s for %s (cwd=%s)\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID, workDir)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] %s: failed to start %s for %s: %v\n", time.Now().UTC().Format(time.RFC3339), command, snapshot.Task.ID, err)
		return
	}
	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] %s: %s finished %s with error: %v\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID, err)
		return
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: %s finished %s\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID)
}

// allowRetryIfUnchanged removes the notified marker when the task is still in
// the same state after a wake attempt, so a later scan can try again. If the
// task moved forward, the marker stays and the new state drives the next wake.
func (n *wakeNotifier) allowRetryIfUnchanged(ctx context.Context, snapshot store.TaskSnapshot, rule *wakeRule) {
	current, err := n.app.Query.Snapshot(ctx, snapshot.Task.ID, 0)
	if err != nil {
		return
	}
	if current.State.Status != snapshot.State.Status || current.State.Version != snapshot.State.Version {
		return
	}
	if wakeRuleFor(current.State.Status, current.State.ResponsibleClient) == nil {
		return
	}
	key := fmt.Sprintf("%s|%s|%d", snapshot.Task.ID, current.State.Status, current.State.Version)
	n.mu.Lock()
	delete(n.notified, key)
	n.mu.Unlock()
	fmt.Fprintf(n.app.Stdout, "[watch] %s: %s did not advance %s; will retry\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID)
}

func (n *wakeNotifier) markNotified(key string) {
	n.mu.Lock()
	n.notified[key] = true
	n.mu.Unlock()
}

func (n *wakeNotifier) load() error {
	data, err := os.ReadFile(n.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state wakeState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	n.mu.Lock()
	if state.Notified != nil {
		n.notified = state.Notified
	}
	n.mu.Unlock()
	return nil
}

func (n *wakeNotifier) save() error {
	if n.dryRun {
		return nil
	}
	n.mu.Lock()
	state := wakeState{Notified: make(map[string]bool, len(n.notified))}
	for key := range n.notified {
		state.Notified[key] = true
	}
	n.mu.Unlock()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(n.statePath), 0o700); err != nil {
		return err
	}
	temp := n.statePath + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, n.statePath)
}
