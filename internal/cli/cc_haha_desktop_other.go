//go:build !windows

package cli

import "errors"

// The CC-HAHA desktop bridge is currently a Windows integration. Refuse
// discovery on other platforms because the Hub cannot verify the process
// identity that owns the bridge PID.
func verifyCCHahaDesktopBridge(ccHahahaDesktopBridgeDiscovery) error {
	return errors.New("CC-HAHA desktop bridge process verification is only supported on Windows")
}
