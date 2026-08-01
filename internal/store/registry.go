package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"gopkg.in/yaml.v3"
)

var (
	ErrNotFound              = errors.New("resource not found")
	ErrAlreadyExists         = errors.New("resource already exists")
	ErrPolicyVersionConflict = errors.New("project policy version conflict")
)

type RegistryStore interface {
	CreateProject(context.Context, protocol.Project) error
	UpdateProjectPolicy(context.Context, string, int64, string, protocol.CollaborationPolicy, time.Time) (protocol.Project, error)
	RegisterClient(context.Context, protocol.Client) error
	UpdateClient(context.Context, protocol.Client) error
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
	project = project.NormalizePolicy()
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

func (r *FileRegistryStore) UpdateProjectPolicy(ctx context.Context, id string, expectedVersion int64, actor string, policy protocol.CollaborationPolicy, at time.Time) (protocol.Project, error) {
	if !protocol.IsValidID(id) || expectedVersion < 1 || !protocol.IsValidID(actor) || at.IsZero() || at.Location() != time.UTC {
		return protocol.Project{}, fmt.Errorf("invalid project policy update")
	}
	if err := policy.Validate(); err != nil {
		return protocol.Project{}, err
	}
	lock, err := r.Locks.Projects(ctx)
	if err != nil {
		return protocol.Project{}, err
	}
	defer lock.Unlock()
	project, err := r.ReadProject(ctx, id)
	if err != nil {
		return protocol.Project{}, err
	}
	if project.PolicyVersion != expectedVersion {
		return protocol.Project{}, ErrPolicyVersionConflict
	}
	if at.Before(project.CreatedAt) {
		return protocol.Project{}, fmt.Errorf("project policy update time is before project creation")
	}
	project.PolicyHistory = append(project.PolicyHistory, protocol.PolicyAuditEntry{
		Version:  project.PolicyVersion + 1,
		Actor:    actor,
		Origin:   protocol.EventOriginHuman,
		At:       at,
		Previous: project.CollaborationPolicy,
		Current:  policy,
	})
	project.CollaborationPolicy = policy
	project.PolicyVersion++
	if err := project.Validate(id); err != nil {
		return protocol.Project{}, err
	}
	data, err := yaml.Marshal(project)
	if err != nil {
		return protocol.Project{}, err
	}
	if err := writeAtomically(r.FS, r.Replacer, r.projectPath(id), ".project-*.tmp", append(data, '\n')); err != nil {
		return protocol.Project{}, err
	}
	return project, nil
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

// UpdateClient 覆盖式更新已存在的客户端声明；客户端不存在时返回 NotFound。
// 用于统一协议层声明（角色、工作模式、审批模式、模型）的维护。
func (r *FileRegistryStore) UpdateClient(ctx context.Context, client protocol.Client) error {
	if err := client.Validate(client.ID); err != nil {
		return err
	}
	lock, err := r.Locks.Clients(ctx)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	path := r.clientPath(client.ID)
	if _, err := r.FS.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: client %q", ErrNotFound, client.ID)
		}
		return err
	}
	data, err := yaml.Marshal(client)
	if err != nil {
		return err
	}
	return writeAtomically(r.FS, r.Replacer, path, ".client-*.tmp", append(data, '\n'))
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
