package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type LocalProjectResult struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	DeviceID  string `json:"device_id"`
	LocalPath string `json:"local_path"`
	Created   bool   `json:"created"`
}

func (a *App) RegisterLocalProject(ctx context.Context, requestedID, requestedName, localPath string) (LocalProjectResult, error) {
	resolved, err := resolveProjectDirectory(localPath)
	if err != nil {
		return LocalProjectResult{}, err
	}
	lock, err := (store.FlockLocker{}).Lock(ctx, filepath.Join(a.Root, "collaboration", ".runtime", "locks", "local-project-registration.lock"))
	if err != nil {
		return LocalProjectResult{}, err
	}
	defer lock.Unlock()
	name := strings.TrimSpace(requestedName)
	if name == "" {
		name = filepath.Base(resolved)
	}
	explicitID := strings.TrimSpace(requestedID) != ""
	id := strings.TrimSpace(requestedID)
	if id == "" {
		id = projectID(name, resolved, false)
	}
	if !protocol.IsValidID(id) {
		return LocalProjectResult{}, fmt.Errorf("invalid project id %q", id)
	}
	device := DefaultDeviceID()
	if catalog, ok := a.Registry.(store.RegistryCatalog); ok {
		projects, err := catalog.ListProjects(ctx)
		if err != nil {
			return LocalProjectResult{}, err
		}
		for _, existing := range projects {
			binding, bindingErr := a.Bindings.ReadBinding(ctx, device, existing.ID)
			if errors.Is(bindingErr, store.ErrBindingUnavailable) {
				continue
			}
			if bindingErr != nil {
				return LocalProjectResult{}, bindingErr
			}
			if !sameLocalPath(binding.LocalPath, resolved) {
				continue
			}
			if explicitID && id != existing.ID {
				return LocalProjectResult{}, fmt.Errorf("local directory is already registered as project %q", existing.ID)
			}
			return LocalProjectResult{ProjectID: existing.ID, Name: existing.Name, DeviceID: device, LocalPath: resolved}, nil
		}
	}
	for {
		project, readErr := a.Registry.ReadProject(ctx, id)
		if errors.Is(readErr, store.ErrNotFound) {
			break
		}
		if readErr != nil {
			return LocalProjectResult{}, readErr
		}
		if project.Name == name {
			binding, bindingErr := a.Bindings.ReadBinding(ctx, device, id)
			if errors.Is(bindingErr, store.ErrBindingUnavailable) {
				break
			}
			if bindingErr != nil {
				return LocalProjectResult{}, bindingErr
			}
			if sameLocalPath(binding.LocalPath, resolved) {
				return LocalProjectResult{ProjectID: id, Name: name, DeviceID: device, LocalPath: resolved}, nil
			}
		}
		if explicitID {
			return LocalProjectResult{}, fmt.Errorf("project %q is already registered for another local project", id)
		}
		hashed := projectID(name, resolved, true)
		if id == hashed {
			return LocalProjectResult{}, fmt.Errorf("generated project id %q conflicts with another local project", id)
		}
		id = hashed
	}
	created := false
	if !a.Registry.ProjectExists(id) {
		project := protocol.Project{ID: id, Name: name, CreatedAt: a.now()}
		if err := a.Registry.CreateProject(ctx, project); err != nil {
			return LocalProjectResult{}, err
		}
		created = true
	}
	binding := store.ProjectBinding{
		DeviceID:  device,
		ProjectID: id,
		LocalPath: resolved,
		BoundAt:   a.now(),
	}
	if err := a.Bindings.BindProject(ctx, binding); err != nil {
		return LocalProjectResult{}, err
	}
	return LocalProjectResult{ProjectID: id, Name: name, DeviceID: device, LocalPath: resolved, Created: created}, nil
}

func resolveProjectDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("local project path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("local project path must be a directory")
	}
	return filepath.Clean(resolved), nil
}

func projectID(name, path string, alwaysHash bool) string {
	var result strings.Builder
	lastDash := false
	for _, value := range strings.ToLower(name) {
		if unicode.IsLetter(value) && value <= unicode.MaxASCII || unicode.IsDigit(value) {
			result.WriteRune(value)
			lastDash = false
			continue
		}
		if result.Len() > 0 && !lastDash {
			result.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(result.String(), "-")
	if base == "" {
		base = "project"
	}
	hashPath := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		hashPath = strings.ToLower(hashPath)
	}
	sum := sha256.Sum256([]byte(hashPath))
	if alwaysHash || base == "project" {
		base = fmt.Sprintf("%s-%x", base, sum[:4])
	}
	return base
}

func sameLocalPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
