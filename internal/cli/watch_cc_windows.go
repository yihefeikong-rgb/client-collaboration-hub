//go:build windows

package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// wakeCCHaha resumes the task's dedicated CC-HAHA session when it already
// exists and creates it with the same deterministic session ID on first use.
// Reusing one session per task keeps follow-up messages in the same
// conversation instead of starting a fresh context every time.
func (n *wakeNotifier) wakeCCHaha(ctx context.Context, snapshot store.TaskSnapshot, prompt string) error {
	sidecar, err := resolveCCSidecar()
	if err != nil {
		return err
	}
	sessionID := ccSessionUUID(snapshot.Task.ID)
	output, err := n.runCCHaha(ctx, sidecar, snapshot, prompt, "--resume", sessionID)
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(output), "no conversation found") {
		return err
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: no CC-HAHA session for %s yet; creating %s\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, sessionID)
	output, err = n.runCCHaha(ctx, sidecar, snapshot, prompt, "--session-id", sessionID)
	if err != nil {
		return fmt.Errorf("create cc-haha session %s: %w (output: %s)", sessionID, err, trimOutput(output))
	}
	return nil
}

// runCCHaha launches the CC-HAHA sidecar through a Windows ConPTY with the
// given session flag and waits until the process exits, the task moves
// forward, or the timeout expires. A real terminal is required by the sidecar
// CLI; plain pipes hang. Terminal output is captured (and bounded) so the
// caller can distinguish "session missing" from other failures.
func (n *wakeNotifier) runCCHaha(ctx context.Context, sidecar string, snapshot store.TaskSnapshot, prompt, sessionFlag, sessionID string) (string, error) {
	appRoot := filepath.Dir(sidecar)
	pty, err := gopty.New()
	if err != nil {
		return "", fmt.Errorf("create pty: %w", err)
	}
	defer pty.Close()
	conPTY, ok := pty.(gopty.ConPty)
	if !ok {
		return "", fmt.Errorf("expected ConPty on windows, got %T", pty)
	}
	if err := conPTY.Resize(220, 50); err != nil {
		return "", fmt.Errorf("resize pty: %w", err)
	}

	workDir := n.app.Root
	if binding, bindingErr := n.app.Bindings.ReadBinding(ctx, DefaultDeviceID(), snapshot.Project.ID); bindingErr == nil {
		if info, statErr := os.Stat(binding.LocalPath); statErr == nil && info.IsDir() {
			workDir = binding.LocalPath
		}
	}
	cmd := conPTY.Command(sidecar, "cli", "--app-root", appRoot, "--permission-mode", "bypassPermissions", "-p", sessionFlag, sessionID, "--", prompt)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start sidecar: %w", err)
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: CC-HAHA sidecar started for %s (session=%s pid=%d)\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, sessionID, cmd.Process.Pid)

	// Drain the PTY so the sidecar never blocks on output; keep a bounded
	// tail of the terminal rendering for failure diagnosis.
	var outputMu sync.Mutex
	var output strings.Builder
	go func() {
		scanner := bufio.NewScanner(pty)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			outputMu.Lock()
			if output.Len() < 512<<10 {
				output.WriteString(scanner.Text())
				output.WriteByte('\n')
			}
			outputMu.Unlock()
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	timeout := time.NewTimer(n.taskTimeout)
	defer timeout.Stop()
	for {
		select {
		case waitErr := <-waitDone:
			outputMu.Lock()
			captured := output.String()
			outputMu.Unlock()
			return captured, waitErr
		case <-ticker.C:
			current, queryErr := n.app.Query.Snapshot(ctx, snapshot.Task.ID, 0)
			if queryErr == nil && (current.State.Status != snapshot.State.Status || current.State.Version != snapshot.State.Version) {
				_ = cmd.Process.Kill()
				outputMu.Lock()
				captured := output.String()
				outputMu.Unlock()
				return captured, nil
			}
		case <-timeout.C:
			_ = cmd.Process.Kill()
			outputMu.Lock()
			captured := output.String()
			outputMu.Unlock()
			return captured, fmt.Errorf("wake %s timed out after %s", snapshot.Task.ID, n.taskTimeout)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			outputMu.Lock()
			captured := output.String()
			outputMu.Unlock()
			return captured, ctx.Err()
		}
	}
}

func trimOutput(output string) string {
	if len(output) <= 800 {
		return output
	}
	return output[len(output)-800:]
}
