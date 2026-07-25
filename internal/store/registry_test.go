package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestRegistryCreatesAndReadsImmutableRecords(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	project := protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: journalTime}
	client := protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review"}}
	if err := registry.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ReadProject(context.Background(), project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ReadClient(context.Background(), client.ID); err != nil {
		t.Fatal(err)
	}
	if !registry.ProjectExists(project.ID) || !registry.ClientExists(client.ID) || !registry.ClientHasCapability(client.ID, "review") {
		t.Fatal("registry references are incomplete")
	}
	if err := registry.CreateProject(context.Background(), project); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate project error = %v", err)
	}
	if _, err := registry.ReadClient(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing client error = %v", err)
	}
}

func TestRegistryRejectsInvalidStoredYAML(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := os.MkdirAll(filepath.Join(root, "clients"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clients", "codex.yaml"), []byte("id: codex\nname: Codex\ncapabilities: [review]\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ReadClient(context.Background(), "codex"); err == nil {
		t.Fatal("invalid YAML accepted")
	}
}

func TestCreateTaskRequiresRegisteredReferences(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	journal := NewFileTaskJournal(root, FlockLocker{}, registry)
	task := protocol.Task{ID: "T-0001", ProjectID: "project-1", Title: "Test", Objective: "Test", Acceptance: []string{"Pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)}
	if err := journal.CreateTask(context.Background(), task); err == nil {
		t.Fatal("unknown references accepted")
	}
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Demo", CreatedAt: task.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"review"}}); err != nil {
		t.Fatal(err)
	}
	if err := journal.CreateTask(context.Background(), task); err == nil {
		t.Fatal("creator without create_task capability accepted")
	}
}
