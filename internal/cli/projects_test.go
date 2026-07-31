package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterLocalProjectCreatesProjectAndBinding(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	projectPath := t.TempDir()
	result, err := app.RegisterLocalProject(context.Background(), "", "Example Project", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != "example-project" || !result.Created {
		t.Fatalf("unexpected result: %#v", result)
	}
	binding, err := app.Bindings.ReadBinding(context.Background(), DefaultDeviceID(), result.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := filepath.EvalSymlinks(projectPath)
	if binding.LocalPath != filepath.Clean(expected) {
		t.Fatalf("binding path = %q, want %q", binding.LocalPath, expected)
	}
	second, err := app.RegisterLocalProject(context.Background(), "", "Example Project", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("idempotent registration reported a new project")
	}
	renamed, err := app.RegisterLocalProject(context.Background(), "", "A Different Display Name", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ProjectID != result.ProjectID {
		t.Fatalf("same local directory was duplicated as %q", renamed.ProjectID)
	}
}

func TestRegisterLocalProjectDisambiguatesSameDirectoryName(t *testing.T) {
	app := NewApp(t.TempDir(), nil, nil, nil)
	if err := app.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstRoot := filepath.Join(t.TempDir(), "same-name")
	secondRoot := filepath.Join(t.TempDir(), "same-name")
	for _, path := range []string{firstRoot, secondRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	first, err := app.RegisterLocalProject(context.Background(), "", "", firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.RegisterLocalProject(context.Background(), "", "", secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectID == second.ProjectID || !strings.HasPrefix(second.ProjectID, "same-name-") {
		t.Fatalf("project ids were not disambiguated: %q and %q", first.ProjectID, second.ProjectID)
	}
}
