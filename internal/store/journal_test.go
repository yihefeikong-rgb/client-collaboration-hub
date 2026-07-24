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

func TestCommitWritesConsistentEventAndState(t *testing.T) {
	journal, root := newJournal(t)
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	event, next := assignedChange("T-0001")
	if err := journal.Commit(context.Background(), "T-0001", 0, event, next); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Healthy || report.State != next {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

func TestCommitRejectsConflictAndInvariantsWithoutWriting(t *testing.T) {
	journal, root := newJournal(t)
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	event, next := assignedChange("T-0001")
	if err := journal.Commit(context.Background(), "T-0001", 1, event, next); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Commit() error = %v", err)
	}
	event.EventID = 2
	if err := journal.Commit(context.Background(), "T-0001", 0, event, next); err == nil {
		t.Fatal("invalid event accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "T-0001", "messages.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("event log changed: %v", err)
	}
}

func TestCommitSerializesSameTask(t *testing.T) {
	journal, root := newJournal(t)
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	event, next := assignedChange("T-0001")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- journal.Commit(context.Background(), "T-0001", 0, event, next) }()
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

func TestInspectAndRecoverTail(t *testing.T) {
	journal, root := newJournal(t)
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	event, _ := assignedChange("T-0001")
	writeEventLog(t, root, "T-0001", event)
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != RecoverableTail {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
	if err := journal.Commit(context.Background(), "T-0001", 0, event, initialState("T-0001")); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Commit() error = %v", err)
	}
	recovered, err := journal.RecoverTail(context.Background(), "T-0001")
	if err != nil || recovered.After.Health != Healthy || recovered.BackupPath == "" {
		t.Fatalf("RecoverTail() = %+v, %v", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(recovered.BackupPath, "messages.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestInspectDetectsCorruptLog(t *testing.T) {
	journal, root := newJournal(t)
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	path := filepath.Join(root, "tasks", "T-0001", "messages.jsonl")
	if err := os.WriteFile(path, []byte(`{"bad":`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != Corrupt {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
	if _, err := journal.RecoverTail(context.Background(), "T-0001"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("RecoverTail() error = %v", err)
	}
}

func TestInspectCorruptionMatrix(t *testing.T) {
	tests := []struct {
		name  string
		state protocol.State
		data  []byte
	}{
		{"multiple tails", initialState("T-0001"), eventLines(t, "T-0001", 1, 2)},
		{"duplicate id", initialState("T-0001"), eventLines(t, "T-0001", 1, 1)},
		{"state ahead", protocol.State{TaskID: "T-0001", Status: protocol.Draft, LastEventID: 1, ResponsibleClient: "codex", UpdatedAt: journalTime}, nil},
		{"middle bad json", initialState("T-0001"), []byte("{bad}\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journal, root := newJournal(t)
			writeTask(t, root, "T-0001")
			writeState(t, root, tt.state)
			if tt.data != nil {
				if err := os.WriteFile(filepath.Join(root, "tasks", "T-0001", "messages.jsonl"), tt.data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report, err := journal.Inspect(context.Background(), "T-0001")
			if err != nil || report.Health != Corrupt {
				t.Fatalf("Inspect() = %+v, %v", report, err)
			}
		})
	}
}

func TestDifferentTasksCommitIndependently(t *testing.T) {
	journal, root := newJournal(t)
	for _, taskID := range []string{"T-0001", "T-0002"} {
		writeTask(t, root, taskID)
		writeState(t, root, initialState(taskID))
	}
	for _, taskID := range []string{"T-0001", "T-0002"} {
		event, next := assignedChange(taskID)
		if err := journal.Commit(context.Background(), taskID, 0, event, next); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommitReplaceFailureLeavesRecoverableTail(t *testing.T) {
	journal, root := newJournal(t)
	journal.Replacer = failingReplacer{}
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	event, next := assignedChange("T-0001")
	if err := journal.Commit(context.Background(), "T-0001", 0, event, next); err == nil {
		t.Fatal("replace failure accepted")
	}
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != RecoverableTail {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

func TestCommitFaultInjection(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*faultFS)
		want      Health
	}{
		{"open event", func(fs *faultFS) { fs.failOpen = true }, Healthy},
		{"partial event", func(fs *faultFS) { fs.partialAppend = true }, Corrupt},
		{"event sync", func(fs *faultFS) { fs.failAppendSync = true }, RecoverableTail},
		{"state temp create", func(fs *faultFS) { fs.failTemp = true }, RecoverableTail},
		{"state temp write", func(fs *faultFS) { fs.failTempWrite = true }, RecoverableTail},
		{"state temp sync", func(fs *faultFS) { fs.failTempSync = true }, RecoverableTail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journal, root := newJournal(t)
			fs := &faultFS{}
			journal.FS = fs
			writeTask(t, root, "T-0001")
			writeState(t, root, initialState("T-0001"))
			tt.configure(fs)
			event, next := assignedChange("T-0001")
			if err := journal.Commit(context.Background(), "T-0001", 0, event, next); err == nil {
				t.Fatal("fault did not fail commit")
			}
			fs.disableFaults()
			report, err := journal.Inspect(context.Background(), "T-0001")
			if err != nil || report.Health != tt.want {
				t.Fatalf("Inspect() = %+v, %v", report, err)
			}
		})
	}
}

func TestRecoverTailFaultInjectionPreservesTail(t *testing.T) {
	journal, root := newJournal(t)
	fs := &faultFS{}
	journal.FS = fs
	writeTask(t, root, "T-0001")
	writeState(t, root, initialState("T-0001"))
	event, _ := assignedChange("T-0001")
	writeEventLog(t, root, "T-0001", event)
	fs.failBackupOpen = true
	if _, err := journal.RecoverTail(context.Background(), "T-0001"); err == nil {
		t.Fatal("backup failure accepted")
	}
	fs.disableFaults()
	report, err := journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != RecoverableTail {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
	fs.failTruncate = true
	if _, err := journal.RecoverTail(context.Background(), "T-0001"); err == nil {
		t.Fatal("truncate failure accepted")
	}
	fs.disableFaults()
	report, err = journal.Inspect(context.Background(), "T-0001")
	if err != nil || report.Health != RecoverableTail {
		t.Fatalf("Inspect() = %+v, %v", report, err)
	}
}

type failingReplacer struct{}

func (failingReplacer) Replace(string, string) error { return errors.New("replace failed") }

type faultFS struct {
	osFileSystem
	failOpen, failTemp, partialAppend, failAppendSync, failTempWrite, failTempSync, failBackupOpen, failTruncate bool
}

func (fs *faultFS) OpenFile(path string, flag int, perm os.FileMode) (SyncFile, error) {
	if fs.failOpen || (fs.failBackupOpen && filepath.Base(filepath.Dir(path)) != "tasks") {
		return nil, errors.New("open failed")
	}
	file, err := fs.osFileSystem.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return &faultFile{SyncFile: file, partialWrite: fs.partialAppend, failSync: fs.failAppendSync, failTruncate: fs.failTruncate}, nil
}

func (fs *faultFS) CreateTemp(dir, pattern string) (SyncFile, string, error) {
	if fs.failTemp {
		return nil, "", errors.New("temp create failed")
	}
	file, path, err := fs.osFileSystem.CreateTemp(dir, pattern)
	if err != nil {
		return nil, "", err
	}
	return &faultFile{SyncFile: file, failWrite: fs.failTempWrite, failSync: fs.failTempSync}, path, nil
}

func (fs *faultFS) disableFaults() { *fs = faultFS{} }

type faultFile struct {
	SyncFile
	failWrite, partialWrite, failSync, failTruncate bool
}

func (f *faultFile) Write(data []byte) (int, error) {
	if f.failWrite {
		return 0, errors.New("write failed")
	}
	if f.partialWrite {
		return f.SyncFile.Write(data[:len(data)/2])
	}
	return f.SyncFile.Write(data)
}
func (f *faultFile) Sync() error {
	if f.failSync {
		return errors.New("sync failed")
	}
	return f.SyncFile.Sync()
}
func (f *faultFile) Truncate(size int64) error {
	if f.failTruncate {
		return errors.New("truncate failed")
	}
	return f.SyncFile.Truncate(size)
}

func newJournal(t *testing.T) (*FileTaskJournal, string) {
	t.Helper()
	root := t.TempDir()
	return NewFileTaskJournal(root, FlockLocker{}), root
}

func writeTask(t *testing.T, root, taskID string) {
	t.Helper()
	dir := filepath.Join(root, "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "id: " + taskID + "\nproject_id: project-1\ntitle: Test\nobjective: Test\nacceptance: [Pass]\ncreator: codex\nreviewer: codex\ncreated_at: 2026-07-25T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, "task.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeState(t *testing.T, root string, state protocol.State) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", state.TaskID, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeEventLog(t *testing.T, root, taskID string, event protocol.Event) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", taskID, "messages.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func initialState(taskID string) protocol.State {
	return protocol.State{TaskID: taskID, Status: protocol.Draft, ResponsibleClient: "codex", UpdatedAt: journalTime}
}

func assignedChange(taskID string) (protocol.Event, protocol.State) {
	event := protocol.Event{EventID: 1, TaskID: taskID, Type: protocol.EventAssigned, Actor: "codex", At: journalTime, ExpectedVersion: 0}
	next := protocol.State{TaskID: taskID, Status: protocol.Assigned, Version: 1, LastEventID: 1, AssignedClient: "cc-haha", ResponsibleClient: "cc-haha", UpdatedAt: journalTime}
	return event, next
}

func eventLines(t *testing.T, taskID string, ids ...int64) []byte {
	t.Helper()
	var data []byte
	for _, id := range ids {
		event := protocol.Event{EventID: id, TaskID: taskID, Type: protocol.EventAssigned, Actor: "codex", At: journalTime, ExpectedVersion: id - 1}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, encoded...)
		data = append(data, '\n')
	}
	return data
}
