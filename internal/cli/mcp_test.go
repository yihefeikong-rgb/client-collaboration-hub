package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestMCPStdioTransport(t *testing.T) {
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
	command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioTransport$")
	command.Env = append(os.Environ(), "COLLAB_MCP_TEST_HELPER=1", "COLLAB_MCP_TEST_ROOT="+t.TempDir())
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 11 {
		t.Fatalf("stdio MCP tool count = %d, want 11", len(tools.Tools))
	}
}

func TestMCPServerExposesControlledToolsAndGuide(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := app.NewMCPServer()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	list, err := clientSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(list.Tools))
	var createTask *mcp.Tool
	for _, tool := range list.Tools {
		got[tool.Name] = true
		if tool.Name == "collab_create_task" {
			createTask = tool
		}
	}
	for _, name := range []string{
		"collab_list_projects", "collab_register_project", "collab_list_tasks", "collab_get_task",
		"collab_get_next_work", "collab_create_task", "collab_generate_handoff", "collab_submit_candidate",
		"collab_list_events", "collab_list_evidence", "collab_list_submissions",
	} {
		if !got[name] {
			t.Errorf("missing MCP tool %q", name)
		}
	}
	for name := range got {
		if strings.Contains(name, "approve") || strings.Contains(name, "request_changes") {
			t.Errorf("human-final action was exposed through MCP: %q", name)
		}
	}
	if createTask == nil || createTask.Annotations == nil || createTask.Annotations.IdempotentHint {
		t.Fatal("collab_create_task must not advertise idempotency")
	}

	resource, err := clientSession.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: mcpGuideURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || !strings.Contains(resource.Contents[0].Text, "终审模式") {
		t.Fatal("MCP operating guide was not returned")
	}
}

func TestMCPRejectsUnmanagedHandoffDirectory(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.managedHandoffDirectory(context.Background(), t.TempDir()); err == nil {
		t.Fatal("accepted a handoff package outside managed history")
	}
}

func TestMCPAcceptsVerifiedManagedHandoffDirectory(t *testing.T) {
	root := t.TempDir()
	app := NewApp(root, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	projectPath := t.TempDir()
	if _, err := app.RegisterLocalProject(context.Background(), "project-1", "Project", projectPath); err != nil {
		t.Fatal(err)
	}
	task := protocol.Task{
		ID: "T-MCP-1", ProjectID: "project-1", Title: "MCP task", Objective: "Verify managed package",
		Acceptance: []string{"Package is verified"}, Creator: "codex", Reviewer: "codex", CreatedAt: app.now(),
	}
	if err := app.Journal.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	report, err := app.Handoff.ExportNext(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := app.managedHandoffDirectory(context.Background(), report.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(directory) {
		t.Fatalf("managed directory is not absolute: %q", directory)
	}
}

func TestMCPRegisterProjectToolUsesGlobalStore(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := app.NewMCPServer()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "collab_register_project", Arguments: map[string]any{"path": t.TempDir(), "name": "MCP Project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned an error: %#v", result.Content)
	}
	if !app.Registry.ProjectExists("mcp-project") {
		t.Fatal("MCP project registration did not write to the app store")
	}
}
