//go:build windows

package cli

import (
	"fmt"
	"net"
	"os"
	"testing"
)

func TestVerifyCCHahaDesktopBridgeRejectsMissingProcess(t *testing.T) {
	discovery := ccHahahaDesktopBridgeDiscovery{
		Version:   1,
		Endpoint:  "http://127.0.0.1:43123",
		Token:     "valid-bridge-token-123",
		PID:       0xFFFFFFFE,
		BridgePID: 0xFFFFFFFD,
	}
	if err := verifyCCHahaDesktopBridge(discovery); err == nil {
		t.Fatal("bridge with missing desktop owner was accepted")
	}
}

func TestVerifyCCHahaDesktopBridgeAcceptsCurrentProcessTree(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	discovery := ccHahahaDesktopBridgeDiscovery{
		Version:   1,
		Endpoint:  fmt.Sprintf("http://127.0.0.1:%d", port),
		Token:     "valid-bridge-token-123",
		PID:       os.Getppid(),
		BridgePID: os.Getpid(),
	}
	if err := verifyCCHahaDesktopBridge(discovery); err != nil {
		t.Fatalf("current process tree bridge was rejected: %v", err)
	}
}
