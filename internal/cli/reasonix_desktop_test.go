package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWakeReasonixDesktopUsesDiscoveredVisibleBridge(t *testing.T) {
	stateHome := t.TempDir()
	workspace := t.TempDir()
	var received reasonixDesktopTurnRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == reasonixDesktopBridgeHealthPath {
			if r.Header.Get("Authorization") != "Bearer bridge-token" {
				t.Fatalf("health authorization = %q", r.Header.Get("Authorization"))
			}
			writeHealthyReasonixDesktopBridgeHealth(w)
			return
		}
		if r.URL.Path != reasonixDesktopBridgeTurnPath {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer bridge-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(reasonixDesktopTurnResponse{
			DeliveryID:        received.DeliveryID,
			TaskID:            received.TaskID,
			TabID:             "tab-visible",
			TopicID:           "collaboration-topic",
			CollaborationMode: reasonixExecutionNormal,
			WorkProfile:       reasonixWorkDelivery,
			ToolApprovalMode:  reasonixApprovalAuto,
		})
	}))
	defer server.Close()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{
		Version:  1,
		Endpoint: server.URL,
		Token:    "bridge-token",
		PID:      1,
	})

	notifier := &wakeNotifier{app: NewApp(t.TempDir(), nil, nil, nil)}
	if err := wakeReasonixDesktopForTest(notifier, "T-RE-VISIBLE", workspace, "请审查候选", "delivery-visible-42", stateHome); err != nil {
		t.Fatalf("wake Reasonix desktop: %v", err)
	}
	if received.DeliveryID != "delivery-visible-42" || received.TaskID != "T-RE-VISIBLE" || received.WorkspaceRoot != workspace || received.Prompt != "请审查候选" {
		t.Fatalf("request = %+v", received)
	}
	if received.WorkProfile != reasonixWorkDelivery {
		t.Fatalf("work profile = %q, want %q", received.WorkProfile, reasonixWorkDelivery)
	}
	if !strings.Contains(received.Title, "T-RE-VISIBLE") {
		t.Fatalf("title = %q, want task id", received.Title)
	}
}

func TestWakeReasonixDesktopPassesSelectedWorkProfile(t *testing.T) {
	cases := []struct {
		name           string
		receiptProfile string
		wantUncertain  bool
	}{
		{name: "matching receipt", receiptProfile: reasonixWorkBalanced},
		{name: "mismatched receipt", receiptProfile: reasonixWorkDelivery, wantUncertain: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateHome := t.TempDir()
			workspace := t.TempDir()
			var received reasonixDesktopTurnRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == reasonixDesktopBridgeHealthPath {
					writeHealthyReasonixDesktopBridgeHealth(w)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				_ = json.NewEncoder(w).Encode(reasonixDesktopTurnResponse{
					DeliveryID:        received.DeliveryID,
					TaskID:            received.TaskID,
					TabID:             "tab-visible",
					TopicID:           "collaboration-topic",
					CollaborationMode: reasonixExecutionNormal,
					WorkProfile:       tc.receiptProfile,
					ToolApprovalMode:  reasonixApprovalAuto,
				})
			}))
			defer server.Close()
			writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{
				Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1,
			})

			notifier := &wakeNotifier{
				app:                 NewApp(t.TempDir(), nil, nil, nil),
				reasonixWorkProfile: reasonixWorkBalanced,
			}
			err := wakeReasonixDesktopForTest(notifier, "T-RE-BALANCED", workspace, "请审查候选", "delivery-balanced-42", stateHome)
			if received.WorkProfile != reasonixWorkBalanced {
				t.Fatalf("request work profile = %q, want %q", received.WorkProfile, reasonixWorkBalanced)
			}
			if tc.wantUncertain {
				if !isUncertainDelivery(err) {
					t.Fatalf("error = %v, want unconfirmed delivery", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("wake Reasonix desktop: %v", err)
			}
		})
	}
}

func TestReadReasonixDesktopBridgeRejectsNonLoopbackEndpoint(t *testing.T) {
	stateHome := t.TempDir()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{
		Version:  1,
		Endpoint: "http://example.com:3000",
		Token:    "bridge-token",
	})
	if _, err := readReasonixDesktopBridge(filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile)); err == nil {
		t.Fatal("non-loopback bridge endpoint was accepted")
	}
}

func TestWakeReasonixDesktopTreatsMismatchedReceiptAsUncertain(t *testing.T) {
	stateHome := t.TempDir()
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == reasonixDesktopBridgeHealthPath {
			writeHealthyReasonixDesktopBridgeHealth(w)
			return
		}
		_ = json.NewEncoder(w).Encode(reasonixDesktopTurnResponse{
			DeliveryID:        "delivery-other-42",
			TaskID:            "T-RE-VISIBLE",
			TabID:             "tab-visible",
			TopicID:           "collaboration-topic",
			CollaborationMode: reasonixExecutionNormal,
			WorkProfile:       reasonixWorkDelivery,
			ToolApprovalMode:  reasonixApprovalAuto,
		})
	}))
	defer server.Close()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{
		Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1,
	})

	notifier := &wakeNotifier{app: NewApp(t.TempDir(), nil, nil, nil)}
	err := wakeReasonixDesktopForTest(notifier, "T-RE-VISIBLE", workspace, "请审查候选", "delivery-visible-42", stateHome)
	if !isUncertainDelivery(err) {
		t.Fatalf("error = %v, want unconfirmed delivery", err)
	}
}

func TestReadReasonixDesktopBridgeRejectsUnsupportedVersion(t *testing.T) {
	stateHome := t.TempDir()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{
		Version:  2,
		Endpoint: "http://127.0.0.1:3000",
		Token:    "bridge-token",
		PID:      1,
	})
	if _, err := readReasonixDesktopBridge(filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile)); err == nil {
		t.Fatal("unsupported bridge discovery version was accepted")
	}
}

func TestReadReasonixDesktopBridgeRejectsTrailingData(t *testing.T) {
	stateHome := t.TempDir()
	path := filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile)
	data := []byte(`{"version":1,"endpoint":"http://127.0.0.1:3000","token":"bridge-token","pid":1}{}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write discovery: %v", err)
	}
	if _, err := readReasonixDesktopBridge(path); err == nil {
		t.Fatal("trailing discovery data was accepted")
	}
}

func TestReasonixDesktopStateHomeIgnoresArbitraryStateOverride(t *testing.T) {
	override := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", override)
	t.Setenv("REASONIX_HOME", override)
	stateHome, err := reasonixDesktopStateHome()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(stateHome) == filepath.Clean(override) {
		t.Fatalf("state home = %q, accepted arbitrary environment override %q", stateHome, override)
	}
}

func TestWakeReasonixDesktopFailsClosedForHTTPResponseWithoutPreDeliveryReceipt(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      reasonixDesktopBridgeFailure
		uncertain bool
	}{
		{
			name:   "matching pre-delivery rejection",
			status: http.StatusConflict,
			body:   reasonixDesktopBridgeFailure{Error: "task_busy", DeliveryID: "delivery-http-42", State: "rejected_before_delivery"},
		},
		{
			name:      "missing explicit state",
			status:    http.StatusConflict,
			body:      reasonixDesktopBridgeFailure{Error: "task_busy", DeliveryID: "delivery-http-42"},
			uncertain: true,
		},
		{
			name:      "different delivery id",
			status:    http.StatusConflict,
			body:      reasonixDesktopBridgeFailure{Error: "task_busy", DeliveryID: "delivery-other-42", State: "rejected_before_delivery"},
			uncertain: true,
		},
		{
			name:      "unknown pre-delivery error code",
			status:    http.StatusConflict,
			body:      reasonixDesktopBridgeFailure{Error: "unsupported", DeliveryID: "delivery-http-42", State: "rejected_before_delivery"},
			uncertain: true,
		},
		{
			name:      "non-conflict status",
			status:    http.StatusServiceUnavailable,
			body:      reasonixDesktopBridgeFailure{Error: "task_busy", DeliveryID: "delivery-http-42", State: "rejected_before_delivery"},
			uncertain: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateHome := t.TempDir()
			workspace := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == reasonixDesktopBridgeHealthPath {
					writeHealthyReasonixDesktopBridgeHealth(w)
					return
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer server.Close()
			writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1})

			notifier := &wakeNotifier{app: NewApp(t.TempDir(), nil, nil, nil)}
			err := wakeReasonixDesktopForTest(notifier, "T-RE-HTTP", workspace, "请审查候选", "delivery-http-42", stateHome)
			if tc.uncertain {
				if !isUncertainDelivery(err) {
					t.Fatalf("error = %v, want uncertain delivery", err)
				}
				return
			}
			if err == nil || isUncertainDelivery(err) {
				t.Fatalf("error = %v, want explicit pre-delivery rejection", err)
			}
		})
	}
}

func TestWakeReasonixDesktopRejectsUnverifiedBridgeProcess(t *testing.T) {
	stateHome := t.TempDir()
	workspace := t.TempDir()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1})

	notifier := &wakeNotifier{app: NewApp(t.TempDir(), nil, nil, nil)}
	err := notifier.wakeReasonixDesktopAt(context.Background(), "T-RE-PID", workspace, "请审查候选", "delivery-pid-42", filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile), func(int) error {
		return errors.New("not reasonix")
	})
	if err == nil || isUncertainDelivery(err) {
		t.Fatalf("error = %v, want process identity rejection", err)
	}
	if requests != 0 {
		t.Fatalf("bridge was contacted before process verification: %d requests", requests)
	}
}

func TestWakeReasonixDesktopRejectsIncompatibleHealthBeforeTurn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*reasonixDesktopBridgeHealth)
	}{
		{
			name: "protocol major",
			mutate: func(health *reasonixDesktopBridgeHealth) {
				health.Protocol.Major = reasonixDesktopBridgeProtocolMajor + 1
			},
		},
		{
			name: "required capability",
			mutate: func(health *reasonixDesktopBridgeHealth) {
				health.Capabilities = []string{reasonixDesktopCapabilityVisibleUserTurn, reasonixDesktopCapabilityProfileReceipt}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateHome := t.TempDir()
			workspace := t.TempDir()
			turnRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == reasonixDesktopBridgeHealthPath {
					health := healthyReasonixDesktopBridgeHealth()
					tc.mutate(&health)
					_ = json.NewEncoder(w).Encode(health)
					return
				}
				if r.URL.Path == reasonixDesktopBridgeTurnPath {
					turnRequests++
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1})

			err := wakeReasonixDesktopForTest(&wakeNotifier{app: NewApp(t.TempDir(), nil, nil, nil)}, "T-RE-INCOMPATIBLE", workspace, "请审查候选", "delivery-incompatible-42", stateHome)
			if !errors.Is(err, errReasonixDesktopBridgeIncompatible) {
				t.Fatalf("error = %v, want incompatible bridge", err)
			}
			if turnRequests != 0 {
				t.Fatalf("turn requests = %d, want 0", turnRequests)
			}
		})
	}
}

func TestReasonixAdapterDoctorReportsCompatibleBridgeWithoutPostingTurn(t *testing.T) {
	stateHome := t.TempDir()
	turnRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == reasonixDesktopBridgeHealthPath {
			writeHealthyReasonixDesktopBridgeHealth(w)
			return
		}
		if r.URL.Path == reasonixDesktopBridgeTurnPath {
			turnRequests++
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1})

	result, err := reasonixAdapterDoctorAt(context.Background(), filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile), func(int) error { return nil })
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if result.Status != "COMPATIBLE" || result.Protocol == nil || result.Protocol.Major != reasonixDesktopBridgeProtocolMajor || result.ClientInfo == nil || result.ClientInfo.Name != reasonixDesktopBridgeClientName {
		t.Fatalf("doctor result = %+v", result)
	}
	if turnRequests != 0 {
		t.Fatalf("turn requests = %d, want 0", turnRequests)
	}
}

func TestReasonixAdapterDoctorReportsIncompatibleBridgeWithoutPostingTurn(t *testing.T) {
	stateHome := t.TempDir()
	turnRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == reasonixDesktopBridgeHealthPath {
			health := healthyReasonixDesktopBridgeHealth()
			health.Protocol.Major = reasonixDesktopBridgeProtocolMajor + 1
			_ = json.NewEncoder(w).Encode(health)
			return
		}
		if r.URL.Path == reasonixDesktopBridgeTurnPath {
			turnRequests++
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{Version: 1, Endpoint: server.URL, Token: "bridge-token", PID: 1})

	result, err := reasonixAdapterDoctorAt(context.Background(), filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile), func(int) error { return nil })
	if !errors.Is(err, errReasonixDesktopBridgeIncompatible) {
		t.Fatalf("doctor error = %v, want incompatible bridge", err)
	}
	if result.Status != "INCOMPATIBLE" || result.Protocol == nil || result.Protocol.Major != reasonixDesktopBridgeProtocolMajor+1 || result.Reason == "" {
		t.Fatalf("doctor result = %+v", result)
	}
	if turnRequests != 0 {
		t.Fatalf("turn requests = %d, want 0", turnRequests)
	}
}

func TestReasonixAdapterDoctorReportsIncompatibleProcessTrust(t *testing.T) {
	stateHome := t.TempDir()
	writeReasonixDesktopDiscovery(t, stateHome, reasonixDesktopBridgeDiscovery{Version: 1, Endpoint: "http://127.0.0.1:43123", Token: "bridge-token", PID: 1})

	result, err := reasonixAdapterDoctorAt(context.Background(), filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile), func(int) error {
		return incompatibleReasonixDesktopBridge("desktop executable Authenticode signature is not trusted")
	})
	if !errors.Is(err, errReasonixDesktopBridgeIncompatible) {
		t.Fatalf("doctor error = %v, want incompatible bridge", err)
	}
	if result.Status != "INCOMPATIBLE" || result.Reason == "" {
		t.Fatalf("doctor result = %+v", result)
	}
}

func TestAdapterDoctorAcceptsOnlyReasonixOrAll(t *testing.T) {
	app := NewApp(t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{}, nil)
	for _, client := range []string{"cc-haha", "codex", ""} {
		if code := app.Run([]string{"adapter", "doctor", "--client", client}); code != ExitValidation {
			t.Fatalf("client %q exit code = %d, want %d", client, code, ExitValidation)
		}
	}
}

func wakeReasonixDesktopForTest(n *wakeNotifier, taskID, workspaceRoot, prompt, deliveryID, discoveryPath string) error {
	return n.wakeReasonixDesktopAt(context.Background(), taskID, workspaceRoot, prompt, deliveryID, filepath.Join(discoveryPath, reasonixDesktopBridgeDiscoveryFile), func(int) error { return nil })
}

func writeReasonixDesktopDiscovery(t *testing.T, stateHome string, discovery reasonixDesktopBridgeDiscovery) {
	t.Helper()
	data, err := json.Marshal(discovery)
	if err != nil {
		t.Fatalf("marshal discovery: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile), data, 0o600); err != nil {
		t.Fatalf("write discovery: %v", err)
	}
}

func healthyReasonixDesktopBridgeHealth() reasonixDesktopBridgeHealth {
	return reasonixDesktopBridgeHealth{
		Status: "ok",
		Protocol: reasonixDesktopBridgeProtocol{
			Name:  reasonixDesktopBridgeProtocolName,
			Major: reasonixDesktopBridgeProtocolMajor,
			Minor: 0,
		},
		Client: reasonixDesktopBridgeClient{Name: reasonixDesktopBridgeClientName, Version: "test", Build: "test"},
		Capabilities: []string{
			reasonixDesktopCapabilityVisibleUserTurn,
			reasonixDesktopCapabilityDeliveryIdempotency,
			reasonixDesktopCapabilityProfileReceipt,
		},
		CollaborationMode: reasonixExecutionNormal,
		WorkProfile:       reasonixWorkDelivery,
		ToolApprovalMode:  reasonixApprovalAuto,
	}
}

func writeHealthyReasonixDesktopBridgeHealth(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(healthyReasonixDesktopBridgeHealth())
}
