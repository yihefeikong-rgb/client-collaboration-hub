package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type HandoffHistoryEntry struct {
	TaskID       string    `json:"task_id"`
	TargetClient string    `json:"target_client"`
	Adapter      string    `json:"adapter"`
	PackageID    string    `json:"package_id"`
	ThroughEvent int64     `json:"through_event"`
	OutputDir    string    `json:"output_dir"`
	CreatedAt    time.Time `json:"created_at"`
}

type HandoffHistory interface {
	List(context.Context, string) ([]HandoffHistoryEntry, error)
	Append(context.Context, HandoffHistoryEntry) error
}

type FileHandoffHistory struct {
	WorkspaceRoot string
	Locker        store.Locker
}

func NewFileHandoffHistory(workspaceRoot string, locker store.Locker) *FileHandoffHistory {
	return &FileHandoffHistory{WorkspaceRoot: workspaceRoot, Locker: locker}
}

func (h *FileHandoffHistory) List(ctx context.Context, taskID string) ([]HandoffHistoryEntry, error) {
	if !protocol.IsValidID(taskID) {
		return nil, fmt.Errorf("invalid handoff history task id")
	}
	lock, err := h.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Unlock()
	entries, err := h.read()
	if err != nil {
		return nil, err
	}
	result := make([]HandoffHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.TaskID == taskID {
			result = append(result, entry)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].OutputDir < result[right].OutputDir
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result, nil
}

func (h *FileHandoffHistory) Append(ctx context.Context, entry HandoffHistoryEntry) error {
	if err := entry.validate(h.WorkspaceRoot); err != nil {
		return err
	}
	lock, err := h.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	entries, err := h.read()
	if err != nil {
		return err
	}
	for _, existing := range entries {
		if existing == entry {
			return nil
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path()), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(h.path(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if count, err := file.Write(append(data, '\n')); err != nil {
		writeErr = err
	} else if count != len(data)+1 {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (h *FileHandoffHistory) read() ([]HandoffHistoryEntry, error) {
	info, err := os.Lstat(h.path())
	if errors.Is(err, os.ErrNotExist) {
		return []HandoffHistoryEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("handoff history is unsafe")
	}
	data, err := os.ReadFile(h.path())
	if err != nil {
		return nil, err
	}
	data, err = h.repairUnterminatedTail(data)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte{'\n'})
	entries := make([]HandoffHistoryEntry, 0, len(lines))
	for index, line := range lines {
		if len(line) == 0 {
			if index == len(lines)-1 {
				continue
			}
			return nil, fmt.Errorf("handoff history contains an empty record")
		}
		entry, err := h.decodeEntry(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (h *FileHandoffHistory) decodeEntry(line []byte) (HandoffHistoryEntry, error) {
	var entry HandoffHistoryEntry
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return HandoffHistoryEntry{}, fmt.Errorf("decode handoff history: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return HandoffHistoryEntry{}, fmt.Errorf("handoff history contains multiple JSON values")
	}
	if err := entry.validate(h.WorkspaceRoot); err != nil {
		return HandoffHistoryEntry{}, err
	}
	return entry, nil
}

func (h *FileHandoffHistory) repairUnterminatedTail(data []byte) ([]byte, error) {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data, nil
	}
	lastNewline := bytes.LastIndexByte(data, '\n')
	tailStart := lastNewline + 1
	tail := data[tailStart:]
	if json.Valid(tail) {
		if _, err := h.decodeEntry(tail); err != nil {
			return nil, err
		}
		if err := h.appendNewline(); err != nil {
			return nil, err
		}
		return append(data, '\n'), nil
	}
	if err := h.truncate(tailStart); err != nil {
		return nil, err
	}
	return data[:tailStart], nil
}

func (h *FileHandoffHistory) appendNewline() error {
	file, err := os.OpenFile(h.path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if count, err := file.Write([]byte{'\n'}); err != nil {
		writeErr = err
	} else if count != 1 {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (h *FileHandoffHistory) truncate(size int) error {
	file, err := os.OpenFile(h.path(), os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	truncateErr := file.Truncate(int64(size))
	if truncateErr == nil {
		truncateErr = file.Sync()
	}
	closeErr := file.Close()
	if truncateErr != nil {
		return truncateErr
	}
	return closeErr
}

func (h *FileHandoffHistory) lock(ctx context.Context) (store.Lock, error) {
	if h == nil || h.Locker == nil || strings.TrimSpace(h.WorkspaceRoot) == "" {
		return nil, fmt.Errorf("handoff history is not configured")
	}
	return h.Locker.Lock(ctx, filepath.Join(h.WorkspaceRoot, "collaboration", ".runtime", "locks", "handoff-history.lock"))
}

func (h *FileHandoffHistory) path() string {
	return filepath.Join(h.WorkspaceRoot, "collaboration", ".runtime", "handoff-history.jsonl")
}

func (entry HandoffHistoryEntry) validate(workspaceRoot string) error {
	if !protocol.IsValidID(entry.TaskID) || !protocol.IsValidID(entry.TargetClient) || !validAdapter(entry.Adapter) || !validPackageID(entry.PackageID) || entry.ThroughEvent < 1 || entry.CreatedAt.IsZero() || entry.CreatedAt.Location() != time.UTC {
		return fmt.Errorf("invalid handoff history entry")
	}
	if adapterForTarget(entry.TargetClient) != entry.Adapter {
		return fmt.Errorf("handoff history adapter does not match target")
	}
	if _, err := resolveRuntimeHandoffPath(workspaceRoot, entry.OutputDir); err != nil {
		return err
	}
	return nil
}

func validAdapter(value string) bool {
	return value == "manual-codex" || value == "manual-cc-haha"
}

func validPackageID(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, value := range value[len("sha256:"):] {
		if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
}

func resolveRuntimeHandoffPath(workspaceRoot, outputDir string) (string, error) {
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(outputDir) == "" || filepath.IsAbs(outputDir) {
		return "", fmt.Errorf("invalid handoff history output")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	handoffs := filepath.Join(root, "collaboration", ".runtime", "handoffs")
	resolved := filepath.Join(root, filepath.FromSlash(outputDir))
	if samePath(handoffs, resolved) || !pathWithin(handoffs, resolved) {
		return "", fmt.Errorf("handoff history output is outside the runtime handoff directory")
	}
	return filepath.Clean(resolved), nil
}
