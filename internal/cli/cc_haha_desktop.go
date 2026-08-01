package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ccHahahaDesktopBridgeDiscoveryFile = "desktop-collaboration-bridge.json"
	ccHahahaDesktopBridgeHealthPath    = "/v1/collaboration/health"
	ccHahahaDesktopBridgeTurnPath      = "/v1/collaboration/turns"
	ccHahahaDesktopBridgeTimeout       = 20 * time.Second
	ccHahahaDesktopBridgeMaxBody       = 256 << 10
	ccHahahaDesktopConfigDirName       = ".claude"
	ccHahahaDesktopStateDirName        = "cc-haha"

	ccHahahaDesktopBridgeProtocolName  = "desktop-collaboration-bridge"
	ccHahahaDesktopBridgeProtocolMajor = 1
	ccHahahaDesktopBridgeClientName    = "cc-haha"

	ccHahahaDesktopCapabilityVisibleUserTurn     = "visible_user_turn"
	ccHahahaDesktopCapabilityDeliveryIdempotency = "delivery_idempotency"
	ccHahahaDesktopCapabilityProfileReceipt      = "profile_receipt"

	ccHahahaExecutionNormal   = "normal"
	ccHahahaWorkDefault       = "default"
	ccHahahaWorkControlled    = "controlled"
	ccHahahaApprovalAsk       = "ask"
)

type ccHahahaDesktopBridgeDiscovery struct {
	Version   int    `json:"version"`
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
	PID       int    `json:"pid"`
	BridgePID int    `json:"bridge_pid"`
}

type ccHahahaDesktopTurnRequest struct {
	DeliveryID    string `json:"delivery_id"`
	TaskID        string `json:"task_id"`
	WorkspaceRoot string `json:"workspace_root"`
	Prompt        string `json:"prompt"`
	Title         string `json:"title"`
	WorkProfile   string `json:"work_profile,omitempty"`
}

type ccHahahaDesktopTurnResponse struct {
	DeliveryID        string `json:"delivery_id"`
	TaskID            string `json:"task_id"`
	SessionID         string `json:"session_id"`
	State             string `json:"state"`
	CollaborationMode string `json:"collaboration_mode"`
	WorkProfile       string `json:"work_profile"`
	ToolApprovalMode  string `json:"tool_approval_mode"`
}

type ccHahahaDesktopBridgeFailure struct {
	Error      string `json:"error"`
	DeliveryID string `json:"delivery_id"`
	State      string `json:"state"`
}

type ccHahahaDesktopBridgeProtocol struct {
	Name  string `json:"name"`
	Major int    `json:"major"`
	Minor int    `json:"minor"`
}

type ccHahahaDesktopBridgeClient struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
}

type ccHahahaDesktopBridgeHealth struct {
	Status            string                       `json:"status"`
	Protocol          ccHahahaDesktopBridgeProtocol `json:"protocol"`
	Client            ccHahahaDesktopBridgeClient   `json:"client"`
	Capabilities      []string                     `json:"capabilities"`
	CollaborationMode string                       `json:"collaboration_mode"`
	WorkProfile       string                       `json:"work_profile"`
	ToolApprovalMode  string                       `json:"tool_approval_mode"`
}

var errCCHahaDesktopBridgeIncompatible = errors.New("CC-HAHA desktop bridge is incompatible")

type ccHahahaDesktopBridgeIncompatibleError struct {
	reason string
}

// ccHahahaDesktopBridgeDiscoveryPath returns the CC-HAHA desktop discovery
// file. CC-HAHA writes it under ~/.claude/cc-haha, matching the desktop
// client's own config root.
func ccHahahaDesktopBridgeDiscoveryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("user home directory is unavailable")
	}
	return filepath.Join(
		home,
		ccHahahaDesktopConfigDirName,
		ccHahahaDesktopStateDirName,
		ccHahahaDesktopBridgeDiscoveryFile,
	), nil
}

func (e *ccHahahaDesktopBridgeIncompatibleError) Error() string {
	return errCCHahaDesktopBridgeIncompatible.Error() + ": " + e.reason
}

func (e *ccHahahaDesktopBridgeIncompatibleError) Unwrap() error {
	return errCCHahaDesktopBridgeIncompatible
}

// wakeCCHahaDesktop uses the authenticated desktop bridge only. In particular,
// it never probes legacy local-server ports, opens a websocket, or starts a
// headless CLI process as a fallback.
func (n *wakeNotifier) wakeCCHahaDesktop(ctx context.Context, taskID, workspaceRoot, prompt, deliveryID string) error {
	discoveryPath, err := ccHahahaDesktopBridgeDiscoveryPath()
	if err != nil {
		return errors.New("CC-HAHA desktop bridge is unavailable")
	}
	return n.wakeCCHahaDesktopAt(ctx, taskID, workspaceRoot, prompt, deliveryID, discoveryPath, verifyCCHahaDesktopBridge)
}

// wakeCCHahaDesktopAt contains the protocol exchange after production code has
// selected the canonical discovery path and Windows process verifier. Keeping
// those choices outside this helper makes the protocol testable without
// allowing caller-controlled discovery directories in production.
func (n *wakeNotifier) wakeCCHahaDesktopAt(ctx context.Context, taskID, workspaceRoot, prompt, deliveryID, discoveryPath string, verifyBridge func(ccHahahaDesktopBridgeDiscovery) error) error {
	workProfile, err := normalizeCCHahaWorkProfile(n.ccHahahaWorkProfile)
	if err != nil {
		return fmt.Errorf("invalid CC-HAHA work profile: %w", err)
	}
	discovery, err := readCCHahaDesktopBridge(discoveryPath)
	if err != nil {
		return errors.New("CC-HAHA desktop bridge is unavailable; start the patched CC-HAHA desktop app")
	}
	if verifyBridge == nil {
		return errors.New("CC-HAHA desktop bridge process verifier is unavailable")
	}
	if err := verifyBridge(discovery); err != nil {
		return fmt.Errorf("CC-HAHA desktop bridge process identity is invalid: %w", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, ccHahahaDesktopBridgeTimeout)
	defer cancel()
	if _, err := inspectCCHahaDesktopBridge(requestCtx, discovery, workProfile); err != nil {
		return err
	}

	requestBody, err := json.Marshal(ccHahahaDesktopTurnRequest{
		DeliveryID:    deliveryID,
		TaskID:        taskID,
		WorkspaceRoot: workspaceRoot,
		Prompt:        prompt,
		Title:         "CC-HAHA · " + taskID,
		WorkProfile:   workProfile,
	})
	if err != nil {
		return fmt.Errorf("encode CC-HAHA desktop turn: %w", err)
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(discovery.Endpoint, "/")+ccHahahaDesktopBridgeTurnPath, bytes.NewReader(requestBody))
	if err != nil {
		return errors.New("CC-HAHA desktop bridge endpoint is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+discovery.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := ccHahahaDesktopHTTPClient().Do(request)
	if err != nil {
		return uncertainDelivery(errors.New("CC-HAHA desktop bridge did not confirm whether it accepted the turn"))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure ccHahahaDesktopBridgeFailure
		_ = json.NewDecoder(io.LimitReader(response.Body, ccHahahaDesktopBridgeMaxBody)).Decode(&failure)
		// A retry is safe only when the bridge proves this exact delivery was
		// rejected before it could create a visible user turn. All other
		// outcomes are ambiguous and stay behind the existing human decision.
		if response.StatusCode == http.StatusConflict && failure.DeliveryID == deliveryID && failure.State == "rejected_before_delivery" && isCCHahaPreDeliveryFailure(failure.Error) {
			return fmt.Errorf("CC-HAHA desktop bridge rejected delivery before submission: %s", failure.Error)
		}
		return uncertainDelivery(fmt.Errorf("CC-HAHA desktop bridge did not confirm delivery (%s)", response.Status))
	}

	var result ccHahahaDesktopTurnResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, ccHahahaDesktopBridgeMaxBody))
	if err := decoder.Decode(&result); err != nil {
		return uncertainDelivery(errors.New("CC-HAHA desktop bridge returned an invalid acknowledgement"))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return uncertainDelivery(errors.New("CC-HAHA desktop bridge acknowledgement contains trailing data"))
	}
	if result.DeliveryID != deliveryID || result.TaskID != taskID || strings.TrimSpace(result.SessionID) == "" || result.State != "accepted" {
		return uncertainDelivery(errors.New("CC-HAHA desktop bridge did not confirm the visible task conversation"))
	}
	if result.CollaborationMode != ccHahahaExecutionNormal || result.WorkProfile != workProfile || result.ToolApprovalMode != ccHahahaApprovalAsk {
		return uncertainDelivery(fmt.Errorf("CC-HAHA desktop bridge did not confirm normal/%s/ask", workProfile))
	}
	return nil
}

// inspectCCHahaDesktopBridge completes authenticated health negotiation before
// any turn is sent. An unavailable or incompatible health response is never a
// delivery attempt and therefore is deliberately not marked uncertain.
func inspectCCHahaDesktopBridge(ctx context.Context, discovery ccHahahaDesktopBridgeDiscovery, expectedWorkProfile string) (ccHahahaDesktopBridgeHealth, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(discovery.Endpoint, "/")+ccHahahaDesktopBridgeHealthPath, nil)
	if err != nil {
		return ccHahahaDesktopBridgeHealth{}, errors.New("CC-HAHA desktop bridge health endpoint is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+discovery.Token)
	response, err := ccHahahaDesktopHTTPClient().Do(request)
	if err != nil {
		return ccHahahaDesktopBridgeHealth{}, errors.New("CC-HAHA desktop bridge health check failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ccHahahaDesktopBridgeHealth{}, fmt.Errorf("CC-HAHA desktop bridge health check returned %s", response.Status)
	}
	var health ccHahahaDesktopBridgeHealth
	decoder := json.NewDecoder(io.LimitReader(response.Body, ccHahahaDesktopBridgeMaxBody))
	if err := decoder.Decode(&health); err != nil {
		return ccHahahaDesktopBridgeHealth{}, errors.New("CC-HAHA desktop bridge returned an invalid health response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ccHahahaDesktopBridgeHealth{}, errors.New("CC-HAHA desktop bridge health response contains trailing data")
	}
	if err := validateCCHahaDesktopBridgeHealth(health, expectedWorkProfile); err != nil {
		return health, err
	}
	return health, nil
}

func validateCCHahaDesktopBridgeHealth(health ccHahahaDesktopBridgeHealth, expectedWorkProfile string) error {
	if health.Status != "ok" {
		return incompatibleCCHahaDesktopBridge("health status is not ok")
	}
	if health.Protocol.Name != ccHahahaDesktopBridgeProtocolName {
		return incompatibleCCHahaDesktopBridge("protocol name does not match")
	}
	if health.Protocol.Major != ccHahahaDesktopBridgeProtocolMajor {
		return incompatibleCCHahaDesktopBridge(fmt.Sprintf("protocol major %d is unsupported", health.Protocol.Major))
	}
	if health.Protocol.Minor < 0 {
		return incompatibleCCHahaDesktopBridge("protocol minor is invalid")
	}
	if health.Client.Name != ccHahahaDesktopBridgeClientName {
		return incompatibleCCHahaDesktopBridge("client name does not match")
	}
	if strings.TrimSpace(health.Client.Version) == "" || strings.TrimSpace(health.Client.Build) == "" {
		return incompatibleCCHahaDesktopBridge("client identity is incomplete")
	}
	capabilities := make(map[string]bool, len(health.Capabilities))
	for _, capability := range health.Capabilities {
		capabilities[strings.TrimSpace(capability)] = true
	}
	for _, required := range []string{
		ccHahahaDesktopCapabilityVisibleUserTurn,
		ccHahahaDesktopCapabilityDeliveryIdempotency,
		ccHahahaDesktopCapabilityProfileReceipt,
	} {
		if !capabilities[required] {
			return incompatibleCCHahaDesktopBridge("required capability " + required + " is unavailable")
		}
	}
	if health.CollaborationMode != ccHahahaExecutionNormal || health.WorkProfile != expectedWorkProfile || health.ToolApprovalMode != ccHahahaApprovalAsk {
		return incompatibleCCHahaDesktopBridge(fmt.Sprintf("health did not confirm normal/%s/ask", expectedWorkProfile))
	}
	return nil
}

func incompatibleCCHahaDesktopBridge(reason string) error {
	return &ccHahahaDesktopBridgeIncompatibleError{reason: reason}
}

func isCCHahaPreDeliveryFailure(code string) bool {
	switch strings.TrimSpace(code) {
	case "unsupported_work_profile", "workspace_root_must_be_absolute", "workspace_root_unavailable", "delivery_id_conflict", "session_state_unknown", "task_session_workspace_conflict", "task_session_permission_conflict", "active_session_permission_conflict", "bridge_unavailable":
		return true
	default:
		return false
	}
}

func normalizeCCHahaWorkProfile(workProfile string) (string, error) {
	switch strings.TrimSpace(workProfile) {
	case "", ccHahahaWorkDefault:
		return ccHahahaWorkDefault, nil
	case ccHahahaWorkControlled:
		return ccHahahaWorkControlled, nil
	default:
		return "", errors.New("must be default or controlled")
	}
}

func ccHahahaDesktopHTTPClient() *http.Client {
	return &http.Client{
		Timeout: ccHahahaDesktopBridgeTimeout,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func readCCHahaDesktopBridge(path string) (ccHahahaDesktopBridgeDiscovery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ccHahahaDesktopBridgeDiscovery{}, err
	}
	if len(data) == 0 || len(data) > ccHahahaDesktopBridgeMaxBody {
		return ccHahahaDesktopBridgeDiscovery{}, errors.New("bridge discovery is invalid")
	}
	var discovery ccHahahaDesktopBridgeDiscovery
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&discovery); err != nil {
		return ccHahahaDesktopBridgeDiscovery{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ccHahahaDesktopBridgeDiscovery{}, errors.New("bridge discovery contains trailing data")
	}
	if err := validateCCHahaDesktopBridgeDiscovery(discovery); err != nil {
		return ccHahahaDesktopBridgeDiscovery{}, err
	}
	return discovery, nil
}

func validateCCHahaDesktopBridgeDiscovery(discovery ccHahahaDesktopBridgeDiscovery) error {
	if discovery.Version != 1 || !validCCHahaDesktopPID(discovery.PID) || !validCCHahaDesktopPID(discovery.BridgePID) || discovery.PID == discovery.BridgePID {
		return errors.New("bridge discovery identity is invalid")
	}
	if _, err := ccHahahaDesktopEndpointPort(discovery.Endpoint); err != nil {
		return errors.New("bridge endpoint must be authenticated loopback HTTP")
	}
	if !validCCHahaDesktopToken(discovery.Token) {
		return errors.New("bridge token is invalid")
	}
	return nil
}

func ccHahahaDesktopEndpointPort(rawEndpoint string) (uint16, error) {
	endpoint, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil {
		return 0, err
	}
	if endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return 0, errors.New("endpoint is not a loopback bridge")
	}
	port, err := strconv.ParseUint(endpoint.Port(), 10, 16)
	if err != nil || port == 0 {
		return 0, errors.New("endpoint port is invalid")
	}
	return uint16(port), nil
}

func validCCHahaDesktopPID(pid int) bool {
	return pid > 0 && uint64(pid) <= uint64(^uint32(0))
}

func validCCHahaDesktopToken(token string) bool {
	if len(token) < 16 || len(token) > 256 || strings.TrimSpace(token) != token {
		return false
	}
	for _, value := range token {
		if (value < 'a' || value > 'z') && (value < 'A' || value > 'Z') && (value < '0' || value > '9') && value != '-' && value != '_' {
			return false
		}
	}
	return true
}
