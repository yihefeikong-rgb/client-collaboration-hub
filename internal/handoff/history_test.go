package handoff

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

func TestHandoffHistoryRecoversUnterminatedTail(t *testing.T) {
	root := t.TempDir()
	history := NewFileHandoffHistory(root, store.FlockLocker{})
	first := testHistoryEntry("one", 1, 'a')
	if err := history.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(root, "collaboration", ".runtime", "handoff-history.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"task_id":`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := history.List(context.Background(), "T-0001")
	if err != nil || len(entries) != 1 || entries[0] != first {
		t.Fatalf("recovered entries = %#v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "collaboration", ".runtime", "handoff-history.jsonl"))
	if err != nil || len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("repaired history = %q, %v", data, err)
	}
	second := testHistoryEntry("two", 2, 'b')
	if err := history.Append(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	entries, err = history.List(context.Background(), "T-0001")
	if err != nil || len(entries) != 2 || entries[1] != second {
		t.Fatalf("entries after retry = %#v, %v", entries, err)
	}
}

func testHistoryEntry(name string, throughEvent int64, digit byte) HandoffHistoryEntry {
	return HandoffHistoryEntry{
		TaskID:       "T-0001",
		TargetClient: "cc-haha",
		Adapter:      "manual-cc-haha",
		PackageID:    "sha256:" + strings.Repeat(string(digit), 64),
		ThroughEvent: throughEvent,
		OutputDir:    "collaboration/.runtime/handoffs/T-0001/cc-haha/" + name,
		CreatedAt:    time.Date(2026, 7, 28, 0, 0, int(throughEvent), 0, time.UTC),
	}
}
