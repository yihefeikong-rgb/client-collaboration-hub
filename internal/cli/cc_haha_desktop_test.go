package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCCHahaDesktopDiscovery(t *testing.T, dir string, discovery ccHahahaDesktopBridgeDiscovery) string {
	t.Helper()
	path := filepath.Join(dir, ccHahahaDesktopBridgeDiscoveryFile)
	data, err := json.Marshal(discovery)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeHealthyCCHahaDesktopBridgeHealth(w http.ResponseWriter, workProfile string) {
	_ = json.NewEncoder(w).Encode(ccHahahaDesktopBridgeHealth{
		Status: "ok",
		Protocol: ccHahahaDesktopBridgeProtocol{
			Name:  ccHahahaDesktopBridgeProtocolName,
			Major: ccHahahaDesktopBridgeProtocolMajor,
			Minor: 0,
		},
		Client: ccHahahaDesktopBridgeClient{
			Name:    ccHahahaDesktopBridgeClientName,
			Version: "1.2.3-test",
			Build:   "test-build",
		},
		Capabilities: []string{
			ccHahahaDesktopCapabilityVisibleUserTurn,
			ccHahahaDesktopCapabilityDeliveryIdempotency,
			ccHahahaDesktopCapabilityProfileReceipt,
		},
		CollaborationMode: ccHahahaExecutionNormal,
		WorkProfile:       workProfile,
		ToolApprovalMode:  ccHahahaApprovalAsk,
	})
}

func TestCCHahaDesktopBridgeDiscoveryPathLayout(t *testing.T) {
	path, err := ccHahahaDesktopBridgeDiscoveryPath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) ||
		!strings.Contains(path, ccHahahaDesktopConfigDirName) ||
		!strings.Contains(path, ccHahahaDesktopStateDirName) ||
		filepath.Base(path) != ccHahahaDesktopBridgeDiscoveryFile {
		t.Fatalf("discovery path = %q", path)
	}
}

func TestReadCCHahaDesktopBridgeRejectsInvalidDiscovery(t *testing.T) {
	dir := t.TempDir()
	valid := ccHahahaDesktopBridgeDiscovery{
		Version:   1,
		Endpoint:  "http://127.0.0.1:43123",
		Token:     "valid-bridge-token-123",
		PID:       100,
		BridgePID: 200,
	}
	t.Run("non-loopback endpoint", func(t *testing.T) {
		bad := valid
		bad.Endpoint = "http://example.com:3000"
		path := writeCCHahaDesktopDiscovery(t, dir, bad)
		if _, err := readCCHahaDesktopBridge(path); err == nil {
			t.Fatal("non-loopback endpoint was accepted")
		}
	})
	t.Run("same pid and bridge pid", func(t *testing.T) {
		bad := valid
		bad.BridgePID = bad.PID
		path := writeCCHahaDesktopDiscovery(t, dir, bad)
		if _, err := readCCHahaDesktopBridge(path); err == nil {
			t.Fatal("identical owner and bridge PID was accepted")
		}
	})
	t.Run("invalid token", func(t *testing.T) {
		bad := valid
		bad.Token = "short"
		path := writeCCHahaDesktopDiscovery(t, dir, bad)
		if _, err := readCCHahaDesktopBridge(path); err == nil {
			t.Fatal("invalid token was accepted")
		}
	})
	t.Run("unknown discovery field", func(t *testing.T) {
		path := filepath.Join(dir, "unknown.json")
		if err := os.WriteFile(path, []byte(`{"version":1,"endpoint":"http://127.0.0.1:43123","token":"valid-bridge-token-123","pid":100,"bridge_pid":200,"extra":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCCHahaDesktopBridge(path); err == nil {
			t.Fatal("unknown discovery field was accepted")
		}
	})
}

func TestNormalizeCCHahaWorkProfile(t *testing.T) {
	for _, value := range []string{"", ccHahahaWorkDefault, " default ", ccHahahaWorkControlled, "controlled"} {
		if _, err := normalizeCCHahaWorkProfile(value); err != nil {
			t.Fatalf("normalizeCCHahaWorkProfile(%q) failed: %v", value, err)
		}
	}
	for _, value := range []string{"yolo", "delivery", "balanced"} {
		if _, err := normalizeCCHahaWorkProfile(value); err == nil {
			t.Fatalf("normalizeCCHahaWorkProfile(%q) accepted an invalid profile", value)
		}
	}
}

func TestValidateCCHahaDesktopBridgeHealthParametric(t *testing.T) {
	base := ccHahahaDesktopBridgeHealth{
		Status: "ok",
		Protocol: ccHahahaDesktopBridgeProtocol{
			Name:  ccHahahaDesktopBridgeProtocolName,
			Major: ccHahahaDesktopBridgeProtocolMajor,
		},
		Client: ccHahahaDesktopBridgeClient{
			Name:    ccHahahaDesktopBridgeClientName,
			Version: "1.0.0",
			Build:   "build",
		},
		Capabilities: []string{
			ccHahahaDesktopCapabilityVisibleUserTurn,
			ccHahahaDesktopCapabilityDeliveryIdempotency,
			ccHahahaDesktopCapabilityProfileReceipt,
		},
		CollaborationMode: ccHahahaExecutionNormal,
		ToolApprovalMode:  ccHahahaApprovalAsk,
	}
	for _, profile := range []string{ccHahahaWorkDefault, ccHahahaWorkControlled} {
		health := base
		health.WorkProfile = profile
		if err := validateCCHahaDesktopBridgeHealth(health, profile); err != nil {
			t.Fatalf("validate %s failed: %v", profile, err)
		}
	}
	health := base
	health.WorkProfile = ccHahahaWorkDefault
	if err := validateCCHahaDesktopBridgeHealth(health, ccHahahaWorkControlled); err == nil {
		t.Fatal("health with default profile passed controlled validation")
	}
}

func TestWakeCCHahaDesktopUsesDiscoveredVisibleBridge(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	var received ccHahahaDesktopTurnRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bridge-token-12345678" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path == ccHahahaDesktopBridgeHealthPath {
			writeHealthyCCHahaDesktopBridgeHealth(w, ccHahahaWorkControlled)
			return
		}
		if r.URL.Path != ccHahahaDesktopBridgeTurnPath {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(ccHahahaDesktopTurnResponse{
			DeliveryID:        received.DeliveryID,
			TaskID:            received.TaskID,
			SessionID:         "stable-session",
			State:             "accepted",
			CollaborationMode: ccHahahaExecutionNormal,
			WorkProfile:       ccHahahaWorkControlled,
			ToolApprovalMode:  ccHahahaApprovalAsk,
		})
	}))
	defer server.Close()
	path := writeCCHahaDesktopDiscovery(t, dir, ccHahahaDesktopBridgeDiscovery{
		Version:   1,
		Endpoint:  server.URL,
		Token:     "bridge-token-12345678",
		PID:       100,
		BridgePID: 200,
	})

	notifier := &wakeNotifier{
		app:                 NewApp(t.TempDir(), nil, nil, nil),
		ccHahahaWorkProfile: ccHahahaWorkControlled,
	}
	err := notifier.wakeCCHahaDesktopAt(
		context.Background(),
		"T-CC-VISIBLE",
		workspace,
		"请处理任务",
		"delivery-cc-42",
		path,
		func(ccHahahaDesktopBridgeDiscovery) error { return nil },
	)
	if err != nil {
		t.Fatalf("wake CC-HAHA desktop: %v", err)
	}
	if received.DeliveryID != "delivery-cc-42" || received.TaskID != "T-CC-VISIBLE" || received.WorkspaceRoot != workspace || received.Prompt != "请处理任务" {
		t.Fatalf("request = %+v", received)
	}
	if received.WorkProfile != ccHahahaWorkControlled {
		t.Fatalf("work profile = %q, want %q", received.WorkProfile, ccHahahaWorkControlled)
	}
}

func TestWakeCCHahaDesktopTreatsMismatchedReceiptAsUncertain(t *testing.T) {
	dir := t.TempDir()
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ccHahahaDesktopBridgeHealthPath {
			writeHealthyCCHahaDesktopBridgeHealth(w, ccHahahaWorkDefault)
			return
		}
		var received ccHahahaDesktopTurnRequest
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(ccHahahaDesktopTurnResponse{
			DeliveryID:        received.DeliveryID,
			TaskID:            received.TaskID,
			SessionID:         "stable-session",
			State:             "accepted",
			CollaborationMode: ccHahahaExecutionNormal,
			WorkProfile:       ccHahahaWorkControlled,
			ToolApprovalMode:  ccHahahaApprovalAsk,
		})
	}))
	defer server.Close()
	path := writeCCHahaDesktopDiscovery(t, dir, ccHahahaDesktopBridgeDiscovery{
		Version:   1,
		Endpoint:  server.URL,
		Token:     "bridge-token-12345678",
		PID:       100,
		BridgePID: 200,
	})

	notifier := &wakeNotifier{
		app:                 NewApp(t.TempDir(), nil, nil, nil),
		ccHahahaWorkProfile: ccHahahaWorkDefault,
	}
	err := notifier.wakeCCHahaDesktopAt(
		context.Background(),
		"T-CC-MISMATCH",
		workspace,
		"请处理任务",
		"delivery-cc-43",
		path,
		func(ccHahahaDesktopBridgeDiscovery) error { return nil },
	)
	if !isUncertainDelivery(err) {
		t.Fatalf("error = %v, want unconfirmed delivery", err)
	}
}
