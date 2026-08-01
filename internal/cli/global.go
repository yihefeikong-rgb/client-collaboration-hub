package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

const HomeEnvironment = "COLLAB_HOME"

func ResolveDataRoot() (string, error) {
	if configured := os.Getenv(HomeEnvironment); configured != "" {
		return normalizeDataRoot(configured)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve local application data: %w", err)
	}
	return normalizeDataRoot(filepath.Join(cache, "client-collaboration-hub"))
}

func normalizeDataRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve collaboration home: %w", err)
	}
	if info, err := os.Stat(absolute); err == nil && !info.IsDir() {
		return "", errors.New("collaboration home must be a directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func DefaultDeviceID() string {
	switch runtime.GOOS {
	case "windows":
		return "local-windows"
	case "linux":
		return "local-linux"
	case "darwin":
		return "local-macos"
	default:
		return "local-device"
	}
}

func (a *App) EnsureInitialized(ctx context.Context) error {
	for _, directory := range []string{"projects", "clients", "tasks", "bindings", ".runtime"} {
		if err := os.MkdirAll(filepath.Join(a.Root, "collaboration", directory), 0o700); err != nil {
			return err
		}
	}
	defaults := []protocol.Client{
		{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review", "import_export"}},
		{ID: "cc-haha", Name: "CC-HAHA", Capabilities: []string{"execute", "create_task", "import_export"}},
		{ID: "reasonix", Name: "Reasonix (RE)", Capabilities: []string{"review", "create_task", "import_export"}},
	}
	for _, client := range defaults {
		if a.Registry.ClientExists(client.ID) {
			continue
		}
		if err := a.Registry.RegisterClient(ctx, client); err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			return err
		}
	}
	return nil
}
