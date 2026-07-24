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

var (
	ErrVersionConflict  = errors.New("version conflict")
	ErrRecoveryRequired = errors.New("recovery required")
	ErrCorrupt          = errors.New("task journal is corrupt")
)

type Health string

const (
	Healthy         Health = "HEALTHY"
	RecoverableTail Health = "RECOVERABLE_TAIL"
	Corrupt         Health = "CORRUPT"
)

type HealthReport struct {
	Health      Health
	Reason      string
	State       protocol.State
	TailOffset  int64
	LastEventID int64
	EventCount  int
}

type RecoveryReport struct {
	Before     HealthReport
	After      HealthReport
	BackupPath string
}

type TaskJournal interface {
	Inspect(context.Context, string) (HealthReport, error)
	Commit(context.Context, string, int64, protocol.Event, protocol.State) error
	RecoverTail(context.Context, string) (RecoveryReport, error)
}

type AtomicReplacer interface {
	Replace(tempPath, targetPath string) error
}

type osReplacer struct{}

func (osReplacer) Replace(tempPath, targetPath string) error {
	return os.Rename(tempPath, targetPath)
}

type SyncFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
	Truncate(int64) error
}

type FileSystem interface {
	ReadFile(string) ([]byte, error)
	OpenFile(string, int, os.FileMode) (SyncFile, error)
	CreateTemp(string, string) (SyncFile, string, error)
	MkdirAll(string, os.FileMode) error
	Remove(string) error
}

type osFileSystem struct{}

func (osFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (osFileSystem) OpenFile(path string, flag int, perm os.FileMode) (SyncFile, error) {
	return os.OpenFile(path, flag, perm)
}
func (osFileSystem) CreateTemp(dir, pattern string) (SyncFile, string, error) {
	file, err := os.CreateTemp(dir, pattern)
	return file, fileName(file), err
}
func (osFileSystem) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (osFileSystem) Remove(path string) error                     { return os.Remove(path) }

func fileName(file *os.File) string {
	if file == nil {
		return ""
	}
	return file.Name()
}

type FileTaskJournal struct {
	Root     string
	Locks    ScopedLocks
	FS       FileSystem
	Replacer AtomicReplacer
	Now      func() time.Time
}

func NewFileTaskJournal(root string, locker Locker) *FileTaskJournal {
	return &FileTaskJournal{
		Root:     root,
		Locks:    ScopedLocks{Root: root, Locker: locker},
		FS:       osFileSystem{},
		Replacer: osReplacer{},
		Now:      time.Now,
	}
}

func (j *FileTaskJournal) Inspect(ctx context.Context, taskID string) (HealthReport, error) {
	lock, err := j.Locks.Task(ctx, taskID)
	if err != nil {
		return HealthReport{}, err
	}
	defer lock.Unlock()
	return j.inspectUnlocked(taskID)
}

func (j *FileTaskJournal) Commit(ctx context.Context, taskID string, expectedVersion int64, event protocol.Event, nextState protocol.State) error {
	lock, err := j.Locks.Task(ctx, taskID)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	report, err := j.inspectUnlocked(taskID)
	if err != nil {
		return err
	}
	switch report.Health {
	case RecoverableTail:
		return ErrRecoveryRequired
	case Corrupt:
		return ErrCorrupt
	}
	if expectedVersion != report.State.Version {
		return ErrVersionConflict
	}
	if err := j.validateCommit(taskID, report.State, event, nextState); err != nil {
		return err
	}
	encodedEvent, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := j.appendAndSync(j.messagesPath(taskID), append(encodedEvent, '\n')); err != nil {
		return err
	}
	if err := j.writeStateAtomically(j.statePath(taskID), nextState); err != nil {
		return err
	}
	final, err := j.inspectUnlocked(taskID)
	if err != nil {
		return err
	}
	if final.Health != Healthy {
		return fmt.Errorf("commit postcondition: %w", ErrCorrupt)
	}
	return nil
}

func (j *FileTaskJournal) RecoverTail(ctx context.Context, taskID string) (RecoveryReport, error) {
	lock, err := j.Locks.Task(ctx, taskID)
	if err != nil {
		return RecoveryReport{}, err
	}
	defer lock.Unlock()
	report, err := j.inspectUnlocked(taskID)
	if err != nil {
		return RecoveryReport{}, err
	}
	if report.Health == Corrupt {
		backup, backupErr := j.backup(taskID, report)
		return RecoveryReport{Before: report, BackupPath: backup}, errors.Join(ErrCorrupt, backupErr)
	}
	if report.Health != RecoverableTail {
		return RecoveryReport{Before: report, After: report}, nil
	}
	backup, err := j.backup(taskID, report)
	if err != nil {
		return RecoveryReport{Before: report}, err
	}
	file, err := j.FS.OpenFile(j.messagesPath(taskID), os.O_WRONLY, 0)
	if err != nil {
		return RecoveryReport{Before: report, BackupPath: backup}, err
	}
	if err := file.Truncate(report.TailOffset); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return RecoveryReport{Before: report, BackupPath: backup}, err
	}
	after, err := j.inspectUnlocked(taskID)
	if err != nil {
		return RecoveryReport{Before: report, BackupPath: backup}, err
	}
	if after.Health != Healthy {
		return RecoveryReport{Before: report, After: after, BackupPath: backup}, ErrCorrupt
	}
	return RecoveryReport{Before: report, After: after, BackupPath: backup}, nil
}

func (j *FileTaskJournal) inspectUnlocked(taskID string) (HealthReport, error) {
	taskData, err := j.FS.ReadFile(filepath.Join(j.taskDir(taskID), "task.yaml"))
	if err != nil {
		return HealthReport{Health: Corrupt, Reason: err.Error()}, nil
	}
	task, err := protocol.DecodeTask(taskData, taskID+".yaml", nil)
	if err != nil {
		return HealthReport{Health: Corrupt, Reason: err.Error()}, nil
	}
	state, err := j.readState(taskID)
	if err != nil {
		return HealthReport{Health: Corrupt, Reason: err.Error()}, nil
	}
	if err := state.Validate(task); err != nil {
		return HealthReport{Health: Corrupt, State: state, Reason: err.Error()}, nil
	}
	data, err := j.FS.ReadFile(j.messagesPath(taskID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return HealthReport{}, err
	}
	return inspectJournal(data, taskID, state)
}

func inspectJournal(data []byte, taskID string, state protocol.State) (HealthReport, error) {
	report := HealthReport{Health: Healthy, State: state}
	if len(data) == 0 {
		if state.LastEventID == 0 {
			return report, nil
		}
		return corruptReport(report, "state is ahead of event log"), nil
	}
	if data[len(data)-1] != '\n' {
		return corruptReport(report, "incomplete JSONL tail"), nil
	}
	var offset int64
	var tail protocol.Event
	var tailOffset int64
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	for index, line := range lines {
		lineEnd := offset + int64(len(line)+1)
		event, err := protocol.DecodeEventLine(line, taskID)
		if err != nil || event.EventID != int64(index+1) {
			return corruptReport(report, "invalid or non-contiguous event"), nil
		}
		report.EventCount++
		report.LastEventID = event.EventID
		if event.EventID == state.LastEventID+1 {
			tail, tailOffset = event, offset
		}
		offset = lineEnd
	}
	if report.LastEventID < state.LastEventID {
		return corruptReport(report, "state last_event_id does not match event log"), nil
	}
	if report.LastEventID == state.LastEventID {
		return report, nil
	}
	if report.LastEventID == state.LastEventID+1 && tail.ExpectedVersion == state.Version {
		report.Health = RecoverableTail
		report.TailOffset = tailOffset
		return report, nil
	}
	return corruptReport(report, "uncommitted event tail is not uniquely recoverable"), nil
}

func corruptReport(report HealthReport, reason string) HealthReport {
	report.Health = Corrupt
	report.Reason = reason
	return report
}

func (j *FileTaskJournal) validateCommit(taskID string, state protocol.State, event protocol.Event, next protocol.State) error {
	taskData, err := j.FS.ReadFile(filepath.Join(j.taskDir(taskID), "task.yaml"))
	if err != nil {
		return err
	}
	task, err := protocol.DecodeTask(taskData, taskID+".yaml", nil)
	if err != nil {
		return err
	}
	if err := event.Validate(taskID); err != nil {
		return err
	}
	if event.EventID != state.LastEventID+1 || event.ExpectedVersion != state.Version {
		return fmt.Errorf("event does not follow current state")
	}
	if next.TaskID != taskID || next.Version != state.Version+1 || next.LastEventID != event.EventID || !next.UpdatedAt.Equal(event.At) {
		return fmt.Errorf("next state does not match event")
	}
	return next.Validate(task)
}

func (j *FileTaskJournal) readState(taskID string) (protocol.State, error) {
	var state protocol.State
	data, err := j.FS.ReadFile(j.statePath(taskID))
	if err != nil {
		return state, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return state, fmt.Errorf("state contains multiple JSON values")
	}
	if state.TaskID != taskID {
		return state, fmt.Errorf("state task_id mismatch")
	}
	return state, nil
}

func (j *FileTaskJournal) appendAndSync(path string, data []byte) error {
	if err := j.FS.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := j.FS.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
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

func (j *FileTaskJournal) writeStateAtomically(path string, state protocol.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := j.FS.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, tempPath, err := j.FS.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	defer j.FS.Remove(tempPath)
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return j.Replacer.Replace(tempPath, path)
}

func (j *FileTaskJournal) backup(taskID string, report HealthReport) (string, error) {
	now := j.Now().UTC()
	dir := filepath.Join(j.Root, ".runtime", "recovery", taskID, fmt.Sprintf("%s-%d", now.Format("20060102T150405.000000000Z"), now.UnixNano()))
	if err := j.FS.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	state, err := j.FS.ReadFile(j.statePath(taskID))
	if err != nil {
		return "", err
	}
	messages, err := j.FS.ReadFile(j.messagesPath(taskID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	reportData, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	for name, data := range map[string][]byte{"state.json": state, "messages.jsonl": messages, "report.json": append(reportData, '\n')} {
		if err := j.writeBackup(filepath.Join(dir, name), data); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (j *FileTaskJournal) writeBackup(path string, data []byte) error {
	file, err := j.FS.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (j *FileTaskJournal) taskDir(taskID string) string {
	return filepath.Join(j.Root, "tasks", taskID)
}
func (j *FileTaskJournal) statePath(taskID string) string {
	return filepath.Join(j.taskDir(taskID), "state.json")
}
func (j *FileTaskJournal) messagesPath(taskID string) string {
	return filepath.Join(j.taskDir(taskID), "messages.jsonl")
}
