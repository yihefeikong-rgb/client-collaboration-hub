package protocol

import (
	"fmt"
	"testing"
	"time"
)

type replayEvidenceResolver map[string]Evidence

func (r replayEvidenceResolver) ResolveEvidence(taskID, id string) (Evidence, error) {
	evidence, ok := r[id]
	if !ok || evidence.TaskID != taskID {
		return Evidence{}, fmt.Errorf("evidence %q not found", id)
	}
	return evidence, nil
}

func TestReplayRejectsIllegalIntermediateTransition(t *testing.T) {
	task := replayTask()
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventApproved, Actor: "codex", At: task.CreatedAt, ExpectedVersion: 1},
	}
	if _, err := Replay(task, events, testReferences{}, replayEvidenceResolver{}); err == nil {
		t.Fatal("task_created -> approved accepted")
	}
}

func TestReplayEvidenceAddedPreservesBusinessState(t *testing.T) {
	task := replayTask()
	evidence := Evidence{ID: "E-0001", TaskID: task.ID, Kind: EvidenceDiff, Summary: "Diff", CreatedBy: "codex", CreatedAt: task.CreatedAt}
	events := []Event{
		{EventID: 1, TaskID: task.ID, Type: EventTaskCreated, Actor: "codex", At: task.CreatedAt, Body: task.Title, ExpectedVersion: 0},
		{EventID: 2, TaskID: task.ID, Type: EventEvidenceAdded, Actor: "codex", At: task.CreatedAt, Body: evidence.Summary, EvidenceRefs: []string{evidence.ID}, ExpectedVersion: 1},
	}
	state, err := Replay(task, events, testReferences{}, replayEvidenceResolver{evidence.ID: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != Draft || state.Version != 2 || state.LastEventID != 2 || state.ResponsibleClient != task.Creator {
		t.Fatalf("state = %+v", state)
	}
}

func replayTask() Task {
	return Task{ID: "T-0001", ProjectID: "project-1", Title: "Test", Objective: "Test", Acceptance: []string{"Pass"}, Creator: "codex", Reviewer: "codex", CreatedAt: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)}
}
