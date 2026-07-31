package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestCatalogListsVerifiedRegistryAndTasks(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	ctx := context.Background()
	if err := registry.RegisterClient(ctx, protocol.Client{ID: "zeta", Name: "Zeta", Capabilities: []string{"execute"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(ctx, protocol.Client{ID: "alpha", Name: "Alpha", Capabilities: []string{"create_task", "review"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.CreateProject(ctx, protocol.Project{ID: "project-z", Name: "Z", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := registry.CreateProject(ctx, protocol.Project{ID: "project-a", Name: "A", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	clients, err := registry.ListClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := registry.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{clients[0].ID, clients[1].ID}; !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("clients = %v", got)
	}
	if got := []string{projects[0].ID, projects[1].ID}; !reflect.DeepEqual(got, []string{"project-a", "project-z"}) {
		t.Fatalf("projects = %v", got)
	}

	evidence := NewFileEvidenceStore(root)
	journal := NewFileTaskJournal(root, FlockLocker{}, registry, evidence)
	for _, id := range []string{"T-0002", "T-0001"} {
		task := protocol.Task{ID: id, ProjectID: "project-a", Title: id, Objective: "List tasks", Acceptance: []string{"Pass"}, Creator: "alpha", Reviewer: "alpha", CreatedAt: time.Now().UTC()}
		if err := journal.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := journal.ListTaskIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"T-0001", "T-0002"}) {
		t.Fatalf("task ids = %v", ids)
	}
}

func TestCatalogRejectsUnexpectedEntries(t *testing.T) {
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := os.MkdirAll(filepath.Join(root, "clients"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clients", "note.txt"), []byte("not a client"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ListClients(context.Background()); err == nil {
		t.Fatal("ListClients accepted an unexpected entry")
	}
}
