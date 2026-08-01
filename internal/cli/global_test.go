package cli

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResolveDataRootUsesOverride(t *testing.T) {
	expected := filepath.Join(t.TempDir(), "hub")
	t.Setenv(HomeEnvironment, expected)
	actual, err := ResolveDataRoot()
	if err != nil {
		t.Fatal(err)
	}
	if actual != filepath.Clean(expected) {
		t.Fatalf("ResolveDataRoot() = %q, want %q", actual, expected)
	}
}

func TestEnsureInitializedRegistersDefaultClients(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"codex", "cc-haha", "reasonix"} {
		if !app.Registry.ClientExists(id) {
			t.Fatalf("default client %q was not registered", id)
		}
	}
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatalf("second initialization failed: %v", err)
	}
}

func TestWorkspacePathUsesWorkingDirectory(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	app.WorkingDirectory = filepath.Join(t.TempDir(), "workspace")
	want := filepath.Join(app.WorkingDirectory, "candidate.json")
	if got := app.workspacePath("candidate.json"); got != want {
		t.Fatalf("workspacePath() = %q, want %q", got, want)
	}
}
