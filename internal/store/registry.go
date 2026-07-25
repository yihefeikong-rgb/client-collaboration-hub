package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"gopkg.in/yaml.v3"
)

var (
	ErrNotFound      = errors.New("resource not found")
	ErrAlreadyExists = errors.New("resource already exists")
)

type RegistryStore interface {
	CreateProject(context.Context, protocol.Project) error
	RegisterClient(context.Context, protocol.Client) error
	ReadProject(context.Context, string) (protocol.Project, error)
	ReadClient(context.Context, string) (protocol.Client, error)
	ProjectExists(string) bool
	ClientExists(string) bool
	ClientHasCapability(string, string) bool
}

type FileRegistryStore struct {
	Root     string
	Locks    ScopedLocks
	FS       FileSystem
	Replacer AtomicReplacer
}

func NewFileRegistryStore(root string, locker Locker) *FileRegistryStore {
	return &FileRegistryStore{
		Root:     root,
		Locks:    ScopedLocks{Root: root, Locker: locker},
		FS:       osFileSystem{},
		Replacer: osReplacer{},
	}
}

func (r *FileRegistryStore) CreateProject(ctx context.Context, project protocol.Project) error {
	if err := project.Validate(project.ID); err != nil {
		return err
	}
	lock, err := r.Locks.Projects(ctx)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	return r.createProject(project)
}

func (r *FileRegistryStore) RegisterClient(ctx context.Context, client protocol.Client) error {
	if err := client.Validate(client.ID); err != nil {
		return err
	}
	lock, err := r.Locks.Clients(ctx)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	return r.registerClient(client)
}

func (r *FileRegistryStore) ReadProject(_ context.Context, id string) (protocol.Project, error) {
	var project protocol.Project
	if !protocol.IsValidID(id) {
		return project, fmt.Errorf("invalid project id %q", id)
	}
	data, err := r.FS.ReadFile(r.projectPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return project, fmt.Errorf("%w: project %q", ErrNotFound, id)
	}
	if err != nil {
		return project, err
	}
	return protocol.DecodeProject(data, id+".yaml")
}

func (r *FileRegistryStore) ReadClient(_ context.Context, id string) (protocol.Client, error) {
	var client protocol.Client
	if !protocol.IsValidID(id) {
		return client, fmt.Errorf("invalid client id %q", id)
	}
	data, err := r.FS.ReadFile(r.clientPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return client, fmt.Errorf("%w: client %q", ErrNotFound, id)
	}
	if err != nil {
		return client, err
	}
	return protocol.DecodeClient(data, id+".yaml")
}

func (r *FileRegistryStore) ProjectExists(id string) bool {
	_, err := r.ReadProject(context.Background(), id)
	return err == nil
}

func (r *FileRegistryStore) ClientExists(id string) bool {
	_, err := r.ReadClient(context.Background(), id)
	return err == nil
}

func (r *FileRegistryStore) ClientHasCapability(id, capability string) bool {
	client, err := r.ReadClient(context.Background(), id)
	return err == nil && client.HasCapability(capability)
}

func (r *FileRegistryStore) createProject(project protocol.Project) error {
	path := r.projectPath(project.ID)
	if err := r.requireAbsent(path, "project", project.ID); err != nil {
		return err
	}
	data, err := yaml.Marshal(project)
	if err != nil {
		return err
	}
	return writeAtomically(r.FS, r.Replacer, path, ".project-*.tmp", append(data, '\n'))
}

func (r *FileRegistryStore) registerClient(client protocol.Client) error {
	path := r.clientPath(client.ID)
	if err := r.requireAbsent(path, "client", client.ID); err != nil {
		return err
	}
	data, err := yaml.Marshal(client)
	if err != nil {
		return err
	}
	return writeAtomically(r.FS, r.Replacer, path, ".client-*.tmp", append(data, '\n'))
}

func (r *FileRegistryStore) requireAbsent(path, kind, id string) error {
	_, err := r.FS.Stat(path)
	if err == nil {
		return fmt.Errorf("%w: %s %q", ErrAlreadyExists, kind, id)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (r *FileRegistryStore) projectPath(id string) string {
	return filepath.Join(r.Root, "projects", id+".yaml")
}

func (r *FileRegistryStore) clientPath(id string) string {
	return filepath.Join(r.Root, "clients", id+".yaml")
}

func writeAtomically(fs FileSystem, replacer AtomicReplacer, path, pattern string, data []byte) error {
	if err := fs.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, tempPath, err := fs.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	defer fs.Remove(tempPath)
	writeErr := writeFull(file, data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return replacer.Replace(tempPath, path)
}
