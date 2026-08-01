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
	"runtime"
	"strings"
	"time"
)

const (
	reasonixDesktopBridgeDiscoveryFile = "desktop-collaboration-bridge.json"
	reasonixDesktopBridgeHealthPath    = "/v1/collaboration/health"
	reasonixDesktopBridgeTurnPath      = "/v1/collaboration/turns"
	reasonixDesktopBridgeTimeout       = 20 * time.Second
	reasonixDesktopBridgeMaxBody       = 256 << 10

	reasonixDesktopBridgeProtocolName  = "desktop-collaboration-bridge"
	reasonixDesktopBridgeProtocolMajor = 1
	reasonixDesktopBridgeClientName    = "reasonix"

	reasonixDesktopCapabilityVisibleUserTurn     = "visible_user_turn"
	reasonixDesktopCapabilityDeliveryIdempotency = "delivery_idempotency"
	reasonixDesktopCapabilityProfileReceipt      = "profile_receipt"

	reasonixExecutionNormal = "normal"
	reasonixWorkBalanced    = "balanced"
	reasonixWorkDelivery    = "delivery"
	reasonixApprovalAuto    = "auto"
	reasonixApprovalYolo    = "yolo"
)

type reasonixDesktopBridgeDiscovery struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
}

type reasonixDesktopTurnRequest struct {
	DeliveryID    string `json:"delivery_id"`
	TaskID        string `json:"task_id"`
	WorkspaceRoot string `json:"workspace_root"`
	Prompt        string `json:"prompt"`
	Title         string `json:"title"`
	WorkProfile   string `json:"work_profile,omitempty"`
}

type reasonixDesktopTurnResponse struct {
	DeliveryID        string `json:"delivery_id"`
	TaskID            string `json:"task_id"`
	TabID             string `json:"tab_id"`
	TopicID           string `json:"topic_id"`
	CollaborationMode string `json:"collaboration_mode"`
	WorkProfile       string `json:"work_profile"`
	ToolApprovalMode  string `json:"tool_approval_mode"`
}

type reasonixDesktopBridgeFailure struct {
	Error      string `json:"error"`
	DeliveryID string `json:"delivery_id"`
	State      string `json:"state"`
}

type reasonixDesktopBridgeProtocol struct {
	Name  string `json:"name"`
	Major int    `json:"major"`
	Minor int    `json:"minor"`
}

type reasonixDesktopBridgeClient struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
}

type reasonixDesktopBridgeHealth struct {
	Status            string                        `json:"status"`
	Protocol          reasonixDesktopBridgeProtocol `json:"protocol"`
	Client            reasonixDesktopBridgeClient   `json:"client"`
	Capabilities      []string                      `json:"capabilities"`
	CollaborationMode string                        `json:"collaboration_mode"`
	WorkProfile       string                        `json:"work_profile"`
	ToolApprovalMode  string                        `json:"tool_approval_mode"`
}

var errReasonixDesktopBridgeIncompatible = errors.New("Reasonix desktop bridge is incompatible")

type reasonixDesktopBridgeIncompatibleError struct {
	reason string
}

func (e *reasonixDesktopBridgeIncompatibleError) Error() string {
	return errReasonixDesktopBridgeIncompatible.Error() + ": " + e.reason
}

func (e *reasonixDesktopBridgeIncompatibleError) Unwrap() error {
	return errReasonixDesktopBridgeIncompatible
}

func (n *wakeNotifier) wakeReasonixDesktop(ctx context.Context, taskID, workspaceRoot, prompt, deliveryID string) error {
	discoveryPath, err := reasonixDesktopBridgeDiscoveryPath()
	if err != nil {
		return errors.New("cannot locate the Reasonix desktop bridge")
	}
	return n.wakeReasonixDesktopAt(ctx, taskID, workspaceRoot, prompt, deliveryID, discoveryPath, verifyReasonixDesktopProcess)
}

// wakeReasonixDesktopAt contains the bridge exchange after production code has
// selected the canonical discovery path and process verifier. Keeping those
// decisions outside this helper lets the protocol be tested without treating a
// caller-controlled directory as a production discovery source.
func (n *wakeNotifier) wakeReasonixDesktopAt(ctx context.Context, taskID, workspaceRoot, prompt, deliveryID, discoveryPath string, verifyProcess func(int) error) error {
	workProfile, err := normalizeReasonixWorkProfile(n.reasonixWorkProfile)
	if err != nil {
		return fmt.Errorf("invalid Reasonix work profile: %w", err)
	}
	discovery, err := readReasonixDesktopBridge(discoveryPath)
	if err != nil {
		return errors.New("Reasonix desktop bridge is unavailable; start the patched Reasonix desktop app")
	}
	if verifyProcess == nil {
		return errors.New("Reasonix desktop bridge process verifier is unavailable")
	}
	if err := verifyProcess(discovery.PID); err != nil {
		return fmt.Errorf("Reasonix desktop bridge process identity is invalid: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, reasonixDesktopBridgeTimeout)
	defer cancel()
	if _, err := inspectReasonixDesktopBridge(requestCtx, discovery); err != nil {
		return err
	}
	requestBody, err := json.Marshal(reasonixDesktopTurnRequest{
		DeliveryID:    deliveryID,
		TaskID:        taskID,
		WorkspaceRoot: workspaceRoot,
		Prompt:        prompt,
		Title:         "RE 审查 · " + taskID,
		WorkProfile:   workProfile,
	})
	if err != nil {
		return fmt.Errorf("encode Reasonix desktop turn: %w", err)
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(discovery.Endpoint, "/")+reasonixDesktopBridgeTurnPath, bytes.NewReader(requestBody))
	if err != nil {
		return errors.New("Reasonix desktop bridge endpoint is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+discovery.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := reasonixDesktopHTTPClient().Do(request)
	if err != nil {
		return uncertainDelivery(errors.New("Reasonix desktop bridge did not confirm whether it accepted the turn"))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure reasonixDesktopBridgeFailure
		_ = json.NewDecoder(io.LimitReader(response.Body, reasonixDesktopBridgeMaxBody)).Decode(&failure)
		// A retry is safe only when the bridge explicitly proves that this exact
		// delivery was rejected before it could create a visible user turn. Every
		// other non-200 response is ambiguous: the turn may have reached the
		// desktop client before its response, connection, or persistence failed.
		if response.StatusCode == http.StatusConflict && failure.DeliveryID == deliveryID && failure.State == "rejected_before_delivery" && isReasonixPreDeliveryFailure(failure.Error) {
			return fmt.Errorf("Reasonix desktop bridge rejected delivery before submission: %s", failure.Error)
		}
		return uncertainDelivery(fmt.Errorf("Reasonix desktop bridge did not confirm delivery (%s)", response.Status))
	}
	var result reasonixDesktopTurnResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, reasonixDesktopBridgeMaxBody)).Decode(&result); err != nil {
		return uncertainDelivery(errors.New("Reasonix desktop bridge returned an invalid acknowledgement"))
	}
	if result.DeliveryID != deliveryID || result.TaskID != taskID || result.TabID == "" || result.TopicID == "" {
		return uncertainDelivery(errors.New("Reasonix desktop bridge did not confirm the visible task conversation"))
	}
	if result.CollaborationMode != reasonixExecutionNormal ||
		result.WorkProfile != workProfile ||
		(result.ToolApprovalMode != reasonixApprovalAuto && result.ToolApprovalMode != reasonixApprovalYolo) {
		return uncertainDelivery(fmt.Errorf("Reasonix desktop bridge did not confirm normal/%s/auto-or-yolo", workProfile))
	}
	return nil
}

// inspectReasonixDesktopBridge verifies the authenticated, versioned health
// contract before a caller is allowed to submit a visible desktop turn.
func inspectReasonixDesktopBridge(ctx context.Context, discovery reasonixDesktopBridgeDiscovery) (reasonixDesktopBridgeHealth, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(discovery.Endpoint, "/")+reasonixDesktopBridgeHealthPath, nil)
	if err != nil {
		return reasonixDesktopBridgeHealth{}, errors.New("Reasonix desktop bridge health endpoint is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+discovery.Token)
	response, err := reasonixDesktopHTTPClient().Do(request)
	if err != nil {
		return reasonixDesktopBridgeHealth{}, errors.New("Reasonix desktop bridge health check failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return reasonixDesktopBridgeHealth{}, fmt.Errorf("Reasonix desktop bridge health check returned %s", response.Status)
	}
	var health reasonixDesktopBridgeHealth
	decoder := json.NewDecoder(io.LimitReader(response.Body, reasonixDesktopBridgeMaxBody))
	if err := decoder.Decode(&health); err != nil {
		return reasonixDesktopBridgeHealth{}, errors.New("Reasonix desktop bridge returned an invalid health response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return reasonixDesktopBridgeHealth{}, errors.New("Reasonix desktop bridge health response contains trailing data")
	}
	if err := validateReasonixDesktopBridgeHealth(health); err != nil {
		return health, err
	}
	return health, nil
}

func validateReasonixDesktopBridgeHealth(health reasonixDesktopBridgeHealth) error {
	if health.Status != "ok" {
		return incompatibleReasonixDesktopBridge("health status is not ok")
	}
	if health.Protocol.Name != reasonixDesktopBridgeProtocolName {
		return incompatibleReasonixDesktopBridge("protocol name does not match")
	}
	if health.Protocol.Major != reasonixDesktopBridgeProtocolMajor {
		return incompatibleReasonixDesktopBridge(fmt.Sprintf("protocol major %d is unsupported", health.Protocol.Major))
	}
	if health.Protocol.Minor < 0 {
		return incompatibleReasonixDesktopBridge("protocol minor is invalid")
	}
	if health.Client.Name != reasonixDesktopBridgeClientName {
		return incompatibleReasonixDesktopBridge("client name does not match")
	}
	if strings.TrimSpace(health.Client.Version) == "" || strings.TrimSpace(health.Client.Build) == "" {
		return incompatibleReasonixDesktopBridge("client identity is incomplete")
	}
	capabilities := make(map[string]bool, len(health.Capabilities))
	for _, capability := range health.Capabilities {
		capabilities[strings.TrimSpace(capability)] = true
	}
	for _, required := range []string{
		reasonixDesktopCapabilityVisibleUserTurn,
		reasonixDesktopCapabilityDeliveryIdempotency,
		reasonixDesktopCapabilityProfileReceipt,
	} {
		if !capabilities[required] {
			return incompatibleReasonixDesktopBridge("required capability " + required + " is unavailable")
		}
	}
	return nil
}

func incompatibleReasonixDesktopBridge(reason string) error {
	return &reasonixDesktopBridgeIncompatibleError{reason: reason}
}

func isReasonixPreDeliveryFailure(code string) bool {
	switch strings.TrimSpace(code) {
	case "task_busy", "delivery_failed", "desktop_starting":
		return true
	default:
		return false
	}
}

func normalizeReasonixWorkProfile(workProfile string) (string, error) {
	switch strings.TrimSpace(workProfile) {
	case "", reasonixWorkDelivery:
		return reasonixWorkDelivery, nil
	case reasonixWorkBalanced:
		return reasonixWorkBalanced, nil
	default:
		return "", errors.New("must be balanced or delivery")
	}
}

func reasonixDesktopHTTPClient() *http.Client {
	return &http.Client{
		Timeout: reasonixDesktopBridgeTimeout,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func reasonixDesktopBridgeDiscoveryPath() (string, error) {
	stateHome, err := reasonixDesktopStateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateHome, reasonixDesktopBridgeDiscoveryFile), nil
}

func reasonixDesktopStateHome() (string, error) {
	if runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(dir) == "" {
			return "", errors.New("user config directory is unavailable")
		}
		return filepath.Join(dir, "reasonix"), nil
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".reasonix"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "reasonix"), nil
}

func readReasonixDesktopBridge(path string) (reasonixDesktopBridgeDiscovery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return reasonixDesktopBridgeDiscovery{}, err
	}
	if len(data) > reasonixDesktopBridgeMaxBody {
		return reasonixDesktopBridgeDiscovery{}, errors.New("bridge discovery is too large")
	}
	var discovery reasonixDesktopBridgeDiscovery
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&discovery); err != nil {
		return reasonixDesktopBridgeDiscovery{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return reasonixDesktopBridgeDiscovery{}, errors.New("bridge discovery contains trailing data")
	}
	if err := validateReasonixDesktopBridgeDiscovery(discovery); err != nil {
		return reasonixDesktopBridgeDiscovery{}, err
	}
	return discovery, nil
}

func validateReasonixDesktopBridgeDiscovery(discovery reasonixDesktopBridgeDiscovery) error {
	if discovery.Version != 1 || discovery.PID <= 0 {
		return errors.New("bridge discovery version is invalid")
	}
	endpoint, err := url.Parse(strings.TrimSpace(discovery.Endpoint))
	if err != nil {
		return err
	}
	if endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("bridge endpoint must be authenticated loopback HTTP")
	}
	if strings.TrimSpace(discovery.Token) == "" || len(discovery.Token) > 256 {
		return errors.New("bridge token is invalid")
	}
	return nil
}
