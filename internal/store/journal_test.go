package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

var journalTime = time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

func TestCreateTaskCreatesAuditableInitialState(t *testing.T) {
	journal, root := newJournal(t)
	if err := journal.CreateTask(context.Background(), testTask("T-0001")); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Healthy || report.State.Version != 1 || report.State.LastEventID != 1 || report.State.Status != protocol.Draft {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "T-0001", "task.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := journal.CreateTask(context.Background(), testTask("T-0001")); err == nil {
		t.Fatal("duplicate task accepted")
	}
}

func TestCommitTransitionDerivesEventAndState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	next, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionRequest{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil)
	if err != nil || next.Status != protocol.Assigned || next.Version != 2 || next.AssignedClient != "cc-haha" {
		t.Fatalf("CommitTransition() = %+v, %v", next, err)
	}
	event := readEvents(t, journal.Root, "T-0001")[1]
	if event.Type != protocol.EventAssigned || event.TargetClient != "cc-haha" || event.EventID != 2 || event.ExpectedVersion != 1 {
		t.Fatalf("event = %+v", event)
	}
	if _, err := journal.CommitTransition(context.Background(), "T-0001", 2, protocol.TransitionRequest{Action: protocol.Approve, Actor: "codex", At: journalTime}, nil); err == nil {
		t.Fatal("illegal transition accepted")
	}
}

func TestAppendMessagePreservesBusinessState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	next, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime)
	if err != nil || next.Status != protocol.Draft || next.ResponsibleClient != "codex" || next.Version != 2 || next.LastEventID != 2 {
		t.Fatalf("AppendMessage() = %+v, %v", next, err)
	}
	if event := readEvents(t, journal.Root, "T-0001")[1]; event.Type != protocol.EventMessageAdded || event.Body != "hello" {
		t.Fatalf("event = %+v", event)
	}
}

func TestCommitSerializesSameTask(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		}
		if errors.Is(err, ErrVersionConflict) {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestInspectRejectsInvalidAuditChain(t *testing.T) {
	tests := []struct {
		name  string
		state protocol.State
		event protocol.Event
	}{
		{"time moves backward", protocol.State{TaskID: "T-0001", Status: protocol.Draft, Version: 2, LastEventID: 2, ResponsibleClient: "codex", UpdatedAt: journalTime}, protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventMessageAdded, Actor: "codex", At: journalTime.Add(-time.Second), Body: "bad", ExpectedVersion: 1}},
		{"duplicate task_created", protocol.State{TaskID: "T-0001", Status: protocol.Draft, Version: 2, LastEventID: 2, ResponsibleClient: "codex", UpdatedAt: journalTime}, protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventTaskCreated, Actor: "codex", At: journalTime, Body: "Test", ExpectedVersion: 1}},
		{"assigned target differs from state", protocol.State{TaskID: "T-0001", Status: protocol.Assigned, Version: 2, LastEventID: 2, AssignedClient: "other-client", ResponsibleClient: "other-client", UpdatedAt: journalTime}, protocol.Event{EventID: 2, TaskID: "T-0001", Type: protocol.EventAssigned, Actor: "codex", At: journalTime, TargetClient: "cc-haha", ExpectedVersion: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal, root := newJournal(t)
			createTask(t, journal, "T-0001")
			writeState(t, root, test.state)
			events := append(readEvents(t, root, "T-0001"), test.event)
			writeEvents(t, root, "T-0001", events)
			report, err := journal.Inspect(context.Background(), "T-0001")
			if err != nil || report.Health != Corrupt {
				t.Fatalf("Inspect() = %+v, %v", report, err)
			}
		})
	}
}

func TestRecoverTailRestoresPriorState(t *testing.T) {
	journal, _ := newJournal(t)
	createTask(t, journal, "T-0001")
	journal.Replacer = failingReplacer{}
	_, err := journal.CommitTransition(context.Background(), "T-0001", 1, protocol.TransitionRequest{Action: protocol.Assign, Actor: "codex", NextAssignee: "cc-haha", At: journalTime}, nil)
	if err == nil {
		t.Fatal("replace failure accepted")
	}
	journal.Replacer = osReplacer{}
	before, _ := journal.Inspect(context.Background(), "T-0001")
	if before.Health != RecoverableTail {
		t.Fatalf("before = %+v", before)
	}
	recovered, err := journal.RecoverTail(context.Background(), "T-0001")
	if err != nil || recovered.After.Health != Healthy || recovered.After.State != before.State || recovered.BackupPath == "" {
		t.Fatalf("RecoverTail() = %+v, %v", recovered, err)
	}
}

func TestFaultInjectionShortWritesAndOutcomeUnknown(t *testing.T) {
	journal, _ := newJournal(t)
	fs := &faultFS{}
	journal.FS = fs
	createTask(t, journal, "T-0001")
	fs.partialTemp = true
	_, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime)
	if err == nil {
		t.Fatal("state short write accepted")
	}
	fs.disableFaults()
	report, _ := journal.Inspect(context.Background(), "T-0001")
	if report.Health != RecoverableTail {
		t.Fatalf("report = %+v", report)
	}
	fs.partialBackup = true
	if _, err := journal.RecoverTail(context.Background(), "T-0001"); err == nil {
		t.Fatal("backup short write accepted")
	}
	fs.disableFaults()
	report, _ = journal.Inspect(context.Background(), "T-0001")
	if report.Health != RecoverableTail {
		t.Fatalf("tail was truncated after failed backup: %+v", report)
	}
}

func TestCommitReturnsOutcomeUnknownAfterReplace(t *testing.T) {
	journal, _ := newJournal(t)
	fs := &faultFS{}
	journal.FS = fs
	createTask(t, journal, "T-0001")
	journal.Replacer = replaceThenBreakReader{fs: fs}
	if _, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "hello", journalTime); !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	fs.failRead = false
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Healthy || report.State.Version != 2 {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

type failingReplacer struct{}

func (failingReplacer) Replace(string, string) error { return errors.New("replace failed") }

type faultFS struct {
	osFileSystem
	partialTemp, partialBackup, failRead bool
}

func (fs *faultFS) ReadFile(path string) ([]byte, error) {
	if fs.failRead {
		return nil, errors.New("read failed")
	}
	return fs.osFileSystem.ReadFile(path)
}

func (fs *faultFS) CreateTemp(dir, pattern string) (SyncFile, string, error) {
	file, path, err := fs.osFileSystem.CreateTemp(dir, pattern)
	return &faultFile{SyncFile: file, partial: fs.partialTemp}, path, err
}
func (fs *faultFS) OpenFile(path string, flag int, perm os.FileMode) (SyncFile, error) {
	file, err := fs.osFileSystem.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return &faultFile{SyncFile: file, partial: fs.partialBackup && filepath.Base(filepath.Dir(path)) != "T-0001"}, nil
}
func (fs *faultFS) disableFaults() { *fs = faultFS{} }

type faultFile struct {
	SyncFile
	partial bool
}

type replaceThenBreakReader struct{ fs *faultFS }

func (r replaceThenBreakReader) Replace(tempPath, targetPath string) error {
	if err := os.Rename(tempPath, targetPath); err != nil {
		return err
	}
	r.fs.failRead = true
	return nil
}

func (f *faultFile) Write(data []byte) (int, error) {
	if f.partial {
		return f.SyncFile.Write(data[:len(data)/2])
	}
	return f.SyncFile.Write(data)
}

func newJournal(t *testing.T) (*FileTaskJournal, string) {
	t.Helper()
	root := t.TempDir()
	registry := NewFileRegistryStore(root, FlockLocker{})
	if err := registry.CreateProject(context.Background(), protocol.Project{ID: "project-1", Name: "Test", CreatedAt: journalTime}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "codex", Name: "Codex", Capabilities: []string{"create_task", "review"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterClient(context.Background(), protocol.Client{ID: "cc-haha", Name: "CC-HAHA", Capabilities: []string{"execute"}}); err != nil {
		t.Fatal(err)
	}
	return NewFileTaskJournal(root, FlockLocker{}, registry), root
}
func createTask(t *testing.T, journal *FileTaskJournal, taskID string) {
	t.Helper()
	if err := journal.CreateTask(context.Background(), testTask(taskID)); err != nil {
		t.Fatal(err)
	}
}
func testTask(id string) protocol.Task {
	return protocol.Task{ID: id, ProjectID: "project-1", Title: "Test", Objective: "Test", Acceptance: []string{"Pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: journalTime}
}
func writeState(t *testing.T, root string, state protocol.State) {
	t.Helper()
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(root, "tasks", state.TaskID, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func readEvents(t *testing.T, root, id string) []protocol.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "tasks", id, "messages.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []protocol.Event
	for _, line := range bytesLines(data) {
		event, err := protocol.DecodeEventLine(line, id)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}
func writeEvents(t *testing.T, root, id string, events []protocol.Event) {
	t.Helper()
	var data []byte
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		data = append(data, encoded...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", id, "messages.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func bytesLines(data []byte) [][]byte {
	data = data[:len(data)-1]
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	return append(lines, data[start:])
}
