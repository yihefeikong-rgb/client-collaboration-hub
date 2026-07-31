//go:build !windows

package cli

import (
	"context"
	"os"
	"os/exec"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

// wakeCCHaha is a best-effort fallback on non-Windows platforms. CC-HAHA is a
// Windows desktop client; the official Claude CLI is intentionally avoided.
func (n *wakeNotifier) wakeCCHaha(ctx context.Context, snapshot store.TaskSnapshot, prompt string) error {
	cmd := exec.CommandContext(ctx, n.ccCommand, "-p", prompt)
	cmd.Dir = n.app.Root
	cmd.Env = os.Environ()
	cmd.Stdout = n.app.Stdout
	cmd.Stderr = n.app.Stderr
	return cmd.Run()
}
