//go:build !windows

package cli

import (
	"context"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

// wakeCCHaha deliberately does not fall back to a CLI on non-Windows hosts.
// A process exit cannot prove that this exact desktop delivery was accepted.
func (n *wakeNotifier) wakeCCHaha(_ context.Context, _ store.TaskSnapshot, _ string, _ string) error {
	return unsupportedCCHahaDesktopDelivery()
}
