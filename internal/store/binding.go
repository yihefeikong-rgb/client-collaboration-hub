package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

var ErrBindingUnavailable = errors.New("binding unavailable")

type ProjectBinding struct {
	DeviceID  string    `json:"device_id"`
	ProjectID string    `json:"project_id"`
	LocalPath string    `json:"local_path"`
	Revision  string    `json:"revision,omitempty"`
	BoundAt   time.Time `json:"bound_at"`
}

type BindingStore interface {
	BindProject(context.Context, ProjectBinding) error
	ReadBinding(context.Context, string, string) (ProjectBinding, error)
	BindingAvailable(context.Context, string, string) bool
}

type FileBindingStore struct {
	Root       string
	Locks      ScopedLocks
	FS         FileSystem
	Replacer   AtomicReplacer
	References protocol.References
}

func NewFileBindingStore(root string, locker Locker, references protocol.References) *FileBindingStore {
	return &FileBindingStore{
		Root:       root,
		Locks:      ScopedLocks{Root: root, Locker: locker},
		FS:         osFileSystem{},
		Replacer:   osReplacer{},
		References: references,
	}
}

func (s *FileBindingStore) BindProject(ctx context.Context, binding ProjectBinding) error {
	if s.References == nil || !protocol.IsValidID(binding.DeviceID) || !protocol.IsValidID(binding.ProjectID) {
		return fmt.Errorf("invalid project binding")
	}
	if !s.References.ProjectExists(binding.ProjectID) {
		return fmt.Errorf("%w: project %q", ErrNotFound, binding.ProjectID)
	}
	path, err := normalizeBindingPath(binding.LocalPath)
	if err != nil {
		return err
	}
	binding.LocalPath = path
	if binding.BoundAt.IsZero() || binding.BoundAt.Location() != time.UTC {
		return fmt.Errorf("binding bound_at must be a UTC timestamp")
	}
	lock, err := s.Locks.Binding(ctx, binding.DeviceID, binding.ProjectID)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	return writeAtomically(s.FS, s.Replacer, s.bindingPath(binding.DeviceID, binding.ProjectID), ".binding-*.tmp", append(data, '\n'))
}

func (s *FileBindingStore) ReadBinding(ctx context.Context, deviceID, projectID string) (ProjectBinding, error) {
	var binding ProjectBinding
	if !protocol.IsValidID(deviceID) || !protocol.IsValidID(projectID) {
		return binding, fmt.Errorf("invalid binding id")
	}
	if err := ctx.Err(); err != nil {
		return binding, err
	}
	data, err := s.FS.ReadFile(s.bindingPath(deviceID, projectID))
	if errors.Is(err, os.ErrNotExist) {
		return binding, fmt.Errorf("%w: project %q on device %q", ErrBindingUnavailable, projectID, deviceID)
	}
	if err != nil {
		return binding, err
	}
	if err := decodeBinding(data, &binding); err != nil {
		return binding, err
	}
	if err := binding.validate(deviceID, projectID); err != nil {
		return binding, err
	}
	return binding, nil
}

func (s *FileBindingStore) BindingAvailable(ctx context.Context, deviceID, projectID string) bool {
	binding, err := s.ReadBinding(ctx, deviceID, projectID)
	if err != nil {
		return false
	}
	info, err := os.Stat(binding.LocalPath)
	return err == nil && info.IsDir()
}

func (b ProjectBinding) validate(deviceID, projectID string) error {
	if b.DeviceID != deviceID || b.ProjectID != projectID || !protocol.IsValidID(b.DeviceID) || !protocol.IsValidID(b.ProjectID) {
		return fmt.Errorf("binding id does not match path")
	}
	if b.LocalPath == "" || !isAbsoluteLocalPath(b.LocalPath) || filepath.Clean(b.LocalPath) != b.LocalPath {
		return fmt.Errorf("binding local_path is invalid")
	}
	if b.BoundAt.IsZero() || b.BoundAt.Location() != time.UTC {
		return fmt.Errorf("binding bound_at must be a UTC timestamp")
	}
	return nil
}

func decodeBinding(data []byte, binding *ProjectBinding) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(binding); err != nil {
		return fmt.Errorf("decode binding: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("binding contains multiple JSON values")
		}
		return fmt.Errorf("decode extra binding value: %w", err)
	}
	return nil
}

func normalizeBindingPath(localPath string) (string, error) {
	if localPath == "" {
		return "", fmt.Errorf("binding path is required")
	}
	absolute, err := filepath.Abs(localPath)
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
		return "", fmt.Errorf("binding path is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func isAbsoluteLocalPath(value string) bool {
	if filepath.IsAbs(value) {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func (s *FileBindingStore) bindingPath(deviceID, projectID string) string {
	return filepath.Join(s.Root, "bindings", deviceID, projectID+".local.json")
}
