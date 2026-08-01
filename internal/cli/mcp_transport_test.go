package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestMCPStdioTransportContentLength verifies that the MCP server can be
// driven by a host using Content-Length framing (for example the Claude Code
// desktop sidecar), not only by newline-delimited JSON.
func TestMCPStdioTransportContentLength(t *testing.T) {
	if os.Getenv("COLLAB_MCP_TEST_HELPER") == "1" {
		app := NewApp(os.Getenv("COLLAB_MCP_TEST_ROOT"), os.Stdout, os.Stderr, nil)
		if err := app.EnsureInitialized(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := app.runMCP(context.Background()); err != nil {
			t.Fatal(err)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioTransportContentLength$")
	command.Env = append(os.Environ(), "COLLAB_MCP_TEST_HELPER=1", "COLLAB_MCP_TEST_ROOT="+t.TempDir())
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()

	reader := bufio.NewReader(stdout)
	send := func(message map[string]any) {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(raw), raw); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() []byte {
		t.Helper()
		body, err := readContentLengthFrame(reader)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}

	send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "content-length-test", "version": "1"},
		},
	})
	var initialize struct {
		ID     int `json:"id"`
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(receive(), &initialize); err != nil {
		t.Fatal(err)
	}
	if initialize.ID != 1 || initialize.Result.ServerInfo.Name != "client-collaboration-hub" {
		t.Fatalf("unexpected initialize response: %+v", initialize)
	}

	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	var tools struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(receive(), &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Result.Tools) != 12 {
		t.Fatalf("Content-Length framed tool count = %d, want 12", len(tools.Result.Tools))
	}
	found := false
	for _, tool := range tools.Result.Tools {
		if tool.Name == "collab_list_projects" {
			found = true
		}
	}
	if !found {
		t.Fatal("collab_list_projects missing from Content-Length framed response")
	}
}

func readContentLengthFrame(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("invalid content-length header: %w", err)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing content-length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}
