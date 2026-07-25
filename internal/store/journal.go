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
	"strings"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"gopkg.in/yaml.v3"
)

var (
	ErrVersionConflict      = errors.New("version conflict")
	ErrRecoveryRequired     = errors.New("recovery required")
	ErrCorrupt              = errors.New("task journal is corrupt")
	ErrCommitOutcomeUnknown = errors.New("commit outcome unknown")
	ErrTaskNotFound         = errors.New("task not found")
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
	CreateTask(context.Context, protocol.Task) error
	CommitTransition(context.Context, string, int64, protocol.TransitionRequest, []string) (protocol.State, error)
	AppendMessage(context.Context, string, int64, string, string, time.Time) (protocol.State, error)
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
	MkdirTemp(string, string) (string, error)
	Stat(string) (os.FileInfo, error)
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
func (osFileSystem) MkdirAll(path string, perm os.FileMode) error  { return os.MkdirAll(path, perm) }
func (osFileSystem) MkdirTemp(dir, pattern string) (string, error) { return os.MkdirTemp(dir, pattern) }
func (osFileSystem) Stat(path string) (os.FileInfo, error)         { return os.Stat(path) }
func (osFileSystem) Remove(path string) error                      { return os.Remove(path) }

func fileName(file *os.File) string {
	if file == nil {
		return ""
	}
	return file.Name()
}

type FileTaskJournal struct {
	Root       string
	Locks      ScopedLocks
	FS         FileSystem
	Replacer   AtomicReplacer
	Now        func() time.Time
	References protocol.References
}

func NewFileTaskJournal(root string, locker Locker, references protocol.References) *FileTaskJournal {
	return &FileTaskJournal{
		Root:       root,
		Locks:      ScopedLocks{Root: root, Locker: locker},
		FS:         osFileSystem{},
		Replacer:   osReplacer{},
		Now:        time.Now,
		References: references,
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

func (j *FileTaskJournal) CreateTask(ctx context.Context, task protocol.Task) error {
	if j.References == nil {
		return fmt.Errorf("task journal references are required")
	}
	if err := task.Validate(task.ID, j.References); err != nil {
		return err
	}
	lock, err := j.Locks.Task(ctx, task.ID)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	if _, err := j.FS.Stat(j.taskDir(task.ID)); err == nil || !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("task %q already exists", task.ID)
		}
		return err
	}
	parent := filepath.Join(j.Root, "tasks")
	if err := j.FS.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tempDir, err := j.FS.MkdirTemp(parent, ".task-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	event := protocol.Event{EventID: 1, TaskID: task.ID, Type: protocol.EventTaskCreated, Actor: task.Creator, At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0}
	state := protocol.State{TaskID: task.ID, Status: protocol.Draft, Version: 1, LastEventID: 1, ResponsibleClient: task.Creator, UpdatedAt: task.CreatedAt}
	if err := j.writeTaskFiles(tempDir, task, state, event); err != nil {
		return err
	}
	return j.Replacer.Replace(tempDir, j.taskDir(task.ID))
}

func (j *FileTaskJournal) writeTaskFiles(dir string, task protocol.Task, state protocol.State, event protocol.Event) error {
	taskData, err := yaml.Marshal(task)
	if err != nil {
		return err
	}
	stateData, err := json.Marshal(state)
	if err != nil {
		return err
	}
	eventData, err := json.Marshal(event)
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		"task.yaml":      taskData,
		"state.json":     append(stateData, '\n'),
		"messages.jsonl": append(eventData, '\n'),
	} {
		if err := j.writeNewFile(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	return nil
}

func (j *FileTaskJournal) writeNewFile(path string, data []byte) error {
	file, err := j.FS.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := writeFull(file, data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (j *FileTaskJournal) CommitTransition(ctx context.Context, taskID string, expectedVersion int64, request protocol.TransitionRequest, evidenceRefs []string) (protocol.State, error) {
	lock, err := j.Locks.Task(ctx, taskID)
	if err != nil {
		return protocol.State{}, err
	}
	defer lock.Unlock()
	report, err := j.inspectUnlocked(taskID)
	if err != nil {
		return protocol.State{}, err
	}
	switch report.Health {
	case RecoverableTail:
		return protocol.State{}, ErrRecoveryRequired
	case Corrupt:
		return protocol.State{}, ErrCorrupt
	}
	if expectedVersion != report.State.Version {
		return protocol.State{}, ErrVersionConflict
	}
	task, err := j.readTask(taskID)
	if err != nil {
		return protocol.State{}, err
	}
	nextState, err := protocol.Transition(report.State, task, request)
	if err != nil {
		return protocol.State{}, err
	}
	event, err := eventForTransition(taskID, report.State, request, evidenceRefs)
	if err != nil {
		return protocol.State{}, err
	}
	if err := j.commitRecord(taskID, report.State, event, nextState); err != nil {
		return protocol.State{}, err
	}
	return nextState, nil
}

func (j *FileTaskJournal) AppendMessage(ctx context.Context, taskID string, expectedVersion int64, actor, body string, at time.Time) (protocol.State, error) {
	lock, err := j.Locks.Task(ctx, taskID)
	if err != nil {
		return protocol.State{}, err
	}
	defer lock.Unlock()
	report, err := j.inspectUnlocked(taskID)
	if err != nil {
		return protocol.State{}, err
	}
	if report.Health == RecoverableTail {
		return protocol.State{}, ErrRecoveryRequired
	}
	if report.Health == Corrupt {
		return protocol.State{}, ErrCorrupt
	}
	if expectedVersion != report.State.Version {
		return protocol.State{}, ErrVersionConflict
	}
	if !protocol.IsValidID(actor) || strings.TrimSpace(body) == "" || validateJournalTime(at) != nil || at.Before(report.State.UpdatedAt) {
		return protocol.State{}, fmt.Errorf("invalid message intent")
	}
	next := report.State
	next.Version++
	next.LastEventID++
	next.UpdatedAt = at
	event := protocol.Event{EventID: next.LastEventID, TaskID: taskID, Type: protocol.EventMessageAdded, Actor: actor, At: at, Body: body, ExpectedVersion: report.State.Version}
	if err := j.commitRecord(taskID, report.State, event, next); err != nil {
		return protocol.State{}, err
	}
	return next, nil
}

func eventForTransition(taskID string, state protocol.State, request protocol.TransitionRequest, evidenceRefs []string) (protocol.Event, error) {
	var eventType protocol.EventType
	switch request.Action {
	case protocol.Assign:
		eventType = protocol.EventAssigned
	case protocol.Accept:
		eventType = protocol.EventAccepted
	case protocol.Submit:
		eventType = protocol.EventSubmitted
	case protocol.RequestChanges:
		eventType = protocol.EventChangesRequested
	case protocol.Resume:
		eventType = protocol.EventRevisionStarted
	case protocol.Approve:
		eventType = protocol.EventApproved
	case protocol.Block:
		eventType = protocol.EventBlocked
	default:
		return protocol.Event{}, fmt.Errorf("unknown transition action %q", request.Action)
	}
	event := protocol.Event{EventID: state.LastEventID + 1, TaskID: taskID, Type: eventType, Actor: request.Actor, At: request.At, EvidenceRefs: evidenceRefs, ExpectedVersion: state.Version}
	if request.Action == protocol.Assign {
		event.TargetClient = request.NextAssignee
	}
	if request.Action == protocol.RequestChanges {
		event.Body = request.Feedback
	}
	return event, event.Validate(taskID)
}

func validateJournalTime(at time.Time) error {
	if at.IsZero() || at.Location() != time.UTC {
		return fmt.Errorf("time must be UTC and non-zero")
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
	if after.Health != Healthy || after.State != report.State {
		return RecoveryReport{Before: report, After: after, BackupPath: backup}, ErrCorrupt
	}
	return RecoveryReport{Before: report, After: after, BackupPath: backup}, nil
}

func (j *FileTaskJournal) inspectUnlocked(taskID string) (HealthReport, error) {
	taskData, err := j.FS.ReadFile(filepath.Join(j.taskDir(taskID), "task.yaml"))
	if err != nil {
		return HealthReport{Health: Corrupt, Reason: err.Error()}, nil
	}
	task, err := protocol.DecodeTask(taskData, taskID+".yaml", j.References)
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
		return corruptReport(report, "task_created event is missing"), nil
	}
	if data[len(data)-1] != '\n' {
		return corruptReport(report, "incomplete JSONL tail"), nil
	}
	var offset int64
	var tail protocol.Event
	var tailOffset int64
	var previousAt time.Time
	var lastStatus protocol.Event
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	for index, line := range lines {
		lineEnd := offset + int64(len(line)+1)
		event, err := protocol.DecodeEventLine(line, taskID)
		if err != nil || event.EventID != int64(index+1) || event.ExpectedVersion != int64(index) || (!previousAt.IsZero() && event.At.Before(previousAt)) {
			return corruptReport(report, "invalid or non-contiguous event"), nil
		}
		if index == 0 && event.Type != protocol.EventTaskCreated {
			return corruptReport(report, "first event must be task_created"), nil
		}
		if index > 0 && event.Type == protocol.EventTaskCreated {
			return corruptReport(report, "task_created event must be unique"), nil
		}
		previousAt = event.At
		if event.EventID <= state.LastEventID && event.Type != protocol.EventMessageAdded {
			lastStatus = event
		}
		report.EventCount++
		report.LastEventID = event.EventID
		if event.EventID == state.LastEventID+1 {
			tail, tailOffset = event, offset
		}
		offset = lineEnd
	}
	if report.LastEventID < state.LastEventID || state.Version != state.LastEventID {
		return corruptReport(report, "state last_event_id does not match event log"), nil
	}
	if report.LastEventID == state.LastEventID {
		if !state.UpdatedAt.Equal(previousAt) || !eventTypeMatchesState(lastStatus.Type, state.Status) || (lastStatus.Type == protocol.EventAssigned && state.AssignedClient != lastStatus.TargetClient) {
			return corruptReport(report, "state does not match committed event chain"), nil
		}
		return report, nil
	}
	if report.LastEventID == state.LastEventID+1 && tail.ExpectedVersion == state.Version && !tail.At.Before(state.UpdatedAt) {
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

func eventTypeMatchesState(eventType protocol.EventType, status protocol.Status) bool {
	switch eventType {
	case protocol.EventTaskCreated:
		return status == protocol.Draft
	case protocol.EventAssigned:
		return status == protocol.Assigned
	case protocol.EventAccepted, protocol.EventRevisionStarted:
		return status == protocol.Working
	case protocol.EventSubmitted:
		return status == protocol.Review
	case protocol.EventChangesRequested:
		return status == protocol.RevisionRequired
	case protocol.EventApproved:
		return status == protocol.Done
	case protocol.EventBlocked:
		return status == protocol.Blocked
	default:
		return false
	}
}

func (j *FileTaskJournal) commitRecord(taskID string, state protocol.State, event protocol.Event, next protocol.State) error {
	task, err := j.readTask(taskID)
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
	if err := next.Validate(task); err != nil {
		return err
	}
	encodedEvent, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := j.appendAndSync(j.messagesPath(taskID), append(encodedEvent, '\n')); err != nil {
		return err
	}
	if err := j.writeStateAtomically(j.statePath(taskID), next); err != nil {
		return err
	}
	final, err := j.inspectUnlocked(taskID)
	if err != nil || final.Health != Healthy || final.State != next || final.LastEventID != event.EventID {
		return fmt.Errorf("%w: inspect after replacement", ErrCommitOutcomeUnknown)
	}
	return nil
}

func (j *FileTaskJournal) readTask(taskID string) (protocol.Task, error) {
	var task protocol.Task
	data, err := j.FS.ReadFile(filepath.Join(j.taskDir(taskID), "task.yaml"))
	if err != nil {
		return task, err
	}
	return protocol.DecodeTask(data, taskID+".yaml", j.References)
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
	writeErr := writeFull(file, data)
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
	if err := writeFull(file, append(data, '\n')); err != nil {
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
	writeErr := writeFull(file, data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func writeFull(file SyncFile, data []byte) error {
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
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
