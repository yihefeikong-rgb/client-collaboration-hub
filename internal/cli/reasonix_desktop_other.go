//go:build !windows

package cli

import "errors"

// The current desktop bridge is a Windows integration. Refuse discovery on
// platforms where the Hub cannot verify the process image that owns the PID.
func verifyReasonixDesktopProcess(int) error {
	return errors.New("Reasonix desktop bridge process verification is only supported on Windows")
}
