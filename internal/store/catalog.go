package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

// RegistryCatalog exposes verified, read-only registry listings for local operator views.
type RegistryCatalog interface {
	ListClients(context.Context) ([]protocol.Client, error)
	ListProjects(context.Context) ([]protocol.Project, error)
}

// TaskCatalog exposes task identifiers without reading or mutating task state.
type TaskCatalog interface {
	ListTaskIDs(context.Context) ([]string, error)
}

func (r *FileRegistryStore) ListClients(ctx context.Context) ([]protocol.Client, error) {
	exists, err := directoryExists(filepath.Join(r.Root, "clients"))
	if err != nil {
		return nil, err
	}
	if !exists {
		return []protocol.Client{}, nil
	}
	lock, err := r.Locks.Clients(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Unlock()
	ids, err := listYAMLIDs(filepath.Join(r.Root, "clients"))
	if err != nil {
		return nil, err
	}
	clients := make([]protocol.Client, 0, len(ids))
	for _, id := range ids {
		client, err := r.ReadClient(ctx, id)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, nil
}

func (r *FileRegistryStore) ListProjects(ctx context.Context) ([]protocol.Project, error) {
	exists, err := directoryExists(filepath.Join(r.Root, "projects"))
	if err != nil {
		return nil, err
	}
	if !exists {
		return []protocol.Project{}, nil
	}
	lock, err := r.Locks.Projects(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Unlock()
	ids, err := listYAMLIDs(filepath.Join(r.Root, "projects"))
	if err != nil {
		return nil, err
	}
	projects := make([]protocol.Project, 0, len(ids))
	for _, id := range ids {
		project, err := r.ReadProject(ctx, id)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (j *FileTaskJournal) ListTaskIDs(_ context.Context) ([]string, error) {
	directory := filepath.Join(j.Root, "tasks")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !protocol.IsValidID(name) {
			return nil, fmt.Errorf("invalid task directory %q", name)
		}
		if err := requireDirectory(entry); err != nil {
			return nil, fmt.Errorf("task directory %q: %w", name, err)
		}
		ids = append(ids, name)
	}
	sort.Strings(ids)
	return ids, nil
}

func listYAMLIDs(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if filepath.Ext(name) != ".yaml" {
			return nil, fmt.Errorf("unexpected registry entry %q", name)
		}
		id := strings.TrimSuffix(name, ".yaml")
		if !protocol.IsValidID(id) {
			return nil, fmt.Errorf("invalid registry id %q", id)
		}
		if err := requireRegularFile(entry); err != nil {
			return nil, fmt.Errorf("registry entry %q: %w", name, err)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func directoryExists(directory string) (bool, error) {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%q must be a directory", directory)
	}
	return true, nil
}

func requireDirectory(entry fs.DirEntry) error {
	if entry.Type()&fs.ModeSymlink != 0 {
		return errors.New("symbolic links are not allowed")
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("must be a directory")
	}
	return nil
}

func requireRegularFile(entry fs.DirEntry) error {
	if entry.Type()&fs.ModeSymlink != 0 {
		return errors.New("symbolic links are not allowed")
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("must be a regular file")
	}
	return nil
}
