//go:build windows

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

// wakeCCHaha wakes CC-HAHA through the desktop app's local server instead of
// launching a headless CLI directly. The server owns the CLI subprocess, keeps
// the session alive, and pushes real-time output to the desktop UI, so the
// human operator sees the agent working instead of a silent background job.
func (n *wakeNotifier) wakeCCHaha(ctx context.Context, snapshot store.TaskSnapshot, prompt, deliveryID string) error {
	baseURL, err := resolveCCHahaServer()
	if err != nil {
		return err
	}
	sessionID, err := n.ensureCCHahaSession(ctx, baseURL, snapshot)
	if err != nil {
		return err
	}
	if err := sendCCHahaMessage(ctx, baseURL, sessionID, prompt, deliveryID); err != nil {
		return fmt.Errorf("send message to cc-haha session %s: %w", sessionID, err)
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: sent wake message to CC-HAHA for %s (session=%s server=%s)\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, sessionID, baseURL)
	return nil
}

// ensureCCHahaSession returns the task's dedicated CC-HAHA session. The
// mapping is persisted so follow-up messages resume the same conversation.
func (n *wakeNotifier) ensureCCHahaSession(ctx context.Context, baseURL string, snapshot store.TaskSnapshot) (string, error) {
	if sessionID := n.ccSessionID(snapshot.Task.ID); sessionID != "" {
		if sessionExists(ctx, baseURL, sessionID) {
			return sessionID, nil
		}
		fmt.Fprintf(n.app.Stdout, "[watch] %s: session %s for %s no longer exists; creating a new one\n", time.Now().UTC().Format(time.RFC3339), sessionID, snapshot.Task.ID)
	}
	workDir := n.app.Root
	if binding, bindingErr := n.app.Bindings.ReadBinding(ctx, DefaultDeviceID(), snapshot.Project.ID); bindingErr == nil {
		if info, statErr := os.Stat(binding.LocalPath); statErr == nil && info.IsDir() {
			workDir = binding.LocalPath
		}
	}
	body, _ := json.Marshal(map[string]any{
		"workDir":        workDir,
		"permissionMode": "bypassPermissions",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/sessions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("create cc-haha session: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("create cc-haha session: server returned %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("decode create session response: %w", err)
	}
	if created.SessionID == "" {
		return "", fmt.Errorf("create session response missing sessionId")
	}
	if err := n.setCCSessionID(snapshot.Task.ID, created.SessionID); err != nil {
		return "", err
	}
	return created.SessionID, nil
}

// sendCCHahaMessage delivers one user message to the session over the server's
// WebSocket gateway. The server starts or resumes the managed CLI process and
// streams output to every connected client (including the desktop UI). The
// connection is closed after the message is accepted; the CLI keeps running
// because the server owns its lifecycle.
func sendCCHahaMessage(ctx context.Context, baseURL, sessionID, prompt, deliveryID string) error {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1)
	connection, _, err := dialer.DialContext(ctx, wsURL+"/ws/"+sessionID, nil)
	if err != nil {
		return fmt.Errorf("connect websocket: %w", err)
	}
	defer connection.Close()

	payload, _ := json.Marshal(map[string]any{
		"type":        "user_message",
		"content":     prompt,
		"delivery_id": deliveryID,
	})
	if err := connection.WriteMessage(websocket.TextMessage, payload); err != nil {
		return uncertainDelivery(fmt.Errorf("write user_message: %w", err))
	}

	// A thinking status is emitted before CC has handed the user message to the
	// managed session, so it is not a receipt. The patched CC server replies
	// with delivery_ack only after the exact delivery id has been accepted.
	_ = connection.SetReadDeadline(time.Now().Add(20 * time.Second))
	for {
		select {
		case <-ctx.Done():
			return uncertainDelivery(ctx.Err())
		default:
		}
		_, data, err := connection.ReadMessage()
		if err != nil {
			return uncertainDelivery(fmt.Errorf("wait for server acknowledgement: %w", err))
		}
		var message struct {
			Type       string `json:"type"`
			DeliveryID string `json:"delivery_id"`
			State      string `json:"state"`
			Error      string `json:"message"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		switch message.Type {
		case "error":
			return uncertainDelivery(fmt.Errorf("server rejected turn: %s", message.Error))
		case "delivery_ack":
			return validateCCHahaDeliveryAck(deliveryID, message.DeliveryID, message.State)
		}
	}
}

// resolveCCHahaServer discovers the CC-HAHA desktop local server. The port is
// persisted by the desktop app after each start, so we read that first and
// fall back to probing known ports.
func resolveCCHahaServer() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("CC_HAHA_SERVER_URL")); explicit != "" {
		return strings.TrimRight(explicit, "/"), nil
	}
	candidates := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		statePath := filepath.Join(home, ".claude", "desktop-server-state.json")
		if data, readErr := os.ReadFile(statePath); readErr == nil {
			var state struct {
				LastPort int `json:"lastPort"`
			}
			if json.Unmarshal(data, &state) == nil && state.LastPort > 0 {
				candidates = append(candidates, fmt.Sprintf("http://127.0.0.1:%d", state.LastPort))
			}
		}
	}
	candidates = append(candidates, "http://127.0.0.1:10558", "http://127.0.0.1:10220", "http://127.0.0.1:6906")
	for _, candidate := range candidates {
		if ccServerAlive(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("CC-HAHA desktop server not reachable; start the desktop app or set CC_HAHA_SERVER_URL")
}

func ccServerAlive(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(baseURL + "/api/status")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func sessionExists(ctx context.Context, baseURL, sessionID string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/sessions/"+sessionID, nil)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}
