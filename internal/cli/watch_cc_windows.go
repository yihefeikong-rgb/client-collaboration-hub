//go:build windows

package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gopty "github.com/aymanbagabas/go-pty"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

// resolveCCSidecar finds the CC-HAHA sidecar executable. It never falls back
// to the official Claude CLI: the sidecar is the only supported CC-HAHA entry.
func resolveCCSidecar() (string, error) {
	if explicit := os.Getenv("CC_HAHA_SIDECAR_PATH"); explicit != "" {
		if info, err := os.Stat(explicit); err == nil && !info.IsDir() {
			return explicit, nil
		}
		return "", fmt.Errorf("CC_HAHA_SIDECAR_PATH %q does not exist", explicit)
	}
	candidates := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "claude-code-desktop", "Claude Code Haha", "resources", "app.asar.unpacked", "src-tauri", "binaries", "claude-sidecar-x86_64-pc-windows-msvc.exe"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("CC-HAHA sidecar not found; set CC_HAHA_SIDECAR_PATH")
}

// wakeCCHaha launches the CC-HAHA sidecar through a Windows ConPTY and waits
// until the process exits, the task moves forward, or the timeout expires.
// A real terminal is required by the sidecar CLI; plain pipes hang.
func (n *wakeNotifier) wakeCCHaha(ctx context.Context, snapshot store.TaskSnapshot, prompt string) error {
	sidecar, err := resolveCCSidecar()
	if err != nil {
		return err
	}
	appRoot := filepath.Dir(sidecar)
	pty, err := gopty.New()
	if err != nil {
		return fmt.Errorf("create pty: %w", err)
	}
	defer pty.Close()
	conPTY, ok := pty.(gopty.ConPty)
	if !ok {
		return fmt.Errorf("expected ConPty on windows, got %T", pty)
	}
	if err := conPTY.Resize(220, 50); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}

	workDir := n.app.Root
	if binding, bindingErr := n.app.Bindings.ReadBinding(ctx, DefaultDeviceID(), snapshot.Project.ID); bindingErr == nil {
		if info, statErr := os.Stat(binding.LocalPath); statErr == nil && info.IsDir() {
			workDir = binding.LocalPath
		}
	}
	cmd := conPTY.Command(sidecar, "cli", "--app-root", appRoot, "--permission-mode", "bypassPermissions", "--", prompt)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sidecar: %w", err)
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: CC-HAHA sidecar started for %s (pid=%d)\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, cmd.Process.Pid)

	// Drain the PTY so the sidecar never blocks on output; content is
	// discarded because it is terminal rendering, not structured results.
	go func() {
		scanner := bufio.NewScanner(pty)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
		}
	}()

	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(n.taskTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-waitDone:
			return nil
		case <-ticker.C:
			current, queryErr := n.app.Query.Snapshot(ctx, snapshot.Task.ID, 0)
			if queryErr == nil && (current.State.Status != snapshot.State.Status || current.State.Version != snapshot.State.Version) {
				_ = cmd.Process.Kill()
				return nil
			}
		case <-timeout.C:
			_ = cmd.Process.Kill()
			return fmt.Errorf("wake %s timed out after %s", snapshot.Task.ID, n.taskTimeout)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		}
	}
}
