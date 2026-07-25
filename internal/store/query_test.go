package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

func TestTaskQueryReturnsConsistentCursorSnapshot(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	if _, err := journal.AppendMessage(context.Background(), "T-0001", 1, "codex", "Created", journalTime); err != nil {
		t.Fatal(err)
	}
	evidence := protocol.Evidence{ID: "E-0001", TaskID: "T-0001", Kind: protocol.EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: journalTime}
	if _, err := journal.AddEvidence(context.Background(), "T-0001", 2, evidence); err != nil {
		t.Fatal(err)
	}
	query := NewFileTaskQuery(journal, NewFileRegistryStore(root, FlockLocker{}))
	snapshot, err := query.Snapshot(context.Background(), "T-0001", 1)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Health != Healthy || snapshot.FromEvent != 1 || snapshot.ThroughEvent != 3 || len(snapshot.Events) != 2 || snapshot.Events[0].EventID != 2 || len(snapshot.Evidence) != 1 || snapshot.Evidence[0].ID != evidence.ID {
		t.Fatalf("Snapshot() = %+v", snapshot)
	}
	if snapshot.ActionActor != "codex" || !reflect.DeepEqual(snapshot.AllowedActions, []protocol.Action{protocol.Assign, protocol.Message, protocol.AddEvidence}) {
		t.Fatalf("allowed actions = %v", snapshot.AllowedActions)
	}
	for _, after := range []int64{-1, 4} {
		if _, err := query.Snapshot(context.Background(), "T-0001", after); err == nil {
			t.Fatalf("after_event %d accepted", after)
		}
	}
}

func TestTaskQueryReportsNonHealthyTaskWithoutExportableContents(t *testing.T) {
	journal, root := newJournal(t)
	createTask(t, journal, "T-0001")
	writeState(t, root, protocol.State{TaskID: "T-0001", Status: protocol.Draft, Version: 2, LastEventID: 2, ResponsibleClient: "codex", UpdatedAt: journalTime})
	query := NewFileTaskQuery(journal, NewFileRegistryStore(root, FlockLocker{}))
	snapshot, err := query.Snapshot(context.Background(), "T-0001", 0)
	if err != nil || snapshot.Health != Corrupt || len(snapshot.Events) != 0 || len(snapshot.Evidence) != 0 {
		t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
	}
}
